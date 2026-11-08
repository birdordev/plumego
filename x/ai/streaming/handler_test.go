package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/x/ai/orchestration"
)

func TestHandler(t *testing.T) {
	t.Run("NewHandler", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		if handler == nil {
			t.Fatal("expected non-nil handler")
		}
	})

	t.Run("HandleStreamNoWorkflowID", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		req := httptest.NewRequest(http.MethodGet, "/stream", nil)
		w := httptest.NewRecorder()

		handler.HandleStream(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("HandleStreamWithWorkflowID", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		req := httptest.NewRequest(http.MethodGet, "/stream?workflow_id=test-123", nil)
		w := httptest.NewRecorder()

		handler.HandleStream(w, req)

		// Should setup SSE and start streaming
		if w.Code != 0 && w.Code != http.StatusOK {
			t.Errorf("unexpected status code: %d", w.Code)
		}

		// Check SSE headers
		contentType := w.Header().Get("Content-Type")
		if !strings.Contains(contentType, "text/event-stream") {
			t.Errorf("expected text/event-stream content type, got %s", contentType)
		}

		complete := decodeStreamingSSEData[terminalCompleteEvent](t, w.Body.String(), "complete")
		if complete.Event != "complete" || complete.Message != "Workflow execution completed" {
			t.Fatalf("unexpected complete event: %+v", complete)
		}
	})

	t.Run("HandleExecuteInvalidMethod", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		req := httptest.NewRequest(http.MethodGet, "/execute", nil)
		w := httptest.NewRecorder()

		handler.HandleExecute(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", w.Code)
		}
		assertStreamingErrorCode(t, w, contract.CodeMethodNotAllowed)
	})

	t.Run("HandleExecuteInvalidJSON", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader("invalid json"))
		w := httptest.NewRecorder()

		handler.HandleExecute(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
		assertStreamingErrorCode(t, w, contract.CodeInvalidJSON)
		if strings.Contains(w.Body.String(), "invalid character") {
			t.Fatalf("response leaked JSON decoder detail: %s", w.Body.String())
		}
	})

	t.Run("HandleExecuteNoWorkflowID", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		body := strings.NewReader(`{"name": "test-workflow"}`)
		req := httptest.NewRequest(http.MethodPost, "/execute", body)
		w := httptest.NewRecorder()

		handler.HandleExecute(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("HandleStreamSSECreateError", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		req := httptest.NewRequest(http.MethodGet, "/stream?workflow_id=test-123", nil)
		w := newStreamingNonFlusherResponseWriter()

		handler.HandleStream(w, req)

		assertStreamingNonFlusherError(t, w, http.StatusInternalServerError, contract.CodeInternalError, "failed to create SSE stream")
		if strings.Contains(w.body.String(), "streaming not supported") {
			t.Fatalf("response leaked stream creation error: %s", w.body.String())
		}
	})

	t.Run("HandleExecuteSSECreateError", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		body := strings.NewReader(`{"workflow_id": "test-123", "name": "test-workflow"}`)
		req := httptest.NewRequest(http.MethodPost, "/execute", body)
		w := newStreamingNonFlusherResponseWriter()

		handler.HandleExecute(w, req)

		assertStreamingNonFlusherError(t, w, http.StatusInternalServerError, contract.CodeInternalError, "failed to create SSE stream")
		if strings.Contains(w.body.String(), "streaming not supported") {
			t.Fatalf("response leaked stream creation error: %s", w.body.String())
		}
	})

	t.Run("HandleExecuteValid", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)
		handler := NewHandler(streamEngine)

		body := strings.NewReader(`{"workflow_id": "test-123", "name": "test-workflow"}`)
		req := httptest.NewRequest(http.MethodPost, "/execute", body)
		w := httptest.NewRecorder()

		handler.HandleExecute(w, req)

		// Should setup SSE and start execution
		output := w.Body.String()
		if output == "" {
			t.Error("expected output, got empty string")
		}
		result := decodeStreamingSSEData[terminalResultEvent](t, output, "result")
		if result.Event != "result" || !result.Success || result.ResultsCount != 0 {
			t.Fatalf("unexpected result event: %+v", result)
		}
	})
}

