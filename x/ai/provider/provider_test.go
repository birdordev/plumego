package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDefaultHTTPClient_BoundsHandshakeButNotStream(t *testing.T) {
	client := defaultHTTPClient()
	if client.Timeout != 0 {
		t.Errorf("defaultHTTPClient() total Timeout = %v, want 0 so SSE streams stay ctx-driven", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("defaultHTTPClient() Transport = %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout <= 0 {
		t.Error("defaultHTTPClient() ResponseHeaderTimeout must be positive to fail hung peers")
	}
	if transport.TLSHandshakeTimeout <= 0 {
		t.Error("defaultHTTPClient() TLSHandshakeTimeout must be positive")
	}
}

func TestDefaultProviders_ShareTimeoutContract(t *testing.T) {
	openai := NewOpenAIProvider("key")
	claude := NewClaudeProvider("key")
	if openai.httpClient == nil || claude.httpClient == nil {
		t.Fatal("providers must default to a non-nil client")
	}
	if openai.httpClient.Timeout != 0 || claude.httpClient.Timeout != 0 {
		t.Error("default clients must not set a total Timeout (would kill SSE streams)")
	}
}

func TestProvider_ResponseHeaderTimeoutFailsFast(t *testing.T) {
	// A server that delays headers beyond the client's ResponseHeaderTimeout
	// must fail the request fast instead of blocking forever. The handler
	// delay is bounded so httptest.Server.Close cannot hang. Uses a custom
	// client with a tiny timeout to exercise the same mechanism the default
	// client configures with 30s.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"late","choices":[]}`))
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond},
	}
	p := NewOpenAIProvider("key", WithOpenAIBaseURL(srv.URL), WithOpenAIHTTPClient(client))

	start := time.Now()
	_, err := p.Complete(context.Background(), &CompletionRequest{
		Model:    "gpt-4",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Complete() expected timeout error from slow server")
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("Complete() error = %v, want a network timeout", err)
	}
	if elapsed >= 300*time.Millisecond {
		t.Errorf("Complete() took %v, want failure before the server's 300ms header delay", elapsed)
	}
}

func TestNewTextMessage(t *testing.T) {
	msg := NewTextMessage(RoleUser, "Hello")

	if msg.Role != RoleUser {
		t.Errorf("Role = %v, want %v", msg.Role, RoleUser)
	}

	text := msg.GetText()
	if text != "Hello" {
		t.Errorf("GetText() = %v, want Hello", text)
	}
}

func TestNewToolResultMessage(t *testing.T) {
	msg := NewToolResultMessage("tool-123", "result", false)

	if msg.Role != RoleTool {
		t.Errorf("Role = %v, want %v", msg.Role, RoleTool)
	}

	if msg.ToolCallID != "tool-123" {
		t.Errorf("ToolCallID = %v, want tool-123", msg.ToolCallID)
	}

	blocks, ok := msg.Content.([]ContentBlock)
	if !ok || len(blocks) == 0 {
		t.Fatal("Content should be []ContentBlock with at least one block")
	}

	if blocks[0].Type != ContentTypeToolResult {
		t.Errorf("ContentBlock type = %v, want %v", blocks[0].Type, ContentTypeToolResult)
	}
}

func TestCompletionResponse_GetText(t *testing.T) {
	resp := &CompletionResponse{
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "Hello "},
			{Type: ContentTypeText, Text: "World"},
			{Type: ContentTypeToolUse, ToolUse: &ToolUse{Name: "test"}},
		},
	}

	text := resp.GetText()
	want := "Hello World"
	if text != want {
		t.Errorf("GetText() = %v, want %v", text, want)
	}
}

