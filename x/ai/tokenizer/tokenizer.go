// Package tokenizer provides token counting for AI models.
package tokenizer

import (
	"sync/atomic"
	"unicode/utf8"
)

// Tokenizer is the interface for counting tokens.
type Tokenizer interface {
	// Count returns the number of tokens in the text.
	Count(text string) (int, error)

	// CountMessages returns the total tokens in a message list.
	CountMessages(messages []Message) (int, error)

	// ModelName returns the model this tokenizer is for.
	ModelName() string
}

// Message represents a chat message.
type Message struct {
	Role    string // system, user, assistant, tool
	Content string
	Name    string // optional
}

// TokenUsage tracks token consumption.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// Add adds token counts.
func (t *TokenUsage) Add(other TokenUsage) {
	t.InputTokens += other.InputTokens
	t.OutputTokens += other.OutputTokens
	t.TotalTokens += other.TotalTokens
}

// StreamCounter estimates token usage from accumulated streaming response text.
type StreamCounter struct {
	tokenizer   Tokenizer
	inputCount  atomic.Int64
	outputCount atomic.Int64
	chunkBuffer string
	chunkSize   int
}

// NewStreamCounter creates a new stream counter.
func NewStreamCounter(tokenizer Tokenizer) *StreamCounter {
	return &StreamCounter{
		tokenizer: tokenizer,
		chunkSize: 100, // Minimum cumulative byte length for a streaming estimate.
	}
}

// SetInputTokens sets the input token count.
func (s *StreamCounter) SetInputTokens(count int) {
	s.inputCount.Store(int64(count))
}

// OnChunk appends a chunk and recounts the complete text once it reaches the
// byte threshold. Call OnChunk and OnComplete sequentially.
func (s *StreamCounter) OnChunk(chunk string) {
	s.chunkBuffer += chunk

	if len(s.chunkBuffer) >= s.chunkSize {
		if count, err := s.tokenizer.Count(s.chunkBuffer); err == nil {
			s.outputCount.Store(int64(count))
		}
	}
}

// OnComplete counts the complete accumulated text, including text below the
// streaming threshold.
func (s *StreamCounter) OnComplete() {
	if s.chunkBuffer != "" {
		if count, err := s.tokenizer.Count(s.chunkBuffer); err == nil {
			s.outputCount.Store(int64(count))
		}
	}
}

// Usage returns the current token usage.
func (s *StreamCounter) Usage() TokenUsage {
	input := s.inputCount.Load()
	output := s.outputCount.Load()
	return TokenUsage{
		InputTokens:  int(input),
		OutputTokens: int(output),
		TotalTokens:  int(input + output),
	}
}

// SimpleTokenizer provides a basic token estimation based on character count.
// This is fast but approximate. Use model-specific tokenizers for accuracy.
type SimpleTokenizer struct {
	model           string
	charsPerToken   float64
	messageOverhead int
}

// NewSimpleTokenizer creates a simple tokenizer.
func NewSimpleTokenizer(model string) *SimpleTokenizer {
	return &SimpleTokenizer{
		model:           model,
		charsPerToken:   4.0, // Rough average for English
		messageOverhead: 4,   // Overhead per message
	}
}

// Count implements Tokenizer.
func (t *SimpleTokenizer) Count(text string) (int, error) {
	// Count by characters and UTF-8 runes
	charCount := len(text)
	runeCount := utf8.RuneCountInString(text)

	// Use the smaller of char/4 or rune count as estimate
	estimate := float64(charCount) / t.charsPerToken
	if float64(runeCount) < estimate {
		estimate = float64(runeCount)
	}

	// Round up
	tokens := int(estimate + 0.5)
	if tokens < 1 && text != "" {
		tokens = 1
	}

	return tokens, nil
}

// CountMessages implements Tokenizer.
func (t *SimpleTokenizer) CountMessages(messages []Message) (int, error) {
	total := 0

	for _, msg := range messages {
		// Count content
		contentTokens, err := t.Count(msg.Content)
		if err != nil {
			return 0, err
		}
		total += contentTokens

		// Add overhead for message structure
		total += t.messageOverhead

		// Add tokens for role and name
		if msg.Role != "" {
			roleTokens, _ := t.Count(msg.Role)
			total += roleTokens
		}
		if msg.Name != "" {
			nameTokens, _ := t.Count(msg.Name)
			total += nameTokens
		}
	}

	return total, nil
}

