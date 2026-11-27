package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func exercisePowerQueueRepair(t *testing.T, app http.Handler, db *sql.DB, store operations.Store, scope identity.Scope,
	op operations.Operation, cloud, operator, reviewer string) {
	t.Helper()
	const root = "/v1/tenants/tenant/projects/project/operations/"
	target := root + op.ID
	attempts := waitPowerQueueRelease(t, db, op.ID)
	const body = `{"evidence_ref":"fixture/queue-repair","repair_queue":true}`
	post := func(token, data string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return scheduleResponse(t, app, http.MethodPost, target+"/retry-observation", token, "", data, status)
	}
	post(reviewer, body, http.StatusConflict)
	pitrExec(t, db, `INSERT INTO cmp_operation_attempts (task_id,attempt_no,started_at,finished_at)
		VALUES ($1,$2,now()-interval '1 minute',now())`, op.ID, attempts)
	pitrExec(t, db, `UPDATE cmp_operation_tasks SET status='leased',lease_owner='fixture-old-worker',
		lease_until=now()+interval '5 minutes',expires_at=now()-interval '1 minute' WHERE id=$1`, op.ID)
	post(reviewer, body, http.StatusConflict)
	var issue string
	switch cloud {
	case provider.TencentCVM:
		pitrExec(t, db, "DELETE FROM cmp_operation_tasks WHERE id=$1", op.ID)
		pitrExec(t, db, `UPDATE cmp_instance_operations SET status='queued' WHERE operation_id=$1`, op.ID)
		post(reviewer, body, http.StatusConflict)
		pitrExec(t, db, `UPDATE cmp_instance_operations SET status='observing' WHERE operation_id=$1`, op.ID)
		issue = "missing_task"
	case provider.AlibabaECS:
		pitrExec(t, db, `UPDATE cmp_operation_tasks SET status='dead',lease_owner=NULL,lease_until=NULL WHERE id=$1`, op.ID)
		issue = "dead_letter"
	case provider.HuaweiECS:
		pitrExec(t, db, `UPDATE cmp_operation_tasks SET lease_until=now()-interval '1 minute' WHERE id=$1`, op.ID)
		pitrExec(t, db, `UPDATE cmp_instance_operations SET status='applying' WHERE operation_id=$1`, op.ID)
		issue = "expired_task"
	default:
		t.Fatal("unexpected provider")
	}
	var detail operations.Operation
	decodeProjectData(t, freshnessRequest(t, app, target, operator), &detail)
	if detail.QueueIssueReason != issue {
		t.Fatalf("repairable operation detail: %+v, want %s", detail, issue)
	}
	post(operator, body, http.StatusForbidden)
	scheduleResponse(t, app, http.MethodPost, "/v1/tenants/tenant/projects/foreign/operations/"+op.ID+"/retry-observation",
		reviewer, "", body, http.StatusForbidden)
	// 失败发生在修复任务之后，必须撤销整笔修复。
	pitrExec(t, db, `ALTER TABLE cmp_instance_operation_events ADD CONSTRAINT reject_repair_for_test
		CHECK (status<>'observing') NOT VALID`)
	_, err := store.RetryCreationObservation(t.Context(), reviewer, scope, op.ID,
		operations.ObservationRetry{EvidenceRef: "fixture/queue-repair", RepairQueue: true})
	if err == nil || errors.Is(err, operations.ErrConflict) || errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("expected transaction failure after queue repair, got %v", err)
	}
	pitrExec(t, db, `ALTER TABLE cmp_instance_operation_events DROP CONSTRAINT reject_repair_for_test`)
	rolledBack, err := store.Get(t.Context(), operator, scope, op.ID)
	if err != nil || rolledBack.QueueIssueReason != issue {
		t.Fatalf("repair transaction changed residual queue issue: %+v, %v", rolledBack, err)
	}
	if cloud == provider.TencentCVM {
		server := httptest.NewTLSServer(app)
		defer server.Close()
		var output bytes.Buffer
		err := runOperationsClient(t.Context(), []string{"retry-observation", "tenant", "project", op.ID,
			"--evidence-ref", "fixture/queue-repair", "--repair-queue"}, server.URL, reviewer, nil, &output, server.Client().Transport)
		var receipt operations.Operation
		if err != nil || json.Unmarshal(output.Bytes(), &receipt) != nil || receipt.OutcomeReason != "queue_observation_restarted" {
			t.Fatalf("HTTPS CLI did not repair queue: %s, %v", output.String(), err)
		}
	} else {
		var workers sync.WaitGroup
		for range 4 {
			workers.Go(func() {
				got, err := store.RetryCreationObservation(t.Context(), reviewer, scope, op.ID,
					operations.ObservationRetry{EvidenceRef: "fixture/queue-repair", RepairQueue: true})
				if err != nil || (got.Status != "observing" && got.Status != "succeeded") || got.ProviderRequestID != op.ProviderRequestID {
					t.Errorf("repair replay: %+v, %v", got, err)
				}
			})
		}
		workers.Wait()
	}
	post(reviewer, body, http.StatusOK)
	post(reviewer, `{"evidence_ref":"fixture/queue-repair"}`, http.StatusConflict)
	var retries, audit, remaining, recordedAttempts int
	err = db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM cmp_instance_observation_retries WHERE operation_id=$1 AND repair_queue),
		(SELECT count(*) FROM cmp_project_audit WHERE subject_id=$1 AND category='observation' AND actor_id='reviewer'),
		max_attempts-attempts,
		(SELECT count(*) FROM cmp_operation_attempts WHERE task_id=$1 AND attempt_no<=$2)
		FROM cmp_operation_tasks WHERE id=$1`, op.ID, attempts).Scan(&retries, &audit, &remaining, &recordedAttempts)
	if err != nil || retries != 1 || audit != 1 || remaining < 7 || recordedAttempts < 1 {
		t.Fatalf("repair history and budget retries=%d audit=%d remaining=%d attempts=%d, %v", retries, audit, remaining, recordedAttempts, err)
	}
}

func waitPowerQueueRelease(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		var attempts int
		if err := db.QueryRowContext(ctx, "SELECT status,attempts FROM cmp_operation_tasks WHERE id=$1", id).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		if status == "queued" && attempts > 0 {
			return attempts
		}
		select {
		case <-ctx.Done():
			t.Fatalf("task %s was not released after dispatch: %v", id, ctx.Err())
		case <-ticker.C:
		}
	}
}
