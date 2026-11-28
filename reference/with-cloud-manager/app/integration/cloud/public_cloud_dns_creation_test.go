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
	"with-cloud-manager/app/commands"
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

func TestDNSCreationOperationRequests(t *testing.T) {
	base, err := url.Parse("https://cloud.example.test")
	if err != nil {
		t.Fatal(err)
	}
	submit, err := commands.PlanOperationRequest(base, []string{"create-dns-record", "tenant", "project", "--idempotency-key", "once", "--if-match", "1"})
	if err != nil || submit.Target.String() != "https://cloud.example.test/v1/tenants/tenant/projects/project/dns-zones/records" ||
		submit.Method != http.MethodPost || submit.Status != http.StatusAccepted {
		t.Fatalf("unexpected creation command: %+v, %v", submit, err)
	}
	retry, err := commands.PlanOperationRequest(base, []string{"retry-observation", "tenant", "project", "operation", "--evidence-ref", "test/audit", "--provider-resource-id", "102"})
	if err != nil || retry.Target.String() != "https://cloud.example.test/v1/tenants/tenant/projects/project/operations/operation/retry-observation" ||
		string(retry.Body) != `{"evidence_ref":"test/audit","provider_resource_id":"102"}` {
		t.Fatalf("unexpected retry command: %+v, %v", retry, err)
	}
}

func TestAcceptancePublicCloudDNSCreation(t *testing.T) {
	for _, f := range []*dnsCreationFixture{
		{dnsFixture: dnsFixture{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou", parent: "fixture"},
			command: "tencent", id: "6501"}},
		{dnsFixture: dnsFixture{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou", parent: "fixture"},
			command: "alibaba", id: "alidns-fixture"}},
		{dnsFixture: dnsFixture{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4", parent: "fixture"},
			command: "huawei", id: "97000000000040008000000000000001"}},
	} {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudDNSCreation(t, f)
		})
	}
}

type dnsCreationFixture struct {
	dnsFixture
	writes        atomic.Int32
	returnReceipt atomic.Bool
}