// ModelName implements Tokenizer.
func (t *SimpleTokenizer) ModelName() string {
	return t.model
}

// ClaudeTokenizer provides token counting for Claude models.
type ClaudeTokenizer struct {
	model           string
	charsPerToken   float64
	messageOverhead int
}

// NewClaudeTokenizer creates a Claude tokenizer.
func NewClaudeTokenizer(model string) *ClaudeTokenizer {
	return &ClaudeTokenizer{
		model:           model,
		charsPerToken:   3.5, // Claude's average
		messageOverhead: 5,
	}
}

// Count implements Tokenizer.
func (t *ClaudeTokenizer) Count(text string) (int, error) {
	if text == "" {
		return 0, nil
	}

	// Count UTF-8 runes for better accuracy with Unicode
	runeCount := utf8.RuneCountInString(text)
	charCount := len(text)

	// Use weighted average
	estimate := (float64(charCount)/t.charsPerToken + float64(runeCount)) / 2.0

	tokens := int(estimate + 0.5)
	if tokens < 1 {
		tokens = 1
	}

	return tokens, nil
}

// CountMessages implements Tokenizer.
func (t *ClaudeTokenizer) CountMessages(messages []Message) (int, error) {
	total := 0

	for _, msg := range messages {
		// Count content
		contentTokens, err := t.Count(msg.Content)
		if err != nil {
			return 0, err
		}
		total += contentTokens

		// Add message overhead
		total += t.messageOverhead

		// Role tokens
		if msg.Role != "" {
			total += 1 // Role is typically 1 token
		}
	}

	return total, nil
}

// ModelName implements Tokenizer.
func (t *ClaudeTokenizer) ModelName() string {
	return t.model
}

// GPTTokenizer provides token counting for GPT models.
type GPTTokenizer struct {
	model           string
	charsPerToken   float64
	messageOverhead int
}

// NewGPTTokenizer creates a GPT tokenizer.
func NewGPTTokenizer(model string) *GPTTokenizer {
	return &GPTTokenizer{
		model:           model,
		charsPerToken:   4.0, // GPT average
		messageOverhead: 3,
	}
}

// Count implements Tokenizer.
func (t *GPTTokenizer) Count(text string) (int, error) {
	if text == "" {
		return 0, nil
	}

	charCount := len(text)
	estimate := float64(charCount) / t.charsPerToken

	tokens := int(estimate + 0.5)
	if tokens < 1 {
		tokens = 1
	}

	return tokens, nil
}

// CountMessages implements Tokenizer.
func (t *GPTTokenizer) CountMessages(messages []Message) (int, error) {
	total := t.messageOverhead // Base overhead

	for _, msg := range messages {
		// Count content
		contentTokens, err := t.Count(msg.Content)
		if err != nil {
			return 0, err
		}
		total += contentTokens

		// Per-message overhead
		total += 4 // <|start|>role<|message|>content<|end|>

		// Role and name
		if msg.Role != "" {
			total += 1
		}
		if msg.Name != "" {
			nameTokens, _ := t.Count(msg.Name)
			total += nameTokens + 1 // name + delimiter
		}
	}

	// Add reply priming
	total += 3 // <|start|>assistant<|message|>

	return total, nil
}

// ModelName implements Tokenizer.
func (t *GPTTokenizer) ModelName() string {
	return t.model
}

// GetTokenizer returns the appropriate tokenizer for a model.
func GetTokenizer(model string) Tokenizer {
	// Detect model type from name
	if isClaudeModel(model) {
		return NewClaudeTokenizer(model)
	} else if isGPTModel(model) {
		return NewGPTTokenizer(model)
	}

	// Default to simple tokenizer
	return NewSimpleTokenizer(model)
}

// isClaudeModel checks if a model is a Claude model.
func isClaudeModel(model string) bool {
	// Check for common Claude model patterns
	return len(model) >= 6 && model[:6] == "claude"
}

// isGPTModel checks if a model is a GPT model.
func isGPTModel(model string) bool {
	// Check for common GPT model patterns
	if len(model) >= 3 && model[:3] == "gpt" {
		return true
	}
	if len(model) >= 7 && model[:7] == "text-da" { // text-davinci
		return true
	}
	return false
}
