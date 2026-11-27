package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const bucketScheduleToken = "fixture-bucket-schedule-operator"
const bucketScheduleRef = "fixture/snapshot-delete"

func TestAcceptanceBucketSettingSchedules(t *testing.T) {
	for _, f := range []*bucketFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "tencent", location: "ap-beijing"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			command: "alibaba", location: "cn-shanghai"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			command: "huawei", location: "cn-north-4"},
	} {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testBucketSettingSweeps(t, f)
		})
	}
}

func testBucketSettingSweeps(t *testing.T, f *bucketFixture) {
	t.Helper()
	db, app, execute := setupBucketSettingSweep(t, f)
	root := "/v1/tenants/tenant/projects/project/cloud-accounts/" + f.vendor + "/inventory-schedules"
	project := "/v1/tenants/tenant/projects/project/governance/"
	var mode atomic.Int32
	mode.Store(1)
	f.settingFault = func(w http.ResponseWriter, r *http.Request, name string) bool {
		if r.URL.RawQuery != "encryption" || mode.Load() != 1 {
			return false
		}
		switch name {
		case f.names[1]:
			w.WriteHeader(http.StatusForbidden)
		case f.names[3]:
			w.Header().Set("Retry-After", "75")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			return false
		}
		return true
	}
	add := func(kind, region string, code int) *httptest.ResponseRecorder {
		t.Helper()
		body := fmt.Sprintf(`{"kind":%q,"region":%q,"interval_seconds":600,"enabled":true}`, kind, region)
		return scheduleResponse(t, app, "POST", root, bucketScheduleToken, "", body, code)
	}
	add(controller.BucketEncryptionScheduleKind, f.region, 400)
	const register = `{"kind":"object-bucket-encryption","region":"global","interval_seconds":600,"enabled":true}`
	scheduleResponse(t, app, "POST", root, "fixture-bucket-schedule-reviewer", "", register, 403)
	scheduleResponse(t, app, "POST", "/v1/tenants/tenant/projects/other/cloud-accounts/"+f.vendor+"/inventory-schedules", bucketScheduleToken, "", register, 403)
	encryption := decodeSchedule(t, add(controller.BucketEncryptionScheduleKind, "global", 201))
	var freshness inventory.FreshnessReport
	decodeProjectData(t, scheduleResponse(t, app, "GET", project+"inventory-freshness", bucketScheduleToken, "", "", 200), &freshness)
	if freshness.Summary.Total != 0 {
		t.Fatalf("specialized plans entered full inventory counts: %+v", freshness.Summary)
	}

	baseline := f.firstReads.Load()
	first := runBucketSettingBatch(t, db, execute, encryption.ID, 1)
	if first.outcome != "throttled" || first.cursor != f.names[2]+"@"+fixtureBucketOld ||
		first.rejected != 1 || first.cooldown < 70 || f.firstReads.Load()-baseline != 2 {
		t.Fatalf("batch did not commit checked prefix and cool down: %+v; list calls: %d", first, f.firstReads.Load()-baseline)
	}
	assertBucketSweepCoverage(t, app, project, 2, 0)
	mode.Store(0)
	bucketSweepSQL(t, db, `UPDATE cmp_inventory_account_dispatch SET cooldown_until=NULL`)
	dueBucketSweep(t, db, encryption.ID)
	last := runBucketSettingBatch(t, db, execute, encryption.ID, 2)
	if last.cursor != "" || last.outcome != "success" || last.rejected != 0 || last.next < 590 {
		t.Fatalf("last batch did not complete the round: %+v", last)
	}
	assertBucketSweepCoverage(t, app, project, len(f.names)-1, 0)

	version := decodeSchedule(t, add(controller.BucketVersioningScheduleKind, "global", 201))
	f.versionMode.Store(5)
	baseline = f.firstReads.Load()
	first = runBucketSettingBatch(t, db, execute, version.ID, 1)
	if first.outcome != "success" || first.cursor != f.names[provider.BucketSettingBatchLimit-1]+"@"+fixtureBucketOld ||
		first.rejected != 1 || first.next > 5 || f.firstReads.Load()-baseline != 2 {
		t.Fatalf("bounded batch did not quarantine recreated bucket: %+v; list calls: %d", first, f.firstReads.Load()-baseline)
	}
	assertBucketSweepCoverage(t, app, project, len(f.names)-1, provider.BucketSettingBatchLimit-1)
	f.recreated.Store(false)
	f.versionMode.Store(0)
	dueBucketSweep(t, db, version.ID)
	last = runBucketSettingBatch(t, db, execute, version.ID, 2)
	if last.outcome != "success" || last.cursor != "" || last.next < 590 {
		t.Fatalf("second versioning batch did not finish: %+v", last)
	}
	assertBucketSweepCoverage(t, app, project, len(f.names)-1, len(f.names)-1)
	list := accountPageData[controller.Schedule](t, scheduleResponse(t, app, "GET", root, bucketScheduleToken, "", "", 200))
	if len(list.Items) != 2 || list.Items[0].BucketBatchRejected == nil || *list.Items[0].BucketBatchRejected != 0 {
		t.Fatalf("schedule API omitted persisted batch diagnostics: %+v", list)
	}
	if f.vendor == provider.TencentCVM {
		checkBucketSweepFences(t, db, app, execute, root+"/"+version.ID, version.ID, len(f.names))
	}
}

