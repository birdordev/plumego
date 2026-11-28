package cloudtest

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func TestAcceptancePublicCloudWorkloadScale(t *testing.T) {
	for _, cluster := range publicCloudClusterFixtures() {
		t.Run(cluster.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudWorkloadScale(t, cluster)
		})
	}
}

func testPublicCloudWorkloadScale(t *testing.T, cluster *clusterFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-workload-scale-operator-token", "fixture-workload-scale-reviewer-token"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cluster.vendor, region: cluster.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, cluster.remoteIdentity(), actor, reviewer)
	if _, err := db.ExecContext(ctx, "UPDATE cmp_memberships SET role='admin' WHERE principal_id='operator'"); err != nil {
		t.Fatal(err)
	}
	kube := &scaleFixture{}
	collector := &workloadFixture{mutation: func(w http.ResponseWriter, r *http.Request) bool { return kube.serve(t, w, r) }}
	scan, refresh, profilePath, readerPath, credentials := prepareWorkloadFixture(t, db, cluster, collector)
	if err := scan(); err != nil {
		t.Fatal(err)
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	writerPath := filepath.Join(t.TempDir(), "writer.json")
	credentials.Token = scaleFixtureBearer
	saveWorkloadProfile(t, writerPath, cloud.WorkloadScaleProfile{ClusterWorkloadProfile: credentials, MaxReplicas: 8})
	manifest := filepath.Join(t.TempDir(), "worker.json")
	saveWorkloadProfile(t, manifest, map[string]string{workloadRef: profilePath})
	cancel := startWorkloadScaleWorker(t, db, manifest)
	defer cancel()

	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := inventory.ClusterSnapshotQuery{Tenant: "tenant", Project: "project", Account: cluster.vendor,
		Region: cluster.region, ID: cluster.id, Limit: 1}
	snapshot, err := inventory.NewStore(db).ReadClusterWorkloads(ctx, actor, scope)
	if err != nil {
		t.Fatal(err)
	}
	const root = "/v1/tenants/tenant/projects/"
	const operationsPath = root + "project/operations/"
	path := root + "project/kubernetes-workloads/scale"
	input := operations.WorkloadScaleInput{Account: cluster.vendor, Region: cluster.region, AssetID: cluster.id, Snapshot: snapshot.Snapshot,
		WorkloadScaleTarget: provider.WorkloadScaleTarget{UID: "uid-billing", Namespace: "allowed", Kind: "Deployment", Name: "billing"}, Replicas: 4}
	submit := func(key string) operations.Operation {
		var op operations.Operation
		receipt := workloadScaleHTTP(t, app, http.MethodPost, path, actor, key, `"1"`, input, http.StatusAccepted)
		testsupport.DecodeProjectData(t, receipt, &op)
		if op.Status != "awaiting_approval" || receipt.Header().Get("Location") != operationsPath+op.ID {
			t.Fatalf("invalid workload approval receipt: %+v", op)
		}
		return op
	}
	approve := func(id string) {
		workloadScaleHTTP(t, app, http.MethodPost, operationsPath+id+"/approval", reviewer, "", "",
			map[string]string{"decision": "approve"}, http.StatusOK)
	}

	bad := input
	bad.Snapshot = "00000000-0000-4000-8000-000000000000"
	workloadScaleHTTP(t, app, http.MethodPost, path, actor, "stale", `"1"`, bad, http.StatusPreconditionFailed)
	workloadScaleHTTP(t, app, http.MethodPost, root+"other/kubernetes-workloads/scale", actor, "other", `"1"`, input, http.StatusForbidden)
	workloadScaleHTTP(t, app, http.MethodPost, path, actor, "revision", `"9"`, input, http.StatusPreconditionFailed)
	bad = input
	bad.Replicas = 0
	workloadScaleHTTP(t, app, http.MethodPost, path, actor, "zero", `"1"`, bad, http.StatusBadRequest)

	if r := workloadScaleHTTP(t, app, http.MethodGet, "/openapi.json", actor, "", "", nil, http.StatusOK); !strings.Contains(r.Body.String(), "scaleKubernetesWorkload") {
		t.Fatal("scale contract missing from OpenAPI")
	}
	op := submit("disabled")
	httpAPI := httptest.NewTLSServer(app)
	defer httpAPI.Close()
	var receipt bytes.Buffer
	args := []string{"scale-workload", "tenant", "project", "--idempotency-key", "disabled", "--if-match", "1"}
	if err := commands.RunOperationsClient(ctx, args, httpAPI.URL, actor, workloadScaleBody(t, input), &receipt, httpAPI.Client().Transport); err != nil ||
		!strings.Contains(receipt.String(), op.ID) {
		t.Fatalf("CLI did not replay same scale intent: %v", err)
	}
	approve(op.ID)
	waitWorkloadScale(t, db, op.ID, "failed")
	if kube.writes.Load() != 0 {
		t.Fatal("missing writer config allowed a scale")
	}
	profile := cloud.PublicCloudProfile{Ref: workloadRef,
		KubernetesWorkloads:       map[string]string{cluster.region + "/" + cluster.id: readerPath},
		KubernetesWorkloadWriters: map[string]string{cluster.region + "/" + cluster.id: writerPath}}
	saveWorkloadProfile(t, profilePath, profile)

	kube.mode.Store(scaleFixtureWrongUID)
	op = submit("wrong-uid")
	approve(op.ID)
	waitWorkloadScale(t, db, op.ID, "failed")
	if kube.writes.Load() != 0 {
		t.Fatal("replacement workload was sent a write")
	}

	kube.mode.Store(scaleFixtureConflict)
	op = submit("conflict")
	approve(op.ID)
	if reason := waitWorkloadScale(t, db, op.ID, "failed"); reason != "workload_scale_rejected" || kube.replicas.Load() != 0 {
		t.Fatalf("resourceVersion conflict was accepted: %s", reason)
	}

	kube.mode.Store(scaleFixtureLoseReceipt)
	late, err := inventory.NewStore(db).PrepareClusterWorkloads(ctx, inventory.Key{
		Tenant: scope.Tenant, Account: scope.Account, Region: scope.Region, Kind: provider.KubernetesCluster,
	}, cluster.id, 1, func(context.Context) ([]provider.ClusterWorkload, error) { return snapshot.Workloads, nil })
	if err != nil {
		t.Fatal(err)
	}
	op = submit("drop-receipt")
	if replay := submit("drop-receipt"); replay.ID != op.ID {
		t.Fatal("workload request was duplicated")
	}
	assertActiveWorkloadScale(t, db, app, op.ID, cluster, actor, input)
	approve(op.ID)
	waitWorkloadScale(t, db, op.ID, "observing")
	kube.mode.Store(scaleFixtureDrift)
	expediteWorkloadScale(t, db, op.ID)
	waitWorkloadScale(t, db, op.ID, "needs_review")
	workloadScaleHTTP(t, app, http.MethodPost, operationsPath+op.ID+"/resolution", reviewer, "", "",
		map[string]string{"decision": "succeeded", "evidence_ref": "kubernetes/unverified"}, http.StatusConflict)
	kube.mode.Store(scaleFixtureReady)
	workloadScaleHTTP(t, app, http.MethodPost, operationsPath+op.ID+"/retry-observation", reviewer, "", "",
		map[string]string{"evidence_ref": "kubernetes/scale-readback"}, http.StatusOK)
	waitWorkloadScale(t, db, op.ID, "succeeded")
	if kube.writes.Load() != 2 {
		t.Fatalf("ambiguous submission was resent: %d writes", kube.writes.Load())
	}
	verifyConfirmedWorkloadScale(t, db, scope, actor, op.ID, snapshot.Snapshot)
	if err := dbutil.WithTransaction(ctx, db, nil, func(tx *sql.Tx) error { return late(ctx, tx) }); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("older workload refresh overwrote confirmed scale: %v", err)
	}
}

