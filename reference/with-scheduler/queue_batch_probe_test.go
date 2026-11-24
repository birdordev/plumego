package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresQueueBatchProbe(t *testing.T) {
	if os.Getenv("PLUMEGO_TEST_QUEUE_CAPACITY") != "1" {
		t.Skip("set PLUMEGO_TEST_QUEUE_CAPACITY=1 to run the capacity probe")
	}
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	cfg := mqstore.DefaultSQLConfig()
	cfg.DeferredExpiration = true
	queue := mqstore.NewSQL(db, cfg)
	repo := repository{db, queue}
	var version string
	if err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("PostgreSQL %s", version)

	const groupSize, samples = 32, 32
	type probeStats struct {
		latencies []time.Duration
		total     time.Duration
		wal       int64
	}
	var submits, acknowledgments [2]probeStats
	measure := func(stats *probeStats, action func()) {
		t.Helper()
		var origin string
		if err := db.QueryRowContext(ctx, "SELECT pg_current_wal_insert_lsn()::text").Scan(&origin); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		action()
		elapsed := time.Since(start)
		var bytes int64
		err := db.QueryRowContext(ctx, "SELECT pg_wal_lsn_diff(pg_current_wal_insert_lsn(), $1::text::pg_lsn)::bigint", origin).Scan(&bytes)
		if err != nil {
			t.Fatal(err)
		}
		stats.latencies = append(stats.latencies, elapsed)
		stats.total += elapsed
		stats.wal += bytes
	}
	for sample := range samples {
		for offset := range 2 {
			mode := (sample + offset) % 2
			input := make([]batchSubmission, groupSize)
			for i := range input {
				input[i] = batchSubmission{submission: submission{Topic: "demo.record", Payload: json.RawMessage(`{"short":"task"}`)},
					Key: fmt.Sprintf("bench-%d-%d-%d", mode, sample, i)}
			}
			measure(&submits[mode], func() {
				if mode == 0 {
					for _, item := range input {
						if _, err := repo.submit(ctx, "tenant", item.Key, item.submission); err != nil {
							t.Fatal(err)
						}
					}
					return
				}
				if ids, err := repo.submitBatch(ctx, "tenant", input); err != nil || len(ids) != groupSize {
					t.Fatalf("batch insert count=%d err=%v", len(ids), err)
				}
			})
			tasks, err := mq.NewTaskQueue(queue).ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "probe", Limit: groupSize})
			if err != nil || len(tasks) != groupSize {
				t.Fatalf("claim count=%d err=%v", len(tasks), err)
			}
			items := make([]mqstore.TaskAcknowledgment, len(tasks))
			for i, task := range tasks {
				items[i] = mqstore.TaskAcknowledgment{TaskID: task.ID, LeaseOwner: task.LeaseOwner}
			}
			measure(&acknowledgments[mode], func() {
				if mode == 0 {
					for _, item := range items {
						if err := queue.Ack(ctx, item.TaskID, item.LeaseOwner, time.Now()); err != nil {
							t.Fatal(err)
						}
					}
					return
				}
				results, err := queue.AckBatch(ctx, items, time.Now())
				if err != nil || len(results) != groupSize {
					t.Fatalf("batch acknowledgment count=%d err=%v", len(results), err)
				}
				for _, err := range results {
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
	report := func(label string, stats probeStats) {
		t.Helper()
		slices.Sort(stats.latencies)
		p95 := stats.latencies[(samples*95+99)/100-1]
		t.Logf("%s: %.1f items/s; group of %d p95=%s; WAL %d bytes (%.1f bytes/item)",
			label, (samples*groupSize)/stats.total.Seconds(), groupSize, p95, stats.wal, float64(stats.wal)/(samples*groupSize))
	}
	report("single submission", submits[0])
	report("batch submission", submits[1])
	report("single acknowledgment", acknowledgments[0])
	report("batch acknowledgment", acknowledgments[1])
}
