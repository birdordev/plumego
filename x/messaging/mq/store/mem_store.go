package store

import (
	"container/heap"
	"context"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

type MemStore struct {
	mu         sync.RWMutex
	tasks      map[string]*memRecord
	queued     map[string]*taskDeadlines
	spareQueue *taskDeadlines
	leased     taskDeadlines
	expires    taskDeadlines
	dedupe     map[string]string
	nowFunc    func() time.Time
}

type memRecord struct {
	task        mq.Task
	status      string
	lastError   string
	lastErrorAt time.Time
}

func NewMemory(cfg MemConfig) *MemStore {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &MemStore{
		tasks:   make(map[string]*memRecord),
		queued:  make(map[string]*taskDeadlines),
		leased:  taskDeadlines{kind: leaseDeadline},
		expires: taskDeadlines{kind: expiryDeadline},
		dedupe:  make(map[string]string),
		nowFunc: cfg.Now,
	}
}

var _ mq.TaskStore = (*MemStore)(nil)

// Insert stores a task with its own payload and metadata.
func (m *MemStore) Insert(_ context.Context, task mq.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tasks[task.ID]; exists {
		return mq.ErrDuplicateTask
	}
	if task.DedupeKey != "" {
		key := dedupeKey(task.TenantID, task.DedupeKey)
		if _, exists := m.dedupe[key]; exists {
			return mq.ErrDuplicateTask
		}
		m.dedupe[key] = task.ID
	}

	rec := &memRecord{task: cloneTask(task), status: mq.TaskStatusQueued}
	m.tasks[task.ID] = rec
	m.queueRecord(rec)
	return nil
}

// Reserve returns leased tasks with caller-owned payloads and metadata.
func (m *MemStore) Reserve(_ context.Context, opts mq.ReserveOptions) ([]mq.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if opts.Limit <= 0 {
		return nil, nil
	}
	if opts.Now.IsZero() {
		opts.Now = m.nowFunc()
	}
	if opts.Lease <= 0 {
		opts.Lease = mq.DefaultLeaseDuration
	}

	var candidates reserveCandidates
	bounded := opts.Limit <= 64
	for m.expires.Len() != 0 && !m.expires.deadline(m.expires.entries[0]).After(opts.Now) {
		rec := m.expires.entries[0]
		m.unqueueRecord(rec)
		rec.status = mq.TaskStatusExpired
	}

	consider := func(rec *memRecord) {
		if !bounded {
			candidates = append(candidates, rec)
		} else if len(candidates) < opts.Limit {
			heap.Push(&candidates, rec)
		} else if compareReserveCandidates(rec, candidates[0]) < 0 {
			candidates[0] = rec
			if opts.Limit > 1 {
				heap.Fix(&candidates, 0)
			}
		}
	}
	if len(opts.Topics) == 0 {
		for _, queue := range m.queued {
			queue.visit(0, opts.Now, consider)
		}
	} else {
		for i, topic := range opts.Topics {
			if slices.Contains(opts.Topics[:i], topic) {
				continue
			}
			if queue := m.queued[topic]; queue != nil {
				queue.visit(0, opts.Now, consider)
			}
		}
	}
	var expiredLeases []*memRecord
	m.leased.visit(0, opts.Now, func(rec *memRecord) {
		if rec.task.LeaseUntil.IsZero() || !rec.task.LeaseUntil.Before(opts.Now) {
			return
		}
		if !rec.task.ExpiresAt.IsZero() && !rec.task.ExpiresAt.After(opts.Now) {
			expiredLeases = append(expiredLeases, rec)
			return
		}
		if !rec.task.AvailableAt.After(opts.Now) && (len(opts.Topics) == 0 || topicAllowed(rec.task.Topic, opts.Topics)) {
			consider(rec)
		}
	})
	for _, rec := range expiredLeases {
		m.leased.remove(rec)
		rec.status = mq.TaskStatusExpired
	}
	slices.SortFunc(candidates, compareReserveCandidates)

	limit := opts.Limit
	if limit > len(candidates) {
		limit = len(candidates)
	}

	selected := make([]mq.Task, 0, limit)
	for i := 0; i < limit; i++ {
		rec := candidates[i]
		if rec.status == mq.TaskStatusQueued {
			m.unqueueRecord(rec)
			m.leased.push(rec)
		}
		rec.status = mq.TaskStatusLeased
		rec.task.LeaseOwner = opts.ConsumerID
		rec.task.LeaseUntil = opts.Now.Add(opts.Lease)
		m.leased.fix(rec)
		rec.task.Attempts++
		rec.task.UpdatedAt = opts.Now
		selected = append(selected, cloneTask(rec.task))
	}

	return selected, nil
}

func compareReserveCandidates(a, b *memRecord) int {
	if a.task.Priority > b.task.Priority {
		return -1
	}
	if a.task.Priority < b.task.Priority {
		return 1
	}
	return a.task.CreatedAt.Compare(b.task.CreatedAt)
}

type reserveCandidates []*memRecord

func (c reserveCandidates) Len() int           { return len(c) }
func (c reserveCandidates) Less(i, j int) bool { return compareReserveCandidates(c[i], c[j]) > 0 }
func (c reserveCandidates) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }
func (c *reserveCandidates) Push(value any)    { *c = append(*c, value.(*memRecord)) }
func (c *reserveCandidates) Pop() any {
	last := len(*c) - 1
	value := (*c)[last]
	(*c)[last] = nil
	*c = (*c)[:last]
	return value
}

