//go:build unix

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresWorkflowVersionRolloutAndRollback(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	t.Setenv("SCHEDULER_WORKFLOW_RELEASE_ACTOR", "publisher")
	api := func(tenant, actor string) http.Handler {
		t.Helper()
		app, err := newAppWithActor("", "token", tenant, actor, repo)
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	publisher := api("tenant", "publisher")
	client := api("tenant", "client")
	other := api("other", "publisher")
	request := func(app http.Handler, method, path, key, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w
	}
	const defs = "/workflow-definitions/review"
	const graph1 = `{"nodes":[
		{"id":"gate","wait":{"key":"approve","actor":"client","deadline":"2099-01-01T00:00:00Z"}},
		{"id":"work","depends_on":["gate"],"task":{"topic":"demo.record","payload":{}}}
	]}`
	graph2 := strings.Replace(graph1, `"payload":{}`, `"task_version":2,"payload":{"message":"v2"}`, 1)
	request(client, http.MethodPut, defs+"/versions/1", "", graph1, http.StatusForbidden)
	request(publisher, http.MethodPut, defs+"/versions/1", "", graph1, http.StatusNoContent)
	request(publisher, http.MethodPut, defs+"/versions/1", "", graph1, http.StatusNoContent)
	request(publisher, http.MethodPut, defs+"/versions/1", "", graph2, http.StatusConflict)
	request(publisher, http.MethodPut, defs+"/versions/2", "", graph2, http.StatusNoContent)
	request(other, http.MethodPut, defs+"/active", "initial", `{"version":1,"expected_version":0}`, http.StatusNotFound)
	request(publisher, http.MethodPut, defs+"/active", "initial", `{"version":1,"expected_version":0}`, http.StatusNoContent)
	start := func(key, input string) string {
		t.Helper()
		w := request(client, http.MethodPost, "/workflows", key, input, http.StatusAccepted)
		return strings.TrimPrefix(w.Header().Get("Location"), "/workflows/")
	}
	const active = `{"definition_name":"review"}`
	v1 := start("old", active)
	main := startSchedulerChild(t, db, "workflow")
	t.Setenv("SCHEDULER_WORKER_POOL", "short")
	t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "1")
	startSchedulerChild(t, db, "worker")
	waitForNode := func(id string, version int, nodeID, state string) workflowNodeStatus {
		t.Helper()
		var got workflowNodeStatus
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			status, err := repo.workflowStatus(ctx, "tenant", id)
			if err != nil {
				return false, err
			}
			if status.DefinitionName != "review" || status.DefinitionVersion != version {
				t.Fatalf("invalid version pin: %+v", status)
			}
			for _, n := range status.Nodes {
				if n.ID == nodeID {
					got = n
				}
			}
			return got.State == state, nil
		})
		return got
	}
	waitForNode(v1, 1, "gate", "waiting")
	request(publisher, http.MethodPut, defs+"/active", "rollout", `{"version":2,"expected_version":1}`, http.StatusNoContent)
	if start("old", active) != v1 {
		t.Fatal("idempotent start moved to the active version")
	}
	request(client, http.MethodPost, "/workflows", "old",
		`{"definition_name":"review","definition_version":2}`, http.StatusConflict)
	v2 := start("current", active)
	killSchedulerChild(t, main)
	startSchedulerChild(t, db, "workflow")
	waitForNode(v2, 2, "gate", "waiting")
	signal := func(id string) {
		t.Helper()
		request(client, http.MethodPost, "/workflows/"+id+"/signals", "", `{"key":"approve"}`, http.StatusOK)
	}
	signal(v2)
	v2Run := waitForNode(v2, 2, "work", "queued").RunID
	rollback := `{"version":1,"expected_version":2}`
	request(publisher, http.MethodPut, defs+"/active", "rollback-release", rollback, http.StatusNoContent)
	request(publisher, http.MethodPut, defs+"/active", "rollback-release", rollback, http.StatusNoContent)
	request(publisher, http.MethodPut, defs+"/active", "stale", `{"version":2,"expected_version":0}`, http.StatusConflict)
	request(publisher, http.MethodPut, defs+"/active", "rollout", `{"version":2,"expected_version":1}`, http.StatusNoContent)
	request(publisher, http.MethodPut, defs+"/active", "rollout", rollback, http.StatusConflict)
	if start("current", active) != v2 {
		t.Fatal("rollback changed the pinned result of an earlier command")
	}
	v1AfterRollback := start("rollback", active)
	waitForNode(v1AfterRollback, 1, "gate", "waiting")
	signal(v1)
	signal(v1AfterRollback)
	waitForNode(v1, 1, "work", "succeeded")
	waitForNode(v1AfterRollback, 1, "work", "succeeded")
	status, err := repo.runStatus(t.Context(), "tenant", v2Run)
	if err != nil || status.QueueStatus != "queued" || status.ClaimAttempts != 0 {
		t.Fatalf("v1 worker consumed the pinned v2 task: %+v %v", status, err)
	}
	t.Setenv("SCHEDULER_WORKER_TASK_VERSIONS", "2")
	startSchedulerChild(t, db, "worker")
	waitForNode(v2, 2, "work", "succeeded")
	request(other, http.MethodPost, "/workflows", "cross-tenant", active, http.StatusNotFound)
	var published, releases int
	if err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM scheduler_audit WHERE operation='workflow.definition_publish'),
		(SELECT count(*) FROM scheduler_audit WHERE operation='workflow.definition_activate')`).
		Scan(&published, &releases); err != nil || published != 2 || releases != 3 {
		t.Fatalf("release audit counts: published=%d activation=%d %v", published, releases, err)
	}
}
