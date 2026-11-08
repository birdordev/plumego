package sse

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/contract"
)

func TestNewStream(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := t.Context()

	stream, err := NewStream(ctx, w)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	defer stream.Close()

	// Check headers
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %v, want text/event-stream", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %v, want no-cache", cc)
	}
}

func TestStream_Send(t *testing.T) {
	tests := []struct {
		name    string
		event   *Event
		want    string
		wantErr bool
	}{
		{
			name: "simple data",
			event: &Event{
				Data: "hello",
			},
			want: "data: hello\n\n",
		},
		{
			name: "with event type",
			event: &Event{
				Event: "message",
				Data:  "world",
			},
			want: "event: message\ndata: world\n\n",
		},
		{
			name: "with ID",
			event: &Event{
				ID:   "123",
				Data: "test",
			},
			want: "id: 123\ndata: test\n\n",
		},
		{
			name: "with retry",
			event: &Event{
				Retry: 3000,
				Data:  "retry-test",
			},
			want: "retry: 3000\ndata: retry-test\n\n",
		},
		{
			name: "full event",
			event: &Event{
				Event: "update",
				ID:    "456",
				Data:  "complete",
				Retry: 5000,
			},
			want: "id: 456\nevent: update\nretry: 5000\ndata: complete\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx := t.Context()

			stream, err := NewStream(ctx, w)
			if err != nil {
				t.Fatalf("NewStream() error = %v", err)
			}
			defer stream.Close()

			if err := stream.Send(tt.event); (err != nil) != tt.wantErr {
				t.Errorf("Send() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !strings.Contains(w.Body.String(), tt.want) {
				t.Errorf("Send() output = %v, want %v", w.Body.String(), tt.want)
			}
		})
	}
}

func TestStream_SendData(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := t.Context()

	stream, err := NewStream(ctx, w)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	defer stream.Close()

	if err := stream.SendData("test data"); err != nil {
		t.Errorf("SendData() error = %v", err)
	}

	want := "data: test data\n\n"
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("SendData() output = %v, want %v", w.Body.String(), want)
	}
}

func TestStream_SendJSON(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := t.Context()

	stream, err := NewStream(ctx, w)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	defer stream.Close()

	jsonData := `{"message":"hello"}`
	if err := stream.SendJSON("json", jsonData); err != nil {
		t.Errorf("SendJSON() error = %v", err)
	}

	output := w.Body.String()
	if !strings.Contains(output, "event: json") {
		t.Errorf("SendJSON() missing event type")
	}
	if !strings.Contains(output, jsonData) {
		t.Errorf("SendJSON() missing data")
	}
}

func TestStream_Close(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := t.Context()

	stream, err := NewStream(ctx, w)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}

	if err := stream.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Sending after close should fail
	if err := stream.SendData("test"); err == nil {
		t.Error("Send() after Close() should fail")
	}

	// Double close should not error
	if err := stream.Close(); err != nil {
		t.Errorf("Close() second time error = %v", err)
	}
}

func TestStream_ContextCancel(t *testing.T) {
	w := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(t.Context())

	stream, err := NewStream(ctx, w)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	defer stream.Close()

	// Cancel context
	cancel()

	// Wait a bit for cancellation to propagate
	time.Sleep(10 * time.Millisecond)

	// Sending should fail with context error
	if err := stream.SendData("test"); err == nil {
		t.Error("Send() after context cancel should fail")
	}
}

func TestHandle(t *testing.T) {
	handler := Handle(func(s *Stream) error {
		return s.SendData("test message")
	})

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %v, want %v", w.Code, http.StatusOK)
	}

	want := "data: test message\n\n"
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("Body = %v, want to contain %v", w.Body.String(), want)
	}
}

func TestHandle_Error(t *testing.T) {
	handler := Handle(func(s *Stream) error {
		return nil // Normal completion
	})

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %v, want %v", w.Code, http.StatusOK)
	}
}

func TestHandleStreamCreationErrorUsesSafeContractMessage(t *testing.T) {
	handler := Handle(func(s *Stream) error {
		return s.SendData("unused")
	})

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	w := newNonFlusherResponseWriter()

	handler.ServeHTTP(w, req)

	if w.code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body: %s", w.code, http.StatusInternalServerError, w.body.String())
	}

	var resp struct {
		Error struct {
			Code     string                 `json:"code"`
			Message  string                 `json:"message"`
			Category contract.ErrorCategory `json:"category"`
			Type     contract.ErrorType     `json:"type,omitempty"`
			Details  map[string]any         `json:"details,omitempty"`
		} `json:"error"`
		RequestID string `json:"request_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(w.body.String()), &resp); err != nil {
		t.Fatalf("decode error response: %v; body: %s", err, w.body.String())
	}
	if resp.Error.Code != contract.CodeInternalError {
		t.Fatalf("code = %q, want %q", resp.Error.Code, contract.CodeInternalError)
	}
	if resp.Error.Message != "failed to create SSE stream" {
		t.Fatalf("message = %q", resp.Error.Message)
	}
	if strings.Contains(w.body.String(), "streaming not supported") {
		t.Fatalf("response leaked stream creation error: %s", w.body.String())
	}
}

func TestHandleErrorEventUsesSafeData(t *testing.T) {
	handler := Handle(func(*Stream) error {
		return errors.New("secret provider detail")
	})

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Fatalf("expected error event, got %s", body)
	}
	if !strings.Contains(body, "data: stream handler failed") {
		t.Fatalf("expected safe error event data, got %s", body)
	}
	if strings.Contains(body, "secret provider detail") {
		t.Fatalf("response leaked handler error: %s", body)
	}
}

type nonFlusherResponseWriter struct {
	header http.Header
	body   strings.Builder
	code   int
}

func newNonFlusherResponseWriter() *nonFlusherResponseWriter {
	return &nonFlusherResponseWriter{header: make(http.Header), code: http.StatusOK}
}

func (w *nonFlusherResponseWriter) Header() http.Header {
	return w.header
}

func (w *nonFlusherResponseWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *nonFlusherResponseWriter) WriteHeader(statusCode int) {
	w.code = statusCode
}

func TestStream_KeepAlive(t *testing.T) {
	w := httptest.NewRecorder()
	ctx := t.Context()

	stream, err := NewStream(ctx, w)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}

	// Set short keep-alive interval
	stream.SetKeepAliveInterval(50 * time.Millisecond)

	// Wait for keep-alive
	time.Sleep(100 * time.Millisecond)

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	output := w.Body.String()
	if !strings.Contains(output, ": keep-alive") {
		t.Errorf("Keep-alive not found in output: %v", output)
	}
}
