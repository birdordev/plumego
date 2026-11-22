package fileapi

import (
	"context"
	"errors"
	"sync"
	"time"
)

type accessKey struct {
	tenant string
	file   string
}

type accessUpdates struct {
	ctx     context.Context
	queue   chan accessKey
	mu      sync.Mutex
	pending map[accessKey]struct{}
}

// EnableDeferredAccessUpdates configures best-effort access tracking for a
// handler before serving requests. Updates for the same file coalesce while
// pending. Saturated queues drop new updates. Stop cancels in-flight work and
// discards queued updates. Call it during application shutdown.
func (h *Handler) EnableDeferredAccessUpdates(parent context.Context, capacity int, timeout time.Duration) (stop func(), err error) {
	if h == nil || h.metadata == nil || parent == nil || capacity <= 0 || timeout <= 0 || h.accessUpdates != nil {
		return nil, errors.New("invalid deferred access update configuration")
	}
	ctx, cancel := context.WithCancel(parent)
	updates := &accessUpdates{
		ctx:     ctx,
		queue:   make(chan accessKey, capacity),
		pending: make(map[accessKey]struct{}),
	}
	h.accessUpdates = updates
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			updates.mu.Lock()
			clear(updates.pending)
			for len(updates.queue) != 0 {
				<-updates.queue
			}
			updates.mu.Unlock()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case key := <-updates.queue:
				if ctx.Err() != nil {
					return
				}
				op, finish := context.WithTimeout(ctx, timeout)
				_ = h.metadata.UpdateAccessTime(op, key.tenant, key.file)
				finish()
				updates.mu.Lock()
				delete(updates.pending, key)
				updates.mu.Unlock()
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}, nil
}

func (u *accessUpdates) enqueue(key accessKey) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ctx.Err() != nil {
		return
	}
	if _, exists := u.pending[key]; exists {
		return
	}
	select {
	case u.queue <- key:
		u.pending[key] = struct{}{}
	default:
	}
}
