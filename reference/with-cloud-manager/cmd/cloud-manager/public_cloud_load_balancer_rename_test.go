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

const fixtureLoadBalancerReceipt = "cf4ccf28-62ba-47d7-8498-884dd29143f2"

type loadBalancerRenameFixture struct {
	loadBalancerConfigurationFixture
	name        atomic.Value
	writes      atomic.Int32
	jobReads    atomic.Int32
	settled     atomic.Bool
	dropReply   atomic.Bool
	wrongTarget atomic.Bool
}

func TestAcceptancePublicCloudLoadBalancerRename(t *testing.T) {
	for _, product := range []struct{ vendor, account, region, kind, id string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "tencent-clb", "lb-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-alb", "alb-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-nlb", "nlb-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-clb", "lb-fixture"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "huawei-elb", lbConfigurationHuaweiID},
	} {
		t.Run(product.kind, func(t *testing.T) {
			t.Parallel()
			f := &loadBalancerRenameFixture{loadBalancerConfigurationFixture: loadBalancerConfigurationFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: product.vendor, account: product.account, region: product.region},
				product:            product.kind, id: product.id,
			}}
			testLoadBalancerRename(t, f)
		})
	}
}

func TestAcceptancePublicCloudLoadBalancerListenerRename(t *testing.T) {
	for _, product := range []struct{ vendor, account, region, kind, id, listener string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "tencent-clb", "lb-fixture", "lbl-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-alb", "alb-fixture", "lsn-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-nlb", "nlb-fixture", "lsn-fixture@443"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "huawei-elb", lbConfigurationHuaweiID, lbConfigurationHuaweiListener},
	} {
		t.Run(product.kind, func(t *testing.T) {
			t.Parallel()
			f := &loadBalancerRenameFixture{loadBalancerConfigurationFixture: loadBalancerConfigurationFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: product.vendor, account: product.account, region: product.region},
				product:            product.kind, id: product.id, listener: product.listener,
			}}
			testLoadBalancerRename(t, f)
		})
	}
}

func TestAcceptancePublicCloudLoadBalancerTargetGroupRename(t *testing.T) {
	for _, product := range []struct{ vendor, account, region, kind, id, group string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "tencent-clb", "lb-fixture", "lbtg-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-alb", "alb-fixture", "sgp-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "alibaba-nlb", "nlb-fixture", "sgp-fixture"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "huawei-elb", lbConfigurationHuaweiID, lbConfigurationHuaweiPool},
	} {
		t.Run(product.kind, func(t *testing.T) {
			t.Parallel()
			f := &loadBalancerRenameFixture{loadBalancerConfigurationFixture: loadBalancerConfigurationFixture{
				vpcCreationFixture: vpcCreationFixture{vendor: product.vendor, account: product.account, region: product.region},
				product:            product.kind, id: product.id, group: product.group,
			}}
			testLoadBalancerRename(t, f)
		})
	}
}

