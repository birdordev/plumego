// Package provider provides a unified interface for LLM providers.
//
// Stability: stable — this package follows semantic versioning guarantees.
// Breaking changes require a major version bump.
package provider

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/birdordev/plumego/x/ai/tokenizer"
)

// defaultHTTPClient returns the HTTP client used when the caller does not
// supply one via the provider's WithXxxHTTPClient option. It bounds connection
// setup and the wait for response headers, but deliberately sets no total
// Timeout: completion requests and SSE streams can legitimately run for
// minutes, so the overall request lifetime is governed by the caller's
// context. Callers needing a hard cap should pass their own client.
func defaultHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
		},
	}
}

// Provider is the unified interface for LLM providers.
type Provider interface {
	// Name returns the provider name (e.g., "claude", "openai").
	Name() string

	// Complete sends a completion request.
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)

	// CompleteStream sends a streaming completion request.
	CompleteStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error)

	// ListModels returns available models.
	ListModels(ctx context.Context) ([]Model, error)

	// GetModel returns model information.
	GetModel(ctx context.Context, modelID string) (*Model, error)

	// CountTokens counts tokens in text.
	CountTokens(text string) (int, error)
}

// CompletionRequest represents a completion request.
type CompletionRequest struct {
	// Model ID (required)
	Model string

	// Messages (required for chat models)
	Messages []Message

	// System prompt (optional, some providers use this separately)
	System string

	// Tools available for the model to call
	Tools []Tool

	// Tool choice strategy
	ToolChoice ToolChoice

	// Maximum tokens to generate
	MaxTokens int

	// Temperature (0.0 to 1.0)
	Temperature float64

	// Top P sampling
	TopP float64

	// Top K sampling
	TopK int

	// Stop sequences
	StopSequences []string

	// Stream response
	Stream bool

	// Metadata for tracking
	Metadata map[string]string
}

// CompletionResponse represents a completion response.
type CompletionResponse struct {
	ID         string               `json:"id"`
	Model      string               `json:"model"`
	Role       Role                 `json:"role,omitempty"`
	Content    []ContentBlock       `json:"content"`
	StopReason StopReason           `json:"stop_reason"`
	Usage      tokenizer.TokenUsage `json:"usage"`
	Metadata   map[string]any       `json:"metadata,omitempty"`
}

// Message represents a chat message.
type Message struct {
	Role       Role   `json:"role"`
	Content    any    `json:"content"` // string or []ContentBlock
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ContentBlock represents a piece of content.
type ContentBlock struct {
	// Type: text, image, tool_use, tool_result
	Type ContentType

	// Text content
	Text string

	// Tool use
	ToolUse *ToolUse

	// Tool result
	ToolResult *ToolResult

	// Image (base64 or URL)
	Image *Image
}

// ToolUse represents a tool call from the model.
type ToolUse struct {
	// Tool call ID
	ID string

	// Tool name
	Name string

	// Tool input (JSON)
	Input map[string]any
}

// ToolResult represents the result of a tool call.
type ToolResult struct {
	// Tool call ID
	ToolUseID string

	// Result content
	Content string

	// Whether the tool call failed
	IsError bool
}

// Image represents an image input.
type Image struct {
	// Source type: base64, url
	Type string

	// Image data or URL
	Data string

	// Media type (e.g., "image/png")
	MediaType string
}

// Tool represents a function the model can call.
type Tool struct {
	// Type (currently only "function")
	Type string

	// Function definition
	Function FunctionDef
}

// FunctionDef defines a callable function.
type FunctionDef struct {
	// Function name
	Name string

	// Description
	Description string

	// Parameters schema (JSON Schema)
	Parameters map[string]any

	// Required parameters
	Required []string
}

// ToolChoice specifies how the model should use tools.
type ToolChoice struct {
	// Type: auto, any, tool, none
	Type string

	// Specific tool name (when Type is "tool")
	Name string
}

// Model represents model information.
type Model struct {
	ID              string
	Name            string
	Provider        string
	ContextWindow   int
	MaxOutputTokens int
	InputPricePerM  float64 // Price per 1M input tokens
	OutputPricePerM float64 // Price per 1M output tokens
	Capabilities    []string
}

// StreamReader reads streaming responses.
type StreamReader struct {
	reader io.ReadCloser
	parser StreamParser
	err    error
}

// Next returns the next chunk.
func (s *StreamReader) Next() (*StreamChunk, error) {
	if s.err != nil {
		return nil, s.err
	}

	chunk, err := s.parser.Parse()
	if err != nil {
		s.err = err
		return nil, err
	}

	return chunk, nil
}

// Close closes the stream.
func (s *StreamReader) Close() error {
	if s.reader != nil {
		return s.reader.Close()
	}
	return nil
}

// StreamChunk represents a streaming chunk.
type StreamChunk struct {
	// Chunk type: content_block_start, content_block_delta, content_block_stop, message_delta, message_stop
	Type string

	// Content delta
	Delta *ContentDelta

	// Token usage (only in final chunk)
	Usage *tokenizer.TokenUsage

	// Stop reason (only in final chunk)
	StopReason StopReason
}

// ContentDelta represents incremental content.
type ContentDelta struct {
	// Type: text, tool_use
	Type ContentType

	// Text delta
	Text string

	// Tool use delta
	ToolUse *ToolUse
}

// StreamParser parses streaming responses.
type StreamParser interface {
	Parse() (*StreamChunk, error)
}

// Role represents message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentType represents content type.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeImage      ContentType = "image"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
)

