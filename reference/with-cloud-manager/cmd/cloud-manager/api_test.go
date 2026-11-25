package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/operations"
)

func TestAcceptancePostgresReadiness(t *testing.T) {
	db := dbtest.Open(t)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	get := func(path string, wantStatus int, wantBody string) {
		t.Helper()
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != wantStatus || !strings.Contains(w.Body.String(), wantBody) || strings.Contains(w.Body.String(), "cmp_migrations") {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
		}
	}
	get("/healthz", http.StatusOK, `"ok"`)
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := identity.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := inventory.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := identity.MigrateServiceTokens(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := controller.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := identity.MigrateAccounts(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := operations.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := identity.MigrateBrowserSessions(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := audit.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := alerts.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := nativeconsole.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusOK, `"ready"`)
}
