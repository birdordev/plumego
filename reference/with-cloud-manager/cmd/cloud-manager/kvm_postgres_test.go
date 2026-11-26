package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/kvm"
)

const (
	fixtureKVMSite           = "/v1/tenants/lab/projects/compute/kvm/sites/site-a"
	fixtureKVMAdmin          = "fixture-kvm-project-admin-credential-0001"
	fixtureKVMOperator       = "fixture-kvm-project-operator-credential-0001"
	fixtureKVMSecondOperator = "fixture-kvm-second-operator-credential-0001"
	fixtureKVMAgent          = "fixture-kvm-host-one-agent-credential-0001"
	fixtureKVMReplacement    = "fixture-kvm-replacement-agent-credential-0001"
	fixtureKVMReviewer       = "fixture-kvm-independent-reviewer-credential-0001"
	fixtureKVMGuest          = "fixture-kvm-another-tenant-credential-0001"
	fixtureKVMUUID           = "feaa8a19-4765-4bb4-a383-3a1011ba0001"
)

func prepareKVM(t *testing.T) (*sql.DB, http.Handler, http.Handler) {
	t.Helper()
	db, ctx := dbtest.Open(t), t.Context()
	db.SetMaxOpenConns(12)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('lab'),('elsewhere');
		INSERT INTO cmp_projects VALUES ('lab','compute'),('lab','other'),('elsewhere','compute');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
		('lab','administrator','service','kvm-test','administrator'),
		('lab','operator','service','kvm-test','operator'),('lab','operator-2','service','kvm-test','operator-2'),
		('lab','reviewer','service','kvm-test','reviewer'),('lab','host-one-agent','service','kvm-test','host-one-agent'),('lab','replacement-agent','service','kvm-test','replacement-agent'),
		('elsewhere','administrator','service','kvm-test','administrator');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
		('lab','compute','administrator','admin'),('lab','other','administrator','admin'),
		('lab','compute','operator','operator'),('lab','compute','operator-2','operator'),
		('lab','compute','reviewer','approver'),('lab','compute','host-one-agent','viewer'),('lab','compute','replacement-agent','viewer'),
		('elsewhere','compute','administrator','admin')`); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []struct{ tenant, principal, token string }{
		{"lab", "administrator", fixtureKVMAdmin}, {"lab", "operator", fixtureKVMOperator},
		{"lab", "operator-2", fixtureKVMSecondOperator}, {"lab", "reviewer", fixtureKVMReviewer}, {"lab", "host-one-agent", fixtureKVMAgent},
		{"lab", "replacement-agent", fixtureKVMReplacement}, {"elsewhere", "administrator", fixtureKVMGuest},
	} {
		tokenHash := sha256.Sum256([]byte(subject.token))
		if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,$2,$3,now()+interval '15 minutes')`, tokenHash[:], subject.tenant, subject.principal); err != nil {
			t.Fatal(err)
		}
	}
	replica := func() http.Handler {
		app, err := newAPI(db, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Prepare(); err != nil {
			t.Fatal(err)
		}
		return app
	}
	return db, replica(), replica()
}

func kvmRequest(t *testing.T, handler http.Handler, token, method, path, match, key string, data any) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if data != nil {
		var err error
		body, err = json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if match != "" {
		request.Header.Set("If-Match", match)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func expectKVM(t *testing.T, response *httptest.ResponseRecorder, want int, data any) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d, response = %s", response.Code, want, response.Body)
	}
	if data != nil {
		decodeProjectData(t, response, data)
	}
}

func fixtureKVMHost() kvm.HostSpec {
	return kvm.HostSpec{ID: "host-one", UUID: fixtureKVMUUID, Agent: "host-one-agent",
		FailureDomain: "rack-1", Networks: []string{"app", "backup"}, StoragePool: "local", GPUModel: "a10"}
}

