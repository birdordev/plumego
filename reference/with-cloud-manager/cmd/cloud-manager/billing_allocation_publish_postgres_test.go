package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"with-cloud-manager/internal/billing"
)

func exerciseBillingAllocationPublication(t *testing.T, db *sql.DB, server *httptest.Server, common []string, tokens map[string]string) {
	t.Helper()
	const query = "?account=cloud&period=2026-11&source=focus-1.0&revision=2"
	const route = "/v1/tenants/tenant/projects/finance/billing/allocations"
	const policy = `{"default":[{"center":"product","weight":7500},{"center":"ops","weight":2500}]}`
	post := func(path, body, token, match string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		w := httptest.NewRecorder()
		server.Config.Handler.ServeHTTP(w, r)
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "test-sensitive-resource") {
			t.Fatalf("POST %s returned %d instead of %d: %s", path, w.Code, status, w.Body)
		}
		return w
	}
	admin, viewer := tokens["admin"], tokens["viewer"]
	previewPath := route + "/preview" + query
	post(previewPath, policy, viewer, "", http.StatusForbidden)
	post(strings.Replace(previewPath, "/finance/", "/shared/", 1), policy, admin, "", http.StatusForbidden)
	post(strings.Replace(previewPath, "/tenant/", "/outside/", 1), policy, admin, "", http.StatusForbidden)
	post(strings.Replace(previewPath, "&revision=2", "", 1), policy, admin, "", http.StatusBadRequest)
	post(previewPath, `{"rules":[],"Rules":[]}`, admin, "", http.StatusBadRequest)
	post(previewPath, strings.Repeat(" ", billing.MaxAllocationPolicyBytes)+policy, admin, "", http.StatusRequestEntityTooLarge)
	commands := func(action string, args ...string) []string {
		return append(append([]string{action}, common...), args...)
	}
	var preview billing.AllocationPreview
	var output bytes.Buffer
	if err := runBillingClient(t.Context(), commands("allocation-preview", "2"), server.URL, admin, strings.NewReader(policy), &output, nil); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &preview); err != nil || preview.CurrentVersion != "0" || preview.SourceRows != 1 ||
		preview.ReportVersion != "2" || len(preview.Totals) != 2 {
		t.Fatalf("unexpected CLI preview: %s, %v", output.String(), err)
	}
	publication := func(value billing.AllocationPreview) string {
		return route + query + "&report_sha256=" + value.ReportDigest + "&policy_sha256=" + value.PolicyDigest
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_billing_allocations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("preview modified allocations: %d %v", count, err)
	}
	post(publication(preview), policy, admin, "", http.StatusPreconditionFailed)
	post(publication(preview), `{}`, admin, `"0"`, http.StatusPreconditionFailed)
	wrongReport := preview
	wrongReport.ReportDigest = strings.Repeat("0", 64)
	post(publication(wrongReport), policy, admin, `"0"`, http.StatusPreconditionFailed)
	post(publication(preview), policy, viewer, `"0"`, http.StatusForbidden)

	output.Reset()
	if err := runBillingClient(t.Context(), commands("allocation-publish", "2", "0", preview.ReportDigest, preview.PolicyDigest),
		server.URL, admin, strings.NewReader(policy), &output, nil); err != nil {
		t.Fatal(err)
	}
	var result billing.Allocation
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Version != "1" || result.ReportVersion != "2" ||
		!reflect.DeepEqual(result.Totals, preview.Totals) {
		t.Fatalf("published values differ from their preview: %s, %v", output.String(), err)
	}
	replay := post(publication(preview), policy, admin, `"0"`, http.StatusOK)
	decodeProjectData(t, replay, &result)
	if !result.Replayed || result.Version != "1" || replay.Header().Get("ETag") != `"1"` {
		t.Fatalf("unexpected replay: %+v", result)
	}

	drafts := []string{`{"default":[{"center":"a","weight":10000}]}`, `{"default":[{"center":"b","weight":10000}]}`}
	status := make(chan int, len(drafts))
	paths := make([]string, len(drafts))
	for i, draft := range drafts {
		var changed billing.AllocationPreview
		decodeProjectData(t, post(previewPath, draft, admin, "", http.StatusOK), &changed)
		if changed.CurrentVersion != "1" {
			t.Fatalf("unexpected version: %+v", changed)
		}
		paths[i] = publication(changed)
	}
	for i, draft := range drafts {
		go func(path, body string) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+admin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("If-Match", `"1"`)
			w := httptest.NewRecorder()
			server.Config.Handler.ServeHTTP(w, req)
			status <- w.Code
		}(paths[i], draft)
	}
	first, second := <-status, <-status
	if first+second != http.StatusOK+http.StatusPreconditionFailed || min(first, second) != http.StatusOK {
		t.Fatalf("concurrent publication returned %d and %d", first, second)
	}

	const third = `{"default":[{"center":"audit-failure","weight":10000}]}`
	var final billing.AllocationPreview
	decodeProjectData(t, post(previewPath, third, admin, "", http.StatusOK), &final)
	if final.CurrentVersion != "2" {
		t.Fatalf("missing committed version: %+v", final)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE FUNCTION reject_fixture_allocation_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END; $$;
		CREATE TRIGGER reject_fixture_allocation_audit BEFORE INSERT ON cmp_project_audit FOR EACH ROW
		WHEN (NEW.category='billing_allocation') EXECUTE FUNCTION reject_fixture_allocation_audit();`); err != nil {
		t.Fatal(err)
	}
	post(publication(final), third, admin, `"2"`, http.StatusServiceUnavailable)
	var versions, audit int
	if err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM cmp_billing_allocations),
		(SELECT count(*) FROM cmp_project_audit WHERE category='billing_allocation' AND action='published' AND actor_id='admin')`).
		Scan(&versions, &audit); err != nil || versions != 3 || audit != 2 {
		t.Fatalf("publication and audit were not atomic: versions=%d audit=%d %v", versions, audit, err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/tenants/tenant/projects/finance/audit?category=billing_allocation", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	server.Config.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "billing_allocation") {
		t.Fatalf("allocation audit unavailable: %d %s", rec.Code, rec.Body)
	}
}
