package sharding

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/birdordev/plumego/x/data/internal/testx"
	"github.com/birdordev/plumego/x/data/rw"
)

func queryRowError(err error) *sql.Row {
	return testx.QueryRowError(err)
}

// handleCrossShardQuery handles queries that span multiple shards
func (r *Router) handleCrossShardQuery(ctx context.Context, query string, args []any) (*sql.Rows, error) {
	r.recordCrossShardQuery()

	switch r.config.CrossShardPolicy {
	case CrossShardDeny:
		return nil, ErrCrossShardQuery

	case CrossShardFirst:
		return nil, ErrCrossShardQuery

	case CrossShardFirstSuccess:
		resolved := make([]*ResolvedShard, 0, len(r.shards))
		for i := range r.shards {
			resolved = append(resolved, &ResolvedShard{ShardIndex: i})
		}
		return r.queryResolvedShards(ctx, query, args, resolved)

	default:
		return nil, fmt.Errorf("unknown cross-shard policy: %v", r.config.CrossShardPolicy)
	}
}

func (r *Router) handleResolvedShardsQuery(ctx context.Context, query string, args []any, resolved []*ResolvedShard) (*sql.Rows, error) {
	r.recordCrossShardQuery()

	switch r.config.CrossShardPolicy {
	case CrossShardDeny:
		return nil, ErrCrossShardQuery
	case CrossShardFirst:
		if len(resolved) == 0 {
			return nil, ErrNoShards
		}
		return r.queryOneResolvedShard(ctx, query, args, resolved[0])
	case CrossShardFirstSuccess:
		return r.queryResolvedShards(ctx, query, args, resolved)
	default:
		return nil, fmt.Errorf("unknown cross-shard policy: %v", r.config.CrossShardPolicy)
	}
}

func (r *Router) queryRowResolvedShards(ctx context.Context, query string, args []any, resolved []*ResolvedShard) *sql.Row {
	r.recordCrossShardQuery()

	switch r.config.CrossShardPolicy {
	case CrossShardDeny:
		return queryRowError(ErrCrossShardQuery)
	case CrossShardFirst:
		if len(resolved) == 0 {
			return queryRowError(ErrNoShards)
		}
		return r.queryRowOneResolvedShard(ctx, query, args, resolved[0])
	case CrossShardFirstSuccess:
		return queryRowError(errors.New("sharding: QueryRowContext does not support CrossShardFirstSuccess"))
	default:
		return queryRowError(fmt.Errorf("unknown cross-shard policy: %v", r.config.CrossShardPolicy))
	}
}

func (r *Router) queryOneResolvedShard(ctx context.Context, query string, args []any, resolved *ResolvedShard) (*sql.Rows, error) {
	if resolved.ShardIndex < 0 || resolved.ShardIndex >= len(r.shards) {
		r.recordRoutingError()
		return nil, fmt.Errorf("%w: index %d", ErrShardNotFound, resolved.ShardIndex)
	}
	r.recordShardQuery(resolved.ShardIndex)
	rewrittenQuery, err := r.rewriter.Rewrite(query, resolved.ShardIndex)
	if err != nil {
		r.recordRoutingError()
		return nil, fmt.Errorf("failed to rewrite SQL: %w", err)
	}
	return r.shards[resolved.ShardIndex].QueryContext(ctx, rewrittenQuery, args...)
}

func (r *Router) queryRowOneResolvedShard(ctx context.Context, query string, args []any, resolved *ResolvedShard) *sql.Row {
	if resolved.ShardIndex < 0 || resolved.ShardIndex >= len(r.shards) {
		r.recordRoutingError()
		return queryRowError(fmt.Errorf("%w: index %d", ErrShardNotFound, resolved.ShardIndex))
	}
	r.recordShardQuery(resolved.ShardIndex)
	rewrittenQuery, err := r.rewriter.Rewrite(query, resolved.ShardIndex)
	if err != nil {
		r.recordRoutingError()
		return queryRowError(fmt.Errorf("failed to rewrite SQL: %w", err))
	}
	return r.shards[resolved.ShardIndex].QueryRowContext(ctx, rewrittenQuery, args...)
}

type crossShardQueryResult struct {
	rows  *sql.Rows
	err   error
	shard int
	done  context.CancelFunc
}

// queryResolvedShards executes a query on resolved shards concurrently.
// It returns the first successful *sql.Rows and closes all other result sets.
// This implements the CrossShardFirstSuccess policy; note that it does NOT
// merge rows from multiple shards — callers receive data from exactly one shard.
func (r *Router) queryResolvedShards(ctx context.Context, query string, args []any, resolved []*ResolvedShard) (*sql.Rows, error) {
	for _, item := range resolved {
		if item.ShardIndex < 0 || item.ShardIndex >= len(r.shards) {
			r.recordRoutingError()
			return nil, fmt.Errorf("%w: index %d", ErrShardNotFound, item.ShardIndex)
		}
	}

	results := make(chan crossShardQueryResult, len(resolved))
	cancels := make(map[int]context.CancelFunc, len(resolved))
	var wg sync.WaitGroup

	// Launch queries on resolved shards concurrently.
	for _, item := range resolved {
		shardCtx, cancel := context.WithCancel(ctx)
		cancels[item.ShardIndex] = cancel
		wg.Add(1)
		go func(ctx context.Context, idx int, s *rw.Cluster, done context.CancelFunc) {
			defer wg.Done()
			rewrittenQuery, err := r.rewriter.Rewrite(query, idx)
			if err != nil {
				results <- crossShardQueryResult{err: fmt.Errorf("rewrite shard %d: %w", idx, err), shard: idx, done: done}
				return
			}
			r.recordShardExecution(idx)
			rows, err := s.QueryContext(ctx, rewrittenQuery, args...)
			results <- crossShardQueryResult{rows: rows, err: err, shard: idx, done: done}
		}(shardCtx, item.ShardIndex, r.shards[item.ShardIndex], cancel)
	}

	// Close the results channel once all goroutines have written.
	// The channel is buffered so goroutines never block regardless of whether
	// the consumer is still reading.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Return the first success immediately, then drain and close late result
	// sets in the background. The winning context must remain active because
	// database/sql can continue to observe it while the caller reads rows.
	var errs []error

	for res := range results {
		if res.err != nil {
			res.done()
			errs = append(errs, fmt.Errorf("shard %d: %w", res.shard, res.err))
			continue
		}

		for idx, cancel := range cancels {
			if idx != res.shard {
				cancel()
			}
		}

		go drainCrossShardResults(results, res.shard)
		return res.rows, nil
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("%w: %w", ErrAllShardsFailed, errors.Join(errs...))
	}

	return nil, ErrAllShardsFailed
}

func drainCrossShardResults(results <-chan crossShardQueryResult, winningShard int) {
	for res := range results {
		if res.err == nil && res.rows != nil && res.shard != winningShard {
			_ = res.rows.Close()
		}
		if res.shard != winningShard {
			res.done()
		}
	}
}
