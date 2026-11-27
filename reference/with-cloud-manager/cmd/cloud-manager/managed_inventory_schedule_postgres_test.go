package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceManagedInventorySchedule(t *testing.T) {
	db, app, _ := setupAccountInventory(t)
	root := accountSourcePath + "/tc/inventory-schedules"
	const intervals = `{"region":"ap-test","kind":"instance","interval_seconds":600,"enabled":true}`
	scheduleResponse(t, app, "POST", root, inventoryAdminToken, "", intervals, 409)
	verifyModeFixture(t, db, "tc")
	scheduleResponse(t, app, "POST", root, inventoryViewerToken, "", intervals, 403)
	scheduleResponse(t, app, "GET", accountDestPath+"/tc/inventory-schedules", inventoryAdminToken, "", "", 403)
	scheduleResponse(t, app, "POST", root, inventoryAdminToken, "", `{"region":"ap-test","kind":"dns-zone","interval_seconds":60,"enabled":true}`, 400)
	scheduleResponse(t, app, "POST", root, inventoryAdminToken, "", `{"region":"global","kind":"dns-zone","interval_seconds":60}`, 400)
	first := scheduleResponse(t, app, "POST", root, inventoryAdminToken, "", intervals, 201)
	scheduleResponse(t, app, "POST", root, inventoryAdminToken, "", intervals, 409)
	item := decodeSchedule(t, first)
	if first.Header().Get("ETag") != `"1"` || item.Kind != provider.Instance || !item.Enabled || item.Region != "ap-test" {
		t.Fatalf("unexpected schedule: %+v", item)
	}
	path := root + "/" + item.ID
	scheduleResponse(t, app, "PATCH", path, inventoryAdminToken, `"1"`, `{"interval_seconds":600,"enabled":false,"run_now":true}`, 400)
	scheduleResponse(t, app, "POST", root, inventoryAdminToken, "", `{"region":"global","kind":"dns-zone","interval_seconds":60,"enabled":false}`, 201)
	page := accountPageData[controller.Schedule](t, scheduleResponse(t, app, "GET", root+"?limit=1", inventoryAdminToken, "", "", 200))
	if len(page.Items) != 1 || page.Next != item.ID {
		t.Fatalf("unexpected first page: %+v", page)
	}
	next := accountPageData[controller.Schedule](t, scheduleResponse(t, app, "GET", root+"?limit=1&after="+page.Next, inventoryAdminToken, "", "", 200))
	if len(next.Items) != 1 || next.Items[0].Kind != provider.DNSZone || next.Next != "" {
		t.Fatalf("unexpected continuation: %+v", next)
	}
	checkSchedulePublicationFence(t, db, app, path)
	updated := scheduleResponse(t, app, "PATCH", path, inventoryAdminToken, `"3"`, `{"interval_seconds":900,"enabled":true,"run_now":true}`, 200)
	if result := decodeSchedule(t, updated); result.Revision != 4 || result.Interval != 900 || result.LastOutcome != "success" {
		t.Fatalf("unexpected immediate schedule: %+v", result)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var out bytes.Buffer
	if err := runCloudAccountsClient(t.Context(), []string{"schedules", "tenant", "source", "tc", "--limit", "1"},
		server.URL, inventoryAdminToken, nil, &out, server.Client().Transport); err != nil || !strings.Contains(out.String(), `"interval_seconds":900`) {
		t.Fatalf("schedule list CLI: %v %s", err, out.String())
	}
	var audited int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_project_audit WHERE actor_id='admin'
		AND account_id='tc' AND action LIKE 'inventory-schedule-%'`).Scan(&audited); err != nil || audited != 5 {
		t.Fatalf("unexpected schedule audit count %d: %v", audited, err)
	}
}

func checkSchedulePublicationFence(t *testing.T, db *sql.DB, app http.Handler, path string) {
	t.Helper()
	atSecond := make(chan struct{})
	unblock := make(chan struct{})
	result := make(chan error, 1)
	stop, wait := runManagedSchedule(t, db, func(ctx context.Context, task controller.Task) (controller.Publish, error) {
		err := scanManagedFixture(ctx, db, task, func(ctx context.Context, cursor string) (provider.Page, error) {
			if cursor == "" {
				return provider.Page{Objects: []provider.Object{{ID: "first"}}, Next: "next"}, nil
			}
			close(atSecond)
			select {
			case <-unblock:
				return provider.Page{Objects: []provider.Object{{ID: "late"}}}, nil
			case <-ctx.Done():
				return provider.Page{}, ctx.Err()
			}
		})
		result <- err
		return nil, err
	})
	awaitManagedSchedule(t, atSecond)
	scheduleResponse(t, app, "PATCH", path, inventoryAdminToken, `"1"`, `{"interval_seconds":600,"enabled":false}`, 200)
	scheduleResponse(t, app, "PATCH", path, inventoryAdminToken, `"1"`, `{"interval_seconds":700,"enabled":false}`, 412)
	close(unblock)
	if err := awaitManagedSchedule(t, result); err == nil {
		t.Fatal("paused schedule published a late page")
	}
	stop()
	wait()
	assertScheduleAssets(t, db, "first", false)
	scheduleResponse(t, app, "PATCH", path, inventoryAdminToken, `"2"`, `{"interval_seconds":600,"enabled":true}`, 200)
	finished := make(chan error, 1)
	stop, wait = runManagedSchedule(t, db, func(ctx context.Context, task controller.Task) (controller.Publish, error) {
		return nil, scanManagedFixture(ctx, db, task, func(_ context.Context, cursor string) (provider.Page, error) {
			if cursor != "next" {
				return provider.Page{}, errors.New("managed plan did not resume its checkpoint")
			}
			defer func() { finished <- nil }()
			return provider.Page{Objects: []provider.Object{{ID: "second"}}}, nil
		})
	})
	awaitManagedSchedule(t, finished)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		var success bool
		err := db.QueryRowContext(ctx, `SELECT last_outcome='success' FROM cmp_inventory_schedules WHERE kind='instance' AND last_outcome IS NOT NULL`).Scan(&success)
		if err == nil && success {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("resumed plan did not finish")
		case <-time.After(10 * time.Millisecond):
		}
	}
	stop()
	wait()
	assertScheduleAssets(t, db, "first,second", true)
}

type managedDiscovery func(context.Context, string) (provider.Page, error)

func (f managedDiscovery) Discover(ctx context.Context, _ provider.Scope, cursor string) (provider.Page, error) {
	return f(ctx, cursor)
}

func scanManagedFixture(ctx context.Context, db *sql.DB, task controller.Task, read managedDiscovery) error {
	store := inventory.NewStore(db)
	claim, err := store.ClaimScheduled(ctx, task.Key, time.Minute, task.CredentialRevision, task.Lease)
	if err != nil {
		return err
	}
	return scanInventoryPages(ctx, store, claim, read)
}

func runManagedSchedule(t *testing.T, db *sql.DB, execute controller.Execute) (context.CancelFunc, func()) {
	t.Helper()
	ctx, stop := context.WithCancel(t.Context())
	t.Cleanup(stop)
	done := make(chan error, 1)
	go func() { done <- controller.NewStore(db).Run(ctx, execute) }()
	return stop, func() {
		t.Helper()
		if err := awaitManagedSchedule(t, done); err != nil {
			t.Fatal(err)
		}
	}
}

func awaitManagedSchedule[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for managed inventory schedule")
		var empty T
		return empty
	}
}

func assertScheduleAssets(t *testing.T, db *sql.DB, want string, completed bool) {
	t.Helper()
	var assets string
	var hasCompletion bool
	err := db.QueryRowContext(t.Context(), `SELECT string_agg(external_id,',' ORDER BY external_id), bool_or(completed_at IS NOT NULL)
		FROM cmp_inventory_scopes JOIN cmp_inventory_objects USING (scope_id) WHERE account_id='tc'`).Scan(&assets, &hasCompletion)
	if err != nil || assets != want || hasCompletion != completed {
		t.Fatalf("unexpected scope objects=%q completed=%t error=%v", assets, hasCompletion, err)
	}
}

func decodeSchedule(t *testing.T, response *httptest.ResponseRecorder) controller.Schedule {
	t.Helper()
	var item struct {
		Data controller.Schedule `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	return item.Data
}

func scheduleResponse(t *testing.T, app http.Handler, method, path, token, revision, body string, code int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("If-Match", revision)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, req)
	if response.Code != code || strings.Contains(response.Body.String(), "fixture/ref") {
		t.Fatalf("schedule %s %s returned %d: %s", method, path, response.Code, response.Body)
	}
	return response
}