// StopReason represents why generation stopped.
type StopReason string

const (
	StopReasonEndTurn      StopReason = "end_turn"
	StopReasonMaxTokens    StopReason = "max_tokens"
	StopReasonStopSequence StopReason = "stop_sequence"
	StopReasonToolUse      StopReason = "tool_use"
	StopReasonError        StopReason = "error"
)

// AutoToolChoice lets the model decide whether to use tools.
func AutoToolChoice() ToolChoice {
	return ToolChoice{Type: "auto"}
}

// NoneToolChoice prevents the model from using tools.
func NoneToolChoice() ToolChoice {
	return ToolChoice{Type: "none"}
}

// AnyToolChoice requires the model to use a tool.
func AnyToolChoice() ToolChoice {
	return ToolChoice{Type: "any"}
}

// ToolChoiceAuto lets the model decide whether to use tools.
// Compatibility: retained for the stable provider API. Prefer AutoToolChoice
// for a fresh value that cannot be affected by package variable mutation.
var ToolChoiceAuto = ToolChoice{Type: "auto"}

// ToolChoiceNone prevents the model from using tools.
// Compatibility: retained for the stable provider API. Prefer NoneToolChoice
// for a fresh value that cannot be affected by package variable mutation.
var ToolChoiceNone = ToolChoice{Type: "none"}

// ToolChoiceAny requires the model to use a tool.
// Compatibility: retained for the stable provider API. Prefer AnyToolChoice
// for a fresh value that cannot be affected by package variable mutation.
var ToolChoiceAny = ToolChoice{Type: "any"}

// ToolChoiceTool requires the model to use a specific tool.
func ToolChoiceTool(name string) ToolChoice {
	return ToolChoice{Type: "tool", Name: name}
}

// NewTextMessage creates a text message.
func NewTextMessage(role Role, text string) Message {
	return Message{
		Role:    role,
		Content: text,
	}
}

// NewToolResultMessage creates a tool result message.
func NewToolResultMessage(toolUseID string, content string, isError bool) Message {
	return Message{
		Role: RoleTool,
		Content: []ContentBlock{
			{
				Type: ContentTypeToolResult,
				ToolResult: &ToolResult{
					ToolUseID: toolUseID,
					Content:   content,
					IsError:   isError,
				},
			},
		},
		ToolCallID: toolUseID,
	}
}

// GetText returns the text content from a message.
func (m *Message) GetText() string {
	switch v := m.Content.(type) {
	case string:
		return v
	case []ContentBlock:
		for _, block := range v {
			if block.Type == ContentTypeText {
				return block.Text
			}
		}
	}
	return ""
}

// GetToolUses returns all tool uses from content blocks.
func (r *CompletionResponse) GetToolUses() []*ToolUse {
	var toolUses []*ToolUse
	for _, block := range r.Content {
		if block.Type == ContentTypeToolUse && block.ToolUse != nil {
			toolUses = append(toolUses, block.ToolUse)
		}
	}
	return toolUses
}

// GetText returns all text content concatenated.
func (r *CompletionResponse) GetText() string {
	var text string
	for _, block := range r.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}

// HasToolUse checks if the response contains tool uses.
func (r *CompletionResponse) HasToolUse() bool {
	return r.StopReason == StopReasonToolUse && len(r.GetToolUses()) > 0
}
