package cloudtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

type dnsStatusFixture struct {
	dnsFixture
	writes atomic.Int32
	drift  atomic.Bool
}

func TestAcceptancePublicCloudDNSStatus(t *testing.T) {
	for _, f := range dnsStatusFixtures() {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudDNSStatus(t, f)
		})
	}
}

func dnsStatusFixtures() []*dnsStatusFixture {
	return []*dnsStatusFixture{
		{dnsFixture: dnsFixture{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou", parent: "fixture"},
			command: "tencent", id: "6501"}},
		{dnsFixture: dnsFixture{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou", parent: "fixture"},
			command: "alibaba", id: "alidns-fixture"}},
		{dnsFixture: dnsFixture{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4", parent: "fixture"},
			command: "huawei", id: "97000000000040008000000000000001"}},
	}
}

func testPublicCloudDNSStatus(t *testing.T, f *dnsStatusFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor = "fixture-public-dns-status-operator"
	const reviewer = "fixture-public-dns-status-reviewer"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET role='admin' WHERE principal_id='operator'`); err != nil {
		t.Fatal(err)
	}
	config := cloud.PublicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveStatus(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	if err := cloud.RunPublicCloud(ctx, db, []string{"scan-" + f.command, "tenant", f.vendor, "global", "dns-zone"}, f.vendor, path, transport); err != nil {
		t.Fatal(err)
	}
	refresh := func() error {
		return cloud.RunPublicCloud(ctx, db, []string{"refresh-" + f.command + "-dns-zone", "tenant", f.vendor, f.id}, f.vendor, path, transport)
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}

	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	const root = "/v1/tenants/tenant/projects/"
	recordsPath := root + "project/assets/dns-zones/" + f.id + "/records?account=" + f.vendor + "&offset=100"
	request := func(method, path, token, key, rev string, body []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if rev != "" {
			r.Header.Set("If-Match", rev)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s returned %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	var snapshot inventory.DNSRecordsPage
	testsupport.DecodeProjectData(t, request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusOK), &snapshot)
	if len(snapshot.Records) != 1 || snapshot.Records[0].Type != "A" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	body := func(id string, observed time.Time, enabled bool) []byte {
		t.Helper()
		value, err := json.Marshal(operations.DNSRecordStatusInput{Account: f.vendor, AssetID: f.id, RecordID: id, ObservedAt: observed, Enabled: &enabled})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	startBody := body(f.recordID(), snapshot.ObservedAt, false)
	submit := func(project, key string, content []byte, status int) operations.Operation {
		t.Helper()
		w := request(http.MethodPost, root+project+"/dns-zones/record-status", actor, key, `"1"`, content, status)
		var op operations.Operation
		if status == http.StatusAccepted {
			testsupport.DecodeProjectData(t, w, &op)
			if op.Action != "set-dns-record-status" || w.Header().Get("Location") != root+project+"/operations/"+op.ID {
				t.Fatalf("wrong operation receipt: %+v", op)
			}
		}
		return op
	}
	submit("project", "stale-snapshot", body(f.recordID(), snapshot.ObservedAt.Add(-time.Second), false), http.StatusPreconditionFailed)
	submit("project", "no-change", body(f.recordID(), snapshot.ObservedAt, true), http.StatusBadRequest)
	submit("other", "other-project", startBody, http.StatusForbidden)

	store := operations.NewStore(db)
	var writeAccess atomic.Bool
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			profile := config.Config
			profile.AllowDNSRecordStatus = writeAccess.Load()
			client, err := publiccloud.Connect(ctx, binding.Provider, profile, transport)
			if err != nil {
				return nil, err
			}
			if client.Identity() != binding.RemoteIdentity {
				return nil, identity.ErrForbidden
			}
			return client.InstanceOperator(), nil
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, state, true)
	}
	disabled := submit("project", "disabled", startBody, http.StatusAccepted)
	if _, err := store.Approve(ctx, actor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-approval returned %v", err)
	}
	approve(disabled)
	if stopped := wait(disabled, "failed"); stopped.OutcomeReason != "dns_record_status_disabled" || f.writes.Load() != 0 {
		t.Fatalf("disabled profile wrote remotely: %+v", stopped)
	}

	writeAccess.Store(true)
	f.drift.Store(true)
	stale := submit("project", "changed-at-provider", startBody, http.StatusAccepted)
	approve(stale)
	if stopped := wait(stale, "failed"); stopped.OutcomeReason != "dns_record_status_preconditions_unconfirmed" || f.writes.Load() != 0 {
		t.Fatalf("remote drift reached write: %+v", stopped)
	}
	f.drift.Store(false)
	op := submit("project", "live", startBody, http.StatusAccepted)
	if repeated := submit("project", "live", startBody, http.StatusAccepted); repeated.ID != op.ID {
		t.Fatal("retry created a second operation")
	}
	submit("project", "parallel", startBody, http.StatusConflict)
	active, err := store.Get(ctx, reviewer, scope, op.ID)
	if err != nil || active.DNSRecordStatusIntent == nil || active.DNSRecordStatusIntent.RecordID != f.recordID() ||
		active.DNSRecordStatusIntent.Enabled {
		t.Fatalf("approval missing DNS intent: %+v %v", active, err)
	}
	reserved := `{"account":` + strconv.Quote(f.vendor) + `,"region":"global","kind":"dns-zone","asset_id":` + strconv.Quote(f.id) + `,"target_project":"other"}`
	request(http.MethodPatch, root+"project/assets/ownership", actor, "", `"1"`, []byte(reserved), http.StatusConflict)
	approve(op)

	applied := wait(op, "needs_review")
	if applied.OutcomeReason != "dns_record_status_requires_evidence" || f.writes.Load() != 1 {
		t.Fatalf("missing single-write review: %+v writes=%d", applied, f.writes.Load())
	}
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/asserted"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed cloud evidence: %v", err)
	}

	// 只读续查期间交错一个在变更前开始的快照写回。
	inv := inventory.NewStore(db)
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: "global", Kind: provider.DNSZone}
	writeAccess.Store(false)
	err = inv.RefreshDNSRecords(ctx, key, f.id, 1, dnsTestReaderFunc(func(context.Context, provider.Scope, provider.DNSZoneRef) ([]provider.DNSRecord, error) {
		f.drift.Store(false)
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "fixture/recheck"}); err != nil {
			return nil, err
		}
		confirmed := wait(op, "succeeded")
		if confirmed.OutcomeReason != "dns_record_status_observed" || f.writes.Load() != 1 {
			t.Fatalf("observation retry repeated write: %+v", confirmed)
		}
		return snapshot.Records, nil
	}))
	if !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("late snapshot write returned %v", err)
	}
	request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusNotFound)
	var events, reservations int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM cmp_operation_event_outbox AS o JOIN cmp_instance_operation_events AS e ON (o.sequence=e.event_id)
			WHERE o.event_type='dns-zone.operation.succeeded' AND e.operation_id=$1),
		(SELECT count(*) FROM cmp_dns_zone_change_reservations)`, op.ID).Scan(&events, &reservations); err != nil ||
		events != 1 || reservations != 0 {
		t.Fatalf("terminal accounting events=%d reservations=%d err=%v", events, reservations, err)
	}
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	testsupport.DecodeProjectData(t, request(http.MethodGet, recordsPath, actor, "", "", nil, http.StatusOK), &snapshot)
	if len(snapshot.Records) != 1 || snapshot.Records[0].Status != "disable" {
		t.Fatalf("reloaded wrong record status: %+v", snapshot)
	}
}

