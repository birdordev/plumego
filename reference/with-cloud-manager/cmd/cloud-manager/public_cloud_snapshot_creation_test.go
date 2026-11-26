package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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

const (
	fixtureSnapshotName = "snapshot-test"
	fixtureSnapshotTag  = "plumego-cmp-snapshot"
)

func TestAcceptancePublicCloudSnapshotCreation(t *testing.T) {
	for _, f := range []*snapshotCreationFixture{
		{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou", volume: "disk-fixture", id: "snap-fixture"},
		{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou", volume: "d-fixture", id: "s-fixture"},
		{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4", mode: "v2",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010"},
		{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4", mode: "v5",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010"},
	} {
		t.Run(f.vendor+f.mode, func(t *testing.T) {
			testPublicCloudSnapshotCreation(t, f)
		})
	}
}

func testPublicCloudSnapshotCreation(t *testing.T, f *snapshotCreationFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
			('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
			('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),
			('tenant','other','operator','operator');
		INSERT INTO cmp_operation_event_sinks(sink_id,tenant_id,project_id,endpoint_hash)
			VALUES ('snapshot-events','tenant','project',decode(repeat('00',32),'hex'));`)
	const actor, reviewer = "fixture-snapshot-operator-service-token", "fixture-snapshot-reviewer-service-token"
	for _, role := range []struct{ name, token string }{{"operator", actor}, {"reviewer", reviewer}} {
		digest := sha256.Sum256([]byte(role.token))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,'tenant',$2,now()+interval '10 minutes')`, digest[:], role.name)
	}
	const profileRef = "fixture/snapshot"
	identityFixture := vpcCreationFixture{vendor: f.vendor, account: f.account, region: f.region, parent: f.volume}
	exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant',$1,'project',$1,$2,$3,'write_enabled')`, f.vendor, profileRef, identityFixture.remoteIdentity())
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'volume',1,now())`, f.vendor, f.region)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$2,'source volume','available',1 FROM cmp_inventory_scopes WHERE account_id=$1`, f.vendor, f.volume)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
			identityFixture.serve(t, w, r)
			return
		}
		if !f.serve(t, w, r) {
			t.Errorf("unexpected %s %s %s %s %s request", f.vendor, r.Method, r.Host, r.URL.Path,
				r.Header.Get("X-TC-Action")+r.Header.Get("X-ACS-Action"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	profile := publicCloudProfile{Ref: profileRef, Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}, HuaweiSnapshotAPI: f.mode}}
	if f.vendor == provider.HuaweiECS {
		profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	save := func(name string, allow bool) string {
		t.Helper()
		profile.AllowSnapshotCreation = allow
		data, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	disabledProfile, enabledProfile := save("disabled.json", false), save("enabled.json", true)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const projectPath = "/v1/tenants/tenant/projects/project"
	submit := func(project, key, match string, attached bool, want int) operations.Operation {
		t.Helper()
		body := fmt.Sprintf(`{"account":%q,"region":%q,"volume_id":%q,"name":%q,"allow_attached":%t}`,
			f.vendor, f.region, f.volume, fixtureSnapshotName, attached)
		r := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/"+project+"/snapshots", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("submit snapshot %s: status %d, want %d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.ResourceKind != provider.Snapshot || op.ID == "" || w.Header().Get("Location") != projectPath+"/operations/"+op.ID {
				t.Fatalf("unexpected receipt: %+v", op)
			}
		}
		return op
	}
	store := operations.NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	var enabled atomic.Bool
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(workerCtx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			config := disabledProfile
			if enabled.Load() {
				config = enabledProfile
			}
			return connectPublicCloudOperator(ctx, binding, config, cloudFixtureTransport{endpoint, server.Client().Transport})
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
			t.Error("snapshot worker did not stop")
		}
	}()
	approve := func(id string) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, id, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(id, status string, advance bool) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, id, status, advance)
	}
	submit("project", "missing-revision", "", false, http.StatusPreconditionFailed)
	submit("project", "stale-source", `"2"`, false, http.StatusPreconditionFailed)
	submit("other", "foreign-source", `"1"`, false, http.StatusForbidden)
	disabled := submit("project", "disabled", `"1"`, true, http.StatusAccepted)
	approve(disabled.ID)
	if failure := wait(disabled.ID, "failed", false); failure.OutcomeReason != "provider_creation_blocked" || f.preflights.Load() != 0 {
		t.Fatalf("disabled creation reached the provider: %+v", failure)
	}
	enabled.Store(true)
	attached := submit("project", "require-consent", `"1"`, false, http.StatusAccepted)
	approve(attached.ID)
	if failure := wait(attached.ID, "failed", false); failure.OutcomeReason != "provider_creation_blocked" || f.writes.Load() != 0 {
		t.Fatalf("attached volume without consent reached create: %+v", failure)
	}
	op := submit("project", "new-snapshot", `"1"`, true, http.StatusAccepted)
	if op.Status != "awaiting_approval" || f.writes.Load() != 0 {
		t.Fatalf("snapshot creation skipped approval: %+v", op)
	}
	if replay := submit("project", "new-snapshot", `"1"`, true, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("retry created another operation: %+v", replay)
	}
	if _, err := store.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("operator approved own snapshot request: %v", err)
	}
	if f.vendor == provider.TencentCVM {
		api := httptest.NewTLSServer(app)
		defer api.Close()
		var reply bytes.Buffer
		request := fmt.Sprintf(`{"account":%q,"region":%q,"volume_id":%q,"name":%q,"allow_attached":true}`,
			f.vendor, f.region, f.volume, fixtureSnapshotName)
		cli := []string{"create-snapshot", "tenant", "project", "--idempotency-key", "new-snapshot", "--if-match", "1"}
		if err := runOperationsClient(ctx, cli, api.URL, actor, strings.NewReader(request), &reply, api.Client().Transport); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(reply.String(), op.ID) {
			t.Fatalf("CLI received a different operation: %s", &reply)
		}
		exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
			VALUES ('tenant',$1,$2,'instance',1,now())`, f.vendor, f.region)
		exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,'ins-fixture','instance','RUNNING',1 FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='instance'`, f.vendor)
		mount := operations.Input{Account: f.vendor, Region: f.region,
			AssetID: "ins-fixture", Action: "attach-volume", VolumeID: f.volume, VolumeRevision: 1}
		if _, err := store.Submit(ctx, actor, scope, "concurrent-mount", 1, mount); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("snapshot did not block concurrent mount: %v", err)
		}
		batch := operations.BatchInput{Items: []operations.BatchItem{{Input: mount, Revision: 1}}}
		preview, err := store.PreviewBatch(ctx, actor, scope, batch)
		if err != nil || preview.CanSubmit || len(preview.Items) != 1 || preview.Items[0].Reason != "busy" ||
			preview.Items[0].VolumeRevision != 1 {
			t.Fatalf("batch preview did not explain the snapshot conflict: %+v, err=%v", preview, err)
		}
	}
	submit("project", "new-snapshot", `"1"`, false, http.StatusConflict)
	submit("project", "concurrent-snapshot", `"1"`, true, http.StatusConflict)
	approve(op.ID)
	pending := wait(op.ID, "observing", false)
	wantReason := "submission_outcome_unknown"
	if f.mode == "v5" {
		wantReason = "provider_accepted"
	}
	if pending.OutcomeReason != wantReason || pending.AssetID != "" || pending.SnapshotIntent == nil ||
		pending.SnapshotIntent.VolumeRevision != 1 || pending.SnapshotIntent.VolumeID != f.volume || !pending.SnapshotIntent.AllowAttached {
		t.Fatalf("unexpected waiting operation: %+v", pending)
	}
	waitPendingCloudObservation(t, db, op.ID)
	if f.mode == "v5" {
		exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until,running)
			VALUES ('tenant',$1,$2,'snapshot',1,now(),true)`, f.vendor, f.region)
		exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links,snapshot)
			SELECT scope_id,$2,$3,'creating',generation,jsonb_build_array(jsonb_build_object('kind','volume','id',$4::text,'relation','snapshot_of')),
				'{"class":"standard","size_gib":40}'::jsonb FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='snapshot'`,
			f.vendor, f.id, fixtureSnapshotName, f.volume)
	}
	f.ready.Store(true)
	complete := wait(op.ID, "succeeded", true)
	if complete.AssetID != f.id || f.writes.Load() != 1 {
		t.Fatalf("snapshot failed to converge without duplicate write: %+v (writes=%d)", complete, f.writes.Load())
	}
	if f.mode == "v5" {
		var running bool
		if err := db.QueryRowContext(ctx, "SELECT running FROM cmp_inventory_scopes WHERE kind='snapshot'").Scan(&running); err != nil || running {
			t.Fatalf("stale inventory scan was not fenced: running=%t err=%v", running, err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='snapshot.operation.succeeded'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("snapshot terminal events: count=%d err=%v", count, err)
	}
	r := httptest.NewRequest(http.MethodGet, projectPath+"/assets?kind=snapshot", nil)
	r.Header.Set("Authorization", "Bearer "+actor)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list snapshots: %d %s", w.Code, w.Body)
	}
	var page inventory.Assets
	decodeProjectData(t, w, &page)
	if len(page.Items) != 1 || page.Items[0].ID != f.id || page.Items[0].ParentID != f.volume ||
		page.Items[0].Snapshot == nil || page.Items[0].Snapshot.SizeGiB != 40 || page.Items[0].OwnershipRevision != 1 {
		t.Fatalf("incorrect snapshot inventory: %+v", page)
	}
}

