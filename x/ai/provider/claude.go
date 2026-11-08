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

	"github.com/birdordev/plumego/x/ai/tokenizer"
)

const (
	claudeAPIBaseURL = "https://api.anthropic.com/v1"
	claudeAPIVersion = "2023-06-01"
)

// ClaudeProvider implements Provider for Anthropic's Claude API.
type ClaudeProvider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	tokenizer  tokenizer.Tokenizer
}

// NewClaudeProvider creates a new Claude provider. Without WithClaudeHTTPClient
// it uses a client that bounds connection and response-header waits; the
// overall request lifetime is governed by the context passed to
// Complete/CompleteStream.
func NewClaudeProvider(apiKey string, opts ...ClaudeOption) *ClaudeProvider {
	p := &ClaudeProvider{
		apiKey:     apiKey,
		baseURL:    claudeAPIBaseURL,
		httpClient: defaultHTTPClient(),
		tokenizer:  tokenizer.NewClaudeTokenizer("claude-3"),
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// ClaudeOption configures Claude provider.
type ClaudeOption func(*ClaudeProvider)

// WithClaudeBaseURL sets the base URL.
func WithClaudeBaseURL(url string) ClaudeOption {
	return func(p *ClaudeProvider) {
		p.baseURL = url
	}
}

// WithClaudeHTTPClient sets the HTTP client.
func WithClaudeHTTPClient(client *http.Client) ClaudeOption {
	return func(p *ClaudeProvider) {
		p.httpClient = client
	}
}

// Name implements Provider.
func (p *ClaudeProvider) Name() string {
	return "claude"
}

// Complete implements Provider.
func (p *ClaudeProvider) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	// Build request
	claudeReq, err := p.buildRequest(req)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	// Make HTTP request
	httpReq, err := p.newHTTPRequest(ctx, "POST", "/messages", claudeReq)
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check error
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, readErrorBody(resp.Body))
	}

	// Parse response
	var claudeResp claudeMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&claudeResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return p.parseResponse(&claudeResp), nil
}

// CompleteStream implements Provider.
func (p *ClaudeProvider) CompleteStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error) {
	// Build request
	claudeReq, err := p.buildRequest(req)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	claudeReq.Stream = true

	// Make HTTP request
	httpReq, err := p.newHTTPRequest(ctx, "POST", "/messages", claudeReq)
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
		_ = resp.Body.Close()
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, body)
	}

	return &StreamReader{
		reader: resp.Body,
		parser: &claudeStreamParser{scanner: bufio.NewScanner(resp.Body)},
	}, nil
}

// ListModels implements Provider.
func (p *ClaudeProvider) ListModels(ctx context.Context) ([]Model, error) {
	// Claude doesn't have a list models endpoint
	// Return static list
	return []Model{
		{
			ID:              "claude-3-opus-20240229",
			Name:            "Claude 3 Opus",
			Provider:        "claude",
			ContextWindow:   200000,
			MaxOutputTokens: 4096,
			InputPricePerM:  15.0,
			OutputPricePerM: 75.0,
			Capabilities:    []string{"text", "image", "tools"},
		},
		{
			ID:              "claude-3-sonnet-20240229",
			Name:            "Claude 3 Sonnet",
			Provider:        "claude",
			ContextWindow:   200000,
			MaxOutputTokens: 4096,
			InputPricePerM:  3.0,
			OutputPricePerM: 15.0,
			Capabilities:    []string{"text", "image", "tools"},
		},
		{
			ID:              "claude-3-haiku-20240307",
			Name:            "Claude 3 Haiku",
			Provider:        "claude",
			ContextWindow:   200000,
			MaxOutputTokens: 4096,
			InputPricePerM:  0.25,
			OutputPricePerM: 1.25,
			Capabilities:    []string{"text", "image", "tools"},
		},
	}, nil
}

// GetModel implements Provider.
func (p *ClaudeProvider) GetModel(ctx context.Context, modelID string) (*Model, error) {
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
func (p *ClaudeProvider) CountTokens(text string) (int, error) {
	return p.tokenizer.Count(text)
}

// buildRequest builds Claude API request.
func (p *ClaudeProvider) buildRequest(req *CompletionRequest) (*claudeMessageRequest, error) {
	claudeReq := &claudeMessageRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		Messages:  make([]claudeMessage, 0, len(req.Messages)),
		System:    req.System,
		Stream:    req.Stream,
	}

	// Set defaults
	if claudeReq.MaxTokens == 0 {
		claudeReq.MaxTokens = 4096
	}

	// Temperature
	if req.Temperature > 0 {
		claudeReq.Temperature = &req.Temperature
	}

	// Top P
	if req.TopP > 0 {
		claudeReq.TopP = &req.TopP
	}

	// Top K
	if req.TopK > 0 {
		claudeReq.TopK = &req.TopK
	}

	// Stop sequences
	if len(req.StopSequences) > 0 {
		claudeReq.StopSequences = req.StopSequences
	}

	// Tools
	if len(req.Tools) > 0 {
		claudeReq.Tools = make([]claudeTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			claudeReq.Tools = append(claudeReq.Tools, claudeTool{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				InputSchema: t.Function.Parameters,
			})
		}
	}

	// Messages
	for _, msg := range req.Messages {
		claudeMsg := claudeMessage{
			Role: string(msg.Role),
		}

		// Convert content
		switch v := msg.Content.(type) {
		case string:
			claudeMsg.Content = v
		case []ContentBlock:
			blocks := make([]map[string]any, 0, len(v))
			for _, block := range v {
				blocks = append(blocks, p.contentBlockToMap(block))
			}
			claudeMsg.Content = blocks
		}

		claudeReq.Messages = append(claudeReq.Messages, claudeMsg)
	}

	return claudeReq, nil
}

