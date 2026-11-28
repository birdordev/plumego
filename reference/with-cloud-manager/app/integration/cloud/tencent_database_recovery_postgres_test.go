package cloudtest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptanceTencentDatabaseRecovery(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	point := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	f := &tencentDatabaseRecoveryFixture{
		databaseBackupsFixture: databaseBackupsFixture{
			databaseFixture: &databaseFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
				id:                 tencentRecoverySource, product: provider.TencentCDB,
			},
			start: point.Add(-30 * time.Second),
		},
		point: point,
		placement: provider.DatabaseRecoveryPlacement{VPC: "vpc-isolatedrds", Subnet: "subnet-isolatedrds",
			SecurityGroup: "sg-isolatedrds", Zone: "ap-guangzhou-3", Flavor: "UNIVERSAL", StorageType: "local_ssd",
			StorageGiB: 40, CPU: 4, MemoryMiB: 8192},
	}
	f.pointRow = func(family string) map[string]any { return f.databaseRow(family, f.id, "source-db", "8.0", f.region) }
	seedTencentDatabaseRecovery(t, db, f)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := publiccloud.Config{Regions: []string{f.region}, AccountID: f.account, AccessKeyID: fixtureLaunchKey, AccessKeySecret: fixtureLaunchSecret,
		SecurityToken: fixtureLaunchSession, DatabaseBackupTimeZones: map[string]string{f.id: "Asia/Shanghai"},
		TencentDatabaseRecoveryPlacements: map[string]provider.DatabaseRecoveryPlacement{f.region: f.placement}}
	connect := func(ctx context.Context) (*publiccloud.Client, error) {
		return publiccloud.Connect(ctx, f.vendor, cfg, cloudFixtureTransport{endpoint, remote.Client().Transport})
	}
	client, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assets := inventory.NewStore(db)
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.DatabaseInstance}
	if err := assets.RefreshDatabaseRestoreWindows(ctx, key, f.id, 1, client); err != nil {
		t.Fatal(err)
	}
	query := inventory.DatabaseAssetQuery{Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id}
	observed, err := assets.ReadDatabaseRestoreWindows(ctx, recoveryActor, query)
	if err != nil {
		t.Fatal(err)
	}
	input := operations.DatabaseRecoveryInput{Account: f.vendor, Region: f.region, ObservedAt: observed.ObservedAt,
		DatabaseRecovery: provider.DatabaseRecovery{SourceID: f.id, Name: recoveryName, RestoreTime: point, DatabaseRecoveryPlacement: f.placement}}
	store := operations.NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	startDatabaseRecoveryWorker(t, store, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		client, err := connect(ctx)
		if err != nil {
			return nil, err
		}
		if binding.Provider != f.vendor || client.Identity() != binding.RemoteIdentity {
			return nil, identity.ErrForbidden
		}
		return client.InstanceOperator(), nil
	})
	submit := func(key string) operations.Operation {
		t.Helper()
		op, err := store.SubmitDatabaseRecovery(ctx, recoveryActor, scope, key, 1, input)
		if err != nil || op.Status != "awaiting_approval" || op.AssetID != f.id {
			t.Fatalf("unexpected recovery receipt: %+v %v", op, err)
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		f.operation.Store(op.ID)
		if _, err := store.Approve(ctx, recoveryReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, recoveryActor, op.ID, state, true)
	}
	for _, invalid := range []struct {
		mode int32
		key  string
	}{{tencentRecoveryEncrypted, "encrypted-source"}, {tencentRecoveryRejected, "native-preflight-rejected"}} {
		f.mode.Store(invalid.mode)
		op := submit(invalid.key)
		approve(op)
		wait(op, "failed")
	}
	if f.writes.Load() != 0 || f.dryRuns.Load() != 1 {
		t.Fatal("encrypted source or rejected preflight reached clone dispatch")
	}
	f.mode.Store(tencentRecoveryWrongPoint)
	op := submit("clone")
	if again := submit("clone"); again.ID != op.ID {
		t.Fatal("idempotent submission changed the operation identity")
	}
	if _, err := store.Approve(ctx, recoveryActor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("author could approve their own recovery: %v", err)
	}
	approve(op)
	op = wait(op, "needs_review")
	if op.ProviderRequestID != tencentRecoveryJob || op.DatabaseRecoveryIntent == nil ||
		!op.DatabaseRecoveryIntent.CanRetryObservation || op.DatabaseRecoveryIntent.TargetID != "" || f.asyncReads.Load() < 2 {
		t.Fatalf("inconsistent receipt for rejected history: %+v", op)
	}
	f.mode.Store(tencentRecoveryDuplicateHistory)
	retryFixtureObservation(t, store, scope, recoveryReviewer, op.ID, "native/history-second-page")
	wait(op, "needs_review")
	f.mode.Store(tencentRecoveryNormal)
	retryFixtureObservation(t, store, scope, recoveryReviewer, op.ID, "native/unique-clone-history")
	op = wait(op, "succeeded")
	assertTencentRecoveryRegistered(t, db, op, tencentRecoveryTarget)

	f.mode.Store(tencentRecoveryLostReceipt)
	lost := submit("lost-native-receipt")
	approve(lost)
	lost = wait(lost, "needs_review")
	if lost.ProviderRequestID != "" || lost.DatabaseRecoveryIntent == nil || !lost.DatabaseRecoveryIntent.CanRetryObservation ||
		lost.DatabaseRecoveryIntent.TargetID != "" || f.writes.Load() != 2 {
		t.Fatalf("lost receipt failed to remain recoverable by independent observation: %+v", lost)
	}
	f.mode.Store(tencentRecoveryNormal)
	retryFixtureObservation(t, store, scope, recoveryReviewer, lost.ID, "native/approved-security-group")
	lost = wait(lost, "succeeded")
	assertTencentRecoveryRegistered(t, db, lost, tencentRecoveryLost)
	if f.writes.Load() != 2 || f.dryRuns.Load() != 3 {
		t.Fatal("observation submitted a duplicate clone")
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	detail := scheduleResponse(t, app, http.MethodGet, "/v1/tenants/tenant/projects/project/operations/"+lost.ID, recoveryActor, "", "", http.StatusOK)
	for _, private := range []string{f.accepted.Load().marker, "fixture-private-cdb-key", fixtureDatabaseHost} {
		if strings.Contains(detail.Body.String(), private) {
			t.Fatal("operation output exposed native verification data")
		}
	}
}

func seedTencentDatabaseRecovery(t *testing.T, db *sql.DB, f *tencentDatabaseRecoveryFixture) {
	t.Helper()
	ctx := t.Context()
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region, volume: "fixture-volume",
		id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), recoveryActor, recoveryReviewer)
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_scopes
		(tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant',$1,$2,kind,1,now() FROM unnest(ARRAY['database-instance','network','subnet','security-group']) kind`, f.vendor, f.region); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links,database_instance)
		SELECT scope_id,source.id,source.name,'Running',1,source.links,source.db FROM cmp_inventory_scopes JOIN (VALUES
			('database-instance',$3::text,'source-db',jsonb_build_array(jsonb_build_object('kind','network','relation','in_network','id',$4::text)),
				jsonb_build_object('product','tencent-cdb','engine','mysql','version','8.0')),
			('network',$5::text,'isolated-vpc','[]'::jsonb,NULL),
			('subnet',$6::text,'isolated-subnet',jsonb_build_array(jsonb_build_object('kind','network','relation','in_network','id',$5::text),
				jsonb_build_object('kind','zone','relation','in_zone','id',$7::text)),NULL),
			('security-group',$8::text,'isolated-group','[]'::jsonb,NULL)
		) AS source(kind,id,name,links,db) USING(kind)
		WHERE tenant_id='tenant' AND account_id=$1 AND region=$2`,
		f.vendor, f.region, f.id, f.databaseVPC(), f.placement.VPC, f.placement.Subnet, f.placement.Zone, f.placement.SecurityGroup); err != nil {
		t.Fatal(err)
	}
}

func assertTencentRecoveryRegistered(t *testing.T, db *sql.DB, op operations.Operation, target string) {
	t.Helper()
	if op.DatabaseRecoveryIntent == nil || op.DatabaseRecoveryIntent.TargetID != target ||
		op.DatabaseRecoveryIntent.CPU != 4 || op.DatabaseRecoveryIntent.MemoryMiB != 8192 {
		t.Fatalf("operation did not publish the observed clone and approved sizes: %+v", op)
	}
	var product, owner, sourceName string
	var events int
	err := db.QueryRowContext(t.Context(), `SELECT target.database_instance->>'product', own.project_id,source.name,
		(SELECT count(*) FROM cmp_operation_event_outbox AS o JOIN cmp_instance_operation_events AS e ON e.event_id=o.sequence
		 WHERE o.event_type='database-instance.operation.succeeded' AND e.operation_id=i.operation_id)
		FROM cmp_database_recovery_intents AS i
		JOIN cmp_inventory_objects AS target ON (target.scope_id,target.external_id)=(i.registered_scope_id,i.provider_resource_id)
		JOIN cmp_inventory_objects AS source ON source.scope_id=target.scope_id AND source.external_id=$2
		JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(target.scope_id,target.external_id)
		WHERE i.operation_id=$1 AND target.external_id=$3`, op.ID, tencentRecoverySource, target).Scan(&product, &owner, &sourceName, &events)
	if err != nil || product != provider.TencentCDB || owner != "project" || sourceName != "source-db" || events != 1 {
		t.Fatalf("clone was not published with its ownership and terminal event: %s %s %s %d %v", product, owner, sourceName, events, err)
	}
}