func setupBucketSettingSweep(t *testing.T, f *bucketFixture) (*sql.DB, http.Handler, controller.Execute) {
	t.Helper()
	db := dbtest.Open(t)
	if err := migrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), bucketScheduleToken, "fixture-bucket-schedule-reviewer")
	bucketSweepSQL(t, db, `UPDATE cmp_memberships SET role='admin' WHERE project_id='project' AND principal_id='operator'`)
	for i := range provider.BucketSettingBatchLimit + 3 {
		name := fmt.Sprintf("fixture-sweep-%02d", i)
		if f.vendor == provider.TencentCVM {
			name += "-1250000000"
		}
		f.names = append(f.names, name)
	}
	f.name = f.names[0]
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveBuckets(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	profile := publicCloudProfile{Ref: bucketScheduleRef, Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		profile.Regions = append(profile.Regions, f.location)
		profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"scan-" + f.command, "tenant", f.vendor, "global", "object-bucket"}
	if err := runPublicCloud(t.Context(), db, args, f.vendor, path, transport); err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return db, app, inventoryScheduleExecutor(db, map[string]string{bucketScheduleRef: path}, transport)
}

type bucketSweepState struct {
	outcome, cursor string
	rejected        int
	cooldown, next  float64
}

func runBucketSettingBatch(t *testing.T, db *sql.DB, execute controller.Execute, id string, fence int) bucketSweepState {
	t.Helper()
	stop, wait := runManagedSchedule(t, db, execute)
	defer wait()
	defer stop()
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	for {
		var state bucketSweepState
		err := db.QueryRowContext(ctx, `SELECT last_outcome,bucket_cursor,COALESCE(bucket_batch_rejected,-1),
			COALESCE(extract(epoch FROM cooldown_until-clock_timestamp()),0),extract(epoch FROM next_at-clock_timestamp())
			FROM cmp_inventory_schedules JOIN cmp_inventory_account_dispatch USING (tenant_id,account_id)
			WHERE cmp_inventory_schedules.schedule_id=$1 AND fence=$2 AND cmp_inventory_schedules.lease_until IS NULL
			AND last_outcome IS NOT NULL`, id, fence).Scan(&state.outcome, &state.cursor, &state.rejected, &state.cooldown, &state.next)
		if err == nil {
			return state
		}
		if err != sql.ErrNoRows {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("schedule %s did not commit fence %d", id, fence)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func dueBucketSweep(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	bucketSweepSQL(t, db, `UPDATE cmp_inventory_schedules SET next_at=clock_timestamp() WHERE schedule_id=$1`, id)
}

func bucketSweepSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func assertBucketSweepCoverage(t *testing.T, app http.Handler, project string, encryption, versioning int) {
	t.Helper()
	var report inventory.BucketProtectionReport
	decodeProjectData(t, scheduleResponse(t, app, "GET", project+"bucket-protection", bucketScheduleToken, "", "", 200), &report)
	if report.Summary.Encryption.Fresh != int64(encryption) || report.Summary.Versioning.Fresh != int64(versioning) {
		t.Fatalf("scheduled results missing from project report: %+v", report.Summary)
	}
}

func checkBucketSweepFences(t *testing.T, db *sql.DB, app http.Handler, execute controller.Execute, path, id string, total int) {
	t.Helper()
	readVersioning := func() (time.Time, int) {
		t.Helper()
		var latest time.Time
		var current int
		err := db.QueryRowContext(t.Context(), `SELECT max(observed_at),count(*) FILTER (WHERE credential_revision=2)
			FROM cmp_bucket_versioning`).Scan(&latest, &current)
		if err != nil {
			t.Fatal(err)
		}
		return latest, current
	}
	before, _ := readVersioning()
	dueBucketSweep(t, db, id)
	changePreparedBucketBatch(t, db, execute, func() {
		scheduleResponse(t, app, "PATCH", path, bucketScheduleToken, `"1"`, `{"enabled":false,"interval_seconds":600}`, 200)
	})
	if after, _ := readVersioning(); !after.Equal(before) {
		t.Fatal("paused batch published observations")
	}
	scheduleResponse(t, app, "PATCH", path, bucketScheduleToken, `"2"`, `{"enabled":true,"interval_seconds":600}`, 200)
	first := runBucketSettingBatch(t, db, execute, id, 6)
	if first.cursor == "" || first.rejected != 0 {
		t.Fatalf("resumed schedule missed its first page: %+v", first)
	}
	before, _ = readVersioning()
	dueBucketSweep(t, db, id)
	changePreparedBucketBatch(t, db, execute, func() {
		bucketSweepSQL(t, db, `UPDATE cmp_cloud_accounts SET credential_revision=2;
			UPDATE cmp_inventory_scopes SET credential_revision=2 WHERE kind='object-bucket'`)
	})
	if after, current := readVersioning(); !after.Equal(before) || current != 0 {
		t.Fatal("old credential generation published observations")
	}
	dueBucketSweep(t, db, id)
	waiting := runBucketSettingBatch(t, db, execute, id, 8)
	if _, current := readVersioning(); waiting.outcome != "failure" || current != 0 {
		t.Fatalf("incomplete new inventory was accepted: %+v, observations=%d", waiting, current)
	}
	bucketSweepSQL(t, db, `UPDATE cmp_inventory_scopes SET completed_credential_revision=2 WHERE kind='object-bucket'`)
	dueBucketSweep(t, db, id)
	restarted := runBucketSettingBatch(t, db, execute, id, 9)
	if _, current := readVersioning(); restarted.cursor != first.cursor || current != provider.BucketSettingBatchLimit {
		t.Fatalf("credential rotation skipped the first batch: %+v, observations=%d of %d", restarted, current, total)
	}
}

func changePreparedBucketBatch(t *testing.T, db *sql.DB, execute controller.Execute, change func()) {
	t.Helper()
	ready := make(chan error, 1)
	release := make(chan struct{})
	stop, wait := runManagedSchedule(t, db, func(ctx context.Context, task controller.Task) (controller.Publish, error) {
		publish, err := execute(ctx, task)
		ready <- err
		select {
		case <-release:
			return publish, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	defer wait()
	defer stop()
	if err := awaitManagedSchedule(t, ready); err != nil {
		t.Fatal(err)
	}
	change()
	close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	for {
		var released bool
		if err := db.QueryRowContext(ctx, `SELECT schedule_id IS NULL FROM cmp_inventory_account_dispatch`).Scan(&released); err != nil {
			t.Fatal(err)
		}
		if released {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("account lease was not released")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
