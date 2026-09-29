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

// CohereProvider generates embeddings using Cohere's API.
type CohereProvider struct {
	apiKey     string
	model      string
	inputType  string
	dimensions int
	baseURL    string
	httpClient *http.Client
	maxRetries int
}

// CohereConfig holds Cohere provider configuration.
type CohereConfig struct {
	APIKey     string
	Model      string // embed-english-v3.0, embed-multilingual-v3.0, embed-english-light-v3.0
	InputType  string // search_document, search_query, classification, clustering
	Dimensions int    // Optional: for v3 models
	Timeout    time.Duration
	MaxRetries int
	BaseURL    string
}

// NewCohereProvider creates a new Cohere embedding provider.
func NewCohereProvider(config CohereConfig) (*CohereProvider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	if config.Model == "" {
		config.Model = "embed-english-v3.0"
	}

	if config.InputType == "" {
		config.InputType = "search_document"
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}

	if config.BaseURL == "" {
		config.BaseURL = "https://api.cohere.com/v1"
	}

	// Set default dimensions based on model
	dimensions := config.Dimensions
	if dimensions == 0 {
		switch config.Model {
		case "embed-english-v3.0", "embed-multilingual-v3.0":
			dimensions = 1024
		case "embed-english-light-v3.0", "embed-multilingual-light-v3.0":
			dimensions = 384
		case "embed-english-v2.0":
			dimensions = 4096
		default:
			dimensions = 1024
		}
	}

	return &CohereProvider{
		apiKey:     config.APIKey,
		model:      config.Model,
		inputType:  config.InputType,
		dimensions: dimensions,
		baseURL:    config.BaseURL,
		maxRetries: config.MaxRetries,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
	}, nil
}

// Name returns the provider name.
func (p *CohereProvider) Name() string {
	return "cohere"
}

// Model returns the model name.
func (p *CohereProvider) Model() string {
	return p.model
}

// Dimensions returns the embedding dimension count.
func (p *CohereProvider) Dimensions() int {
	return p.dimensions
}

// CostPerToken returns the cost per token.
func (p *CohereProvider) CostPerToken() float64 {
	// Pricing as of 2024 (per million tokens)
	switch p.model {
	case "embed-english-v3.0", "embed-multilingual-v3.0":
		return 0.0001 / 1000000 // $0.10 per 1M tokens
	case "embed-english-light-v3.0", "embed-multilingual-light-v3.0":
		return 0.00001 / 1000000 // $0.01 per 1M tokens
	default:
		return 0.0001 / 1000000
	}
}

// MaxBatchSize returns the maximum batch size.
func (p *CohereProvider) MaxBatchSize() int {
	return 96 // Cohere max batch size
}

// SupportsAsync returns false (Cohere doesn't support async embeddings).
func (p *CohereProvider) SupportsAsync() bool {
	return false
}

// Generate creates an embedding from text.
func (p *CohereProvider) Generate(ctx context.Context, text string) (*Embedding, error) {
	embeddings, err := p.GenerateBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return embeddings[0], nil
}

// GenerateBatch creates embeddings for multiple texts.
func (p *CohereProvider) GenerateBatch(ctx context.Context, texts []string) ([]*Embedding, error) {
	if len(texts) == 0 {
		return []*Embedding{}, nil
	}

	if len(texts) > p.MaxBatchSize() {
		return nil, fmt.Errorf("batch size %d exceeds maximum %d", len(texts), p.MaxBatchSize())
	}

	// Prepare request
	reqBody := map[string]any{
		"texts":           texts,
		"model":           p.model,
		"input_type":      p.inputType,
		"truncate":        "END",
		"embedding_types": []string{"float"},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make API call with retries
	var resp *cohereResponse
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
	embeddings := make([]*Embedding, len(resp.Embeddings))
	for i, vector := range resp.Embeddings {
		hash := sha256.Sum256([]byte(texts[i]))
		hashStr := hex.EncodeToString(hash[:])

		// Estimate tokens (Cohere uses approximate token count)
		tokensUsed := len(texts[i]) / 4 // Rough estimate
		cost := float64(tokensUsed) * p.CostPerToken()

		embeddings[i] = &Embedding{
			Vector:     vector,
			Model:      p.model,
			Provider:   "cohere",
			Dimensions: len(vector),
			Text:       texts[i],
			Hash:       hashStr,
			CreatedAt:  time.Now(),
			TokensUsed: tokensUsed,
			Cost:       cost,
		}
	}

	return embeddings, nil
}

// makeRequest makes an HTTP request to the Cohere API.
func (p *CohereProvider) makeRequest(ctx context.Context, jsonData []byte) (*cohereResponse, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		fmt.Sprintf("%s/embed", p.baseURL),
		bytes.NewReader(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.apiKey))
	req.Header.Set("Request-Source", "plumego")

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
		var errResp cohereErrorResponse
		if err := json.Unmarshal(body, &errResp); err == nil {
			return nil, fmt.Errorf("API error: %s", errResp.Message)
		}
		return nil, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	var result cohereResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &result, nil
}

// Cohere API response types
type cohereResponse struct {
	ID         string      `json:"id"`
	Embeddings [][]float64 `json:"embeddings"`
	Texts      []string    `json:"texts"`
	Meta       cohereMeta  `json:"meta"`
}

type cohereMeta struct {
	APIVersion struct {
		Version string `json:"version"`
	} `json:"api_version"`
	BilledUnits struct {
		InputTokens int `json:"input_tokens"`
	} `json:"billed_units"`
}

type cohereErrorResponse struct {
	Message string `json:"message"`
}