func testPublicCloudDNSCreation(t *testing.T, f *dnsCreationFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor = "fixture-public-dns-creation-operator"
	const reviewer = "fixture-public-dns-creation-reviewer"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	config := cloud.PublicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	profile, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(profilePath, profile, 0600); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveCreation(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	run := func(args ...string) error {
		return cloud.RunPublicCloud(ctx, db, args, f.vendor, profilePath, transport)
	}
	if err := run("scan-"+f.command, "tenant", f.vendor, "global", "dns-zone"); err != nil {
		t.Fatal(err)
	}
	refresh := func() error { return run("refresh-"+f.command+"-dns-zone", "tenant", f.vendor, f.id) }
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
	request := func(method, path, token, key string, body []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("If-Match", `"1"`)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s returned %d want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	var snapshot inventory.DNSRecordsPage
	testsupport.DecodeProjectData(t, request(http.MethodGet, recordsPath, actor, "", nil, http.StatusOK), &snapshot)
	spec := f.spec()
	body := func(spec provider.DNSRecordSpec, observed time.Time) []byte {
		t.Helper()
		data, err := json.Marshal(operations.DNSRecordCreationInput{Account: f.vendor, AssetID: f.id, ObservedAt: observed, DNSRecordSpec: spec})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	submit := func(project, key string, content []byte, want int) operations.Operation {
		t.Helper()
		reply := request(http.MethodPost, root+project+"/dns-zones/records", actor, key, content, want)
		var op operations.Operation
		if want == http.StatusAccepted {
			testsupport.DecodeProjectData(t, reply, &op)
			if op.Action != "create-dns-record" || reply.Header().Get("Location") != root+project+"/operations/"+op.ID {
				t.Fatalf("wrong receipt: %+v", op)
			}
		}
		return op
	}
	base := body(spec, snapshot.ObservedAt)
	submit("project", "stale", body(spec, snapshot.ObservedAt.Add(time.Second)), http.StatusPreconditionFailed)
	submit("project", "conflict", body(provider.DNSRecordSpec{Name: "www-100.example.test", Type: "A", Value: "192.0.2.43", TTL: 60}, snapshot.ObservedAt), http.StatusConflict)
	submit("project", "outside", body(provider.DNSRecordSpec{Name: "another.example.net", Type: spec.Type, Value: spec.Value, TTL: spec.TTL}, snapshot.ObservedAt), http.StatusBadRequest)
	submit("other", "not-owned", base, http.StatusForbidden)

	store := operations.NewStore(db)
	var writeEnabled atomic.Bool
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			profile := config.Config
			profile.AllowDNSRecordCreation = writeEnabled.Load()
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
	disabled := submit("project", "disabled", base, http.StatusAccepted)
	if _, err := store.Approve(ctx, actor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-approval returned %v", err)
	}
	approve(disabled)
	wait(disabled, "failed")
	writeEnabled.Store(true)
	drifted := submit("project", "drifted", base, http.StatusAccepted)
	f.created.Store(&dnsFixtureCreatedRecord{DNSRecordSpec: spec})
	approve(drifted)
	wait(drifted, "failed")
	f.created.Store(nil)
	if f.writes.Load() != 0 {
		t.Fatal("blocked creation dispatched a write")
	}

	op := submit("project", "create", base, http.StatusAccepted)
	if replayed := submit("project", "create", base, http.StatusAccepted); replayed.ID != op.ID {
		t.Fatal("idempotent replay did not return the same operation")
	}
	submit("project", "concurrent", base, http.StatusConflict)
	approve(op)
	if f.vendor == provider.AlibabaECS {
		wait(op, "needs_review")
		pause, stop := context.WithTimeout(ctx, 4*time.Second)
		defer stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			var status string
			if err := db.QueryRowContext(pause, "SELECT status FROM cmp_operation_tasks WHERE id=$1", op.ID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status == "done" {
				break
			}
			select {
			case <-pause.Done():
				t.Fatal("review queue did not settle")
			case <-ticker.C:
			}
		}
		if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "fixture/audit"}); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("manual success bypassed observation: %v", err)
		}
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "fixture/audit"}); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("missing evidence record ID returned %v", err)
		}
		writeEnabled.Store(false)
		retry := []byte(`{"evidence_ref":"fixture/audit","provider_resource_id":"102"}`)
		request(http.MethodPost, root+"project/operations/"+op.ID+"/retry-observation", reviewer, "", retry, http.StatusOK)
		otherEvidence := []byte(`{"evidence_ref":"fixture/audit","provider_resource_id":"101"}`)
		request(http.MethodPost, root+"project/operations/"+op.ID+"/retry-observation", reviewer, "", otherEvidence, http.StatusConflict)
	}
	final := wait(op, "succeeded")
	if final.DNSRecordCreationIntent == nil || final.DNSRecordCreationIntent.RecordID != f.createdRecordID() ||
		final.OutcomeReason != "dns_record_creation_observed" || f.writes.Load() != 1 {
		t.Fatalf("creation was not verified with one dispatched write: %+v, writes=%d", final, f.writes.Load())
	}
	request(http.MethodGet, recordsPath, actor, "", nil, http.StatusNotFound)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	res := request(http.MethodGet, recordsPath, actor, "", nil, http.StatusOK)
	if strings.Contains(res.Body.String(), "plumego-dns:") {
		t.Fatal("internal operation marker appeared in the DNS projection")
	}
	testsupport.DecodeProjectData(t, res, &snapshot)
	if snapshot.Total != 102 {
		t.Fatalf("created record not visible in refreshed snapshot: %d", snapshot.Total)
	}

	if f.vendor == provider.AlibabaECS {
		// 模拟记录在云端删除后再次创建，验证有回执时可直接确认。
		f.created.Store(nil)
		if err := refresh(); err != nil {
			t.Fatal(err)
		}
		testsupport.DecodeProjectData(t, request(http.MethodGet, recordsPath, actor, "", nil, http.StatusOK), &snapshot)
		f.returnReceipt.Store(true)
		writeEnabled.Store(true)
		withReceipt := submit("project", "with-receipt", body(spec, snapshot.ObservedAt), http.StatusAccepted)
		approve(withReceipt)
		confirmed := wait(withReceipt, "succeeded")
		if confirmed.ProviderRequestID != "dns-created-fixture" || confirmed.DNSRecordCreationIntent == nil ||
			confirmed.DNSRecordCreationIntent.RecordID != f.createdRecordID() || f.writes.Load() != 2 {
			t.Fatalf("creation receipt was not retained and verified: %+v", confirmed)
		}
	}

}

