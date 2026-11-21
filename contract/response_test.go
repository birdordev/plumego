package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestPutJSONBufferRetainsOnlyBoundedCapacity(t *testing.T) {
	putJSONBuffer(nil)
	for _, capacity := range []int{maxPooledJSONBufferCapacity, maxPooledJSONBufferCapacity + 1} {
		buf := bytes.NewBuffer(make([]byte, 1, capacity))
		wantLen := buf.Len()
		if capacity == maxPooledJSONBufferCapacity {
			wantLen = 0
		}
		putJSONBuffer(buf)
		if got := buf.Len(); got != wantLen {
			t.Errorf("buffer of capacity %d after put has length %d, want %d", capacity, got, wantLen)
		}
	}
}

func TestLargeJSONResponsePreservesSubsequentJSONWrites(t *testing.T) {
	for _, payload := range []string{strings.Repeat("a", maxPooledJSONBufferCapacity+1), "small"} {
		w := httptest.NewRecorder()
		if err := WriteResponse(w, nil, http.StatusOK, payload, nil); err != nil {
			t.Fatal(err)
		}
		var response struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Data != payload {
			t.Fatalf("payload length %d: received length %d, decode error = %v", len(payload), len(response.Data), err)
		}
	}
	w := httptest.NewRecorder()
	if err := WriteError(w, nil, APIError{status: http.StatusBadRequest, message: "invalid request"}); err != nil {
		t.Fatal(err)
	}
	var response errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error.Message != "invalid request" {
		t.Fatalf("error message = %q, decode error = %v", response.Error.Message, err)
	}
}

func TestWriteResponseNoBodyFraming(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusResetContent} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "4")
				if status == http.StatusResetContent {
					w.Header().Set("Transfer-Encoding", "chunked")
				}
				if err := WriteResponse(w, r, status, make(chan int), nil); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			resp, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || len(body) != 0 || resp.StatusCode != status || resp.ContentLength != 0 || len(resp.TransferEncoding) != 0 {
				t.Fatalf("status=%d length=%d transfer=%v body=%q error=%v", resp.StatusCode, resp.ContentLength, resp.TransferEncoding, body, err)
			}
		})
	}
}