func cloneTask(task mq.Task) mq.Task {
	task.Payload = slices.Clone(task.Payload)
	task.Meta = maps.Clone(task.Meta)
	return task
}

func (m *MemStore) Ack(_ context.Context, taskID, consumerID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.tasks[taskID]
	if !ok {
		return mq.ErrTaskNotFound
	}
	if rec.status != mq.TaskStatusLeased || rec.task.LeaseOwner != consumerID || (!rec.task.LeaseUntil.IsZero() && rec.task.LeaseUntil.Before(now)) {
		return mq.ErrLeaseLost
	}

	rec.status = mq.TaskStatusDone
	m.leased.remove(rec)
	rec.task.LeaseOwner = ""
	rec.task.LeaseUntil = time.Time{}
	rec.task.UpdatedAt = now
	return nil
}

func (m *MemStore) Release(_ context.Context, taskID, consumerID string, opts mq.ReleaseOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.tasks[taskID]
	if !ok {
		return mq.ErrTaskNotFound
	}
	if rec.status != mq.TaskStatusLeased || rec.task.LeaseOwner != consumerID || (!rec.task.LeaseUntil.IsZero() && rec.task.LeaseUntil.Before(opts.Now)) {
		return mq.ErrLeaseLost
	}

	m.leased.remove(rec)
	rec.status = mq.TaskStatusQueued
	rec.task.LeaseOwner = ""
	rec.task.LeaseUntil = time.Time{}
	rec.task.AvailableAt = opts.RetryAt
	m.queueRecord(rec)
	rec.task.UpdatedAt = opts.Now
	rec.lastError = opts.Reason
	rec.lastErrorAt = opts.Now
	return nil
}

func (m *MemStore) MoveToDLQ(_ context.Context, taskID, consumerID, reason string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.tasks[taskID]
	if !ok {
		return mq.ErrTaskNotFound
	}
	if rec.status != mq.TaskStatusLeased || rec.task.LeaseOwner != consumerID || (!rec.task.LeaseUntil.IsZero() && rec.task.LeaseUntil.Before(now)) {
		return mq.ErrLeaseLost
	}

	rec.status = mq.TaskStatusDead
	m.leased.remove(rec)
	rec.task.LeaseOwner = ""
	rec.task.LeaseUntil = time.Time{}
	rec.task.UpdatedAt = now
	rec.lastError = reason
	rec.lastErrorAt = now
	return nil
}

func (m *MemStore) ExtendLease(_ context.Context, taskID, consumerID string, lease time.Duration, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.tasks[taskID]
	if !ok {
		return mq.ErrTaskNotFound
	}
	if rec.status != mq.TaskStatusLeased || rec.task.LeaseOwner != consumerID || (!rec.task.LeaseUntil.IsZero() && rec.task.LeaseUntil.Before(now)) {
		return mq.ErrLeaseLost
	}

	if lease <= 0 {
		lease = mq.DefaultLeaseDuration
	}
	rec.task.LeaseUntil = now.Add(lease)
	m.leased.fix(rec)
	rec.task.UpdatedAt = now
	return nil
}

func (m *MemStore) Stats(_ context.Context) (mq.Stats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var stats mq.Stats
	for _, rec := range m.tasks {
		switch rec.status {
		case mq.TaskStatusQueued:
			stats.Queued++
		case mq.TaskStatusLeased:
			stats.Leased++
		case mq.TaskStatusDead:
			stats.Dead++
		case mq.TaskStatusExpired:
			stats.Expired++
		}
	}
	return stats, nil
}

// ReplayDLQ moves tasks from the dead-letter state back to queued.
func (m *MemStore) ReplayDLQ(_ context.Context, opts ReplayOptions) (ReplayResult, error) {
	if m == nil {
		return ReplayResult{}, mq.ErrNotInitialized
	}

	now := opts.Now
	if now.IsZero() {
		now = m.nowFunc()
	}
	availableAt := opts.AvailableAt
	if availableAt.IsZero() {
		availableAt = now
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dead := make([]*memRecord, 0)
	for _, rec := range m.tasks {
		if rec.status == mq.TaskStatusDead {
			dead = append(dead, rec)
		}
	}

	if len(dead) == 0 {
		return ReplayResult{Replayed: 0, Remaining: 0}, nil
	}

	sort.Slice(dead, func(i, j int) bool {
		return dead[i].task.UpdatedAt.Before(dead[j].task.UpdatedAt)
	})

	limit := opts.Max
	if limit <= 0 || limit > len(dead) {
		limit = len(dead)
	}

	for i := 0; i < limit; i++ {
		rec := dead[i]
		rec.status = mq.TaskStatusQueued
		rec.task.LeaseOwner = ""
		rec.task.LeaseUntil = time.Time{}
		rec.task.AvailableAt = availableAt
		m.queueRecord(rec)
		rec.task.UpdatedAt = now
		if opts.ResetAttempts {
			rec.task.Attempts = 0
		}
	}

	return ReplayResult{
		Replayed:  limit,
		Remaining: len(dead) - limit,
	}, nil
}

func dedupeKey(tenantID, dedupe string) string {
	return tenantID + ":" + dedupe
}

func topicAllowed(topic string, topics []string) bool {
	for _, t := range topics {
		if t == topic {
			return true
		}
	}
	return false
}
