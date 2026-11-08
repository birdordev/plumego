package protocolmw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/birdordev/plumego/contract"
	gatewayproto "github.com/birdordev/plumego/x/gateway/protocol"
)

type stubProtocolRequest struct {
	method string
	body   []byte
}

func (r *stubProtocolRequest) Method() string               { return r.method }
func (r *stubProtocolRequest) Headers() map[string][]string { return map[string][]string{} }
func (r *stubProtocolRequest) Body() io.Reader              { return bytes.NewReader(r.body) }
func (r *stubProtocolRequest) Metadata() map[string]any     { return map[string]any{} }

type stubProtocolResponse struct {
	status int
	body   []byte
}

func (r *stubProtocolResponse) StatusCode() int              { return r.status }
func (r *stubProtocolResponse) Headers() map[string][]string { return map[string][]string{} }
func (r *stubProtocolResponse) Body() io.Reader              { return bytes.NewReader(r.body) }
func (r *stubProtocolResponse) Metadata() map[string]any     { return map[string]any{} }

type protocolErrorResponse struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

type stubAdapter struct {
	lastBody     string
	transformErr error
	executeErr   error
	encodeErr    error
}

func (a *stubAdapter) Name() string                             { return "stub" }
func (a *stubAdapter) Handles(_ *gatewayproto.HTTPRequest) bool { return true }

func (a *stubAdapter) Transform(_ context.Context, req *gatewayproto.HTTPRequest) (gatewayproto.Request, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	a.lastBody = string(body)
	if a.transformErr != nil {
		return nil, a.transformErr
	}
	return &stubProtocolRequest{method: req.Method, body: body}, nil
}

func (a *stubAdapter) Execute(_ context.Context, req gatewayproto.Request) (gatewayproto.Response, error) {
	if a.executeErr != nil {
		return nil, a.executeErr
	}
	return &stubProtocolResponse{
		status: http.StatusCreated,
		body:   []byte("created:" + req.Method()),
	}, nil
}

func (a *stubAdapter) Encode(_ context.Context, resp gatewayproto.Response, writer gatewayproto.ResponseWriter) error {
	if a.encodeErr != nil {
		return a.encodeErr
	}
	writer.WriteHeader(resp.StatusCode())
	body, err := io.ReadAll(resp.Body())
	if err != nil {
		return err
	}
	_, err = writer.Write(body)
	return err
}

type errReadCloser struct{}

func (errReadCloser) Read(_ []byte) (int, error) { return 0, errors.New("read body failed") }
func (errReadCloser) Close() error               { return nil }

func TestMiddlewareNilRegistryPassThrough(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusAccepted)
	})

	req := httptest.NewRequest(http.MethodGet, "/passthrough", nil)
	rec := httptest.NewRecorder()
	Middleware(nil)(next).ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatalf("expected next handler to be called")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}
}