// TestWriteResponseSuccessWithStructData verifies WriteResponse encodes struct data correctly.
func TestWriteResponseSuccessWithStructData(t *testing.T) {
	type userData struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	rec := httptest.NewRecorder()

	data := userData{ID: "42", Name: "Alice"}
	err := WriteResponse(rec, req, http.StatusOK, data, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get(HeaderContentType); ct != ContentTypeJSON {
		t.Fatalf("expected content-type %s, got %s", ContentTypeJSON, ct)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	dataMap, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be map, got %T", resp.Data)
	}
	if dataMap["id"] != "42" || dataMap["name"] != "Alice" {
		t.Fatalf("expected data to contain id and name, got %+v", dataMap)
	}
}

// TestWriteResponseSuccessWithMapData verifies WriteResponse encodes map data correctly.
func TestWriteResponseSuccessWithMapData(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	data := map[string]string{"status": "active", "type": "user"}
	err := WriteResponse(rec, req, http.StatusOK, data, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	dataMap, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected data to be map, got %T", resp.Data)
	}
	if dataMap["status"] != "active" || dataMap["type"] != "user" {
		t.Fatalf("expected data fields, got %+v", dataMap)
	}
}

// TestWriteResponseSuccessWithSliceData verifies WriteResponse encodes slice data correctly.
func TestWriteResponseSuccessWithSliceData(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	rec := httptest.NewRecorder()

	data := []map[string]string{
		{"id": "1", "name": "item1"},
		{"id": "2", "name": "item2"},
	}
	err := WriteResponse(rec, req, http.StatusOK, data, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	dataSlice, ok := resp.Data.([]any)
	if !ok {
		t.Fatalf("expected data to be slice, got %T", resp.Data)
	}
	if len(dataSlice) != 2 {
		t.Fatalf("expected 2 items, got %d", len(dataSlice))
	}
}

// TestWriteResponseNilDataOmitedFromJSON verifies nil data omits the data field due to omitempty tag.
func TestWriteResponseNilDataOmitedFromJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	err := WriteResponse(rec, req, http.StatusOK, nil, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	body := rec.Body.String()
	if body != "{}\n" {
		t.Fatalf("expected empty JSON object for nil data, got %q", body)
	}
}

// TestWriteResponseWithMeta verifies WriteResponse includes meta data when provided.
func TestWriteResponseWithMeta(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	rec := httptest.NewRecorder()

	data := map[string]string{"id": "42"}
	meta := map[string]any{"total": 100, "limit": 10}
	err := WriteResponse(rec, req, http.StatusOK, data, meta)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Meta["total"] != float64(100) || resp.Meta["limit"] != float64(10) {
		t.Fatalf("expected meta fields, got %+v", resp.Meta)
	}
}

// TestWriteResponseInjectsRequestID verifies request ID from context is injected into response.
func TestWriteResponseInjectsRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := WithRequestID(req.Context(), "req-123")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	data := map[string]string{"status": "ok"}
	err := WriteResponse(rec, req, http.StatusOK, data, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.RequestID != "req-123" {
		t.Fatalf("expected request id 'req-123', got %q", resp.RequestID)
	}
}

// TestWriteResponseNoRequestIDWhenNilRequest verifies no request ID when request is nil.
func TestWriteResponseNoRequestIDWhenNilRequest(t *testing.T) {
	rec := httptest.NewRecorder()

	err := WriteResponse(rec, nil, http.StatusOK, map[string]string{"status": "ok"}, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.RequestID != "" {
		t.Fatalf("expected no request id when request is nil, got %q", resp.RequestID)
	}
}

// TestWriteResponseAllSuccessStatuses verifies all 2xx statuses are accepted.
func TestWriteResponseAllSuccessStatuses(t *testing.T) {
	tests := []struct {
		status int
		name   string
	}{
		{http.StatusOK, "200"},
		{http.StatusCreated, "201"},
		{http.StatusAccepted, "202"},
		{http.StatusNoContent, "204"},
		{206, "206 PartialContent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			err := WriteResponse(rec, req, tt.status, map[string]string{"ok": "true"}, nil)
			if err != nil {
				t.Fatalf("unexpected write error: %v", err)
			}

			if rec.Code != tt.status {
				t.Fatalf("expected status %d, got %d", tt.status, rec.Code)
			}
		})
	}
}

// TestWriteResponseRejectedNonSuccessStatuses verifies non-2xx statuses are rejected.
func TestWriteResponseRejectedNonSuccessStatuses(t *testing.T) {
	tests := []struct {
		status int
		name   string
	}{
		{http.StatusBadRequest, "400"},
		{http.StatusUnauthorized, "401"},
		{http.StatusNotFound, "404"},
		{http.StatusInternalServerError, "500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			err := WriteResponse(rec, req, tt.status, map[string]string{"error": "test"}, nil)
			if err != ErrInvalidResponseStatus {
				t.Fatalf("expected ErrInvalidResponseStatus, got %v", err)
			}
		})
	}
}

// TestWriteResponseRejectedNilResponseWriter verifies nil ResponseWriter returns error.
func TestWriteResponseRejectedNilResponseWriter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	err := WriteResponse(nil, req, http.StatusOK, map[string]string{"ok": "true"}, nil)
	if err != ErrResponseWriterNil {
		t.Fatalf("expected ErrResponseWriterNil, got %v", err)
	}
}

func TestWriteResponseBodylessSuccessStatuses(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusResetContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			rec := httptest.NewRecorder()
			if err := WriteResponse(rec, nil, status, make(chan int), nil); err != nil {
				t.Fatalf("bodyless response encoded data: %v", err)
			}
			if rec.Code != status || rec.Body.Len() != 0 || rec.Header().Get(HeaderContentType) != "" {
				t.Fatalf("status = %d, body = %q, headers = %v", rec.Code, rec.Body.String(), rec.Header())
			}
		})
	}
}

type countingResponseWriter struct {
	*httptest.ResponseRecorder
	headerWrites int
	bodyWrites   int
	writeErr     error
}

func (w *countingResponseWriter) WriteHeader(status int) {
	w.headerWrites++
	w.ResponseRecorder.WriteHeader(status)
}

func (w *countingResponseWriter) Write(p []byte) (int, error) {
	w.bodyWrites++
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.ResponseRecorder.Write(p)
}

type panickingErrorDetail struct{}

func (panickingErrorDetail) MarshalJSON() ([]byte, error) {
	panic("private encoder diagnostic")
}