type snapshotCreationFixture struct {
	vendor, account, region, mode, volume, id string
	preflights, writes                        atomic.Int32
	marker                                    atomic.Pointer[string]
	ready                                     atomic.Bool
}

func (f *snapshotCreationFixture) acceptMarker(marker string) bool {
	f.writes.Add(1)
	return len(marker) == 64 && f.marker.CompareAndSwap(nil, &marker)
}

func (f *snapshotCreationFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	send := func(value any) { writeCloudFixtureJSON(t, w, value) }
	q := r.URL.Query()
	switch f.vendor {
	case provider.TencentCVM:
		raw, err := io.ReadAll(r.Body)
		if err != nil || r.Host != "cbs.tencentcloudapi.com" || r.Header.Get("X-TC-Region") != f.region ||
			r.Header.Get("X-TC-Version") != "2017-03-12" || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
			!validFixtureTencentSignature(r, raw, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		return f.tencent(raw, r.Header.Get("X-TC-Action"), func(value any) { send(map[string]any{"Response": value}) })
	case provider.AlibabaECS:
		if r.Host != "ecs."+f.region+".aliyuncs.com" || q.Get("RegionId") != f.region ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
			!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		return f.alibaba(w, q, r.Header.Get("X-ACS-Action"), send)
	default:
		if r.Host != "evs."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			return false
		}
		return f.huawei(w, r, send)
	}
}

