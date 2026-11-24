//go:build unix

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresWorkflowDiamondSurvivesDispatcherRestart(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("", "token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	other, err := newApp("", "token", "other", repo)
	if err != nil {
		t.Fatal(err)
	}
	request := func(handler http.Handler, method, path, body, key string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	const graph = `{"nodes":[
		{"id":"root","task":{"topic":"demo.record","payload":{}}},
		{"id":"left","depends_on":["root"],"task":{"topic":"demo.record","payload":{}}},
		{"id":"right","depends_on":["root"],"task":{"topic":"demo.record","payload":{}}},
		{"id":"join","depends_on":["left","right"],"task":{"topic":"demo.record","payload":{}}}
	]}`
	create := func(key string) string {
		t.Helper()
		response := request(app, http.MethodPost, "/workflows", graph, key, http.StatusAccepted)
		return strings.TrimPrefix(response.Header().Get("Location"), "/workflows/")
	}
	flow, separate := create("first"), create("second")
	if flow == "" || flow == separate || create("first") != flow {
		t.Fatal("workflow identities did not isolate exact replays from separate instances")
	}
	request(app, http.MethodPost, "/workflows", strings.Replace(graph, "demo.record", "demo.compute", 1),
		"first", http.StatusConflict)
	request(app, http.MethodPost, "/workflows",
		strings.Replace(graph, `"id":"root"`, `"id":"root","depends_on":["join"]`, 1), "cycle", http.StatusBadRequest)
	request(other, http.MethodGet, "/workflows/"+flow, "", "", http.StatusNotFound)
	node := func(flowID, id, state string) workflowNodeStatus {
		t.Helper()
		var found workflowNodeStatus
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			flow, err := repo.workflowStatus(ctx, "tenant", flowID)
			for _, n := range flow.Nodes {
				if n.ID == id {
					found = n
				}
			}
			return found.State == state, err
		})
		return found
	}
	first := startSchedulerChild(t, db, "workflow")
	second := startSchedulerChild(t, db, "workflow")
	a := node(flow, "root", "queued")
	b := node(separate, "root", "queued")
	if a.RunID == b.RunID {
		t.Fatal("independent DAGs shared a run")
	}
	claim := func(limit int) map[string]mq.Task {
		t.Helper()
		tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{Topics: []string{recordTaskType}, Limit: limit,
			ConsumerID: "workflow-fixture", Lease: time.Minute, Now: time.Now()})
		if err != nil || len(tasks) != limit {
			t.Fatalf("reserve %d: got %v %v", limit, tasks, err)
		}
		got := make(map[string]mq.Task, limit)
		for _, task := range tasks {
			got[task.ID] = task
		}
		return got
	}
	roots := claim(2)
	if _, ok := roots[b.RunID]; !ok {
		t.Fatal("independent root not claimed")
	}
	if err := repo.cancelRun(ctx, "tenant", b.RunID); err != nil {
		t.Fatal(err)
	}
	node(separate, "join", "skipped")
	ack := func(task mq.Task) error {
		return queue.Ack(ctx, task.ID, task.LeaseOwner, time.Now())
	}
	succeed := func(task mq.Task) {
		t.Helper()
		if err := repo.recordResult(ctx, task); err != nil {
			t.Fatal(err)
		}
		if err := ack(task); err != nil {
			t.Fatal(err)
		}
	}
	succeed(roots[a.RunID])
	left, right := node(flow, "left", "queued"), node(flow, "right", "queued")
	killSchedulerChild(t, first)
	killSchedulerChild(t, second)
	branches := claim(2)
	succeed(branches[left.RunID])
	if n := node(flow, "join", "blocked"); n.RunID != "" {
		t.Fatal("join started before its second predecessor completed")
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE scheduler_workflow_nodes ADD CONSTRAINT reject_join_ready
		CHECK (node_id <> 'join' OR state <> 'ready')`); err != nil {
		t.Fatal(err)
	}
	pending := branches[right.RunID]
	if err := repo.recordResult(ctx, pending); err != nil {
		t.Fatal(err)
	}
	if err := ack(pending); err == nil {
		t.Fatal("predecessor committed even though successor readiness failed")
	}
	node(flow, "right", "queued")
	status, err := repo.runStatus(ctx, "tenant", pending.ID)
	if err != nil || status.QueueStatus != "leased" {
		t.Fatalf("failed transition did not roll back: %+v %v", status, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE scheduler_workflow_nodes DROP CONSTRAINT reject_join_ready"); err != nil {
		t.Fatal(err)
	}
	if err := ack(pending); err != nil {
		t.Fatal(err)
	}
	node(flow, "join", "ready")
	startSchedulerChild(t, db, "workflow")
	node(flow, "join", "queued")
	t.Setenv("SCHEDULER_WORKER_POOL", "short")
	startSchedulerChild(t, db, "worker")
	node(flow, "join", "succeeded")
	success, err := repo.workflowStatus(ctx, "tenant", flow)
	if err != nil || success.State != "succeeded" {
		t.Fatalf("successful flow: %+v %v", success, err)
	}
	failure, err := repo.workflowStatus(ctx, "tenant", separate)
	if err != nil || failure.State != "failed" {
		t.Fatalf("failed flow: %+v %v", failure, err)
	}
	var tasks int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_runs").Scan(&tasks); err != nil || tasks != 5 {
		t.Fatalf("duplicate or missing dispatch: runs=%d %v", tasks, err)
	}
}

func TestPostgresWorkflowConcurrentJoinAndTenantCapacity(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	task := submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)}
	graph := workflowDefinition{Nodes: []workflowNode{
		{ID: "first", Task: &task}, {ID: "second", Task: &task},
		{ID: "join", DependsOn: []string{"first", "second"}, Task: &task},
	}}
	if _, err := db.ExecContext(ctx, "INSERT INTO scheduler_tenant_usage (tenant_id,max_pending_runs) VALUES ('full',1)"); err != nil {
		t.Fatal(err)
	}
	filler, err := repo.submit(ctx, "full", "filler", submission{
		Topic: recordTaskType, Payload: json.RawMessage(`{}`), RunAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := repo.createWorkflow(ctx, "full", "blocked", workflowDefinition{
		Nodes: []workflowNode{{ID: "root", Task: &task}},
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := repo.createWorkflow(ctx, "tenant", "parallel", graph)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if id, err := repo.dispatchWorkflowNode(ctx); err != nil || id == "" {
			t.Fatalf("dispatch at capacity: %q %v", id, err)
		}
	}
	throttled, err := repo.workflowStatus(ctx, "full", blocked)
	if err != nil || len(throttled.Nodes) != 1 || throttled.Nodes[0].State != "ready" ||
		throttled.Nodes[0].RunID != "" {
		t.Fatalf("quota deferral lost ready node: %+v %v", throttled, err)
	}

	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{Topics: []string{recordTaskType}, Limit: 2,
		ConsumerID: "parallel", Lease: time.Minute, Now: time.Now()})
	if err != nil || len(tasks) != 2 {
		t.Fatalf("claim parallel roots: %v %v", tasks, err)
	}
	for _, claimed := range tasks {
		if err := repo.recordResult(ctx, claimed); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, len(tasks))
	for _, claimed := range tasks {
		go func() {
			<-start
			results <- queue.Ack(ctx, claimed.ID, claimed.LeaseOwner, time.Now())
		}()
	}
	close(start)
	for range tasks {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	flow, err := repo.workflowStatus(ctx, "tenant", active)
	if err != nil || len(flow.Nodes) != 3 || flow.Nodes[1].ID != "join" ||
		flow.Nodes[1].State != "ready" || flow.Nodes[0].State != "succeeded" || flow.Nodes[2].State != "succeeded" {
		t.Fatalf("parallel completions lost the fan-in: %+v %v", flow, err)
	}
	if err := repo.cancelRun(ctx, "full", filler); err != nil {
		t.Fatal(err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		if _, err := repo.dispatchWorkflowNode(ctx); err != nil {
			return false, err
		}
		status, err := repo.workflowStatus(ctx, "full", blocked)
		return err == nil && status.Nodes[0].State == "queued", err
	})
}
