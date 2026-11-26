package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const weightEndpointMember = "10.20.0.7"
const weightEndpointSibling = "10.20.0.8"
const weightEndpointPort = 8080
const backendFixtureVPC = "71000000-0000-4000-8000-000000000001"
const backendFixtureSubnet = "71000000-0000-4000-8000-000000000002"

type backendWeightFixture struct {
	loadBalancerRenameFixture
	member, other                                                             string
	weight                                                                    atomic.Int32
	adding, removing, removed, memberListUnavailable, changedMemberAttributes atomic.Bool
}

func TestAcceptancePublicCloudLoadBalancerBackendWeight(t *testing.T) {
	publicCloudBackendFixtures(t, testLoadBalancerBackendWeight)
}

func publicCloudBackendFixtures(t *testing.T, run func(*testing.T, *backendWeightFixture)) {
	t.Helper()
	for _, test := range []struct{ vendor, account, region, product, asset, group, member, other string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "tencent-clb", "lb-fixture", "lbtg-fixture", "ins-weight", "ins-sibling"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-alb", "alb-fixture", "sgp-fixture", "i-weight", "i-sibling"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-nlb", "nlb-fixture", "sgp-fixture", "i-weight", "i-sibling"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "huawei-elb", lbConfigurationHuaweiID, lbConfigurationHuaweiPool, "70000000-0000-4000-8000-000000000031", "70000000-0000-4000-8000-000000000032"},
	} {
		t.Run(test.product, func(t *testing.T) {
			t.Parallel()
			f := &backendWeightFixture{loadBalancerRenameFixture: loadBalancerRenameFixture{loadBalancerConfigurationFixture: loadBalancerConfigurationFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: test.vendor, account: test.account, region: test.region}, product: test.product, id: test.asset, group: test.group,
			}}, member: test.member, other: test.other}
			run(t, f)
		})
	}
}

func (f *backendWeightFixture) memberKey(secret []byte, id, address string) string {
	if f.vendor == provider.HuaweiECS {
		address = ""
	}
	value := fixtureHMAC(secret, id+"\x00"+address+"\x00"+strconv.Itoa(weightEndpointPort))
	return hex.EncodeToString(value[:16])
}

