package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storefile "github.com/birdordev/plumego/store/file"
)

func newS3Server(t *testing.T) (*httptest.Server, map[string][]byte) {
	t.Helper()
	store := make(map[string][]byte)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		pathStr := r.URL.Path
		parts := strings.SplitN(strings.TrimPrefix(pathStr, "/"), "/", 2)
		key := ""
		if len(parts) == 2 {
			key = parts[1]
		}

		if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
			type content struct {
				XMLName      struct{}  `xml:"Contents"`
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			}
			type listResult struct {
				XMLName  xml.Name  `xml:"ListBucketResult"`
				Contents []content `xml:"Contents"`
			}
			var result listResult
			prefix := r.URL.Query().Get("prefix")
			for k, v := range store {
				if prefix == "" || strings.HasPrefix(k, prefix) {
					result.Contents = append(result.Contents, content{
						Key:          k,
						Size:         int64(len(v)),
						LastModified: time.Now(),
					})
				}
			}
			w.Header().Set("Content-Type", "application/xml")
			xml.NewEncoder(w).Encode(result)
			return
		}

		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			store[key] = body
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			data, ok := store[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		case http.MethodHead:
			data, ok := store[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			if _, ok := store[key]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(store, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, store
}

func newTestS3Storage(t *testing.T, srv *httptest.Server) *S3Storage {
	t.Helper()
	host := strings.TrimPrefix(srv.URL, "http://")
	s, err := NewS3Storage(S3Config{
		Endpoint:  host,
		Bucket:    "testbucket",
		UseSSL:    false,
		PathStyle: true,
	}, nil)
	if err != nil {
		t.Fatalf("NewS3Storage: %v", err)
	}
	s.client = &http.Client{}
	return s
}

type failingSaveMetadata struct {
	mockMetadata
	err error
}

func (m *failingSaveMetadata) Save(context.Context, *File) error {
	return m.err
}

func TestS3Storage_Put_Get(t *testing.T) {
	srv, _ := newS3Server(t)
	s := newTestS3Storage(t, srv)
	ctx := t.Context()

	content := []byte("hello s3")
	result, err := s.Put(ctx, PutOptions{
		TenantID:    "t1",
		Reader:      bytes.NewReader(content),
		FileName:    "test.txt",
		ContentType: "text/plain",
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if result.StorageType != "s3" {
		t.Errorf("StorageType = %q, want s3", result.StorageType)
	}
	if result.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", result.Size, len(content))
	}

	reader, err := s.Get(ctx, result.Path)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer reader.Close()
	got, _ := io.ReadAll(reader)
	if !bytes.Equal(got, content) {
		t.Errorf("Get content = %q, want %q", got, content)
	}
}

func TestS3Storage_Put_SpoolsAndSetsContentLength(t *testing.T) {
	content := bytes.Repeat([]byte("a"), 1024*1024)
	var gotContentLength int64
	var gotBodySize int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		gotContentLength = r.ContentLength
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("ReadAll body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		gotBodySize = len(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := newTestS3Storage(t, srv)
	file, err := s.Put(t.Context(), PutOptions{
		TenantID:    "t1",
		Reader:      bytes.NewReader(content),
		FileName:    "large.bin",
		ContentType: "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if gotContentLength != int64(len(content)) {
		t.Fatalf("ContentLength = %d, want %d", gotContentLength, len(content))
	}
	if gotBodySize != len(content) {
		t.Fatalf("uploaded body size = %d, want %d", gotBodySize, len(content))
	}
	sum := sha256.Sum256(content)
	if file.Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("Hash = %q, want %q", file.Hash, hex.EncodeToString(sum[:]))
	}
}

func TestS3Storage_PutRejectsKnownSizeAboveSinglePutLimit(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := newTestS3Storage(t, srv)
	s.maxSinglePutBytes = 5

	_, err := s.Put(t.Context(), PutOptions{
		TenantID:    "t1",
		Reader:      strings.NewReader("abcdef"),
		FileName:    "too-large.txt",
		ContentType: "text/plain",
		Size:        6,
	})
	if !errors.Is(err, storefile.ErrInvalidSize) {
		t.Fatalf("Put error = %v, want ErrInvalidSize", err)
	}
	if requests != 0 {
		t.Fatalf("S3 server received %d requests, want 0", requests)
	}
}

func TestS3Storage_PutRejectsUnknownSizeAboveSinglePutLimit(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := newTestS3Storage(t, srv)
	s.maxSinglePutBytes = 5

	_, err := s.Put(t.Context(), PutOptions{
		TenantID:    "t1",
		Reader:      strings.NewReader("abcdef"),
		FileName:    "too-large.txt",
		ContentType: "text/plain",
		Size:        -1,
	})
	if !errors.Is(err, storefile.ErrInvalidSize) {
		t.Fatalf("Put error = %v, want ErrInvalidSize", err)
	}
	if requests != 0 {
		t.Fatalf("S3 server received %d requests, want 0", requests)
	}
}

func TestS3Storage_Put_UsesConfiguredTempDir(t *testing.T) {
	tempDir := t.TempDir()
	content := []byte("spooled in configured temp dir")
	seenSpool := make(chan bool, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		matches, _ := filepath.Glob(filepath.Join(tempDir, "plumego-s3-upload-*"))
		seenSpool <- len(matches) > 0
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	s, err := NewS3Storage(S3Config{
		Endpoint:  host,
		Bucket:    "testbucket",
		UseSSL:    false,
		PathStyle: true,
		TempDir:   tempDir,
	}, nil)
	if err != nil {
		t.Fatalf("NewS3Storage: %v", err)
	}
	s.client = &http.Client{}

	if _, err := s.Put(t.Context(), PutOptions{
		TenantID:    "t1",
		Reader:      bytes.NewReader(content),
		FileName:    "temp.txt",
		ContentType: "text/plain",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if !<-seenSpool {
		t.Fatal("expected upload spool file in configured temp dir")
	}
}

func TestS3Storage_Put_BoundsErrorBody(t *testing.T) {
	largeBody := strings.Repeat("x", 16*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(largeBody))
	}))
	t.Cleanup(srv.Close)

	s := newTestS3Storage(t, srv)
	_, err := s.Put(t.Context(), PutOptions{
		TenantID:    "t1",
		Reader:      strings.NewReader("body"),
		FileName:    "err.txt",
		ContentType: "text/plain",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if len(msg) > s3ErrorBodyLimit+512 {
		t.Fatalf("error length = %d, want bounded near %d", len(msg), s3ErrorBodyLimit)
	}
	if !strings.Contains(msg, "...(truncated)") {
		t.Fatalf("error %q does not mark truncated body", msg)
	}
}

func TestS3Storage_Get_NotFound(t *testing.T) {
	srv, _ := newS3Server(t)
	s := newTestS3Storage(t, srv)

	_, err := s.Get(t.Context(), "nonexistent/key.txt")
	if !errors.Is(err, storefile.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestS3Storage_RejectsUnsafePublicPaths(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := newTestS3Storage(t, srv)
	ctx := t.Context()

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "Get", run: func() error { _, err := s.Get(ctx, "../secret.txt"); return err }},
		{name: "Delete", run: func() error { return s.Delete(ctx, "/secret.txt") }},
		{name: "Exists", run: func() error { _, err := s.Exists(ctx, "tenant/../secret.txt"); return err }},
		{name: "Stat", run: func() error { _, err := s.Stat(ctx, "tenant/.."); return err }},
		{name: "List", run: func() error { _, err := s.List(ctx, "../tenant/", 10); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called = false
			if err := tt.run(); !errors.Is(err, storefile.ErrInvalidPath) {
				t.Fatalf("%s error = %v, want ErrInvalidPath", tt.name, err)
			}
			if called {
				t.Fatalf("%s reached S3 server for unsafe path", tt.name)
			}
		})
	}
}

func TestS3Storage_Delete(t *testing.T) {
	srv, store := newS3Server(t)
	s := newTestS3Storage(t, srv)

	store["mykey.txt"] = []byte("data")

	if err := s.Delete(t.Context(), "mykey.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := store["mykey.txt"]; ok {
		t.Error("key should be deleted from mock store")
	}
}

func TestS3Storage_Delete_NotFound(t *testing.T) {
	srv, _ := newS3Server(t)
	s := newTestS3Storage(t, srv)

	err := s.Delete(t.Context(), "ghost.txt")
	if !errors.Is(err, storefile.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestS3Storage_Exists(t *testing.T) {
	srv, store := newS3Server(t)
	s := newTestS3Storage(t, srv)
	ctx := t.Context()

	exists, err := s.Exists(ctx, "nope.txt")
	if err != nil || exists {
		t.Errorf("expected not found, got exists=%v err=%v", exists, err)
	}

	store["exists.txt"] = []byte("yes")
	exists, err = s.Exists(ctx, "exists.txt")
	if err != nil || !exists {
		t.Errorf("expected found, got exists=%v err=%v", exists, err)
	}
}

func TestS3Storage_ExistsReturnsServerErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("method = %s, want HEAD", r.Method)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	s := newTestS3Storage(t, srv)
	exists, err := s.Exists(t.Context(), "broken.txt")
	if err == nil {
		t.Fatalf("Exists() error = nil, want server error")
	}
	if exists {
		t.Fatalf("Exists() = true, want false on server error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("Exists() error = %v, want status 500", err)
	}
}

func TestS3Storage_Stat(t *testing.T) {
	srv, store := newS3Server(t)
	s := newTestS3Storage(t, srv)
	ctx := t.Context()

	_, err := s.Stat(ctx, "missing.txt")
	if !errors.Is(err, storefile.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing file, got %v", err)
	}

	store["stat.txt"] = []byte("content")
	stat, err := s.Stat(ctx, "stat.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if stat.Path != "stat.txt" {
		t.Errorf("Path = %q, want stat.txt", stat.Path)
	}
}

func TestS3Storage_GetURL(t *testing.T) {
	srv, _ := newS3Server(t)
	s := newTestS3Storage(t, srv)

	url, err := s.GetURL(t.Context(), "some/file.txt", time.Minute)
	if err != nil {
		t.Fatalf("GetURL: %v", err)
	}
	if url == "" {
		t.Error("expected non-empty URL")
	}

	_, err = s.GetURL(t.Context(), "../secret.txt", time.Minute)
	if !errors.Is(err, storefile.ErrInvalidPath) {
		t.Fatalf("GetURL unsafe path error = %v, want ErrInvalidPath", err)
	}
}

func TestS3Storage_List(t *testing.T) {
	srv, store := newS3Server(t)
	s := newTestS3Storage(t, srv)

	store["t1/file1.txt"] = []byte("a")
	store["t1/file2.txt"] = []byte("b")
	store["t2/file3.txt"] = []byte("c")

	files, err := s.List(t.Context(), "t1/", 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("List count = %d, want 2", len(files))
	}

	all, err := s.List(t.Context(), "", 10)
	if err != nil {
		t.Fatalf("List empty prefix: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("List empty prefix count = %d, want 3", len(all))
	}
}

func TestS3Storage_ListFollowsPagination(t *testing.T) {
	var tokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("list-type") != "2" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		tokens = append(tokens, r.URL.Query().Get("continuation-token"))

		type content struct {
			XMLName      struct{}  `xml:"Contents"`
			Key          string    `xml:"Key"`
			Size         int64     `xml:"Size"`
			LastModified time.Time `xml:"LastModified"`
		}
		type listResult struct {
			XMLName               xml.Name  `xml:"ListBucketResult"`
			IsTruncated           bool      `xml:"IsTruncated"`
			NextContinuationToken string    `xml:"NextContinuationToken,omitempty"`
			Contents              []content `xml:"Contents"`
		}

		result := listResult{}
		switch r.URL.Query().Get("continuation-token") {
		case "":
			result.IsTruncated = true
			result.NextContinuationToken = "page-2"
			result.Contents = []content{
				{Key: "t1/file1.txt", Size: 1, LastModified: time.Now()},
				{Key: "t1/file2.txt", Size: 2, LastModified: time.Now()},
			}
		case "page-2":
			result.Contents = []content{{Key: "t1/file3.txt", Size: 3, LastModified: time.Now()}}
		default:
			t.Fatalf("unexpected continuation-token %q", r.URL.Query().Get("continuation-token"))
		}

		w.Header().Set("Content-Type", "application/xml")
		if err := xml.NewEncoder(w).Encode(result); err != nil {
			t.Fatalf("encode list result: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	s := newTestS3Storage(t, srv)

	files, err := s.List(t.Context(), "t1/", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("List count = %d, want 3", len(files))
	}
	if len(tokens) != 2 || tokens[0] != "" || tokens[1] != "page-2" {
		t.Fatalf("continuation tokens = %#v, want [\"\", \"page-2\"]", tokens)
	}
}

func TestS3Storage_ListLimitBoundsPagination(t *testing.T) {
	var requests int
	var gotMaxKeys string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("list-type") != "2" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		requests++
		gotMaxKeys = r.URL.Query().Get("max-keys")

		type content struct {
			XMLName      struct{}  `xml:"Contents"`
			Key          string    `xml:"Key"`
			Size         int64     `xml:"Size"`
			LastModified time.Time `xml:"LastModified"`
		}
		result := struct {
			XMLName               xml.Name  `xml:"ListBucketResult"`
			IsTruncated           bool      `xml:"IsTruncated"`
			NextContinuationToken string    `xml:"NextContinuationToken,omitempty"`
			Contents              []content `xml:"Contents"`
		}{
			IsTruncated:           true,
			NextContinuationToken: "page-2",
			Contents: []content{
				{Key: "t1/file1.txt", Size: 1, LastModified: time.Now()},
				{Key: "t1/file2.txt", Size: 2, LastModified: time.Now()},
			},
		}

		w.Header().Set("Content-Type", "application/xml")
		if err := xml.NewEncoder(w).Encode(result); err != nil {
			t.Fatalf("encode list result: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	s := newTestS3Storage(t, srv)

	files, err := s.List(t.Context(), "t1/", 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("List count = %d, want 2", len(files))
	}
	if requests != 1 {
		t.Fatalf("List requests = %d, want 1", requests)
	}
	if gotMaxKeys != "2" {
		t.Fatalf("max-keys = %q, want 2", gotMaxKeys)
	}
}

func TestS3Storage_ListTruncatedWithoutTokenFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := struct {
			XMLName     xml.Name `xml:"ListBucketResult"`
			IsTruncated bool     `xml:"IsTruncated"`
		}{
			IsTruncated: true,
		}
		w.Header().Set("Content-Type", "application/xml")
		if err := xml.NewEncoder(w).Encode(result); err != nil {
			t.Fatalf("encode list result: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	s := newTestS3Storage(t, srv)

	_, err := s.List(t.Context(), "t1/", 0)
	if err == nil || !strings.Contains(err.Error(), "missing continuation token") {
		t.Fatalf("List error = %v, want missing continuation token", err)
	}
}

func TestS3Storage_Copy(t *testing.T) {
	srv, store := newS3Server(t)
	s := newTestS3Storage(t, srv)

	store["src/file.txt"] = []byte("copy me")

	if err := s.Copy(t.Context(), "src/file.txt", "dst/file.txt"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
}

func TestS3Storage_CopyEscapesSourceHeader(t *testing.T) {
	var gotSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSource = r.Header.Get("x-amz-copy-source")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := newTestS3Storage(t, srv)
	if err := s.Copy(t.Context(), "src folder/file name.txt", "dst/file.txt"); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	if !strings.Contains(gotSource, "src%20folder/file%20name.txt") {
		t.Fatalf("copy source header = %q, want escaped segments with preserved hierarchy", gotSource)
	}
	if strings.Contains(gotSource, "/../") || strings.Contains(gotSource, " ") {
		t.Fatalf("copy source header contains unsafe path text: %q", gotSource)
	}
}

func TestS3Storage_CopyRejectsUnsafePaths(t *testing.T) {
	srv, _ := newS3Server(t)
	s := newTestS3Storage(t, srv)

	if err := s.Copy(t.Context(), "../src.txt", "dst/file.txt"); !errors.Is(err, storefile.ErrInvalidPath) {
		t.Fatalf("Copy unsafe source error = %v, want ErrInvalidPath", err)
	}
	if err := s.Copy(t.Context(), "src/file.txt", "../dst.txt"); !errors.Is(err, storefile.ErrInvalidPath) {
		t.Fatalf("Copy unsafe destination error = %v, want ErrInvalidPath", err)
	}
}

func TestS3Storage_Put_Deduplication(t *testing.T) {
	srv, _ := newS3Server(t)
	host := strings.TrimPrefix(srv.URL, "http://")
	s, _ := NewS3Storage(S3Config{
		Endpoint:  host,
		Bucket:    "testbucket",
		PathStyle: true,
	}, &mockMetadata{})
	s.client = &http.Client{}

	ctx := t.Context()
	content := []byte("deduplicated s3 content")

	first, err := s.Put(ctx, PutOptions{TenantID: "t1", Reader: bytes.NewReader(content), FileName: "dup.bin"})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	second, err := s.Put(ctx, PutOptions{TenantID: "t1", Reader: bytes.NewReader(content), FileName: "dup.bin"})
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if second.Hash != first.Hash {
		t.Errorf("expected deduplication, hash mismatch: %q vs %q", first.Hash, second.Hash)
	}
}

func TestS3Storage_PutReportsCleanupFailureAfterMetadataError(t *testing.T) {
	saveErr := errors.New("metadata down")
	deleteCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			deleteCalled = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	host := strings.TrimPrefix(srv.URL, "http://")
	s, err := NewS3Storage(S3Config{
		Endpoint:  host,
		Bucket:    "testbucket",
		PathStyle: true,
	}, &failingSaveMetadata{err: saveErr})
	if err != nil {
		t.Fatalf("NewS3Storage: %v", err)
	}
	s.client = &http.Client{}

	_, err = s.Put(t.Context(), PutOptions{
		TenantID: "t1",
		Reader:   bytes.NewReader([]byte("content")),
		FileName: "orphan.txt",
	})
	if !errors.Is(err, saveErr) {
		t.Fatalf("Put error = %v, want metadata error", err)
	}
	if !strings.Contains(err.Error(), "cleanup uploaded object") {
		t.Fatalf("Put error = %v, want cleanup failure detail", err)
	}
	if !deleteCalled {
		t.Fatal("expected cleanup delete to be attempted")
	}
}

func TestS3Storage_Put_RejectsUnsafeTenantID(t *testing.T) {
	srv, _ := newS3Server(t)
	s := newTestS3Storage(t, srv)

	_, err := s.Put(t.Context(), PutOptions{
		TenantID: "../t1",
		Reader:   bytes.NewReader([]byte("content")),
		FileName: "bad.txt",
	})
	if !errors.Is(err, storefile.ErrInvalidPath) {
		t.Fatalf("Put error = %v, want ErrInvalidPath", err)
	}
}

func TestS3Storage_Put_DeduplicationIsTenantScoped(t *testing.T) {
	srv, _ := newS3Server(t)
	host := strings.TrimPrefix(srv.URL, "http://")
	s, _ := NewS3Storage(S3Config{
		Endpoint:  host,
		Bucket:    "testbucket",
		PathStyle: true,
	}, &mockMetadata{})
	s.client = &http.Client{}

	content := []byte("same s3 bytes")
	first, err := s.Put(t.Context(), PutOptions{TenantID: "t1", Reader: bytes.NewReader(content), FileName: "same.bin"})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}
	second, err := s.Put(t.Context(), PutOptions{TenantID: "t2", Reader: bytes.NewReader(content), FileName: "same.bin"})
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if second.TenantID != "t2" {
		t.Fatalf("second TenantID = %q, want t2", second.TenantID)
	}
	if second.ID == first.ID {
		t.Fatalf("second upload reused first tenant metadata id %q", second.ID)
	}
}

func TestS3Storage_buildURL_VirtualHosted(t *testing.T) {
	s := &S3Storage{endpoint: "s3.amazonaws.com", bucket: "mybucket", useSSL: true, pathStyle: false}
	got := s.buildURL("tenant/file.txt")
	if !strings.HasPrefix(got, "https://mybucket.s3.amazonaws.com/") {
		t.Errorf("buildURL = %q, expected virtual-hosted HTTPS prefix", got)
	}
	if !strings.Contains(got, "file.txt") {
		t.Errorf("buildURL = %q, expected file.txt in URL", got)
	}
}

func TestS3Storage_buildURL_PathStyle(t *testing.T) {
	s := &S3Storage{endpoint: "minio.local:9000", bucket: "testbucket", useSSL: false, pathStyle: true}
	got := s.buildURL("folder/object.png")
	if !strings.HasPrefix(got, "http://minio.local:9000/testbucket/") {
		t.Errorf("buildURL = %q, expected path-style HTTP prefix", got)
	}
	if !strings.Contains(got, "object.png") {
		t.Errorf("buildURL = %q, expected object.png in URL", got)
	}
	if !strings.Contains(got, "/folder/object.png") {
		t.Errorf("buildURL = %q, expected object hierarchy to be preserved", got)
	}
}

func TestS3Storage_buildURL_EscapesSegments(t *testing.T) {
	s := &S3Storage{endpoint: "minio.local:9000", bucket: "testbucket", useSSL: false, pathStyle: true}
	got := s.buildURL("folder name/object #1.png")
	if !strings.Contains(got, "/folder%20name/object%20%231.png") {
		t.Fatalf("buildURL = %q, want escaped path segments with slash separators", got)
	}
}

func TestS3Storage_buildURL_PathTraversalEncoded(t *testing.T) {
	s := &S3Storage{endpoint: "s3.amazonaws.com", bucket: "mybucket", useSSL: true, pathStyle: true}
	got := s.buildURL("../../etc/passwd")
	if strings.Contains(got, "/../") || strings.HasSuffix(got, "/..") {
		t.Errorf("buildURL contains unencoded path traversal, got %q", got)
	}
}

func TestNewS3Storage_MissingConfig(t *testing.T) {
	_, err := NewS3Storage(S3Config{}, nil)
	if err == nil {
		t.Fatal("expected error for missing S3 config")
	}
}

func TestNewS3Storage_InvalidSinglePutLimit(t *testing.T) {
	_, err := NewS3Storage(S3Config{
		Endpoint:          "localhost:9000",
		Bucket:            "testbucket",
		MaxSinglePutBytes: -1,
	}, nil)
	if !errors.Is(err, storefile.ErrInvalidSize) {
		t.Fatalf("NewS3Storage error = %v, want ErrInvalidSize", err)
	}
}

func TestNewS3Storage_DefaultRegion(t *testing.T) {
	s, err := NewS3Storage(S3Config{
		Endpoint: "s3.amazonaws.com",
		Bucket:   "my-bucket",
	}, nil)
	if err != nil {
		t.Fatalf("NewS3Storage: %v", err)
	}
	if s.region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", s.region)
	}
}
