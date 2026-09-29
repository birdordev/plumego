package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spcent/plumego/x/ai/tokenizer"
)

const (
	openaiAPIBaseURL = "https://api.openai.com/v1"
)

// OpenAIProvider implements Provider for OpenAI's API.
type OpenAIProvider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	tokenizer  tokenizer.Tokenizer
}

// NewOpenAIProvider creates a new OpenAI provider. Without WithOpenAIHTTPClient
// it uses a client that bounds connection and response-header waits; the
// overall request lifetime is governed by the context passed to
// Complete/CompleteStream.
func NewOpenAIProvider(apiKey string, opts ...OpenAIOption) *OpenAIProvider {
	p := &OpenAIProvider{
		apiKey:     apiKey,
		baseURL:    openaiAPIBaseURL,
		httpClient: defaultHTTPClient(),
		tokenizer:  tokenizer.NewGPTTokenizer("gpt-4"),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// OpenAIOption configures OpenAI provider.
type OpenAIOption func(*OpenAIProvider)

// WithOpenAIBaseURL sets the base URL.
func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.baseURL = url
	}
}

// WithOpenAIHTTPClient sets the HTTP client.
func WithOpenAIHTTPClient(client *http.Client) OpenAIOption {
	return func(p *OpenAIProvider) {
		p.httpClient = client
	}
}

// Name implements Provider.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Complete implements Provider.
func (p *OpenAIProvider) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	// Build request
	openaiReq, err := p.buildRequest(req)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	// Make HTTP request
	httpReq, err := p.newHTTPRequest(ctx, "POST", "/chat/completions", openaiReq)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	// Check error
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	// Parse response
	var openaiResp openaiChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.parseResponse(&openaiResp), nil
}

// CompleteStream implements Provider.
func (p *OpenAIProvider) CompleteStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error) {
	// Build request
	openaiReq, err := p.buildRequest(req)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	openaiReq.Stream = true

	// Make HTTP request
	httpReq, err := p.newHTTPRequest(ctx, "POST", "/chat/completions", openaiReq)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	// Check error
	if resp.StatusCode != http.StatusOK {
		body := readErrorBody(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, body)
	}

	return &StreamReader{
		reader: resp.Body,
		parser: &openaiStreamParser{scanner: bufio.NewScanner(resp.Body)},
	}, nil
}

// ListModels implements Provider.
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Return static list of common models
	return []Model{
		{
			ID:              "gpt-4-turbo-preview",
			Name:            "GPT-4 Turbo",
			Provider:        "openai",
			ContextWindow:   128000,
			MaxOutputTokens: 4096,
			InputPricePerM:  10.0,
			OutputPricePerM: 30.0,
			Capabilities:    []string{"text", "tools"},
		},
		{
			ID:              "gpt-4",
			Name:            "GPT-4",
			Provider:        "openai",
			ContextWindow:   8192,
			MaxOutputTokens: 4096,
			InputPricePerM:  30.0,
			OutputPricePerM: 60.0,
			Capabilities:    []string{"text", "tools"},
		},
		{
			ID:              "gpt-3.5-turbo",
			Name:            "GPT-3.5 Turbo",
			Provider:        "openai",
			ContextWindow:   16385,
			MaxOutputTokens: 4096,
			InputPricePerM:  0.5,
			OutputPricePerM: 1.5,
			Capabilities:    []string{"text", "tools"},
		},
	}, nil
}

// GetModel implements Provider.
func (p *OpenAIProvider) GetModel(ctx context.Context, modelID string) (*Model, error) {
	models, err := p.ListModels(ctx)
	if err != nil {
		return nil, err
	}

	for _, m := range models {
		if m.ID == modelID {
			return &m, nil
		}
	}

	return nil, fmt.Errorf("model not found: %s", modelID)
}

// CountTokens implements Provider.
func (p *OpenAIProvider) CountTokens(text string) (int, error) {
	return p.tokenizer.Count(text)
}

