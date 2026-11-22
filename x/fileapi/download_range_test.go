package fileapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	storefile "github.com/birdordev/plumego/store/file"
	datafile "github.com/birdordev/plumego/x/data/file"
	tenantcore "github.com/birdordev/plumego/x/tenant/core"
)

func TestS3DownloadRangesAndConditions(t *testing.T) {
	const object = "0123456789"
	requests := make(chan string, 20)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requested := req.Header.Get("Range")
		requests <- strings.TrimSpace(req.Method + " " + requested)
		if requested == "bytes=4-5" {
			// 模拟后端返回的区间与请求不一致。
			w.Header().Set("Content-Range", "bytes 5-6/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("56"))
			return
		}
		http.ServeContent(w, req, "sample.txt", time.Time{}, strings.NewReader(object))
	}))
	t.Cleanup(origin.Close)
	storage, err := datafile.NewS3Storage(datafile.S3Config{
		Endpoint: strings.TrimPrefix(origin.URL, "http://"), Bucket: "fixture", PathStyle: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	metadata := &mockMetadataManager{getFunc: func(_ context.Context, _, id string) (*datafile.File, error) {
		return &datafile.File{
			File: storefile.File{ID: id, Path: "tenant/sample.txt", Name: "sample.txt",
				Size: int64(len(object)), MimeType: "text/plain", Hash: "sample"},
			TenantID: "tenant",
		}, nil
	}}
	handler := NewHandler(storage, metadata)
	send := func(tenant, method, rangeRequest, ifRange, ifNoneMatch string, upstream ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := withRouteParam(httptest.NewRequest(method, "/files/sample", nil), "id", "sample")
		req = req.WithContext(tenantcore.WithTenantID(req.Context(), tenant))
		req.Header.Set("Range", rangeRequest)
		req.Header.Set("If-Range", ifRange)
		req.Header.Set("If-None-Match", ifNoneMatch)
		w := httptest.NewRecorder()
		handler.Download(w, req)
		var observed []string
		for len(requests) > 0 {
			observed = append(observed, <-requests)
		}
		if !reflect.DeepEqual(observed, upstream) || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s %s: upstream=%v, expected=%v, cache=%q", method, rangeRequest, observed, upstream, w.Header().Get("Cache-Control"))
		}
		return w
	}

	head := send("tenant", http.MethodHead, "bytes=1-2", "", "", "HEAD")
	tag := head.Header().Get("ETag")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "10" ||
		head.Header().Get("Accept-Ranges") != "bytes" || tag == "" {
		t.Fatalf("HEAD: %d %v %q", head.Code, head.Header(), head.Body)
	}
	for _, tc := range []struct {
		rangeRequest, ifRange, noneMatch, contentRange, body string
		status                                               int
		upstream                                             []string
	}{
		{"", "", "", "", object, 200, []string{"GET"}},
		{"bytes=1-3", tag, "", "bytes 1-3/10", "123", 206, []string{"HEAD", "GET bytes=1-3"}},
		{"bytes=-3", "", "", "bytes 7-9/10", "789", 206, []string{"HEAD", "GET bytes=7-9"}},
		{"bytes=8-", "", "", "bytes 8-9/10", "89", 206, []string{"HEAD", "GET bytes=8-9"}},
		{"bytes=1-2", `"previous"`, "", "", object, 200, []string{"HEAD", "GET"}},
		{"bytes=1-2", "", tag, "", "", 304, []string{"HEAD"}},
		{"bytes=10-", "", "", "bytes */10", "", 416, []string{"HEAD"}},
		{"bytes=0-1,8-9", "", "", "", object, 200, []string{"HEAD", "GET"}},
		{"bytes=4-5", "", "", "", "", 500, []string{"HEAD", "GET bytes=4-5"}},
	} {
		got := send("tenant", http.MethodGet, tc.rangeRequest, tc.ifRange, tc.noneMatch, tc.upstream...)
		if got.Code != tc.status || got.Header().Get("Content-Range") != tc.contentRange ||
			(tc.status < 400 && got.Body.String() != tc.body) || strings.Contains(got.Body.String(), "unexpected S3 content range") {
			t.Errorf("range %q: %d %v %q", tc.rangeRequest, got.Code, got.Header(), got.Body)
		}
	}
	denied := send("another-tenant", http.MethodGet, "bytes=1-2", "", "")
	if denied.Code != http.StatusForbidden || denied.Header().Get("ETag") != "" || denied.Header().Get("Content-Range") != "" {
		t.Fatalf("cross-tenant download: %d %v", denied.Code, denied.Header())
	}
}
