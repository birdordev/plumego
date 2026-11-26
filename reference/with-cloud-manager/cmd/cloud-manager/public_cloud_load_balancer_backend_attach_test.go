package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
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

type backendAttachFixture struct {
	*backendWeightFixture
	differentVPC   atomic.Bool
	changedAddress atomic.Bool
}

func TestAcceptancePublicCloudLoadBalancerBackendAttach(t *testing.T) {
	publicCloudBackendFixtures(t, func(t *testing.T, f *backendWeightFixture) {
		testLoadBalancerBackendAttach(t, &backendAttachFixture{backendWeightFixture: f})
	})
}

func testLoadBalancerBackendAttach(t *testing.T, f *backendAttachFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	seedLoadBalancerConfiguration(t, db, &f.loadBalancerConfigurationFixture)
	sourceID := f.sourceInstanceID()
	f.name.Store("group")
	f.removed.Store(true)
	f.settled.Store(true)
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
  VALUES ('tenant',$1,$2,'instance',1,now()) ON CONFLICT DO NOTHING`, f.vendor, f.region); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
  SELECT scope_id,$3,'source instance','running',1 FROM cmp_inventory_scopes
  WHERE (tenant_id,account_id,region,kind)=('tenant',$1,$2,'instance')`, f.vendor, f.region, sourceID); err != nil {
		t.Fatal(err)
	}
	inv := inventory.NewStore(db)
	k := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.LoadBalancer}
	q := inventory.LoadBalancerConfigurationQuery{Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id}
	read := func(_ context.Context, _ provider.Scope, req provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
		sibling := f.memberKey(req.BackendSecret, f.other, weightEndpointSibling)
		return provider.LoadBalancerConfiguration{TargetGroups: []provider.LoadBalancerTargetGroup{{ID: f.group, Name: "group", Exclusive: true}},
			Backends: &provider.LoadBalancerBackendSnapshot{Members: []provider.LoadBalancerBackendMember{{Key: sibling, GroupID: f.group, Port: weightEndpointPort,
				Weight: new(100), WeightEditable: true}}}}, nil
	}
	request := provider.LoadBalancerConfigurationRequest{ID: f.id, IncludeBackends: true}
	if err := inv.RefreshLoadBalancerConfiguration(ctx, k, request, 1, loadBalancerConfigurationReaderFunc(read)); err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveAttach(t, w, r) }))
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
	var enabled atomic.Bool
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			config := profile
			config.AllowLoadBalancerBackendAttach = enabled.Load()
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
			t.Error("backend attach worker did not stop")
		}
	}()
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	encode := func(id string, revision int64, port int) []byte {
		t.Helper()
		body, err := json.Marshal(operations.LoadBalancerBackendAttachInput{Account: f.vendor, Region: f.region, AssetID: f.id, TargetGroupID: f.group,
			InstanceID: id, InstanceRevision: revision, Port: port})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	submit := func(project, requestKey, rev string, body []byte, want int) operations.Operation {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/"+project+"/load-balancers/attach-backend", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+lbConfigurationActor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", rev)
		req.Header.Set("Idempotency-Key", requestKey)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s returned %d want %d: %s", requestKey, w.Code, want, w.Body.String())
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.Action != "attach-load-balancer-backend" || w.Header().Get("Location") != "/v1/tenants/tenant/projects/"+project+"/operations/"+op.ID {
				t.Fatalf("invalid receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, lbConfigurationReviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, state string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, lbConfigurationActor, op.ID, state, true)
	}
	body := encode(sourceID, 1, weightEndpointPort)
	submit("project", "bad-port", `"1"`, encode(sourceID, 1, 0), http.StatusBadRequest)
	submit("project", "wrong-source-version", `"1"`, encode(sourceID, 2, weightEndpointPort), http.StatusPreconditionFailed)
	submit("project", "wrong-parent-version", `"2"`, body, http.StatusPreconditionFailed)
	submit("project", "unknown-source", `"1"`, encode(f.other, 1, weightEndpointPort), http.StatusForbidden)
	submit("other", "other-project", `"1"`, body, http.StatusForbidden)
	disabled := submit("project", "disabled", `"1"`, body, http.StatusAccepted)
	if _, err := store.Approve(ctx, lbConfigurationActor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self approval accepted: %v", err)
	}
	approve(disabled)
	wait(disabled, "failed")
	enabled.Store(true)
	mismatch := submit("project", "other-vpc", `"1"`, body, http.StatusAccepted)
	f.differentVPC.Store(true)
	approve(mismatch)
	wait(mismatch, "failed")
	f.differentVPC.Store(false)
	duplicate := submit("project", "existing-binding", `"1"`, body, http.StatusAccepted)
	f.removed.Store(false)
	approve(duplicate)
	wait(duplicate, "failed")
	f.removed.Store(true)
	if f.writes.Load() != 0 {
		t.Fatal("failed precondition reached provider mutation")
	}

	const requestKey = "attach-test"
	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{"attach-load-balancer-backend", "tenant", "project", "--idempotency-key", requestKey, "--if-match", "1"},
		local.URL, lbConfigurationActor, bytes.NewReader(body), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var op operations.Operation
	if err := json.Unmarshal(output.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	if op.Status != "awaiting_approval" || submit("project", requestKey, `"1"`, body, http.StatusAccepted).ID != op.ID {
		t.Fatalf("CLI and API diverged: %+v", op)
	}
	deletion := operations.Input{Account: f.vendor, Region: f.region, AssetID: sourceID, Action: "delete"}
	preview, err := store.PreviewDeletion(ctx, lbConfigurationActor, scope, deletion)
	if err != nil || !preview.ActiveBackendAttachment {
		t.Fatalf("missing source deletion protection: %+v %v", preview, err)
	}
	if preview.ProviderSupported {
		if _, err := store.Submit(ctx, lbConfigurationActor, scope, "delete-with-active-attach", 1, deletion); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("instance deletion could race attachment: %v", err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	refreshCtx, cancelRefresh := context.WithCancel(ctx)
	defer cancelRefresh()
	go func() {
		result <- inv.RefreshLoadBalancerConfiguration(refreshCtx, k, request, 1, loadBalancerConfigurationReaderFunc(
			func(ctx context.Context, scope provider.Scope, req provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
				close(started)
				select {
				case <-release:
					return read(ctx, scope, req)
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
	f.dropReply.Store(f.vendor == provider.TencentCVM)
	approve(op)
	wait(op, "observing")
	waitPendingCloudObservation(t, db, op.ID)
	f.removed.Store(false)
	f.settled.Store(true)
	if f.vendor == provider.HuaweiECS {
		f.changedAddress.Store(true)
		wait(op, "needs_review")
		f.changedAddress.Store(false)
		enabled.Store(false)
		retryCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		for {
			_, err := store.RetryCreationObservation(retryCtx, lbConfigurationReviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "source-interface-verified"})
			if err == nil {
				break
			}
			if !errors.Is(err, operations.ErrConflict) {
				t.Fatal(err)
			}
			select {
			case <-retryCtx.Done():
				t.Fatal("observation did not become retryable")
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	op = wait(op, "succeeded")
	close(release)
	if err := <-result; !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("old refresh erased confirmed backend: %v", err)
	}
	receipt := fixtureLoadBalancerReceipt
	if f.dropReply.Load() {
		receipt = ""
	}
	if f.writes.Load() != 1 || op.ProviderRequestID != receipt || op.LoadBalancerBackendAttachIntent == nil ||
		op.LoadBalancerBackendAttachIntent.InstanceID != sourceID || !provider.ValidLoadBalancerBackendKey(op.LoadBalancerBackendAttachIntent.MemberKey) {
		t.Fatalf("attachment proof or dispatch count inconsistent: %+v writes=%d", op, f.writes.Load())
	}
	cfg, err := inv.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q)
	if err != nil || len(cfg.Backends.Members) != 2 || !slices.ContainsFunc(cfg.Backends.Members, func(m provider.LoadBalancerBackendMember) bool {
		return m.Key == op.LoadBalancerBackendAttachIntent.MemberKey && m.Port == weightEndpointPort && m.Weight != nil && *m.Weight == 0 && m.WeightEditable
	}) {
		t.Fatalf("attachment projection inconsistent: %+v %v", cfg, err)
	}
	preview, err = store.PreviewDeletion(ctx, lbConfigurationActor, scope, deletion)
	if err != nil || preview.ActiveBackendAttachment {
		t.Fatalf("terminal attachment still reserved source: %+v %v", preview, err)
	}
	var binding string
	var events int
	if err := db.QueryRowContext(ctx, `SELECT binding_key,(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='load-balancer.operation.succeeded')
  FROM cmp_load_balancer_backend_attach_intents WHERE operation_id=$1`, op.ID).Scan(&binding, &events); err != nil ||
		!provider.ValidLoadBalancerBackendKey(binding) || events != 1 {
		t.Fatalf("attachment ledger inconsistent: %d %v", events, err)
	}
	encoded, err := json.Marshal(op)
	if err != nil || strings.Contains(string(encoded), weightEndpointMember) || strings.Contains(string(encoded), backendFixtureSubnet) || strings.Contains(string(encoded), binding) {
		t.Fatal("operation published private binding data")
	}
	if f.vendor == provider.HuaweiECS && strings.Contains(string(encoded), f.member) {
		t.Fatal("operation published Huawei member UUID")
	}
}

func (f *backendAttachFixture) serveAttach(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	action := r.Header.Get("X-TC-Action")
	ali := r.Header.Get("X-ACS-Action")
	switch {
	case r.Host == "cvm.tencentcloudapi.com":
		if f.tencentAttachSource(t, w, r) {
			return
		}
	case strings.HasPrefix(r.Host, "ecs.") && f.vendor == provider.AlibabaECS:
		if f.alibabaAttachSource(t, w, r) {
			return
		}
	case strings.HasPrefix(r.Host, "ecs.") && f.vendor == provider.HuaweiECS:
		if f.huaweiAttachSource(t, w, r) {
			return
		}
	case action == "RegisterTargetGroupInstances":
		if f.tencentAttachWrite(t, w, r) {
			return
		}
	case ali == "AddServersToServerGroup":
		if f.alibabaAttachWrite(t, w, r) {
			return
		}
	case r.Method == http.MethodPost && f.vendor == provider.HuaweiECS && strings.HasSuffix(r.URL.Path, "/members"):
		if f.huaweiAttachWrite(t, w, r) {
			return
		}
	default:
		f.serveWeight(t, w, r)
		return
	}
	t.Errorf("unexpected attach request: %s %s %s %s", r.Method, r.Host, action, ali)
	w.WriteHeader(http.StatusBadRequest)
}

func (f *backendAttachFixture) tencentAttachSource(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(r.Body)
	var request struct{ InstanceIds []string }
	if err != nil || r.Header.Get("X-TC-Action") != "DescribeInstances" || r.Header.Get("X-TC-Version") != "2017-03-12" ||
		r.Header.Get("X-TC-Region") != f.region || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) ||
		json.Unmarshal(body, &request) != nil || !singleTarget(request.InstanceIds, f.member) {
		return false
	}
	network := "vpc-fixture"
	if f.differentVPC.Load() {
		network = "vpc-unrelated"
	}
	return sendNetworkFixture(t, w, map[string]any{"Response": map[string]any{"TotalCount": 1, "InstanceSet": []map[string]any{{
		"InstanceId": f.member, "InstanceState": "RUNNING", "PrivateIpAddresses": []string{weightEndpointMember}, "VirtualPrivateCloud": map[string]string{"VpcId": network},
	}}}})
}

func (f *backendAttachFixture) alibabaAttachSource(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Action") != "DescribeInstances" ||
		r.Header.Get("X-ACS-Version") != "2014-05-26" || q.Get("RegionId") != f.region || q.Get("InstanceIds") != `["`+f.member+`"]` ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	network := "vpc-fixture"
	if f.differentVPC.Load() {
		network = "vpc-unrelated"
	}
	return sendNetworkFixture(t, w, map[string]any{"Instances": map[string]any{"Instance": []map[string]any{{
		"InstanceId": f.member, "RegionId": f.region, "Status": "Running", "VpcAttributes": map[string]any{
			"VpcId": network, "PrivateIpAddress": map[string]any{"IpAddress": []string{weightEndpointMember}},
		},
	}}}})
}

func (f *backendAttachFixture) sourceInstanceID() string {
	if f.vendor == provider.HuaweiECS {
		return "71000000-0000-4000-8000-000000000099"
	}
	return f.member
}

func (f *backendAttachFixture) huaweiAttachSource(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Host != "ecs."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || r.Method != http.MethodGet {
		return false
	}
	prefix := "/v1/" + fixtureHuaweiProject + "/cloudservers/" + f.sourceInstanceID()
	switch r.URL.Path {
	case prefix:
		network := backendFixtureVPC
		if f.differentVPC.Load() {
			network = "71000000-0000-4000-8000-000000000011"
		}
		return sendNetworkFixture(t, w, map[string]any{"server": map[string]any{"id": f.sourceInstanceID(), "tenant_id": fixtureHuaweiProject, "status": "ACTIVE", "metadata": map[string]string{"vpc_id": network}}})
	case prefix + "/os-interface":
		address := weightEndpointMember
		if f.changedAddress.Load() {
			address = "10.20.0.99"
		}
		return sendNetworkFixture(t, w, map[string]any{"interfaceAttachments": []map[string]any{{
			"port_id": "71000000-0000-4000-8000-000000000003", "port_state": "ACTIVE", "fixed_ips": []map[string]string{{"ip_address": address, "subnet_id": backendFixtureSubnet}},
		}}})
	}
	return false
}

func (f *backendAttachFixture) recordAttach(t *testing.T, w http.ResponseWriter, reply func() bool) bool {
	t.Helper()
	f.writes.Add(1)
	f.adding.Store(true)
	f.settled.Store(false)
	if f.dropReply.Load() {
		return dropImageCreationReply(t, w)
	}
	return reply()
}

func (f *backendAttachFixture) tencentAttachWrite(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "clb.tencentcloudapi.com" || r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Version") != "2018-03-17" ||
		!validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var request struct {
		TargetGroupId        string
		TargetGroupInstances []struct {
			BindIP string
			Port   int
			Weight *int
		}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || request.TargetGroupId != f.group || len(request.TargetGroupInstances) != 1 {
		return false
	}
	member := request.TargetGroupInstances[0]
	if member.BindIP != weightEndpointMember || member.Port != weightEndpointPort || member.Weight == nil || *member.Weight != 0 {
		return false
	}
	return f.recordAttach(t, w, func() bool {
		return sendNetworkFixture(t, w, map[string]any{"Response": map[string]string{"RequestId": fixtureLoadBalancerReceipt}})
	})
}

func (f *backendAttachFixture) alibabaAttachWrite(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	nlb := f.product == "alibaba-nlb"
	host, version := "alb.", "2020-06-16"
	if nlb {
		host, version = "nlb.", "2022-04-30"
	}
	if r.Host != host+f.region+".aliyuncs.com" || r.Header.Get("X-ACS-Version") != version ||
		r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) || r.ParseForm() != nil {
		return false
	}
	group, count := r.URL.Query().Get("ServerGroupId"), 5
	fields := r.PostForm
	if nlb {
		if len(r.URL.Query()) != 0 || fields.Get("RegionId") != f.region {
			return false
		}
		group, count = fields.Get("ServerGroupId"), 7
	} else if len(r.URL.Query()) != 1 {
		return false
	}
	if len(fields) != count || group != f.group || fields.Get("Servers.1.ServerId") != f.member || fields.Get("Servers.1.ServerType") != "Ecs" ||
		fields.Get("Servers.1.ServerIp") != weightEndpointMember || fields.Get("Servers.1.Port") != "8080" || fields.Get("Servers.1.Weight") != "0" {
		return false
	}
	return f.recordAttach(t, w, func() bool {
		return sendNetworkFixture(t, w, map[string]any{"JobId": fixtureLoadBalancerReceipt, "ServerGroupId": f.group})
	})
}

func (f *backendAttachFixture) huaweiAttachWrite(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Host != "elb."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM ||
		r.URL.Path != "/v3/"+fixtureHuaweiProject+"/elb/pools/"+f.group+"/members" || r.Header.Get("Content-Type") != "application/json" {
		return false
	}
	var request struct {
		Member struct {
			Address string
			Subnet  string `json:"subnet_cidr_id"`
			Port    int    `json:"protocol_port"`
			Weight  *int
		}
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&request) != nil || request.Member.Address != weightEndpointMember || request.Member.Subnet != backendFixtureSubnet ||
		request.Member.Port != weightEndpointPort || request.Member.Weight == nil || *request.Member.Weight != 0 {
		return false
	}
	return f.recordAttach(t, w, func() bool {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(map[string]any{"request_id": fixtureLoadBalancerReceipt, "member": map[string]any{
			"id": f.member, "project_id": fixtureHuaweiProject, "protocol_port": weightEndpointPort, "weight": 0, "address": weightEndpointMember, "subnet_cidr_id": backendFixtureSubnet,
		}}) == nil
	})
}
