package cloudtest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/delivery"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const pitrServiceToken = "fixture-pitr-service-operator-token-value"
const pitrParkedInstance = "ins-parked"

func TestOperationWorkerRejectsUnknownMode(t *testing.T) {
	t.Setenv("CLOUD_MANAGER_DATABASE_URL", "postgresql://postgres@127.0.0.1/postgres")
	t.Setenv("CLOUD_MANAGER_WORKER_MODE", "observer")
	t.Setenv("CLOUD_MANAGER_WORKER_TENANT", "")
	if err := cloudmanager.Run(t.Context(), []string{"worker"}); err == nil ||
		err.Error() != "CLOUD_MANAGER_WORKER_MODE must be dispatch or observe-only" {
		t.Fatalf("unexpected worker startup result: %v", err)
	}
}

func TestOperationWorkerRejectsInvalidTenant(t *testing.T) {
	t.Setenv("CLOUD_MANAGER_DATABASE_URL", "postgresql://postgres@127.0.0.1/postgres")
	t.Setenv("CLOUD_MANAGER_WORKER_MODE", "observe-only")
	t.Setenv("CLOUD_MANAGER_WORKER_TENANT", "tenant/project")
	if err := cloudmanager.Run(t.Context(), []string{"worker"}); err == nil || err.Error() != "CLOUD_MANAGER_WORKER_TENANT must be a tenant ID" {
		t.Fatalf("unexpected dedicated worker startup result: %v", err)
	}
}

