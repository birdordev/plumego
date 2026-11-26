package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const (
	lbOldDNS     = "edge-v1.example.test"
	lbNewDNS     = "edge-v2.example.test"
	lbOldPrivate = "10.0.0.9"
	lbNewPrivate = "10.0.0.10"
)

func TestAcceptancePublicCloudLoadBalancerInventory(t *testing.T) {
	for _, cloud := range []struct {
		vendor, account, region, first, second, vpc, subnet, product string
		count                                                        int
	}{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "lb-first", "lb-second", "vpc-fixture", "subnet-fixture", "tencent-clb", 2},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alb-first", "alb-second", "vpc-fixture", "vsw-fixture", "alibaba-alb", 4},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4",
			"70000000-0000-4000-8000-000000000001", "70000000-0000-4000-8000-000000000002",
			"70000000-0000-4000-8000-000000000011", "70000000-0000-4000-8000-000000000012", "huawei-elb", 2},
	} {
		t.Run(cloud.vendor, func(t *testing.T) {
			t.Parallel()
			fixture := &loadBalancerFixture{cloud: vpcCreationFixture{vendor: cloud.vendor, account: cloud.account, region: cloud.region},
				first: cloud.first, second: cloud.second, vpc: cloud.vpc, subnet: cloud.subnet}
			testPublicCloudLoadBalancerInventory(t, fixture, cloud.product, cloud.count)
		})
	}
}

