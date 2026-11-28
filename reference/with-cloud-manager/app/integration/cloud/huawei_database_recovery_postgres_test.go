package cloudtest

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
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const recoveryActor, recoveryReviewer = "fixture-database-recovery-operator", "fixture-database-recovery-reviewer"
const recoveryRoute = "/v1/tenants/tenant/projects/project/databases/recover"

func TestAcceptanceHuaweiDatabaseRecovery(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	f := &databaseRecoveryFixture{
		databaseFixture: databaseFixture{
			vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			id:                 recoverySource, product: provider.HuaweiRDS,
		},
		point: time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond),
		placement: provider.DatabaseRecoveryPlacement{VPC: recoveryVPC, Subnet: recoverySubnet, SecurityGroup: recoveryGroup,
			Flavor: "rds.mysql.s1.large", Zone: "cn-north-4a", StorageType: "CLOUDSSD", StorageGiB: 40},
	}
	seedDatabaseRecovery(t, db, f)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	defer remote.Close()
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	connect := func(ctx context.Context, enabled bool) (*publiccloud.Client, error) {
		cfg := publiccloud.Config{Regions: []string{f.region}, AccountID: f.account, ProjectID: fixtureHuaweiProject, IAMToken: fixtureHuaweiIAM}
		if enabled {
			cfg.HuaweiDatabaseRecoveryPlacements = map[string]provider.DatabaseRecoveryPlacement{f.region: f.placement}
		}
		return publiccloud.Connect(ctx, f.vendor, cfg, transport)
	}
	client, err := connect(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	assets := inventory.NewStore(db)
	assetKey := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.DatabaseInstance}
	if err := assets.RefreshDatabaseRestoreWindows(ctx, assetKey, f.id, 1, client); err != nil {
		t.Fatal(err)
	}
	observed, err := assets.ReadDatabaseRestoreWindows(ctx, recoveryActor,
		inventory.DatabaseAssetQuery{Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id})
	if err != nil {
		t.Fatal(err)
	}
	input := operations.DatabaseRecoveryInput{Account: f.vendor, Region: f.region, ObservedAt: observed.ObservedAt,
		DatabaseRecovery: provider.DatabaseRecovery{SourceID: f.id, Name: recoveryName, RestoreTime: f.point, DatabaseRecoveryPlacement: f.placement}}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	defer local.Close()
	store := operations.NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	var enabled atomic.Bool
	startDatabaseRecoveryWorker(t, store, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		client, err := connect(ctx, enabled.Load())
		if err != nil {
			return nil, err
		}
		if client.Identity() != binding.RemoteIdentity || binding.Provider != provider.HuaweiECS {
			return nil, identity.ErrForbidden
		}
		return client.InstanceOperator(), nil
	})
	submit := func(path, key, revision string, input operations.DatabaseRecoveryInput, status int) operations.Operation {
		t.Helper()
		req := httptest.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(recoveryTestJSON(t, input)))
		req.Header.Set("Authorization", "Bearer "+recoveryActor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("recovery %s status=%d want=%d body=%s", key, w.Code, status, w.Body)
		}
		var op operations.Operation
		if status == http.StatusAccepted {
			testsupport.DecodeProjectData(t, w, &op)
			if op.Action != "recover-database-instance" || op.AssetID != recoverySource || op.Status != "awaiting_approval" ||
				w.Header().Get("Location") != "/v1/tenants/tenant/projects/project/operations/"+op.ID {
				t.Fatalf("wrong restore receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, recoveryReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, recoveryActor, op.ID, state, true)
	}

	submit(strings.Replace(recoveryRoute, "/project/", "/other/", 1), "outside", `"1"`, input, http.StatusForbidden)
	submit(recoveryRoute, "wrong-revision", `"2"`, input, http.StatusPreconditionFailed)
	gap := input
	gap.RestoreTime = f.point.Add(10 * time.Second)
	submit(recoveryRoute, "in-gap", `"1"`, gap, http.StatusPreconditionFailed)
	outdated := input
	outdated.ObservedAt = input.ObservedAt.Add(-time.Minute)
	submit(recoveryRoute, "wrong-snapshot", `"1"`, outdated, http.StatusPreconditionFailed)
	sameNetwork := input
	sameNetwork.VPC = f.databaseVPC()
	submit(recoveryRoute, "source-network", `"1"`, sameNetwork, http.StatusPreconditionFailed)

	disabled := submit(recoveryRoute, "not-enabled", `"1"`, input, http.StatusAccepted)
	if _, err := store.Approve(ctx, recoveryActor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester could approve recovery: %v", err)
	}
	approve(disabled)
	wait(disabled, "failed")
	enabled.Store(true)
	f.mode.Store(1)
	drift := submit(recoveryRoute, "live-window-change", `"1"`, input, http.StatusAccepted)
	approve(drift)
	wait(drift, "failed")
	f.mode.Store(4)
	soldOut := submit(recoveryRoute, "target-sold-out", `"1"`, input, http.StatusAccepted)
	approve(soldOut)
	wait(soldOut, "failed")
	if f.writes.Load() != 0 {
		t.Fatal("invalid recovery placement or stale live window was dispatched")
	}

	f.mode.Store(2)
	var output bytes.Buffer
	err = commands.RunOperationsClient(ctx, []string{"recover-database-instance", "tenant", "project", "--idempotency-key", "apply", "--if-match", "1"},
		local.URL, recoveryActor, bytes.NewReader(recoveryTestJSON(t, input)), &output, local.Client().Transport)
	var accepted operations.Operation
	if err != nil || json.Unmarshal(output.Bytes(), &accepted) != nil || accepted.ID == "" {
		t.Fatalf("recovery submission via CLI: %v %s", err, output.String())
	}
	if again := submit(recoveryRoute, "apply", `"1"`, input, http.StatusAccepted); again.ID != accepted.ID {
		t.Fatal("restore replay created another operation")
	}
	submit(recoveryRoute, "busy", `"1"`, input, http.StatusConflict)
	sourceName := f.id
	if _, err := store.SubmitDatabaseRename(ctx, recoveryActor, scope, "rename-during-recovery", 1, operations.DatabaseRenameInput{
		Account: f.vendor, Region: f.region, AssetID: f.id, CurrentName: &sourceName, Name: "valid_name",
	}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("database rename bypassed the active recovery reservation: %v", err)
	}
	approve(accepted)
	op := wait(accepted, "needs_review")
	if op.ProviderRequestID != recoveryJob || op.DatabaseRecoveryIntent == nil || op.DatabaseRecoveryIntent.TargetID != recoveryTarget ||
		f.writes.Load() != 1 {
		t.Fatalf("mismatched job escaped review: %+v", op)
	}
	var visible bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_inventory_objects WHERE external_id=$1)`,
		recoveryTarget).Scan(&visible); err != nil || visible {
		t.Fatalf("unconfirmed target entered inventory: %t %v", visible, err)
	}
	f.mode.Store(0)
	retryNameChangeObservation(t, store, scope, recoveryReviewer, op.ID)
	op = wait(op, "succeeded")
	assertDatabaseRecoveryRegistered(t, db, op)
	detail := scheduleResponse(t, app, http.MethodGet, "/v1/tenants/tenant/projects/project/operations/"+op.ID, recoveryActor, "", "", http.StatusOK)
	if strings.Contains(detail.Body.String(), recoveryKeyID) {
		t.Fatal("operation output disclosed the native disk encryption key ID")
	}
	if f.writes.Load() != 1 || f.reads.Load() != 2 {
		t.Fatal("confirmed operation did not use a read-only retry")
	}

	f.mode.Store(3)
	lost := submit(recoveryRoute, "lost-receipt", `"1"`, input, http.StatusAccepted)
	approve(lost)
	op = wait(lost, "needs_review")
	if op.ProviderRequestID != "" || op.DatabaseRecoveryIntent == nil || op.DatabaseRecoveryIntent.TargetID != "" || f.writes.Load() != 2 {
		t.Fatalf("lost receipt was retried or inferred from a name: %+v", op)
	}
	if _, err := store.RetryCreationObservation(ctx, recoveryReviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "fixture/native-audit"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("observation without native receipt was queued: %v", err)
	}
}

func recoveryTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func seedDatabaseRecovery(t *testing.T, db *sql.DB, f *databaseRecoveryFixture) {
	t.Helper()
	ctx := t.Context()
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), recoveryActor, recoveryReviewer)
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_scopes
		(tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant',$1,$2,kind,1,now() FROM unnest(ARRAY['database-instance','network','subnet','security-group']) kind;
		`, f.vendor, f.region); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links,database_instance)
		SELECT scope_id,source.id,source.id,'ACTIVE',1,source.links,source.db
		FROM cmp_inventory_scopes JOIN (VALUES
			('database-instance',$3::text,jsonb_build_array(jsonb_build_object('kind','network','relation','in_network','id',$4::text)),
				jsonb_build_object('product','huawei-rds','engine','mysql','version','8.0')),
			('network',$5::text,'[]'::jsonb,NULL),('security-group',$6::text,'[]'::jsonb,NULL),
			('subnet',$7::text,jsonb_build_array(jsonb_build_object('kind','network','relation','in_network','id',$5::text)),NULL)
		) AS source(kind,id,links,db) USING (kind)
		WHERE tenant_id='tenant' AND account_id=$1 AND region=$2`,
		f.vendor, f.region, recoverySource, f.databaseVPC(), recoveryVPC, recoveryGroup, recoverySubnet); err != nil {
		t.Fatal(err)
	}
}

func startDatabaseRecoveryWorker(t *testing.T, store operations.Store, connect func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error)) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- store.RunWorker(ctx, connect) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("recovery worker did not stop")
		}
	})
}

func assertDatabaseRecoveryRegistered(t *testing.T, db *sql.DB, op operations.Operation) {
	t.Helper()
	var product, engine, owner, sourceName string
	var events int
	err := db.QueryRowContext(t.Context(), `SELECT target.database_instance->>'product',target.database_instance->>'engine',own.project_id,source.name,
		(SELECT count(*) FROM cmp_operation_event_outbox AS o JOIN cmp_instance_operation_events AS e ON e.event_id=o.sequence
		 WHERE o.event_type='database-instance.operation.succeeded' AND e.operation_id=i.operation_id)
		FROM cmp_database_recovery_intents AS i
		JOIN cmp_inventory_objects AS target ON (target.scope_id,target.external_id)=(i.registered_scope_id,i.provider_resource_id)
		JOIN cmp_inventory_objects AS source ON source.scope_id=target.scope_id AND source.external_id=$2
		JOIN cmp_asset_ownership AS own ON (own.scope_id,own.external_id)=(target.scope_id,target.external_id)
		WHERE i.operation_id=$1`, op.ID, recoverySource).Scan(&product, &engine, &owner, &sourceName, &events)
	if err != nil || product != provider.HuaweiRDS || engine != "mysql" || owner != "project" || sourceName != recoverySource || events != 1 {
		t.Fatalf("restore target and operation were not published together: %s %s %s %s %d %v", product, engine, owner, sourceName, events, err)
	}
}
