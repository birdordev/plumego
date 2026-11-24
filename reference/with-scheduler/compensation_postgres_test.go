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

func TestPostgresWorkflowCompensationRetriesAndManualTakeover(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	t.Setenv("SCHEDULER_COMPENSATION_ACTOR", "operator")
	t.Setenv("SCHEDULER_WORKER_POOL", "short")
	api := func(tenant, actor string) http.Handler {
		t.Helper()
		app, err := newAppWithActor("", "token", tenant, actor, repo)
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	operator := api("tenant", "operator")
	viewer := api("tenant", "viewer")
	outsider := api("other", "operator")
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
	request(viewer, http.MethodPost, "/runs", "direct", `{"topic":"demo.release","payload":{}}`, http.StatusBadRequest)
	request(viewer, http.MethodPost, "/runs", "reserve", `{"topic":"demo.reserve","payload":{"quantity":1}}`, http.StatusBadRequest)

	if _, err := db.ExecContext(ctx, `CREATE TABLE compensation_fixture_failure (quantity integer PRIMARY KEY);
		INSERT INTO compensation_fixture_failure VALUES (7),(11);
		CREATE FUNCTION test_block_compensation() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF COALESCE(current_setting('scheduler.actor', true),'')='' AND EXISTS (
				SELECT 1 FROM compensation_fixture_failure f WHERE f.quantity=NEW.quantity) THEN
				RAISE EXCEPTION USING ERRCODE='40001', MESSAGE='fixture compensation unavailable';
			END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER test_block_compensation BEFORE UPDATE OF released_at ON scheduler_reservations
			FOR EACH ROW EXECUTE FUNCTION test_block_compensation()`); err != nil {
		t.Fatal(err)
	}
	graph := workflowDefinition{Nodes: []workflowNode{
		{ID: "first", Task: &submission{Topic: reserveTaskType, Payload: json.RawMessage(`{"quantity":7}`)}},
		{ID: "second", DependsOn: []string{"first"}, Task: &submission{Topic: reserveTaskType, Payload: json.RawMessage(`{"quantity":11}`)}},
		{ID: "later", DependsOn: []string{"second"},
			Task: &submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`), RunAt: time.Now().Add(time.Hour)}},
	}}
	body, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	w := request(viewer, http.MethodPost, "/workflows", "workflow", string(body), http.StatusAccepted)
	path := w.Header().Get("Location")
	flow := strings.TrimPrefix(path, "/workflows/")
	startSchedulerChild(t, db, "workflow")
	startSchedulerChild(t, db, "worker")
	var later string
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		status, err := repo.workflowStatus(ctx, "tenant", flow)
		if err != nil {
			return false, err
		}
		for _, node := range status.Nodes {
			if node.ID == "later" {
				later = node.RunID
			}
		}
		return later != "", nil
	})
	if err := repo.cancelRun(ctx, "tenant", later); err != nil {
		t.Fatal(err)
	}

	type effect struct {
		id, key, state string
		released       bool
	}
	read := func(ctx context.Context, qty int) (effect, error) {
		var item effect
		err := db.QueryRowContext(ctx, `SELECT r.run_id,c.effect_key,c.state,r.released_at IS NOT NULL
			FROM scheduler_reservations r JOIN scheduler_compensations c ON c.reservation_id=r.run_id
			WHERE c.workflow_id=$1 AND r.quantity=$2`, flow, qty).Scan(&item.id, &item.key, &item.state, &item.released)
		return item, err
	}
	wait := func(qty int, state string) effect {
		t.Helper()
		var item effect
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			var err error
			item, err = read(ctx, qty)
			return item.state == state, err
		})
		return item
	}
	newest := wait(11, "manual")
	oldest, err := read(ctx, 7)
	if err != nil || newest.released || oldest.released || oldest.state != "registered" || newest.key == oldest.key {
		t.Fatalf("reverse order or effect registration: older=%+v newer=%+v %v", oldest, newest, err)
	}
	var attempts, receipts int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(sum(q.execution_attempts),0),count(s.run_id)
		FROM scheduler_compensation_runs j JOIN mq_tasks q ON q.id=j.run_id
		LEFT JOIN scheduled_results s ON s.run_id=q.id WHERE j.reservation_id=$1`, newest.id).
		Scan(&attempts, &receipts); err != nil || attempts != 3 || receipts != 0 {
		t.Fatalf("compensation failed outside the retry/receipt boundary: attempts=%d receipts=%d %v", attempts, receipts, err)
	}
	newestPath := path + "/compensations/" + newest.id
	request(viewer, http.MethodPost, newestPath, "manual", `{"action":"release"}`, http.StatusForbidden)
	request(outsider, http.MethodPost, newestPath, "manual", `{"action":"release"}`, http.StatusNotFound)
	request(operator, http.MethodPost, newestPath, "manual", `{"action":"release"}`, http.StatusNoContent)
	request(operator, http.MethodPost, newestPath, "manual", `{"action":"release"}`, http.StatusNoContent)
	request(operator, http.MethodPost, newestPath, "manual", `{"action":"retry"}`, http.StatusConflict)
	if item := wait(11, "released"); !item.released || item.key != newest.key {
		t.Fatalf("manual release did not reuse the effect key: %+v", item)
	}

	oldest = wait(7, "manual")
	if _, err := db.ExecContext(ctx, "DELETE FROM compensation_fixture_failure WHERE quantity=7"); err != nil {
		t.Fatal(err)
	}
	oldestPath := path + "/compensations/" + oldest.id
	request(operator, http.MethodPost, oldestPath, "retry", `{"action":"retry"}`, http.StatusNoContent)
	request(operator, http.MethodPost, oldestPath, "retry", `{"action":"retry"}`, http.StatusNoContent)
	if item := wait(7, "released"); !item.released || item.key != oldest.key {
		t.Fatalf("replacement job did not use the original effect key: %+v", item)
	}
	request(viewer, http.MethodGet, path+"/compensations", "", "", http.StatusOK)
	request(outsider, http.MethodGet, path+"/compensations", "", "", http.StatusNotFound)
	var audit, jobs, reversed int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM scheduler_audit WHERE operation='workflow.compensation_manual' AND actor='operator'),
		(SELECT count(*) FROM scheduler_compensation_runs WHERE reservation_id=$1),
		(SELECT count(*) FROM scheduler_reservations r JOIN scheduler_compensations c ON r.run_id=c.reservation_id
			WHERE c.workflow_id=$2 AND r.released_at IS NOT NULL AND r.release_key=c.effect_key)`,
		oldest.id, flow).Scan(&audit, &jobs, &reversed); err != nil || audit != 2 || jobs != 2 || reversed != 2 {
		t.Fatalf("final business state or audit: audit=%d retry_runs=%d released=%d %v", audit, jobs, reversed, err)
	}
}
