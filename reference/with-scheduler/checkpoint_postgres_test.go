//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresCheckpointResumesAfterWorkerCrash(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	t.Setenv("SCHEDULER_WORKER_POOL", "cpu")
	t.Setenv("SCHEDULER_WORKER_CONCURRENCY", "1")
	t.Setenv("SCHEDULER_WORKER_LEASE_DURATION", "4s")
	t.Setenv("SCHEDULER_WORKER_TASK_TIMEOUT", "20s")

	api := func(tenant string) http.Handler {
		t.Helper()
		app, err := newApp("", "token", tenant, repo)
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	app, other := api("tenant"), api("other")
	request := func(app http.Handler, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "checkpoint")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	response := request(app, http.MethodPost, "/runs",
		`{"topic":"demo.checkpoint","payload":{"shards":2,"items_per_shard":8,"segment_size":3,"output_version":2}}`, http.StatusAccepted)
	path := response.Header().Get("Location")
	id := strings.TrimPrefix(path, "/runs/")
	request(app, http.MethodGet, path+"/checkpoints", "", http.StatusNotFound)

	barrier := newCrashBarrier(t, db, 1, `BEFORE INSERT ON scheduler_checkpoint_segments
		FOR EACH ROW WHEN (NEW.shard=0 AND NEW.start_offset=3)`)
	original := startSchedulerChild(t, db, "worker")
	barrier.wait(t, db)
	checkpoint, err := readCheckpoints(ctx, db, "tenant", id)
	if err != nil || len(checkpoint.Cursors) != 2 || checkpoint.Cursors[0].NextOffset != 3 ||
		checkpoint.Cursors[0].Generation != 1 || checkpoint.Cursors[1].NextOffset != 0 || checkpoint.OutputVersion != 2 {
		t.Fatalf("pre-crash checkpoint: %+v %v", checkpoint, err)
	}
	var outputCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_checkpoint_segments WHERE run_id=$1", id).
		Scan(&outputCount); err != nil || outputCount != 1 {
		t.Fatalf("precommit segment escaped its transaction: outputs=%d %v", outputCount, err)
	}
	stale := mq.Task{ID: id, TenantID: "tenant"}
	if err := db.QueryRowContext(ctx, "SELECT attempts,lease_owner FROM mq_tasks WHERE id=$1", id).
		Scan(&stale.Attempts, &stale.LeaseOwner); err != nil {
		t.Fatal(err)
	}
	killSchedulerChild(t, original)
	barrier.abortWaiter(t, db)
	barrier.unlock(t)
	startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.runStatus(ctx, "tenant", id)
		return status.QueueStatus == "done" && status.ClaimAttempts == 2 && status.ExecutionAttempts == 2, err
	})
	if err := repo.commitCheckpointSegment(ctx, stale, 2, 0, 3, 6, segmentDigest(2, 0, 3, 6)); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("stale claim could still commit a segment: %v", err)
	}
	w := request(app, http.MethodGet, path+"/checkpoints", "", http.StatusOK)
	var receipt struct{ Data checkpointStatus }
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || len(receipt.Data.Cursors) != 2 ||
		receipt.Data.OutputVersion != 2 {
		t.Fatalf("checkpoint API: %s %v", w.Body.String(), err)
	}
	for _, cursor := range receipt.Data.Cursors {
		if cursor.NextOffset != 8 || cursor.Generation != 2 {
			t.Fatalf("unexpected terminal cursor: %+v", cursor)
		}
	}
	request(other, http.MethodGet, path+"/checkpoints", "", http.StatusNotFound)
	rows, err := db.QueryContext(ctx, `SELECT shard,start_offset,end_offset,output_version,generation,digest
		FROM scheduler_checkpoint_segments WHERE run_id=$1 ORDER BY shard,start_offset`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := 0
	for rows.Next() {
		var shard, start, end, version, generation int
		var output []byte
		if err := rows.Scan(&shard, &start, &end, &version, &generation, &output); err != nil {
			t.Fatal(err)
		}
		want := segmentDigest(2, shard, start, end)
		wantGeneration := 2
		if got == 0 {
			wantGeneration = 1
		}
		if generation != wantGeneration || version != 2 || !bytes.Equal(output, want[:]) ||
			shard != got/3 || start != got%3*3 || end != min(start+3, 8) {
			t.Fatalf("segment %d: shard=%d [%d,%d) version=%d generation=%d digest=%x",
				got, shard, start, end, version, generation, output)
		}
		got++
	}
	if err := rows.Err(); err != nil || got != 6 {
		t.Fatalf("missing or duplicate segment outputs: %d %v", got, err)
	}
}
