package cloudtest

import (
	"database/sql"
	"testing"

	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func checkRedfishPXE(t *testing.T, db *sql.DB, fixture *redfishPowerFixture, enable func(),
	submit func(string, string, int) operations.Operation, approve func(operations.Operation),
	wait func(operations.Operation, string) operations.Operation) {
	t.Helper()
	stage, cancel := provider.StageBareMetalPXE, provider.CancelBareMetalPXE
	submit("invalid-pxe-action", "power-on-bare-metal", 400)
	run := func(key, action, end string) operations.Operation {
		t.Helper()
		op := submit(key, action, 202)
		if op.Status != "awaiting_approval" {
			t.Fatalf("PXE operation skipped approval: %+v", op)
		}
		approve(op)
		if end == "needs_review" {
			wait(op, "observing")
			if _, err := db.ExecContext(t.Context(), "UPDATE cmp_operation_tasks SET max_attempts=2 WHERE id=$1", op.ID); err != nil {
				t.Fatal(err)
			}
		}
		return wait(op, end)
	}
	if got := run("pxe-disabled", stage, "failed"); got.OutcomeReason != "bare_metal_pxe_disabled" {
		t.Fatalf("wrong disabled result: %+v", got)
	}
	enable()
	run("pxe-powered-on", stage, "failed")
	fixture.on.Store(false)
	fixture.bootMode.Store(2)
	run("pxe-existing-other", stage, "failed")
	run("pxe-cancel-other", cancel, "failed")
	fixture.bootMode.Store(0)
	fixture.weakETag.Store(true)
	run("pxe-weak-etag", stage, "failed")
	fixture.weakETag.Store(false)
	if fixture.pxeWrites.Load() != 0 {
		t.Fatal("rejected requests reached PATCH")
	}
	fixture.dropPXE.Store(true)
	got := run("pxe-lost-receipt", stage, "succeeded")
	if got.OutcomeReason != "desired_boot_override_observed" || fixture.pxeWrites.Load() != 1 || fixture.bootMode.Load() != 1 {
		t.Fatalf("lost response did not converge with one write: %+v", got)
	}
	if got := submit("pxe-lost-receipt", stage, 202); got.Status != "succeeded" {
		t.Fatalf("idempotent PXE retry was not a replay: %+v", got)
	}
	run("pxe-already-set", stage, "succeeded")
	if fixture.pxeWrites.Load() != 1 {
		t.Fatal("existing one-time boot override was written again")
	}
	run("cancel-pxe", cancel, "succeeded")
	if fixture.pxeWrites.Load() != 2 || fixture.bootMode.Load() != 0 || fixture.on.Load() {
		t.Fatal("cancellation changed power or did not disable the boot override")
	}
	fixture.pxeConflict.Store(true)
	run("pxe-etag-conflict", stage, "needs_review")
	if fixture.pxeWrites.Load() != 3 || fixture.bootMode.Load() != 0 || fixture.writes.Load() != 3 {
		t.Fatal("ETag conflict was retried or changed the server")
	}
}