func testLoadBalancerBackendWeight(t *testing.T, f *backendWeightFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	seedLoadBalancerConfiguration(t, db, &f.loadBalancerConfigurationFixture)
	f.name.Store("group")
	f.settled.Store(true)
	f.weight.Store(20)
	localInventory := inventory.NewStore(db)
	k := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.LoadBalancer}
	q := inventory.LoadBalancerConfigurationQuery{Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id}
	read := func(_ context.Context, _ provider.Scope, r provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
		return provider.LoadBalancerConfiguration{
			TargetGroups: []provider.LoadBalancerTargetGroup{{ID: f.group, Name: "group", Exclusive: true}},
			Backends: &provider.LoadBalancerBackendSnapshot{Members: []provider.LoadBalancerBackendMember{
				{Key: f.memberKey(r.BackendSecret, f.member, weightEndpointMember), GroupID: f.group, Port: weightEndpointPort, Weight: new(20), WeightEditable: true},
				{Key: f.memberKey(r.BackendSecret, f.other, weightEndpointSibling), GroupID: f.group, Port: weightEndpointPort, Weight: new(100), WeightEditable: true},
			}},
		}, nil
	}
	request := provider.LoadBalancerConfigurationRequest{ID: f.id, IncludeBackends: true}
	readWeight := func(key string) (int, error) {
		cfg, err := localInventory.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q)
		if err != nil {
			return -1, err
		}
		for _, member := range cfg.Backends.Members {
			if member.Key == key && member.Weight != nil {
				return *member.Weight, nil
			}
		}
		return -1, errors.New("backend member was not projected")
	}
	var cfg inventory.LoadBalancerConfiguration
	for refresh := range 2 {
		if err := localInventory.RefreshLoadBalancerConfiguration(ctx, k, request, 1, loadBalancerConfigurationReaderFunc(read)); err != nil {
			t.Fatal(err)
		}
		latest, err := localInventory.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q)
		if err != nil {
			t.Fatal(err)
		}
		if refresh > 0 && !slices.EqualFunc(cfg.Backends.Members, latest.Backends.Members, func(a, b provider.LoadBalancerBackendMember) bool { return a.Key == b.Key }) {
			t.Fatal("member identifiers changed across refreshes")
		}
		cfg = latest
	}

	n := slices.IndexFunc(cfg.Backends.Members, func(m provider.LoadBalancerBackendMember) bool { return m.Weight != nil && *m.Weight == 20 })
	if n < 0 {
		t.Fatal("missing member")
	}
	memberKey := cfg.Backends.Members[n].Key
	otherKey := cfg.Backends.Members[1-n].Key

	const otherParent = "alternate-parent"
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,load_balancer)
  SELECT scope_id,$2,name,status,seen_generation,load_balancer FROM cmp_inventory_objects WHERE external_id=$1`, f.id, otherParent); err != nil {
		t.Fatal(err)
	}
	alternateRequest := request
	alternateRequest.ID = otherParent
	if err := localInventory.RefreshLoadBalancerConfiguration(ctx, k, alternateRequest, 1, loadBalancerConfigurationReaderFunc(read)); err != nil {
		t.Fatal(err)
	}
	alternateQuery := q
	alternateQuery.ID = otherParent
	alternate, err := localInventory.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, alternateQuery)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(alternate.Backends.Members, func(m provider.LoadBalancerBackendMember) bool { return m.Key == memberKey || m.Key == otherKey }) {
		t.Fatal("member identifier leaked across parent assets")
	}

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveWeight(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	profile := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}
	if f.vendor == provider.HuaweiECS {
		profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	var enabled atomic.Bool
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			config := profile
			config.AllowLoadBalancerBackendWeight = enabled.Load()
			client, err := publiccloud.Connect(ctx, binding.Provider, config, transport)
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
			t.Error("backend weight worker did not stop")
		}
	}()
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	input := func(current, desired int) []byte {
		t.Helper()
		body, err := json.Marshal(operations.LoadBalancerBackendWeightInput{Account: f.vendor, Region: f.region, AssetID: f.id,
			TargetGroupID: f.group, MemberKey: memberKey, CurrentWeight: &current, Weight: &desired})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	submit := func(project, key string, body []byte, status int) operations.Operation {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/"+project+"/load-balancers/backend-weight", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+lbConfigurationActor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-Match", `"1"`)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s returned %d, want %d: %s", key, w.Code, status, w.Body.String())
		}
		var op operations.Operation
		if status == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.Action != "set-load-balancer-backend-weight" || w.Header().Get("Location") != "/v1/tenants/tenant/projects/"+project+"/operations/"+op.ID {
				t.Fatalf("invalid receipt: %+v", op)
			}
		}
		if strings.Contains(w.Body.String(), f.member) || strings.Contains(w.Body.String(), weightEndpointMember) {
			t.Fatal("provider backend identity leaked")
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, lbConfigurationReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, lbConfigurationActor, op.ID, status, true)
	}
	body := input(20, 0)
	submit("other", "wrong-project", body, http.StatusForbidden)
	submit("project", "stale-weight", input(19, 0), http.StatusPreconditionFailed)
	submit("project", "key-from-other-parent", bytes.Replace(body, []byte(f.id), []byte(otherParent), 1), http.StatusPreconditionFailed)
	submit("project", "invalid-weight", input(20, 101), http.StatusBadRequest)
	submit("project", "other-key", bytes.Replace(body, []byte(memberKey), []byte(strings.Repeat("a", 32)), 1), http.StatusPreconditionFailed)
	disabled := submit("project", "disabled", body, http.StatusAccepted)
	if disabled.Status != "awaiting_approval" {
		t.Fatal("operation skipped approval")
	}
	if _, err := store.Approve(ctx, lbConfigurationActor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self approval accepted: %v", err)
	}
	approve(disabled)
	wait(disabled, "failed")
	enabled.Store(true)
	drift := submit("project", "changed-weight", body, http.StatusAccepted)
	f.weight.Store(21)
	approve(drift)
	wait(drift, "failed")
	f.weight.Store(20)
	shared := submit("project", "changed-association", body, http.StatusAccepted)
	f.wrongTarget.Store(true)
	approve(shared)
	wait(shared, "failed")
	f.wrongTarget.Store(false)
	if f.writes.Load() != 0 {
		t.Fatalf("precondition leaked %d cloud writes", f.writes.Load())
	}

	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"set-load-balancer-backend-weight", "tenant", "project", "--idempotency-key", "update-weight", "--if-match", "1"},
		local.URL, lbConfigurationActor, bytes.NewReader(body), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var first operations.Operation
	if err := json.Unmarshal(output.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Status != "awaiting_approval" || submit("project", "update-weight", body, http.StatusAccepted).ID != first.ID {
		t.Fatal("replay diverged from CLI receipt")
	}
	submit("project", "update-weight", input(20, 30), http.StatusConflict)
	started, release := make(chan struct{}), make(chan struct{})
	refreshResult := make(chan error, 1)
	staleCtx, cancelStale := context.WithCancel(ctx)
	defer cancelStale()
	go func() {
		refreshResult <- localInventory.RefreshLoadBalancerConfiguration(staleCtx, k, request, 1,
			loadBalancerConfigurationReaderFunc(func(ctx context.Context, sc provider.Scope, r provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
				close(started)
				select {
				case <-release:
					return read(ctx, sc, r)
				case <-ctx.Done():
					return provider.LoadBalancerConfiguration{}, ctx.Err()
				}
			}))
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("stale refresh did not reach reader")
	}
	approve(first)
	wait(first, "observing")
	waitPendingCloudObservation(t, db, first.ID)
	f.settled.Store(true)
	op := wait(first, "succeeded")
	if op.ProviderRequestID != fixtureLoadBalancerReceipt || op.LoadBalancerBackendWeightIntent == nil ||
		op.LoadBalancerBackendWeightIntent.MemberKey != memberKey || op.LoadBalancerBackendWeightIntent.Weight != 0 {
		t.Fatalf("missing member evidence: %+v", op)
	}
	close(release)
	if err := <-refreshResult; !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("stale refresh committed: %v", err)
	}
	if got, err := readWeight(memberKey); err != nil || got != 0 {
		t.Fatalf("member weight %d: %v", got, err)
	}
	if got, err := readWeight(otherKey); err != nil || got != 100 {
		t.Fatalf("unrelated member weight %d: %v", got, err)
	}
	if f.vendor != provider.HuaweiECS && f.jobReads.Load() < 2 {
		t.Fatal("async receipt bypassed job observation")
	}

	f.dropReply.Store(true)
	second := submit("project", "dropped-reply", input(0, 60), http.StatusAccepted)
	approve(second)
	wait(second, "observing")
	waitPendingCloudObservation(t, db, second.ID)
	if err := localInventory.RefreshLoadBalancerConfiguration(ctx, k, provider.LoadBalancerConfigurationRequest{ID: f.id}, 1,
		loadBalancerConfigurationReaderFunc(func(context.Context, provider.Scope, provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
			return provider.LoadBalancerConfiguration{}, nil
		})); err != nil {
		t.Fatal(err)
	}
	f.settled.Store(true)
	op = wait(second, "succeeded")
	if op.ProviderRequestID != "" || f.writes.Load() != 2 || f.weight.Load() != 60 {
		t.Fatalf("uncertain result did not reconcile once: %+v writes=%d", op, f.writes.Load())
	}
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='load-balancer.operation.succeeded'`).Scan(&events); err != nil || events != 2 {
		t.Fatalf("unexpected outbox: %d %v", events, err)
	}
}

