package cloudtest

import (
	"context"
	"database/sql"
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

const cacheRenameActor = "fixture-cache-rename-operator-token"
const cacheRenameReviewer = "fixture-cache-rename-reviewer-token"

func seedCacheRename(t *testing.T, db *sql.DB, f *cacheRenameFixture) {
	t.Helper()
	ctx := t.Context()
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"},
		f.remoteIdentity(), cacheRenameActor, cacheRenameReviewer)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET role='admin'
		WHERE tenant_id='tenant' AND principal_id='operator'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `WITH scope AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'cache-instance',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,cache_instance)
		SELECT scope_id,$3,'original-name','available',1,jsonb_build_object('product',$4::text,'engine','redis')
		FROM scope`, f.vendor, f.region, f.id, f.product); err != nil {
		t.Fatal(err)
	}
}

func startCacheRenameWorker(t *testing.T, store operations.Store, f *cacheRenameFixture, transport http.RoundTripper, enabled *atomic.Bool) {
	t.Helper()
	worker, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			config := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}, AllowCacheRename: enabled.Load()}
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
			t.Error("cache rename worker did not stop")
		}
	})
}

func assertCacheRenamePublished(t *testing.T, db *sql.DB, f *cacheRenameFixture, id string) {
	t.Helper()
	var name string
	var events, audits, reservations int
	err := db.QueryRowContext(t.Context(), `SELECT o.name,
		(SELECT count(*) FROM cmp_operation_event_outbox AS box
		 JOIN cmp_instance_operation_events AS ev ON ev.event_id=box.sequence
		 WHERE ev.operation_id=$2 AND box.event_type='cache-instance.operation.succeeded'),
		(SELECT count(*) FROM cmp_project_audit
		 WHERE subject_id=$2 AND category='operation' AND action='rename-cache-instance' AND outcome='succeeded'),
		(SELECT count(*) FROM cmp_cache_rename_reservations AS r
		 WHERE (r.scope_id,r.external_id)=(o.scope_id,o.external_id))
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (s.tenant_id,s.account_id,s.kind,o.external_id)=('tenant',$1,'cache-instance',$3)`,
		f.vendor, id, f.id).Scan(&name, &events, &audits, &reservations)
	if err != nil || name != "recovered-name" || events != 1 || audits != 1 || reservations != 0 {
		t.Fatalf("inconsistent publication: name=%q events=%d audit=%d reservations=%d err=%v",
			name, events, audits, reservations, err)
	}
}
