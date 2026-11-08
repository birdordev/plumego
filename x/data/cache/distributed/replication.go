package distributed

import (
	"context"
	"sync"
	"time"

	"github.com/birdordev/plumego/store/cache"
)

type asyncReplicationJob struct {
	operation string
	key       string
	nodeID    string
	run       func(context.Context) error
}

// setSyncReplicas writes the primary first, then selected secondaries synchronously.
func (dc *DistributedCache) setSyncReplicas(ctx context.Context, nodes []CacheNode, key string, value []byte, ttl time.Duration) error {
	start := time.Now()
	if len(nodes) == 0 {
		dc.recordReplicationFailure()
		dc.recordReplicationLag(start)
		return ErrNoNodesAvailable
	}

	primary := nodes[0]
	if !primary.IsHealthy() {
		dc.recordReplicationFailure()
		dc.recordReplicationLag(start)
		return ErrNodeUnhealthy
	}
	if err := primary.Cache().Set(ctx, key, cloneBytes(value), ttl); err != nil {
		dc.recordReplicationFailure()
		dc.recordReplicationLag(start)
		return err
	}
	if len(nodes) == 1 {
		dc.recordReplicationLag(start)
		return nil
	}

	errChan := make(chan error, len(nodes)-1)
	healthyNodes := make([]CacheNode, 0, len(nodes)-1)
	var firstErr error

	for _, node := range nodes[1:] {
		if !node.IsHealthy() {
			errChan <- ErrNodeUnhealthy
			continue
		}
		healthyNodes = append(healthyNodes, node)
	}

	if len(healthyNodes) > 0 {
		var wg sync.WaitGroup
		jobs := make(chan CacheNode)
		workers := dc.replicaWriteConcurrency(len(healthyNodes))
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for node := range jobs {
					if err := node.Cache().Set(ctx, key, cloneBytes(value), ttl); err != nil {
						errChan <- err
					}
				}
			}()
		}

		for _, node := range healthyNodes {
			jobs <- node
		}
		close(jobs)
		wg.Wait()
	}
	close(errChan)

	// Return first error if any
	for err := range errChan {
		if firstErr == nil {
			firstErr = err
		}
		dc.recordReplicationFailure()
	}

	dc.recordReplicationLag(start)
	return firstErr
}

func (dc *DistributedCache) replicaWriteConcurrency(replicaCount int) int {
	if replicaCount <= 1 {
		return 1
	}
	limit := dc.syncConcurrency
	if limit <= 0 {
		limit = dc.asyncConcurrency
	}
	if limit <= 0 {
		limit = defaultAsyncReplicationMaxConcurrency
	}
	if limit > replicaCount {
		return replicaCount
	}
	return limit
}

// setAsyncReplicas writes to primary synchronously, others asynchronously
func (dc *DistributedCache) setAsyncReplicas(ctx context.Context, nodes []CacheNode, key string, value []byte, ttl time.Duration) error {
	if len(nodes) == 0 {
		return ErrNoNodesAvailable
	}

	// Write to primary node synchronously
	primary := nodes[0]
	if !primary.IsHealthy() {
		return ErrNodeUnhealthy
	}

	err := primary.Cache().Set(ctx, key, cloneBytes(value), ttl)
	if err != nil {
		return err
	}

	// Write to replicas asynchronously
	for i := 1; i < len(nodes); i++ {
		node := nodes[i]
		if !node.IsHealthy() {
			dc.recordReplicationFailure()
			continue
		}

		replicaValue := append([]byte(nil), value...)
		replicaNode := node
		dc.scheduleAsyncReplication(asyncReplicationJob{
			operation: "set",
			key:       key,
			nodeID:    replicaNode.ID(),
			run: func(replicaCtx context.Context) error {
				return replicaNode.Cache().Set(replicaCtx, key, replicaValue, ttl)
			},
		})
	}

	return nil
}

func (dc *DistributedCache) incrReplicas(ctx context.Context, nodes []CacheNode, key string, delta int64) (int64, error) {
	if len(nodes) == 0 {
		return 0, ErrNoNodesAvailable
	}

	primary := nodes[0]
	if !primary.IsHealthy() {
		return 0, ErrNodeUnhealthy
	}
	counter, ok := primary.Cache().(cache.CounterCache)
	if !ok {
		return 0, cache.ErrCapabilityUnsupported
	}

	value, err := counter.Incr(ctx, key, delta)
	if err != nil {
		return 0, err
	}

	switch dc.replicationMode {
	case ReplicationSync:
		if err := dc.incrSecondaryReplicas(ctx, nodes[1:], key, delta); err != nil {
			return value, err
		}
	case ReplicationAsync:
		for _, node := range nodes[1:] {
			if !node.IsHealthy() {
				dc.recordReplicationFailure()
				continue
			}
			replicaNode := node
			dc.scheduleAsyncReplication(asyncReplicationJob{
				operation: "incr",
				key:       key,
				nodeID:    replicaNode.ID(),
				run: func(replicaCtx context.Context) error {
					counter, ok := replicaNode.Cache().(cache.CounterCache)
					if !ok {
						return cache.ErrCapabilityUnsupported
					}
					_, err := counter.Incr(replicaCtx, key, delta)
					return err
				},
			})
		}
	}

	return value, nil
}

