package main

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptancePublicCloudClusterWorkloads(t *testing.T) {
	for _, cluster := range publicCloudClusterFixtures() {
		t.Run(cluster.command, func(t *testing.T) {
			t.Parallel()
			testClusterWorkloads(t, cluster)
		})
	}
}

func testClusterWorkloads(t *testing.T, cluster *clusterFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-workload-inventory-operator", "fixture-workload-inventory-reviewer"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cluster.vendor, region: cluster.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, cluster.remoteIdentity(), actor, reviewer)
	f := &workloadFixture{}
	scan, refresh, profilePath, kubePath, credentials := prepareWorkloadFixture(t, db, cluster, f)
	if err := refresh(); !errors.Is(err, inventory.ErrStale) || f.calls.Load() != 0 {
		t.Fatalf("refresh without a current parent used the cluster credential: %v", err)
	}
	if err := scan(); err != nil {
		t.Fatal(err)
	}
	mismatch := credentials
	mismatch.CloudIdentity = "another-account"
	saveWorkloadProfile(t, kubePath, mismatch)
	if err := refresh(); !provider.IsFailure(err, provider.Unauthorized) || f.calls.Load() != 0 {
		t.Fatalf("misbound workload credential was used: %v", err)
	}
	saveWorkloadProfile(t, kubePath, credentials)

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	get := func(project, query string, want int) inventory.ClusterWorkloadsPage {
		t.Helper()
		values := url.Values{"account": {cluster.vendor}, "region": {cluster.region}, "limit": {"1"}}
		r := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant/projects/"+project+"/assets/kubernetes-clusters/"+cluster.id+"/workloads?"+values.Encode()+query, nil)
		r.Header.Set("Authorization", "Bearer "+actor)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" ||
			strings.Contains(w.Body.String(), workloadBearer) || strings.Contains(w.Body.String(), workloadValue) {
			t.Fatalf("workload response status=%d want=%d cache=%s", w.Code, want, w.Header().Get("Cache-Control"))
		}
		var result inventory.ClusterWorkloadsPage
		if want == http.StatusOK {
			decodeProjectData(t, w, &result)
		}
		return result
	}
	get("project", "", http.StatusNotFound)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	first := get("project", "", http.StatusOK)
	if first.Snapshot == "" || len(first.Workloads) != 1 || first.Total != 2 || first.NextOffset != 1 {
		t.Fatalf("first workload page has wrong shape: %+v", first)
	}
	item := first.Workloads[0]
	if item.Name != "billing" || item.Namespace != "allowed" || item.Ready == nil || *item.Ready != 0 {
		t.Fatalf("workload row lost identity or known zero: %+v", item)
	}
	get("other", "", http.StatusNotFound)
	get("project", "&offset=1", http.StatusBadRequest)
	get("project", "&snapshot=", http.StatusBadRequest)
	after := "&snapshot=" + url.QueryEscape(first.Snapshot) + "&offset=" + strconv.Itoa(first.NextOffset)
	second := get("project", after, http.StatusOK)
	if len(second.Workloads) != 1 || second.Workloads[0].Name != "payments" || second.NextOffset != 0 {
		t.Fatalf("second workload page has wrong shape: %+v", second)
	}
	var persisted string
	if err := db.QueryRowContext(ctx, "SELECT workloads::text FROM cmp_cluster_workloads").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, workloadBearer) || strings.Contains(persisted, workloadValue) {
		t.Fatal("raw Kubernetes workload data reached storage")
	}
	f.mode.Store(workloadExpired)
	if err := refresh(); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("expired continuation did not abort refresh: %v", err)
	}
	if get("project", "", http.StatusOK).Snapshot != first.Snapshot {
		t.Fatal("expired Kubernetes continuation replaced a complete snapshot")
	}

	var scheduleID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO cmp_inventory_schedules
		(tenant_id,account_id,region,kind,target_id,interval_seconds,scheduled_at)
		VALUES ('tenant',$1,$2,'kubernetes-workloads',$3,300,clock_timestamp()-interval '1 hour') RETURNING schedule_id`,
		cluster.vendor, cluster.region, cluster.id).Scan(&scheduleID); err != nil {
		t.Fatal(err)
	}
	f.mode.Store(workloadChanged)
	execute := inventoryScheduleExecutor(db, map[string]string{workloadRef: profilePath}, nil)
	runClusterSchedule(t, db, scheduleID, execute, "success", 0)
	got := get("project", "", http.StatusOK)
	if got.Snapshot == first.Snapshot || len(got.Workloads) != 1 || got.Workloads[0].Name != "billing-updated" {
		t.Fatalf("new workload snapshot not published: %+v", got)
	}
	get("project", after, http.StatusConflict)
	f.mode.Store(workloadThrottled)
	runClusterSchedule(t, db, scheduleID, execute, "throttled", 1)
	if get("project", "", http.StatusOK).Snapshot != got.Snapshot {
		t.Fatal("throttled schedule replaced the complete workload snapshot")
	}
	checkWorkloadScheduleFreshness(t, db, scheduleID)

	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
		SELECT tenant_id,scope_id,$1,'other',2 FROM cmp_inventory_scopes WHERE kind='kubernetes-cluster'`, cluster.id); err != nil {
		t.Fatal(err)
	}
	get("project", "", http.StatusNotFound)
	get("other", "", http.StatusOK)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_cloud_accounts SET credential_revision=credential_revision+1
		WHERE tenant_id='tenant' AND account_id=$1`, cluster.vendor); err != nil {
		t.Fatal(err)
	}
	get("other", "", http.StatusNotFound)
	before := f.calls.Load()
	if err := refresh(); !provider.IsFailure(err, provider.Unauthorized) || before != f.calls.Load() {
		t.Fatalf("previous-generation Kubernetes credential was used: %v", err)
	}
}

func checkWorkloadScheduleFreshness(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	ctx := t.Context()
	var success time.Time
	var retryDelayOK bool
	if err := db.QueryRowContext(ctx, `SELECT last_success_at,next_at>=last_finished_at+interval '45 seconds'
		FROM cmp_inventory_schedules WHERE schedule_id=$1`, id).Scan(&success, &retryDelayOK); err != nil {
		t.Fatal(err)
	}
	if success.IsZero() || !retryDelayOK {
		t.Fatalf("schedule success timestamp or Retry-After lost: %s %t", success, retryDelayOK)
	}
	for range 2 {
		if _, err := alerts.NewStore(db).EvaluateNext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var stale int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_project_alerts
		WHERE rule='scan_stale' AND subject_id=$1`, strconv.FormatInt(id, 10)).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("recent targeted refresh produced a stale alert: count=%d error=%v", stale, err)
	}
}
