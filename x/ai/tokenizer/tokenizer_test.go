package tokenizer

import (
	"testing"
)

func TestSimpleTokenizer_Count(t *testing.T) {
	tokenizer := NewSimpleTokenizer("test-model")

	tests := []struct {
		name    string
		text    string
		wantMin int
		wantMax int
	}{
		{
			name:    "empty",
			text:    "",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "short text",
			text:    "hello",
			wantMin: 1,
			wantMax: 5,
		},
		{
			name:    "sentence",
			text:    "The quick brown fox jumps over the lazy dog",
			wantMin: 8,
			wantMax: 15,
		},
		{
			name:    "unicode",
			text:    "你好世界",
			wantMin: 2,
			wantMax: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := tokenizer.Count(tt.text)
			if err != nil {
				t.Errorf("Count() error = %v", err)
				return
			}
			if count < tt.wantMin || count > tt.wantMax {
				t.Errorf("Count() = %v, want between %v and %v", count, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestClaudeTokenizer_Count(t *testing.T) {
	tokenizer := NewClaudeTokenizer("claude-3-opus-20240229")

	tests := []struct {
		name    string
		text    string
		wantMin int
		wantMax int
	}{
		{
			name:    "empty",
			text:    "",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "short text",
			text:    "hello",
			wantMin: 1,
			wantMax: 5,
		},
		{
			name:    "sentence",
			text:    "The quick brown fox jumps over the lazy dog",
			wantMin: 10,
			wantMax: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := tokenizer.Count(tt.text)
			if err != nil {
				t.Errorf("Count() error = %v", err)
				return
			}
			if count < tt.wantMin || count > tt.wantMax {
				t.Errorf("Count() = %v, want between %v and %v", count, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestGPTTokenizer_Count(t *testing.T) {
	tokenizer := NewGPTTokenizer("gpt-4")

	tests := []struct {
		name    string
		text    string
		wantMin int
		wantMax int
	}{
		{
			name:    "empty",
			text:    "",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "short text",
			text:    "hello",
			wantMin: 1,
			wantMax: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := tokenizer.Count(tt.text)
			if err != nil {
				t.Errorf("Count() error = %v", err)
				return
			}
			if count < tt.wantMin || count > tt.wantMax {
				t.Errorf("Count() = %v, want between %v and %v", count, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestTokenizer_CountMessages(t *testing.T) {
	tokenizer := NewClaudeTokenizer("claude-3-opus-20240229")

	messages := []Message{
		{Role: "user", Content: "Hello, how are you?"},
		{Role: "assistant", Content: "I'm doing well, thank you!"},
		{Role: "user", Content: "What's the weather like?"},
	}

	count, err := tokenizer.CountMessages(messages)
	if err != nil {
		t.Errorf("CountMessages() error = %v", err)
		return
	}

	// Should be at least the sum of content tokens plus overhead
	if count < 10 {
		t.Errorf("CountMessages() = %v, want at least 10", count)
	}
}

func TestStreamCounter(t *testing.T) {
	tokenizer := NewSimpleTokenizer("test-model")
	counter := NewStreamCounter(tokenizer)

	// Set input tokens
	counter.SetInputTokens(10)

	// Simulate streaming chunks
	chunks := []string{
		"Hello ",
		"world, ",
		"this is ",
		"a test.",
	}

	for _, chunk := range chunks {
		counter.OnChunk(chunk)
	}

	counter.OnComplete()

	usage := counter.Usage()

	if usage.InputTokens != 10 {
		t.Errorf("InputTokens = %v, want 10", usage.InputTokens)
	}

	if usage.OutputTokens < 1 {
		t.Errorf("OutputTokens = %v, want > 0", usage.OutputTokens)
	}

	if usage.TotalTokens != usage.InputTokens+usage.OutputTokens {
		t.Errorf("TotalTokens = %v, want %v", usage.TotalTokens, usage.InputTokens+usage.OutputTokens)
	}
}

func TestTokenUsage_Add(t *testing.T) {
	usage1 := TokenUsage{
		InputTokens:  10,
		OutputTokens: 20,
		TotalTokens:  30,
	}

	usage2 := TokenUsage{
		InputTokens:  5,
		OutputTokens: 15,
		TotalTokens:  20,
	}

	usage1.Add(usage2)

	if usage1.InputTokens != 15 {
		t.Errorf("InputTokens = %v, want 15", usage1.InputTokens)
	}
	if usage1.OutputTokens != 35 {
		t.Errorf("OutputTokens = %v, want 35", usage1.OutputTokens)
	}
	if usage1.TotalTokens != 50 {
		t.Errorf("TotalTokens = %v, want 50", usage1.TotalTokens)
	}
}

func TestGetTokenizer(t *testing.T) {
	tests := []struct {
		model    string
		wantType string
	}{
		{
			model:    "claude-3-opus-20240229",
			wantType: "*tokenizer.ClaudeTokenizer",
		},
		{
			model:    "gpt-4",
			wantType: "*tokenizer.GPTTokenizer",
		},
		{
			model:    "gpt-3.5-turbo",
			wantType: "*tokenizer.GPTTokenizer",
		},
		{
			model:    "unknown-model",
			wantType: "*tokenizer.SimpleTokenizer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			tokenizer := GetTokenizer(tt.model)
			if tokenizer == nil {
				t.Error("GetTokenizer() returned nil")
				return
			}

			// Check model name
			if tokenizer.ModelName() != tt.model {
				t.Errorf("ModelName() = %v, want %v", tokenizer.ModelName(), tt.model)
			}
		})
	}
}

func TestIsClaudeModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-3-opus-20240229", true},
		{"claude-2.1", true},
		{"gpt-4", false},
		{"gemini-pro", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isClaudeModel(tt.model); got != tt.want {
				t.Errorf("isClaudeModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsGPTModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"gpt-4", true},
		{"gpt-3.5-turbo", true},
		{"text-davinci-003", true},
		{"claude-3-opus", false},
		{"gemini-pro", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isGPTModel(tt.model); got != tt.want {
				t.Errorf("isGPTModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func BenchmarkSimpleTokenizer_Count(b *testing.B) {
	tokenizer := NewSimpleTokenizer("test-model")
	text := "The quick brown fox jumps over the lazy dog. " +
		"This is a longer text to benchmark token counting performance."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tokenizer.Count(text)
	}
}

func BenchmarkClaudeTokenizer_Count(b *testing.B) {
	tokenizer := NewClaudeTokenizer("claude-3-opus-20240229")
	text := "The quick brown fox jumps over the lazy dog. " +
		"This is a longer text to benchmark token counting performance."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tokenizer.Count(text)
	}
}

func BenchmarkStreamCounter(b *testing.B) {
	tokenizer := NewSimpleTokenizer("test-model")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		counter := NewStreamCounter(tokenizer)
		counter.SetInputTokens(10)
		counter.OnChunk("Hello world")
		counter.OnComplete()
		_ = counter.Usage()
	}
}

func TestSimpleAndGPTCountMessages(t *testing.T) {
	messages := []Message{
		{Role: "user", Name: "name", Content: "12345678"},
		{},
	}
	for _, tc := range []struct {
		name         string
		tokenizer    Tokenizer
		empty, multi int
	}{
		{"simple", NewSimpleTokenizer("fixture"), 0, 12},
		{"gpt", NewGPTTokenizer("fixture"), 6, 19},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := tc.tokenizer.CountMessages(nil); got != tc.empty || err != nil {
				t.Errorf("empty CountMessages = %d, %v; want %d", got, err, tc.empty)
			}
			if got, err := tc.tokenizer.CountMessages(messages); got != tc.multi || err != nil {
				t.Errorf("CountMessages = %d, %v; want %d", got, err, tc.multi)
			}
		})
	}
}
