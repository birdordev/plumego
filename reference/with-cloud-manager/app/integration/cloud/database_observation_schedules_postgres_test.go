package cloudtest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func testDatabaseObservationSchedules(t *testing.T, db *sql.DB, app http.Handler, address, token string,
	f *databaseBackupsFixture, execute controller.Execute, backupPath, restorePath string) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE cmp_memberships SET role='admin' WHERE project_id='project' AND principal_id='operator'`)
	root := "/v1/tenants/tenant/projects/project/cloud-accounts/" + f.vendor + "/inventory-schedules"
	body := func(kind, id string) string {
		return fmt.Sprintf(`{"kind":%q,"target_id":%q,"region":%q,"interval_seconds":600,"enabled":true}`, kind, id, f.region)
	}
	restoreBody := body(controller.DatabaseRestoreWindowScheduleKind, f.id)
	for _, invalid := range []string{
		body(controller.DatabaseRestoreWindowScheduleKind, ""),
		body(string(provider.DatabaseInstance), f.id),
	} {
		scheduleResponse(t, app, http.MethodPost, root, token, "", invalid, http.StatusBadRequest)
	}
	scheduleResponse(t, app, http.MethodPost, root, token, "", body(controller.DatabaseRestoreWindowScheduleKind, "outside"), http.StatusForbidden)
	scheduleResponse(t, app, http.MethodPost, strings.Replace(root, "/projects/project/", "/projects/other/", 1), token, "", restoreBody, http.StatusForbidden)

	var output bytes.Buffer
	err := commands.RunCloudAccountsClient(t.Context(), []string{"schedule-add", "tenant", "project", f.vendor},
		address, token, strings.NewReader(restoreBody), &output, nil)
	var restore controller.Schedule
	if err != nil || json.Unmarshal(output.Bytes(), &restore) != nil || restore.TargetID != f.id || restore.ID == "" {
		t.Fatalf("CLI did not register the database target: %v %s", err, output.String())
	}
	backup := decodeSchedule(t, scheduleResponse(t, app, http.MethodPost, root, token, "",
		body(controller.DatabaseBackupScheduleKind, f.id), http.StatusCreated))
	if backup.TargetID != f.id || backup.ID == restore.ID {
		t.Fatalf("backup and restore observations did not get independent schedules: %+v", backup)
	}
	scheduleResponse(t, app, http.MethodPost, root, token, "", restoreBody, http.StatusConflict)
	page := accountPageData[controller.Schedule](t, scheduleResponse(t, app, http.MethodGet, root, token, "", "", http.StatusOK))
	if len(page.Items) != 2 || page.Items[0].TargetID != f.id || page.Items[1].TargetID != f.id {
		t.Fatalf("targeted observations are missing from the account schedules: %+v", page)
	}
	var freshness inventory.FreshnessReport
	testsupport.DecodeProjectData(t, scheduleResponse(t, app, http.MethodGet, "/v1/tenants/tenant/projects/project/governance/inventory-freshness",
		token, "", "", http.StatusOK), &freshness)
	if freshness.Summary.Total != 0 {
		t.Fatal("database observation plans entered the full inventory denominator")
	}

	readWindow := func() inventory.DatabaseRestoreWindows {
		t.Helper()
		var snap inventory.DatabaseRestoreWindows
		testsupport.DecodeProjectData(t, scheduleResponse(t, app, http.MethodGet, restorePath, token, "", "", http.StatusOK), &snap)
		return snap
	}
	oldWindow := readWindow()
	f.mode.Store(0)
	f.restoreMode.Store(0)
	stop, wait := runManagedSchedule(t, db, execute)
	stopAndWait := sync.OnceFunc(func() { stop(); wait() })
	defer stopAndWait()
	waitForDatabaseSchedules(t, db, "success", 2)
	var savedBackups inventory.DatabaseBackupPage
	testsupport.DecodeProjectData(t, scheduleResponse(t, app, http.MethodGet, backupPath, token, "", "", http.StatusOK), &savedBackups)
	savedWindow := readWindow()
	if savedBackups.Total != 101 || !savedWindow.ObservedAt.After(oldWindow.ObservedAt) ||
		len(savedWindow.Windows) == 0 || !savedWindow.Windows[0].Start.Equal(f.start) {
		t.Fatal("scheduled cloud reads did not publish both project snapshots")
	}
	const runNow = `{"interval_seconds":600,"enabled":true,"run_now":true}`
	f.restoreMode.Store(1)
	path := root + "/" + restore.ID
	scheduleResponse(t, app, http.MethodPatch, path, token, `"1"`, runNow, http.StatusOK)
	waitForDatabaseSchedules(t, db, "throttled", 1)
	var cooling bool
	err = db.QueryRowContext(t.Context(), `SELECT cooldown_until>statement_timestamp()
		FROM cmp_inventory_account_dispatch WHERE (tenant_id,account_id)=('tenant',$1)`, f.vendor).Scan(&cooling)
	if err != nil || !cooling || !readWindow().ObservedAt.Equal(savedWindow.ObservedAt) {
		t.Fatalf("throttled refresh changed the snapshot or did not cool down the account: %v", err)
	}
	stopAndWait()
	if f.product != provider.TencentCDB {
		return
	}
	exec(`UPDATE cmp_inventory_account_dispatch SET cooldown_until=NULL WHERE account_id=$1`, f.vendor)
	exec(`UPDATE cmp_inventory_schedules SET next_at=clock_timestamp() WHERE schedule_id=$1`, restore.ID)
	f.restoreMode.Store(0)
	changePreparedSchedule(t, db, execute, func() {
		scheduleResponse(t, app, http.MethodPatch, path, token, `"2"`, `{"enabled":false,"interval_seconds":600}`, http.StatusOK)
	})
	if !readWindow().ObservedAt.Equal(savedWindow.ObservedAt) {
		t.Fatal("a disabled schedule published its late observation")
	}
	scheduleResponse(t, app, http.MethodPatch, path, token, `"3"`, runNow, http.StatusOK)
	changePreparedSchedule(t, db, execute, func() {
		exec(`UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE account_id=$1`, f.vendor)
	})
	var observed time.Time
	err = db.QueryRowContext(t.Context(), `SELECT observed_at FROM cmp_database_restore_windows WHERE external_id=$1`, f.id).Scan(&observed)
	if err != nil || !observed.Equal(savedWindow.ObservedAt) {
		t.Fatalf("previous credential revision published its late observation: %v", err)
	}
	scheduleResponse(t, app, http.MethodGet, restorePath, token, "", "", http.StatusNotFound)
}

func waitForDatabaseSchedules(t *testing.T, db *sql.DB, outcome string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_inventory_schedules
			WHERE kind IN ('database-backups','database-restore-windows') AND last_outcome=$1 AND lease_until IS NULL`, outcome).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("database schedules did not settle with %s", outcome)
		case <-ticker.C:
		}
	}
}