func (f *dnsStatusFixture) recordID() string {
	if f.vendor == provider.HuaweiECS {
		return fmt.Sprintf("%032x", 101)
	}
	return "101"
}

func (f *dnsStatusFixture) serveStatus(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	recordID := f.recordID()
	value := "192.0.2.42"
	if f.drift.Load() {
		value = "192.0.2.43"
	}
	write := false
	switch {
	case r.Host == "dnspod.tencentcloudapi.com" && (r.Header.Get("X-TC-Action") == "DescribeRecord" || r.Header.Get("X-TC-Action") == "ModifyRecordStatus"):
		write = r.Header.Get("X-TC-Action") == "ModifyRecordStatus"
		body, err := io.ReadAll(r.Body)
		var request map[string]json.RawMessage
		if err != nil || json.Unmarshal(body, &request) != nil ||
			!validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) ||
			string(request["RecordId"]) != recordID || string(request["DomainId"]) != f.id ||
			string(request["Domain"]) != `"example.test"` ||
			(write && (len(request) != 4 || string(request["Status"]) != `"DISABLE"`)) ||
			(!write && len(request) != 3) {
			t.Errorf("invalid DNSPod record status request: %s", r.Header.Get("X-TC-Action"))
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if !write {
			enabled := 1
			if f.lastDisabled.Load() {
				enabled = 0
			}
			zone, _ := strconv.Atoi(f.id)
			testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"RecordInfo": map[string]any{
				"Id": 101, "DomainId": zone, "SubDomain": "www-100", "RecordType": "A", "RecordLineId": "90=0",
				"Enabled": enabled, "Value": value, "TTL": 60}}})
			return
		}
	case r.Host == "alidns.aliyuncs.com" && (r.Header.Get("X-ACS-Action") == "DescribeDomainRecordInfo" || r.Header.Get("X-ACS-Action") == "SetDomainRecordStatus"):
		write = r.Header.Get("X-ACS-Action") == "SetDomainRecordStatus"
		query := r.URL.Query()
		if !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) || query.Get("RecordId") != recordID ||
			(write && (len(query) != 2 || query.Get("Status") != "Disable")) ||
			(!write && len(query) != 1) {
			t.Errorf("invalid AliDNS record status request: %s", r.Header.Get("X-ACS-Action"))
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if !write {
			state := "Enable"
			if f.lastDisabled.Load() {
				state = "Disable"
			}
			testsupport.WriteJSON(t, w, map[string]any{"DomainId": f.id, "RecordId": recordID, "RR": "www-100",
				"Type": "A", "Status": state, "Line": "default", "TTL": 60, "Value": value, "Locked": false})
			return
		}
	case r.Host == "dns.cn-north-4.myhuaweicloud.com" &&
		(strings.HasSuffix(r.URL.Path, "/recordsets/"+recordID) || strings.HasSuffix(r.URL.Path, "/statuses/set")):
		write = r.Method == http.MethodPut
		if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			http.Error(w, "invalid identity", http.StatusForbidden)
			return
		}
		if write {
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != `{"status":"DISABLE"}` || r.URL.Path != "/v2.1/recordsets/"+recordID+"/statuses/set" {
				t.Error("invalid Huawei DNS status request")
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
		} else {
			if r.Method != http.MethodGet || r.URL.Path != "/v2/zones/"+f.id+"/recordsets/"+recordID {
				t.Error("invalid Huawei DNS target read")
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			state := "ACTIVE"
			if f.lastDisabled.Load() {
				state = "DISABLE"
			}
			testsupport.WriteJSON(t, w, map[string]any{"id": recordID, "name": "www-100.example.test.", "type": "A",
				"status": state, "ttl": 60, "records": []string{value}, "zone_id": f.id, "zone_name": "example.test.", "project_id": fixtureHuaweiProject, "default": false})
			return
		}
	default:
		f.serveDNS(t, w, r)
		return
	}

	f.writes.Add(1)
	f.lastDisabled.Store(true)
	f.drift.Store(true)
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	_ = conn.Close()
}