func assertActiveWorkloadScale(t *testing.T, db *sql.DB, app http.Handler, id string, cluster *clusterFixture, actor string, input operations.WorkloadScaleInput) {
	t.Helper()
	const base = "/v1/tenants/tenant/projects/project/"
	workloadScaleHTTP(t, app, http.MethodPost, base+"operations/"+id+"/approval", actor, "", "",
		map[string]string{"decision": "approve"}, http.StatusForbidden)
	workloadScaleHTTP(t, app, http.MethodPost, base+"kubernetes-workloads/scale", actor, "second", `"1"`, input, http.StatusConflict)
	workloadScaleHTTP(t, app, http.MethodPatch, base+"assets/ownership", actor, "", `"1"`,
		inventory.OwnershipTransfer{Kind: provider.KubernetesCluster, Account: cluster.vendor, Region: cluster.region,
			AssetID: cluster.id, TargetProject: "other"}, http.StatusConflict)
	var active int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM cmp_workload_change_reservations WHERE operation_id=$1", id).Scan(&active); err != nil || active != 1 {
		t.Fatalf("missing operation reservation: %d %v", active, err)
	}
}

func verifyConfirmedWorkloadScale(t *testing.T, db *sql.DB, query inventory.ClusterSnapshotQuery, actor, id, oldSnapshot string) {
	t.Helper()
	ctx := t.Context()
	updated, err := inventory.NewStore(db).ReadClusterWorkloads(ctx, actor, query)
	if err != nil || len(updated.Workloads) != 1 || updated.Workloads[0].Desired == nil ||
		*updated.Workloads[0].Desired != 4 || updated.Snapshot == oldSnapshot || updated.Workloads[0].Ready != nil {
		t.Fatalf("confirmed scale snapshot not updated: %+v %v", updated, err)
	}
	var outbox, active int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM cmp_operation_event_outbox AS o JOIN cmp_instance_operation_events AS e ON o.sequence=e.event_id
			WHERE e.operation_id=$1 AND o.event_type='kubernetes-cluster.operation.succeeded'),
		(SELECT count(*) FROM cmp_workload_change_reservations WHERE operation_id=$1)`, id).Scan(&outbox, &active); err != nil || outbox != 1 || active != 0 {
		t.Fatalf("terminal evidence or reservation incorrect: outbox=%d active=%d error=%v", outbox, active, err)
	}
}

func startWorkloadScaleWorker(t *testing.T, db *sql.DB, manifest string) func() {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- cloud.RunWorker(ctx, db, manifest, "", "") }()
	return func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func expediteWorkloadScale(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), "UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1 AND status='queued'", id); err != nil {
		t.Fatal(err)
	}
}

func waitWorkloadScale(t *testing.T, db *sql.DB, id, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status, reason, queue string
		err := db.QueryRowContext(ctx, `SELECT o.status,COALESCE(o.outcome_reason,''),t.status FROM cmp_instance_operations o
			JOIN cmp_operation_tasks t ON o.operation_id=t.id WHERE o.operation_id=$1`, id).Scan(&status, &reason, &queue)
		if err != nil {
			t.Fatal(err)
		}
		terminal := want == "failed" || want == "needs_review" || want == "succeeded"
		if status == want && ((terminal && queue == "done") || (!terminal && queue == "queued")) {
			return reason
		}
		select {
		case <-ctx.Done():
			t.Fatalf("expected %s, got %s/%s (%s)", want, status, queue, reason)
		case <-ticker.C:
		}
	}
}