func fixtureKVMReport() kvm.Report {
	return kvm.Report{UUID: fixtureKVMUUID, Generation: 1, Sequence: 1,
		Capacity: kvm.Capacity{VCPU: 4, MemoryMiB: 4096, DiskMiB: 16384, GPU: 2}}
}

func fixtureKVMPlacement() kvm.PlacementInput {
	return kvm.PlacementInput{Capacity: kvm.Capacity{VCPU: 1, MemoryMiB: 512, DiskMiB: 1024},
		Network: "app", StoragePool: "local", TTLSeconds: 300}
}

func TestAcceptanceKVMPlacementConcurrency(t *testing.T) {
	db, first, second := prepareKVM(t)
	hosts := fixtureKVMSite + "/hosts"
	request := fixtureKVMPlacement()
	path := fixtureKVMSite + "/placements"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", hosts, "", "", fixtureKVMHost()), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", hosts+"/host-one/capacity", "", "", fixtureKVMReport()), 200, nil)

	run := func(sameKey bool) []*httptest.ResponseRecorder {
		t.Helper()
		const requests = 16
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make([]*httptest.ResponseRecorder, requests)
		for i := range results {
			handler := first
			if i%2 == 1 {
				handler = second
			}
			wg.Go(func() {
				<-start
				key := "shared-key"
				if !sameKey {
					key = "parallel-" + strconv.Itoa(i)
				}
				results[i] = kvmRequest(t, handler, fixtureKVMOperator, "POST", path, "", key, request)
			})
		}
		close(start)
		wg.Wait()
		return results
	}
	var original kvm.Placement
	for _, result := range run(true) {
		var held kvm.Placement
		expectKVM(t, result, 201, &held)
		if original.ID != "" && held.ID != original.ID {
			t.Fatalf("same key allocated multiple placements: %q and %q", original.ID, held.ID)
		}
		original = held
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "DELETE", path+"/"+original.ID, "", "", nil), 200, nil)
	accepted := 0
	for _, result := range run(false) {
		if result.Code == http.StatusConflict {
			continue
		}
		expectKVM(t, result, 201, nil)
		accepted++
	}
	var held, reserved, releases int
	err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM cmp_kvm_placements WHERE released_at IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()),
		(SELECT count(*) FROM cmp_kvm_events WHERE action='reserve'),
		(SELECT count(*) FROM cmp_kvm_events WHERE action='release')`).Scan(&held, &reserved, &releases)
	if err != nil || accepted != 4 || held != 4 || reserved != 5 || releases != 1 {
		t.Fatalf("concurrent holds: accepted=%d held=%d reserved=%d released=%d error=%v", accepted, held, reserved, releases, err)
	}

	alternate := fixtureKVMHost()
	alternate.ID, alternate.Agent, alternate.FailureDomain = "host-two", "replacement-agent", "rack-2"
	alternate.UUID = "feaa8a19-4765-4bb4-a383-3a1011ba0002"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", hosts, "", "", alternate), 201, nil)
	report := fixtureKVMReport()
	report.UUID = alternate.UUID
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", hosts+"/host-two/capacity", "", "", report), 200, nil)
	var fallback kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", path, "", "fallback", request), 201, &fallback)
	if fallback.Host != alternate.ID {
		t.Fatalf("no fallback to another eligible host: %+v", fallback)
	}

	if _, err := db.ExecContext(t.Context(), `CREATE FUNCTION reject_kvm_test_event() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture journal failure'; END; $$;
		CREATE TRIGGER reject_kvm_test_event BEFORE INSERT ON cmp_kvm_events
		FOR EACH ROW WHEN (NEW.action='reserve') EXECUTE FUNCTION reject_kvm_test_event()`); err != nil {
		t.Fatal(err)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", path, "", "journal-failure", request), 503, nil)
	var partial int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_placements
		WHERE idempotency_key='journal-failure'`).Scan(&partial); err != nil || partial != 0 {
		t.Fatalf("journal failure left partial hold: %d %v", partial, err)
	}
}
