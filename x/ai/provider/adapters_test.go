package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testClaudeCompletionResponse struct {
	ID         string                   `json:"id"`
	Type       string                   `json:"type"`
	Role       string                   `json:"role"`
	Model      string                   `json:"model"`
	StopReason string                   `json:"stop_reason"`
	Content    []testClaudeContentBlock `json:"content"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type testClaudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type testOpenAICompletionResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Choices []testOpenAIChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type testOpenAIChoice struct {
	Index        int               `json:"index"`
	FinishReason string            `json:"finish_reason"`
	Message      testOpenAIMessage `json:"message"`
}

type testOpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// --- ClaudeProvider ---

func TestNewClaudeProvider_Defaults(t *testing.T) {
	p := NewClaudeProvider("key-123")
	if p.Name() != "claude" {
		t.Errorf("Name() = %q, want claude", p.Name())
	}
	if p.apiKey != "key-123" {
		t.Errorf("apiKey = %q, want key-123", p.apiKey)
	}
	if p.baseURL != claudeAPIBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, claudeAPIBaseURL)
	}
	if p.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

func TestWithClaudeBaseURL(t *testing.T) {
	p := NewClaudeProvider("k", WithClaudeBaseURL("http://custom:1234"))
	if p.baseURL != "http://custom:1234" {
		t.Errorf("baseURL = %q, want http://custom:1234", p.baseURL)
	}
}

func TestWithClaudeHTTPClient(t *testing.T) {
	custom := &http.Client{}
	p := NewClaudeProvider("k", WithClaudeHTTPClient(custom))
	if p.httpClient != custom {
		t.Error("httpClient was not replaced by WithClaudeHTTPClient")
	}
}

func TestClaudeProvider_Complete_OK(t *testing.T) {
	resp := testClaudeCompletionResponse{
		ID:         "msg-1",
		Type:       "message",
		Role:       "assistant",
		Model:      "claude-3-opus",
		StopReason: "end_turn",
		Content: []testClaudeContentBlock{
			{Type: "text", Text: "Hello from Claude"},
		},
	}
	resp.Usage.InputTokens = 10
	resp.Usage.OutputTokens = 5
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") == "" {
			t.Error("missing x-api-key header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewClaudeProvider("test-key", WithClaudeBaseURL(srv.URL))
	result, err := p.Complete(t.Context(), &CompletionRequest{
		Model:     "claude-3-opus",
		Messages:  []Message{NewTextMessage(RoleUser, "hi")},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.GetText() != "Hello from Claude" {
		t.Errorf("text = %q, want 'Hello from Claude'", result.GetText())
	}
}

func TestClaudeProvider_Complete_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer srv.Close()

	p := NewClaudeProvider("bad-key", WithClaudeBaseURL(srv.URL))
	_, err := p.Complete(t.Context(), &CompletionRequest{
		Model:    "claude-3",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Error("expected error for 401 response")
	}
}

func TestClaudeProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = (*ClaudeProvider)(nil)
}

// --- OpenAIProvider ---

func TestNewOpenAIProvider_Defaults(t *testing.T) {
	p := NewOpenAIProvider("sk-123")
	if p.Name() != "openai" {
		t.Errorf("Name() = %q, want openai", p.Name())
	}
	if p.apiKey != "sk-123" {
		t.Errorf("apiKey = %q, want sk-123", p.apiKey)
	}
	if p.baseURL != openaiAPIBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, openaiAPIBaseURL)
	}
	if p.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}

func TestWithOpenAIBaseURL(t *testing.T) {
	p := NewOpenAIProvider("k", WithOpenAIBaseURL("http://custom:5678"))
	if p.baseURL != "http://custom:5678" {
		t.Errorf("baseURL = %q, want http://custom:5678", p.baseURL)
	}
}

func TestWithOpenAIHTTPClient(t *testing.T) {
	custom := &http.Client{}
	p := NewOpenAIProvider("k", WithOpenAIHTTPClient(custom))
	if p.httpClient != custom {
		t.Error("httpClient was not replaced by WithOpenAIHTTPClient")
	}
}

func TestOpenAIProvider_Complete_OK(t *testing.T) {
	resp := testOpenAICompletionResponse{
		ID:    "chatcmpl-1",
		Model: "gpt-4o",
		Choices: []testOpenAIChoice{
			{
				Index:        0,
				FinishReason: "stop",
				Message: testOpenAIMessage{
					Role:    "assistant",
					Content: "Hello from OpenAI",
				},
			},
		},
	}
	resp.Usage.PromptTokens = 10
	resp.Usage.CompletionTokens = 5
	resp.Usage.TotalTokens = 15
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("sk-test", WithOpenAIBaseURL(srv.URL))
	result, err := p.Complete(t.Context(), &CompletionRequest{
		Model:     "gpt-4o",
		Messages:  []Message{NewTextMessage(RoleUser, "hi")},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.GetText() != "Hello from OpenAI" {
		t.Errorf("text = %q, want 'Hello from OpenAI'", result.GetText())
	}
}

func TestOpenAIProvider_Complete_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	p := NewOpenAIProvider("sk-bad", WithOpenAIBaseURL(srv.URL))
	_, err := p.Complete(t.Context(), &CompletionRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Error("expected error for 429 response")
	}
}

func TestOpenAIProvider_ImplementsProvider(t *testing.T) {
	var _ Provider = (*OpenAIProvider)(nil)
}

type adapterStreamTransport func(*http.Request) (*http.Response, error)

func (f adapterStreamTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type adapterStreamBody struct {
	io.Reader
	closes int
}

func (b *adapterStreamBody) Close() error { b.closes++; return nil }

func TestClaudeCompleteStreamOwnsResponseLifecycle(t *testing.T) {
	payload := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"fixture\"}}\n\n"
	for _, tc := range []struct {
		name    string
		status  int
		content string
	}{
		{"stream", http.StatusOK, payload},
		{"response error", http.StatusServiceUnavailable, "fixture unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &adapterStreamBody{Reader: strings.NewReader(tc.content)}
			calls := 0
			client := &http.Client{Transport: adapterStreamTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				var data struct{ Stream bool }
				if err := json.NewDecoder(r.Body).Decode(&data); err != nil || !data.Stream || r.Method != http.MethodPost {
					t.Errorf("stream request=%+v, %v", data, err)
				}
				_ = r.Body.Close()
				return &http.Response{StatusCode: tc.status, Body: body}, nil
			})}
			p := NewClaudeProvider("", WithClaudeHTTPClient(client))
			stream, err := p.CompleteStream(t.Context(), &CompletionRequest{Model: "fixture", Messages: []Message{NewTextMessage(RoleUser, "prompt")}})
			if tc.status != http.StatusOK {
				if stream != nil || err == nil || body.closes != 1 || calls != 1 {
					t.Errorf("failure=%v, %v, closes=%d calls=%d", stream, err, body.closes, calls)
				}
				return
			}
			if err != nil || stream == nil {
				t.Fatalf("CompleteStream=%v, %v", stream, err)
			}
			if body.closes != 0 {
				t.Error("response closed before reader received it")
			}
			if got, err := stream.Next(); err != nil || got == nil || got.Delta == nil || got.Delta.Text != "fixture" {
				t.Errorf("Next=%+v, %v", got, err)
			}
			if _, err := stream.Next(); !errors.Is(err, io.EOF) {
				t.Errorf("end of stream=%v", err)
			}
			if err := stream.Close(); err != nil || body.closes != 1 || calls != 1 {
				t.Errorf("Close=%v, closes=%d calls=%d", err, body.closes, calls)
			}
		})
	}
}

func TestOpenAICompleteStreamOwnsResponse(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			payload := "data: {\"choices\":[{\"delta\":{\"content\":\"fixture\"}}]}\n\ndata: [DONE]\n\ndata: ignored\n"
			body := &adapterStreamBody{Reader: strings.NewReader(payload)}
			client := &http.Client{Transport: adapterStreamTransport(func(r *http.Request) (*http.Response, error) {
				defer r.Body.Close()
				var request struct{ Stream bool }
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || !request.Stream {
					t.Errorf("stream request = %+v, %v", request, err)
				}
				return &http.Response{StatusCode: status, Body: body}, nil
			})}
			adapter := NewOpenAIProvider("", WithOpenAIHTTPClient(client))
			stream, err := adapter.CompleteStream(t.Context(), &CompletionRequest{
				Model: "fixture", Messages: []Message{NewTextMessage(RoleUser, "prompt")},
			})
			if status != http.StatusOK {
				if stream != nil || err == nil || body.closes != 1 {
					t.Fatalf("stream = %v, err = %v, closes = %d", stream, err, body.closes)
				}
				return
			}
			if err != nil || stream == nil {
				t.Fatalf("CompleteStream = %v, %v", stream, err)
			}
			if chunk, err := stream.Next(); err != nil || chunk == nil || chunk.Delta == nil || chunk.Delta.Text != "fixture" {
				t.Errorf("Next = %+v, %v", chunk, err)
			}
			if _, err := stream.Next(); !errors.Is(err, io.EOF) || body.closes != 0 {
				t.Errorf("terminal event = %v, closes = %d; want EOF and open body", err, body.closes)
			}
			if err := stream.Close(); err != nil || body.closes != 1 {
				t.Errorf("Close = %v, closes = %d", err, body.closes)
			}
		})
	}
}