func TestStreamWorkflow(t *testing.T) {
	t.Run("BasicExecution", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		workflow := &orchestration.Workflow{
			Name:  "test-workflow",
			Steps: []orchestration.Step{},
		}

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		err := StreamWorkflow(w, req, workflow, "workflow-1", streamEngine)
		if err != nil {
			t.Fatalf("StreamWorkflow failed: %v", err)
		}

		output := w.Body.String()
		if output == "" {
			t.Error("expected output")
		}
	})

	t.Run("WithAgents", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		mockProvider := &mockProvider{
			output: "test output",
		}

		mockAgent := &orchestration.Agent{
			Name:     "test-agent",
			Provider: mockProvider,
		}

		workflow := &orchestration.Workflow{
			Name:  "test-workflow",
			State: make(map[string]any),
			Steps: []orchestration.Step{
				&orchestration.SequentialStep{
					StepName:  "test-step",
					Agent:     mockAgent,
					InputFn:   func(state map[string]any) string { return "test input" },
					OutputKey: "result",
				},
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		err := StreamWorkflow(w, req, workflow, "workflow-1", streamEngine)
		if err != nil {
			t.Fatalf("StreamWorkflow failed: %v", err)
		}

		output := w.Body.String()
		if !contains(output, "test-") {
			t.Error("expected test- in output")
		}
	})
}

func TestHandleWithCallback(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		callback := func(ctx context.Context) (*orchestration.Workflow, error) {
			return &orchestration.Workflow{
				Name:  "callback-workflow",
				Steps: []orchestration.Step{},
			}, nil
		}

		handler := HandleWithCallback(streamEngine, callback)

		req := httptest.NewRequest(http.MethodGet, "/?workflow_id=test-123", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		output := w.Body.String()
		if output == "" {
			t.Error("expected output")
		}
	})

	t.Run("CallbackError", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		callback := func(ctx context.Context) (*orchestration.Workflow, error) {
			return nil, fmt.Errorf("callback error")
		}

		handler := HandleWithCallback(streamEngine, callback)

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("expected 500, got %d", w.Code)
		}
		assertStreamingErrorCode(t, w, contract.CodeInternalError)
		if strings.Contains(w.Body.String(), "callback error") {
			t.Fatalf("response leaked callback error: %s", w.Body.String())
		}
	})

	t.Run("AutoGenerateWorkflowID", func(t *testing.T) {
		engine := orchestration.NewEngine()
		streamEngine := NewStreamingEngine(engine, nil)

		callback := func(ctx context.Context) (*orchestration.Workflow, error) {
			return &orchestration.Workflow{
				Name:  "test-workflow",
				Steps: []orchestration.Step{},
			}, nil
		}

		handler := HandleWithCallback(streamEngine, callback)

		// No workflow_id in query
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		handler(w, req)

		// Should succeed with auto-generated ID
		output := w.Body.String()
		if output == "" {
			t.Error("expected output with auto-generated workflow ID")
		}
	})
}

type streamingNonFlusherResponseWriter struct {
	header http.Header
	body   strings.Builder
	code   int
}

func newStreamingNonFlusherResponseWriter() *streamingNonFlusherResponseWriter {
	return &streamingNonFlusherResponseWriter{header: make(http.Header), code: http.StatusOK}
}

func (w *streamingNonFlusherResponseWriter) Header() http.Header {
	return w.header
}

func (w *streamingNonFlusherResponseWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *streamingNonFlusherResponseWriter) WriteHeader(statusCode int) {
	w.code = statusCode
}

func assertStreamingErrorCode(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()

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
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v; body: %s", err, w.Body.String())
	}
	if resp.Error.Code != code {
		t.Fatalf("code = %q, want %q; body: %s", resp.Error.Code, code, w.Body.String())
	}
}

func assertStreamingNonFlusherError(t *testing.T, w *streamingNonFlusherResponseWriter, status int, code, message string) {
	t.Helper()

	if w.code != status {
		t.Fatalf("status = %d, want %d; body: %s", w.code, status, w.body.String())
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
	if resp.Error.Code != code {
		t.Fatalf("code = %q, want %q; body: %s", resp.Error.Code, code, w.body.String())
	}
	if resp.Error.Message != message {
		t.Fatalf("message = %q, want %q; body: %s", resp.Error.Message, message, w.body.String())
	}
}

func decodeStreamingSSEData[T any](t *testing.T, body, eventName string) T {
	t.Helper()

	for _, block := range strings.Split(body, "\n\n") {
		if block == "" {
			continue
		}
		var event string
		var data string
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if event != eventName {
			continue
		}
		var payload T
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatalf("decode SSE event %q data: %v; data=%s", eventName, err, data)
		}
		return payload
	}

	t.Fatalf("SSE event %q not found in body: %s", eventName, body)
	var zero T
	return zero
}
