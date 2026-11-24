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

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresWorkflowWaitEarlyDuplicateLateAndRestart(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	api := func(tenant, actor string) http.Handler {
		t.Helper()
		app, err := newAppWithActor("", "test-token", tenant, actor, repo)
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	sender := api("tenant", "approver")
	forbidden := api("tenant", "viewer")
	outsider := api("other", "approver")
	post := func(app http.Handler, route, key string, payload any, want int) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		app.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("POST %s = %d: %s", route, out.Code, out.Body.String())
		}
		return out
	}
	task := &submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)}
	wait := &workflowWaitSpec{Key: "review", Actor: "approver", Deadline: time.Now().Add(time.Minute)}
	create := func(key string, prerequisite bool) string {
		t.Helper()
		n := workflowNode{ID: "approval", Wait: wait}
		nodes := []workflowNode{{ID: "after", DependsOn: []string{"approval"}, Task: task}}
		if prerequisite {
			n.DependsOn = []string{"before"}
			nodes = append(nodes, workflowNode{ID: "before", Task: task})
		}
		nodes = append(nodes, n)
		out := post(sender, "/workflows", key, workflowDefinition{Nodes: nodes}, http.StatusAccepted)
		return strings.TrimPrefix(out.Header().Get("Location"), "/workflows/")
	}
	signal := func(app http.Handler, flow string, status int) string {
		t.Helper()
		out := post(app, "/workflows/"+flow+"/signals", "", map[string]string{"key": "review"}, status)
		if status != http.StatusOK {
			return ""
		}
		var receipt struct {
			Data map[string]string
		}
		if err := json.Unmarshal(out.Body.Bytes(), &receipt); err != nil || receipt.Data["received_at"] == "" {
			t.Fatalf("invalid signal response: %s %v", out.Body.String(), err)
		}
		return receipt.Data["received_at"]
	}
	expect := func(flow, id, state string) workflowNodeStatus {
		t.Helper()
		var node workflowNodeStatus
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			status, err := repo.workflowStatus(ctx, "tenant", flow)
			for _, item := range status.Nodes {
				if item.ID == id {
					node = item
				}
			}
			return node.State == state, err
		})
		return node
	}

	early, live, late := create("early", true), create("live", false), create("late", false)
	signal(outsider, early, http.StatusNotFound)
	signal(forbidden, early, http.StatusForbidden)
	first := signal(sender, early, http.StatusOK)
	if duplicate := signal(sender, early, http.StatusOK); duplicate != first {
		t.Fatalf("early duplicate changed its receipt: %s vs %s", duplicate, first)
	}
	expect(early, "approval", "blocked")
	process := startSchedulerChild(t, db, "workflow")
	expect(early, "before", "queued")
	if node := expect(live, "approval", "waiting"); node.RunID != "" || node.SignalKey != "review" || node.Deadline == nil {
		t.Fatalf("invalid wait node: %+v", node)
	}
	expect(late, "approval", "waiting")
	killSchedulerChild(t, process)

	// 模拟流程进程离线期间截止。提前接收的信号在截止后激活仍有效。
	if _, err := db.ExecContext(ctx, `UPDATE scheduler_workflow_waits SET deadline = CASE
		WHEN signal_at IS NULL THEN clock_timestamp() - interval '1 second'
		ELSE signal_at + interval '1 microsecond' END WHERE workflow_id IN ($1,$2)`, early, late); err != nil {
		t.Fatal(err)
	}
	signal(sender, late, http.StatusGone)
	expect(late, "approval", "waiting")
	second := signal(sender, live, http.StatusOK)
	if duplicate := signal(sender, live, http.StatusOK); duplicate != second {
		t.Fatalf("duplicate changed its receipt: %s vs %s", duplicate, second)
	}
	expect(live, "after", "ready")
	if afterDeadline := signal(sender, early, http.StatusOK); afterDeadline != first {
		t.Fatal("accepted signal could not be replayed after deadline")
	}

	t.Setenv("SCHEDULER_WORKER_POOL", "short")
	t.Setenv("SCHEDULER_WORKER_CONCURRENCY", "1")
	startSchedulerChild(t, db, "worker")
	expect(early, "before", "succeeded")
	expect(early, "approval", "ready")
	startSchedulerChild(t, db, "workflow")
	expect(early, "after", "succeeded")
	expect(live, "after", "succeeded")
	expect(late, "approval", "failed")
	expect(late, "after", "skipped")
	var runs, accepted int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM mq_tasks),
		(SELECT count(*) FROM scheduler_audit WHERE operation='workflow.signal')`).Scan(&runs, &accepted); err != nil || runs != 3 || accepted != 2 {
		t.Fatalf("waits consumed queue slots or duplicate audit: tasks=%d signals=%d %v", runs, accepted, err)
	}
}
