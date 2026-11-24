package mq

import (
	"context"
	"time"
)

type taskAvailability interface {
	NextAvailable(ctx context.Context, topics []string, now time.Time) (time.Time, error)
}

func nextIdlePoll(current, maximum time.Duration) time.Duration {
	if maximum <= current {
		return current
	}
	return current + min(current, maximum-current)
}

func jitterIdlePoll(current, base, maximum time.Duration) time.Duration {
	if maximum <= base {
		return base
	}
	spread := min(current/4, current-base)
	return current - time.Duration(float64(spread)*randFloat64())
}

func (w *Worker) idlePollDelay(topics []string, idle time.Duration) time.Duration {
	delay := jitterIdlePoll(idle, w.config.PollInterval, w.config.MaxIdlePollInterval)
	if w.config.MaxIdlePollInterval <= w.config.PollInterval {
		return delay
	}
	if store, ok := w.queue.store.(taskAvailability); ok {
		ctx, cancel := context.WithTimeout(w.pollCtx, min(w.config.PollInterval, 100*time.Millisecond))
		defer cancel()
		at, err := store.NextAvailable(ctx, topics, w.queue.now())
		if err == nil && !at.IsZero() {
			delay = min(delay, max(time.Millisecond, at.Sub(w.queue.now())))
		}
	}
	return delay
}

func (w *Worker) waitIdle(delay time.Duration, wake *<-chan struct{}) (notified, running bool) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-w.pollCtx.Done():
			return false, false
		case <-w.queue.ready:
			return true, true
		case _, ok := <-*wake:
			if ok {
				return true, true
			}
			*wake = nil
		case <-timer.C:
			return false, true
		}
	}
}
