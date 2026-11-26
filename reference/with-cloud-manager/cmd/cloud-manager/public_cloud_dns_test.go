package main

import (
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

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const dnsFixtureSecret = "dns-ownership-token-from-cloud"

type dnsFixture struct {
	vpcCreationFixture
	command, id  string
	mode         atomic.Int32
	infoReads    atomic.Int32
	lastDisabled atomic.Bool
	lastDeleted  atomic.Bool
	created      atomic.Pointer[dnsFixtureCreatedRecord]
}

func TestAcceptancePublicCloudDNS(t *testing.T) {
	for _, f := range []*dnsFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou", parent: "fixture"},
			command: "tencent", id: "6501"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou", parent: "fixture"},
			command: "alibaba", id: "alidns-fixture"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4", parent: "fixture"},
			command: "huawei", id: "97000000000040008000000000000001"},
	} {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudDNS(t, f)
		})
	}
}

func testPublicCloudDNS(t *testing.T, f *dnsFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-public-dns-inventory-operator", "fixture-public-dns-inventory-reviewer"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	config := publicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
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
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveDNS(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	run := func(args ...string) error {
		t.Helper()
		if !validCommand(args) {
			t.Fatalf("invalid real command: %v", args)
		}
		return runPublicCloud(ctx, db, args, f.vendor, path, transport)
	}
	scan := func() error { return run("scan-"+f.command, "tenant", f.vendor, "global", "dns-zone") }
	refresh := func() error { return run("refresh-"+f.command+"-dns-zone", "tenant", f.vendor, f.id) }
	if err := scan(); err != nil {
		t.Fatal(err)
	}
	if err := run("scan-"+f.command, "tenant", f.vendor, f.region, "dns-zone"); err == nil {
		t.Fatal("accepted a regional DNS inventory")
	}
	var observed int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_inventory_objects JOIN cmp_inventory_scopes USING(scope_id)
		WHERE kind='dns-zone' AND region='global' AND removed_at IS NULL`).Scan(&observed); err != nil || observed != 101 {
		t.Fatalf("zone inventory count=%d err=%v", observed, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_schedules(tenant_id,account_id,region,kind,interval_seconds)
		VALUES ('tenant',$1,'global','dns-zone',120)`, f.vendor); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_schedules(tenant_id,account_id,region,kind,interval_seconds)
		VALUES ('tenant',$1,$2,'dns-zone',120)`, f.vendor, f.region); err == nil {
		t.Fatal("registered a regional DNS schedule")
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(target, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		app.ServeHTTP(res, req)
		if res.Code != status || (strings.Contains(target, "/assets") && res.Header().Get("Cache-Control") != "no-store") {
			t.Fatalf("%s returned %d: %s", target, res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), dnsFixtureSecret) {
			t.Fatal("record value disclosed outside the provider")
		}
		return res
	}
	const root = "/v1/tenants/tenant/projects/"
	recordsPath := "/assets/dns-zones/" + f.id + "/records?account=" + f.vendor
	var assets inventory.Assets
	decodeProjectData(t, get(root+"project/assets?kind=dns-zone&limit=2", actor, http.StatusOK), &assets)
	if len(assets.Items) != 2 || assets.Items[0].Kind != provider.DNSZone || assets.Items[0].Region != "global" {
		t.Fatalf("unexpected DNS assets: %+v", assets.Items)
	}
	get(root+"project"+recordsPath, "", http.StatusUnauthorized)
	get(root+"project"+recordsPath, actor, http.StatusNotFound)
	get(root+"project"+recordsPath+"&limit=0", actor, http.StatusBadRequest)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}

	var first, tail inventory.DNSRecordsPage
	decodeProjectData(t, get(root+"project"+recordsPath+"&limit=100", actor, http.StatusOK), &first)
	decodeProjectData(t, get(root+"project"+recordsPath+"&offset=100&limit=100", actor, http.StatusOK), &tail)
	if first.ObservedAt.IsZero() || !first.ObservedAt.Equal(tail.ObservedAt) || first.Total != 101 || len(first.Records) != 100 ||
		first.NextOffset != 100 || tail.NextOffset != 0 || len(tail.Records) != 1 ||
		first.Records[0].Name != "_verification.example.test" || first.Records[0].Type != "TXT" ||
		first.Records[0].ValueCount != 1 || len(first.Records[0].Values) != 0 ||
		len(tail.Records[0].Values) != 1 || tail.Records[0].Values[0] != "192.0.2.42" {
		t.Fatalf("unexpected DNS page: first=%+v tail=%+v", first.Records[:min(3, len(first.Records))], tail)
	}
	get(root+"other"+recordsPath, actor, http.StatusNotFound)
	f.mode.Store(1)
	if err := refresh(); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("second-page throttling returned %v", err)
	}
	f.mode.Store(2)
	if err := refresh(); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("changed page total returned %v", err)
	}
	f.mode.Store(3)
	if err := refresh(); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("duplicate records returned %v", err)
	}
	if f.vendor == provider.AlibabaECS {
		f.mode.Store(4)
		f.infoReads.Store(0)
		if err := refresh(); !provider.IsFailure(err, provider.PreconditionFailed) {
			t.Fatalf("recreated AliDNS name returned %v", err)
		}
	}
	var kept inventory.DNSRecordsPage
	decodeProjectData(t, get(root+"project"+recordsPath, actor, http.StatusOK), &kept)
	if !kept.ObservedAt.Equal(first.ObservedAt) || kept.Total != first.Total {
		t.Fatalf("failed refresh replaced snapshot: %+v", kept)
	}

	f.mode.Store(5)
	if err := scan(); err != nil {
		t.Fatal(err)
	}
	get(root+"project"+recordsPath, actor, http.StatusNotFound)
	f.mode.Store(0)
	if err := scan(); err != nil {
		t.Fatal(err)
	}
	testDNSRefreshOrdering(t, ctx, inventory.NewStore(db), f, refresh)
	if _, err := db.ExecContext(ctx, "UPDATE cmp_cloud_accounts SET credential_revision=credential_revision+1 WHERE account_id=$1", f.vendor); err != nil {
		t.Fatal(err)
	}
	get(root+"project"+recordsPath, actor, http.StatusNotFound)
	get("/readyz", "", http.StatusOK)
}

type dnsTestReaderFunc func(context.Context, provider.Scope, provider.DNSZoneRef) ([]provider.DNSRecord, error)

func (f dnsTestReaderFunc) ReadDNSRecords(ctx context.Context, scope provider.Scope, zone provider.DNSZoneRef) ([]provider.DNSRecord, error) {
	return f(ctx, scope, zone)
}

func testDNSRefreshOrdering(t *testing.T, ctx context.Context, store inventory.Store, f *dnsFixture, refresh func() error) {
	t.Helper()
	key := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: "global", Kind: provider.DNSZone}
	var newErr error
	stale := store.RefreshDNSRecords(ctx, key, f.id, 1, dnsTestReaderFunc(
		func(context.Context, provider.Scope, provider.DNSZoneRef) ([]provider.DNSRecord, error) {
			newErr = refresh()
			return []provider.DNSRecord{}, nil
		}))
	if newErr != nil || !errors.Is(stale, inventory.ErrStale) {
		t.Fatalf("concurrent refresh result: newer=%v older=%v", newErr, stale)
	}
}

func (f *dnsFixture) serveDNS(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var action string
	var offset int
	var valid bool
	send := func(value any) { writeCloudFixtureJSON(t, w, value) }
	switch f.vendor {
	case provider.TencentCVM:
		action = r.Header.Get("X-TC-Action")
		if action == "GetCallerIdentity" && f.tencent(t, w, r) {
			return
		}
		body, err := io.ReadAll(r.Body)
		var input struct {
			Type, Domain, ErrorOnEmpty string
			DomainId                   int
			Offset, Limit              int
		}
		valid = err == nil && json.Unmarshal(body, &input) == nil && r.Host == "dnspod.tencentcloudapi.com" &&
			r.Header.Get("X-TC-Version") == "2021-03-23" && r.Header.Get("X-TC-Region") == "" &&
			r.Header.Get("X-TC-Token") == fixtureLaunchSession && validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) &&
			input.Limit == 100 && ((action == "DescribeDomainList" && input.Type == "MINE") ||
			(action == "DescribeRecordList" && strconv.Itoa(input.DomainId) == f.id && input.Domain == "example.test" && input.ErrorOnEmpty == "no"))
		offset = input.Offset
		send = func(value any) { writeCloudFixtureJSON(t, w, map[string]any{"Response": value}) }
	case provider.AlibabaECS:
		action = r.Header.Get("X-ACS-Action")
		if action == "GetCallerIdentity" && f.alibaba(t, w, r) {
			return
		}
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("PageNumber"))
		offset = (page - 1) * 100
		valid = r.Host == "alidns.aliyuncs.com" && r.Header.Get("X-ACS-Version") == "2015-01-09" &&
			r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession && validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
		if valid && action == "DescribeDomainInfo" && q.Get("DomainName") == "example.test" {
			id := f.id
			if f.mode.Load() == 4 && f.infoReads.Add(1) == 2 {
				id += "-replacement"
			}
			send(map[string]string{"DomainId": id, "DomainName": "example.test"})
			return
		}
		valid = valid && q.Get("PageSize") == "100" && (action == "DescribeDomains" ||
			(action == "DescribeDomainRecords" && q.Get("DomainName") == "example.test"))
	case provider.HuaweiECS:
		if r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
			return
		}
		q := r.URL.Query()
		offset, _ = strconv.Atoi(q.Get("offset"))
		valid = r.Host == "dns.cn-north-4.myhuaweicloud.com" && r.Method == http.MethodGet &&
			r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM && q.Get("limit") == "100"
		switch {
		case r.URL.Path == "/v2/zones" && q.Get("type") == "public":
			action = "zones"
		case r.URL.Path == "/v2/zones/"+f.id+"/recordsets":
			action = "records"
		default:
			valid = false
		}
	}
	if !valid || (offset != 0 && offset != 100) {
		t.Errorf("unexpected DNS request %s %s %s", r.Method, r.Host, action)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch action {
	case "DescribeDomainList", "DescribeDomains", "zones":
		send(f.zonePage(offset))
	case "DescribeRecordList", "DescribeDomainRecords", "records":
		if offset == 100 && f.mode.Load() == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		send(f.recordsPage(offset))
	default:
		t.Errorf("unexpected DNS action %s", action)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *dnsFixture) zonePage(offset int) any {
	total := 101
	if f.mode.Load() == 5 {
		total = 0
	}
	rows := make([]map[string]any, 0, 100)
	for i := offset; i < min(total, offset+100); i++ {
		name := "Example.TEST."
		if i != 0 {
			name = fmt.Sprintf("zone-%03d.example.test.", i)
		}
		id := f.id
		switch f.vendor {
		case provider.TencentCVM:
			n, _ := strconv.Atoi(id)
			rows = append(rows, map[string]any{"DomainId": n + i, "Name": name, "Status": "ENABLE"})
		case provider.AlibabaECS:
			if i != 0 {
				id += strconv.Itoa(i)
			}
			rows = append(rows, map[string]any{"DomainId": id, "DomainName": name})
		case provider.HuaweiECS:
			if i != 0 {
				id = fmt.Sprintf("%032x", i)
			}
			rows = append(rows, map[string]any{"id": id, "name": name, "status": "ACTIVE", "zone_type": "public", "project_id": fixtureHuaweiProject})
		}
	}
	switch f.vendor {
	case provider.TencentCVM:
		return map[string]any{"DomainCountInfo": map[string]int{"DomainTotal": total}, "DomainList": rows}
	case provider.AlibabaECS:
		return map[string]any{"TotalCount": total, "PageSize": 100, "PageNumber": offset/100 + 1, "Domains": map[string]any{"Domain": rows}}
	default:
		return map[string]any{"metadata": map[string]int{"total_count": total}, "zones": rows}
	}
}

type dnsFixtureCreatedRecord struct {
	provider.DNSRecordSpec
	marker string
}

func (f *dnsFixture) recordsPage(offset int) any {
	total := 101
	created := f.created.Load()
	if created != nil {
		total++
	}
	deleted := f.lastDeleted.Load()
	if deleted {
		total--
	}
	if f.mode.Load() == 2 && offset == 100 {
		total = 100
	}
	rows := make([]map[string]any, 0, 100)
	for index := offset; index < min(total, offset+100); index++ {
		i := index
		if deleted && index >= 100 {
			i++
		}
		rr, typ, value := fmt.Sprintf("www-%03d", i), "A", "192.0.2.42"
		if i == 0 {
			rr, typ, value = "_verification", "TXT", dnsFixtureSecret
		}
		id := i + 1
		if i == 100 && f.mode.Load() == 3 {
			id = 1
		}
		status, huaweiStatus, line := "Enable", "ACTIVE", "0"
		if i == 100 {
			line = "90=0"
			if f.lastDisabled.Load() {
				status, huaweiStatus = "Disable", "DISABLE"
			}
		}
		var marker string
		ttl := 60
		if i == 101 {
			rr, typ, value = strings.TrimSuffix(created.Name, ".example.test"), created.Type, created.Value
			marker, ttl = created.marker, created.TTL
		}
		switch f.vendor {
		case provider.TencentCVM:
			rows = append(rows, map[string]any{"RecordId": id, "Name": rr, "Type": typ, "Value": value, "Status": strings.ToUpper(status), "LineId": line, "TTL": ttl, "Remark": marker})
		case provider.AlibabaECS:
			rows = append(rows, map[string]any{"RecordId": strconv.Itoa(id), "RR": rr, "Type": typ, "Value": value, "Status": status, "TTL": ttl, "Line": "default"})
		case provider.HuaweiECS:
			rows = append(rows, map[string]any{"id": fmt.Sprintf("%032x", id), "name": rr + ".example.test.", "type": typ, "records": []string{value},
				"status": huaweiStatus, "ttl": ttl, "description": marker, "zone_id": f.id, "zone_name": "example.test.", "project_id": fixtureHuaweiProject})
		}
	}
	switch f.vendor {
	case provider.TencentCVM:
		return map[string]any{"RecordCountInfo": map[string]int{"TotalCount": total}, "RecordList": rows}
	case provider.AlibabaECS:
		return map[string]any{"TotalCount": total, "PageSize": 100, "PageNumber": offset/100 + 1, "DomainRecords": map[string]any{"Record": rows}}
	default:
		return map[string]any{"metadata": map[string]int{"total_count": total}, "recordsets": rows}
	}
}
