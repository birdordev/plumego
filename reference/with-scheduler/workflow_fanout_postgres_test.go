//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func fanoutFixture(count, limit int) workflowDefinition {
	task := submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)}
	return workflowDefinition{Nodes: []workflowNode{
		{ID: "map", Fanout: &workflowFanoutSpec{Count: count, MaxInFlight: limit, Task: task}},
		{ID: "join", DependsOn: []string{"map"}, Task: &task},
	}}
}

func readFanoutFixture(t *testing.T, ctx context.Context, repo repository, id string) (workflowStatus, workflowFanoutStatus) {
	t.Helper()
	status, err := repo.workflowStatus(ctx, "tenant", id)
	if err != nil || len(status.Nodes) != 2 || status.Nodes[1].Fanout == nil {
		t.Fatalf("fan-out status: %+v %v", status, err)
	}
	return status, *status.Nodes[1].Fanout
}

func TestPostgresWorkflowFanoutBarrierSurvivesPartialFailureAndRestart(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	flow, err := repo.createWorkflow(ctx, "tenant", "partial", fanoutFixture(5, 2))
	if err != nil {
		t.Fatal(err)
	}
	process := startSchedulerChild(t, db, "workflow")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		_, counts := readFanoutFixture(t, ctx, repo, flow)
		return counts.Created == 2 && counts.Active == 2, nil
	})
	killSchedulerChild(t, process)
	claimed, err := queue.ReserveFenced(ctx, mq.ReserveOptions{Topics: []string{recordTaskType}, Limit: 2,
		ConsumerID: "fanout-fixture", Lease: time.Minute, Now: time.Now()})
	if err != nil || len(claimed) != 2 {
		t.Fatalf("first page: %v %v", claimed, err)
	}
	if err := queue.MoveToDLQ(ctx, claimed[0].ID, claimed[0].LeaseOwner, "fixture failure", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.recordResult(ctx, claimed[1]); err != nil {
		t.Fatal(err)
	}
	if err := queue.Ack(ctx, claimed[1].ID, claimed[1].LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	status, counts := readFanoutFixture(t, ctx, repo, flow)
	if status.State != "running" || status.Nodes[0].State != "blocked" ||
		counts.Created != 2 || counts.Active != 0 || counts.Failed != 1 || counts.Succeeded != 1 {
		t.Fatalf("early barrier release: %+v %+v", status, counts)
	}
	startSchedulerChild(t, db, "workflow")
	t.Setenv("SCHEDULER_WORKER_POOL", "short")
	startSchedulerChild(t, db, "worker")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, counts = readFanoutFixture(t, ctx, repo, flow)
		return status.State == "failed", nil
	})
	if counts.Created != 5 || counts.Succeeded != 4 || counts.Failed != 1 || counts.Active != 0 ||
		status.Nodes[0].State != "skipped" || status.Nodes[0].RunID != "" {
		t.Fatalf("partial-failure barrier: %+v %+v", status, counts)
	}
	succeeded, err := repo.createWorkflow(ctx, "tenant", "success", fanoutFixture(7, 3))
	if err != nil {
		t.Fatal(err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, counts = readFanoutFixture(t, ctx, repo, succeeded)
		return status.State == "succeeded", nil
	})
	if counts.Succeeded != 7 || status.Nodes[0].RunID == "" || status.Nodes[0].State != "succeeded" {
		t.Fatalf("successful barrier failed to release exactly one successor: %+v %+v", status, counts)
	}
}

func TestPostgresWorkflowFanoutMillionItemBoundAndCancellation(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	flow, err := repo.createWorkflow(ctx, "tenant", "million", fanoutFixture(1000000, 130))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.dispatchWorkflowNode(ctx); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{32, 64, 96, 128, 130} {
		if _, err := repo.expandWorkflowFanout(ctx); err != nil {
			t.Fatal(err)
		}
		_, counts := readFanoutFixture(t, ctx, repo, flow)
		if counts.Total != 1000000 || counts.Created != want || counts.Active != want {
			t.Fatalf("page %d exceeded bounds or lost the cursor: %+v", i, counts)
		}
	}
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("one million logical children, 130 materialized, %d bytes allocated by creation and five pages", allocated)
	if allocated > 16<<20 {
		t.Fatal("fan-out allocated more than 16 MiB for the bounded working set")
	}
	if id, err := repo.expandWorkflowFanout(ctx); err != nil || id != "" {
		t.Fatalf("expanded a full barrier: %q %v", id, err)
	}
	first, err := repo.fanoutChildren(ctx, "tenant", flow, "map", 0)
	if err != nil || len(first.Items) != 128 || first.NextAfter != 128 {
		t.Fatalf("first child page: len=%d next=%d %v", len(first.Items), first.NextAfter, err)
	}
	second, err := repo.fanoutChildren(ctx, "tenant", flow, "map", first.NextAfter)
	if err != nil || len(second.Items) != 2 || second.Items[0].Ordinal != 129 || second.NextAfter != 0 {
		t.Fatalf("second child page: %+v %v", second, err)
	}
	claim, err := queue.ReserveFenced(ctx, mq.ReserveOptions{Topics: []string{recordTaskType}, Limit: 1,
		ConsumerID: "prefetch", Lease: time.Minute, Now: time.Now()})
	if err != nil || len(claim) != 1 {
		t.Fatalf("prefetch: %v %v", claim, err)
	}
	app, err := newApp("", "token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	other, err := newApp("", "token", "other", repo)
	if err != nil {
		t.Fatal(err)
	}
	call := func(app http.Handler, method, suffix string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, "/workflows/"+flow+"/fanouts/map/"+suffix, nil)
		req.Header.Set("Authorization", "Bearer token")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, suffix, w.Code, w.Body.String())
		}
	}
	call(other, http.MethodGet, "children", http.StatusNotFound)
	call(app, http.MethodGet, "children?after=128", http.StatusOK)
	call(other, http.MethodPost, "cancel", http.StatusNotFound)
	call(app, http.MethodPost, "cancel", http.StatusNoContent)
	if _, err := repo.beginExecution(ctx, claim[0]); !errors.Is(err, mq.ErrClaimPaused) {
		t.Fatalf("canceled fan-out still allowed a prefetched task to start: %v", err)
	}
	startSchedulerChild(t, db, "workflow")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, counts := readFanoutFixture(t, ctx, repo, flow)
		if status.State == "running" {
			return false, nil
		}
		if counts.Created != 130 || counts.Canceled != 130 || counts.Active != 0 ||
			!counts.CancelRequested || status.Nodes[0].State != "skipped" {
			t.Fatalf("canceled barrier: %+v %+v", status, counts)
		}
		return status.State == "failed", nil
	})
	call(app, http.MethodPost, "cancel", http.StatusNoContent)
}
