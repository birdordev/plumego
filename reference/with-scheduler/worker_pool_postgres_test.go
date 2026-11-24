//go:build unix

package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresShortPoolSLODuringLongIOPoolSaturation(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	start := func(pool, concurrency, connections string) string {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SCHEDULER_WORKER_POOL", pool)
		t.Setenv("SCHEDULER_WORKER_CONCURRENCY", concurrency)
		t.Setenv("SCHEDULER_WORKER_DB_MAX_OPEN", connections)
		t.Setenv("SCHEDULER_WORKER_DB_MAX_IDLE", "0")
		t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "")
		t.Setenv("SCHEDULER_METRICS_ADDR", address)
		startSchedulerChild(t, db, "worker")
		return "http://" + address + "/metrics"
	}
	client := &http.Client{Timeout: time.Second}
	scrape := func(endpoint string, lines ...string) {
		t.Helper()
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return false, err
			}
			response, err := client.Do(req)
			if err != nil {
				return false, nil
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				return false, err
			}
			for _, line := range lines {
				if !strings.Contains(string(body), line+"\n") {
					return false, nil
				}
			}
			return response.StatusCode == http.StatusOK, nil
		})
	}
	submit := func(key, taskType, payload string) string {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", key, submission{Topic: taskType, Payload: json.RawMessage(payload)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	shortMetrics := start("short", "1", "4")
	cpuMetrics := start("cpu", "1", "3")
	ioMetrics := start("io", "2", "2")
	scrape(shortMetrics, `plumego_scheduler_worker_capacity{pool="short"} 1`, `plumego_scheduler_worker_db_max_open{pool="short"} 4`)
	scrape(cpuMetrics, `plumego_scheduler_worker_capacity{pool="cpu"} 1`, `plumego_scheduler_worker_db_max_open{pool="cpu"} 3`)
	first := submit("io-1", ioTaskType, `{"delay_ms":8000}`)
	second := submit("io-2", ioTaskType, `{"delay_ms":8000}`)
	scrape(ioMetrics, `plumego_scheduler_worker_active{pool="io"} 2`, `plumego_scheduler_worker_db_in_use{pool="io"} 2`,
		`plumego_scheduler_worker_capacity{pool="io"} 2`, `plumego_scheduler_worker_db_max_open{pool="io"} 2`)
	backlog := submit("io-backlog", ioTaskType, `{"delay_ms":8000}`)
	short := submit("short", recordTaskType, `{}`)
	cpu := submit("cpu", computeTaskType, `{}`)
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var done int
		err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks WHERE id IN ($1, $2) AND status = 'done'", short, cpu).Scan(&done)
		return done == 2, err
	})
	var elapsed float64
	var ioCompleted int
	if err := db.QueryRowContext(ctx, `SELECT extract(epoch FROM r.completed_at - q.available_at),
		(SELECT COUNT(*) FROM scheduled_results WHERE run_id IN ($2, $3))
		FROM scheduled_results r JOIN mq_tasks q ON q.id = r.run_id WHERE r.run_id = $1`, short, first, second).Scan(&elapsed, &ioCompleted); err != nil {
		t.Fatal(err)
	}
	t.Logf("short task completed in %.3fs with two active I/O jobs and a saturated I/O database pool", elapsed)
	if elapsed > 2 || ioCompleted != 0 {
		t.Fatalf("short-pool SLO failed: elapsed=%.3f io_completed=%d", elapsed, ioCompleted)
	}
	slo, err := repo.readSLO(ctx)
	if err != nil || slo.io <= 0 || slo.short != 0 || slo.cpu != 0 {
		t.Fatalf("incorrect per-pool runnable backlog: %+v %v", slo, err)
	}
	scrape(cpuMetrics, `plumego_scheduler_execution_duration_seconds_count{queue="demo.compute",task_type="demo.compute",result="succeeded"} 1`)
	if err := repo.cancelRun(ctx, "tenant", backlog); err != nil {
		t.Fatal(err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var done int
		err := db.QueryRowContext(ctx, "SELECT count(*) FROM mq_tasks WHERE id IN ($1, $2) AND status = 'done'", first, second).Scan(&done)
		return done == 2, err
	})
}