func TestWriteResponseRecoversEncoderPanicBeforeCommit(t *testing.T) {
	w := &countingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	err := WriteResponse(w, nil, http.StatusOK, panickingErrorDetail{}, nil)
	if err != errResponseEncodingPanic || w.headerWrites != 0 || w.bodyWrites != 0 || w.Header().Get(HeaderContentType) != "" {
		t.Fatalf("error=%v, header writes=%d, body writes=%d, headers=%v", err, w.headerWrites, w.bodyWrites, w.Header())
	}
}

func TestWriteErrorRecoversDetailEncoderPanic(t *testing.T) {
	w := &countingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithRequestID(req.Context(), "fallback-id"))
	err := WriteError(w, req, NewErrorBuilder().Type(TypeBadRequest).
		Message("bad detail").Detail("value", panickingErrorDetail{}).Build())
	if err != errErrorDetailEncodingPanic {
		t.Fatalf("WriteError = %v, want fixed encoding diagnostic", err)
	}
	if w.Code != http.StatusBadRequest || w.headerWrites != 1 || w.bodyWrites != 1 {
		t.Fatalf("status = %d, header writes = %d, body writes = %d", w.Code, w.headerWrites, w.bodyWrites)
	}
	var body errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != CodeBadRequest || body.Error.Message != "bad detail" ||
		body.Error.Details != nil || body.RequestID != "fallback-id" {
		t.Fatalf("unexpected fallback body: %+v", body)
	}
}

func TestResponseWritersReturnWriteFailureWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		write        func(http.ResponseWriter) error
		encodingFail bool
	}{
		{"success", http.StatusCreated, func(w http.ResponseWriter) error {
			return WriteResponse(w, nil, http.StatusCreated, "created", nil)
		}, false},
		{"error", http.StatusBadRequest, func(w http.ResponseWriter) error {
			return WriteError(w, nil, NewErrorBuilder().Type(TypeBadRequest).Build())
		}, false},
		{"fallback", http.StatusBadRequest, func(w http.ResponseWriter) error {
			return WriteError(w, nil, NewErrorBuilder().Type(TypeBadRequest).Detail("bad", make(chan int)).Build())
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &countingResponseWriter{ResponseRecorder: httptest.NewRecorder(), writeErr: io.ErrClosedPipe}
			err := tc.write(w)
			var encodingErr *json.UnsupportedTypeError
			if !errors.Is(err, io.ErrClosedPipe) || errors.As(err, &encodingErr) != tc.encodingFail {
				t.Fatalf("write error = %v, want closed pipe (encoding failure: %t)", err, tc.encodingFail)
			}
			if w.Code != tc.status || w.headerWrites != 1 || w.bodyWrites != 1 {
				t.Fatalf("status = %d, header writes = %d, body writes = %d", w.Code, w.headerWrites, w.bodyWrites)
			}
		})
	}
}

// TestWriteResponseContentTypeAlwaysSetForBodyStatuses verifies Content-Type header is set.
func TestWriteResponseContentTypeAlwaysSetForBodyStatuses(t *testing.T) {
	tests := []struct {
		status int
		name   string
	}{
		{http.StatusOK, "200"},
		{http.StatusCreated, "201"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			_ = WriteResponse(rec, req, tt.status, map[string]string{"ok": "true"}, nil)

			ct := rec.Header().Get(HeaderContentType)
			if ct != ContentTypeJSON {
				t.Fatalf("expected content-type %s, got %s", ContentTypeJSON, ct)
			}
		})
	}
}

// TestWriteResponseEncodingFailureReturnsError verifies encoding failures are returned.
func TestWriteResponseEncodingFailureReturnsError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// Create a map with a channel, which cannot be JSON-encoded
	data := map[string]any{"bad": make(chan int)}
	err := WriteResponse(rec, req, http.StatusOK, data, nil)
	if err == nil {
		t.Fatal("expected encoding error, got nil")
	}
}

// TestWriteResponseEncodingFailureDoesNotCommitHeaders verifies headers not sent on encoding failure.
func TestWriteResponseEncodingFailureDoesNotCommitHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// Intentionally pass a map with unencodable value
	data := map[string]any{"bad": make(chan int)}
	_ = WriteResponse(rec, req, http.StatusOK, data, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status to remain 200 (uncommitted), got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected no body on encoding failure, got %q", rec.Body.String())
	}
}

