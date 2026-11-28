package cloudtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const (
	lbConfigurationActor          = "balance-configuration-reader-operator"
	lbConfigurationReviewer       = "balance-configuration-reader-reviewer"
	lbConfigurationHuaweiID       = "70000000-0000-4000-8000-000000000001"
	lbConfigurationHuaweiListener = "70000000-0000-4000-8000-000000000011"
	lbConfigurationHuaweiSecond   = "70000000-0000-4000-8000-000000000012"
	lbConfigurationHuaweiPool     = "70000000-0000-4000-8000-000000000021"
)

type loadBalancerConfigurationFixture struct {
	vpcCreationFixture
	command, product, id, listener, group string
	listeners, groups                     int
	classic, paginated                    bool
	failure                               atomic.Int32
	backendFailure                        atomic.Int32
}

func TestAcceptancePublicCloudLoadBalancerConfiguration(t *testing.T) {
	for _, f := range []*loadBalancerConfigurationFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "refresh-tencent-load-balancer", product: "tencent-clb", id: "lb-fixture", listener: "lbl-fixture", group: "lbtg-fixture", listeners: 2, groups: 1},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "refresh-tencent-load-balancer", product: "tencent-clb", id: "lb-classic", listener: "lbl-fixture", group: "lb-classic.default", listeners: 1, groups: 1, classic: true},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			command: "refresh-alibaba-load-balancer", product: "alibaba-alb", id: "alb-fixture", listener: "lsn-fixture", group: "sgp-forward", listeners: 2, groups: 2, paginated: true},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			command: "refresh-alibaba-load-balancer", product: "alibaba-nlb", id: "nlb-fixture", listener: "lsn-fixture@443", group: "sgp-forward", listeners: 1, groups: 1},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			command: "refresh-alibaba-load-balancer", product: "alibaba-clb", id: "lb-classic", listener: "lb-classic.tcp.443", group: "rsp-fixture", listeners: 2, groups: 2, classic: true},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			command: "refresh-huawei-load-balancer", product: "huawei-elb", id: lbConfigurationHuaweiID, listener: lbConfigurationHuaweiListener,
			group: lbConfigurationHuaweiPool, listeners: 2, groups: 1, paginated: true},
	} {
		t.Run(f.product+"/"+f.id, func(t *testing.T) {
			t.Parallel()
			testLoadBalancerConfiguration(t, f)
		})
	}
}

