package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func loadProtectedTenants(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	tenants := strings.Split(raw, ",")
	if len(tenants) > 32 {
		return nil, errors.New("too many protected Worker tenants")
	}
	for i, tenant := range tenants {
		tenant = strings.TrimSpace(tenant)
		if !runCursorID.MatchString(tenant) || slices.Contains(tenants[:i], tenant) {
			return nil, errors.New("invalid protected Worker tenant")
		}
		tenants[i] = tenant
	}
	return tenants, nil
}

type fairClaimQueue struct {
	mq.TaskStore
	store   *mqstore.SQLStore
	tenants []string
	turn    atomic.Uint64
}

func (q *fairClaimQueue) Reserve(ctx context.Context, options mq.ReserveOptions) ([]mq.Task, error) {
	if options.Limit <= 0 || len(q.tenants) == 0 {
		return q.TaskStore.Reserve(ctx, options)
	}
	turn := (q.turn.Add(1) - 1) % uint64(len(q.tenants)+1)
	if turn == uint64(len(q.tenants)) {
		return q.TaskStore.Reserve(ctx, options)
	}
	protected := options
	protected.Limit = 1
	tasks, err := q.store.ReserveTenant(ctx, protected, q.tenants[turn])
	if err != nil || len(tasks) > 0 {
		return tasks, err
	}
	return q.TaskStore.Reserve(ctx, options)
}
