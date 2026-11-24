package main

import (
	"context"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

type ackRequest struct {
	ctx    context.Context
	item   mqstore.TaskAcknowledgment
	result chan error
}

type batchedQueue struct {
	*mqstore.SQLStore
	requests chan ackRequest
	ctx      context.Context
	window   time.Duration
	limit    int
}

func startBatchedQueue(ctx context.Context, store *mqstore.SQLStore, window time.Duration, limit int) (*batchedQueue, <-chan struct{}) {
	queue := &batchedQueue{SQLStore: store, ctx: ctx, requests: make(chan ackRequest, limit), window: window, limit: limit}
	done := make(chan struct{})
	go func() {
		defer close(done)
		queue.collect()
	}()
	return queue, done
}

func (q *batchedQueue) Ack(ctx context.Context, taskID, owner string, _ time.Time) error {
	request := ackRequest{ctx: ctx, item: mqstore.TaskAcknowledgment{TaskID: taskID, LeaseOwner: owner}, result: make(chan error, 1)}
	select {
	case q.requests <- request:
	case <-q.ctx.Done():
		return q.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.result:
		return err
	case <-q.ctx.Done():
		return q.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *batchedQueue) collect() {
	requests := make([]ackRequest, 0, q.limit)
	for {
		select {
		case <-q.ctx.Done():
			return
		case first := <-q.requests:
			requests = append(requests, first)
		}
		timer := time.NewTimer(q.window)
	collecting:
		for len(requests) < q.limit {
			select {
			case next := <-q.requests:
				requests = append(requests, next)
			case <-timer.C:
				break collecting
			case <-q.ctx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
		q.flush(requests)
		clear(requests)
		requests = requests[:0]
	}
}

func (q *batchedQueue) flush(requests []ackRequest) {
	active := make([]ackRequest, 0, len(requests))
	items := make([]mqstore.TaskAcknowledgment, 0, len(requests))
	for _, request := range requests {
		if err := request.ctx.Err(); err != nil {
			request.result <- err
			continue
		}
		active = append(active, request)
		items = append(items, request.item)
	}
	if len(active) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(q.ctx, 2*time.Second)
	defer cancel()
	now := time.Now()
	if len(active) == 1 {
		item := items[0]
		active[0].result <- q.SQLStore.Ack(ctx, item.TaskID, item.LeaseOwner, now)
		return
	}
	results, err := q.SQLStore.AckBatch(ctx, items, now)
	for i, request := range active {
		if err != nil {
			request.result <- err
		} else {
			request.result <- results[i]
		}
	}
}