func testLoadBalancerConfiguration(t *testing.T, f *loadBalancerConfigurationFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	seedLoadBalancerConfiguration(t, db, f)
	profile := cloud.PublicCloudProfile{Ref: "fixture/snapshot-delete", Config: publiccloud.Config{
		AccountID: f.account, Regions: []string{f.region},
	}}
	if f.vendor == provider.HuaweiECS {
		profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveConfig(t, w, r) }))
	t.Cleanup(remote.Close)
	target, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{target, remote.Client().Transport}
	args := []string{f.command, "tenant", f.vendor, f.region, f.id}
	if !cloudmanager.ValidCommand(args) {
		t.Fatalf("rejected real command: %v", args)
	}
	refresh := func() error { return cloud.RunPublicCloud(ctx, db, args, f.vendor, path, transport) }
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(project, extra, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		endpoint := "/v1/tenants/tenant/projects/" + project + "/assets/load-balancers/" + f.id + "/configuration?account=" + f.vendor + "&region=" + f.region + extra
		req := httptest.NewRequest(http.MethodGet, endpoint, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		app.ServeHTTP(res, req)
		if res.Code != status || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s returned %d: %s", endpoint, res.Code, res.Body.String())
		}
		return res
	}
	get("project", "", "", http.StatusUnauthorized)
	get("project", "", lbConfigurationActor, http.StatusNotFound)
	get("project", "&region="+f.region, lbConfigurationActor, http.StatusBadRequest)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	var saved inventory.LoadBalancerConfiguration
	response := get("project", "", lbConfigurationActor, http.StatusOK)
	if strings.Contains(response.Body.String(), "other-project") {
		t.Fatal("shared target group disclosed its other parent")
	}
	testsupport.DecodeProjectData(t, response, &saved)
	if saved.Backends != nil || saved.ObservedAt.IsZero() || len(saved.Listeners) != f.listeners || len(saved.TargetGroups) != f.groups ||
		!slices.ContainsFunc(saved.Listeners, func(value provider.LoadBalancerListener) bool {
			return value.ID == f.listener && value.Port == 443 &&
				(f.group == "" || slices.Contains(value.TargetGroupIDs, f.group))
		}) {
		t.Fatalf("unexpected configuration: %+v", saved)
	}
	for _, group := range saved.TargetGroups {
		if group.Exclusive != (f.vendor == provider.HuaweiECS && group.ID == f.group) {
			t.Fatalf("exclusive projection for %q did not reflect provider associations: %+v", f.product, group)
		}
	}
	get("other", "", lbConfigurationActor, http.StatusNotFound)
	if f.paginated {
		for _, mode := range []struct {
			value int32
			kind  provider.FailureKind
		}{{1, provider.Throttled}, {2, provider.InvalidResponse}} {
			f.failure.Store(mode.value)
			if err := refresh(); !provider.IsFailure(err, mode.kind) {
				t.Fatalf("refresh accepted failed second page: %v", err)
			}
			var kept inventory.LoadBalancerConfiguration
			testsupport.DecodeProjectData(t, get("project", "", lbConfigurationActor, http.StatusOK), &kept)
			if !kept.ObservedAt.Equal(saved.ObservedAt) || len(kept.Listeners) != f.listeners {
				t.Fatalf("saved snapshot was partially replaced: %+v", kept)
			}
		}
		f.failure.Store(0)
	}
	backendArgs := append(slices.Clone(args), "--include-backends")
	if !cloudmanager.ValidCommand(backendArgs) || cloudmanager.ValidCommand(append(slices.Clone(args), "--unknown")) {
		t.Fatal("invalid backend refresh command contract")
	}
	refreshBackends := func() error { return cloud.RunPublicCloud(ctx, db, backendArgs, f.vendor, path, transport) }
	if err := refreshBackends(); err != nil {
		t.Fatal(err)
	}
	response = get("project", "", lbConfigurationActor, http.StatusOK)
	for _, value := range []string{fixtureBackendIP, fixtureBackendServer, "other-project", fixtureForeignBackendListener, fixtureUnmonitoredServer} {
		if strings.Contains(response.Body.String(), value) {
			t.Fatalf("backend response leaked unprojected fields: %s", response.Body)
		}
	}
	var enriched inventory.LoadBalancerConfiguration
	testsupport.DecodeProjectData(t, response, &enriched)
	expectedMembers, expectedChecks := f.groups, 1
	if f.vendor == provider.HuaweiECS || f.product == "alibaba-alb" {
		expectedMembers, expectedChecks = 2, 2
	}
	if f.classic {
		expectedMembers++
		if f.product == "alibaba-clb" {
			expectedMembers++
		}
	}
	if f.vendor == provider.TencentCVM && !f.classic {
		expectedMembers += 2
	}
	if enriched.Backends == nil || len(enriched.Backends.Members) != expectedMembers || len(enriched.Backends.Checks) != expectedChecks {
		t.Fatalf("unexpected backend data: %+v", enriched.Backends)
	}
	switch f.product {
	case "alibaba-nlb":
		expectBackendState(t, enriched.Backends, f.listener, 445, "", "unhealthy")
	case "alibaba-alb":
		expectBackendState(t, enriched.Backends, f.listener, 443, "rule-fixture", "initial")
	case "alibaba-clb":
		validateAlibabaClassicGroups(t, &enriched, f.id)
	case "tencent-clb":
		if !f.classic {
			validateTencentDirectGroups(t, &enriched)
		}
	}
	if f.group != "" && !slices.ContainsFunc(enriched.Backends.Members, func(member provider.LoadBalancerBackendMember) bool {
		return member.GroupID == f.group && member.Weight != nil && *member.Weight == 0 &&
			slices.ContainsFunc(enriched.Backends.Checks, func(check provider.LoadBalancerBackendCheck) bool {
				return check.Key == member.Key && check.Status == "unhealthy" && check.ListenerID == f.listener
			})
	}) {
		t.Fatalf("backend member didn't match its reported check: %+v", enriched.Backends)
	}
	get("other", "", lbConfigurationActor, http.StatusNotFound)
	modes := []int32{1, 2}
	if f.product == "alibaba-nlb" {
		modes = append(modes, 3)
	}
	if f.product == "alibaba-clb" {
		modes = append(modes, 3, 4)
	}
	if f.vendor == provider.TencentCVM && !f.classic {
		modes = append(modes, 3)
	}
	for _, mode := range modes {
		f.backendFailure.Store(mode)
		err := refreshBackends()
		if (mode == 1 && !provider.IsFailure(err, provider.Throttled)) ||
			(mode != 1 && !provider.IsFailure(err, provider.InvalidResponse) && !provider.IsFailure(err, provider.Unauthorized)) {
			t.Fatalf("backend observation accepted malformed or incomplete data: %v", err)
		}
		var kept inventory.LoadBalancerConfiguration
		testsupport.DecodeProjectData(t, get("project", "", lbConfigurationActor, http.StatusOK), &kept)
		if !kept.ObservedAt.Equal(enriched.ObservedAt) || kept.Backends == nil || len(kept.Backends.Checks) != expectedChecks ||
			!slices.Equal(kept.Backends.Checks, enriched.Backends.Checks) {
			t.Fatalf("incomplete backend data overwrote existing snapshot: %+v", kept)
		}
	}
	f.backendFailure.Store(0)
	if _, err := db.ExecContext(ctx, "UPDATE cmp_inventory_objects SET missing_generation=1 WHERE external_id=$1", f.id); err != nil {
		t.Fatal(err)
	}
	get("project", "", lbConfigurationActor, http.StatusNotFound)
	if err := refresh(); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("accepted missing parent: %v", err)
	}
}