// TestWriteResponseCreatedStatus verifies 201 Created is accepted and encoded correctly.
func TestWriteResponseCreatedStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/users", nil)
	rec := httptest.NewRecorder()

	data := map[string]string{"id": "new-user-1"}
	err := WriteResponse(rec, req, http.StatusCreated, data, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", rec.Code)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Data == nil {
		t.Fatal("expected data to be present in 201 response")
	}
}

// TestWriteResponseNot2xxStatusRejected verifies non-2xx statuses are rejected (e.g., 304).
func TestWriteResponseNot2xxStatusRejected(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	rec := httptest.NewRecorder()

	// 304 Not Modified is a valid HTTP status, but WriteResponse only accepts 2xx
	err := WriteResponse(rec, req, http.StatusNotModified, map[string]string{"cached": "true"}, nil)
	if err != ErrInvalidResponseStatus {
		t.Fatalf("expected ErrInvalidResponseStatus for 304, got %v", err)
	}
}

// TestWriteResponseWithRequestMetadataAndRequestID verifies both request context and request ID propagate.
func TestWriteResponseWithRequestMetadataAndRequestID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	ctx := WithRequestID(req.Context(), "req-456")
	ctx = WithRequestContext(ctx, RequestContext{
		Params: map[string]string{"id": "42"},
	})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	data := map[string]string{"id": "42", "name": "Bob"}
	err := WriteResponse(rec, req, http.StatusOK, data, nil)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	var resp response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.RequestID != "req-456" {
		t.Fatalf("expected request id 'req-456', got %q", resp.RequestID)
	}
	// Verify request context metadata is available in context but not in the response
	if reqCtx := RequestContextFromContext(req.Context()); reqCtx.Params["id"] != "42" {
		t.Fatalf("expected request context to be available, got %+v", reqCtx.Params)
	}
}

// TestWriteResponseEmptyMetaOmitedFromJSON verifies nil/empty meta omits the meta field.
func TestWriteResponseEmptyMetaOmitedFromJSON(t *testing.T) {
	tests := []struct {
		name string
		meta map[string]any
	}{
		{name: "nil", meta: nil},
		{name: "empty", meta: map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			_ = WriteResponse(rec, req, http.StatusOK, map[string]string{"ok": "true"}, tt.meta)

			body := rec.Body.String()
			// When meta is nil or empty, the response should omit the meta field
			var resp map[string]any
			if err := json.Unmarshal([]byte(body), &resp); err != nil {
				t.Fatalf("failed to parse response: %v", err)
			}
			if _, hasMeta := resp["meta"]; hasMeta && tt.meta == nil {
				t.Fatalf("expected meta field to be omitted for nil meta, got %+v", resp)
			}
		})
	}
}

// TestWriteResponseVariousDataTypes verifies WriteResponse handles various primitive types.
func TestWriteResponseVariousDataTypes(t *testing.T) {
	tests := []struct {
		name string
		data any
	}{
		{name: "string", data: "hello"},
		{name: "int", data: 42},
		{name: "float", data: 3.14},
		{name: "bool", data: true},
		{name: "empty string", data: ""},
		{name: "zero int", data: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			err := WriteResponse(rec, req, http.StatusOK, tt.data, nil)
			if err != nil {
				t.Fatalf("unexpected write error for %v: %v", tt.data, err)
			}

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rec.Code)
			}

			var resp response
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			if resp.Data == nil {
				t.Fatalf("expected data to be present for type %T", tt.data)
			}
		})
	}
}

// TestWriteResponseZeroStatus returns error for 0 status.
func TestWriteResponseZeroStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	err := WriteResponse(rec, req, 0, map[string]string{"ok": "true"}, nil)
	if err != ErrInvalidResponseStatus {
		t.Fatalf("expected ErrInvalidResponseStatus for status 0, got %v", err)
	}
}

// TestWriteResponseInvalidHighStatus returns error for status > 299.
func TestWriteResponseInvalidHighStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	err := WriteResponse(rec, req, 300, map[string]string{"ok": "true"}, nil)
	if err != ErrInvalidResponseStatus {
		t.Fatalf("expected ErrInvalidResponseStatus for status 300, got %v", err)
	}
}

// TestWriteResponseRejectsInformationalStatus checks the 2xx-only boundary.
func TestWriteResponseRejectsInformationalStatus(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	err := WriteResponse(rec, req, 100, map[string]string{"ok": "true"}, nil)
	if err != ErrInvalidResponseStatus {
		t.Fatalf("expected ErrInvalidResponseStatus for status 100, got %v", err)
	}
}

