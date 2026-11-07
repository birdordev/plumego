package embedding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrResponseTooLarge is returned when an embedding API response body exceeds
// maxResponseBytes, so oversized responses fail loudly instead of being read
// into memory unbounded.
var ErrResponseTooLarge = errors.New("embedding: provider response exceeds size limit")

// maxResponseBytes bounds embedding API response bodies read into memory
// (64 MiB — a 96-text batch of 3072-dimension vectors is ~5 MiB as JSON).
const maxResponseBytes int64 = 64 << 20

// readResponseBody reads at most maxResponseBytes from r and fails with
// ErrResponseTooLarge when the source is larger, so truncation is never
// silent. It reads one byte past the limit so overflow is detectable.
func readResponseBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	return body, nil
}

// Provider generates embeddings with additional capabilities.
type Provider interface {
	// Name returns the provider name (e.g., "openai", "cohere").
	Name() string

	// Generate creates an embedding from text.
	Generate(ctx context.Context, text string) (*Embedding, error)

	// GenerateBatch creates embeddings for multiple texts.
	GenerateBatch(ctx context.Context, texts []string) ([]*Embedding, error)

	// Dimensions returns the embedding dimension count.
	Dimensions() int

	// Model returns the model name.
	Model() string

	// CostPerToken returns the cost per token (for cost tracking).
	CostPerToken() float64

	// MaxBatchSize returns the maximum batch size supported.
	MaxBatchSize() int

	// SupportsAsync returns true if provider supports async embedding generation.
	SupportsAsync() bool
}

// Embedding represents a vector embedding with metadata.
type Embedding struct {
	// Vector representation
	Vector []float64 `json:"vector"`

	// Metadata
	Model      string    `json:"model"`
	Provider   string    `json:"provider"`
	Dimensions int       `json:"dimensions"`
	Text       string    `json:"text"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"created_at"`

	// Token usage
	TokensUsed int     `json:"tokens_used"`
	Cost       float64 `json:"cost"`
}

// Config holds common provider configuration.
type Config struct {
	APIKey     string
	Model      string
	Timeout    time.Duration
	MaxRetries int
	BaseURL    string
}

// Stats holds embedding generation statistics.
type Stats struct {
	TotalEmbeddings int64
	TotalTokens     int64
	TotalCost       float64
	AverageLatency  time.Duration
	ErrorRate       float64
}

// BatchRequest represents a batch embedding request.
type BatchRequest struct {
	Texts      []string
	Model      string
	ReturnMeta bool
}

// BatchResponse represents a batch embedding response.
type BatchResponse struct {
	Embeddings []*Embedding
	TokensUsed int
	Cost       float64
	Duration   time.Duration
}

// ProviderWithStats wraps a provider with statistics tracking.
type ProviderWithStats struct {
	Provider Provider
	stats    *Stats
}

// NewProviderWithStats creates a new provider with statistics tracking.
func NewProviderWithStats(provider Provider) *ProviderWithStats {
	return &ProviderWithStats{
		Provider: provider,
		stats:    &Stats{},
	}
}

// Generate wraps the provider's Generate method with stats tracking.
func (p *ProviderWithStats) Generate(ctx context.Context, text string) (*Embedding, error) {
	start := time.Now()
	embedding, err := p.Provider.Generate(ctx, text)
	duration := time.Since(start)

	p.stats.TotalEmbeddings++
	if embedding != nil {
		p.stats.TotalTokens += int64(embedding.TokensUsed)
		p.stats.TotalCost += embedding.Cost
	}
	if err != nil {
		p.stats.ErrorRate = float64(p.stats.TotalEmbeddings-1) / float64(p.stats.TotalEmbeddings)
	}

	// Update average latency
	if p.stats.TotalEmbeddings > 0 {
		p.stats.AverageLatency = time.Duration(
			(int64(p.stats.AverageLatency)*p.stats.TotalEmbeddings + int64(duration)) /
				(p.stats.TotalEmbeddings + 1),
		)
	}

	return embedding, err
}

// GenerateBatch wraps the provider's GenerateBatch method with stats tracking.
func (p *ProviderWithStats) GenerateBatch(ctx context.Context, texts []string) ([]*Embedding, error) {
	start := time.Now()
	embeddings, err := p.Provider.GenerateBatch(ctx, texts)
	duration := time.Since(start)

	p.stats.TotalEmbeddings += int64(len(texts))
	for _, emb := range embeddings {
		p.stats.TotalTokens += int64(emb.TokensUsed)
		p.stats.TotalCost += emb.Cost
	}
	if err != nil {
		p.stats.ErrorRate = float64(p.stats.TotalEmbeddings-int64(len(texts))) / float64(p.stats.TotalEmbeddings)
	}

	// Update average latency
	if p.stats.TotalEmbeddings > 0 {
		p.stats.AverageLatency = time.Duration(
			(int64(p.stats.AverageLatency)*p.stats.TotalEmbeddings + int64(duration)) /
				(p.stats.TotalEmbeddings + int64(len(texts))),
		)
	}

	return embeddings, err
}

// Name returns the provider name.
func (p *ProviderWithStats) Name() string {
	return p.Provider.Name()
}

// Dimensions returns the embedding dimension count.
func (p *ProviderWithStats) Dimensions() int {
	return p.Provider.Dimensions()
}

// Model returns the model name.
func (p *ProviderWithStats) Model() string {
	return p.Provider.Model()
}

// CostPerToken returns the cost per token.
func (p *ProviderWithStats) CostPerToken() float64 {
	return p.Provider.CostPerToken()
}

// MaxBatchSize returns the maximum batch size.
func (p *ProviderWithStats) MaxBatchSize() int {
	return p.Provider.MaxBatchSize()
}

// SupportsAsync returns true if provider supports async generation.
func (p *ProviderWithStats) SupportsAsync() bool {
	return p.Provider.SupportsAsync()
}

// Stats returns the current statistics.
func (p *ProviderWithStats) Stats() *Stats {
	return p.stats
}

// ResetStats resets all statistics.
func (p *ProviderWithStats) ResetStats() {
	p.stats = &Stats{}
}