func (f *snapshotCreationFixture) tencent(raw []byte, action string, send func(any)) bool {
	var body struct {
		DiskIds              []string
		DiskId, SnapshotName string
		Tags                 []struct{ Key, Value string }
		Filters              []struct {
			Name   string
			Values []string
		}
	}
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	switch action {
	case "DescribeDisks":
		if len(body.DiskIds) != 1 || body.DiskIds[0] != f.volume {
			return false
		}
		f.preflights.Add(1)
		send(map[string]any{"DiskSet": []any{map[string]any{"DiskId": f.volume, "DiskState": "ATTACHED", "SnapshotAbility": true}}})
	case "CreateSnapshot":
		if body.DiskId != f.volume || body.SnapshotName != fixtureSnapshotName || len(body.Tags) != 1 ||
			body.Tags[0].Key != fixtureSnapshotTag || !f.acceptMarker(body.Tags[0].Value) {
			return false
		}
		panic(http.ErrAbortHandler)
	case "DescribeSnapshots":
		marker := f.marker.Load()
		expected := map[string]string{"snapshot-type": "PRIVATE_SNAPSHOT", "disk-id": f.volume, "snapshot-name": fixtureSnapshotName}
		if marker == nil || len(body.Filters) != len(expected) {
			return false
		}
		for _, filter := range body.Filters {
			if len(filter.Values) != 1 || expected[filter.Name] != filter.Values[0] {
				return false
			}
			delete(expected, filter.Name)
		}
		status, progress := "CREATING", 50
		if f.ready.Load() {
			status, progress = "NORMAL", 100
		}
		send(map[string]any{"TotalCount": 1, "SnapshotSet": []any{map[string]any{
			"SnapshotId": f.id, "SnapshotName": fixtureSnapshotName, "SnapshotState": status, "SnapshotType": "PRIVATE_SNAPSHOT",
			"DiskId": f.volume, "DiskSize": 40, "Percent": progress, "Tags": []map[string]string{{"Key": fixtureSnapshotTag, "Value": *marker}},
		}}})
	default:
		return false
	}
	return true
}