type responseWriteProbe struct {
	*httptest.ResponseRecorder
	headerCalls int
	short       bool
	err         error
}

func (w *responseWriteProbe) WriteHeader(status int) {
	w.headerCalls++
	w.ResponseRecorder.WriteHeader(status)
}

func (w *responseWriteProbe) Write(p []byte) (int, error) {
	if w.short {
		p = p[:1]
	}
	n, _ := w.ResponseRecorder.Write(p)
	return n, w.err
}

func TestJSONResponseReportsShortWrite(t *testing.T) {
	writerErr := errors.New("write failed")
	for _, response := range []struct {
		name   string
		status int
		write  func(http.ResponseWriter) error
	}{
		{"success", http.StatusOK, func(w http.ResponseWriter) error { return WriteResponse(w, nil, http.StatusOK, "body", nil) }},
		{"error", http.StatusNotFound, func(w http.ResponseWriter) error {
			return WriteError(w, nil, NewErrorBuilder().Type(TypeNotFound).Build())
		}},
	} {
		for _, tc := range []struct {
			name  string
			short bool
			err   error
			want  error
		}{
			{"full", false, nil, nil},
			{"short", true, nil, io.ErrShortWrite},
			{"writer error", true, writerErr, writerErr},
		} {
			t.Run(response.name+"/"+tc.name, func(t *testing.T) {
				w := &responseWriteProbe{ResponseRecorder: httptest.NewRecorder(), short: tc.short, err: tc.err}
				w.Header().Set("Cache-Control", "private")
				w.Header().Set("X-Application", "fixture")
				if err := response.write(w); !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
				if w.Code != response.status || w.headerCalls != 1 {
					t.Fatalf("status = %d, header calls = %d", w.Code, w.headerCalls)
				}
				h := w.Result().Header
				if h.Get("Cache-Control") != "private" || h.Get("X-Application") != "fixture" || h.Get("Content-Type") != ContentTypeJSON {
					t.Fatalf("committed headers=%v", h)
				}
			})
		}
	}
}

func TestWriteErrorJoinsEncodingFailureWithShortWrite(t *testing.T) {
	w := &responseWriteProbe{ResponseRecorder: httptest.NewRecorder(), short: true}
	err := WriteError(w, nil, NewErrorBuilder().Type(TypeNotFound).Detail("unsupported", make(chan int)).Build())
	var encErr *json.UnsupportedTypeError
	if !errors.As(err, &encErr) || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("error = %v, want encoding failure and short write", err)
	}
	if w.Code != http.StatusNotFound || w.headerCalls != 1 {
		t.Fatalf("status=%d commits=%d", w.Code, w.headerCalls)
	}
}

func TestWriteResponseBodylessFramingBeforeTransport(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusResetContent} {
		w := &countingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
		w.Header().Set("Content-Length", "4")
		w.Header().Set("Transfer-Encoding", "chunked")
		if err := WriteResponse(w, nil, status, make(chan int), nil); err != nil {
			t.Fatal(err)
		}
		h := w.Result().Header
		wantLength := ""
		if status == http.StatusResetContent {
			wantLength = "0"
		}
		if w.headerWrites != 1 || w.bodyWrites != 0 || h.Get("Content-Length") != wantLength || h.Get("Transfer-Encoding") != "" {
			t.Fatalf("status=%d commits=%d body writes=%d headers=%v", status, w.headerWrites, w.bodyWrites, h)
		}
	}
}

func TestBodylessResponseKeepsUnrelatedHeaders(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusResetContent} {
		w := &countingResponseWriter{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w.Header().Set("Content-Length", "10")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set(RequestIDHeader, "request-123")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-App", "example")
		if err := WriteResponse(w, req, status, nil, nil); err != nil {
			t.Fatal(err)
		}
		h := w.Result().Header
		if h.Get(RequestIDHeader) != "request-123" || h.Get("Cache-Control") != "no-store" ||
			h.Get("X-App") != "example" || h.Get("Transfer-Encoding") != "" || w.headerWrites != 1 || w.bodyWrites != 0 {
			t.Fatalf("status=%d headers=%v writes=%d/%d", status, h, w.headerWrites, w.bodyWrites)
		}
	}
}

type pausedJSONWrite struct {
	*httptest.ResponseRecorder
	entered, resume chan struct{}
}