func (dc *DistributedCache) incrSecondaryReplicas(ctx context.Context, nodes []CacheNode, key string, delta int64) error {
	start := time.Now()
	defer dc.recordReplicationLag(start)

	for _, node := range nodes {
		if !node.IsHealthy() {
			dc.recordReplicationFailure()
			return ErrNodeUnhealthy
		}
		counter, ok := node.Cache().(cache.CounterCache)
		if !ok {
			dc.recordReplicationFailure()
			return cache.ErrCapabilityUnsupported
		}
		if _, err := counter.Incr(ctx, key, delta); err != nil {
			dc.recordReplicationFailure()
			return err
		}
	}
	return nil
}

func (dc *DistributedCache) appendReplicas(ctx context.Context, nodes []CacheNode, key string, data []byte) error {
	if len(nodes) == 0 {
		return ErrNoNodesAvailable
	}

	primary := nodes[0]
	if !primary.IsHealthy() {
		return ErrNodeUnhealthy
	}
	appender, ok := primary.Cache().(cache.AppenderCache)
	if !ok {
		return cache.ErrCapabilityUnsupported
	}

	if err := appender.Append(ctx, key, cloneBytes(data)); err != nil {
		return err
	}

	switch dc.replicationMode {
	case ReplicationSync:
		start := time.Now()
		for _, node := range nodes[1:] {
			if !node.IsHealthy() {
				dc.recordReplicationFailure()
				dc.recordReplicationLag(start)
				return ErrNodeUnhealthy
			}
			appender, ok := node.Cache().(cache.AppenderCache)
			if !ok {
				dc.recordReplicationFailure()
				dc.recordReplicationLag(start)
				return cache.ErrCapabilityUnsupported
			}
			if err := appender.Append(ctx, key, cloneBytes(data)); err != nil {
				dc.recordReplicationFailure()
				dc.recordReplicationLag(start)
				return err
			}
		}
		dc.recordReplicationLag(start)
	case ReplicationAsync:
		for _, node := range nodes[1:] {
			if !node.IsHealthy() {
				dc.recordReplicationFailure()
				continue
			}
			replicaData := append([]byte(nil), data...)
			replicaNode := node
			dc.scheduleAsyncReplication(asyncReplicationJob{
				operation: "append",
				key:       key,
				nodeID:    replicaNode.ID(),
				run: func(replicaCtx context.Context) error {
					appender, ok := replicaNode.Cache().(cache.AppenderCache)
					if !ok {
						return cache.ErrCapabilityUnsupported
					}
					return appender.Append(replicaCtx, key, replicaData)
				},
			})
		}
	}

	return nil
}

func (dc *DistributedCache) replicationNodes(key string) ([]CacheNode, error) {
	if dc.replicationMode == ReplicationNone {
		node, err := dc.ring.Get(key)
		if err != nil {
			return nil, err
		}
		return []CacheNode{node}, nil
	}
	return dc.ring.GetN(key, dc.replicationFactor)
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	return append([]byte(nil), data...)
}

func (dc *DistributedCache) startAsyncReplicationWorkers(count int) {
	for i := 0; i < count; i++ {
		dc.asyncWG.Add(1)
		go dc.asyncReplicationWorker()
	}
}

func (dc *DistributedCache) asyncReplicationWorker() {
	defer dc.asyncWG.Done()
	for {
		select {
		case <-dc.asyncStop:
			return
		case job := <-dc.asyncQueue:
			dc.runAsyncReplication(job)
		}
	}
}

func (dc *DistributedCache) scheduleAsyncReplication(job asyncReplicationJob) {
	if dc.asyncClosed.Load() {
		dc.dropAsyncReplication(job, AsyncReplicationDropClosed)
		return
	}
	select {
	case dc.asyncQueue <- job:
	case <-dc.asyncStop:
		dc.dropAsyncReplication(job, AsyncReplicationDropClosed)
	default:
		dc.dropAsyncReplication(job, AsyncReplicationDropQueueFull)
	}
}

func (dc *DistributedCache) drainAsyncReplicationQueue() {
	for {
		select {
		case job := <-dc.asyncQueue:
			dc.dropAsyncReplication(job, AsyncReplicationDropClosed)
		default:
			return
		}
	}
}

func (dc *DistributedCache) runAsyncReplication(job asyncReplicationJob) {
	start := time.Now()
	replicaCtx, cancel := dc.asyncReplicationContext()
	defer cancel()
	err := job.run(replicaCtx)
	dc.recordReplicationLag(start)
	if err != nil {
		dc.recordReplicationFailure()
	}
}

func (dc *DistributedCache) dropAsyncReplication(job asyncReplicationJob, reason AsyncReplicationDropReason) {
	dc.recordReplicationFailure()
	if dc.asyncDropHandler != nil {
		dc.callAsyncDropHandler(AsyncReplicationDrop{
			Operation: job.operation,
			Key:       job.key,
			NodeID:    job.nodeID,
			Reason:    reason,
		})
	}
}

func (dc *DistributedCache) callAsyncDropHandler(drop AsyncReplicationDrop) {
	defer func() {
		if recover() != nil {
			dc.recordReplicationFailure()
		}
	}()
	dc.asyncDropHandler(drop)
}

func (dc *DistributedCache) asyncReplicationContext() (context.Context, context.CancelFunc) {
	if dc.asyncTimeout <= 0 {
		return context.WithTimeout(context.Background(), defaultAsyncTimeout)
	}
	return context.WithTimeout(context.Background(), dc.asyncTimeout)
}