// buildRequest builds OpenAI API request.
func (p *OpenAIProvider) buildRequest(req *CompletionRequest) (*openaiChatRequest, error) {
	openaiReq := &openaiChatRequest{
		Model:    req.Model,
		Messages: make([]openaiMessage, 0, len(req.Messages)),
		Stream:   req.Stream,
	}

	// Temperature
	if req.Temperature > 0 {
		openaiReq.Temperature = &req.Temperature
	}

	// Top P
	if req.TopP > 0 {
		openaiReq.TopP = &req.TopP
	}

	// Max tokens
	if req.MaxTokens > 0 {
		openaiReq.MaxTokens = &req.MaxTokens
	}

	// Stop sequences
	if len(req.StopSequences) > 0 {
		openaiReq.Stop = req.StopSequences
	}

	// Tools
	if len(req.Tools) > 0 {
		openaiReq.Tools = make([]openaiTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			openaiReq.Tools = append(openaiReq.Tools, openaiTool{
				Type: "function",
				Function: openaiFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			})
		}
	}

	// Messages
	for _, msg := range req.Messages {
		openaiMsg := openaiMessage{
			Role: string(msg.Role),
		}

		// Convert content
		switch v := msg.Content.(type) {
		case string:
			openaiMsg.Content = v
		default:
			// OpenAI has different format, simplify to text for now
			openaiMsg.Content = msg.GetText()
		}

		openaiReq.Messages = append(openaiReq.Messages, openaiMsg)
	}

	return openaiReq, nil
}

// parseResponse converts OpenAI response to standard format.
func (p *OpenAIProvider) parseResponse(resp *openaiChatResponse) *CompletionResponse {
	result := &CompletionResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: tokenizer.TokenUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		result.StopReason = StopReason(choice.FinishReason)

		// Convert content
		if choice.Message.Content != "" {
			result.Content = append(result.Content, ContentBlock{
				Type: ContentTypeText,
				Text: choice.Message.Content,
			})
		}

		// Tool calls
		for _, tc := range choice.Message.ToolCalls {
			var input map[string]any
			json.Unmarshal([]byte(tc.Function.Arguments), &input)

			result.Content = append(result.Content, ContentBlock{
				Type: ContentTypeToolUse,
				ToolUse: &ToolUse{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: input,
				},
			})
		}
	}

	return result
}

// newHTTPRequest creates a new HTTP request.
func (p *OpenAIProvider) newHTTPRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	return req, nil
}

// OpenAI API types

type openaiChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Tools       []openaiTool    `json:"tools,omitempty"`
}

type openaiMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiTool struct {
	Type     string         `json:"type"`
	Function openaiFunction `json:"function"`
}

type openaiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openaiToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openaiChatResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   openaiUsage    `json:"usage"`
}

type openaiChoice struct {
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// openaiStreamParser parses OpenAI streaming responses.
type openaiStreamParser struct {
	scanner *bufio.Scanner
}

// Parse implements StreamParser.
func (p *openaiStreamParser) Parse() (*StreamChunk, error) {
	for p.scanner.Scan() {
		line := p.scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// Parse SSE data
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			// Check for [DONE]
			if data == "[DONE]" {
				return nil, io.EOF
			}

			return p.parseChunk(data)
		}
	}

	if err := p.scanner.Err(); err != nil {
		return nil, err
	}

	return nil, io.EOF
}

// parseChunk parses a streaming chunk.
func (p *openaiStreamParser) parseChunk(data string) (*StreamChunk, error) {
	var streamResp struct {
		ID      string `json:"id"`
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}

	if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
		return nil, err
	}

	chunk := &StreamChunk{
		Type: "content_block_delta",
	}

	if len(streamResp.Choices) > 0 {
		choice := streamResp.Choices[0]

		if choice.Delta.Content != "" {
			chunk.Delta = &ContentDelta{
				Type: ContentTypeText,
				Text: choice.Delta.Content,
			}
		}

		if choice.FinishReason != "" {
			chunk.StopReason = StopReason(choice.FinishReason)
		}
	}

	return chunk, nil
}