func (w *pausedJSONWrite) Write(data []byte) (int, error) {
	close(w.entered)
	<-w.resume
	return w.ResponseRecorder.Write(data)
}

func TestPooledJSONBufferRemainsOwnedUntilWriteReturns(t *testing.T) {
	first := &pausedJSONWrite{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- WriteResponse(first, nil, http.StatusOK, "first", nil) }()
	<-first.entered
	second := httptest.NewRecorder()
	err := WriteResponse(second, nil, http.StatusOK, strings.Repeat("x", 1024), nil)
	close(first.resume)
	if firstErr := <-done; firstErr != nil || err != nil {
		t.Fatalf("encoding errors: %v, %v", firstErr, err)
	}
	if first.Body.String() != "{\"data\":\"first\"}\n" || !json.Valid(second.Body.Bytes()) || second.Body.Len() <= 1024 {
		t.Fatalf("first=%q second size=%d", first.Body, second.Body.Len())
	}
}

func TestWriteErrorPreservesIntegerDetailsAndEmptyContainers(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(WithRequestID(req.Context(), "fixture"))
	w := httptest.NewRecorder()
	err := NewErrorBuilder().Type(TypeValidation).Details(map[string]any{
		"integer": int64(9007199254740993), "empty": []string{}, "nil": nil,
	}).Build()
	if writeErr := WriteError(w, req, err); writeErr != nil {
		t.Fatal(writeErr)
	}
	var body struct {
		Error     struct{ Details map[string]json.RawMessage }
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"integer": "9007199254740993", "empty": "[]", "nil": "null"} {
		if got := string(body.Error.Details[k]); got != want {
			t.Errorf("%s = %s, want %s", k, got, want)
		}
	}
	if body.RequestID != "fixture" {
		t.Fatalf("request ID = %q", body.RequestID)
	}
}

func TestErrorRequestIDFallbackAndCauseIsolation(t *testing.T) {
	for _, input := range []struct{ explicit, inherited, want string }{
		{"explicit", "parent", "explicit"},
		{"", "parent", "parent"},
		{"invalid\n", "parent", "invalid"},
		{"bad\nvalue", "parent", "parent"},
		{"", "", ""},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(WithRequestID(r.Context(), input.inherited))
		cause := errors.New("private fixture message")
		apiErr := NewErrorBuilder().Type(TypeInternal).RequestID(input.explicit).Wrap(cause).Build()
		if err := WriteError(w, r, apiErr); err != nil {
			t.Fatal(err)
		}
		var got errorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.RequestID != input.want ||
			strings.Contains(w.Body.String(), cause.Error()) {
			t.Fatalf("request_id=%q want=%q response=%s err=%v", got.RequestID, input.want, w.Body, err)
		}
	}
}

func TestSuccessEncodingFailureLeavesHeadersForReplacementResponse(t *testing.T) {
	w := &responseWriteProbe{ResponseRecorder: httptest.NewRecorder()}
	w.Header().Set("Content-Type", "application/fixture")
	w.Header().Set("Cache-Control", "private")
	if err := WriteResponse(w, nil, http.StatusOK, make(chan int), nil); err == nil || w.headerCalls != 0 ||
		w.Header().Get("Content-Type") != "application/fixture" || w.Header().Get("Cache-Control") != "private" {
		t.Fatalf("encoding err=%v commits=%d headers=%v", err, w.headerCalls, w.Header())
	}
	if err := WriteResponse(w, nil, http.StatusAccepted, "fixture", nil); err != nil || w.headerCalls != 1 ||
		w.Header().Get("Content-Type") != ContentTypeJSON || w.Header().Get("Cache-Control") != "private" {
		t.Fatalf("replacement err=%v commits=%d headers=%v", err, w.headerCalls, w.Header())
	}
}

func TestBodylessStatusesSkipEncodingWhileNormalizingErrors(t *testing.T) {
	for _, status := range []int{204, 205} {
		rec := httptest.NewRecorder()
		rec.Header().Set("X-Existing", "preserved")
		rec.Header().Set("Transfer-Encoding", "chunked")
		rec.Header().Set("Content-Length", "4")
		if err := WriteResponse(rec, nil, status, make(chan int), nil); err != nil || rec.Code != status ||
			rec.Body.Len() != 0 || rec.Header().Get("Transfer-Encoding") != "" || rec.Header().Get("X-Existing") != "preserved" {
			t.Fatalf("status=%d output=%d %v %q err=%v", status, rec.Code, rec.Header(), rec.Body.String(), err)
		}
		want := ""
		if status == 205 {
			want = "0"
		}
		if got := rec.Header().Get("Content-Length"); got != want {
			t.Fatalf("status=%d length=%q", status, got)
		}
		rec = httptest.NewRecorder()
		if err := WriteError(rec, nil, APIError{status: status}); err != nil || rec.Code != 500 || !strings.Contains(rec.Body.String(), `"error":`) {
			t.Fatalf("failure status=%d body=%q err=%v", rec.Code, rec.Body.String(), err)
		}
	}
}