func testLoadBalancerRename(t *testing.T, f *loadBalancerRenameFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	seedLoadBalancerConfiguration(t, db, &f.loadBalancerConfigurationFixture)
	action, suffix := "rename-load-balancer", "rename"
	component := f.listener != "" || f.group != ""
	const otherParent = "alternate-parent"
	inventoryStore := inventory.NewStore(db)
	inventoryKey := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.LoadBalancer}
	configRequest := provider.LoadBalancerConfigurationRequest{ID: f.id}
	readConfig := func(context.Context, provider.Scope, provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
		if f.group != "" {
			return provider.LoadBalancerConfiguration{
				Listeners: []provider.LoadBalancerListener{{ID: "fixture-listener", Name: "routing", Protocol: "TCP", Port: 443, TargetGroupIDs: []string{f.group}}},
				TargetGroups: []provider.LoadBalancerTargetGroup{
					{ID: f.group, Name: "balancer", Algorithm: "round-robin", Exclusive: true},
					{ID: "shared-group", Name: "shared"},
				},
			}, nil
		}
		return provider.LoadBalancerConfiguration{Listeners: []provider.LoadBalancerListener{
			{ID: f.listener, Name: "balancer", Protocol: "TCP", Port: 443, TargetGroupIDs: []string{"group-one"}},
			{ID: "sibling", Name: "untouched", Protocol: "HTTP", Port: 80},
		}, TargetGroups: []provider.LoadBalancerTargetGroup{{ID: "group-one", Name: "backend"}}}, nil
	}
	switch {
	case f.listener != "":
		action, suffix = "rename-load-balancer-listener", "rename-listener"
	case f.group != "":
		action, suffix = "rename-load-balancer-target-group", "rename-target-group"
	}
	if component {
		if err := inventoryStore.RefreshLoadBalancerConfiguration(ctx, inventoryKey, configRequest, 1, loadBalancerConfigurationReaderFunc(readConfig)); err != nil {
			t.Fatal(err)
		}
	}
	if f.group != "" {
		if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,load_balancer)
   SELECT scope_id,$2,name,status,seen_generation,load_balancer FROM cmp_inventory_objects WHERE external_id=$1`, f.id, otherParent); err != nil {
			t.Fatal(err)
		}
		if err := inventoryStore.RefreshLoadBalancerConfiguration(ctx, inventoryKey, provider.LoadBalancerConfigurationRequest{ID: otherParent},
			1, loadBalancerConfigurationReaderFunc(readConfig)); err != nil {
			t.Fatal(err)
		}
	}
	f.name.Store("balancer")
	f.settled.Store(true)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { f.serveRename(t, w, req) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewTLSServer(app)
	t.Cleanup(local.Close)
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
	var pauseDispatch atomic.Bool
	checked, resume := make(chan struct{}), make(chan struct{})
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if pauseDispatch.CompareAndSwap(true, false) {
				close(checked)
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			config := profile
			config.AllowLoadBalancerRename = !component && enabled.Load()
			config.AllowLoadBalancerTargetGroupRename = f.group != "" && enabled.Load()
			config.AllowLoadBalancerListenerRename = f.listener != "" && enabled.Load()
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
			t.Error("rename worker did not stop")
		}
	}()
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	const base = "/v1/tenants/tenant/projects/"
	payload := func(current, desired string) []byte {
		t.Helper()
		input := operations.LoadBalancerRenameInput{
			Account: f.vendor, Region: f.region, AssetID: f.id, CurrentName: &current, Name: desired,
		}
		var body any = input
		if f.listener != "" {
			body = operations.LoadBalancerListenerRenameInput{LoadBalancerRenameInput: input, ListenerID: f.listener}
		} else if f.group != "" {
			body = operations.LoadBalancerTargetGroupRenameInput{LoadBalancerRenameInput: input, TargetGroupID: f.group}
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	submit := func(project, key, revision string, body []byte, want int) operations.Operation {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, base+project+"/load-balancers/"+suffix, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+lbConfigurationActor)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s returned %d, want %d: %s", key, w.Code, want, w.Body.String())
		}
		var out operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &out)
			if out.ResourceKind != provider.LoadBalancer || out.Action != action ||
				w.Header().Get("Location") != base+project+"/operations/"+out.ID {
				t.Fatalf("wrong operation receipt: %+v", out)
			}
		}
		return out
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

	request := payload("balancer", "north-edge")
	submit("other", "wrong-project", `"1"`, request, http.StatusForbidden)
	submit("project", "wrong-version", `"2"`, request, http.StatusPreconditionFailed)
	submit("project", "old-name", `"1"`, payload("outdated", "north-edge"), http.StatusPreconditionFailed)
	if f.listener != "" {
		submit("project", "missing-listener", `"1"`, bytes.Replace(request, []byte(f.listener), []byte("unrelated-listener"), 1), http.StatusPreconditionFailed)
		submit("project", "invalid-listener", `"1"`, bytes.Replace(request, []byte(f.listener), []byte("../unexpected"), 1), http.StatusBadRequest)
	}
	if f.group != "" {
		submit("project", "shared-group", `"1"`, bytes.Replace(request, []byte(f.group), []byte("shared-group"), 1), http.StatusPreconditionFailed)
	}
	disabled := submit("project", "disabled", `"1"`, request, http.StatusAccepted)
	if disabled.Status != "awaiting_approval" {
		t.Fatalf("rename skipped approval: %+v", disabled)
	}
	if _, err := store.Approve(ctx, lbConfigurationActor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester could approve: %v", err)
	}
	approve(disabled)
	if op := wait(disabled, "failed"); op.OutcomeReason != "load_balancer_rename_disabled" || f.writes.Load() != 0 {
		t.Fatalf("disabled write escaped preflight: %+v", op)
	}
	enabled.Store(true)
	drift := submit("project", "remote-drift", `"1"`, request, http.StatusAccepted)
	f.name.Store("renamed-externally")
	approve(drift)
	if op := wait(drift, "failed"); op.OutcomeReason != "load_balancer_rename_preconditions_unconfirmed" || f.writes.Load() != 0 {
		t.Fatalf("remote name change escaped preflight: %+v", op)
	}
	f.name.Store("balancer")
	if component {
		outsideParent := submit("project", "wrong-provider-parent", `"1"`, request, http.StatusAccepted)
		f.wrongTarget.Store(true)
		approve(outsideParent)
		wait(outsideParent, "failed")
		f.wrongTarget.Store(false)
		if f.writes.Load() != 0 {
			t.Fatal("unexpected component or parent reached write")
		}
	}
	if f.product == "tencent-clb" {
		revoked := submit("project", "revoked-approval", `"1"`, request, http.StatusAccepted)
		pauseDispatch.Store(true)
		approve(revoked)
		select {
		case <-checked:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not get to provider connection")
		}
		if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET active=false,revision=revision+1 WHERE principal_id='reviewer'`); err != nil {
			t.Fatal(err)
		}
		close(resume)
		if op := wait(revoked, "failed"); op.OutcomeReason != "dispatch_unavailable" || f.writes.Load() != 0 {
			t.Fatalf("withdrawn approval reached vendor: %+v", op)
		}
		if _, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET active=true,revision=revision+1 WHERE principal_id='reviewer'`); err != nil {
			t.Fatal(err)
		}
	}

	var output bytes.Buffer
	if err := runOperationsClient(ctx, []string{action, "tenant", "project", "--idempotency-key", "rename-first", "--if-match", "1"},
		local.URL, lbConfigurationActor, bytes.NewReader(request), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var first operations.Operation
	if err := json.Unmarshal(output.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.Status != "awaiting_approval" {
		t.Fatalf("invalid CLI receipt: %+v", first)
	}
	if replay := submit("project", "rename-first", `"1"`, request, http.StatusAccepted); replay.ID != first.ID {
		t.Fatal("replay changed operation ID")
	}
	submit("project", "rename-first", `"1"`, payload("balancer", "a-different-name"), http.StatusConflict)
	submit("project", "rename-parallel", `"1"`, request, http.StatusConflict)
	if f.group != "" {
		submit("project", "rename-on-different-parent", `"1"`, bytes.Replace(request, []byte(f.id), []byte(otherParent), 1), http.StatusConflict)
	}
	var claimed inventory.Claim
	refreshStarted, finishRefresh := make(chan struct{}), make(chan struct{})
	refreshResult := make(chan error, 1)
	if !component {
		var err error
		claimed, err = inventoryStore.Claim(ctx, inventoryKey, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		refreshCtx, cancelRefresh := context.WithCancel(ctx)
		defer cancelRefresh()
		go func() {
			refreshResult <- inventoryStore.RefreshLoadBalancerConfiguration(refreshCtx, inventoryKey, configRequest, 1,
				loadBalancerConfigurationReaderFunc(func(readCtx context.Context, sc provider.Scope, req provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
					close(refreshStarted)
					select {
					case <-finishRefresh:
						return readConfig(readCtx, sc, req)
					case <-readCtx.Done():
						return provider.LoadBalancerConfiguration{}, readCtx.Err()
					}
				}))
		}()
		select {
		case <-refreshStarted:
		case <-time.After(5 * time.Second):
			t.Fatal("stale refresh did not reach cloud read")
		}
	}
	approve(first)
	wait(first, "observing")
	waitPendingCloudObservation(t, db, first.ID)
	f.settled.Store(true)
	first = wait(first, "succeeded")
	if first.LoadBalancerRenameIntent == nil || first.LoadBalancerRenameIntent.Name != "north-edge" ||
		first.LoadBalancerRenameIntent.CurrentName != "balancer" || first.LoadBalancerRenameIntent.ListenerID != f.listener || first.LoadBalancerRenameIntent.TargetGroupID != f.group || first.ProviderRequestID != fixtureLoadBalancerReceipt {
		t.Fatalf("bad confirmed operation: %+v", first)
	}
	if !component {
		if _, _, err := inventoryStore.ScanPage(ctx, claimed, staleAssetDiscovery{}); !errors.Is(err, inventory.ErrStale) {
			t.Fatalf("late scan could overwrite confirmed rename: %v", err)
		}
	} else {
		close(finishRefresh)
		if err := <-refreshResult; !errors.Is(err, inventory.ErrStale) {
			t.Fatalf("late configuration could overwrite confirmed name: %v", err)
		}
	}
	if ((f.product == "tencent-clb" && f.group == "") || f.product == "alibaba-nlb" || (f.product == "alibaba-alb" && component)) && f.jobReads.Load() < 2 {
		t.Fatal("async job bypassed observation")
	}

	f.dropReply.Store(true)
	second := submit("project", "rename-disconnect", `"1"`, payload("north-edge", "south-edge"), http.StatusAccepted)
	approve(second)
	wait(second, "observing")
	waitPendingCloudObservation(t, db, second.ID)
	f.settled.Store(true)
	second = wait(second, "succeeded")
	if second.ProviderRequestID != "" || f.writes.Load() != 2 {
		t.Fatalf("lost reply did not reconcile exactly once: %+v writes=%d", second, f.writes.Load())
	}
	var actualName string
	var events int
	expectedName := "south-edge"
	if component {
		expectedName = "balancer"
		cfg, err := inventoryStore.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, inventory.LoadBalancerConfigurationQuery{
			Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id,
		})
		if err != nil {
			t.Fatal(err)
		}
		if f.group != "" {
			if len(cfg.Listeners) != 1 || len(cfg.TargetGroups) != 2 || cfg.Listeners[0].Name != "routing" ||
				cfg.TargetGroups[0].Name != "south-edge" || cfg.TargetGroups[0].Algorithm != "ROUND-ROBIN" || !cfg.TargetGroups[0].Exclusive ||
				cfg.TargetGroups[1].Name != "shared" || cfg.TargetGroups[1].Exclusive {
				t.Fatalf("target group projection incomplete: %+v", cfg)
			}
		} else if len(cfg.Listeners) != 2 || len(cfg.TargetGroups) != 1 || cfg.Listeners[0].Name != "south-edge" ||
			cfg.Listeners[1].Name != "untouched" || cfg.TargetGroups[0].Name != "backend" {
			t.Fatalf("listener projection incomplete: %+v", cfg)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT name,(SELECT count(*) FROM cmp_operation_event_outbox
		WHERE event_type='load-balancer.operation.succeeded') FROM cmp_inventory_objects WHERE external_id=$1`, f.id).
		Scan(&actualName, &events); err != nil || actualName != expectedName || events != 2 {
		t.Fatalf("rename and outbox did not converge together: name=%q events=%d err=%v", actualName, events, err)
	}
	if f.product == "tencent-clb" || f.group != "" {
		retry := submit("project", "rename-retry-observation", `"1"`, payload("south-edge", "retry-edge"), http.StatusAccepted)
		approve(retry)
		wait(retry, "observing")
		if f.group != "" {
			waitPendingCloudObservation(t, db, retry.ID)
			f.wrongTarget.Store(true)
		} else if _, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET max_attempts=2 WHERE id=$1`, retry.ID); err != nil {
			t.Fatal(err)
		}
		wait(retry, "needs_review")
		if f.group != "" {
			config, err := inventoryStore.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, inventory.LoadBalancerConfigurationQuery{
				Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id,
			})
			if err != nil || len(config.TargetGroups) != 2 || config.TargetGroups[0].Name != "south-edge" {
				t.Fatalf("shared remote relationship allowed premature confirmation: %+v %v", config, err)
			}
			f.wrongTarget.Store(false)
		}
		if _, err := store.Resolve(ctx, lbConfigurationReviewer, scope, retry.ID, operations.Resolution{
			Decision: "succeeded", EvidenceRef: "incident-1",
		}); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("manual assertion bypassed provider proof: %v", err)
		}
		enabled.Store(false)
		f.settled.Store(true)
		request := operations.ObservationRetry{EvidenceRef: "incident-1"}
		review, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := store.RetryCreationObservation(review, lbConfigurationReviewer, scope, retry.ID, request); err == nil {
				break
			} else if !errors.Is(err, operations.ErrConflict) {
				t.Fatal(err)
			}
			select {
			case <-review.Done():
				t.Fatal("worker did not finish the observation lease")
			case <-ticker.C:
			}
		}
		wait(retry, "succeeded")
		if f.writes.Load() != 3 {
			t.Fatalf("read-only resumption dispatched a write: %d", f.writes.Load())
		}
	}
}

func (f *loadBalancerRenameFixture) serveRename(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	switch r.Host {
	case "sts.tencentcloudapi.com", "sts.aliyuncs.com", "iam.myhuaweicloud.com":
		f.serve(t, w, r)
		return
	}
	var ok bool
	switch f.vendor {
	case provider.TencentCVM:
		ok = f.tencentRename(t, w, r)
	case provider.AlibabaECS:
		ok = f.alibabaRename(t, w, r)
	case provider.HuaweiECS:
		ok = f.huaweiRename(t, w, r)
	}
	if !ok {
		t.Errorf("unexpected rename request: %s %s %s %s %s %v", r.Method, r.Host, r.URL.Path,
			r.Header.Get("X-TC-Action"), r.Header.Get("X-ACS-Action"), r.URL.Query())
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *loadBalancerRenameFixture) acceptRename(t *testing.T, w http.ResponseWriter, id, desired string, reply func() bool) bool {
	t.Helper()
	if id != f.id || (desired != "north-edge" && desired != "south-edge" && desired != "retry-edge") {
		return false
	}
	f.writes.Add(1)
	f.name.Store(desired)
	f.settled.Store(false)
	if f.dropReply.Load() {
		return dropImageCreationReply(t, w)
	}
	return reply()
}

func (f *loadBalancerRenameFixture) tencentRename(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "clb.tencentcloudapi.com" || r.Header.Get("X-TC-Version") != "2018-03-17" ||
		r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
		!validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var input struct {
		TaskId, LoadBalancerId, LoadBalancerName, ListenerId, ListenerName, TargetGroupId, TargetGroupName string
		TargetGroupIds                                                                                     []string
		ListenerIds                                                                                        []string
		LoadBalancerIds                                                                                    []string
	}
	if json.Unmarshal(body, &input) != nil {
		return false
	}
	reply := func(data any) bool { return sendNetworkFixture(t, w, map[string]any{"Response": data}) }
	switch r.Header.Get("X-TC-Action") {
	case "DescribeLoadBalancers":
		if !singleTarget(input.LoadBalancerIds, f.id) {
			return false
		}
		state := 1
		if (f.dropReply.Load() || f.group != "") && !f.settled.Load() {
			state = 0
		}
		return reply(map[string]any{"TotalCount": 1, "LoadBalancerSet": []map[string]any{{
			"LoadBalancerId": f.id, "LoadBalancerType": "INTERNAL", "LoadBalancerName": f.name.Load(), "Status": state, "Forward": 1, "VpcId": "vpc-fixture",
		}}})
	case "DescribeTargetGroups":
		if f.group == "" || !singleTarget(input.TargetGroupIds, f.group) {
			return false
		}
		references := []map[string]string{{"LoadBalancerId": f.id}}
		if f.wrongTarget.Load() {
			references = append(references, map[string]string{"LoadBalancerId": "lb-shared"})
		}
		return reply(map[string]any{"TotalCount": 1, "TargetGroupSet": []map[string]any{{
			"TargetGroupId": f.group, "TargetGroupName": f.name.Load(), "AssociatedRule": references, "VpcId": "vpc-fixture",
		}}})
	case "ModifyTargetGroupAttribute":
		var fields map[string]string
		if f.group == "" || input.TargetGroupId != f.group || json.Unmarshal(body, &fields) != nil || len(fields) != 2 {
			return false
		}
		return f.acceptRename(t, w, f.id, input.TargetGroupName, func() bool {
			return reply(map[string]string{"RequestId": fixtureLoadBalancerReceipt})
		})
	case "DescribeListeners":
		if input.LoadBalancerId != f.id || !singleTarget(input.ListenerIds, f.listener) {
			return false
		}
		id := f.listener
		if f.wrongTarget.Load() {
			id = "lbl-unrelated"
		}
		return reply(map[string]any{"TotalCount": 1, "Listeners": []map[string]any{{"ListenerId": id, "ListenerName": f.name.Load()}}})
	case "ModifyListener":
		var fields map[string]string
		if json.Unmarshal(body, &fields) != nil || len(fields) != 3 || input.ListenerId != f.listener {
			return false
		}
		return f.acceptRename(t, w, input.LoadBalancerId, input.ListenerName, func() bool {
			return reply(map[string]string{"RequestId": fixtureLoadBalancerReceipt})
		})
	case "ModifyLoadBalancerAttributes":
		return f.acceptRename(t, w, input.LoadBalancerId, input.LoadBalancerName, func() bool {
			return reply(map[string]string{"RequestId": fixtureLoadBalancerReceipt})
		})
	case "DescribeTaskStatus":
		if input.TaskId != fixtureLoadBalancerReceipt || len(input.LoadBalancerIds) != 0 {
			return false
		}
		f.jobReads.Add(1)
		state := 2
		if f.settled.Load() {
			state = 0
		}
		return reply(map[string]int{"Status": state})
	default:
		return false
	}
}

func (f *loadBalancerRenameFixture) alibabaRename(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	action, version, q := r.Header.Get("X-ACS-Action"), r.Header.Get("X-ACS-Version"), r.URL.Query()
	host, requiredVersion := "slb.aliyuncs.com", "2014-05-15"
	switch f.product {
	case "alibaba-alb":
		host, requiredVersion = "alb."+f.region+".aliyuncs.com", "2020-06-16"
	case "alibaba-nlb":
		host, requiredVersion = "nlb."+f.region+".aliyuncs.com", "2022-04-30"
		if action == "UpdateLoadBalancerAttribute" || action == "UpdateListenerAttribute" || action == "UpdateServerGroupAttribute" || action == "ListServerGroups" {
			if len(q) != 0 || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
				return false
			}
			q = r.PostForm
		}
	}
	if r.Host != host || version != requiredVersion ||
		(f.product != "alibaba-alb" && q.Get("RegionId") != f.region) {
		return false
	}
	status := "Active"
	if !f.settled.Load() && (f.product != "alibaba-nlb" || f.dropReply.Load()) {
		status = "Configuring"
	}
	row := map[string]any{"LoadBalancerId": f.id, "LoadBalancerName": f.name.Load(), "RegionId": f.region,
		"AddressType": "Intranet", "VpcId": "vpc-fixture", "Address": "10.0.1.20", "LoadBalancerStatus": status}
	switch action {
	case "ListLoadBalancers":
		if f.product == "alibaba-clb" || q.Get("LoadBalancerIds.1") != f.id {
			return false
		}
		return sendNetworkFixture(t, w, map[string]any{"TotalCount": 1, "LoadBalancers": []map[string]any{row}})
	case "DescribeLoadBalancers":
		if f.product != "alibaba-clb" || q.Get("LoadBalancerId") != f.id {
			return false
		}
		return sendNetworkFixture(t, w, map[string]any{"TotalCount": 1, "PageSize": 50, "PageNumber": 1,
			"LoadBalancers": map[string]any{"LoadBalancer": []map[string]any{row}}})
	case "ListServerGroups":
		if f.group == "" || q.Get("ServerGroupIds.1") != f.group {
			return false
		}
		parents := []string{f.id}
		if f.wrongTarget.Load() {
			parents = append(parents, "other-parent")
		}
		state := "Available"
		if f.dropReply.Load() && !f.settled.Load() {
			state = "Configuring"
		}
		return sendNetworkFixture(t, w, map[string]any{"ServerGroups": []map[string]any{{
			"ServerGroupId": f.group, "ServerGroupName": f.name.Load(), "ServerGroupStatus": state, "RegionId": f.region, "RelatedLoadBalancerIds": parents, "VpcId": "vpc-fixture", "ServerGroupType": "Instance",
		}}})
	case "UpdateServerGroupAttribute":
		expected := 2
		if f.product == "alibaba-nlb" {
			expected++
		}
		if f.group == "" || q.Get("ServerGroupId") != f.group || len(q) != expected {
			return false
		}
		return f.acceptRename(t, w, f.id, q.Get("ServerGroupName"), func() bool {
			return sendNetworkFixture(t, w, map[string]any{"JobId": fixtureLoadBalancerReceipt, "ServerGroupId": f.group})
		})
	case "GetListenerAttribute":
		if q.Get("ListenerId") != f.listener {
			return false
		}
		id := f.id
		if f.wrongTarget.Load() {
			id = "different-parent"
		}
		readiness := "Running"
		if f.dropReply.Load() && !f.settled.Load() {
			readiness = "Configuring"
		}
		return sendNetworkFixture(t, w, map[string]any{"ListenerId": f.listener, "LoadBalancerId": id,
			"RegionId": f.region, "ListenerDescription": f.name.Load(), "ListenerStatus": readiness})
	case "UpdateListenerAttribute":
		expected := 2
		if f.product == "alibaba-nlb" {
			expected++
		}
		if f.listener == "" || q.Get("ListenerId") != f.listener || len(q) != expected {
			return false
		}
		return f.acceptRename(t, w, f.id, q.Get("ListenerDescription"), func() bool {
			return sendNetworkFixture(t, w, map[string]any{"JobId": fixtureLoadBalancerReceipt})
		})
	case "ListAsynJobs":
		id, resType, task := f.listener, "listener", "UpdateListenerAttribute"
		if f.group != "" {
			id, resType, task = f.group, "servergroup", "UpdateServerGroupAttribute"
		}
		if f.product != "alibaba-alb" || q.Get("ResourceIds.1") != id || q.Get("JobIds.1") != fixtureLoadBalancerReceipt ||
			q.Get("ResourceType") != resType || q.Get("ApiName") != task {
			return false
		}
		f.jobReads.Add(1)
		state := "Processing"
		if f.settled.Load() {
			state = "Succeeded"
		}
		return sendNetworkFixture(t, w, map[string]any{"Jobs": []map[string]string{{
			"Id": fixtureLoadBalancerReceipt, "ApiName": task, "ResourceId": id, "ResourceType": resType, "Status": state,
		}}})
	case "UpdateLoadBalancerAttribute", "SetLoadBalancerName":
		if (f.product == "alibaba-clb") != (action == "SetLoadBalancerName") {
			return false
		}
		return f.acceptRename(t, w, q.Get("LoadBalancerId"), q.Get("LoadBalancerName"), func() bool {
			return sendNetworkFixture(t, w, map[string]any{"RequestId": fixtureLoadBalancerReceipt, "JobId": fixtureLoadBalancerReceipt})
		})
	case "GetJobStatus":
		if f.product != "alibaba-nlb" || q.Get("JobId") != fixtureLoadBalancerReceipt {
			return false
		}
		f.jobReads.Add(1)
		state := "processing"
		if f.settled.Load() {
			state = "Succeeded"
		}
		return sendNetworkFixture(t, w, map[string]any{"Status": state})
	default:
		return false
	}
}

func (f *loadBalancerRenameFixture) huaweiRename(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	prefix := "/v3/" + fixtureHuaweiProject + "/elb/loadbalancers"
	if r.Host != "elb."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	if f.group != "" && r.URL.Path == "/v3/"+fixtureHuaweiProject+"/elb/pools/"+f.group {
		parents := []map[string]string{{"id": f.id}}
		if f.wrongTarget.Load() {
			parents = append(parents, map[string]string{"id": lbConfigurationHuaweiSecond})
		}
		reply := func() bool {
			return sendNetworkFixture(t, w, map[string]any{"request_id": fixtureLoadBalancerReceipt, "pool": map[string]any{
				"id": f.group, "project_id": fixtureHuaweiProject, "name": f.name.Load(), "loadbalancers": parents,
			}})
		}
		if r.Method == http.MethodGet {
			return reply()
		}
		var body struct{ Pool struct{ Name string } }
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if r.Method != http.MethodPut || r.Header.Get("Content-Type") != "application/json" || decoder.Decode(&body) != nil {
			return false
		}
		return f.acceptRename(t, w, f.id, body.Pool.Name, reply)
	}
	if f.listener != "" && r.URL.Path == "/v3/"+fixtureHuaweiProject+"/elb/listeners/"+f.listener {
		parent := f.id
		if f.wrongTarget.Load() {
			parent = lbConfigurationHuaweiSecond
		}
		write := func() bool {
			return sendNetworkFixture(t, w, map[string]any{"request_id": fixtureLoadBalancerReceipt, "listener": map[string]any{
				"id": f.listener, "project_id": fixtureHuaweiProject, "name": f.name.Load(), "loadbalancers": []map[string]string{{"id": parent}},
			}})
		}
		if r.Method == http.MethodGet {
			return write()
		}
		var body struct{ Listener struct{ Name string } }
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if r.Method != http.MethodPut || r.Header.Get("Content-Type") != "application/json" || decoder.Decode(&body) != nil {
			return false
		}
		return f.acceptRename(t, w, f.id, body.Listener.Name, write)
	}
	switch r.Method {
	case http.MethodGet:
		if r.URL.Path != prefix || r.URL.Query().Get("id") != f.id {
			return false
		}
		status := "ACTIVE"
		if !f.settled.Load() {
			status = "PENDING_UPDATE"
		}
		return sendNetworkFixture(t, w, map[string]any{"loadbalancers": []map[string]any{{
			"id": f.id, "project_id": fixtureHuaweiProject, "name": f.name.Load(), "vip_address": "10.0.1.12", "provisioning_status": status, "vpc_id": backendFixtureVPC,
		}}, "page_info": map[string]any{"current_count": 1}})
	case http.MethodPut:
		if r.URL.Path != prefix+"/"+f.id || r.Header.Get("Content-Type") != "application/json" {
			return false
		}
		var body struct{ LoadBalancer struct{ Name string } }
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			return false
		}
		return f.acceptRename(t, w, strings.TrimPrefix(r.URL.Path, prefix+"/"), body.LoadBalancer.Name, func() bool {
			return sendNetworkFixture(t, w, map[string]any{"request_id": fixtureLoadBalancerReceipt, "loadbalancer": map[string]string{
				"id": f.id, "project_id": fixtureHuaweiProject, "name": body.LoadBalancer.Name,
			}})
		})
	}
	return false
}