func (f *backendWeightFixture) applyWeight(t *testing.T, w http.ResponseWriter, weight int, reply func() bool) bool {
	t.Helper()
	if weight != 0 && weight != 60 {
		return false
	}
	f.writes.Add(1)
	f.weight.Store(int32(weight))
	f.settled.Store(false)
	if f.dropReply.Load() {
		return dropImageCreationReply(t, w)
	}
	return reply()
}

func (f *backendWeightFixture) applyDetach(t *testing.T, w http.ResponseWriter, reply func() bool) bool {
	t.Helper()
	if f.weight.Load() != 0 {
		return false
	}
	f.writes.Add(1)
	f.removing.Store(true)
	f.settled.Store(false)
	if f.dropReply.Load() {
		return dropImageCreationReply(t, w)
	}
	return reply()
}

func (f *backendWeightFixture) serveWeight(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	action := r.Header.Get("X-TC-Action")
	aliAction := r.Header.Get("X-ACS-Action")
	if f.memberListUnavailable.Load() && (action == "DescribeTargetGroupInstances" || aliAction == "ListServerGroupServers" ||
		(strings.HasSuffix(r.URL.Path, "/members") && r.Method == http.MethodGet)) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch {
	case action == "DescribeTargetGroupInstances" || action == "ModifyTargetGroupInstancesWeight" || action == "DeregisterTargetGroupInstances":
		if f.tencentWeight(t, w, r, action) {
			return
		}
	case aliAction == "ListServerGroupServers" || aliAction == "UpdateServerGroupServersAttribute" || aliAction == "RemoveServersFromServerGroup" ||
		(aliAction == "ListAsynJobs" && f.product == "alibaba-alb"):
		if f.alibabaWeight(t, w, r, aliAction) {
			return
		}
	case strings.Contains(r.URL.Path, "/members"):
		if f.huaweiWeight(t, w, r) {
			return
		}
	case strings.HasSuffix(r.URL.Path, "/pools/"+f.group) && f.vendor == provider.HuaweiECS:
		parents := []map[string]string{{"id": f.id}}
		if f.wrongTarget.Load() {
			parents = append(parents, map[string]string{"id": lbConfigurationHuaweiSecond})
		}
		if r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM && r.Method == http.MethodGet &&
			sendNetworkFixture(t, w, map[string]any{"pool": map[string]any{"id": f.group, "project_id": fixtureHuaweiProject, "name": "group",
				"loadbalancers": parents, "lb_algorithm": "ROUND_ROBIN", "vpc_id": backendFixtureVPC, "type": "instance"}}) {
			return
		}
	default:
		f.serveRename(t, w, r)
		return
	}
	t.Errorf("unexpected backend weight request: %s %s %s %s", r.Method, r.Host, action, aliAction)
	w.WriteHeader(http.StatusBadRequest)
}

