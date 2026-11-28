package cloudtest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudLoadBalancerBackendDetach(t *testing.T) {
	publicCloudBackendFixtures(t, testLoadBalancerBackendDetach)
}

func testLoadBalancerBackendDetach(t *testing.T, f *backendWeightFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	seedLoadBalancerConfiguration(t, db, &f.loadBalancerConfigurationFixture)
	f.name.Store("group")
	f.settled.Store(true)
	inv := inventory.NewStore(db)
	k := inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.LoadBalancer}
	q := inventory.LoadBalancerConfigurationQuery{Tenant: "tenant", Project: "project", Account: f.vendor, Region: f.region, ID: f.id}
	read := func(_ context.Context, _ provider.Scope, req provider.LoadBalancerConfigurationRequest) (provider.LoadBalancerConfiguration, error) {
		target := f.memberKey(req.BackendSecret, f.member, weightEndpointMember)
		sibling := f.memberKey(req.BackendSecret, f.other, weightEndpointSibling)
		return provider.LoadBalancerConfiguration{
			Listeners:    []provider.LoadBalancerListener{{ID: "listener", Protocol: "TCP", Port: 443, TargetGroupIDs: []string{f.group}}},
			TargetGroups: []provider.LoadBalancerTargetGroup{{ID: f.group, Name: "group", Exclusive: true}},
			Backends: &provider.LoadBalancerBackendSnapshot{
				Members: []provider.LoadBalancerBackendMember{
					{Key: target, GroupID: f.group, Port: weightEndpointPort, Weight: new(0), WeightEditable: true},
					{Key: sibling, GroupID: f.group, Port: weightEndpointPort, Weight: new(100), WeightEditable: true},
				},
				Checks: []provider.LoadBalancerBackendCheck{
					{Key: target, GroupID: f.group, Port: weightEndpointPort, ListenerID: "listener", Status: "unhealthy"},
					{Key: sibling, GroupID: f.group, Port: weightEndpointPort, ListenerID: "listener", Status: "healthy"},
				},
			},
		}, nil
	}
	request := provider.LoadBalancerConfigurationRequest{ID: f.id, IncludeBackends: true}
	if err := inv.RefreshLoadBalancerConfiguration(ctx, k, request, 1, loadBalancerConfigurationReaderFunc(read)); err != nil {
		t.Fatal(err)
	}
	cfg, err := inv.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q)
	if err != nil {
		t.Fatal(err)
	}
	var key, sibling string
	for _, m := range cfg.Backends.Members {
		if *m.Weight == 0 {
			key = m.Key
		} else {
			sibling = m.Key
		}
	}
	if key == "" || sibling == "" {
		t.Fatal("missing backend configuration")
	}
	app, err := routers.NewAPI(db, "", nil)
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
	var enabled atomic.Bool
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			config := profile
			config.AllowLoadBalancerBackendDetach = enabled.Load()
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
			t.Error("backend detach worker did not stop")
		}
	}()
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	encode := func(member string, confirm bool) []byte {
		t.Helper()
		body, err := json.Marshal(operations.LoadBalancerBackendDetachInput{Account: f.vendor, Region: f.region, AssetID: f.id, TargetGroupID: f.group,
			MemberKey: member, ConfirmDetach: confirm})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	submit := func(project, requestKey, rev string, body []byte, want int) operations.Operation {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/"+project+"/load-balancers/detach-backend", bytes.NewReader(body))
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
			testsupport.DecodeProjectData(t, w, &op)
			if op.Action != "detach-load-balancer-backend" ||
				w.Header().Get("Location") != "/v1/tenants/tenant/projects/"+project+"/operations/"+op.ID {
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

	body := encode(key, true)
	submit("project", "unconfirmed", `"1"`, encode(key, false), http.StatusBadRequest)
	submit("project", "wrong-version", `"2"`, body, http.StatusPreconditionFailed)
	submit("other", "other-project", `"1"`, body, http.StatusForbidden)
	submit("project", "nonzero-member", `"1"`, encode(sibling, true), http.StatusPreconditionFailed)
	disabled := submit("project", "disabled", `"1"`, body, http.StatusAccepted)
	if _, err := store.Approve(ctx, lbConfigurationActor, scope, disabled.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self approval accepted: %v", err)
	}
	approve(disabled)
	wait(disabled, "failed")
	enabled.Store(true)
	drift := submit("project", "traffic-restored", `"1"`, body, http.StatusAccepted)
	f.weight.Store(20)
	approve(drift)
	wait(drift, "failed")
	f.weight.Store(0)
	shared := submit("project", "group-shared", `"1"`, body, http.StatusAccepted)
	f.wrongTarget.Store(true)
	approve(shared)
	wait(shared, "failed")
	f.wrongTarget.Store(false)
	if f.writes.Load() != 0 {
		t.Fatal("unavailable operation reached cloud mutation")
	}

	const idempotencyKey = "detach-test"
	var output bytes.Buffer
	if err := commands.RunOperationsClient(ctx, []string{"detach-load-balancer-backend", "tenant", "project", "--idempotency-key", idempotencyKey, "--if-match", "1"},
		local.URL, lbConfigurationActor, bytes.NewReader(body), &output, local.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var op operations.Operation
	if err := json.Unmarshal(output.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	if op.Status != "awaiting_approval" || submit("project", idempotencyKey, `"1"`, body, http.StatusAccepted).ID != op.ID {
		t.Fatalf("CLI and API diverged: %+v", op)
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
		t.Fatal("stale refresh did not reach provider")
	}
	f.dropReply.Store(f.vendor == provider.TencentCVM)
	approve(op)
	wait(op, "observing")
	waitPendingCloudObservation(t, db, op.ID)
	f.settled.Store(true)
	f.changedMemberAttributes.Store(true)
	stepBackendDetachEvidence(t, db, op.ID, 0)
	f.removed.Store(true)
	stepBackendDetachEvidence(t, db, op.ID, 1)
	f.memberListUnavailable.Store(true)
	stepBackendDetachEvidence(t, db, op.ID, 0)
	f.memberListUnavailable.Store(false)
	stepBackendDetachEvidence(t, db, op.ID, 1)
	op = wait(op, "succeeded")
	close(release)
	if err := <-result; !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("stale refresh restored removed binding: %v", err)
	}
	expectedReceipt := fixtureLoadBalancerReceipt
	if f.vendor == provider.HuaweiECS || f.dropReply.Load() {
		expectedReceipt = ""
	}
	if f.writes.Load() != 1 || op.ProviderRequestID != expectedReceipt || op.LoadBalancerBackendDetachIntent == nil ||
		op.LoadBalancerBackendDetachIntent.MemberKey != key {
		t.Fatalf("detachment proof or dispatch count inconsistent: %+v writes=%d", op, f.writes.Load())
	}
	cfg, err = inv.ReadLoadBalancerConfiguration(ctx, lbConfigurationActor, q)
	if err != nil || len(cfg.Backends.Members) != 1 || cfg.Backends.Members[0].Key != sibling ||
		len(cfg.Backends.Checks) != 1 || cfg.Backends.Checks[0].Key != sibling || len(cfg.TargetGroups) != 1 || len(cfg.Listeners) != 1 {
		t.Fatalf("detachment projection inconsistent: %+v %v", cfg, err)
	}
	var events int
	var binding string
	if err := db.QueryRowContext(ctx, `SELECT binding_key,(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='load-balancer.operation.succeeded')
  FROM cmp_load_balancer_backend_detach_intents WHERE operation_id=$1`, op.ID).Scan(&binding, &events); err != nil ||
		!provider.ValidLoadBalancerBackendKey(binding) || binding == key || events != 1 {
		t.Fatalf("detachment ledger inconsistent: %d %v", events, err)
	}
	encoded, err := json.Marshal(op)
	if err != nil || strings.Contains(string(encoded), f.member) || strings.Contains(string(encoded), weightEndpointMember) || strings.Contains(string(encoded), binding) {
		t.Fatal("operation published internal member identity")
	}
}

func stepBackendDetachEvidence(t *testing.T, db *sql.DB, id string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var previous int
	if err := db.QueryRowContext(ctx, `UPDATE cmp_operation_tasks SET available_at=clock_timestamp()
  WHERE id=$1 AND status='queued' RETURNING attempts`, id).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var attempts, observations int
		var status string
		if err := db.QueryRowContext(ctx, `SELECT t.attempts,t.status,i.absence_observations FROM cmp_operation_tasks AS t
    JOIN cmp_load_balancer_backend_detach_intents AS i ON t.id=i.operation_id WHERE t.id=$1`, id).Scan(&attempts, &status, &observations); err != nil {
			t.Fatal(err)
		}
		if attempts > previous && status != "leased" {
			if status != "queued" || observations != want {
				t.Fatalf("evidence prematurely finalized or mismatched: %s %d want %d", status, observations, want)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("next detach observation did not run")
		case <-ticker.C:
		}
	}
}
