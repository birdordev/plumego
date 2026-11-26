package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptancePublicCloudWorkloadWatch(t *testing.T) {
	for _, cluster := range publicCloudClusterFixtures() {
		t.Run(cluster.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudWorkloadWatch(t, cluster)
		})
	}
}

type watchRound struct {
	expected, next string
	change         bool
	throttle       bool
	relistFrom     string
}

func testPublicCloudWorkloadWatch(t *testing.T, cluster *clusterFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-watch-inventory-operator", "fixture-watch-inventory-reviewer"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cluster.vendor, region: cluster.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, cluster.remoteIdentity(), actor, reviewer)
	var phase atomic.Pointer[watchRound]
	f := &workloadFixture{watch: func(w http.ResponseWriter, r *http.Request, kind string) {
		round := phase.Load()
		version := r.URL.Query().Get("resourceVersion")
		if kind != "Deployment" {
			if version != "3" {
				t.Error("unaffected resource cursor advanced")
			}
			return
		}
		if round.relistFrom != "" && version == round.expected {
			writeWorkloadFixture(t, w, map[string]any{"type": "ERROR",
				"object": map[string]any{"kind": "Status", "code": 410, "reason": "Expired"}})
			return
		}
		expected := round.expected
		if round.relistFrom != "" {
			expected = round.relistFrom
		}
		if version != expected {
			t.Errorf("watch resumed from %q, expected %q", version, expected)
		}
		if round.throttle {
			w.Header().Set("Retry-After", "37")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if round.change {
			writeWorkloadFixture(t, w, map[string]any{"type": "MODIFIED", "object": map[string]any{
				"kind": "Deployment", "apiVersion": "apps/v1",
				"metadata": map[string]string{"uid": "uid-billing", "name": "billing", "namespace": "allowed", "resourceVersion": "4"},
				"status":   map[string]int{"readyReplicas": 2}}})
		}
		writeWorkloadFixture(t, w, map[string]any{"type": "BOOKMARK",
			"object": map[string]any{"metadata": map[string]string{"resourceVersion": round.next}}})
	}, listVersion: func(kind string) string {
		if round := phase.Load(); kind == "Deployment" && round != nil && round.relistFrom != "" {
			return round.relistFrom
		}
		return "3"
	}}
	scan, refresh, profile, _, _ := prepareWorkloadFixture(t, db, cluster, f)
	if err := scan(); err != nil {
		t.Fatal(err)
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}

	var plan int64
	if err := db.QueryRowContext(ctx, `INSERT INTO cmp_inventory_schedules
		(tenant_id,account_id,region,kind,target_id,interval_seconds)
		VALUES ('tenant',$1,$2,$3,$4,60) RETURNING schedule_id`,
		cluster.vendor, cluster.region, controller.WorkloadWatchScheduleKind, cluster.id).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !validCommand([]string{"workload-controller"}) {
		t.Fatal("workload controller command is unavailable")
	}
	profiles := map[string]string{workloadRef: profile}
	manifest := filepath.Join(t.TempDir(), "controller.json")
	saveWorkloadProfile(t, manifest, profiles)
	execute := inventoryScheduleExecutor(db, profiles, nil)
	runRole := func(ctx context.Context, _ controller.Execute) error {
		return runController(ctx, db, manifest, true)
	}
	run := func(round watchRound) {
		t.Helper()
		phase.Store(&round)
		outcome, failures := "success", 0
		if round.throttle {
			outcome, failures = "throttled", 1
		}
		runClusterScheduleWith(t, db, plan, execute, outcome, failures, runRole)
	}
	before := f.lists.Load()
	run(watchRound{expected: "3", next: "5", change: true})
	if f.lists.Load() != before+6 {
		t.Fatal("watch bootstrap did not list all five collections")
	}
	store := inventory.NewStore(db)
	query := inventory.ClusterSnapshotQuery{Tenant: "tenant", Project: "project", Account: cluster.vendor, Region: cluster.region, ID: cluster.id, Limit: 1}
	first, err := store.ReadClusterWorkloads(ctx, actor, query)
	if err != nil || len(first.Workloads) != 1 || first.Workloads[0].Ready == nil || *first.Workloads[0].Ready != 2 {
		t.Fatalf("watch projection missing: %+v err=%v", first, err)
	}
	run(watchRound{expected: "5", next: "6"})
	query.Snapshot, query.Offset = first.Snapshot, first.NextOffset
	second, err := store.ReadClusterWorkloads(ctx, actor, query)
	if err != nil || second.Snapshot != first.Snapshot || !second.ObservedAt.Equal(first.ObservedAt) ||
		len(second.Workloads) != 1 || second.Workloads[0].Name != "payments" || f.lists.Load() != before+6 {
		t.Fatalf("bookmark disturbed a pinned page or triggered list: %+v err=%v", second, err)
	}

	task := controller.Task{Key: inventory.Key{Tenant: "tenant", Account: cluster.vendor, Region: cluster.region, Kind: provider.KubernetesCluster},
		ScheduleKind: controller.WorkloadWatchScheduleKind, TargetID: cluster.id, Provider: cluster.vendor, CredentialRef: workloadRef, CredentialRevision: 1}
	phase.Store(&watchRound{expected: "6", next: "7"})
	stale, err := execute(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	run(watchRound{expected: "6", next: "8"})
	if err := dbutil.WithTransaction(ctx, db, nil, func(tx *sql.Tx) error { return stale(ctx, tx) }); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("same-content stale checkpoint replaced the newer cursor: %v", err)
	}
	run(watchRound{expected: "8", throttle: true})
	checkStoredWorkloadCheckpoint(t, db, plan, "8", true)

	before = f.lists.Load()
	f.mode.Store(workloadChanged)
	run(watchRound{expected: "8", next: "21", relistFrom: "20"})
	if f.lists.Load() != before+2 {
		t.Fatal("expired stream did not list exactly its own collection")
	}
	query.Offset, query.Snapshot = 0, ""
	relisted, err := store.ReadClusterWorkloads(ctx, actor, query)
	if err != nil || len(relisted.Workloads) != 1 || relisted.Workloads[0].Name != "billing-updated" {
		t.Fatalf("re-listed content not published: %+v error=%v", relisted, err)
	}
	checkStoredWorkloadCheckpoint(t, db, plan, "21", false)
	query.Snapshot = relisted.Snapshot

	phase.Store(&watchRound{})
	f.mode.Store(workloadHealthy)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadClusterWorkloads(ctx, actor, query); !errors.Is(err, inventory.ErrClusterWorkloadsChanged) {
		t.Fatalf("independent refresh failed to invalidate old pagination: %v", err)
	}
	before = f.lists.Load()
	run(watchRound{expected: "3", next: "11"})
	if f.lists.Load() != before+6 {
		t.Fatal("independent list left a detached watch cursor reusable")
	}
	checkStoredWorkloadCheckpoint(t, db, plan, "11", false)
}

func checkStoredWorkloadCheckpoint(t *testing.T, db *sql.DB, plan int64, want string, throttled bool) {
	t.Helper()
	var raw []byte
	var healthyDelay bool
	if err := db.QueryRowContext(t.Context(), `SELECT watch_versions,
		CASE WHEN $2::boolean THEN next_at>=last_finished_at+interval '37 seconds'
		ELSE next_at<last_finished_at+interval '2 seconds' END
		FROM cmp_cluster_workloads CROSS JOIN cmp_inventory_schedules WHERE schedule_id=$1`,
		plan, throttled).Scan(&raw, &healthyDelay); err != nil {
		t.Fatal(err)
	}
	var versions []provider.WorkloadVersion
	if err := json.Unmarshal(raw, &versions); err != nil || len(versions) != 5 || !healthyDelay {
		t.Fatalf("invalid persisted workload versions or retry delay: versions=%s delay=%s err=%v", raw, strconv.FormatBool(healthyDelay), err)
	}
	for _, version := range versions {
		if version.Kind == "Deployment" && version.Namespace == "allowed" && version.Version == want {
			return
		}
	}
	t.Fatalf("missing durable deployment cursor %q", want)
}