func TestCompletionResponse_GetToolUses(t *testing.T) {
	resp := &CompletionResponse{
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "Hello"},
			{
				Type: ContentTypeToolUse,
				ToolUse: &ToolUse{
					ID:   "tool-1",
					Name: "test_tool",
					Input: map[string]any{
						"arg": "value",
					},
				},
			},
			{
				Type: ContentTypeToolUse,
				ToolUse: &ToolUse{
					ID:   "tool-2",
					Name: "another_tool",
				},
			},
		},
	}

	toolUses := resp.GetToolUses()
	if len(toolUses) != 2 {
		t.Errorf("GetToolUses() count = %v, want 2", len(toolUses))
	}

	if toolUses[0].ID != "tool-1" {
		t.Errorf("First tool ID = %v, want tool-1", toolUses[0].ID)
	}

	if toolUses[1].ID != "tool-2" {
		t.Errorf("Second tool ID = %v, want tool-2", toolUses[1].ID)
	}
}

func TestCompletionResponse_HasToolUse(t *testing.T) {
	tests := []struct {
		name     string
		resp     *CompletionResponse
		expected bool
	}{
		{
			name: "has tool use",
			resp: &CompletionResponse{
				StopReason: StopReasonToolUse,
				Content: []ContentBlock{
					{
						Type:    ContentTypeToolUse,
						ToolUse: &ToolUse{ID: "tool-1"},
					},
				},
			},
			expected: true,
		},
		{
			name: "no tool use - wrong stop reason",
			resp: &CompletionResponse{
				StopReason: StopReasonEndTurn,
				Content: []ContentBlock{
					{
						Type:    ContentTypeToolUse,
						ToolUse: &ToolUse{ID: "tool-1"},
					},
				},
			},
			expected: false,
		},
		{
			name: "no tool use - no tool blocks",
			resp: &CompletionResponse{
				StopReason: StopReasonToolUse,
				Content: []ContentBlock{
					{Type: ContentTypeText, Text: "hello"},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.resp.HasToolUse(); got != tt.expected {
				t.Errorf("HasToolUse() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestToolChoice(t *testing.T) {
	if got := AutoToolChoice(); got.Type != "auto" || got.Name != "" {
		t.Errorf("AutoToolChoice() = %+v, want {Type:auto}", got)
	}

	if got := NoneToolChoice(); got.Type != "none" || got.Name != "" {
		t.Errorf("NoneToolChoice() = %+v, want {Type:none}", got)
	}

	if got := AnyToolChoice(); got.Type != "any" || got.Name != "" {
		t.Errorf("AnyToolChoice() = %+v, want {Type:any}", got)
	}

	toolChoice := ToolChoiceTool("my_tool")
	if toolChoice.Type != "tool" || toolChoice.Name != "my_tool" {
		t.Errorf("ToolChoiceTool() = %+v, want {Type:tool Name:my_tool}", toolChoice)
	}
}

func TestToolChoiceHelpersReturnFreshValues(t *testing.T) {
	auto := AutoToolChoice()
	auto.Type = "mutated"

	if got := AutoToolChoice(); got.Type != "auto" {
		t.Fatalf("AutoToolChoice() after local mutation = %+v, want fresh auto value", got)
	}
}

func TestManager_Register(t *testing.T) {
	t.Run("nil provider", func(t *testing.T) {
		manager := NewManager()
		err := manager.Register(nil)
		if !errors.Is(err, ErrProviderRequired) {
			t.Fatalf("Register() error = %v, want ErrProviderRequired", err)
		}
	})

	t.Run("empty provider name", func(t *testing.T) {
		manager := NewManager()
		err := manager.Register(&mockProvider{})
		if !errors.Is(err, ErrProviderNameRequired) {
			t.Fatalf("Register() error = %v, want ErrProviderNameRequired", err)
		}
	})

	t.Run("valid provider", func(t *testing.T) {
		manager := NewManager()
		mockProvider := &mockProvider{name: "mock"}

		if err := manager.Register(mockProvider); err != nil {
			t.Fatalf("Register() error = %v", err)
		}

		provider, err := manager.Get("mock")
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if provider != mockProvider {
			t.Fatal("expected registered provider")
		}
	})
}

func TestManager_Get_NotFound(t *testing.T) {
	manager := NewManager()

	_, err := manager.Get("nonexistent")
	if err == nil {
		t.Error("Get() should return error for nonexistent provider")
	}
}

func TestMockProvider_QueueReuseAndCalls(t *testing.T) {
	mock := NewMockProvider("mock")
	mock.QueueResponse(&CompletionResponse{
		Model: "mock-model",
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "queued response"},
		},
	})

	req := &CompletionRequest{Model: "mock-model"}
	first, err := mock.Complete(t.Context(), req)
	if err != nil {
		t.Fatalf("Complete() first call error = %v", err)
	}

	second, err := mock.Complete(t.Context(), req)
	if err != nil {
		t.Fatalf("Complete() second call error = %v", err)
	}

	if first.GetText() != "queued response" || second.GetText() != "queued response" {
		t.Fatalf("queued response should be reused, got %q and %q", first.GetText(), second.GetText())
	}

	if mock.CallCount() != 2 {
		t.Fatalf("CallCount() = %d, want 2", mock.CallCount())
	}

	if len(mock.Calls()) != 2 {
		t.Fatalf("Calls() count = %d, want 2", len(mock.Calls()))
	}
}

func TestMockProvider_ErrorAndStreaming(t *testing.T) {
	mock := NewMockProvider("mock")
	mock.SetError(io.ErrUnexpectedEOF)

	if _, err := mock.Complete(t.Context(), &CompletionRequest{Model: "mock-model"}); err == nil {
		t.Fatal("Complete() should return configured error")
	}
	if _, err := mock.CompleteStream(t.Context(), &CompletionRequest{Model: "mock-model"}); err == nil {
		t.Fatal("CompleteStream() should return configured error")
	}

	mock.SetError(nil)
	mock.SetStreamChunks([]*StreamChunk{
		{
			Type: "content_block_delta",
			Delta: &ContentDelta{
				Type: ContentTypeText,
				Text: "chunk-1",
			},
		},
	})

	stream, err := mock.CompleteStream(t.Context(), &CompletionRequest{Model: "mock-model"})
	if err != nil {
		t.Fatalf("CompleteStream() error = %v", err)
	}

	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if chunk.Delta == nil || chunk.Delta.Text != "chunk-1" {
		t.Fatalf("chunk text = %v, want chunk-1", chunk.Delta)
	}

	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("Next() after final chunk = %v, want io.EOF", err)
	}
}

func TestDefaultRouter_Route(t *testing.T) {
	router := &DefaultRouter{}

	mockClaude := &mockProvider{name: "claude"}
	mockOpenAI := &mockProvider{name: "openai"}

	providers := []Provider{mockClaude, mockOpenAI}

	tests := []struct {
		model    string
		expected string
	}{
		{"claude-3-opus", "claude"},
		{"gpt-4", "openai"},
		{"unknown-model", "claude"}, // fallback to first
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			req := &CompletionRequest{Model: tt.model}
			provider, err := router.Route(t.Context(), req, providers)
			if err != nil {
				t.Errorf("Route() error = %v", err)
			}

			if provider.Name() != tt.expected {
				t.Errorf("Route() provider = %v, want %v", provider.Name(), tt.expected)
			}
		})
	}
}

func TestLoadBalancerRouter_Route(t *testing.T) {
	router := &LoadBalancerRouter{}

	mock1 := &mockProvider{name: "provider1"}
	mock2 := &mockProvider{name: "provider2"}
	providers := []Provider{mock1, mock2}

	req := &CompletionRequest{Model: "test"}

	// First call should go to provider1
	provider1, err := router.Route(t.Context(), req, providers)
	if err != nil {
		t.Errorf("Route() error = %v", err)
	}

	// Second call should go to provider2
	provider2, err := router.Route(t.Context(), req, providers)
	if err != nil {
		t.Errorf("Route() error = %v", err)
	}

	if provider1.Name() == provider2.Name() {
		t.Error("LoadBalancerRouter should alternate between providers")
	}
}

func TestMatchesProvider(t *testing.T) {
	tests := []struct {
		model    string
		provider string
		expected bool
	}{
		{"claude-3-opus", "claude", true},
		{"claude-2.1", "claude", true},
		{"gpt-4", "openai", true},
		{"gpt-3.5-turbo", "openai", true},
		{"claude-3-opus", "openai", false},
		{"gpt-4", "claude", false},
	}

	for _, tt := range tests {
		t.Run(tt.model+"_"+tt.provider, func(t *testing.T) {
			if got := matchesProvider(tt.model, tt.provider); got != tt.expected {
				t.Errorf("matchesProvider(%v, %v) = %v, want %v", tt.model, tt.provider, got, tt.expected)
			}
		})
	}
}

// Mock provider for testing
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string {
	return m.name
}

func (m *mockProvider) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	return &CompletionResponse{
		ID:    "test-id",
		Model: req.Model,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "mock response"},
		},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error) {
	return nil, nil
}