func (f *snapshotCreationFixture) alibaba(w http.ResponseWriter, q url.Values, action string, send func(any)) bool {
	switch action {
	case "DescribeDisks":
		if q.Get("DiskIds") != fmt.Sprintf("[%q]", f.volume) {
			return false
		}
		f.preflights.Add(1)
		send(map[string]any{"Disks": map[string]any{"Disk": []any{map[string]any{
			"DiskId": f.volume, "RegionId": f.region, "Status": "In_use", "Category": "cloud_essd",
		}}}})
	case "CreateSnapshot":
		token := q.Get("ClientToken")
		if len(token) < 16 || q.Get("DiskId") != f.volume || q.Get("SnapshotName") != fixtureSnapshotName ||
			q.Get("Tag.1.Key") != fixtureSnapshotTag || !f.acceptMarker(q.Get("Tag.1.Value")) {
			return false
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	case "DescribeSnapshots":
		marker := f.marker.Load()
		if marker == nil || q.Get("DiskId") != f.volume || q.Get("SnapshotName") != fixtureSnapshotName ||
			q.Get("Tag.1.Key") != fixtureSnapshotTag || q.Get("Tag.1.Value") != *marker {
			return false
		}
		status, available := "progressing", false
		if f.ready.Load() {
			status, available = "accomplished", true
		}
		send(map[string]any{"Snapshots": map[string]any{"Snapshot": []any{map[string]any{
			"SnapshotId": f.id, "SnapshotName": fixtureSnapshotName, "Status": status, "RegionId": f.region,
			"SourceDiskId": f.volume, "SourceStorageType": "disk", "Category": "standard", "SourceDiskSize": "40", "Available": available,
			"Tags": map[string]any{"Tag": []map[string]string{{"TagKey": fixtureSnapshotTag, "TagValue": *marker}}},
		}}}})
	default:
		return false
	}
	return true
}

func (f *snapshotCreationFixture) huawei(w http.ResponseWriter, r *http.Request, send func(any)) bool {
	v2 := "/v2/" + fixtureHuaweiProject
	collection := v2 + "/cloudsnapshots"
	if f.mode == "v5" {
		collection = "/v5/" + fixtureHuaweiProject + "/snapshots"
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == v2+"/cloudvolumes/"+f.volume:
		f.preflights.Add(1)
		send(map[string]any{"volume": map[string]string{"id": f.volume, "status": "in-use", "os-vol-tenant-attr:tenant_id": fixtureHuaweiProject}})
	case r.Method == http.MethodPost && r.URL.Path == collection:
		var input struct {
			Snapshot struct {
				VolumeID          string `json:"volume_id"`
				Name, Description string
				Force             *bool
				Metadata          map[string]string
			} `json:"snapshot"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || r.Header.Get("Content-Type") != "application/json" ||
			input.Snapshot.Name != fixtureSnapshotName || input.Snapshot.VolumeID != f.volume {
			return false
		}
		if f.mode == "v2" {
			if input.Snapshot.Force == nil || !*input.Snapshot.Force || input.Snapshot.Description != "" ||
				!f.acceptMarker(input.Snapshot.Metadata[fixtureSnapshotTag]) {
				return false
			}
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			marker, ok := strings.CutPrefix(input.Snapshot.Description, fixtureSnapshotTag+":")
			if input.Snapshot.Force != nil || len(input.Snapshot.Metadata) != 0 || !ok || !f.acceptMarker(marker) {
				return false
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			send(map[string]string{"snapshot_id": f.id, "job_id": "fixture-v5-snapshot-job"})
		}
	case r.Method == http.MethodGet && r.URL.Path == collection+"/detail":
		q := r.URL.Query()
		marker := f.marker.Load()
		if marker == nil || (f.mode == "v2" && (q.Get("volume_id") != f.volume || q.Get("name") != fixtureSnapshotName)) ||
			(f.mode == "v5" && (q.Get("id") != f.id || q.Get("sort_key") != "id" || q.Get("sort_dir") != "asc")) {
			return false
		}
		rows := []any{}
		switch q.Get("marker") {
		case "":
			status := "creating"
			if f.ready.Load() {
				status = "available"
			}
			row := map[string]any{"id": f.id, "volume_id": f.volume, "name": fixtureSnapshotName, "status": status,
				"size": 40, "category": "standard", "project_id": fixtureHuaweiProject}
			if f.mode == "v2" {
				row["os-extended-snapshot-attributes:project_id"] = fixtureHuaweiProject
				row["metadata"] = map[string]string{fixtureSnapshotTag: *marker}
			} else {
				row["description"] = fixtureSnapshotTag + ":" + *marker
			}
			rows = append(rows, row)
		case f.id:
			if f.mode != "v5" {
				return false
			}
		default:
			return false
		}
		send(map[string]any{"count": len(rows), "snapshots": rows})
	default:
		return false
	}
	return true
}
