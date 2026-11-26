package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const databaseRenameVerifier = "fixture-database-rename-verifier"

func seedDatabaseRename(t *testing.T, db *sql.DB, f *databaseRenameFixture) {
	t.Helper()
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"},
		f.remoteIdentity(), databaseRenameActor, databaseRenameReviewer)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET role='admin'
		WHERE tenant_id='tenant' AND principal_id='operator'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `WITH scope AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'database-instance',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,database_instance)
		SELECT scope_id,$3,'original_name','available',1,jsonb_build_object('product',$4::text,'engine','mysql')
		FROM scope`, f.vendor, f.region, f.id, f.product); err != nil {
		t.Fatal(err)
	}
}

func publishDatabaseRenameTicketPolicy(t *testing.T, db *sql.DB, store operations.Store, scope identity.Scope) {
	t.Helper()
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject)
		VALUES ('tenant','verifier','service','fixture','verifier');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role)
		VALUES ('tenant','project','verifier','viewer');
		UPDATE cmp_operation_event_sinks SET include_change_tickets=true`); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(databaseRenameVerifier))
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','verifier',now()+interval '10 minutes')`, digest[:]); err != nil {
		t.Fatal(err)
	}
	policy, err := store.PublishTicketPolicy(ctx, databaseRenameActor, scope, 0, operations.TicketPolicyInput{
		Actions: []string{"rename-database-instance"}, Verifier: "verifier",
	})
	if err != nil || policy.Revision != 1 {
		t.Fatalf("database rename ticket policy: %+v %v", policy, err)
	}
}

func confirmDatabaseRenameTicket(t *testing.T, store operations.Store, scope identity.Scope, op operations.Operation) {
	t.Helper()
	ctx := t.Context()
	if op.TicketPolicyRevision != 1 {
		t.Fatalf("database rename did not pin the ticket policy: %+v", op)
	}
	if _, err := store.Approve(ctx, databaseRenameReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, operations.ErrTicketRequired) {
		t.Fatalf("missing ticket did not block approval: %v", err)
	}
	expires := time.Now().Add(time.Hour)
	ticket, err := store.RecordChangeTicket(ctx, databaseRenameVerifier, scope, op.ID, operations.TicketEvidenceInput{
		TicketID: "database-change-1", SourceRevision: 1, Decision: "approved", ValidUntil: &expires,
	})
	if err != nil || !ticket.Ready {
		t.Fatalf("valid database change ticket: %+v %v", ticket, err)
	}
}

func startDatabaseRenameWorker(t *testing.T, store operations.Store, f *databaseRenameFixture, transport http.RoundTripper, enabled *atomic.Bool) {
	t.Helper()
	worker, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			config := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}, AllowDatabaseRename: enabled.Load()}
			if f.vendor == provider.HuaweiECS {
				config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
			} else {
				config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
			}
			client, err := publiccloud.Connect(ctx, binding.Provider, config, transport)
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
			t.Error("database rename worker did not stop")
		}
	})
}

func retryNameChangeObservation(t *testing.T, store operations.Store, scope identity.Scope, reviewer, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := store.RetryCreationObservation(ctx, reviewer, scope, id, operations.ObservationRetry{
			EvidenceRef: "fixture/verified-instance",
		})
		if err == nil && result.Status == "observing" {
			return
		}
		if !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("observation retry: %+v %v", result, err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("worker did not finish the observation lease")
		case <-ticker.C:
		}
	}
}

func assertDatabaseRenamePublished(t *testing.T, db *sql.DB, f *databaseRenameFixture, id string) {
	t.Helper()
	var name string
	var receipts, audits, reservations int
	err := db.QueryRowContext(t.Context(), `SELECT o.name,
		(SELECT count(*) FROM cmp_operation_event_outbox AS box
		 JOIN cmp_instance_operation_events AS ev ON ev.event_id=box.sequence
		 WHERE ev.operation_id=$2 AND box.event_type='database-instance.operation.succeeded'),
		(SELECT count(*) FROM cmp_project_audit
		 WHERE subject_id=$2 AND category='operation' AND action='rename-database-instance' AND outcome='succeeded'),
		(SELECT count(*) FROM cmp_database_rename_reservations AS r
		 WHERE (r.scope_id,r.external_id)=(o.scope_id,o.external_id))
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (s.tenant_id,s.account_id,s.kind,o.external_id)=('tenant',$1,'database-instance',$3)`,
		f.vendor, id, f.id).Scan(&name, &receipts, &audits, &reservations)
	if err != nil || name != "Recovered_name" || receipts != 1 || audits != 1 || reservations != 0 {
		t.Fatalf("inconsistent publication: name=%q events=%d audit=%d reservations=%d err=%v",
			name, receipts, audits, reservations, err)
	}
}
