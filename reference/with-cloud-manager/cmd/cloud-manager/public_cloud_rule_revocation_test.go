package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func (f *vpcCreationFixture) exerciseRuleRevocation(t *testing.T, db *sql.DB, store operations.Store, app http.Handler,
	scope identity.Scope, actor, reviewer, id string, enabled *atomic.Bool) {
	t.Helper()
	ctx := t.Context()
	input := operations.RuleRevocation{Account: f.vendor, Region: f.region, ParentID: f.parent, RuleID: id}
	submit := func(key, version string, want int) operations.Operation {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/network-operations/revoke-rule", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", version)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("revoke rule %s returned %d, want %d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.ID == "" || op.Action != "revoke-security-group-rule" || op.AssetID != id {
				t.Fatalf("unexpected revocation receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if op.Status != "awaiting_approval" {
			t.Fatalf("revocation did not require independent approval: %+v", op)
		}
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string, advance bool) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, state, advance)
	}
	assertDeclined := func(key, reason string) {
		t.Helper()
		op := submit(key, `"1"`, http.StatusAccepted)
		approve(op)
		if declined := wait(op, "failed", false); declined.OutcomeReason != reason || f.revokes.Load() != 0 {
			t.Fatalf("revocation guard did not stop %s: %+v", key, declined)
		}
	}

	submit("revoke-with-stale-parent", `"2"`, http.StatusPreconditionFailed)
	assertDeclined("revoke-disabled", "rule_revocation_disabled")
	enabled.Store(true)
	f.blockedGroup.Store(true)
	assertDeclined("revoke-unsupported-parent", "rule_revocation_preconditions_unconfirmed")
	f.blockedGroup.Store(false)
	f.badEvidence.Store(true)
	assertDeclined("revoke-changed-rule", "rule_revocation_preconditions_unconfirmed")
	f.badEvidence.Store(false)
	if f.vendor == provider.TencentCVM {
		f.duplicateRule.Store(true)
		assertDeclined("revoke-ambiguous-rule", "rule_revocation_preflight_unavailable")
		f.duplicateRule.Store(false)
	}
	f.revokeOnIdentity.Store(true)
	assertDeclined("revoke-stale-approval", "dispatch_unavailable")

	op := submit("revoke-rule", `"1"`, http.StatusAccepted)
	if replay := submit("revoke-rule", `"1"`, http.StatusAccepted); replay.ID != op.ID {
		t.Fatal("idempotent revocation created another operation")
	}
	submit("overlapping-rule-revocation", `"1"`, http.StatusConflict)
	approve(op)
	wantReason := "submission_outcome_unknown"
	if f.vendor == provider.HuaweiECS {
		wantReason = "provider_accepted"
	}
	if observing := wait(op, "observing", false); observing.OutcomeReason != wantReason || f.revokes.Load() != 1 {
		t.Fatalf("revocation submission: %+v", observing)
	}
	waitPendingCloudObservation(t, db, op.ID)
	f.invalidAbsence.Store(true)
	if review := wait(op, "needs_review", true); review.OutcomeReason != "rule_revocation_requires_evidence" {
		t.Fatalf("incomplete rule evidence: %+v", review)
	}
	f.invalidAbsence.Store(false)
	f.holdRulePoint.Store(f.vendor == provider.HuaweiECS)
	f.visible.Store(false)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID,
		operations.ObservationRetry{EvidenceRef: "fixture/security-rule-absence"}); err != nil {
		t.Fatal(err)
	}
	if f.vendor == provider.HuaweiECS {
		waitPendingCloudObservation(t, db, op.ID)
		f.holdRulePoint.Store(false)
	}
	if succeeded := wait(op, "succeeded", true); succeeded.OutcomeReason != "security_rule_absence_confirmed" || f.revokes.Load() != 1 {
		t.Fatalf("read-only revocation recovery: %+v", succeeded)
	}
	var count int
	var deleted, delivered bool
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT absence_observations FROM cmp_network_intents WHERE operation_id=$1),
		EXISTS(SELECT 1 FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
			WHERE (s.account_id,s.kind,o.external_id)=($2,'security-group-rule',$3) AND o.deletion_confirmed_at IS NOT NULL),
		EXISTS(SELECT 1 FROM cmp_operation_event_outbox AS d JOIN cmp_instance_operation_events AS e ON e.event_id=d.sequence
			WHERE d.event_type='security-group-rule.operation.succeeded' AND e.operation_id=$1)`, op.ID, f.vendor, id).
		Scan(&count, &deleted, &delivered); err != nil || count != 2 || !deleted || !delivered {
		t.Fatalf("revocation confirmation observations=%d deleted=%v delivered=%v err=%v", count, deleted, delivered, err)
	}
	if replay := submit("revoke-rule", `"1"`, http.StatusAccepted); replay.ID != op.ID || replay.Status != "succeeded" {
		t.Fatalf("could not recover accepted revocation: %+v", replay)
	}
}
