package cloudtest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const retirementRequester = "fixture-tencent-retirement-operator-token"
const retirementReviewer = "fixture-tencent-retirement-reviewer-token"

func seedInstanceRetirement(t *testing.T, db *sql.DB, f *retirementFixture) {
	t.Helper()
	ctx := t.Context()
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: retirementDisk, id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), retirementRequester, retirementReviewer)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE cmp_memberships SET role='admin' WHERE principal_id='operator'`)
	exec(`UPDATE cmp_inventory_objects SET status='ATTACHED' WHERE external_id=$1`, retirementDisk)
	exec(`WITH scope AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'instance',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,links,seen_generation)
	SELECT scope_id,$3,'Retirement fixture','STOPPED',jsonb_build_array(jsonb_build_object('kind','volume','id',$4::text,'relation','uses_volume')),1
	FROM scope`, f.vendor, f.region, retirementInstance, retirementDisk)
}

func startRetirementWorker(t *testing.T, store operations.Store, f *retirementFixture, transport http.RoundTripper, enabled *atomic.Bool) {
	t.Helper()
	worker, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			profile := publiccloud.Config{AccountID: f.account, Regions: []string{f.region},
				AccessKeyID: fixtureLaunchKey, AccessKeySecret: fixtureLaunchSecret, SecurityToken: fixtureLaunchSession,
				AllowInstanceRetirement: enabled.Load()}
			client, err := publiccloud.Connect(ctx, binding.Provider, profile, transport)
			if err != nil {
				return nil, err
			}
			if client.Identity() != binding.RemoteIdentity {
				return nil, identity.ErrForbidden
			}
			return client.InstanceOperator(), nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("retirement worker did not stop")
		}
	})
}

func waitRetirementRound(t *testing.T, db *sql.DB, id string, attempt, wantAbsences int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		var attempts, absence int
		err := db.QueryRowContext(ctx, `SELECT t.status,t.attempts,i.absence_observations
			FROM cmp_operation_tasks AS t JOIN cmp_instance_retirement_intents AS i ON i.operation_id=t.id
			WHERE t.id=$1`, id).Scan(&state, &attempts, &absence)
		if err != nil {
			t.Fatal(err)
		}
		if state == "queued" && attempts == attempt {
			if absence != wantAbsences {
				t.Fatalf("retirement observation count %d, want %d", absence, wantAbsences)
			}
			return
		}
		if state == "done" || state == "dead" || attempts > attempt {
			t.Fatalf("retirement finalized or advanced without evidence: %s, attempt %d, absence %d", state, attempts, absence)
		}
		if _, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET available_at=clock_timestamp()
			WHERE id=$1 AND status='queued' AND attempts<$2 AND available_at>clock_timestamp()`, id, attempt); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("retirement observation did not complete")
		case <-ticker.C:
		}
	}
}

func retryRetirementObservation(t *testing.T, store operations.Store, scope identity.Scope, id string, evidence operations.ObservationRetry) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		op, err := store.RetryCreationObservation(ctx, retirementReviewer, scope, id, evidence)
		if err == nil && op.Status == "observing" {
			return
		}
		if !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("retry retirement observation: %+v %v", op, err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("retirement observation lease not released")
		case <-ticker.C:
		}
	}
}

func assertRetirementPublished(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	var removed, diskConfirmed bool
	var events, audit, reservations int
	err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT deletion_confirmed_at IS NOT NULL FROM cmp_inventory_objects WHERE external_id=$1),
		(SELECT deletion_confirmed_at IS NOT NULL FROM cmp_inventory_objects WHERE external_id=$2),
		(SELECT count(*) FROM cmp_operation_event_outbox AS box JOIN cmp_instance_operation_events AS e ON e.event_id=box.sequence
		  WHERE e.operation_id=$3 AND box.event_type='instance.operation.succeeded'),
		(SELECT count(*) FROM cmp_project_audit WHERE subject_id=$3 AND action='retire-instance' AND category='operation' AND outcome='succeeded'),
		(SELECT count(*) FROM cmp_instance_retirement_reservations)`, retirementInstance, retirementDisk, id).
		Scan(&removed, &diskConfirmed, &events, &audit, &reservations)
	if err != nil || !removed || diskConfirmed || events != 1 || audit != 1 || reservations != 0 {
		t.Fatalf("inconsistent retirement publication: instance=%t disk=%t events=%d audit=%d reservations=%d error=%v",
			removed, diskConfirmed, events, audit, reservations, err)
	}
}