func (f *backendWeightFixture) tencentWeight(t *testing.T, w http.ResponseWriter, r *http.Request, action string) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "clb.tencentcloudapi.com" || r.Header.Get("X-TC-Region") != f.region ||
		r.Header.Get("X-TC-Version") != "2018-03-17" || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	respond := func(result any) bool { return sendNetworkFixture(t, w, map[string]any{"Response": result}) }
	if action == "DescribeTargetGroupInstances" {
		var request struct {
			Filters []struct {
				Name   string
				Values []string
			}
			Limit, Offset int
		}
		if json.Unmarshal(body, &request) != nil || len(request.Filters) != 1 || request.Filters[0].Name != "TargetGroupId" ||
			!singleTarget(request.Filters[0].Values, f.group) || request.Limit != 50 || request.Offset != 0 {
			return false
		}
		rows := []map[string]any{
			{"TargetGroupId": f.group, "InstanceId": f.member, "BindIP": weightEndpointMember, "Port": weightEndpointPort, "Weight": f.weight.Load()},
			{"TargetGroupId": f.group, "InstanceId": f.other, "BindIP": weightEndpointSibling, "Port": weightEndpointPort, "Weight": 100},
		}
		if f.removed.Load() {
			rows = rows[1:]
		}
		if f.changedMemberAttributes.Load() && !f.removed.Load() {
			rows[0]["InstanceId"] = "ins-rebound"
		}
		return respond(map[string]any{"TotalCount": len(rows), "RealCount": len(rows), "TargetGroupInstanceSet": rows})
	}
	var request struct {
		TargetGroupId        string
		TargetGroupInstances []struct {
			BindIP string
			Port   int
			Weight *int
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.TargetGroupId != f.group || len(request.TargetGroupInstances) != 1 {
		return false
	}
	member := request.TargetGroupInstances[0]
	if member.BindIP != weightEndpointMember || member.Port != weightEndpointPort {
		return false
	}
	reply := func() bool { return respond(map[string]string{"RequestId": fixtureLoadBalancerReceipt}) }
	if action == "DeregisterTargetGroupInstances" {
		return member.Weight == nil && f.applyDetach(t, w, reply)
	}
	return member.Weight != nil && f.applyWeight(t, w, *member.Weight, reply)
}

func (f *backendWeightFixture) alibabaWeight(t *testing.T, w http.ResponseWriter, r *http.Request, action string) bool {
	nlb := f.product == "alibaba-nlb"
	endpoint, version := "alb.", "2020-06-16"
	if nlb {
		endpoint, version = "nlb.", "2022-04-30"
	}
	if r.Host != endpoint+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Version") != version ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	query := r.URL.Query()
	if nlb || action == "UpdateServerGroupServersAttribute" {
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
			return false
		}
	}
	values := query
	if nlb {
		values = r.PostForm
		if len(query) != 0 || values.Get("RegionId") != f.region {
			return false
		}
	}
	switch action {
	case "ListServerGroupServers":
		if values.Get("ServerGroupId") != f.group || values.Get("MaxResults") != "50" {
			return false
		}
		status := "Available"
		if !f.settled.Load() {
			status = "Configuring"
		}
		row := func(id, address string, weight int32) map[string]any {
			return map[string]any{"ServerGroupId": f.group, "ServerId": id, "ServerIp": address, "ServerType": "Ecs", "Port": weightEndpointPort, "Weight": weight, "Status": status}
		}
		targetAddress := weightEndpointMember
		if f.changedMemberAttributes.Load() {
			targetAddress = "10.20.0.99"
		}
		rows := []map[string]any{row(f.member, targetAddress, f.weight.Load()), row(f.other, weightEndpointSibling, 100)}
		if f.removed.Load() {
			rows = rows[1:]
		}
		return sendNetworkFixture(t, w, map[string]any{"Servers": rows})
	case "RemoveServersFromServerGroup":
		expected := 4
		if nlb {
			expected++
		} else if len(r.PostForm) != 0 {
			return false
		}
		if len(values) != expected || values.Get("ServerGroupId") != f.group ||
			values.Get("Servers.1.ServerId") != f.member || values.Get("Servers.1.Port") != "8080" ||
			values.Get("Servers.1.ServerType") != "Ecs" {
			return false
		}
		return f.applyDetach(t, w, func() bool {
			return sendNetworkFixture(t, w, map[string]any{"JobId": fixtureLoadBalancerReceipt, "ServerGroupId": f.group})
		})
	case "UpdateServerGroupServersAttribute":
		fields := r.PostForm
		expected := 5
		if nlb {
			expected += 2
		} else if len(query) != 1 {
			return false
		}
		next, err := strconv.Atoi(fields.Get("Servers.1.Weight"))
		if err != nil || values.Get("ServerGroupId") != f.group || len(fields) != expected ||
			fields.Get("Servers.1.ServerId") != f.member || fields.Get("Servers.1.Port") != "8080" ||
			fields.Get("Servers.1.ServerIp") != weightEndpointMember || fields.Get("Servers.1.ServerType") != "Ecs" {
			return false
		}
		return f.applyWeight(t, w, next, func() bool { return sendNetworkFixture(t, w, map[string]any{"JobId": fixtureLoadBalancerReceipt}) })
	case "ListAsynJobs":
		api := "UpdateServerGroupServersAttribute"
		if f.adding.Load() {
			api = "AddServersToServerGroup"
		}
		if f.removing.Load() {
			api = "RemoveServersFromServerGroup"
		}
		if query.Get("ResourceIds.1") != f.group || query.Get("ResourceType") != "servergroup" ||
			query.Get("ApiName") != api || query.Get("JobIds.1") != fixtureLoadBalancerReceipt {
			return false
		}
		f.jobReads.Add(1)
		status := "Processing"
		if f.settled.Load() {
			status = "Succeeded"
		}
		return sendNetworkFixture(t, w, map[string]any{"Jobs": []map[string]string{
			{"Id": fixtureLoadBalancerReceipt, "ApiName": api, "ResourceId": f.group, "ResourceType": "servergroup", "Status": status},
		}})
	}
	return false
}