func TestMiddlewareWithConfigNilRegistryPassThrough(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Fatalf("expected passthrough body payload, got %q", string(body))
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/passthrough", bytes.NewBufferString("payload"))
	rec := httptest.NewRecorder()
	MiddlewareWithConfig(Config{})(next).ServeHTTP(rec, req)

	if !nextCalled {
		t.Fatalf("expected next handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
}

func TestMiddlewareWithConfigNilRegistryOnAdapterNotFound(t *testing.T) {
	nextCalled := false
	notFoundCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		OnAdapterNotFound: func(w http.ResponseWriter, _ *http.Request) {
			notFoundCalled = true
			w.WriteHeader(http.StatusTeapot)
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/custom-not-found", nil)
	rec := httptest.NewRecorder()
	MiddlewareWithConfig(cfg)(next).ServeHTTP(rec, req)

	if !notFoundCalled {
		t.Fatalf("expected OnAdapterNotFound to be called")
	}
	if nextCalled {
		t.Fatalf("expected next handler to be skipped when OnAdapterNotFound handles request")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected status %d, got %d", http.StatusTeapot, rec.Code)
	}
}

func TestMiddlewareWithConfigReadBodyErrorUsesOnTransformError(t *testing.T) {
	nextCalled := false
	onTransformCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{
		OnTransformError: func(w http.ResponseWriter, _ *http.Request, err error) {
			onTransformCalled = true
			if err == nil || err.Error() != "read body failed" {
				t.Fatalf("unexpected error: %v", err)
			}
			w.WriteHeader(http.StatusBadRequest)
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/read-error", nil)
	req.Body = errReadCloser{}
	rec := httptest.NewRecorder()
	MiddlewareWithConfig(cfg)(next).ServeHTTP(rec, req)

	if !onTransformCalled {
		t.Fatalf("expected OnTransformError to be called")
	}
	if nextCalled {
		t.Fatalf("expected next handler to be skipped on read error")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}

func TestMiddlewareUsesRegisteredAdapter(t *testing.T) {
	adapter := &stubAdapter{}
	registry := gatewayproto.NewRegistry()
	registry.Register(adapter)

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/proto", bytes.NewBufferString("hello"))
	rec := httptest.NewRecorder()
	Middleware(registry)(next).ServeHTTP(rec, req)

	if nextCalled {
		t.Fatalf("expected next handler to be skipped when adapter handles request")
	}
	if adapter.lastBody != "hello" {
		t.Fatalf("expected adapter body hello, got %q", adapter.lastBody)
	}
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}
	if rec.Body.String() != "created:POST" {
		t.Fatalf("expected response body created:POST, got %q", rec.Body.String())
	}
}

func TestMiddlewareReadBodyErrorUsesGatewayProtocolTransformCode(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/read-error", nil)
	req.Body = errReadCloser{}

	rec := httptest.NewRecorder()
	MiddlewareWithConfig(Config{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}

	payload := decodeProtocolError(t, rec)
	if got := payload.Error.Code; got != CodeProtocolTransformFail {
		t.Fatalf("expected code %q, got %q", CodeProtocolTransformFail, got)
	}
	assertBodyOmits(t, rec.Body.String(), "read body failed")
}

func decodeProtocolError(t *testing.T, rec *httptest.ResponseRecorder) protocolErrorResponse {
	t.Helper()
	if got := rec.Header().Get(contract.HeaderContentType); got != contract.ContentTypeJSON {
		t.Fatalf("content type = %q, want %q", got, contract.ContentTypeJSON)
	}

	var payload protocolErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	return payload
}

func TestMiddlewareTransformErrorOmitsInternalCause(t *testing.T) {
	rec := serveWithAdapter(t, &stubAdapter{transformErr: errors.New("secret transform detail")}, Middleware)

	assertProtocolError(t, rec, http.StatusBadRequest, CodeProtocolTransformFail)
	assertBodyOmits(t, rec.Body.String(), "secret transform detail")
	assertProtocolStage(t, rec, "transform")
}

func TestMiddlewareExecuteErrorOmitsInternalCause(t *testing.T) {
	rec := serveWithAdapter(t, &stubAdapter{executeErr: errors.New("secret execute detail")}, Middleware)

	assertProtocolError(t, rec, http.StatusBadGateway, CodeProtocolExecutionFail)
	assertBodyOmits(t, rec.Body.String(), "secret execute detail")
	assertProtocolStage(t, rec, "execute")
}

func TestMiddlewareEncodeErrorOmitsInternalCause(t *testing.T) {
	rec := serveWithAdapter(t, &stubAdapter{encodeErr: errors.New("secret encode detail")}, Middleware)

	assertProtocolError(t, rec, http.StatusInternalServerError, "INTERNAL_ERROR")
	assertBodyOmits(t, rec.Body.String(), "secret encode detail")
	assertProtocolStage(t, rec, "encode")
}

func serveWithAdapter(t *testing.T, adapter *stubAdapter, factory func(*gatewayproto.Registry) func(http.Handler) http.Handler) *httptest.ResponseRecorder {
	t.Helper()

	registry := gatewayproto.NewRegistry()
	registry.Register(adapter)

	req := httptest.NewRequest(http.MethodPost, "/proto", bytes.NewBufferString("payload"))
	rec := httptest.NewRecorder()
	factory(registry)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)

	return rec
}

func assertProtocolError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}

	payload := decodeProtocolError(t, rec)
	if got := payload.Error.Code; got != code {
		t.Fatalf("code = %q, want %q; body: %s", got, code, rec.Body.String())
	}
}

func assertProtocolStage(t *testing.T, rec *httptest.ResponseRecorder, stage string) {
	t.Helper()

	payload := decodeProtocolError(t, rec)
	if got, _ := payload.Error.Details["stage"].(string); got != stage {
		t.Fatalf("stage = %q, want %q; body: %s", got, stage, rec.Body.String())
	}
}

func assertBodyOmits(t *testing.T, body, value string) {
	t.Helper()
	if bytes.Contains([]byte(body), []byte(value)) {
		t.Fatalf("response body leaked %q: %s", value, body)
	}
}