func seedLoadBalancerConfiguration(t *testing.T, db *sql.DB, fixture *loadBalancerConfigurationFixture) {
	t.Helper()
	ctx := t.Context()
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{
		vendor: fixture.vendor, region: fixture.region, volume: "source-volume", id: "snapshot", class: "standard",
	}, fixture.remoteIdentity(), lbConfigurationActor, lbConfigurationReviewer)
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'load-balancer',1,now())`, fixture.vendor, fixture.region); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,load_balancer)
		SELECT scope_id,$1,'balancer','active',1,jsonb_build_object('product',$2::text,'address_type','private')
		FROM cmp_inventory_scopes WHERE kind='load-balancer'`, fixture.id, fixture.product); err != nil {
		t.Fatal(err)
	}
}

func (f *loadBalancerConfigurationFixture) serveConfig(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch r.Host {
	case "sts.tencentcloudapi.com", "sts.aliyuncs.com", "iam.myhuaweicloud.com":
		f.serve(t, w, r)
		return
	}
	var ok bool
	switch f.vendor {
	case provider.TencentCVM:
		ok = f.tencentConfiguration(t, w, r)
	case provider.AlibabaECS:
		ok = f.alibabaConfiguration(t, w, r)
	case provider.HuaweiECS:
		ok = f.huaweiConfiguration(t, w, r)
	}
	if !ok {
		t.Errorf("unexpected configuration request: %s %s %s %s", r.Method, r.Host, r.Header.Get("X-TC-Action"), r.Header.Get("X-ACS-Action"))
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *loadBalancerConfigurationFixture) tencentConfiguration(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Method != http.MethodPost || r.Host != "clb.tencentcloudapi.com" ||
		r.Header.Get("X-TC-Version") != "2018-03-17" || r.Header.Get("X-TC-Region") != f.region ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var request struct {
		LoadBalancerId                  string
		LoadBalancerIds, TargetGroupIds []string
	}
	if json.Unmarshal(body, &request) != nil {
		return false
	}
	write := func(response any) bool { return sendNetworkFixture(t, w, map[string]any{"Response": response}) }
	switch r.Header.Get("X-TC-Action") {
	case "DescribeLoadBalancers":
		if !singleTarget(request.LoadBalancerIds, f.id) {
			return false
		}
		forward := 1
		if f.classic {
			forward = 0
		}
		return write(map[string]any{"TotalCount": 1, "LoadBalancerSet": []map[string]any{{"LoadBalancerId": f.id, "Forward": forward}}})
	case "DescribeListeners":
		if request.LoadBalancerId != f.id || f.classic {
			return false
		}
		return write(map[string]any{"TotalCount": 2, "Listeners": []map[string]any{
			{"ListenerId": f.listener, "ListenerName": "edge", "Protocol": "HTTPS", "Port": 443,
				"Rules": []map[string]any{
					{"LocationId": "loc-fixture", "TargetGroupList": []map[string]string{{"TargetGroupId": f.group}}},
					{"LocationId": "loc-direct"},
				}},
			{"ListenerId": "lbl-direct", "Protocol": "TCP", "Port": 8443},
		}})
	case "DescribeClassicalLBListeners":
		if request.LoadBalancerId != f.id || !f.classic {
			return false
		}
		return write(map[string]any{"Listeners": []map[string]any{{"ListenerId": f.listener, "Protocol": "tcp", "ListenerPort": 443}}})
	case "DescribeTargetGroups":
		if f.classic || !singleTarget(request.TargetGroupIds, f.group) {
			return false
		}
		return write(map[string]any{"TotalCount": 1, "TargetGroupSet": []map[string]any{{
			"TargetGroupId": f.group, "TargetGroupName": "primary", "Protocol": "HTTP",
			"AssociatedRule": []map[string]string{{"LoadBalancerId": f.id}, {"LoadBalancerId": "lb-other-project"}},
		}}})
	default:
		return f.tencentBackends(t, w, r, body)
	}
}

func (f *loadBalancerConfigurationFixture) alibabaConfiguration(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	action, version, q := r.Header.Get("X-ACS-Action"), r.Header.Get("X-ACS-Version"), r.URL.Query()
	if f.classic {
		if r.Host != "slb.aliyuncs.com" || version != "2014-05-15" || q.Get("RegionId") != f.region {
			return false
		}
		if action == "DescribeVServerGroupAttribute" || action == "DescribeHealthStatus" ||
			action == "DescribeMasterSlaveServerGroups" || action == "DescribeMasterSlaveServerGroupAttribute" {
			return f.alibabaClassicBackends(t, w, r)
		}
		if q.Get("LoadBalancerId") != f.id {
			return false
		}
		return f.alibabaClassicConfiguration(t, w, action)
	}
	nlb := f.product == "alibaba-nlb"
	if nlb {
		if action == "ListServerGroups" || action == "ListServerGroupServers" {
			if len(q) != 0 || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
				return false
			}
			q = r.PostForm
		}
		if r.Host != "nlb."+f.region+".aliyuncs.com" || version != "2022-04-30" || q.Get("RegionId") != f.region {
			return false
		}
	} else if r.Host != "alb."+f.region+".aliyuncs.com" || version != "2020-06-16" || q.Has("RegionId") {
		return false
	}
	if action == "ListServerGroupServers" || action == "GetListenerHealthStatus" {
		return f.alibabaBackends(t, w, action, q)
	}
	if q.Get("MaxResults") != "50" {
		return false
	}
	switch action {
	case "ListLoadBalancers":
		if q.Get("LoadBalancerIds.1") != f.id {
			return false
		}
		return sendNetworkFixture(t, w, map[string]any{"TotalCount": 1, "LoadBalancers": []map[string]any{{
			"LoadBalancerId": f.id, "RegionId": f.region, "VpcId": "vpc-fixture", "AddressType": "Intranet", "LoadBalancerStatus": "Active",
		}}})
	case "ListListeners":
		if q.Get("LoadBalancerIds.1") != f.id {
			return false
		}
		parent, listener, token := f.id, f.listener, ""
		if !nlb && q.Get("NextToken") == "" {
			token = "second"
		}
		if q.Get("NextToken") == "second" && !nlb {
			listener = "lsn-second"
			proceed := f.secondPage(w)
			if !proceed {
				return true
			}
			if f.failure.Load() == 2 {
				parent = "alb-other"
			}
		} else if q.Has("NextToken") {
			return false
		}
		row := map[string]any{"LoadBalancerId": parent, "ListenerId": listener, "ListenerPort": 443, "ListenerProtocol": "HTTPS"}
		if nlb {
			row["ListenerPort"], row["StartPort"], row["EndPort"], row["ListenerProtocol"], row["ServerGroupId"] = 0, "443", "450", "TCP", f.group
		} else {
			row["DefaultActions"] = []map[string]any{{"ForwardGroupConfig": map[string]any{
				"ServerGroupTuples": []map[string]any{{"ServerGroupId": f.group}},
			}}}
		}
		return sendNetworkFixture(t, w, map[string]any{"NextToken": token, "Listeners": []map[string]any{row}})
	case "ListRules":
		if nlb || q.Get("LoadBalancerIds.1") != f.id {
			return false
		}
		return sendNetworkFixture(t, w, map[string]any{"Rules": []map[string]any{{
			"RuleId": "rule-fixture", "LoadBalancerId": f.id, "ListenerId": f.listener, "RuleActions": []map[string]any{{
				"TrafficMirrorConfig": map[string]any{"MirrorGroupConfig": map[string]any{
					"ServerGroupTuples": []map[string]any{{"ServerGroupId": "sgp-mirror"}},
				}},
			}},
		}}})
	case "ListServerGroups":
		if q.Get("ServerGroupIds.1") != f.group || (!nlb && q.Get("ServerGroupIds.2") != "sgp-mirror") {
			return false
		}
		group := func(id string) map[string]any {
			return map[string]any{"ServerGroupId": id, "ServerGroupName": "backend", "Protocol": "HTTPS", "Scheduler": "wrr",
				"RelatedLoadBalancerIds": []string{f.id, "alb-other-project"}}
		}
		groups := []map[string]any{group(f.group)}
		if !nlb {
			groups = append(groups, group("sgp-mirror"))
		}
		return sendNetworkFixture(t, w, map[string]any{"ServerGroups": groups})
	}
	return false
}