func (f *backendWeightFixture) huaweiWeight(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Host != "elb."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	path := "/v3/" + fixtureHuaweiProject + "/elb/pools/" + f.group + "/members"
	row := func(id string, weight int32) map[string]any {
		address := weightEndpointMember
		if id == f.other {
			address = weightEndpointSibling
		}
		return map[string]any{"id": id, "project_id": fixtureHuaweiProject, "protocol_port": weightEndpointPort, "weight": weight, "address": address, "subnet_cidr_id": backendFixtureSubnet}
	}
	if r.Method == http.MethodGet && r.URL.Path == path && r.URL.Query().Get("limit") == "50" {
		rows := []map[string]any{row(f.member, f.weight.Load()), row(f.other, 100)}
		if f.changedMemberAttributes.Load() {
			rows[0]["protocol_port"] = weightEndpointPort + 1
		}
		if f.removed.Load() {
			rows = rows[1:]
		}
		return sendNetworkFixture(t, w, map[string]any{"members": rows, "page_info": map[string]int{"current_count": len(rows)}})
	}
	if r.Method == http.MethodDelete && r.URL.Path == path+"/"+f.member {
		return f.applyDetach(t, w, func() bool { w.WriteHeader(http.StatusNoContent); return true })
	}
	if r.Method != http.MethodPut || r.URL.Path != path+"/"+f.member || r.Header.Get("Content-Type") != "application/json" {
		return false
	}
	var request struct{ Member struct{ Weight *int } }
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Member.Weight == nil {
		return false
	}
	return f.applyWeight(t, w, *request.Member.Weight, func() bool {
		return sendNetworkFixture(t, w, map[string]any{"member": row(f.member, f.weight.Load()), "request_id": fixtureLoadBalancerReceipt})
	})
}
