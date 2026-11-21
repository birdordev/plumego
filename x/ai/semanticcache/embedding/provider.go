package embedding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
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
	TotalEmbeddings int64 // Attempted input texts, including failed requests.
	TotalTokens     int64
	TotalCost       float64
	AverageLatency  time.Duration // Mean duration per completed provider call.
	ErrorRate       float64       // Fraction of completed provider calls that returned an error.
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
	Provider      Provider
	mu            sync.Mutex
	stats         Stats
	calls, failed int64
	meanLatency   float64
}

// NewProviderWithStats creates a new provider with statistics tracking.
func NewProviderWithStats(provider Provider) *ProviderWithStats {
	return &ProviderWithStats{
		Provider: provider,
	}
}

// Generate wraps the provider's Generate method with stats tracking.
func (p *ProviderWithStats) Generate(ctx context.Context, text string) (*Embedding, error) {
	start := time.Now()
	result, err := p.Provider.Generate(ctx, text)
	p.recordStats(time.Since(start), 1, err, result)
	return result, err
}

// GenerateBatch wraps the provider's GenerateBatch method with stats tracking.
func (p *ProviderWithStats) GenerateBatch(ctx context.Context, texts []string) ([]*Embedding, error) {
	start := time.Now()
	results, err := p.Provider.GenerateBatch(ctx, texts)
	p.recordStats(time.Since(start), len(texts), err, results...)
	return results, err
}

func (p *ProviderWithStats) recordStats(elapsed time.Duration, attempted int, err error, results ...*Embedding) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	if err != nil {
		p.failed++
	}
	p.meanLatency += (float64(elapsed) - p.meanLatency) / float64(p.calls)
	p.stats.AverageLatency = time.Duration(p.meanLatency)
	p.stats.ErrorRate = float64(p.failed) / float64(p.calls)
	p.stats.TotalEmbeddings += int64(attempted)
	for _, result := range results {
		if result != nil {
			p.stats.TotalTokens += int64(result.TokensUsed)
			p.stats.TotalCost += result.Cost
		}
	}
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

// Stats returns a caller-owned snapshot of completed calls since the last reset.
func (p *ProviderWithStats) Stats() *Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := p.stats
	return &snapshot
}

// ResetStats clears the statistics. Calls completing afterward are counted in
// the new interval.
func (p *ProviderWithStats) ResetStats() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats = Stats{}
	p.calls = 0
	p.failed = 0
	p.meanLatency = 0
}