func TestAcceptanceCloudManagerPointInTimeRecovery(t *testing.T) {
	if os.Getenv("CLOUD_MANAGER_TEST_PITR") != "1" {
		t.Skip("set CLOUD_MANAGER_TEST_PITR=1 to run the physical PostgreSQL recovery drill")
	}
	primary, backup, archive := newPITRClusters(t)
	source := primary.start(t)
	fixture := seedPITRPower(t, source)
	receiver := startPITREventReceiver(t, filepath.Dir(primary.data))
	if err := delivery.NewStore(source).Register(t.Context(), receiver.destination); err != nil {
		t.Fatal(err)
	}
	sourceAPI := pitrAPI(t, source)
	pitrCommand(t, "pg_basebackup", "--dbname", primary.dsn(), "--pgdata", backup.data, "--wal-method=stream", "--checkpoint=fast", "--no-password")

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.serve(t, w, r)
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cloud := cloudFixtureTransport{endpoint, server.Client().Transport}
	applied, acknowledge := make(chan struct{}), make(chan struct{})
	sourceTransport := pitrPowerTransport(func(req *http.Request) (*http.Response, error) {
		response, err := cloud.RoundTrip(req)
		if req.Header.Get("X-TC-Action") == "StopInstances" && fixture.writes.Load() == 2 {
			close(applied)
			select {
			case <-acknowledge:
			case <-req.Context().Done():
			}
		}
		return response, err
	})
	path := pitrWorkerProfile(t, fixture)
	sourceWorker := startPITRWorker(t, source, path, "dispatch", sourceTransport)

	prior := submitPITRPower(t, sourceAPI, fixture, "start", "before-point")
	waitPITRDone(t, source, prior.ID)
	priorDelivery := assertPITREvidence(t, source, prior.ID, "succeeded", "done", true)
	if fixture.writes.Load() != 1 || !fixture.running.Load() {
		t.Fatal("pre-target startup was not confirmed by the provider fixture")
	}
	pending := submitPITRPower(t, sourceAPI, fixture, "stop", "in-flight")
	select {
	case <-applied:
	case <-time.After(8 * time.Second):
		t.Fatal("provider fixture did not apply the in-flight stop")
	}
	assertPITREvidence(t, source, pending.ID, "applying", "leased", false)
	parked := submitFixturePower(t, sourceAPI, fixture.provider, fixture.region, pitrParkedInstance,
		pitrServiceToken, "start", "parked-before-point", http.StatusAccepted)
	pitrExec(t, source, "UPDATE cmp_operation_tasks SET available_at=clock_timestamp()+interval '1 hour' WHERE id=$1", parked.ID)
	var markerFile string
	if err := source.QueryRowContext(t.Context(), "SELECT pg_walfile_name(pg_create_restore_point($1))", pitrRestorePoint).Scan(&markerFile); err != nil {
		t.Fatal(err)
	}
	close(acknowledge)
	waitPITRDone(t, source, pending.ID)
	sourceWorker()
	pendingDelivery := assertPITREvidence(t, source, pending.ID, "succeeded", "done", true)
	later := submitPITRPower(t, sourceAPI, fixture, "start", "after-point")
	assertPITREvidence(t, source, later.ID, "queued", "queued", false)
	receiver.publish(t, source)
	receiver.assertEffects(t, prior.ID, pending.ID, 1)
	receipts := receiver.receiptManifest(t)
	archivePITRTail(t, source, markerFile, archive)
	pitrCommand(t, "pg_ctl", "-D", primary.data, "-m", "fast", "-w", "stop")

	started := time.Now()
	restored := backup.startRestore(t, archive)
	restoredAPI := pitrAPI(t, restored)
	t.Logf("physical restore to ready took %s", time.Since(started).Round(time.Millisecond))
	if got := assertPITREvidence(t, restored, prior.ID, "succeeded", "done", true); got != priorDelivery {
		t.Fatal("pre-target delivery identity changed after recovery")
	}
	assertPITREvidence(t, restored, pending.ID, "applying", "leased", false)
	assertPITREvidence(t, restored, parked.ID, "queued", "queued", false)
	assertPITRProjection(t, restored, fixture.instance, "RUNNING")
	var missing bool
	if err := restored.QueryRowContext(t.Context(), `SELECT NOT EXISTS (SELECT 1 FROM cmp_instance_operations WHERE operation_id=$1)
		AND NOT EXISTS (SELECT 1 FROM cmp_operation_tasks WHERE id=$1)
		AND NOT EXISTS (SELECT 1 FROM cmp_project_audit WHERE subject_id=$1)`, later.ID).Scan(&missing); err != nil || !missing {
		t.Fatalf("restore included a post-target request: %v", err)
	}
	if got := submitPITRPower(t, restoredAPI, fixture, "start", "before-point"); got.ID != prior.ID || got.Status != "succeeded" {
		t.Fatalf("pre-target key changed after recovery: %+v", got)
	}
	if got := submitPITRPower(t, restoredAPI, fixture, "stop", "in-flight"); got.ID != pending.ID || got.Status != "applying" {
		t.Fatalf("in-flight key changed after recovery: %+v", got)
	}
	reconcile := func() delivery.Reconciliation {
		t.Helper()
		report, err := delivery.NewStore(restored).ReconcileTerminalReceipts(t.Context(), receiver.destination, receipts)
		if err != nil {
			t.Fatal(err)
		}
		return report
	}
	if report := reconcile(); report.Clean || report.Counts["matched"] != 1 || report.Counts["source_unsettled"] != 1 {
		t.Fatalf("unexpected reconciliation before cloud readback: %+v", report.Counts)
	}

	// 缩短演练时间，先校验原租约存在，再让它按正常领取路径过期。
	pitrExec(t, restored, "UPDATE cmp_operation_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1 AND status='leased'", pending.ID)
	pitrExec(t, restored, "UPDATE cmp_operation_tasks SET available_at=clock_timestamp()-interval '1 hour' WHERE id=$1", parked.ID)
	readsBefore := fixture.reads.Load()
	restoreWorker := startPITRWorker(t, restored, path, "observe-only", cloud)
	waitPITRDone(t, restored, pending.ID)
	restoreWorker()
	if got := assertPITREvidence(t, restored, pending.ID, "succeeded", "done", true); got == pendingDelivery {
		t.Fatal("recovery fixture did not regenerate the terminal delivery")
	}
	assertPITREvidence(t, restored, parked.ID, "queued", "queued", false)
	var attempts int
	if err := restored.QueryRowContext(t.Context(), "SELECT attempts FROM cmp_operation_tasks WHERE id=$1", parked.ID).
		Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("undispatched task consumed attempts: %d, %v", attempts, err)
	}
	assertPITRProjection(t, restored, fixture.instance, "STOPPED")
	if fixture.writes.Load() != 2 || fixture.reads.Load() <= readsBefore || fixture.running.Load() {
		t.Fatal("restored operation failed to observe the original effect without resending")
	}
	if report := reconcile(); !report.Clean || report.Counts["matched"] != 1 || report.Counts["regenerated"] != 1 {
		t.Fatalf("unexpected reconciliation before resuming delivery: %+v", report.Counts)
	}
	receiver.publish(t, restored)
	receiver.assertEffects(t, prior.ID, pending.ID, 2)
}

type pitrPowerTransport func(*http.Request) (*http.Response, error)

