package distributed

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/store/cache"
)

// failoverGet attempts to get a value from replica nodes
func (dc *DistributedCache) failoverGet(ctx context.Context, key string, failedNodeID string, cause error) ([]byte, error) {
	if dc.metrics != nil {
		atomic.AddUint64(&dc.metrics.FailoverCount, 1)
	}

	switch dc.failoverStrategy {
	case FailoverAllNodes:
		return dc.getFromNodes(ctx, key, failedNodeID, dc.ring.Nodes(), cause)
	case FailoverRetry:
		return dc.retryFailedNode(ctx, key, failedNodeID, cause)
	default:
		nodes, err := dc.replicationNodes(key)
		if err != nil {
			return nil, err
		}
		return dc.getFromNodes(ctx, key, failedNodeID, nodes, cause)
	}
}

func (dc *DistributedCache) failoverExists(ctx context.Context, key string, failedNodeID string, cause error) (bool, error) {
	if dc.metrics != nil {
		atomic.AddUint64(&dc.metrics.FailoverCount, 1)
	}

	switch dc.failoverStrategy {
	case FailoverAllNodes:
		return dc.existsInNodes(ctx, key, failedNodeID, dc.ring.Nodes(), cause)
	case FailoverRetry:
		var failedNode CacheNode
		for _, node := range dc.ring.Nodes() {
			if node.ID() == failedNodeID {
				failedNode = node
				break
			}
		}
		if failedNode == nil {
			return false, ErrNodeNotFound
		}
		if !failedNode.IsHealthy() {
			return false, ErrNodeUnhealthy
		}
		var lastErr error
		for i := 0; i < dc.failoverAttempts; i++ {
			exists, err := failedNode.Cache().Exists(ctx, key)
			if err == nil {
				return exists, nil
			}
			if errors.Is(err, cache.ErrNotFound) {
				return false, cache.ErrNotFound
			}
			lastErr = err
			if i == dc.failoverAttempts-1 {
				break
			}
			timer := time.NewTimer(dc.failoverBackoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false, ctx.Err()
			case <-timer.C:
			}
		}
		if lastErr != nil {
			return false, lastErr
		}
		return false, cause
	default:
		nodes, err := dc.replicationNodes(key)
		if err != nil {
			return false, err
		}
		return dc.existsInNodes(ctx, key, failedNodeID, nodes, cause)
	}
}

func (dc *DistributedCache) existsInNodes(ctx context.Context, key string, failedNodeID string, nodes []CacheNode, cause error) (bool, error) {
	var firstErr error
	for _, node := range nodes {
		if node.ID() == failedNodeID || !node.IsHealthy() {
			continue
		}
		exists, err := node.Cache().Exists(ctx, key)
		if err == nil && exists {
			return true, nil
		}
		if err != nil && !errors.Is(err, cache.ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return false, firstErr
	}
	return false, cause
}

func (dc *DistributedCache) getFromNodes(ctx context.Context, key string, failedNodeID string, nodes []CacheNode, cause error) ([]byte, error) {
	var firstErr error
	for _, node := range nodes {
		if node.ID() == failedNodeID || !node.IsHealthy() {
			continue
		}

		value, err := node.Cache().Get(ctx, key)
		if err == nil {
			return cloneBytes(value), nil
		}

		if !errors.Is(err, cache.ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}
	if cause != nil && !errors.Is(cause, cache.ErrNotFound) {
		return nil, cause
	}
	return nil, cache.ErrNotFound
}

func (dc *DistributedCache) retryFailedNode(ctx context.Context, key string, failedNodeID string, cause error) ([]byte, error) {
	var failedNode CacheNode
	for _, node := range dc.ring.Nodes() {
		if node.ID() == failedNodeID {
			failedNode = node
			break
		}
	}
	if failedNode == nil {
		return nil, ErrNodeNotFound
	}
	if !failedNode.IsHealthy() {
		return nil, ErrNodeUnhealthy
	}

	var lastErr error
	for i := 0; i < dc.failoverAttempts; i++ {
		value, err := failedNode.Cache().Get(ctx, key)
		if err == nil {
			return cloneBytes(value), nil
		}
		if errors.Is(err, cache.ErrNotFound) {
			return nil, cache.ErrNotFound
		}
		lastErr = err
		if i == dc.failoverAttempts-1 {
			break
		}
		timer := time.NewTimer(dc.failoverBackoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, cause
}