// contentBlockToMap converts ContentBlock to map for JSON.
func (p *ClaudeProvider) contentBlockToMap(block ContentBlock) map[string]any {
	m := map[string]any{
		"type": block.Type,
	}

	switch block.Type {
	case ContentTypeText:
		m["text"] = block.Text
	case ContentTypeImage:
		if block.Image != nil {
			m["source"] = map[string]any{
				"type":       block.Image.Type,
				"data":       block.Image.Data,
				"media_type": block.Image.MediaType,
			}
		}
	case ContentTypeToolResult:
		if block.ToolResult != nil {
			m["tool_use_id"] = block.ToolResult.ToolUseID
			m["content"] = block.ToolResult.Content
			if block.ToolResult.IsError {
				m["is_error"] = true
			}
		}
	}

	return m
}

// parseResponse converts Claude response to standard format.
func (p *ClaudeProvider) parseResponse(resp *claudeMessageResponse) *CompletionResponse {
	result := &CompletionResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		StopReason: StopReason(resp.StopReason),
		Usage: tokenizer.TokenUsage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
			TotalTokens:  resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
		Content: make([]ContentBlock, 0, len(resp.Content)),
	}

	for _, block := range resp.Content {
		result.Content = append(result.Content, p.parseContentBlock(block))
	}

	return result
}

// parseContentBlock converts Claude content block.
func (p *ClaudeProvider) parseContentBlock(block map[string]any) ContentBlock {
	blockType, _ := block["type"].(string)

	switch blockType {
	case "text":
		text, _ := block["text"].(string)
		return ContentBlock{
			Type: ContentTypeText,
			Text: text,
		}
	case "tool_use":
		id, _ := block["id"].(string)
		name, _ := block["name"].(string)
		input, _ := block["input"].(map[string]any)
		return ContentBlock{
			Type: ContentTypeToolUse,
			ToolUse: &ToolUse{
				ID:    id,
				Name:  name,
				Input: input,
			},
		}
	}

	return ContentBlock{}
}

// newHTTPRequest creates a new HTTP request.
func (p *ClaudeProvider) newHTTPRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
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
	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("anthropic-version", claudeAPIVersion)

	return req, nil
}

// Claude API types

type claudeMessageRequest struct {
	Model         string          `json:"model"`
	Messages      []claudeMessage `json:"messages"`
	System        string          `json:"system,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Tools         []claudeTool    `json:"tools,omitempty"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type claudeTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type claudeMessageResponse struct {
	ID         string           `json:"id"`
	Type       string           `json:"type"`
	Role       string           `json:"role"`
	Model      string           `json:"model"`
	Content    []map[string]any `json:"content"`
	StopReason string           `json:"stop_reason"`
	Usage      claudeUsage      `json:"usage"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// claudeStreamParser parses Claude streaming responses.
type claudeStreamParser struct {
	scanner *bufio.Scanner
}

// Parse implements StreamParser.
func (p *claudeStreamParser) Parse() (*StreamChunk, error) {
	for p.scanner.Scan() {
		line := p.scanner.Text()

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Parse SSE event
		if strings.HasPrefix(line, "event: ") {
			// Read next line for data
			if !p.scanner.Scan() {
				break
			}
			dataLine := p.scanner.Text()
			if !strings.HasPrefix(dataLine, "data: ") {
				continue
			}

			data := strings.TrimPrefix(dataLine, "data: ")
			return p.parseEvent(data)
		}
	}

	if err := p.scanner.Err(); err != nil {
		return nil, err
	}

	return nil, io.EOF
}

// parseEvent parses a streaming event.
func (p *claudeStreamParser) parseEvent(data string) (*StreamChunk, error) {
	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return nil, err
	}

	eventType, _ := event["type"].(string)

	chunk := &StreamChunk{
		Type: eventType,
	}

	switch eventType {
	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		deltaType, _ := delta["type"].(string)

		if deltaType == "text_delta" {
			text, _ := delta["text"].(string)
			chunk.Delta = &ContentDelta{
				Type: ContentTypeText,
				Text: text,
			}
		}

	case "message_delta":
		delta, _ := event["delta"].(map[string]any)
		stopReason, _ := delta["stop_reason"].(string)
		chunk.StopReason = StopReason(stopReason)

		usage, _ := event["usage"].(map[string]any)
		if outputTokens, ok := usage["output_tokens"].(float64); ok {
			chunk.Usage = &tokenizer.TokenUsage{
				OutputTokens: int(outputTokens),
			}
		}

	case "message_stop":
		// Final message
	}

	return chunk, nil
}