func (m *mockProvider) ListModels(ctx context.Context) ([]Model, error) {
	return []Model{
		{ID: "mock-model", Name: "Mock Model", Provider: m.name},
	}, nil
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*Model, error) {
	return &Model{ID: modelID, Name: "Mock Model", Provider: m.name}, nil
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text) / 4, nil
}

func TestTaskBasedRouter_InferTaskType(t *testing.T) {
	router := NewTaskBasedRouter()

	tests := []struct {
		name     string
		prompt   string
		expected TaskType
	}{
		{
			name:     "coding task",
			prompt:   "Write a function to sort an array",
			expected: TaskTypeCoding,
		},
		{
			name:     "implement task",
			prompt:   "Implement a REST API endpoint",
			expected: TaskTypeCoding,
		},
		{
			name:     "analysis task",
			prompt:   "Analyze this data and find patterns",
			expected: TaskTypeAnalysis,
		},
		{
			name:     "summarization task",
			prompt:   "Summarize this document for me",
			expected: TaskTypeSummarization,
		},
		{
			name:     "translation task",
			prompt:   "Translate this text to Spanish",
			expected: TaskTypeTranslation,
		},
		{
			name:     "conversation task",
			prompt:   "Hello, how are you today?",
			expected: TaskTypeConversation,
		},
		{
			name:     "default to conversation",
			prompt:   "What is the capital of France?",
			expected: TaskTypeConversation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &CompletionRequest{
				Messages: []Message{
					NewTextMessage(RoleUser, tt.prompt),
				},
			}
			taskType := router.inferTaskType(req)
			if taskType != tt.expected {
				t.Errorf("inferTaskType() = %v, want %v", taskType, tt.expected)
			}
		})
	}
}

