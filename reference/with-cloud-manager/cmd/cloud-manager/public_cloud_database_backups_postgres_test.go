package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudDatabaseBackups(t *testing.T) {
	for _, target := range []struct {
		vendor, account, region, product, id, command string
	}{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", provider.TencentCDB, "cdb-fixture", "tencent"},
		{provider.TencentCVM, "100000000001", "ap-guangzhou", provider.TencentPostgreSQL, "postgres-db-fixture", "tencent"},
		{provider.TencentCVM, "100000000001", "ap-guangzhou", provider.TencentSQLServer, "mssql-db-fixture", "tencent"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", provider.AlibabaRDS, "rm-fixture", "alibaba"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", provider.HuaweiRDS, "97000000000040008000000000000001in01", "huawei"},
	} {
		t.Run(target.product, func(t *testing.T) {
			t.Parallel()
			f := &databaseBackupsFixture{databaseFixture: &databaseFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: target.vendor, account: target.account, region: target.region},
				command:            target.command, id: target.id, product: target.product,
			}, start: time.Now().UTC().Truncate(time.Second).Add(-time.Hour)}
			testPublicCloudDatabaseBackups(t, f)
		})
	}
}

func testPublicCloudDatabaseBackups(t *testing.T, f *databaseBackupsFixture) {
	t.Helper()
	const actor = "fixture-database-backups-operator"
	const reviewer = "fixture-database-backups-reviewer"
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	f.pointRow = func(family string) map[string]any {
		if f.restoreParentDrift.Load() {
			return f.databaseRow(family, f.id+"-outside", "outside", fixtureDatabaseVersion, f.region)
		}
		return f.databaseRow(family, f.id, "backup-parent", fixtureDatabaseVersion, f.region)
	}
	cfg := publicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		cfg.ProjectID, cfg.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		cfg.AccessKeyID, cfg.AccessKeySecret, cfg.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	if f.product == provider.TencentSQLServer {
		cfg.DatabaseBackupTimeZones = map[string]string{f.id: "UTC"}
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(profile, body, 0600); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	run := func(args ...string) error {
		t.Helper()
		if !validCommand(args) {
			t.Fatalf("unregistered database backup command: %v", args)
		}
		return runPublicCloud(ctx, db, args, f.vendor, profile, transport)
	}
	if err := run("scan-"+f.command, "tenant", f.vendor, f.region, "database-instance"); err != nil {
		t.Fatal(err)
	}
	refresh := func() error { return run("refresh-"+f.command+"-database-backups", "tenant", f.vendor, f.region, f.id) }
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(app)
	t.Cleanup(local.Close)
	base := "/v1/tenants/tenant/projects/project/assets/databases/" + url.PathEscape(f.id) + "/backups"
	params := url.Values{"account": {f.vendor}, "region": {f.region}, "limit": {"100"}}
	path := base + "?" + params.Encode()
	get := func(path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := scheduleResponse(t, app, http.MethodGet, path, actor, "", "", status)
		if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "fixture-download") ||
			strings.Contains(w.Body.String(), "hidden-database-backup-name") {
			t.Fatal("database backup response disclosed restricted data")
		}
		return w
	}
	get(path, http.StatusNotFound)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	var first inventory.DatabaseBackupPage
	decodeProjectData(t, get(path, http.StatusOK), &first)
	if first.Total != 101 || first.NextOffset != 100 || len(first.Backups) != 100 || first.ObservedAt.IsZero() ||
		first.End.Sub(first.Start) != provider.DatabaseBackupWindow ||
		first.Backups[0].Status != "ready" || first.Backups[0].SizeBytes == nil || *first.Backups[0].SizeBytes != 4096 ||
		!first.Backups[0].StartedAt.Equal(f.start) {
		t.Fatalf("bad backup projection: total=%d next=%d rows=%d", first.Total, first.NextOffset, len(first.Backups))
	}
	stamp := first.ObservedAt.Format(time.RFC3339Nano)
	get(path+"&offset=100", http.StatusBadRequest)
	params.Set("offset", strconv.Itoa(first.NextOffset))
	params.Set("observed_at", stamp)
	next := base + "?" + params.Encode()
	var last inventory.DatabaseBackupPage
	decodeProjectData(t, get(next, http.StatusOK), &last)
	if len(last.Backups) != 1 || last.NextOffset != 0 {
		t.Fatalf("wrong final backup page: %+v", last)
	}
	get(strings.Replace(path, "/projects/project/", "/projects/other/", 1), http.StatusNotFound)
	get(strings.Replace(path, "/tenants/tenant/", "/tenants/outside/", 1), http.StatusForbidden)

	var output bytes.Buffer
	err = runAssetsClient(ctx, []string{"database-backups", "tenant", "project", f.id, "--account", f.vendor, "--region", f.region,
		"--offset", "100", "--observed-at", stamp}, local.URL, actor, &output, nil)
	var cli inventory.DatabaseBackupPage
	if err != nil || json.Unmarshal(output.Bytes(), &cli) != nil || len(cli.Backups) != 1 || cli.Backups[0].ID != last.Backups[0].ID {
		t.Fatalf("invalid backup CLI page: %s %v", output.String(), err)
	}
	for _, check := range []struct {
		mode int32
		kind provider.FailureKind
	}{{1, provider.Throttled}, {2, provider.InvalidResponse}} {
		f.mode.Store(check.mode)
		if err := refresh(); !provider.IsFailure(err, check.kind) {
			t.Fatalf("broken vendor page accepted: mode=%d %v", check.mode, err)
		}
		get(next, http.StatusOK)
	}
	var stored string
	if err := db.QueryRowContext(ctx, `SELECT backups::text FROM cmp_database_backups`).Scan(&stored); err != nil ||
		strings.Contains(stored, "fixture-download") || strings.Contains(stored, "hidden-database-backup-name") {
		t.Fatalf("restricted metadata was persisted or snapshot unavailable: %v", err)
	}
	f.mode.Store(3)
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.DatabaseInstance}
	store := inventory.NewStore(db)
	err = store.RefreshDatabaseBackups(ctx, key, f.id, 1, fixtureBackupReader(func(context.Context, provider.Scope, string, provider.DatabaseDetails, time.Time) ([]provider.DatabaseBackup, error) {
		if err := refresh(); err != nil {
			return nil, err
		}
		return first.Backups, nil
	}))
	if !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("late scan overwrote a newer empty snapshot: %v", err)
	}
	get(next, http.StatusConflict)
	var empty inventory.DatabaseBackupPage
	decodeProjectData(t, get(path, http.StatusOK), &empty)
	if empty.Backups == nil || empty.Total != 0 || len(empty.Backups) != 0 {
		t.Fatalf("empty native snapshot was not visible: %+v", empty)
	}
	t.Run("restore-windows", func(t *testing.T) {
		restorePath := testPublicCloudDatabaseRestoreWindows(t, db, f, run, app, local.URL, actor)
		if _, err := db.ExecContext(t.Context(), `UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE account_id=$1`, f.vendor); err != nil {
			t.Fatal(err)
		}
		scheduleResponse(t, app, http.MethodGet, path, actor, "", "", http.StatusNotFound)
		scheduleResponse(t, app, http.MethodGet, restorePath, actor, "", "", http.StatusNotFound)
	})
}

type fixtureBackupReader func(context.Context, provider.Scope, string, provider.DatabaseDetails, time.Time) ([]provider.DatabaseBackup, error)

func (read fixtureBackupReader) ReadDatabaseBackups(ctx context.Context, scope provider.Scope, id string, details provider.DatabaseDetails, at time.Time) ([]provider.DatabaseBackup, error) {
	return read(ctx, scope, id, details, at)
}
