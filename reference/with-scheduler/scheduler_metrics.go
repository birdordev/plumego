package main

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

type latencyPhase int

const (
	triggerLatency latencyPhase = iota
	queueLatency
	executionLatency
	finalizationLatency
	dispatchLatency
)

var latencyNames = [...]string{
	"plumego_scheduler_trigger_delay_seconds",
	"plumego_scheduler_queue_delay_seconds",
	"plumego_scheduler_execution_duration_seconds",
	"plumego_scheduler_finalization_delay_seconds",
	"plumego_scheduler_dispatch_delay_seconds",
}

var latencyBounds = [...]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 15, 60, 300, 3600}

type latencyLabels struct {
	phase                   latencyPhase
	queue, taskType, result string
}

type latencyHistogram struct {
	counts [len(latencyBounds) + 1]uint64
	sum    float64
}

type schedulerMetrics struct {
	mu                   sync.Mutex
	series               map[latencyLabels]latencyHistogram
	readSLO              func(context.Context) (schedulerSLO, error)
	renewalFailures      atomic.Uint64
	finalizationFailures atomic.Uint64
	worker               *workerPoolMetrics
}

type workerPoolMetrics struct {
	name     string
	capacity int
	db       *sql.DB
	active   atomic.Int64
}

func (m *workerPoolMetrics) write(w io.Writer) {
	if m == nil {
		return
	}
	stats := m.db.Stats()
	fmt.Fprintf(w, "# TYPE plumego_scheduler_worker_active gauge\nplumego_scheduler_worker_active{pool=%q} %d\n", m.name, m.active.Load())
	fmt.Fprintf(w, "# TYPE plumego_scheduler_worker_capacity gauge\nplumego_scheduler_worker_capacity{pool=%q} %d\n", m.name, m.capacity)
	fmt.Fprintf(w, "# TYPE plumego_scheduler_worker_db_in_use gauge\nplumego_scheduler_worker_db_in_use{pool=%q} %d\n", m.name, stats.InUse)
	fmt.Fprintf(w, "# TYPE plumego_scheduler_worker_db_max_open gauge\nplumego_scheduler_worker_db_max_open{pool=%q} %d\n", m.name, stats.MaxOpenConnections)
	fmt.Fprintf(w, "# TYPE plumego_scheduler_worker_db_waits_total counter\nplumego_scheduler_worker_db_waits_total{pool=%q} %d\n", m.name, stats.WaitCount)
}

func (m *schedulerMetrics) observe(phase latencyPhase, topic, result string, duration time.Duration) {
	if m == nil {
		return
	}
	key := latencyLabels{phase: phase, queue: "unknown", taskType: "unknown", result: result}
	if contract, found := taskContractForTopic(topic); found {
		key.queue, key.taskType = topic, contract.taskType
	}
	seconds := max(duration.Seconds(), 0)
	bucket := sort.SearchFloat64s(latencyBounds[:], seconds)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.series == nil {
		m.series = make(map[latencyLabels]latencyHistogram)
	}
	histogram := m.series[key]
	histogram.counts[bucket]++
	histogram.sum += seconds
	m.series[key] = histogram
}

func (m *schedulerMetrics) handler(next mq.TaskHandler) mq.TaskHandler {
	if m == nil {
		return next
	}
	return func(ctx context.Context, task mq.Task) (err error) {
		if m.worker != nil {
			m.worker.active.Add(1)
			defer m.worker.active.Add(-1)
		}
		m.observe(queueLatency, task.Topic, "started", time.Since(task.AvailableAt))
		start := time.Now()
		result := "panic"
		defer func() {
			m.observe(executionLatency, task.Topic, result, time.Since(start))
		}()
		err = next(ctx, task)
		result = executionResult(ctx, err)
		return err
	}
}

func executionResult(ctx context.Context, err error) string {
	if err == nil && ctx.Err() == nil {
		return "succeeded"
	}
	var failure *mq.TaskFailure
	if errors.As(err, &failure) && failure != nil {
		switch failure.Kind {
		case mq.TaskFailurePermanent, mq.TaskFailureRateLimited, mq.TaskFailureOutcomeUnknown:
			return string(failure.Kind)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if err == nil {
			return "outcome_unknown"
		}
		return "timeout"
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return "canceled"
	}
	return "failed"
}

func (m *schedulerMetrics) finalization(topic, operation string, recordedAt time.Time) {
	if m == nil || recordedAt.IsZero() {
		return
	}
	switch operation {
	case "ack", "release", "dlq":
		m.observe(finalizationLatency, topic, operation, time.Since(recordedAt))
	}
}

func (m *schedulerMetrics) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	type sample struct {
		latencyLabels
		latencyHistogram
	}
	m.mu.Lock()
	samples := make([]sample, 0, len(m.series))
	for labels, histogram := range m.series {
		samples = append(samples, sample{labels, histogram})
	}
	m.mu.Unlock()
	slices.SortFunc(samples, func(a, b sample) int {
		return cmp.Or(cmp.Compare(a.phase, b.phase), cmp.Compare(a.queue, b.queue), cmp.Compare(a.result, b.result))
	})
	var payload strings.Builder
	m.worker.write(&payload)
	m.writeSLO(req.Context(), &payload)
	last := latencyPhase(-1)
	for _, point := range samples {
		name := latencyNames[point.phase]
		if last != point.phase {
			fmt.Fprintf(&payload, "# TYPE %s histogram\n", name)
			last = point.phase
		}
		labels := fmt.Sprintf("queue=%q,task_type=%q,result=%q", point.queue, point.taskType, point.result)
		var cumulative uint64
		for index, bound := range latencyBounds {
			cumulative += point.counts[index]
			fmt.Fprintf(&payload, "%s_bucket{%s,le=%q} %d\n", name, labels, fmt.Sprint(bound), cumulative)
		}
		cumulative += point.counts[len(latencyBounds)]
		fmt.Fprintf(&payload, "%s_bucket{%s,le=\"+Inf\"} %d\n", name, labels, cumulative)
		fmt.Fprintf(&payload, "%s_sum{%s} %g\n%s_count{%s} %d\n", name, labels, point.sum, name, labels, cumulative)
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = io.WriteString(w, payload.String())
}