func (f *loadBalancerConfigurationFixture) alibabaClassicConfiguration(t *testing.T, w http.ResponseWriter, action string) bool {
	switch action {
	case "DescribeLoadBalancers":
		row := map[string]any{"RegionId": f.region, "LoadBalancerId": f.id, "LoadBalancerStatus": "active",
			"VpcId": "vpc-fixture", "VSwitchId": "vsw-fixture", "AddressType": "intranet", "Address": "10.0.1.9"}
		return sendNetworkFixture(t, w, map[string]any{"TotalCount": 1, "PageSize": 50, "PageNumber": 1,
			"LoadBalancers": map[string]any{"LoadBalancer": []map[string]any{row}}})
	case "DescribeLoadBalancerAttribute":
		return sendNetworkFixture(t, w, map[string]any{"RegionId": f.region, "LoadBalancerId": f.id,
			"ListenerPortsAndProtocal": map[string]any{"ListenerPortAndProtocal": []map[string]any{
				{"ListenerPort": 443, "ListenerProtocal": "tcp"}, {"ListenerPort": 8443, "ListenerProtocal": "tcp"},
			}},
			"BackendServers": map[string]any{"BackendServer": []map[string]any{{"ServerId": fixtureUnmonitoredServer, "Weight": 37}}},
		})
	case "DescribeVServerGroups":
		return sendNetworkFixture(t, w, map[string]any{"VServerGroups": map[string]any{"VServerGroup": []map[string]any{{
			"VServerGroupId": f.group, "VServerGroupName": "classic backend", "AssociatedObjects": map[string]any{
				"Listeners": map[string]any{"Listener": []map[string]any{{"Port": 443, "Protocol": "tcp"}}},
			},
		}}}})
	}
	return false
}

