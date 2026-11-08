package provider

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// MockProvider is a Provider implementation for use in tests.
// It records every call and returns pre-configured responses.
//
// Example:
//
//	mock := provider.NewMockProvider("mock")
//	mock.QueueResponse(&provider.CompletionResponse{
//	    Content: []provider.ContentBlock{{Type: provider.ContentTypeText, Text: "hello"}},
//	})
//
//	resp, err := mock.Complete(ctx, req)
//	fmt.Println(mock.CallCount()) // 1
type MockProvider struct {
	mu           sync.Mutex
	providerName string
	queue        []*CompletionResponse // returned in order; last entry is reused indefinitely
	streamChunks []*StreamChunk        // chunks returned by CompleteStream
	callErr      error                 // error returned by Complete/CompleteStream when set
	calls        []*CompletionRequest  // every recorded call
	models       []Model
}

// NewMockProvider creates a mock provider with the given name.
func NewMockProvider(name string) *MockProvider {
	return &MockProvider{
		providerName: name,
		models: []Model{
			{
				ID:              "mock-model",
				Name:            "Mock Model",
				Provider:        name,
				ContextWindow:   200000,
				MaxOutputTokens: 4096,
			},
		},
	}
}

// QueueResponse appends a response to the response queue.
// Responses are returned in order; the last queued response is reused once
// the queue is exhausted.
func (m *MockProvider) QueueResponse(resp *CompletionResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, resp)
}

// SetError configures the mock to return err from Complete and CompleteStream.
// Set to nil to clear.
func (m *MockProvider) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callErr = err
}

// SetStreamChunks configures the chunks returned by CompleteStream.
func (m *MockProvider) SetStreamChunks(chunks []*StreamChunk) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamChunks = chunks
}

// Calls returns all recorded CompletionRequests in call order.
func (m *MockProvider) Calls() []*CompletionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*CompletionRequest, len(m.calls))
	copy(out, m.calls)
	return out
}

// CallCount returns the number of Complete/CompleteStream calls recorded.
func (m *MockProvider) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// Reset clears the call log, response queue, stream chunks, and any configured error.
func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
	m.queue = nil
	m.streamChunks = nil
	m.callErr = nil
}

// Name implements Provider.
func (m *MockProvider) Name() string {
	return m.providerName
}

// Complete implements Provider.
func (m *MockProvider) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, req)

	if m.callErr != nil {
		return nil, m.callErr
	}

	return m.nextResponse(), nil
}

// CompleteStream implements Provider.
func (m *MockProvider) CompleteStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, req)

	if m.callErr != nil {
		return nil, m.callErr
	}

	chunks := make([]*StreamChunk, len(m.streamChunks))
	copy(chunks, m.streamChunks)

	return &StreamReader{
		reader: io.NopCloser(nil),
		parser: &mockStreamParser{chunks: chunks},
	}, nil
}

// ListModels implements Provider.
func (m *MockProvider) ListModels(_ context.Context) ([]Model, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Model, len(m.models))
	copy(out, m.models)
	return out, nil
}

// GetModel implements Provider.
func (m *MockProvider) GetModel(_ context.Context, modelID string) (*Model, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mod := range m.models {
		if mod.ID == modelID {
			cp := mod
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("mock: model not found: %s", modelID)
}

// CountTokens implements Provider using a simple whitespace estimate.
func (m *MockProvider) CountTokens(text string) (int, error) {
	return tokenizer.NewSimpleTokenizer("mock").Count(text)
}

// nextResponse pops the head of the queue; if empty returns an empty response.
// Caller must hold m.mu.
func (m *MockProvider) nextResponse() *CompletionResponse {
	if len(m.queue) == 0 {
		return &CompletionResponse{
			ID:    "mock-resp",
			Model: "mock-model",
		}
	}
	resp := m.queue[0]
	if len(m.queue) > 1 {
		m.queue = m.queue[1:]
	}
	return resp
}

// mockStreamParser returns pre-configured chunks in order.
type mockStreamParser struct {
	chunks []*StreamChunk
	index  int
}

func (p *mockStreamParser) Parse() (*StreamChunk, error) {
	if p.index >= len(p.chunks) {
		return nil, io.EOF
	}
	chunk := p.chunks[p.index]
	p.index++
	return chunk, nil
}