func (f pitrPowerTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func seedPITRPower(t *testing.T, db *sql.DB) *powerFixture {
	t.Helper()
	if err := cloudmanager.MigrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	fixture := &powerFixture{publicCloudResourceFixture: publicCloudResourceFixture{
		provider: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou",
		key: "fixture-key", secret: "fixture-secret", session: "fixture-session",
	}, instance: "ins-pitr"}
	seedPowerDrillData(t, db, fixture, pitrParkedInstance)
	return fixture
}

func seedPowerDrillData(t *testing.T, db *sql.DB, fixture *powerFixture, parkedInstance string) {
	t.Helper()
	pitrExec(t, db, `INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','operator','service','fixture','operator');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('tenant','project','operator','operator');`)
	hash := sha256.Sum256([]byte(pitrServiceToken))
	pitrExec(t, db, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','operator',now()+interval '1 hour')`, hash[:])
	identity := fixture.account
	if fixture.provider == provider.HuaweiECS {
		identity += ":" + fixture.project
	}
	pitrExec(t, db, `INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant',$1,'project',$1,'fixture/pitr',$2,'write_enabled')`, fixture.provider, identity)
	pitrExec(t, db, `INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'instance',1,now())`, fixture.provider, fixture.region)
	pitrExec(t, db, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,'fixture-instance','stopped',1 FROM cmp_inventory_scopes
		CROSS JOIN unnest(ARRAY[$1,$2]::text[]) AS asset`, fixture.instance, parkedInstance)
}

func pitrAPI(t *testing.T, db *sql.DB) http.Handler {
	t.Helper()
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("cloud manager not ready: %d: %s", w.Code, w.Body)
	}
	return app
}

func submitPITRPower(t *testing.T, app http.Handler, f *powerFixture, action, key string) operations.Operation {
	t.Helper()
	return submitFixturePower(t, app, f.provider, f.region, f.instance, pitrServiceToken, action, key, http.StatusAccepted)
}

func pitrWorkerProfile(t *testing.T, f *powerFixture) string {
	t.Helper()
	profile := cloud.PublicCloudProfile{Ref: "fixture/pitr", Config: publiccloud.Config{
		Regions: []string{f.region}, AccountID: f.account, AllowInstancePower: true,
	}}
	if f.provider == provider.HuaweiECS {
		profile.ProjectID, profile.IAMToken = f.project, f.token
	} else {
		profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = f.key, f.secret, f.session
	}
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func startPITRWorker(t *testing.T, db *sql.DB, profile, mode string, transport http.RoundTripper) func() {
	t.Helper()
	store, err := cloud.ConfiguredOperationWorkerStore(db, mode, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(ctx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			return cloud.ConnectPublicCloudOperator(ctx, binding, profile, transport)
		})
	}()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("PITR worker stopped with error: %v", err)
			}
		case <-time.After(8 * time.Second):
			t.Error("PITR worker did not stop")
		}
	})
	t.Cleanup(stop)
	return stop
}

func waitPITRDone(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	pitrWait(t, "operation completion", func(ctx context.Context) (bool, error) {
		var state string
		if err := db.QueryRowContext(ctx, "SELECT status FROM cmp_operation_tasks WHERE id=$1", id).Scan(&state); err != nil {
			return false, err
		}
		if state == "queued" {
			_, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET available_at=clock_timestamp()
				WHERE id=$1 AND status='queued' AND available_at>clock_timestamp()`, id)
			return false, err
		}
		return state == "done", nil
	})
}

func assertPITREvidence(t *testing.T, db *sql.DB, id, operationState, taskState string, terminal bool) string {
	t.Helper()
	var op, task string
	var audited, validLease bool
	var delivery sql.NullString
	err := db.QueryRowContext(t.Context(), `SELECT o.status,t.status,
		CASE WHEN t.status='leased' THEN t.lease_until>clock_timestamp() ELSE true END,
		EXISTS (SELECT 1 FROM cmp_project_audit WHERE subject_id=o.operation_id AND category='operation' AND outcome=o.status),
		(SELECT x.public_id::text FROM cmp_instance_operation_events e JOIN cmp_operation_event_outbox x ON x.sequence=e.event_id
		  WHERE e.operation_id=o.operation_id AND e.status='succeeded')
		FROM cmp_instance_operations o JOIN cmp_operation_tasks t ON t.id=o.operation_id WHERE o.operation_id=$1`, id).
		Scan(&op, &task, &validLease, &audited, &delivery)
	if err != nil || op != operationState || task != taskState || !validLease || !audited || delivery.Valid != terminal {
		t.Fatalf("operation evidence: op=%s task=%s lease=%t audit=%t delivery=%t error=%v", op, task, validLease, audited, delivery.Valid, err)
	}
	return delivery.String
}

func assertPITRProjection(t *testing.T, db *sql.DB, instance, status string) {
	t.Helper()
	var projected string
	err := db.QueryRowContext(t.Context(), "SELECT status FROM cmp_inventory_objects WHERE external_id=$1", instance).Scan(&projected)
	if err != nil || projected != status {
		t.Fatalf("inventory status %q, want %q: %v", projected, status, err)
	}
}

func pitrExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}