func (f *loadBalancerConfigurationFixture) huaweiConfiguration(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	prefix := "/v3/" + fixtureHuaweiProject + "/elb/"
	if r.Method != http.MethodGet || r.Host != "elb."+f.region+".myhuaweicloud.com" ||
		r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	q := r.URL.Query()
	leaf := strings.TrimPrefix(r.URL.Path, prefix)
	if leaf == "pools/"+f.group+"/members" {
		return f.huaweiBackends(t, w, r)
	}
	page := func(key string, row map[string]any, next string) bool {
		row["project_id"] = fixtureHuaweiProject
		return sendNetworkFixture(t, w, map[string]any{key: []map[string]any{row},
			"page_info": map[string]any{"current_count": 1, "next_marker": next}})
	}
	if leaf == "loadbalancers" {
		if q.Get("id") != f.id || q.Get("limit") != "2" {
			return false
		}
		return page(leaf, map[string]any{"id": f.id, "name": "elb edge", "vip_address": "10.0.0.9", "provisioning_status": "ACTIVE"}, "")
	}
	if q.Get("loadbalancer_id") != f.id || q.Get("limit") != "50" {
		return false
	}
	if leaf == "listeners" {
		id, marker, parent := f.listener, f.listener, f.id
		if q.Get("marker") != "" {
			if q.Get("marker") != f.listener {
				return false
			}
			if !f.secondPage(w) {
				return true
			}
			id, marker = lbConfigurationHuaweiSecond, ""
			if f.failure.Load() == 2 {
				parent = "70000000-0000-4000-8000-000000000002"
			}
		}
		return page(leaf, map[string]any{"id": id, "loadbalancers": []map[string]string{{"id": parent}},
			"protocol_port": 443, "protocol": "TCP", "default_pool_id": f.group}, marker)
	}
	if leaf == "pools" && !q.Has("marker") {
		return page(leaf, map[string]any{"id": f.group, "name": "pool", "protocol": "TCP", "lb_algorithm": "ROUND_ROBIN",
			"loadbalancers": []map[string]string{{"id": f.id}}, "listeners": []map[string]string{{"id": f.listener}}}, "")
	}
	return false
}