func TestTaskBasedRouter_Route(t *testing.T) {
	router := NewTaskBasedRouter()

	claudeProvider := &mockProvider{name: "claude"}
	openaiProvider := &mockProvider{name: "openai"}
	providers := []Provider{claudeProvider, openaiProvider}

	tests := []struct {
		name     string
		prompt   string
		expected string
	}{
		{
			name:     "coding routes to claude",
			prompt:   "Write a function in Go",
			expected: "claude",
		},
		{
			name:     "analysis routes to claude",
			prompt:   "Analyze this codebase structure",
			expected: "claude",
		},
		{
			name:     "conversation routes to claude",
			prompt:   "Hello, tell me about yourself",
			expected: "claude", // TaskTypeConversation prefers claude-3-sonnet
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &CompletionRequest{
				Messages: []Message{
					NewTextMessage(RoleUser, tt.prompt),
				},
			}
			provider, err := router.Route(t.Context(), req, providers)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if provider.Name() != tt.expected {
				t.Errorf("Route() provider = %v, want %v", provider.Name(), tt.expected)
			}
		})
	}
}

func TestCostOptimizedRouter_Route(t *testing.T) {
	router := NewCostOptimizedRouter()

	claudeProvider := &mockProvider{name: "claude"}
	openaiProvider := &mockProvider{name: "openai"}
	providers := []Provider{claudeProvider, openaiProvider}

	tests := []struct {
		name     string
		model    string
		expected string
	}{
		{
			name:     "gpt-3.5-turbo routes to openai",
			model:    "gpt-3.5-turbo",
			expected: "openai",
		},
		{
			name:     "gpt-4 routes to openai",
			model:    "gpt-4",
			expected: "openai",
		},
		{
			name:     "claude-3-opus routes to claude",
			model:    "claude-3-opus",
			expected: "claude",
		},
		{
			name:     "unknown model falls back to first provider",
			model:    "unknown-model",
			expected: "claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &CompletionRequest{
				Model:    tt.model,
				Messages: []Message{NewTextMessage(RoleUser, "test")},
			}

			provider, err := router.Route(t.Context(), req, providers)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}

			if provider.Name() != tt.expected {
				t.Errorf("Route() = %v, want %v", provider.Name(), tt.expected)
			}
		})
	}
}

