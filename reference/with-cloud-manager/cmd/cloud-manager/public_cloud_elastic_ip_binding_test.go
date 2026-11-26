package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

type elasticIPBindingFixture struct {
	instance, other, port, network string
	running, bound, wrong, cascade atomic.Bool
	changes                        atomic.Int32
}

func newElasticIPBindingFixture(vendor string) *elasticIPBindingFixture {
	switch vendor {
	case provider.TencentCVM:
		return &elasticIPBindingFixture{instance: "ins-primary", other: "ins-unapproved", network: "vpc-network"}
	case provider.AlibabaECS:
		return &elasticIPBindingFixture{instance: "i-primary", other: "i-unapproved", network: "vpc-network"}
	default:
		return &elasticIPBindingFixture{instance: "30000000-0000-4000-8000-000000000010", other: "30000000-0000-4000-8000-000000000011",
			port: "30000000-0000-4000-8000-000000000012", network: "30000000-0000-4000-8000-000000000013"}
	}
}

func (f *vpcCreationFixture) exerciseElasticIPBinding(t *testing.T, db *sql.DB, store operations.Store, app http.Handler,
	scope identity.Scope, actor, reviewer string, enabled *atomic.Bool) {
	t.Helper()
	ctx := t.Context()
	b := f.binding
	b.running.Store(true)
	defer b.running.Store(false)
	defer enabled.Store(false)

	exec := func(ctx context.Context, query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(ctx, "INSERT INTO cmp_projects VALUES ('tenant','other')")
	exec(ctx, `WITH scoped AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'instance',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,asset,'running',1 FROM scoped CROSS JOIN (VALUES ($3::text),($4::text)) AS ids(asset)`,
		f.vendor, f.region, b.instance, b.other)
	exec(ctx, `INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
		SELECT 'tenant',scope_id,$2,'other',2 FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='instance'`, f.vendor, b.other)
	exec(ctx, `INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
		SELECT 'tenant',scope_id,$2,'project',3 FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='instance'`, f.vendor, b.instance)
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT a.revision FROM cmp_asset_ownership AS a JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (s.account_id,s.kind,a.external_id)=($1,'elastic-ip',$2)`, f.vendor, f.second).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	match := strconv.Quote(strconv.FormatInt(revision, 10))
	const bind, unbind = "bind-elastic-ip", "unbind-elastic-ip"
	base := operations.ElasticIPBinding{ElasticIPRelease: operations.ElasticIPRelease{
		Account: f.vendor, Region: f.region, AssetID: f.second, PublicAddress: fixtureAllocatedSecondIP,
	}, InstanceID: b.instance, InstanceRevision: 3, PortID: b.port}

	submit := func(action, key string, input operations.ElasticIPBinding, want int) operations.Operation {
		t.Helper()
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		route := "/v1/tenants/tenant/projects/project/network-operations"
		r := httptest.NewRequest(http.MethodPost, route+"/"+action, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", match)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s returned %d, want %d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.ID == "" || op.Action != action || op.AssetID != input.AssetID || op.ResourceKind != provider.ElasticIP ||
				w.Header().Get("Location") != route+"/"+op.ID {
				t.Fatalf("invalid binding receipt: %+v", op)
			}
		}
		return op
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if op.Status != "awaiting_approval" {
			t.Fatalf("binding skipped independent approval: %+v", op)
		}
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(op operations.Operation, status string, advance bool) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, status, advance)
	}
	mismatched := base
	mismatched.InstanceID = b.other
	submit(bind, "other-project-instance", mismatched, http.StatusForbidden)
	stale := base
	stale.InstanceRevision++
	submit(bind, "stale-instance-version", stale, http.StatusPreconditionFailed)
	if f.vendor == provider.HuaweiECS {
		mismatched = base
		mismatched.PortID = ""
		submit(bind, "missing-huawei-port", mismatched, http.StatusBadRequest)
	}
	disabled := submit(bind, "binding-disabled", base, http.StatusAccepted)
	approve(disabled)
	if failed := wait(disabled, "failed", false); failed.OutcomeReason != "elastic_ip_binding_disabled" || b.changes.Load() != 0 {
		t.Fatalf("binding was available with default profile: %+v", failed)
	}
	enabled.Store(true)
	if f.vendor == provider.HuaweiECS {
		mismatched = base
		mismatched.PortID = b.other
		unauthorizedPort := submit(bind, "wrong-huawei-port", mismatched, http.StatusAccepted)
		approve(unauthorizedPort)
		if failed := wait(unauthorizedPort, "failed", false); failed.OutcomeReason != "elastic_ip_binding_preconditions_unconfirmed" || b.changes.Load() != 0 {
			t.Fatalf("wrong port reached vendor write: %+v", failed)
		}
	}
	op := submit(bind, "bind", base, http.StatusAccepted)
	if replay := submit(bind, "bind", base, http.StatusAccepted); replay.ID != op.ID {
		t.Fatal("binding created another operation for the same idempotency key")
	}
	detail, err := store.Get(ctx, reviewer, scope, op.ID)
	if err != nil || detail.NetworkIntent == nil || detail.NetworkIntent.PublicAddress != base.PublicAddress ||
		detail.NetworkIntent.AssetRevision != revision || detail.NetworkIntent.ParentID != b.instance ||
		detail.NetworkIntent.ParentRevision != base.InstanceRevision || detail.NetworkIntent.PortID != b.port {
		t.Fatalf("approval intent lost resource coordinates: %+v %v", detail, err)
	}
	overlap := base
	overlap.AssetID, overlap.PublicAddress = f.first, fixtureAllocatedFirstIP
	submit(bind, "same-instance-other-address", overlap, http.StatusConflict)
	if f.vendor != provider.TencentCVM {
		deleteInstance := operations.Input{
			Account: f.vendor, Region: f.region, AssetID: b.instance, Action: "delete",
		}
		preview, err := store.PreviewDeletion(ctx, actor, scope, deleteInstance)
		if err != nil || !preview.ActiveElasticIPBinding {
			t.Fatalf("deletion preview omitted active binding: %+v %v", preview, err)
		}
		if _, err := store.Submit(ctx, actor, scope, "delete-during-binding", base.InstanceRevision, deleteInstance); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("instance deletion did not conflict with binding: %v", err)
		}
	}
	approve(op)
	if observing := wait(op, "observing", false); observing.OutcomeReason != "submission_outcome_unknown" || b.changes.Load() != 1 {
		t.Fatalf("ambiguous binding dispatch: %+v", observing)
	}
	waitPendingCloudObservation(t, db, op.ID)
	b.wrong.Store(true)
	b.bound.Store(true)
	if review := wait(op, "needs_review", true); review.OutcomeReason != "elastic_ip_binding_requires_evidence" {
		t.Fatalf("confirmed the wrong instance: %+v", review)
	}
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{
		Decision: "succeeded", EvidenceRef: "fixture/wrong-eip-target",
	}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success accepted without matching EIP evidence: %v", err)
	}
	b.wrong.Store(false)
	var oldFence int64
	if err := db.QueryRowContext(ctx, `UPDATE cmp_inventory_scopes SET running=true,cursor='older-scan',fence=fence+1,pages=1,lease_until=now()+interval '1 minute'
		WHERE (account_id,kind)=($1,'elastic-ip') RETURNING fence`, f.vendor).Scan(&oldFence); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "fixture/live-instance-binding"}); err != nil {
		t.Fatal(err)
	}
	if succeeded := wait(op, "succeeded", true); succeeded.OutcomeReason != "elastic_ip_binding_confirmed" || b.changes.Load() != 1 {
		t.Fatalf("binding observation did not settle without re-send: %+v", succeeded)
	}
	f.assertElasticIPBindingState(t, db, op.ID, true)
	var fence int64
	if err := db.QueryRowContext(ctx, "SELECT fence FROM cmp_inventory_scopes WHERE (account_id,kind)=($1,'elastic-ip')", f.vendor).
		Scan(&fence); err != nil || fence != oldFence+1 {
		t.Fatalf("old inventory generation remained valid: old %d new %d error %v", oldFence, fence, err)
	}

	if f.vendor == provider.TencentCVM {
		b.cascade.Store(true)
		cascade := submit(unbind, "unbind-would-release", base, http.StatusAccepted)
		approve(cascade)
		if failed := wait(cascade, "failed", false); failed.OutcomeReason != "elastic_ip_binding_preconditions_unconfirmed" || b.changes.Load() != 1 {
			t.Fatalf("unbind allowed Tencent cascade release: %+v", failed)
		}
		b.cascade.Store(false)
	}
	b.wrong.Store(true)
	changed := submit(unbind, "remotely-rebound", base, http.StatusAccepted)
	approve(changed)
	if failed := wait(changed, "failed", false); failed.OutcomeReason != "elastic_ip_binding_preconditions_unconfirmed" || b.changes.Load() != 1 {
		t.Fatalf("unbind targeted a remote relationship that drifted: %+v", failed)
	}
	b.wrong.Store(false)

	unbinding := submit(unbind, "unbind", base, http.StatusAccepted)
	approve(unbinding)
	if observing := wait(unbinding, "observing", false); b.changes.Load() != 2 ||
		(f.vendor == provider.HuaweiECS && observing.ProviderRequestID != fixtureVPCReceipt) {
		t.Fatalf("unbind dispatch did not retain expected receipt: %+v", observing)
	}
	b.bound.Store(false)
	if succeeded := wait(unbinding, "succeeded", true); succeeded.OutcomeReason != "elastic_ip_unbinding_confirmed" || b.changes.Load() != 2 {
		t.Fatalf("unbind did not preserve allocation: %+v", succeeded)
	}
	f.assertElasticIPBindingState(t, db, unbinding.ID, false)
	server := httptest.NewServer(app)
	defer server.Close()
	body, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, replayCase := range []struct {
		action, key string
		original    operations.Operation
	}{{bind, "bind", op}, {unbind, "unbind", unbinding}} {
		var output bytes.Buffer
		args := []string{replayCase.action, scope.Tenant, scope.Project, "--idempotency-key", replayCase.key,
			"--if-match", strconv.FormatInt(revision, 10)}
		if err := runOperationsClient(ctx, args, server.URL, actor, bytes.NewReader(body), &output, nil); err != nil {
			t.Fatal(err)
		}
		var replay operations.Operation
		if err := json.Unmarshal(output.Bytes(), &replay); err != nil || replay.ID != replayCase.original.ID || replay.Status != "succeeded" {
			t.Fatalf("CLI replay of %s: %+v %v", replayCase.action, replay, err)
		}
	}
}

func (f *vpcCreationFixture) assertElasticIPBindingState(t *testing.T, db *sql.DB, operationID string, bound bool) {
	t.Helper()
	var observations int
	var inventoryOK, delivered bool
	err := db.QueryRowContext(t.Context(), `SELECT i.binding_observations,
		o.public_addresses=jsonb_build_array($2::text) AND o.removed_at IS NULL AND o.deletion_confirmed_at IS NULL AND
			(NOT s.running) AND s.cursor='' AND
			(CASE WHEN $4::bool THEN (o.status IN ('BIND','InUse','ACTIVE') AND o.links @> jsonb_build_array(
				jsonb_build_object('kind','instance','id',$3::text,'relation','bound_to')))
			 ELSE (o.status IN ('UNBIND','Available','DOWN') AND o.links='[]'::jsonb) END),
		EXISTS(SELECT 1 FROM cmp_operation_event_outbox AS d JOIN cmp_instance_operation_events AS e ON e.event_id=d.sequence
			WHERE d.event_type='elastic-ip.operation.succeeded' AND e.operation_id=$1)
		FROM cmp_instance_operations AS op JOIN cmp_network_intents AS i USING(operation_id)
		JOIN cmp_inventory_objects AS o ON (op.scope_id,op.external_id)=(o.scope_id,o.external_id)
		JOIN cmp_inventory_scopes AS s ON s.scope_id=o.scope_id
		WHERE op.operation_id=$1`, operationID, fixtureAllocatedSecondIP, f.binding.instance, bound).Scan(&observations, &inventoryOK, &delivered)
	if err != nil || observations != 2 || !inventoryOK || !delivered {
		t.Fatalf("binding evidence, inventory or terminal event missing: %d %t %t %v", observations, inventoryOK, delivered, err)
	}
}

func (b *elasticIPBindingFixture) remoteInstance() string {
	if b.wrong.Load() {
		return b.other
	}
	return b.instance
}

func (f *vpcCreationFixture) tencentElasticIPBinding(t *testing.T, w http.ResponseWriter, r *http.Request, body []byte, action string, send func(any)) bool {
	t.Helper()
	b := f.binding
	if r.Header.Get("X-TC-Version") != "2017-03-12" {
		return false
	}
	var data struct {
		Offset, Limit           int
		InstanceIds, AddressIds []string
		InstanceId, AddressId   string
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil {
		return false
	}
	if action == "DescribeInstances" && r.Host == "cvm.tencentcloudapi.com" && slices.Equal(data.InstanceIds, []string{b.instance}) {
		send(map[string]any{"TotalCount": 1, "InstanceSet": []map[string]string{{"InstanceId": b.instance, "InstanceState": "RUNNING"}}})
		return true
	}
	if r.Host != "vpc.tencentcloudapi.com" {
		return false
	}
	switch action {
	case "DescribeAddresses":
		if !slices.Equal(data.AddressIds, []string{f.second}) || data.Limit != 2 || data.Offset != 0 {
			return false
		}
		row := map[string]any{"AddressId": f.second, "AddressIp": fixtureAllocatedSecondIP, "AddressType": "EIP",
			"AddressStatus": "UNBIND", "IsArrears": false, "IsBlocked": false, "CascadeRelease": b.cascade.Load()}
		if b.bound.Load() {
			row["AddressStatus"], row["InstanceId"], row["InstanceType"] = "BIND", b.remoteInstance(), "CVM"
		}
		send(map[string]any{"TotalCount": 1, "AddressSet": []any{row}})
	case "AssociateAddress", "DisassociateAddress":
		bind := action == "AssociateAddress"
		if data.AddressId != f.second || (bind && data.InstanceId != b.instance) || (!bind && data.InstanceId != "") || b.bound.Load() == bind {
			return false
		}
		b.changes.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) alibabaElasticIPBinding(t *testing.T, w http.ResponseWriter, r *http.Request, action string, send func(any)) bool {
	t.Helper()
	b, q := f.binding, r.URL.Query()
	if q.Get("RegionId") != f.region {
		return false
	}
	if action == "DescribeInstances" && r.Host == "ecs."+f.region+".aliyuncs.com" && r.Header.Get("X-ACS-Version") == "2014-05-26" {
		var ids []string
		if json.Unmarshal([]byte(q.Get("InstanceIds")), &ids) != nil || !slices.Equal(ids, []string{b.instance}) {
			return false
		}
		send(map[string]any{"Instances": map[string]any{"Instance": []any{map[string]any{
			"InstanceId": b.instance, "RegionId": f.region, "Status": "Running", "VpcAttributes": map[string]string{"VpcId": b.network},
		}}}})
		return true
	}
	if r.Host != "vpc.aliyuncs.com" || r.Header.Get("X-ACS-Version") != "2016-04-28" || q.Get("AllocationId") != f.second {
		return false
	}
	switch action {
	case "DescribeEipAddresses":
		if len(q) != 4 || q.Get("PageNumber") != "1" || q.Get("PageSize") != "10" {
			return false
		}
		row := map[string]any{"AllocationId": f.second, "IpAddress": fixtureAllocatedSecondIP, "Status": "Available",
			"RegionId": f.region, "ServiceManaged": 0, "BusinessStatus": "Normal"}
		if b.bound.Load() {
			row["Status"], row["InstanceId"], row["InstanceType"], row["InstanceRegionId"] = "InUse", b.remoteInstance(), "EcsInstance", f.region
		}
		send(map[string]any{"TotalCount": 1, "PageNumber": 1, "PageSize": 10, "EipAddresses": map[string]any{"EipAddress": []any{row}}})
	case "AssociateEipAddress", "UnassociateEipAddress":
		token, err := f.token(r.Context())
		if err != nil || len(q) != 5 || q.Get("InstanceId") != b.instance || q.Get("InstanceType") != "EcsInstance" ||
			q.Get("ClientToken") != token || b.bound.Load() == (action == "AssociateEipAddress") {
			return false
		}
		b.changes.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		return false
	}
	return true
}

func (f *vpcCreationFixture) huaweiElasticIPBinding(t *testing.T, w http.ResponseWriter, r *http.Request, send func(any)) bool {
	t.Helper()
	b := f.binding
	instance := "/v1/" + fixtureHuaweiProject + "/cloudservers/" + b.instance
	if r.Host == "ecs."+f.region+".myhuaweicloud.com" && r.Method == http.MethodGet {
		switch r.URL.Path {
		case instance:
			send(map[string]any{"server": map[string]any{"id": b.instance, "tenant_id": fixtureHuaweiProject,
				"status": "ACTIVE", "locked": false, "metadata": map[string]string{"vpc_id": b.network}}})
		case instance + "/os-interface":
			send(map[string]any{"interfaceAttachments": []any{map[string]any{"port_id": b.port, "port_state": "ACTIVE",
				"fixed_ips": []map[string]string{{"ip_address": "10.0.0.22"}}}}})
		default:
			return false
		}
		return true
	}
	if r.Host != "vpc."+f.region+".myhuaweicloud.com" {
		return false
	}
	path := "/v3/" + fixtureHuaweiProject + "/eip/publicips"
	row := map[string]any{"id": f.second, "project_id": fixtureHuaweiProject, "ip_version": 4, "type": "EIP",
		"status": "DOWN", "public_ip_address": fixtureAllocatedSecondIP}
	if b.bound.Load() {
		row["status"], row["associate_instance_type"], row["associate_instance_id"] = "ACTIVE", "PORT", b.port
		row["vnic"] = map[string]string{"port_id": b.port, "device_id": b.remoteInstance(), "device_owner": "compute:nova", "vpc_id": b.network}
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == path:
		q := r.URL.Query()
		if q.Get("limit") != "2000" {
			return false
		}
		rows := []any{}
		if q.Get("id") == f.second {
			rows = append(rows, row)
		} else if q.Get("vnic.port_id") != b.port || b.bound.Load() {
			return false
		}
		send(map[string]any{"publicips": rows, "page_info": map[string]int{"current_count": len(rows)}})
	case r.Method == http.MethodPost && r.URL.Path == path+"/"+f.second+"/associate-instance":
		var data struct {
			PublicIP struct {
				ID   string `json:"associate_instance_id"`
				Type string `json:"associate_instance_type"`
			} `json:"publicip"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&data) != nil || r.Header.Get("Content-Type") != "application/json" ||
			data.PublicIP.ID != b.port || data.PublicIP.Type != "PORT" || b.bound.Load() {
			return false
		}
		b.changes.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	case r.Method == http.MethodPost && r.URL.Path == path+"/"+f.second+"/disassociate-instance":
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) != 0 || !b.bound.Load() {
			return false
		}
		b.changes.Add(1)
		row["status"] = "DOWN"
		delete(row, "associate_instance_type")
		delete(row, "associate_instance_id")
		delete(row, "vnic")
		send(map[string]any{"request_id": fixtureVPCReceipt, "publicip": row})
	default:
		return false
	}
	return true
}
