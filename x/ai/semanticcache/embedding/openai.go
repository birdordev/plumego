package embedding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenAIProvider generates embeddings using OpenAI's API.
type OpenAIProvider struct {
	apiKey     string
	model      string
	dimensions int
	baseURL    string
	httpClient *http.Client
	maxRetries int
}

// OpenAIConfig holds OpenAI provider configuration.
type OpenAIConfig struct {
	APIKey     string
	Model      string // text-embedding-3-small, text-embedding-3-large, text-embedding-ada-002
	Dimensions int    // Optional: for text-embedding-3-* models
	Timeout    time.Duration
	MaxRetries int
	BaseURL    string
}

// NewOpenAIProvider creates a new OpenAI embedding provider.
func NewOpenAIProvider(config OpenAIConfig) (*OpenAIProvider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	if config.Model == "" {
		config.Model = "text-embedding-3-small"
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}

	// Set default dimensions based on model
	dimensions := config.Dimensions
	if dimensions == 0 {
		switch config.Model {
		case "text-embedding-3-small":
			dimensions = 1536
		case "text-embedding-3-large":
			dimensions = 3072
		case "text-embedding-ada-002":
			dimensions = 1536
		default:
			dimensions = 1536
		}
	}

	return &OpenAIProvider{
		apiKey:     config.APIKey,
		model:      config.Model,
		dimensions: dimensions,
		baseURL:    config.BaseURL,
		maxRetries: config.MaxRetries,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

// Name returns the provider name.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Model returns the model name.
func (p *OpenAIProvider) Model() string {
	return p.model
}

// Dimensions returns the embedding dimension count.
func (p *OpenAIProvider) Dimensions() int {
	return p.dimensions
}

// CostPerToken returns the cost per token.
func (p *OpenAIProvider) CostPerToken() float64 {
	// Pricing as of 2024 (per million tokens)
	switch p.model {
	case "text-embedding-3-small":
		return 0.00002 / 1000000 // $0.02 per 1M tokens
	case "text-embedding-3-large":
		return 0.00013 / 1000000 // $0.13 per 1M tokens
	case "text-embedding-ada-002":
		return 0.0001 / 1000000 // $0.10 per 1M tokens
	default:
		return 0.0001 / 1000000
	}
}

// MaxBatchSize returns the maximum batch size.
func (p *OpenAIProvider) MaxBatchSize() int {
	return 2048 // OpenAI max batch size
}

// SupportsAsync returns false (OpenAI doesn't support async embeddings).
func (p *OpenAIProvider) SupportsAsync() bool {
	return false
}

// Generate creates an embedding from text.
func (p *OpenAIProvider) Generate(ctx context.Context, text string) (*Embedding, error) {
	embeddings, err := p.GenerateBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return embeddings[0], nil
}

// GenerateBatch creates embeddings for multiple texts.
func (p *OpenAIProvider) GenerateBatch(ctx context.Context, texts []string) ([]*Embedding, error) {
	if len(texts) == 0 {
		return []*Embedding{}, nil
	}

	if len(texts) > p.MaxBatchSize() {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(texts), p.MaxBatchSize())
	}

	// Prepare request
	reqBody := map[string]any{
		"input": texts,
		"model": p.model,
	}

	// For text-embedding-3-* models, can specify dimensions
	if p.model == "text-embedding-3-small" || p.model == "text-embedding-3-large" {
		if p.dimensions > 0 {
			reqBody["dimensions"] = p.dimensions
		}
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API call with retries
	var resp *openAIResponse
	var lastErr error

	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		resp, lastErr = p.makeRequest(ctx, jsonData)
		if lastErr == nil {
			break
		}

		if attempt < p.maxRetries {
			// Exponential backoff
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed after %d retries: %w", p.maxRetries, lastErr)
	}

	// Convert response to embeddings
	embeddings := make([]*Embedding, len(resp.Data))
	for i, data := range resp.Data {
		hash := sha256.Sum256([]byte(texts[i]))
		hashStr := hex.EncodeToString(hash[:])

		tokensUsed := resp.Usage.TotalTokens / len(texts) // Approximate per embedding
		cost := float64(tokensUsed) * p.CostPerToken()

		embeddings[i] = &Embedding{
			Vector:     data.Embedding,
			Model:      p.model,
			Provider:   "openai",
			Dimensions: len(data.Embedding),
			Text:       texts[i],
			Hash:       hashStr,
			CreatedAt:  time.Now(),
			TokensUsed: tokensUsed,
			Cost:       cost,
		}
	}

	return embeddings, nil
}

// makeRequest makes an HTTP request to the OpenAI API.
func (p *OpenAIProvider) makeRequest(ctx context.Context, jsonData []byte) (*openAIResponse, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		fmt.Sprintf("%s/embeddings", p.baseURL),
		bytes.NewReader(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.apiKey))

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		var errResp openAIErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return nil, fmt.Errorf("API error: %s", errResp.Error.Message)
		}
		return nil, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	var result openAIResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// OpenAI API response types
type openAIResponse struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type openAIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}
