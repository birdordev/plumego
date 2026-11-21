package cachemanager

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/birdordev/plumego/x/ai/semanticcache/embedding"
	"github.com/birdordev/plumego/x/ai/semanticcache/vectorstore"
)

// Warmer handles cache warming operations.
type Warmer struct {
	backend  vectorstore.Backend
	provider embedding.Provider
}

// NewWarmer creates a new cache warmer.
func NewWarmer(backend vectorstore.Backend, provider embedding.Provider) *Warmer {
	return &Warmer{
		backend:  backend,
		provider: provider,
	}
}

// WarmFromQueries pre-warms the cache from historical queries.
func (w *Warmer) WarmFromQueries(ctx context.Context, queries []QueryResponse) error {
	if len(queries) == 0 {
		return nil
	}

	// Extract texts for batch embedding
	texts := make([]string, len(queries))
	for i, q := range queries {
		texts[i] = q.Query
	}

	// Generate embeddings in batch
	embeddings, err := w.generateBatch(ctx, texts)
	if err != nil {
		return err
	}

	// Create cache entries
	entries := make([]*vectorstore.Entry, len(queries))
	for i, q := range queries {
		hash := sha256.Sum256([]byte(q.Query))
		hashStr := hex.EncodeToString(hash[:])

		entries[i] = &vectorstore.Entry{
			ID:        hashStr,
			Vector:    embeddings[i].Vector,
			Text:      q.Query,
			Response:  q.Response,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Metadata: map[string]any{
				"provider": embeddings[i].Provider,
				"model":    embeddings[i].Model,
				"warmed":   true,
			},
		}
	}

	// Add to cache
	if err := w.backend.AddBatch(ctx, entries); err != nil {
		return fmt.Errorf("failed to add entries to cache: %w", err)
	}

	return nil
}

// batchSize defaults to 100 when the provider has no positive batch limit.
func (w *Warmer) batchSize() int {
	if size := w.provider.MaxBatchSize(); size > 0 {
		return size
	}
	return 100
}

// WarmFromDocuments pre-warms the cache from a document corpus. A batch
// with invalid embedding results is rejected before writing it. Earlier
// batches remain stored. A nonpositive provider batch limit uses batches of 100.
func (w *Warmer) WarmFromDocuments(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return nil
	}

	// Process documents in chunks based on provider batch size
	batchSize := w.batchSize()

	for i := 0; i < len(docs); i += batchSize {
		end := i + batchSize
		if end > len(docs) {
			end = len(docs)
		}

		batch := docs[i:end]
		if err := w.warmBatch(ctx, batch); err != nil {
			return fmt.Errorf("failed to warm batch %d-%d: %w", i, end, err)
		}
	}

	return nil
}

// WarmFromFile reads queries from a file and warms the cache.
func (w *Warmer) WarmFromFile(ctx context.Context, path string) error {
	return fmt.Errorf("%w: warm from file requires a file parser", ErrUnsupportedMaintenance)
}

// WarmWithStrategy warms cache using the selected strategy. When limiting the
// result, most-frequent prefers earlier inputs on equal counts, and recent
// prefers later inputs on equal timestamps. The input order is unchanged.
func (w *Warmer) WarmWithStrategy(ctx context.Context, strategy WarmingStrategy) error {
	switch strategy.Type {
	case StrategyMostFrequent:
		// Warm most frequently accessed queries
		return w.warmMostFrequent(ctx, strategy.Queries, strategy.Limit)
	case StrategyRecent:
		// Warm most recent queries
		return w.warmRecent(ctx, strategy.Queries, strategy.Limit)
	case StrategyAll:
		// Warm all queries
		return w.WarmFromQueries(ctx, strategy.Queries)
	default:
		return fmt.Errorf("unknown warming strategy: %s", strategy.Type)
	}
}

// warmBatch warms a batch of documents.
func (w *Warmer) warmBatch(ctx context.Context, docs []Document) error {
	// Extract texts
	texts := make([]string, len(docs))
	for i, doc := range docs {
		texts[i] = doc.Content
	}

	// Generate embeddings
	embeddings, err := w.generateBatch(ctx, texts)
	if err != nil {
		return err
	}

	// Create entries
	entries := make([]*vectorstore.Entry, len(docs))
	for i, doc := range docs {
		hash := sha256.Sum256([]byte(doc.Content))
		hashStr := hex.EncodeToString(hash[:])

		entries[i] = &vectorstore.Entry{
			ID:        hashStr,
			Vector:    embeddings[i].Vector,
			Text:      doc.Content,
			Response:  doc.Summary,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Metadata: map[string]any{
				"provider":    embeddings[i].Provider,
				"model":       embeddings[i].Model,
				"warmed":      true,
				"document_id": doc.ID,
				"title":       doc.Title,
			},
		}
	}

	// Add to cache
	return w.backend.AddBatch(ctx, entries)
}

