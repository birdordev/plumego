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
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func testPublicCloudDatabaseRestoreWindows(t *testing.T, db *sql.DB, f *databaseBackupsFixture, run func(...string) error,
	app http.Handler, address, actor string) string {
	t.Helper()
	params := url.Values{"account": {f.vendor}, "region": {f.region}}
	path := "/v1/tenants/tenant/projects/project/assets/databases/" + url.PathEscape(f.id) + "/restore-windows?" + params.Encode()
	refresh := func() error {
		return run("refresh-"+f.command+"-database-restore-windows", "tenant", f.vendor, f.region, f.id)
	}
	get := func(path, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := scheduleResponse(t, app, http.MethodGet, path, token, "", "", status)
		if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "fixture-download") {
			t.Fatal("restore window response exposed native response details or missed cache isolation")
		}
		return w
	}
	get(path, "", http.StatusUnauthorized)
	get(path, actor, http.StatusNotFound)
	get(path+"&region="+f.region, actor, http.StatusBadRequest)
	get(path+"&offset=1", actor, http.StatusBadRequest)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	var first inventory.DatabaseRestoreWindows
	testsupport.DecodeProjectData(t, get(path, actor, http.StatusOK), &first)
	scope := provider.DatabaseRestoreInstanceWindow
	if f.product == provider.TencentSQLServer {
		scope = provider.DatabaseRestoreToSourceWindow
	}
	multiple := f.product == provider.TencentCDB || f.product == provider.HuaweiRDS
	total := 1
	if multiple {
		total = 2
	}
	if first.ObservedAt.IsZero() || first.Scope != scope || len(first.Windows) != total ||
		!first.Windows[0].Start.Equal(f.start) || !first.Windows[0].End.Equal(f.start.Add(time.Minute)) ||
		first.Windows[0].Start.Location() != time.UTC {
		t.Fatalf("invalid restore snapshot: %+v", first)
	}
	if multiple && first.Windows[1].Start.Sub(first.Windows[0].End) < time.Minute {
		t.Fatalf("separate recovery windows were joined: %+v", first.Windows)
	}
	get(strings.Replace(path, "/projects/project/", "/projects/other/", 1), actor, http.StatusNotFound)
	get(strings.Replace(path, "/tenants/tenant/", "/tenants/outside/", 1), actor, http.StatusForbidden)
	get(strings.Replace(path, "&region="+f.region, "&region=outside", 1), actor, http.StatusNotFound)

	args := []string{"database-restore-windows", "tenant", "project", f.id, "--account", f.vendor, "--region", f.region}
	var output bytes.Buffer
	err := commands.RunAssetsClient(t.Context(), args, address, actor, &output, nil)
	var cli inventory.DatabaseRestoreWindows
	if err != nil || json.Unmarshal(output.Bytes(), &cli) != nil || len(cli.Windows) != total || !cli.ObservedAt.Equal(first.ObservedAt) {
		t.Fatalf("invalid restore windows CLI: %s %v", output.String(), err)
	}
	if err := commands.RunAssetsClient(t.Context(), append(args, "--offset", "1"), address, actor, &output, nil); err == nil {
		t.Fatal("restore windows accepted pagination flags")
	}
	for _, check := range []struct {
		mode int32
		kind provider.FailureKind
	}{{1, provider.Throttled}, {2, provider.InvalidResponse}, {4, provider.InvalidResponse}, {5, provider.InvalidResponse}} {
		if check.mode == 4 && f.product != provider.HuaweiRDS {
			continue
		}
		f.restoreMode.Store(check.mode)
		err := refresh()
		f.restoreParentDrift.Store(false)
		if !provider.IsFailure(err, check.kind) {
			t.Fatalf("invalid native observation accepted: mode=%d %v", check.mode, err)
		}
		var saved inventory.DatabaseRestoreWindows
		testsupport.DecodeProjectData(t, get(path, actor, http.StatusOK), &saved)
		if !saved.ObservedAt.Equal(first.ObservedAt) {
			t.Fatal("failed refresh replaced last complete snapshot")
		}
	}
	f.restoreMode.Store(3)
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.DatabaseInstance}
	store := inventory.NewStore(db)
	err = store.RefreshDatabaseRestoreWindows(t.Context(), key, f.id, 1, fixtureRestoreWindowReader(func(context.Context, provider.Scope, string, provider.DatabaseDetails) ([]provider.DatabaseRestoreWindow, error) {
		if err := refresh(); err != nil {
			return nil, err
		}
		return first.Windows, nil
	}))
	if !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("late observation overwrote newer restore windows: %v", err)
	}
	var updated inventory.DatabaseRestoreWindows
	testsupport.DecodeProjectData(t, get(path, actor, http.StatusOK), &updated)
	if !updated.ObservedAt.After(first.ObservedAt) || updated.Windows == nil ||
		(multiple && len(updated.Windows) != 0) ||
		(!multiple && (len(updated.Windows) != 1 || !updated.Windows[0].Start.Equal(f.start.Add(time.Minute)))) {
		t.Fatalf("newer native snapshot is unavailable: %+v", updated)
	}
	return path
}

type fixtureRestoreWindowReader func(context.Context, provider.Scope, string, provider.DatabaseDetails) ([]provider.DatabaseRestoreWindow, error)

func (read fixtureRestoreWindowReader) ReadDatabaseRestoreWindows(ctx context.Context, scope provider.Scope, id string, details provider.DatabaseDetails) ([]provider.DatabaseRestoreWindow, error) {
	return read(ctx, scope, id, details)
}