func (f *loadBalancerConfigurationFixture) secondPage(w http.ResponseWriter) bool {
	if f.failure.Load() != 1 {
		return true
	}
	w.Header().Set("Retry-After", "2")
	w.WriteHeader(http.StatusTooManyRequests)
	return false
}

type loadBalancerConfigurationReaderFunc func(context.Context, provider.Scope, provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error)

func (read loadBalancerConfigurationReaderFunc) ReadLoadBalancerConfiguration(ctx context.Context, scope provider.Scope, request provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
	return read(ctx, scope, request)
}

func TestLoadBalancerConfigurationRefreshFencesOlderReads(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	fixture := &loadBalancerConfigurationFixture{vpcCreationFixture: vpcCreationFixture{
		vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou",
	}, product: "tencent-clb", id: "lb-fence"}
	seedLoadBalancerConfiguration(t, db, fixture)
	store := inventory.NewStore(db)
	key := inventory.Key{Tenant: "tenant", Account: fixture.vendor, Region: fixture.region, Kind: provider.LoadBalancer}
	q := inventory.LoadBalancerConfigurationQuery{Tenant: key.Tenant, Project: "project", Account: key.Account, Region: key.Region, ID: fixture.id}
	reader := func(port int) loadBalancerConfigurationReaderFunc {
		return func(_ context.Context, _ provider.Scope, _ provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
			return provider.LoadBalancerConfiguration{Listeners: []provider.LoadBalancerListener{{ID: "lbl-fixture", Protocol: "TCP", Port: port}}}, nil
		}
	}
	withDelayed := func(change func()) {
		t.Helper()
		entered, release, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		defer close(release)
		go func() {
			result <- store.RefreshLoadBalancerConfiguration(ctx, key, provider.LoadBalancerConfigurationRequest{ID: fixture.id}, 1,
				loadBalancerConfigurationReaderFunc(func(ctx context.Context, scope provider.Scope, request provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
					close(entered)
					<-release
					return reader(80)(ctx, scope, request)
				}))
		}()
		select {
		case <-entered:
		case err := <-result:
			t.Fatalf("delayed adapter did not start: %v", err)
		}
		change()
		release <- struct{}{}
		if err := <-result; !errors.Is(err, inventory.ErrStale) {
			t.Fatalf("late snapshot replaced a current snapshot: %v", err)
		}
	}
	withDelayed(func() {
		if err := store.RefreshLoadBalancerConfiguration(ctx, key, provider.LoadBalancerConfigurationRequest{ID: fixture.id}, 1, reader(443)); err != nil {
			t.Fatal(err)
		}
	})
	current, err := store.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q)
	if err != nil || len(current.Listeners) != 1 || current.Listeners[0].Port != 443 || current.TargetGroups == nil {
		t.Fatalf("bad current snapshot %+v %v", current, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_asset_ownership(scope_id, external_id, tenant_id, project_id, revision)
		SELECT scope_id,$1,tenant_id,'other',2 FROM cmp_inventory_scopes WHERE kind='load-balancer'`, fixture.id); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q); !errors.Is(err, inventory.ErrLoadBalancerConfigurationNotFound) {
		t.Fatalf("former project retained configuration access: %v", err)
	}
	q.Project = "other"
	if _, err := store.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q); err != nil {
		t.Fatalf("new project did not inherit configuration access: %v", err)
	}
	withDelayed(func() {
		if _, err := db.ExecContext(ctx, "UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE account_id=$1", key.Account); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := store.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q); !errors.Is(err, inventory.ErrLoadBalancerConfigurationNotFound) {
		t.Fatalf("rotation exposed a prior credential revision: %v", err)
	}
}