func (f *dnsCreationFixture) spec() provider.DNSRecordSpec {
	if f.vendor == provider.HuaweiECS {
		return provider.DNSRecordSpec{Name: "www-new.example.test", Type: "AAAA", Value: "2001:db8::42", TTL: 600}
	}
	return provider.DNSRecordSpec{Name: "www-new.example.test", Type: "A", Value: "192.0.2.43", TTL: 600}
}

func (f *dnsCreationFixture) createdRecordID() string {
	if f.vendor == provider.HuaweiECS {
		return fmt.Sprintf("%032x", 102)
	}
	return "102"
}

func (f *dnsCreationFixture) serveCreation(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	want := f.spec()
	var actual dnsFixtureCreatedRecord
	switch {
	case r.Host == "dnspod.tencentcloudapi.com" && r.Header.Get("X-TC-Action") == "CreateRecord":
		var payload struct {
			DomainId                                                               int
			Domain, SubDomain, RecordType, RecordLine, RecordLineId, Value, Remark string
			TTL                                                                    int
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || json.Unmarshal(body, &payload) != nil ||
			!validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) ||
			strconv.Itoa(payload.DomainId) != f.id || payload.Domain != "example.test" ||
			payload.RecordLine != "默认" || payload.RecordLineId != "0" {
			t.Error("invalid DNSPod create wire contract")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		actual = dnsFixtureCreatedRecord{provider.DNSRecordSpec{Name: payload.SubDomain + "." + payload.Domain, Type: payload.RecordType, Value: payload.Value, TTL: payload.TTL}, payload.Remark}
	case r.Host == "alidns.aliyuncs.com" && r.Header.Get("X-ACS-Action") == "AddDomainRecord":
		query := r.URL.Query()
		ttl, err := strconv.Atoi(query.Get("TTL"))
		if err != nil || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) ||
			query.Get("DomainName") != "example.test" || query.Get("Line") != "default" {
			t.Error("invalid AliDNS create wire contract")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		actual.DNSRecordSpec = provider.DNSRecordSpec{Name: query.Get("RR") + "." + query.Get("DomainName"), Type: query.Get("Type"), Value: query.Get("Value"), TTL: ttl}
	case r.Host == "dns.cn-north-4.myhuaweicloud.com" && r.Method == http.MethodPost && r.URL.Path == "/v2/zones/"+f.id+"/recordsets":
		var payload struct {
			Name, Type, Description string
			TTL                     int
			Records                 []string
		}
		if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || json.NewDecoder(r.Body).Decode(&payload) != nil ||
			len(payload.Records) != 1 || !strings.HasSuffix(payload.Name, ".") {
			t.Error("invalid Huawei DNS create wire contract")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		actual = dnsFixtureCreatedRecord{provider.DNSRecordSpec{Name: strings.TrimSuffix(payload.Name, "."), Type: payload.Type, Value: payload.Records[0], TTL: payload.TTL}, payload.Description}
	default:
		f.serveDNS(t, w, r)
		return
	}
	if actual.DNSRecordSpec != want || (f.vendor != provider.AlibabaECS && !strings.HasPrefix(actual.marker, "plumego-dns:")) {
		t.Error("invalid DNS creation fields")
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	f.writes.Add(1)
	f.created.Store(&actual)
	if f.returnReceipt.Load() {
		testsupport.WriteJSON(t, w, map[string]string{"RequestId": "dns-created-fixture", "RecordId": f.createdRecordID()})
		return
	}
	connection, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	_ = connection.Close()
}
