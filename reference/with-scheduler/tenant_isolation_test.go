package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresSchedulerTenantIsolation(t *testing.T) {
	queue, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const tenantA, tenantB = "tenant-a", "tenant-b"
	apiA, err := newApp("127.0.0.1:0", "key-a", tenantA, repo)
	if err != nil {
		t.Fatal(err)
	}
	apiB, err := newApp("127.0.0.1:0", "key-b", tenantB, repo)
	if err != nil {
		t.Fatal(err)
	}
	request := func(app http.Handler, token, method, path, key, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Tenant-ID", tenantA)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s = %d %s, want %d", method, path, w.Code, w.Body, want)
		}
		return w
	}
	const body = `{"topic":"demo.record","payload":{"private":"input-marker"}}`
	submit := func(app http.Handler, token, key string) string {
		t.Helper()
		w := request(app, token, http.MethodPost, "/runs", key, body, http.StatusAccepted)
		id, ok := strings.CutPrefix(w.Header().Get("Location"), "/runs/")
		if !ok || id == "" {
			t.Fatal("missing run location")
		}
		return id
	}
	complete := submit(apiA, "key-a", "same-submit-key")
	tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "shared-worker", Limit: 1})
	if err != nil || len(tasks) != 1 || tasks[0].ID != complete || tasks[0].TenantID != tenantA {
		t.Fatalf("unexpected worker claim: count=%d error=%v", len(tasks), err)
	}
	wrongTenant := tasks[0]
	wrongTenant.TenantID = tenantB
	if err := repo.recordResult(ctx, wrongTenant); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("foreign worker write = %v", err)
	}
	resultPath := "/runs/" + complete + "/result"
	request(apiA, "key-a", http.MethodGet, resultPath, "", "", http.StatusNotFound)
	if err := repo.recordResult(ctx, tasks[0]); err != nil {
		t.Fatal(err)
	}
	if err := queue.Ack(ctx, complete, tasks[0].LeaseOwner, time.Now()); err != nil {
		t.Fatal(err)
	}
	dlqB := submit(apiB, "key-b", "same-submit-key")
	if dlqB == complete {
		t.Fatal("the same key produced a shared run across tenants")
	}
	if got := failNextTask(t, queue, "permanent task failure"); got.ID != dlqB || got.TenantID != tenantB {
		t.Fatal("unexpected dead-letter for tenant B")
	}
	dlqA := submit(apiA, "key-a", "fail-a")
	if got := failNextTask(t, queue, "permanent task failure"); got.ID != dlqA || got.TenantID != tenantA {
		t.Fatal("unexpected dead-letter for tenant A")
	}
	cancel := submit(apiA, "key-a", "cancel-a")

	request(apiA, "key-b", http.MethodGet, resultPath, "", "", http.StatusUnauthorized)
	hidden := request(apiB, "key-b", http.MethodGet, resultPath, "", "", http.StatusNotFound)
	missing := request(apiB, "key-b", http.MethodGet, "/runs/missing/result", "", "", http.StatusNotFound)
	if hidden.Body.String() != missing.Body.String() || hidden.Header().Get("Content-Disposition") != "" {
		t.Fatal("foreign result response revealed an existing run")
	}
	receipt := request(apiA, "key-a", http.MethodGet, resultPath, "", "", http.StatusOK)
	var envelope struct {
		Data runResult `json:"data"`
	}
	if err := json.Unmarshal(receipt.Body.Bytes(), &envelope); err != nil || envelope.Data.RunID != complete ||
		envelope.Data.CompletedAt.IsZero() || strings.Contains(receipt.Body.String(), "input-marker") ||
		receipt.Header().Get("Content-Disposition") != `attachment; filename="result.json"` ||
		receipt.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unexpected result receipt: %s %s (%v)", receipt.Header(), receipt.Body, err)
	}
	request(apiB, "key-b", http.MethodGet, "/runs/"+cancel, "", "", http.StatusNotFound)
	request(apiB, "key-b", http.MethodPost, "/runs/"+cancel+"/cancel", "", "", http.StatusNotFound)
	request(apiA, "key-a", http.MethodPost, "/runs/"+cancel+"/cancel", "", "", http.StatusNoContent)
	request(apiB, "key-b", http.MethodGet, "/runs/"+dlqA+"/dead-letters", "", "", http.StatusNotFound)
	list := request(apiB, "key-b", http.MethodGet, "/runs", "", "", http.StatusOK)
	if !strings.Contains(list.Body.String(), dlqB) || strings.Contains(list.Body.String(), dlqA) ||
		strings.Contains(list.Body.String(), complete) || strings.Contains(list.Body.String(), cancel) {
		t.Fatalf("unexpected tenant B list: %s", list.Body)
	}

	now := time.Now().UTC()
	replay := func(app http.Handler, token, own, other string, preview bool, status int) {
		t.Helper()
		payload, err := json.Marshal(struct {
			replaySpec
			Preview bool `json:"preview"`
		}{replaySpec{Limit: 25, FailedFrom: now.Add(-time.Hour), FailedTo: now.Add(time.Hour)}, preview})
		if err != nil {
			t.Fatal(err)
		}
		w := request(app, token, http.MethodPost, "/dead-letters/replay", "same-replay-key", string(payload), status)
		if !strings.Contains(w.Body.String(), own) || strings.Contains(w.Body.String(), other) {
			t.Fatalf("replay returned the wrong tenant's work: %s", w.Body)
		}
	}
	replay(apiB, "key-b", dlqB, dlqA, true, http.StatusOK)
	replay(apiB, "key-b", dlqB, dlqA, false, http.StatusAccepted)
	statusA, err := repo.runStatus(ctx, tenantA, dlqA)
	if err != nil || statusA.QueueStatus != mq.TaskStatusDead {
		t.Fatalf("tenant B replay changed tenant A's run: %+v %v", statusA, err)
	}
	replay(apiA, "key-a", dlqA, dlqB, false, http.StatusAccepted)
	if statusB, err := repo.runStatus(ctx, tenantB, dlqB); err != nil || statusB.QueueStatus != mq.TaskStatusQueued {
		t.Fatalf("tenant B run did not requeue: %+v %v", statusB, err)
	}
}
