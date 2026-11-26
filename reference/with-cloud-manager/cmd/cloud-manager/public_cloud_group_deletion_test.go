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

func TestAcceptancePublicCloudSecurityGroupDeletion(t *testing.T) {
	for _, cloud := range []struct{ vendor, account, region, group, vpc string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "sg-fixture", ""},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "sg-fixture", "vpc-fixture"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "40000000-0000-4000-8000-000000000020", ""},
	} {
		t.Run(cloud.vendor, func(t *testing.T) {
			t.Parallel()
			testPublicCloudSecurityGroupDeletion(t, &groupDeleteFixture{
				cloud: vpcCreationFixture{vendor: cloud.vendor, account: cloud.account, region: cloud.region},
				id:    cloud.group, vpc: cloud.vpc,
			})
		})
	}
}

func testPublicCloudSecurityGroupDeletion(t *testing.T, f *groupDeleteFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-group-delete-operator-token", "fixture-group-delete-reviewer-token"
	const ruleID, otherRuleID = "fixture-group-rule", "fixture-other-group-rule"
	cloud := &f.cloud
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cloud.vendor, region: cloud.region,
		volume: "source-volume", id: "source-snapshot", class: "standard"}, cloud.remoteIdentity(), actor, reviewer)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant',$1,$2,kind,1,now() FROM unnest(ARRAY['network','security-group','security-group-rule','instance']) AS kind`,
		cloud.vendor, cloud.region)
	if f.vpc != "" {
		exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,$1,'parent VPC','available',1 FROM cmp_inventory_scopes WHERE kind='network'`, f.vpc)
	}
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links)
		SELECT scope_id,$1,'test group','available',1,CASE WHEN $2::text='' THEN '[]'::jsonb ELSE
		  jsonb_build_array(jsonb_build_object('kind','network','id',$2::text,'relation','in_network')) END
		FROM cmp_inventory_scopes WHERE kind='security-group'`, f.id, f.vpc)
	addRule := func(rule, group string) {
		t.Helper()
		exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links)
			SELECT scope_id,$1,'ingress tcp 443-443 192.0.2.0/24','available',1,
				jsonb_build_array(jsonb_build_object('kind','security-group','id',$2::text,'relation','in_security_group'))
			FROM cmp_inventory_scopes WHERE kind='security-group-rule'`, rule, group)
	}
	addRule(ruleID, f.id)
	addRule(otherRuleID, "other-group")

	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.serve(t, w, r)
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
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	var enabled atomic.Bool
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if binding.Provider != cloud.vendor || binding.RemoteIdentity != cloud.remoteIdentity() {
				return nil, identity.ErrForbidden
			}
			profile := config
			profile.AllowSecurityGroupDeletion = enabled.Load()
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
			t.Error("security group deletion worker did not stop")
		}
	}()

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/tenants/tenant/projects/"
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	confirm := true
	input := operations.NetworkDeletion{Account: cloud.vendor, Region: cloud.region, AssetID: f.id, Kind: provider.SecurityGroup,
		ConfirmRuleRemoval: &confirm}
	submit := func(project, key, revision string, request operations.NetworkDeletion, want int) operations.Operation {
		t.Helper()
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
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
			if op.AssetID != f.id || op.Action != "delete-security-group" || op.ResourceKind != provider.SecurityGroup ||
				w.Header().Get("Location") != base+project+"/network-operations/"+op.ID {
				t.Fatalf("wrong group deletion receipt: %+v", op)
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

	withoutConsent := input
	withoutConsent.ConfirmRuleRemoval = nil
	submit("project", "missing-confirmation", `"1"`, withoutConsent, http.StatusBadRequest)
	submit("other", "wrong-project", `"1"`, input, http.StatusForbidden)
	submit("project", "stale-group", `"2"`, input, http.StatusPreconditionFailed)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links)
		SELECT scope_id,'linked-instance','uses group','stopped',1,
			jsonb_build_array(jsonb_build_object('kind','security-group','id',$1::text,'relation','uses_security_group'))
		FROM cmp_inventory_scopes WHERE kind='instance'`, f.id)
	submit("project", "blocks-linked-instance", `"1"`, input, http.StatusConflict)
	exec(`UPDATE cmp_inventory_objects SET removed_at=now() WHERE external_id='linked-instance'`)

	rule := provider.SecurityRule{Direction: "ingress", Protocol: "tcp", FromPort: 443, ToPort: 443, RemoteCIDR: "192.0.2.0/24"}
	newRule := operations.NetworkInput{Account: cloud.vendor, Region: cloud.region, Kind: provider.SecurityGroupRule,
		NetworkSpec: provider.NetworkSpec{ParentID: f.id, Rule: &rule}}
	revoke := operations.RuleRevocation{Account: cloud.vendor, Region: cloud.region, ParentID: f.id, RuleID: ruleID}
	pending, err := store.SubmitNetworkCreation(ctx, actor, scope, "rule-before-delete", 1, newRule)
	if err != nil {
		t.Fatal(err)
	}
	submit("project", "blocks-pending-rule", `"1"`, input, http.StatusConflict)
	approve(pending, "reject")
	pending, err = store.SubmitSecurityRuleRevocation(ctx, actor, scope, "revoke-before-delete", 1, revoke)
	if err != nil {
		t.Fatal(err)
	}
	submit("project", "blocks-pending-revocation", `"1"`, input, http.StatusConflict)
	approve(pending, "reject")

	disabled := submit("project", "disabled", `"1"`, input, http.StatusAccepted)
	approve(disabled, "approve")
	if result := wait(disabled, "failed"); result.OutcomeReason != "network_deletion_disabled" || f.reads.Load() != 0 || f.writes.Load() != 0 {
		t.Fatalf("disabled group deletion: %+v", result)
	}
	enabled.Store(true)
	for _, block := range []struct {
		mode int32
		key  string
	}{{groupDeleteProtected, "protected"}, {groupDeleteInUse, "in-use"}, {groupDeleteReferenced, "referenced"}} {
		f.mode.Store(block.mode)
		op := submit("project", block.key, `"1"`, input, http.StatusAccepted)
		approve(op, "approve")
		if result := wait(op, "failed"); result.OutcomeReason != "network_deletion_preconditions_unconfirmed" || f.writes.Load() != 0 {
			t.Fatalf("group %s did not fail closed: %+v", block.key, result)
		}
	}
	f.mode.Store(0)
	op := submit("project", "delete", `"1"`, input, http.StatusAccepted)
	if _, err := store.SubmitNetworkCreation(ctx, actor, scope, "rule-during-delete", 1, newRule); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("allowed rule creation during group deletion: %v", err)
	}
	if _, err := store.SubmitSecurityRuleRevocation(ctx, actor, scope, "revoke-during-delete", 1, revoke); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("allowed rule revocation during group deletion: %v", err)
	}
	detail := wait(op, "awaiting_approval")
	if info := detail.NetworkDeletionIntent; info == nil || !info.RuleRemovalConfirmed || info.OwnershipRevision != 1 ||
		info.ParentID != f.vpc || (f.vpc != "" && info.ParentRevision != 1) {
		t.Fatalf("group deletion detail: %+v", detail)
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
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
	if f.writes.Load() != 1 || review.OutcomeReason != "network_deletion_requires_evidence" {
		t.Fatalf("invalid evidence during group deletion: %+v, writes=%d", review, f.writes.Load())
	}
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
	if _, err := store.SubmitNetworkCreation(ctx, actor, scope, "rule-during-review", 1, newRule); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("allowed rule creation during group review: %v", err)
	}
	assertSnapshotTombstone(t, db, f.id, false)
	assertSnapshotTombstone(t, db, ruleID, false)
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "incident/security-group"}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("accepted unverified group deletion: %v", err)
	}
	f.mode.Store(groupDeleteAbsent)
	enabled.Store(false)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "incident/security-group"}); err != nil {
		t.Fatal(err)
	}
	if result := wait(op, "succeeded"); result.OutcomeReason != "network_absence_confirmed" || f.writes.Load() != 1 || f.absences.Load() != 2 {
		t.Fatalf("group deletion recovery: %+v, writes=%d absence=%d", result, f.writes.Load(), f.absences.Load())
	}
	assertSnapshotTombstone(t, db, f.id, true)
	assertSnapshotTombstone(t, db, ruleID, true)
	assertSnapshotTombstone(t, db, otherRuleID, false)
	inv := inventory.NewStore(db)
	key := inventory.Key{Tenant: scope.Tenant, Account: cloud.vendor, Region: cloud.region, Kind: provider.SecurityGroupRule}
	claim, err := inv.Claim(ctx, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	const unseen = "fixture-late-unseen-rule"
	stale := imageDiscovery(func() provider.Page {
		return provider.Page{Objects: []provider.Object{rule.Asset(ruleID, f.id), rule.Asset(unseen, f.id), rule.Asset(otherRuleID, "other-group")}}
	})
	if _, complete, err := inv.ScanPage(ctx, claim, stale); err != nil || !complete {
		t.Fatalf("late rules scan: complete=%t err=%v", complete, err)
	}
	assertSnapshotTombstone(t, db, ruleID, true)
	assertSnapshotTombstone(t, db, otherRuleID, false)
	var unseenCount, events, reservations int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM cmp_inventory_objects WHERE external_id=$1),
		(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='security-group.operation.succeeded'),
		(SELECT count(*) FROM cmp_network_deletion_reservations WHERE operation_id=$2)`, unseen, op.ID).
		Scan(&unseenCount, &events, &reservations); err != nil || unseenCount != 0 || events != 1 || reservations != 0 {
		t.Fatalf("late rule, outbox, or reservation mismatch: unseen=%d events=%d reservations=%d err=%v", unseenCount, events, reservations, err)
	}
	if replay := submit("project", "delete", `"1"`, input, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("replayed request created a new operation: %+v", replay)
	}
}

const (
	groupDeleteProtected int32 = iota + 1
	groupDeleteInUse
	groupDeleteReferenced
	groupDeleteAbsent
)

type groupDeleteFixture struct {
	cloud    vpcCreationFixture
	id, vpc  string
	mode     atomic.Int32
	writes   atomic.Int32
	reads    atomic.Int32
	absences atomic.Int32
}

func (f *groupDeleteFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
		f.cloud.serve(t, w, r)
		return
	}
	var handled bool
	switch f.cloud.vendor {
	case provider.TencentCVM:
		handled = f.serveTencent(t, w, r)
	case provider.AlibabaECS:
		handled = f.serveAlibaba(t, w, r)
	case provider.HuaweiECS:
		handled = f.serveHuawei(t, w, r)
	}
	if !handled {
		t.Errorf("unexpected group deletion request: %s %s %s", r.Method, r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *groupDeleteFixture) groupRows(row map[string]any) []map[string]any {
	f.reads.Add(1)
	if f.writes.Load() == 0 {
		return []map[string]any{row}
	}
	if f.mode.Load() != groupDeleteAbsent {
		return nil
	}
	f.absences.Add(1)
	return []map[string]any{}
}

func (f *groupDeleteFixture) serveTencent(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "vpc.tencentcloudapi.com" || r.Method != http.MethodPost ||
		r.Header.Get("X-TC-Region") != f.cloud.region || r.Header.Get("X-TC-Version") != "2017-03-12" ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var payload struct {
		Limit, SecurityGroupId string
		SecurityGroupIds       []string
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	var response map[string]any
	switch r.Header.Get("X-TC-Action") {
	case "DescribeSecurityGroups":
		if payload.Limit != "2" || !singleTarget(payload.SecurityGroupIds, f.id) {
			return false
		}
		rows := f.groupRows(map[string]any{"SecurityGroupId": f.id, "SecurityGroupName": "test group",
			"Region": f.cloud.region, "IsDefault": f.mode.Load() == groupDeleteProtected})
		response = map[string]any{"TotalCount": len(rows), "SecurityGroupSet": rows}
	case "DescribeSecurityGroupAssociationStatistics":
		if !singleTarget(payload.SecurityGroupIds, f.id) {
			return false
		}
		row := map[string]any{"SecurityGroupId": f.id, "TotalCount": 0, "SG": 0, "ENI": 0}
		switch f.mode.Load() {
		case groupDeleteInUse:
			row["ENI"] = 1
		case groupDeleteReferenced:
			row["SG"] = 1
		}
		response = map[string]any{"SecurityGroupAssociationStatisticsSet": []map[string]any{row}}
	case "DeleteSecurityGroup":
		if payload.SecurityGroupId != f.id || len(payload.SecurityGroupIds) != 0 {
			return false
		}
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	default:
		return false
	}
	return sendNetworkFixture(t, w, map[string]any{"Response": response})
}

func (f *groupDeleteFixture) serveAlibaba(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	q := r.URL.Query()
	if r.Host != "ecs."+f.cloud.region+".aliyuncs.com" || r.Method != http.MethodPost || q.Get("RegionId") != f.cloud.region ||
		r.Header.Get("X-Acs-Version") != "2014-05-26" || r.Header.Get("X-Acs-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var reply map[string]any
	switch r.Header.Get("X-Acs-Action") {
	case "DescribeSecurityGroups":
		if q.Get("SecurityGroupId") != f.id || q.Has("VpcId") || q.Get("MaxResults") != "10" {
			return false
		}
		groupType := "enterprise"
		if f.mode.Load() == groupDeleteProtected {
			groupType = "normal"
		}
		rows := f.groupRows(map[string]any{"SecurityGroupId": f.id, "VpcId": f.vpc, "SecurityGroupName": "test group",
			"SecurityGroupType": groupType, "ServiceManaged": false})
		reply = map[string]any{"RegionId": f.cloud.region, "SecurityGroups": map[string]any{"SecurityGroup": rows}}
	case "DescribeSecurityGroupReferences":
		if q.Get("SecurityGroupId.1") != f.id {
			return false
		}
		refs := []map[string]any{}
		if f.mode.Load() == groupDeleteReferenced {
			refs = append(refs, map[string]any{"SecurityGroupId": "sg-another", "AliUid": f.cloud.account})
		}
		reply = map[string]any{"SecurityGroupReferences": map[string]any{"SecurityGroupReference": []map[string]any{{
			"SecurityGroupId": f.id, "ReferencingSecurityGroups": map[string]any{"ReferencingSecurityGroup": refs},
		}}}}
	case "DescribeInstances":
		if q.Get("SecurityGroupId") != f.id || q.Get("MaxResults") != "10" {
			return false
		}
		reply = map[string]any{"Instances": map[string]any{"Instance": []map[string]any{}}}
	case "DescribeNetworkInterfaces":
		if q.Get("SecurityGroupId") != f.id || q.Get("MaxResults") != "10" {
			return false
		}
		enis := []map[string]any{}
		if f.mode.Load() == groupDeleteInUse {
			enis = append(enis, map[string]any{"NetworkInterfaceId": "eni-test"})
		}
		reply = map[string]any{"NetworkInterfaceSets": map[string]any{"NetworkInterfaceSet": enis}}
	case "DeleteSecurityGroup":
		if q.Get("SecurityGroupId") != f.id || len(q) != 2 {
			return false
		}
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	default:
		return false
	}
	return sendNetworkFixture(t, w, reply)
}

func (f *groupDeleteFixture) serveHuawei(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Host != "vpc."+f.cloud.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	base := "/v3/" + fixtureHuaweiProject + "/vpc/security-groups"
	if r.Method == http.MethodDelete && r.URL.Path == base+"/"+f.id && r.URL.RawQuery == "" {
		f.writes.Add(1)
		return dropImageCreationReply(t, w)
	}
	if r.Method != http.MethodGet {
		return false
	}
	q := r.URL.Query()
	var reply map[string]any
	switch r.URL.Path {
	case base:
		if q.Get("limit") != "2" || q.Get("id") != f.id {
			return false
		}
		name := "test group"
		if f.mode.Load() == groupDeleteProtected {
			name = "default"
		}
		rows := f.groupRows(map[string]any{"id": f.id, "project_id": fixtureHuaweiProject, "name": name})
		reply = map[string]any{"security_groups": rows, "page_info": map[string]any{"current_count": len(rows)}}
	case "/v1/" + fixtureHuaweiProject + "/ports":
		if q.Get("security_groups") != f.id || q.Get("limit") != "1" {
			return false
		}
		ports := []map[string]any{}
		if f.mode.Load() == groupDeleteInUse {
			ports = append(ports, map[string]any{"id": "40000000-0000-4000-8000-000000000061"})
		}
		reply = map[string]any{"ports": ports}
	case "/v3/" + fixtureHuaweiProject + "/vpc/security-group-rules":
		if q.Get("remote_group_id") != f.id || q.Get("limit") != "100" {
			return false
		}
		const first = "40000000-0000-4000-8000-000000000062"
		const second = "40000000-0000-4000-8000-000000000063"
		id, group := first, f.id
		page := map[string]any{"current_count": 1, "next_marker": first}
		switch q.Get("marker") {
		case "":
			if f.mode.Load() == groupDeleteReferenced {
				group = "40000000-0000-4000-8000-000000000064"
			}
		case first:
			id = second
			delete(page, "next_marker")
		default:
			return false
		}
		reply = map[string]any{"security_group_rules": []map[string]any{{
			"id": id, "project_id": fixtureHuaweiProject, "security_group_id": group, "remote_group_id": f.id,
		}}, "page_info": page}
	default:
		return false
	}
	return sendNetworkFixture(t, w, reply)
}