func (w *Warmer) generateBatch(ctx context.Context, texts []string) ([]*embedding.Embedding, error) {
	embeddings, err := w.provider.GenerateBatch(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embeddings: %w", err)
	}
	if len(embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding batch returned %d results for %d inputs", len(embeddings), len(texts))
	}
	for i, result := range embeddings {
		if result == nil {
			return nil, fmt.Errorf("embedding batch returned nil result at index %d", i)
		}
	}
	return embeddings, nil
}

// warmMostFrequent warms most frequently accessed queries.
func (w *Warmer) warmMostFrequent(ctx context.Context, queries []QueryResponse, limit int) error {
	if limit > 0 && limit < len(queries) {
		queries = slices.Clone(queries)
		slices.SortStableFunc(queries, func(a, b QueryResponse) int {
			return cmp.Compare(b.AccessCount, a.AccessCount)
		})
		queries = queries[:limit]
	}

	return w.WarmFromQueries(ctx, queries)
}

// warmRecent warms most recent queries.
func (w *Warmer) warmRecent(ctx context.Context, queries []QueryResponse, limit int) error {
	if limit > 0 && limit < len(queries) {
		queries = slices.Clone(queries)
		slices.SortStableFunc(queries, func(a, b QueryResponse) int {
			return a.Timestamp.Compare(b.Timestamp)
		})
		queries = queries[len(queries)-limit:]
	}

	return w.WarmFromQueries(ctx, queries)
}

// QueryResponse represents a query-response pair for warming.
type QueryResponse struct {
	Query       string
	Response    string
	AccessCount int64
	Timestamp   time.Time
}

// Document represents a document for warming.
type Document struct {
	ID       string
	Title    string
	Content  string
	Summary  string
	Metadata map[string]any
}

// WarmingStrategy defines a cache warming strategy.
type WarmingStrategy struct {
	Type    StrategyType
	Queries []QueryResponse
	Docs    []Document
	Limit   int
}

// StrategyType represents the type of warming strategy.
type StrategyType string

const (
	StrategyMostFrequent StrategyType = "most_frequent"
	StrategyRecent       StrategyType = "recent"
	StrategyAll          StrategyType = "all"
)

// WarmingStats holds statistics about cache warming.
type WarmingStats struct {
	QueriesWarmed   int
	DocumentsWarmed int
	TotalEmbeddings int
	TotalTime       time.Duration
	AverageLatency  time.Duration
	ErrorCount      int
}

// ProgressCallback is called during warming to report progress.
type ProgressCallback func(current, total int, stats *WarmingStats)

// WarmWithProgress warms cache with progress reporting for successful batches.
// Failed batches are reported together. Successfully stored batches remain
// available, and cancellation stops the operation before starting another batch.
// A nonpositive provider batch limit uses batches of 100.
func (w *Warmer) WarmWithProgress(ctx context.Context, queries []QueryResponse, callback ProgressCallback) error {
	if len(queries) == 0 {
		return nil
	}

	stats := &WarmingStats{}
	start := time.Now()
	var failures []error

	// Process queries in batches
	batchSize := w.batchSize()

	for i := 0; i < len(queries); i += batchSize {
		if ctx.Err() != nil {
			break
		}
		end := i + batchSize
		if end > len(queries) {
			end = len(queries)
		}

		batch := queries[i:end]
		if err := w.WarmFromQueries(ctx, batch); err != nil {
			stats.ErrorCount++
			failures = append(failures, fmt.Errorf("warm batch [%d, %d): %w", i, end, err))
			continue
		}

		stats.QueriesWarmed += len(batch)
		stats.TotalEmbeddings += len(batch)

		if callback != nil {
			callback(i+len(batch), len(queries), stats)
		}
	}

	stats.TotalTime = time.Since(start)
	if stats.QueriesWarmed > 0 {
		stats.AverageLatency = stats.TotalTime / time.Duration(stats.QueriesWarmed)
	}

	if cause := context.Cause(ctx); cause != nil {
		failures = append(failures, cause)
	}
	return errors.Join(failures...)
}