func TestFallbackRouter_Route(t *testing.T) {
	primary := NewTaskBasedRouter()
	fallback := &DefaultRouter{}
	router := NewFallbackRouter(primary, fallback)

	claudeProvider := &mockProvider{name: "claude"}
	openaiProvider := &mockProvider{name: "openai"}
	providers := []Provider{claudeProvider, openaiProvider}

	tests := []struct {
		name     string
		prompt   string
		wantName string
	}{
		{
			name:     "primary router works",
			prompt:   "Write code in Python",
			wantName: "claude",
		},
		{
			name:     "fallback on empty providers",
			prompt:   "Any task",
			wantName: "claude", // DefaultRouter falls back to first
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &CompletionRequest{
				Messages: []Message{
					NewTextMessage(RoleUser, tt.prompt),
				},
			}
			provider, err := router.Route(t.Context(), req, providers)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if provider.Name() != tt.wantName {
				t.Errorf("Route() provider = %v, want %v", provider.Name(), tt.wantName)
			}
		})
	}
}

func TestSmartRouter_Route(t *testing.T) {
	// Test with default strategies
	router := NewSmartRouter()

	claudeProvider := &mockProvider{name: "claude"}
	openaiProvider := &mockProvider{name: "openai"}
	providers := []Provider{claudeProvider, openaiProvider}

	tests := []struct {
		name     string
		prompt   string
		wantName string
	}{
		{
			name:     "coding task routed by task type",
			prompt:   "Debug this function",
			wantName: "claude",
		},
		{
			name:     "conversation routed to claude",
			prompt:   "Tell me a story",
			wantName: "claude", // TaskTypeConversation prefers claude-3-sonnet
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &CompletionRequest{
				Messages: []Message{
					NewTextMessage(RoleUser, tt.prompt),
				},
			}
			provider, err := router.Route(t.Context(), req, providers)
			if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
			if provider.Name() != tt.wantName {
				t.Errorf("Route() provider = %v, want %v", provider.Name(), tt.wantName)
			}
		})
	}
}

