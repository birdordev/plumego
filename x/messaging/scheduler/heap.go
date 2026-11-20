package scheduler

import (
	"container/heap"
	"sync/atomic"
	"time"
)

type jobKind int

const (
	jobKindCron jobKind = iota
	jobKindDelay
)

type job struct {
	id                   JobID
	kind                 jobKind
	cron                 CronSpec
	cronExpr             string
	runAt                time.Time
	fn                   TaskFunc
	options              JobOptions
	paused               atomic.Bool // atomic to prevent race condition in dispatch
	canceled             atomic.Bool // atomic to prevent race condition in dispatch
	nextAttempt          int
	running              atomic.Bool
	lastRun              time.Time
	lastError            error
	pending              atomic.Bool // atomic to prevent race between dispatch and execute
	state                JobState
	stateAt              time.Time
	runCount             int64 // total successful executions; protected by scheduler mu
	scheduleVersion      uint64
	resolvedDependencies map[JobID]struct{} // per dependent; protected by scheduler mu
}

type runRequest struct {
	job        *job
	enqueuedAt time.Time
}

type scheduleItem struct {
	runAt   time.Time
	job     *job
	version uint64
	index   int
}

type scheduleHeap []*scheduleItem

func (h scheduleHeap) Len() int { return len(h) }

func (h scheduleHeap) Less(i, j int) bool {
	return h[i].runAt.Before(h[j].runAt)
}

func (h scheduleHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *scheduleHeap) Push(x any) {
	item := x.(*scheduleItem)
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *scheduleHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	item.index = -1
	*h = old[:n-1]
	return item
}

func (h *scheduleHeap) Peek() *scheduleItem {
	if len(*h) == 0 {
		return nil
	}
	return (*h)[0]
}

func (h *scheduleHeap) PushSchedule(item *scheduleItem) {
	heap.Push(h, item)
}

func (h *scheduleHeap) PopDue(now time.Time) *scheduleItem {
	if len(*h) == 0 {
		return nil
	}
	item := (*h)[0]
	if item.runAt.After(now) {
		return nil
	}
	return heap.Pop(h).(*scheduleItem)
}
