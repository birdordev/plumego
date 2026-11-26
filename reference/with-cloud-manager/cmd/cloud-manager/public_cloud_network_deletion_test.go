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

func TestAcceptancePublicCloudNetworkDeletion(t *testing.T) {
	for _, cloud := range []struct{ vendor, account, region, vpc, subnet string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "vpc-fixture", "subnet-fixture"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "vpc-fixture", "vsw-fixture"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4",
			"40000000-0000-4000-8000-000000000010", "40000000-0000-4000-8000-000000000020"},
	} {
		for _, kind := range []provider.Kind{provider.Network, provider.Subnet} {
			t.Run(cloud.vendor+"/"+string(kind), func(t *testing.T) {
				t.Parallel()
				testPublicCloudNetworkDeletion(t, &networkDeleteFixture{
					cloud: vpcCreationFixture{vendor: cloud.vendor, account: cloud.account, region: cloud.region},
					vpc:   cloud.vpc, subnet: cloud.subnet, kind: kind,
				})
			})
		}
	}
}

func testPublicCloudNetworkDeletion(t *testing.T, fixture *networkDeleteFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	const actor, reviewer = "fixture-network-delete-operator-token", "fixture-network-delete-reviewer-token"
	cloud := &fixture.cloud
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cloud.vendor, region: cloud.region,
		volume: "source-volume", id: "source-snapshot", class: "standard"}, cloud.remoteIdentity(), actor, reviewer)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant',$1,$2,kind,1,now() FROM unnest(ARRAY['network','subnet','instance']) AS kind`, cloud.vendor, cloud.region)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$1,'sample VPC','available',1 FROM cmp_inventory_scopes WHERE kind='network'`, fixture.vpc)
	id, action := fixture.vpc, "delete-network"
	if fixture.kind == provider.Subnet {
		id, action = fixture.subnet, "delete-subnet"
		exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links)
			SELECT scope_id,$1,'sample subnet','available',1,
				jsonb_build_array(jsonb_build_object('kind','network','id',$2::text,'relation','in_network'))
			FROM cmp_inventory_scopes WHERE kind='subnet'`, fixture.subnet, fixture.vpc)
	}

	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.serve(t, w, r)
	}))
	defer remote.Close()
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	config := publiccloud.Config{AccountID: cloud.account, Regions: []string{cloud.region}}
	if cloud.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	store := operations.NewStore(db)
	subnetInput := operations.NetworkInput{
		Kind: provider.Subnet, Account: cloud.vendor, Region: cloud.region,
		NetworkSpec: provider.NetworkSpec{Name: "new-subnet", CIDR: "10.1.1.0/24", ParentID: fixture.vpc, Zone: cloud.region + "-a"},
	}
	exec("INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','create-subnet')")
	if fixture.kind == provider.Subnet {
		original, err := store.SubmitNetworkCreation(ctx, actor, scope, "original-subnet", 1, subnetInput)
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE cmp_instance_operations SET status='succeeded',scope_id=s.scope_id,external_id=$2
			FROM cmp_inventory_scopes AS s WHERE operation_id=$1 AND s.kind='subnet'`, original.ID, id)
		if _, err := store.SubmitNetworkCreation(ctx, actor, scope, "overlaps-before-delete", 1, subnetInput); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("overlap before confirmed deletion: %v", err)
		}
	}
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	var enabled atomic.Bool
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if binding.Provider != cloud.vendor || binding.RemoteIdentity != cloud.remoteIdentity() {
				return nil, identity.ErrForbidden
			}
			profile := config
			targetEnabled := enabled.Load()
			profile.AllowVPCDeletion = (fixture.kind == provider.Network) == targetEnabled
			profile.AllowSubnetDeletion = (fixture.kind == provider.Subnet) == targetEnabled
			return publiccloud.Connect(ctx, binding.Provider, profile, cloudFixtureTransport{endpoint, remote.Client().Transport})
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
			t.Error("network deletion worker did not stop")
		}
	}()

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(operations.NetworkDeletion{Account: cloud.vendor, Region: cloud.region, AssetID: id, Kind: fixture.kind})
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/tenants/tenant/projects/"
	submit := func(project, key, revision string, want int) operations.Operation {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, base+project+"/network-operations/delete", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: status %d, want %d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.AssetID != id || op.Action != action || op.ResourceKind != fixture.kind ||
				w.Header().Get("Location") != base+project+"/network-operations/"+op.ID {
				t.Fatalf("wrong network deletion receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation, decision string) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: decision}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, status, true)
	}

	submit("other", "wrong-project", `"1"`, http.StatusForbidden)
	submit("project", "stale-network", `"2"`, http.StatusPreconditionFailed)
	submit("project", "missing-revision", "", http.StatusPreconditionFailed)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,missing_generation,links)
		SELECT scope_id,'linked-instance','uses network','stopped',1,2,
			jsonb_build_array(jsonb_build_object('kind',$1::text,'id',$2::text,'relation','fixture'))
		FROM cmp_inventory_scopes WHERE kind='instance'`, fixture.kind, id)
	submit("project", "blocked-by-missing-candidate", `"1"`, http.StatusConflict)
	exec(`UPDATE cmp_inventory_objects SET removed_at=now() WHERE external_id='linked-instance'`)
	if fixture.kind == provider.Network {
		pending, err := store.SubmitNetworkCreation(ctx, actor, scope, "pending-subnet", 1, subnetInput)
		if err != nil {
			t.Fatal(err)
		}
		submit("project", "blocked-by-pending-subnet", `"1"`, http.StatusConflict)
		approve(pending, "reject")
	}

	disabled := submit("project", "disabled", `"1"`, http.StatusAccepted)
	approve(disabled, "approve")
	if result := wait(disabled, "failed"); result.OutcomeReason != "network_deletion_disabled" || fixture.reads.Load() != 0 || fixture.writes.Load() != 0 {
		t.Fatalf("disabled network deletion: %+v", result)
	}
	enabled.Store(true)
	fixture.blocked.Store(true)
	blocked := submit("project", "blocked-at-provider", `"1"`, http.StatusAccepted)
	approve(blocked, "approve")
	if result := wait(blocked, "failed"); result.OutcomeReason != "network_deletion_preconditions_unconfirmed" || fixture.writes.Load() != 0 {
		t.Fatalf("unsafe network deletion: %+v", result)
	}
	fixture.blocked.Store(false)

	op := submit("project", "delete", `"1"`, http.StatusAccepted)
	if cloud.vendor == provider.TencentCVM && fixture.kind == provider.Network {
		// 复现上线前账本中已有一条活动 VPC 删除申请。
		exec(`DROP TRIGGER cmp_network_deletion_reservation ON cmp_instance_operations;
			DROP FUNCTION cmp_update_network_deletion_reservation();
			ALTER TABLE cmp_network_deletion_reservations DROP CONSTRAINT cmp_network_deletion_operation_fk;
			DELETE FROM cmp_network_deletion_reservations;
			DELETE FROM cmp_migrations WHERE version='0055_network_deletion_backfill';`)
		if err := migrateCloudManager(ctx, db); err != nil {
			t.Fatal(err)
		}
		var reserved bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_network_deletion_reservations WHERE operation_id=$1)`,
			op.ID).Scan(&reserved); err != nil || !reserved {
			t.Fatalf("upgrade failed to reserve an in-flight VPC deletion: reserved=%t err=%v", reserved, err)
		}
	}
	if fixture.kind == provider.Network {
		if _, err := store.SubmitNetworkCreation(ctx, actor, scope, "child-during-delete", 1, subnetInput); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("allowed child creation during VPC deletion: %v", err)
		}
	}
	submit("project", "duplicate", `"1"`, http.StatusConflict)
	if _, err := store.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("operator approved own deletion: %v", err)
	}
	detail := wait(op, "awaiting_approval")
	wantParent := ""
	if fixture.kind == provider.Subnet {
		wantParent = fixture.vpc
	}
	if info := detail.NetworkDeletionIntent; info == nil || info.OwnershipRevision != 1 || info.ParentID != wantParent ||
		(wantParent != "" && info.ParentRevision != 1) {
		t.Fatalf("incomplete approval detail: %+v", detail)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var reply bytes.Buffer
	args := []string{"delete-network", "tenant", "project", "--idempotency-key", "delete", "--if-match", "1"}
	if err := runOperationsClient(ctx, args, server.URL, actor, bytes.NewReader(body), &reply, server.Client().Transport); err != nil || !strings.Contains(reply.String(), op.ID) {
		t.Fatalf("CLI replay: %v %s", err, &reply)
	}

	approve(op, "approve")
	review := wait(op, "needs_review")
	if review.OutcomeReason != "network_deletion_requires_evidence" || fixture.writes.Load() != 1 || fixture.absences.Load() != 1 {
		t.Fatalf("invalid response did not invalidate first absence observation: %+v writes=%d absence=%d",
			review, fixture.writes.Load(), fixture.absences.Load())
	}
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
	assertSnapshotTombstone(t, db, id, false)
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "incident/network"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("accepted success without evidence: %v", err)
	}
	enabled.Store(false)
	fixture.confirm.Store(true)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "incident/network"}); err != nil {
		t.Fatal(err)
	}
	if confirmed := wait(op, "succeeded"); confirmed.OutcomeReason != "network_absence_confirmed" ||
		fixture.writes.Load() != 1 || fixture.absences.Load() != 3 {
		t.Fatalf("network deletion did not converge with read-only recovery: %+v writes=%d absence=%d",
			confirmed, fixture.writes.Load(), fixture.absences.Load())
	}
	inventoryStore := inventory.NewStore(db)
	stale := provider.Object{ID: id, Name: "late inventory result", Status: "available"}
	stale.Link(provider.Network, wantParent, "in_network")
	claim, err := inventoryStore.Claim(ctx, inventory.Key{Tenant: scope.Tenant, Account: cloud.vendor, Region: cloud.region, Kind: fixture.kind}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, complete, err := inventoryStore.ScanPage(ctx, claim, staleAssetDiscovery{stale}); err != nil || !complete {
		t.Fatalf("late inventory scan: complete=%t err=%v", complete, err)
	}
	assertSnapshotTombstone(t, db, id, true)
	if wantParent != "" {
		assertSnapshotTombstone(t, db, wantParent, false)
		if _, err := store.SubmitNetworkCreation(ctx, actor, scope, "reuses-cidr-after-delete", 1, subnetInput); err != nil {
			t.Fatalf("confirmed subnet deletion did not release its original CIDR: %v", err)
		}
	}
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type=$1`,
		string(fixture.kind)+".operation.succeeded").Scan(&events); err != nil || events != 1 {
		t.Fatalf("network deletion outbox: count=%d err=%v", events, err)
	}
	if replay := submit("project", "delete", `"1"`, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("replay after deletion: %+v", replay)
	}
}

type networkDeleteFixture struct {
	cloud       vpcCreationFixture
	vpc, subnet string
	kind        provider.Kind
	blocked     atomic.Bool
	confirm     atomic.Bool
	writes      atomic.Int32
	reads       atomic.Int32
	absences    atomic.Int32
}

func (f *networkDeleteFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
		f.cloud.serve(t, w, r)
		return
	}
	if !f.serveNetwork(t, w, r) {
		t.Errorf("unexpected network deletion request: %s %s %s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *networkDeleteFixture) serveNetwork(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	switch f.cloud.vendor {
	case provider.TencentCVM:
		return f.serveTencent(t, w, r)
	case provider.AlibabaECS:
		return f.serveAlibaba(t, w, r)
	default:
		return f.serveHuawei(t, w, r)
	}
}

// 先返回一次空列表，再返回畸形数据，模拟网络删除回执丢失后的证据中断。
func (f *networkDeleteFixture) networkRows(childList bool, row map[string]any) []map[string]any {
	f.reads.Add(1)
	if childList {
		if f.blocked.Load() {
			return []map[string]any{row}
		}
		return []map[string]any{}
	}
	if f.writes.Load() == 0 {
		return []map[string]any{row}
	}
	if f.confirm.Load() || f.absences.CompareAndSwap(0, 1) {
		if f.confirm.Load() {
			f.absences.Add(1)
		}
		return []map[string]any{}
	}
	return nil
}

func (f *networkDeleteFixture) serveTencent(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "vpc.tencentcloudapi.com" || r.Method != http.MethodPost ||
		r.Header.Get("X-TC-Region") != f.cloud.region || r.Header.Get("X-TC-Version") != "2017-03-12" ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var input struct {
		Limit, VpcId, SubnetId string
		VpcIds, SubnetIds      []string
		Filters                []struct {
			Name   string
			Values []string
		}
	}
	if json.Unmarshal(body, &input) != nil {
		return false
	}
	var rows []map[string]any
	var field string
	switch r.Header.Get("X-TC-Action") {
	case "DescribeVpcs":
		if f.kind != provider.Network || !singleTarget(input.VpcIds, f.vpc) || len(input.Filters) != 0 {
			return false
		}
		field = "VpcSet"
		rows = f.networkRows(false, map[string]any{"VpcId": f.vpc, "IsDefault": false})
	case "DescribeSubnets":
		childList := f.kind == provider.Network
		if childList {
			if len(input.SubnetIds) != 0 || len(input.Filters) != 1 ||
				input.Filters[0].Name != "vpc-id" || !singleTarget(input.Filters[0].Values, f.vpc) {
				return false
			}
		} else if !singleTarget(input.SubnetIds, f.subnet) || len(input.Filters) != 0 {
			return false
		}
		field = "SubnetSet"
		rows = f.networkRows(childList, map[string]any{"SubnetId": f.subnet, "VpcId": f.vpc, "IsDefault": f.blocked.Load()})
	case "DeleteVpc":
		if f.kind != provider.Network || input.VpcId != f.vpc || len(input.Filters) != 0 || input.SubnetId != "" {
			return false
		}
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	case "DeleteSubnet":
		if f.kind != provider.Subnet || input.SubnetId != f.subnet || len(input.Filters) != 0 || input.VpcId != "" {
			return false
		}
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	default:
		return false
	}
	if input.Limit != "2" {
		return false
	}
	return sendNetworkFixture(t, w, map[string]any{"Response": map[string]any{"TotalCount": len(rows), field: rows}})
}

func (f *networkDeleteFixture) serveAlibaba(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	q := r.URL.Query()
	if r.Host != "vpc.aliyuncs.com" || r.Method != http.MethodPost || q.Get("RegionId") != f.cloud.region ||
		r.Header.Get("X-Acs-Version") != "2016-04-28" || r.Header.Get("X-Acs-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var rows []map[string]any
	var container, field string
	switch r.Header.Get("X-Acs-Action") {
	case "DescribeVpcs":
		if f.kind != provider.Network || q.Get("VpcId") != f.vpc || q.Get("VpcOwnerId") != f.cloud.account {
			return false
		}
		container, field = "Vpcs", "Vpc"
		rows = f.networkRows(false, map[string]any{"VpcId": f.vpc, "RegionId": f.cloud.region, "Status": "Available",
			"OwnerId": json.Number(f.cloud.account), "IsDefault": false})
	case "DescribeVSwitches":
		childList := f.kind == provider.Network
		if (childList && (q.Get("VpcId") != f.vpc || q.Has("VSwitchId") || q.Has("VSwitchOwnerId"))) ||
			(!childList && (q.Get("VSwitchId") != f.subnet || q.Has("VpcId"))) {
			return false
		}
		container, field = "VSwitches", "VSwitch"
		rows = f.networkRows(childList, map[string]any{"VSwitchId": f.subnet, "VpcId": f.vpc, "Status": "Available",
			"OwnerId": json.Number(f.cloud.account), "IsDefault": f.blocked.Load()})
	case "DeleteVpc":
		if f.kind != provider.Network || q.Get("VpcId") != f.vpc || q.Get("ForceDelete") != "false" || q.Has("VSwitchId") {
			return false
		}
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	case "DeleteVSwitch":
		if f.kind != provider.Subnet || q.Get("VSwitchId") != f.subnet || q.Has("ForceDelete") || q.Has("VpcId") {
			return false
		}
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	default:
		return false
	}
	if q.Get("PageNumber") != "1" || q.Get("PageSize") != "10" {
		return false
	}
	return sendNetworkFixture(t, w, map[string]any{"TotalCount": len(rows), "PageNumber": 1, "PageSize": 10,
		container: map[string]any{field: rows}})
}

func (f *networkDeleteFixture) serveHuawei(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Host != "vpc."+f.cloud.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	if r.Method == http.MethodDelete {
		want := "/v1/" + fixtureHuaweiProject + "/vpcs/" + f.vpc
		if f.kind == provider.Subnet {
			want += "/subnets/" + f.subnet
		}
		if r.URL.Path != want || r.URL.RawQuery != "" {
			return false
		}
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	}
	q := r.URL.Query()
	if r.Method != http.MethodGet || q.Get("limit") != "2" {
		return false
	}
	var rows []map[string]any
	var field string
	switch r.URL.Path {
	case "/v3/" + fixtureHuaweiProject + "/vpc/vpcs":
		if f.kind != provider.Network || q.Get("id") != f.vpc {
			return false
		}
		field = "vpcs"
		rows = f.networkRows(false, map[string]any{"id": f.vpc, "project_id": fixtureHuaweiProject, "status": "ACTIVE"})
	case "/v3/" + fixtureHuaweiProject + "/vpc/virsubnets":
		childList := f.kind == provider.Network
		if (childList && (q.Get("vpc_id") != f.vpc || q.Has("id"))) || (!childList && (q.Get("id") != f.subnet || q.Has("vpc_id"))) {
			return false
		}
		field = "virsubnets"
		status := "ACTIVE"
		if f.blocked.Load() {
			status = "UNKNOWN"
		}
		rows = f.networkRows(childList, map[string]any{"id": f.subnet, "project_id": fixtureHuaweiProject, "vpc_id": f.vpc, "status": status})
	default:
		return false
	}
	return sendNetworkFixture(t, w, map[string]any{"page_info": map[string]any{"current_count": len(rows)}, field: rows})
}

func singleTarget(values []string, id string) bool {
	return len(values) == 1 && values[0] == id
}

func sendNetworkFixture(t *testing.T, w http.ResponseWriter, response map[string]any) bool {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Error(err)
	}
	return true
}
