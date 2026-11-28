package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptanceAlibabaDatabaseRecovery(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	f := &alibabaDatabaseRecoveryFixture{
		databaseFixture: databaseFixture{
			vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			id:                 alibabaRecoverySource, product: provider.AlibabaRDS,
		},
		point: time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
		placement: provider.DatabaseRecoveryPlacement{VPC: alibabaRecoveryVPC, Subnet: alibabaRecoverySubnet,
			Zone: "cn-hangzhou-b", Flavor: "mysql.n2.medium.1", StorageType: "cloud_essd", StorageGiB: 40},
	}
	f.pointRow = func(family string) map[string]any {
		return f.databaseRow(family, f.id, "source-db", "8.0", f.region)
	}
	seedAlibabaDatabaseRecovery(t, db, f)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := publiccloud.Config{Regions: []string{f.region}, AccountID: f.account, AccessKeyID: fixtureLaunchKey, AccessKeySecret: fixtureLaunchSecret,
		SecurityToken: fixtureLaunchSession, AlibabaDatabaseRecoveryPlacements: map[string]provider.DatabaseRecoveryPlacement{f.region: f.placement}}
	connect := func(ctx context.Context) (*publiccloud.Client, error) {
		return publiccloud.Connect(ctx, f.vendor, cfg, cloudFixtureTransport{endpoint, remote.Client().Transport})
	}
	client, err := connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assets := inventory.NewStore(db)
	assetKey := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.DatabaseInstance}
	if err := assets.RefreshDatabaseRestoreWindows(ctx, assetKey, f.id, 1, client); err != nil {
		t.Fatal(err)
	}
	query := inventory.DatabaseAssetQuery{Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id}
	observed, err := assets.ReadDatabaseRestoreWindows(ctx, recoveryActor, query)
	if err != nil {
		t.Fatal(err)
	}
	input := operations.DatabaseRecoveryInput{Account: f.vendor, Region: f.region, ObservedAt: observed.ObservedAt,
		DatabaseRecovery: provider.DatabaseRecovery{SourceID: f.id, Name: recoveryName, RestoreTime: f.point, DatabaseRecoveryPlacement: f.placement}}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
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
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, recoveryActor, op.ID, state, true)
	}
	submit := func(key string, input operations.DatabaseRecoveryInput) operations.Operation {
		t.Helper()
		var output bytes.Buffer
		err := runOperationsClient(ctx, []string{"recover-database-instance", "tenant", "project", "--idempotency-key", key, "--if-match", "1"},
			local.URL, recoveryActor, bytes.NewReader(recoveryTestJSON(t, input)), &output, local.Client().Transport)
		var op operations.Operation
		if err != nil || json.Unmarshal(output.Bytes(), &op) != nil || op.ID == "" || op.Status != "awaiting_approval" || op.AssetID != f.id {
			t.Fatalf("wrong clone CLI receipt: %v %s", err, &output)
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		f.token.Store(op.ID)
		if _, err := store.Approve(ctx, recoveryReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	subsecond := input
	subsecond.RestoreTime = subsecond.RestoreTime.Add(time.Millisecond)
	if _, err := store.SubmitDatabaseRecovery(ctx, recoveryActor, scope, "subsecond", 1, subsecond); !errors.Is(err, operations.ErrInvalid) {
		t.Fatalf("Alibaba accepted an ambiguous sub-second window: %v", err)
	}
	wrongZone := input
	wrongZone.Zone = "cn-hangzhou-f"
	if _, err := store.SubmitDatabaseRecovery(ctx, recoveryActor, scope, "wrong-subnet-zone", 1, wrongZone); !errors.Is(err, identity.ErrAccountStale) {
		t.Fatalf("Alibaba accepted a vSwitch outside the selected zone: %v", err)
	}
	for _, policy := range []struct {
		mode int32
		key  string
	}{
		{alibabaRecoveryPublicSource, "public-source"}, {alibabaRecoveryOpenList, "unsafe-source-whitelist"},
		{alibabaRecoverySecurityLink, "associated-security-group"},
	} {
		f.mode.Store(policy.mode)
		op := submit(policy.key, input)
		approve(op)
		wait(op, "failed")
	}
	if f.writes.Load() != 0 {
		t.Fatal("unsafe source was cloned")
	}

	f.mode.Store(alibabaRecoveryPublicTarget)
	op := submit("clone", input)
	if replay := submit("clone", input); replay.ID != op.ID {
		t.Fatal("idempotent submission changed the operation ID")
	}
	if _, err := store.Approve(ctx, recoveryActor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("clone author could approve their request: %v", err)
	}
	approve(op)
	op = wait(op, "needs_review")
	if op.ProviderRequestID != alibabaRecoveryJob || op.DatabaseRecoveryIntent == nil ||
		op.DatabaseRecoveryIntent.TargetID != alibabaRecoveryTarget || f.writes.Load() != 1 {
		t.Fatalf("unexpected clone receipt or dispatch count: %+v", op)
	}
	f.mode.Store(alibabaRecoveryWrongKey)
	retryFixtureObservation(t, store, scope, recoveryReviewer, op.ID, "evidence/private-addresses")
	op = wait(op, "needs_review")
	q := query
	q.ID = alibabaRecoveryTarget
	if _, err := assets.ReadDatabaseRestoreWindows(ctx, recoveryActor, q); !errors.Is(err, inventory.ErrDatabaseRestoreWindowsNotFound) {
		t.Fatal("unexpected target restore snapshot")
	}
	assertAlibabaRecoveryInventory(t, db, op.ID, false)
	f.mode.Store(0)
	retryFixtureObservation(t, store, scope, recoveryReviewer, op.ID, "evidence/valid-protection")
	op = wait(op, "succeeded")
	assertAlibabaRecoveryInventory(t, db, op.ID, true)
	detail := scheduleResponse(t, app, http.MethodGet, "/v1/tenants/tenant/projects/project/operations/"+op.ID, recoveryActor, "", "", http.StatusOK)
	if strings.Contains(detail.Body.String(), alibabaRecoveryKey) || strings.Contains(detail.Body.String(), fixtureDatabaseHost) ||
		strings.Contains(detail.Body.String(), "172.16.8.0") || f.writes.Load() != 1 {
		t.Fatal("native security evidence was disclosed or retry dispatched a second clone")
	}

	f.mode.Store(alibabaRecoveryLostReceipt)
	lost := submit("lost-clone-receipt", input)
	approve(lost)
	op = wait(lost, "needs_review")
	if op.OutcomeReason != "database_recovery_receipt_unavailable" || f.writes.Load() != 2 {
		t.Fatalf("accepted clone with lost receipt was resent or inferred: %+v", op)
	}
	if _, err := store.RetryCreationObservation(ctx, recoveryReviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "evidence/no-receipt"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("clone without native receipt allowed read-only retry: %v", err)
	}
}

func seedAlibabaDatabaseRecovery(t *testing.T, db *sql.DB, f *alibabaDatabaseRecoveryFixture) {
	t.Helper()
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region, volume: "fixture-volume",
		id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), recoveryActor, recoveryReviewer)
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_scopes
		(tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant',$1,$2,kind,1,now() FROM unnest(ARRAY['database-instance','network','subnet']) kind`, f.vendor, f.region); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links,database_instance)
		SELECT scope_id,source.id,source.name,'Running',1,source.links,source.db FROM cmp_inventory_scopes JOIN (VALUES
			('database-instance',$3::text,'source-db',jsonb_build_array(jsonb_build_object('kind','network','relation','in_network','id',$4::text)),
				jsonb_build_object('product','alibaba-rds','engine','mysql','version','8.0')),
			('network',$5::text,'isolated-vpc','[]'::jsonb,NULL),
			('subnet',$6::text,'isolated-subnet',jsonb_build_array(jsonb_build_object('kind','network','relation','in_network','id',$5::text),
				jsonb_build_object('kind','zone','relation','in_zone','id',$7::text)),NULL)
		) AS source(kind,id,name,links,db) USING(kind)
		WHERE tenant_id='tenant' AND account_id=$1 AND region=$2`,
		f.vendor, f.region, f.id, f.databaseVPC(), f.placement.VPC, f.placement.Subnet, f.placement.Zone); err != nil {
		t.Fatal(err)
	}
}

func assertAlibabaRecoveryInventory(t *testing.T, db *sql.DB, operation string, succeeded bool) {
	t.Helper()
	var targets, outbox int
	err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM cmp_inventory_objects o JOIN cmp_asset_ownership a USING(scope_id,external_id)
		 WHERE o.external_id=$2 AND a.project_id='project' AND o.database_instance->>'product'='alibaba-rds'),
		(SELECT count(*) FROM cmp_operation_event_outbox AS o JOIN cmp_instance_operation_events AS e ON e.event_id=o.sequence
		 WHERE o.event_type='database-instance.operation.succeeded' AND e.operation_id=$1)`,
		operation, alibabaRecoveryTarget).Scan(&targets, &outbox)
	want := 0
	if succeeded {
		want = 1
	}
	if err != nil || targets != want || outbox != want {
		t.Fatalf("inconsistent clone inventory/outbox state: %d %d %v", targets, outbox, err)
	}
}