func TestSmartRouter_CustomStrategies(t *testing.T) {
	// Test with custom strategy order
	router := NewSmartRouter(
		NewCostOptimizedRouter(),
		NewTaskBasedRouter(),
	)

	claudeProvider := &mockProvider{name: "claude"}
	openaiProvider := &mockProvider{name: "openai"}
	providers := []Provider{claudeProvider, openaiProvider}

	req := &CompletionRequest{
		Model: "gpt-3.5-turbo", // Specify model for CostOptimizedRouter
		Messages: []Message{
			NewTextMessage(RoleUser, "Short question"),
		},
	}

	provider, err := router.Route(t.Context(), req, providers)
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	// Should use first strategy (cost optimized) and route gpt-3.5-turbo to openai
	if provider.Name() != "openai" {
		t.Errorf("Route() with custom strategies = %v, want openai (cost optimized)", provider.Name())
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("contains", func(t *testing.T) {
		if !contains("hello world", "world") {
			t.Error("contains() should find 'world' in 'hello world'")
		}
		if contains("hello world", "universe") {
			t.Error("contains() should not find 'universe' in 'hello world'")
		}
		if !contains("HELLO", "hello") {
			t.Error("contains() should be case-insensitive")
		}
	})

	t.Run("findSubstring", func(t *testing.T) {
		if !findSubstring("hello world", "world") {
			t.Error("findSubstring() should find 'world'")
		}
		if findSubstring("hello world", "universe") {
			t.Error("findSubstring() should not find 'universe'")
		}
		if !findSubstring("HELLO World", "hello") {
			t.Error("findSubstring() should be case-insensitive")
		}
	})

	t.Run("toLower", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"HELLO", "hello"},
			{"World", "world"},
			{"TEST123", "test123"},
			{"mixedCASE", "mixedcase"},
		}
		for _, tt := range tests {
			result := toLower(tt.input)
			if result != tt.expected {
				t.Errorf("toLower(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		}
	})

	t.Run("min", func(t *testing.T) {
		if min(5, 3) != 3 {
			t.Errorf("min(5, 3) = %d, want 3", min(5, 3))
		}
		if min(2, 10) != 2 {
			t.Errorf("min(2, 10) = %d, want 2", min(2, 10))
		}
		if min(7, 7) != 7 {
			t.Errorf("min(7, 7) = %d, want 7", min(7, 7))
		}
	})
}

// --- Manager routing and delegation ---

func TestManager_Route_NoProviders(t *testing.T) {
	mgr := NewManager()
	_, err := mgr.Route(t.Context(), &CompletionRequest{Model: "test"})
	if err == nil {
		t.Error("expected error when no providers registered")
	}
}

func TestManager_Complete_Success(t *testing.T) {
	mock := NewMockProvider("mock")
	mock.QueueResponse(&CompletionResponse{
		Content: []ContentBlock{{Type: ContentTypeText, Text: "delegated"}},
	})
	mgr := NewManager()
	if err := mgr.Register(mock); err != nil {
		t.Fatal(err)
	}

	resp, err := mgr.Complete(t.Context(), &CompletionRequest{Model: "test"})
	if err != nil {
		t.Fatalf("Complete error: %v", err)
	}
	if resp.GetText() != "delegated" {
		t.Errorf("text = %q, want delegated", resp.GetText())
	}
}

func TestManager_Complete_RouteError(t *testing.T) {
	mgr := NewManager() // no providers
	_, err := mgr.Complete(t.Context(), &CompletionRequest{Model: "test"})
	if err == nil {
		t.Error("expected route error when no providers registered")
	}
}

func TestManager_CompleteStream_Success(t *testing.T) {
	mock := NewMockProvider("mock")
	mock.SetStreamChunks([]*StreamChunk{
		{Type: "content_block_delta", Delta: &ContentDelta{Type: ContentTypeText, Text: "streamed"}},
	})
	mgr := NewManager()
	if err := mgr.Register(mock); err != nil {
		t.Fatal(err)
	}

	stream, err := mgr.CompleteStream(t.Context(), &CompletionRequest{Model: "test"})
	if err != nil {
		t.Fatalf("CompleteStream error: %v", err)
	}
	chunk, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error: %v", err)
	}
	if chunk.Delta == nil || chunk.Delta.Text != "streamed" {
		t.Errorf("chunk text = %v, want streamed", chunk.Delta)
	}
}

func TestManager_CompleteStream_RouteError(t *testing.T) {
	mgr := NewManager() // no providers
	_, err := mgr.CompleteStream(t.Context(), &CompletionRequest{Model: "test"})
	if err == nil {
		t.Error("expected route error when no providers registered")
	}
}

func TestManager_WithRouter_UsesCustomRouter(t *testing.T) {
	chosen := &mockProvider{name: "chosen"}
	other := &mockProvider{name: "other"}

	// Custom router always picks the provider named "chosen".
	customRouter := &singleProviderRouter{name: "chosen"}

	mgr := NewManager(WithRouter(customRouter))
	if err := mgr.Register(chosen); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(other); err != nil {
		t.Fatal(err)
	}

	p, err := mgr.Route(t.Context(), &CompletionRequest{})
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if p.Name() != "chosen" {
		t.Errorf("provider = %q, want chosen", p.Name())
	}
}

// singleProviderRouter picks the provider with the given name.
type singleProviderRouter struct{ name string }

func (r *singleProviderRouter) Route(_ context.Context, _ *CompletionRequest, providers []Provider) (Provider, error) {
	for _, p := range providers {
		if p.Name() == r.name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("provider %q not found", r.name)
}
