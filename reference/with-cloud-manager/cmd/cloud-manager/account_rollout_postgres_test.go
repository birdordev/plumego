package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceAccountOperationRollout(t *testing.T) {
	for _, target := range priorityRolloutTargets() {
		t.Run(target.provider, func(t *testing.T) {
			exerciseAccountOperationRollout(t, target)
		})
	}
}

func exerciseAccountOperationRollout(t *testing.T, target rolloutPowerTarget) {
	t.Helper()
	db := dbtest.Open(t)
	if err := migrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	cloud := target.fixture()
	seedPowerDrillData(t, db, cloud, target.parked)
	seedRolloutAuditReader(t, db)
	first, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := rolloutProject + "/cloud-accounts/" + target.provider + "/operation-rollout"
	firstRead := scheduleResponse(t, first, http.MethodGet, path, rolloutAuditToken, "", "", http.StatusOK)
	assertAccountRollout(t, firstRead, `"0"`, 0)
	scheduleResponse(t, first, http.MethodGet, path, pitrServiceToken, "", "", http.StatusForbidden)
	scheduleResponse(t, first, http.MethodGet, strings.Replace(path, "/projects/project/", "/projects/isolated/", 1),
		rolloutAuditToken, "", "", http.StatusForbidden)
	rulesBody, err := json.Marshal(map[string]any{"rules": []operations.RolloutRule{
		{Region: cloud.region, Action: "stop"}, {Region: cloud.region, Action: "start"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const frozen = `{"rules":[]}`
	for _, invalid := range []string{`{}`, `{"rules":null}`, `{"rules":[{"region":"*","action":"start"}]}`} {
		scheduleResponse(t, first, http.MethodPut, path, rolloutAuditToken, `"0"`, invalid, http.StatusBadRequest)
	}
	scheduleResponse(t, first, http.MethodPut, path, rolloutAuditToken, "", frozen, http.StatusPreconditionFailed)
	server := httptest.NewTLSServer(first)
	t.Cleanup(server.Close)
	var cli bytes.Buffer
	if err := runCloudAccountsClient(t.Context(), []string{"rollout-replace", "tenant", "project", target.provider, "0"},
		server.URL, rolloutAuditToken, bytes.NewReader(rulesBody), &cli, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	cli.Reset()
	if err := runCloudAccountsClient(t.Context(), []string{"rollout", "tenant", "project", target.provider},
		server.URL, rolloutAuditToken, nil, &cli, server.Client().Transport); err != nil || !strings.Contains(cli.String(), `"revision":"1"`) {
		t.Fatalf("rollout CLI current revision: %v %s", err, cli.String())
	}
	assertAccountRollout(t, scheduleResponse(t, second, http.MethodPut, path, rolloutAuditToken, `"1"`,
		string(rulesBody), http.StatusOK), `"1"`, 2)
	scheduleResponse(t, first, http.MethodPut, path, rolloutAuditToken, `"0"`, frozen, http.StatusPreconditionFailed)

	const outside = "unapproved-region"
	pitrExec(t, db, `INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'instance',1,now())`, target.provider, outside)
	pitrExec(t, db, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$3,'other region','stopped',1 FROM cmp_inventory_scopes WHERE account_id=$1 AND region=$2`,
		target.provider, outside, target.parked)
	submit := func(app http.Handler, region, asset, action, key string, status int) operations.Operation {
		t.Helper()
		return submitFixturePower(t, app, cloud.provider, region, asset, pitrServiceToken, action, key, status)
	}
	submit(second, outside, target.parked, "start", "other-region", http.StatusForbidden)
	submit(first, cloud.region, target.parked, "reboot", "other-action", http.StatusForbidden)

	queued := submit(first, cloud.region, cloud.instance, "start", "before-freeze", http.StatusAccepted)
	inPreflight := make(chan struct{})
	unblock := make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case unblock <- struct{}{}:
		default:
		}
	})
	cloud.beforeRead = func(ctx context.Context) error {
		close(inPreflight)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-unblock:
			return nil
		}
	}
	cloud.revokeOnRead.Store(true)
	start := rolloutWorkerStarter(t, db, "", pitrWorkerProfile(t, cloud), cloud, target.hosts, false)
	stop := start("", "dispatch")
	select {
	case <-inPreflight:
	case <-t.Context().Done():
		t.Fatal("operation did not enter provider preflight")
	}
	assertAccountRollout(t, scheduleResponse(t, second, http.MethodPut, path, rolloutAuditToken, `"1"`, frozen, http.StatusOK), `"2"`, 0)
	assertLegacyRolloutGuard(t, db, queued.ID)
	submit(first, cloud.region, target.parked, "start", "during-freeze", http.StatusForbidden)
	unblock <- struct{}{}
	waitPITRDone(t, db, queued.ID)
	if cloud.writes.Load() != 0 {
		t.Fatal("the worker sent a write after the freeze committed")
	}
	assertAccountRollout(t, scheduleResponse(t, first, http.MethodPut, path, rolloutAuditToken, `"2"`,
		string(rulesBody), http.StatusOK), `"3"`, 2)
	if replay := submit(second, cloud.region, cloud.instance, "start", "before-freeze", http.StatusAccepted); replay.ID != queued.ID || replay.Status != "failed" {
		t.Fatalf("old queued receipt was reactivated: %+v", replay)
	}
	fresh := submit(first, cloud.region, cloud.instance, "start", "after-freeze", http.StatusAccepted)
	waitPITRDone(t, db, fresh.ID)
	pending := submit(first, cloud.region, cloud.instance, "stop", "in-flight", http.StatusAccepted)
	pitrWait(t, "cloud stop effect", func(context.Context) (bool, error) { return cloud.writes.Load() == 2, nil })
	assertAccountRollout(t, scheduleResponse(t, second, http.MethodPut, path, rolloutAuditToken, `"3"`, frozen, http.StatusOK), `"4"`, 0)
	waitPITRDone(t, db, pending.ID)
	stop()
	if replay := submit(second, cloud.region, cloud.instance, "stop", "in-flight", http.StatusAccepted); replay.ID != pending.ID || replay.Status != "succeeded" ||
		cloud.writes.Load() != 2 || cloud.running.Load() {
		t.Fatalf("in-flight stop did not settle under a frozen policy: %+v writes=%d", replay, cloud.writes.Load())
	}
	assertRolloutAudit(t, db, target.provider)
}

func assertAccountRollout(t *testing.T, response *httptest.ResponseRecorder, etag string, entries int) {
	t.Helper()
	var envelope struct{ Data operations.RolloutPolicy }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	policy := envelope.Data
	if response.Header().Get("ETag") != etag || policy.Rules == nil || len(policy.Rules) != entries {
		t.Fatalf("rollout policy %v, revision %s, rules=%d", policy, response.Header().Get("ETag"), len(policy.Rules))
	}
}

func assertLegacyRolloutGuard(t *testing.T, db *sql.DB, operation string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), "UPDATE cmp_instance_operations SET status='applying' WHERE operation_id=$1", operation)
	var check *pgconn.PgError
	if !errors.As(err, &check) || check.ConstraintName != "cmp_operation_rollout_dispatch" {
		t.Fatalf("legacy dispatch crossed the rollout gate: %v", err)
	}
}

func assertRolloutAudit(t *testing.T, db *sql.DB, account string) {
	t.Helper()
	var events, history int
	err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM cmp_project_audit WHERE account_id=$1 AND action='replace-operation-rollout' AND actor_id='audit-admin'),
		(SELECT count(*) FROM cmp_account_operation_rollouts WHERE account_id=$1)`, account).Scan(&events, &history)
	if err != nil || events != 4 || history != 4 {
		t.Fatalf("rollout audit=%d history=%d: %v", events, history, err)
	}
}