func testPublicCloudLoadBalancerInventory(t *testing.T, f *loadBalancerFixture, product string, wantCount int) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	cloud := &f.cloud
	const actor, reviewer = "fixture-balancer-inventory-operator", "fixture-balancer-inventory-reviewer"
	const ref = "fixture/snapshot-delete"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cloud.vendor, region: cloud.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, cloud.remoteIdentity(), actor, reviewer)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant',$1,$2,kind,1,now() FROM unnest(ARRAY['network','subnet']) AS kind`, cloud.vendor, cloud.region)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,CASE WHEN kind='network' THEN $1 ELSE $2 END,kind,'available',1 FROM cmp_inventory_scopes
		WHERE kind IN ('network','subnet')`, f.vpc, f.subnet)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	defer remote.Close()
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	config := publicCloudProfile{Ref: ref, Config: publiccloud.Config{AccountID: cloud.account, Regions: []string{cloud.region}}}
	if cloud.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	basic := []string{"scan", "tenant", cloud.vendor, cloud.region, "load-balancer"}
	scan := func(ctx context.Context) error { return runPublicCloud(ctx, db, basic, cloud.vendor, path, transport) }
	confirm := func() error {
		return runPublicCloud(ctx, db, append(slices.Clone(basic), f.first), cloud.vendor, path, transport)
	}

	if err := scan(ctx); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("expected a late 429, got %v", err)
	}
	var checkpoint string
	if err := db.QueryRowContext(ctx, `SELECT cursor FROM cmp_inventory_scopes WHERE kind='load-balancer'`).Scan(&checkpoint); err != nil ||
		checkpoint == "" || f.firstReads.Load() != 1 {
		t.Fatalf("checkpoint=%q, first reads=%d, err=%v", checkpoint, f.firstReads.Load(), err)
	}
	f.invalid.Store(true)
	if err := scan(ctx); !provider.IsFailure(err, provider.Unauthorized) && !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("invalid continuation accepted: %v", err)
	}
	f.invalid.Store(false)
	if err := scan(ctx); err != nil || f.firstReads.Load() != 1 {
		t.Fatalf("failed to resume same page: first reads=%d err=%v", f.firstReads.Load(), err)
	}

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(project, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant/projects/"+project+"/assets"+path, nil)
		r.Header.Set("Authorization", "Bearer "+actor)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("query %s status=%d: %s", path, w.Code, w.Body)
		}
		return w
	}
	query := "?kind=load-balancer"
	var assets inventory.Assets
	decodeProjectData(t, get("project", query), &assets)
	if len(assets.Items) != wantCount {
		t.Fatalf("got %d balancers, want %d", len(assets.Items), wantCount)
	}
	if cloud.vendor == provider.AlibabaECS && (!slices.ContainsFunc(assets.Items, func(a inventory.Asset) bool {
		return a.ID == "nlb-fixture" && a.LoadBalancer != nil && a.LoadBalancer.Product == "alibaba-nlb"
	}) || !slices.ContainsFunc(assets.Items, func(a inventory.Asset) bool {
		return a.ID == "lb-classic" && a.LoadBalancer != nil && a.LoadBalancer.Product == "alibaba-clb"
	})) {
		t.Fatalf("Alibaba inventory missed a product: %+v", assets.Items)
	}
	filter, changed := lbOldDNS, lbNewDNS
	if cloud.vendor == provider.HuaweiECS {
		filter, changed = lbOldPrivate, lbNewPrivate
	}
	var topology inventory.Topology
	decodeProjectData(t, get("project", "/topology"+query+"&q="+url.QueryEscape(filter)), &topology)
	if len(topology.Nodes) != 1 || topology.Nodes[0].Asset.ID != f.first ||
		topology.Nodes[0].Asset.LoadBalancer == nil || topology.Nodes[0].Asset.LoadBalancer.Product != product ||
		!slices.ContainsFunc(topology.Nodes[0].Links, func(link inventory.TopologyLink) bool {
			return link.Kind == provider.Network && link.ID == f.vpc
		}) {
		t.Fatalf("bad load balancer topology: %+v", topology)
	}
	if cloud.vendor == provider.TencentCVM && !slices.Equal(topology.Nodes[0].Asset.PublicAddresses,
		[]string{"192.0.2.10", "192.0.2.11", "2001:db8::1"}) {
		t.Fatalf("multi-VIP addresses not normalized: %+v", topology.Nodes[0].Asset)
	}
	body, err := json.Marshal(map[string]string{"account": cloud.vendor, "region": cloud.region, "kind": "network", "asset_id": f.vpc})
	if err != nil {
		t.Fatal(err)
	}
	removeVPC := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/network-operations/delete", strings.NewReader(string(body)))
	removeVPC.Header.Set("Authorization", "Bearer "+actor)
	removeVPC.Header.Set("Content-Type", "application/json")
	removeVPC.Header.Set("Idempotency-Key", "blocked-by-load-balancer")
	removeVPC.Header.Set("If-Match", `"1"`)
	blocked := httptest.NewRecorder()
	app.ServeHTTP(blocked, removeVPC)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("VPC deletion ignored a load balancer dependency: %d %s", blocked.Code, blocked.Body)
	}
	records, err := csv.NewReader(get("project", "/export"+query+"&q="+url.QueryEscape(filter)).Body).ReadAll()
	if err != nil || len(records) != 2 || !slices.Contains(records[0], "private_addresses") || !slices.Contains(records[1], product) {
		t.Fatalf("bad load balancer CSV: %v %v", records, err)
	}
	decodeProjectData(t, get("other", query), &assets)
	if len(assets.Items) != 0 {
		t.Fatalf("another project received load balancers: %+v", assets)
	}

	f.changed.Store(true)
	runFixtureScheduledScan(t, db, inventory.Key{Tenant: "tenant", Account: cloud.vendor, Region: cloud.region, Kind: provider.LoadBalancer}, cloud.vendor, ref, scan)
	var history inventory.Changes
	decodeProjectData(t, get("project", "/changes"+query+"&q="+url.QueryEscape(changed)), &history)
	if !slices.ContainsFunc(history.Items, func(change inventory.Change) bool {
		return change.AssetID == f.first && change.Action == "changed" &&
			change.PreviousLoadBalancer != nil && change.LoadBalancer != nil
	}) {
		t.Fatalf("missing frontend-only change: %+v", history.Items)
	}

	f.missing.Store(true)
	if err := scan(ctx); err != nil {
		t.Fatal(err)
	}
	decodeProjectData(t, get("project", query+"&q="+url.QueryEscape(f.first)), &assets)
	if len(assets.Items) != 1 || assets.Items[0].State != "missing_candidate" {
		t.Fatalf("full scan dropped a candidate: %+v", assets.Items)
	}
	if err := confirm(); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("ambiguous 404 was treated as absence: %v", err)
	}
	decodeProjectData(t, get("project", query+"&q="+url.QueryEscape(f.first)), &assets)
	if len(assets.Items) != 1 || assets.Items[0].State != "missing_candidate" {
		t.Fatalf("404 removed a candidate: %+v", assets.Items)
	}
	f.confirm.Store(true)
	if err := confirm(); err != nil {
		t.Fatalf("exact absence check: %v", err)
	}
	decodeProjectData(t, get("project", query), &assets)
	if len(assets.Items) != wantCount-1 || slices.ContainsFunc(assets.Items, func(a inventory.Asset) bool { return a.ID == f.first }) {
		t.Fatalf("exact absence left wrong inventory: %+v", assets.Items)
	}
}

type loadBalancerFixture struct {
	cloud                              vpcCreationFixture
	first, second, vpc, subnet         string
	firstReads, continued              atomic.Int32
	changed, invalid, missing, confirm atomic.Bool
}

func (f *loadBalancerFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
		f.cloud.serve(t, w, r)
		return
	}
	var ok bool
	switch f.cloud.vendor {
	case provider.TencentCVM:
		ok = f.serveTencent(t, w, r)
	case provider.AlibabaECS:
		ok = f.serveAlibaba(t, w, r)
	case provider.HuaweiECS:
		ok = f.serveHuawei(t, w, r)
	}
	if !ok {
		t.Errorf("unexpected load balancer request: %s %s %s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *loadBalancerFixture) selection(w http.ResponseWriter, continuation, point bool) (string, bool) {
	if point {
		if !f.confirm.Load() {
			w.WriteHeader(http.StatusNotFound)
			return "", false
		}
		return "", true
	}
	if continuation {
		if f.continued.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
			return "", false
		}
		return f.second, true
	}
	f.firstReads.Add(1)
	if f.missing.Load() {
		return f.second, true
	}
	return f.first, true
}

func (f *loadBalancerFixture) dns(id string) string {
	if id != f.first {
		return "second.example.test"
	}
	if f.changed.Load() {
		return lbNewDNS
	}
	return lbOldDNS
}

func (f *loadBalancerFixture) serveTencent(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "clb.tencentcloudapi.com" || r.Method != http.MethodPost ||
		r.Header.Get("X-TC-Action") != "DescribeLoadBalancers" || r.Header.Get("X-TC-Region") != f.cloud.region ||
		r.Header.Get("X-TC-Version") != "2018-03-17" || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
		!validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var request struct {
		Offset, Limit   int
		LoadBalancerIds []string
	}
	if json.Unmarshal(body, &request) != nil {
		return false
	}
	point := len(request.LoadBalancerIds) != 0
	if point {
		if !singleTarget(request.LoadBalancerIds, f.first) || request.Offset != 0 || request.Limit != 2 {
			return false
		}
	} else if (request.Offset != 0 && request.Offset != 1) || request.Limit != 50 {
		return false
	}
	id, proceed := f.selection(w, request.Offset == 1, point)
	if !proceed {
		return true
	}
	rows := []map[string]any{}
	total := 0
	if id != "" {
		total = 2
		if f.missing.Load() {
			total = 1
		}
		vpc := f.vpc
		if f.invalid.Load() {
			vpc = "unexpected-vpc-id"
		}
		rows = append(rows, map[string]any{
			"LoadBalancerId": id, "LoadBalancerName": "fixture CLB", "LoadBalancerType": "OPEN",
			"VpcId": vpc, "SubnetId": f.subnet, "LoadBalancerDomain": f.dns(id),
			"Status": 1, "LoadBalancerVips": []string{"192.0.2.11", "192.0.2.10", "192.0.2.11"},
			"AddressIPv6": "2001:db8:0:0::1", "BackupZoneSet": []map[string]any{{"Zone": "ap-guangzhou-2", "ZoneRegion": f.cloud.region}},
		})
	}
	return sendNetworkFixture(t, w, map[string]any{"Response": map[string]any{"TotalCount": total, "LoadBalancerSet": rows}})
}

func (f *loadBalancerFixture) serveAlibaba(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Method != http.MethodPost || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	q := r.URL.Query()
	switch r.Host {
	case "alb." + f.cloud.region + ".aliyuncs.com":
		point := q.Has("LoadBalancerIds.1")
		if r.Header.Get("X-ACS-Action") != "ListLoadBalancers" || r.Header.Get("X-ACS-Version") != "2020-06-16" ||
			q.Has("RegionId") || q.Get("MaxResults") != "50" ||
			(q.Get("NextToken") != "" && q.Get("NextToken") != "alb-resume") || (point && q.Get("LoadBalancerIds.1") != f.first) {
			return false
		}
		id, proceed := f.selection(w, q.Get("NextToken") != "", point)
		if !proceed {
			return true
		}
		rows := []map[string]any{}
		next := ""
		total := 0
		if id != "" {
			total = 2
			if f.missing.Load() {
				total = 1
			}
			if id == f.first && !point {
				next = "alb-resume"
			}
			vpc := f.vpc
			if f.invalid.Load() {
				vpc = "unexpected-vpc-id"
			}
			rows = append(rows, map[string]any{"LoadBalancerId": id, "LoadBalancerName": "fixture ALB", "LoadBalancerStatus": "Active",
				"DnsName": f.dns(id), "VpcId": vpc, "AddressType": "Internet", "Ipv6AddressType": "Intranet",
				"SecurityGroupIds": []string{"sg-fixture"}})
		}
		return sendNetworkFixture(t, w, map[string]any{"LoadBalancers": rows, "TotalCount": total, "NextToken": next})
	case "nlb." + f.cloud.region + ".aliyuncs.com":
		if r.Header.Get("X-ACS-Action") != "ListLoadBalancers" || r.Header.Get("X-ACS-Version") != "2022-04-30" ||
			q.Get("RegionId") != f.cloud.region || q.Get("MaxResults") != "50" || q.Get("NextToken") != "" || q.Has("LoadBalancerIds.1") {
			return false
		}
		row := map[string]any{"RegionId": f.cloud.region, "LoadBalancerId": "nlb-fixture", "LoadBalancerName": "fixture NLB",
			"LoadBalancerStatus": "Active", "VpcId": f.vpc, "AddressType": "Internet", "Ipv6AddressType": "Internet",
			"ZoneMappings": []map[string]any{
				{"ZoneId": "cn-hangzhou-a", "VSwitchId": f.subnet, "LoadBalancerAddresses": []map[string]any{
					{"PrivateIPv4Address": "10.0.2.10", "PublicIPv4Address": "192.0.2.30", "Ipv6Address": "2001:db8::5", "AllocationId": "eip-fixture"},
				}},
			}}
		return sendNetworkFixture(t, w, map[string]any{"LoadBalancers": []map[string]any{row}, "TotalCount": 1})
	case "slb.aliyuncs.com":
		if r.Header.Get("X-ACS-Action") != "DescribeLoadBalancers" || r.Header.Get("X-ACS-Version") != "2014-05-15" ||
			q.Get("RegionId") != f.cloud.region || q.Get("PageNumber") != "1" || q.Get("PageSize") != "50" || q.Has("LoadBalancerId") {
			return false
		}
		row := map[string]any{"RegionId": f.cloud.region, "LoadBalancerId": "lb-classic", "LoadBalancerName": "fixture Classic",
			"LoadBalancerStatus": "active", "VpcId": f.vpc, "VSwitchId": f.subnet, "AddressType": "intranet", "Address": "10.0.1.9"}
		return sendNetworkFixture(t, w, map[string]any{"TotalCount": 1, "PageSize": 50, "PageNumber": 1,
			"LoadBalancers": map[string]any{"LoadBalancer": []map[string]any{row}}})
	default:
		return false
	}
}

func (f *loadBalancerFixture) serveHuawei(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Method != http.MethodGet || r.Host != "elb."+f.cloud.region+".myhuaweicloud.com" ||
		r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || r.URL.Path != "/v3/"+fixtureHuaweiProject+"/elb/loadbalancers" {
		return false
	}
	q := r.URL.Query()
	marker := q.Get("marker")
	point := q.Has("id")
	if point {
		if q.Get("id") != f.first || q.Get("limit") != "2" || marker != "" {
			return false
		}
	} else if (marker != "" && marker != f.first) || q.Get("limit") != "50" {
		return false
	}
	id, proceed := f.selection(w, marker != "", point)
	if !proceed {
		return true
	}
	rows := []map[string]any{}
	next := ""
	if id != "" {
		project := fixtureHuaweiProject
		if f.invalid.Load() {
			project = "00000000000000000000000000000000"
		}
		private := "10.0.1.99"
		if id == f.first {
			private = lbOldPrivate
			if f.changed.Load() {
				private = lbNewPrivate
			}
			next = f.first
		}
		rows = append(rows, map[string]any{"id": id, "name": "fixture ELB", "project_id": project, "provisioning_status": "ACTIVE",
			"vpc_id": f.vpc, "vip_address": private, "elb_virsubnet_ids": []string{f.subnet},
			"publicips": []map[string]string{{"publicip_id": "70000000-0000-4000-8000-000000000021", "publicip_address": "192.0.2.88"}},
			"eips":      []map[string]string{{"eip_id": "70000000-0000-4000-8000-000000000021", "eip_address": "192.0.2.88"}},
		})
	}
	return sendNetworkFixture(t, w, map[string]any{"loadbalancers": rows, "page_info": map[string]any{"current_count": len(rows), "next_marker": next}})
}