func TestExplicitErrorIDOrValidatedRequestFallback(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(WithRequestID(req.Context(), "parent-fixture"))
	for _, tc := range []struct{ input, want string }{{"explicit", "explicit"}, {"bad\nvalue", "parent-fixture"}, {"", "parent-fixture"}} {
		w := httptest.NewRecorder()
		if err := WriteError(w, req, NewErrorBuilder().Type(TypeNotFound).RequestID(tc.input).Build()); err != nil {
			t.Fatal(err)
		}
		var body errorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.RequestID != tc.want || w.Code != 404 {
			t.Fatalf("status=%d id=%q err=%v", w.Code, body.RequestID, err)
		}
	}
}

type fixtureShortJSONWriter struct {
	*httptest.ResponseRecorder
	err     error
	headers int
}

func (w *fixtureShortJSONWriter) Write(p []byte) (int, error) {
	n, _ := w.ResponseRecorder.Write(p[:len(p)-1])
	return n, w.err
}
func (w *fixtureShortJSONWriter) WriteHeader(code int) {
	w.headers++
	w.ResponseRecorder.WriteHeader(code)
}

func TestEncodedResponseKeepsOriginalErrorOverShortWrite(t *testing.T) {
	cause := errors.New("fixture write")
	for _, status := range []int{200, 404} {
		for _, withCause := range []bool{false, true} {
			w := &fixtureShortJSONWriter{ResponseRecorder: httptest.NewRecorder()}
			want := io.ErrShortWrite
			if withCause {
				w.err = cause
				want = cause
			}
			var err error
			if status == 200 {
				err = WriteResponse(w, nil, status, "sample", nil)
			} else {
				err = WriteError(w, nil, NewErrorBuilder().Type(TypeNotFound).Build())
			}
			if !errors.Is(err, want) || w.headers != 1 || w.Code != status || w.Header().Get(HeaderContentType) != ContentTypeJSON {
				t.Fatalf("status=%d headers=%d err=%v", w.Code, w.headers, err)
			}
		}
	}
}

func TestErrorDetailFallbackJoinsEncodingAndWriterFailures(t *testing.T) {
	w := &fixtureShortJSONWriter{ResponseRecorder: httptest.NewRecorder(), err: io.ErrClosedPipe}
	w.Header().Set("X-Existing", "fixture")
	e := NewErrorBuilder().Type(TypeNotFound).RequestID("fixture-id").Detail("unsupported", make(chan int)).Build()
	err := WriteError(w, nil, e)
	var unsupported *json.UnsupportedTypeError
	if !errors.Is(err, io.ErrClosedPipe) || !errors.As(err, &unsupported) || w.headers != 1 || w.Code != 404 ||
		w.Header().Get("X-Existing") != "fixture" || !strings.Contains(w.Body.String(), "fixture-id") || strings.Contains(w.Body.String(), "details") {
		t.Fatalf("result=%d %v %q err=%v", w.Code, w.Header(), w.Body.String(), err)
	}
}

type countedResetContentMarshaler struct{ calls *int }

func (m countedResetContentMarshaler) MarshalJSON() ([]byte, error) {
	*m.calls++
	return []byte(`"fixture"`), nil
}
func TestResetContentDoesNotCallUserMarshaler(t *testing.T) {
	calls := 0
	payload := countedResetContentMarshaler{calls: &calls}
	rec := httptest.NewRecorder()
	rec.Header().Set("Transfer-Encoding", "chunked")
	if err := WriteResponse(rec, nil, 205, payload, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || rec.Code != 205 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "0" || rec.Header().Get("Transfer-Encoding") != "" {
		t.Fatalf("calls=%d response=%d headers=%v", calls, rec.Code, rec.Header())
	}
	rec = httptest.NewRecorder()
	if err := WriteResponse(rec, nil, 200, payload, nil); err != nil || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}
