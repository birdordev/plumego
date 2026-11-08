package streaming

import (
	"bytes"
	"context"
	"fmt"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// mockProvider is a mock provider for testing
type mockProvider struct {
	output string
	err    error
}

func (m *mockProvider) Complete(ctx context.Context, req *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &provider.CompletionResponse{
		Content: []provider.ContentBlock{
			{Type: provider.ContentTypeText, Text: m.output},
		},
		Usage: tokenizer.TokenUsage{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	}, nil
}

func (m *mockProvider) CompleteStream(ctx context.Context, req *provider.CompletionRequest) (*provider.StreamReader, error) {
	return nil, fmt.Errorf("streaming not supported in mock")
}

func (m *mockProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return nil, fmt.Errorf("list models not supported in mock")
}

func (m *mockProvider) GetModel(ctx context.Context, modelID string) (*provider.Model, error) {
	return nil, fmt.Errorf("get model not supported in mock")
}

func (m *mockProvider) CountTokens(text string) (int, error) {
	return len(text), nil
}

func (m *mockProvider) Name() string {
	return "mock-provider"
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
