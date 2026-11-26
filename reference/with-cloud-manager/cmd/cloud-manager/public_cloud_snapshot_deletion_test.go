package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

func TestAcceptancePublicCloudSnapshotDeletion(t *testing.T) {
	for _, f := range []*snapshotDeletionFixture{
		{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou", volume: "disk-fixture", id: "snap-fixture", class: "PRIVATE_SNAPSHOT"},
		{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou", volume: "d-fixture", id: "s-fixture", class: "standard"},
		{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4", class: "flash",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010"},
		{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4", class: "standard",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010"},
	} {
		t.Run(f.vendor+"/"+f.class, func(t *testing.T) { testPublicCloudSnapshotDeletion(t, f) })
	}
}

func testPublicCloudSnapshotDeletion(t *testing.T, f *snapshotDeletionFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-snapshot-delete-operator-token", "fixture-snapshot-delete-reviewer-token"
	identityFixture := vpcCreationFixture{vendor: f.vendor, account: f.account, region: f.region, parent: f.volume}
	seedSnapshotDeletion(t, db, f, identityFixture.remoteIdentity(), actor, reviewer)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cloud := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
			identityFixture.serve(t, w, r)
			return
		}
		if !f.serve(t, w, r) {
			t.Errorf("unexpected %s %s %s %s request", f.vendor, r.Method, r.Host, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer cloud.Close()
	endpoint, err := url.Parse(cloud.URL)
	if err != nil {
		t.Fatal(err)
	}
	var enabled atomic.Bool
	config := publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
		// 相反的盘点模式验证删除路径由已审批的类别决定。
		if f.class == "flash" {
			config.HuaweiSnapshotAPI = "v5"
		}
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if binding.Provider != f.vendor || binding.RemoteIdentity != identityFixture.remoteIdentity() {
				return nil, identity.ErrForbidden
			}
			profile := config
			profile.AllowSnapshotDeletion = enabled.Load()
			return publiccloud.Connect(ctx, binding.Provider, profile, cloudFixtureTransport{endpoint, cloud.Client().Transport})
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
			t.Error("snapshot deletion worker did not stop")
		}
	}()
	requestBody := func(volume string) string {
		return fmt.Sprintf(`{"account":%q,"region":%q,"asset_id":%q,"volume_id":%q}`, f.vendor, f.region, f.id, volume)
	}
	submit := func(project, key, revision, source string, want int) operations.Operation {
		t.Helper()
		base := "/v1/tenants/tenant/projects/" + project
		r := httptest.NewRequest(http.MethodPost, base+"/snapshots/delete", strings.NewReader(requestBody(source)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Idempotency-Key", key)
		if revision != "" {
			r.Header.Set("If-Match", revision)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("delete request %s: got %d, want %d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if op.AssetID != f.id || op.ResourceKind != provider.Snapshot || op.Action != "delete-snapshot" ||
				w.Header().Get("Location") != base+"/operations/"+op.ID {
				t.Fatalf("unexpected snapshot delete receipt: %+v", op)
			}
		}
		return op
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
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
	submit("project", "no-revision", "", f.volume, http.StatusPreconditionFailed)
	submit("project", "stale", `"2"`, f.volume, http.StatusPreconditionFailed)
	submit("project", "wrong-source", `"1"`, "other-volume", http.StatusForbidden)
	submit("other", "foreign", `"1"`, f.volume, http.StatusForbidden)
	disabled := submit("project", "disabled", `"1"`, f.volume, http.StatusAccepted)
	approve(disabled.ID)
	if result := wait(disabled.ID, "failed", false); result.OutcomeReason != "snapshot_deletion_disabled" || f.reads.Load() != 0 {
		t.Fatalf("disabled delete reached snapshot API: %+v", result)
	}
	enabled.Store(true)
	f.protected.Store(true)
	blocked := submit("project", "protected", `"1"`, f.volume, http.StatusAccepted)
	approve(blocked.ID)
	if result := wait(blocked.ID, "failed", false); result.OutcomeReason != "snapshot_deletion_preconditions_unconfirmed" || f.writes.Load() != 0 {
		t.Fatalf("protected snapshot was sent for deletion: %+v", result)
	}
	f.protected.Store(false)
	op := submit("project", "delete", `"1"`, f.volume, http.StatusAccepted)
	if op.Status != "awaiting_approval" || f.writes.Load() != 0 {
		t.Fatalf("snapshot delete skipped approval: %+v", op)
	}
	if _, err := store.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("submitter approved own deletion: %v", err)
	}
	if replay := submit("project", "delete", `"1"`, f.volume, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("same key got another operation: %+v", replay)
	}
	submit("project", "delete", `"1"`, "other-volume", http.StatusConflict)
	submit("project", "duplicate-delete", `"1"`, f.volume, http.StatusConflict)
	if f.vendor == provider.TencentCVM {
		create := operations.SnapshotInput{Account: f.vendor, Region: f.region, SnapshotSpec: provider.SnapshotSpec{Name: "test-snapshot", VolumeID: f.volume}}
		if _, err := store.SubmitSnapshotCreation(ctx, actor, scope, "create-during-delete", 1, create); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("pending deletion did not serialize source snapshots: %v", err)
		}
		server := httptest.NewTLSServer(app)
		defer server.Close()
		var reply bytes.Buffer
		cli := []string{"delete-snapshot", "tenant", "project", "--idempotency-key", "delete", "--if-match", "1"}
		err := runOperationsClient(ctx, cli, server.URL, actor, strings.NewReader(requestBody(f.volume)), &reply, server.Client().Transport)
		if err != nil || !strings.Contains(reply.String(), op.ID) {
			t.Fatalf("delete CLI replay: data=%s err=%v", &reply, err)
		}
	}
	f.observation.Store(snapshotFixtureAbsent)
	approve(op.ID)
	pending := wait(op.ID, "observing", false)
	if pending.SnapshotIntent == nil || pending.SnapshotIntent.Class != f.class || pending.SnapshotIntent.VolumeID != f.volume ||
		pending.SnapshotIntent.VolumeRevision != 1 || (f.vendor == provider.TencentCVM && pending.OutcomeReason != "submission_outcome_unknown") {
		t.Fatalf("deletion lost authorized target or unknown outcome: %+v", pending)
	}
	waitSnapshotDeletionEvidence(t, db, op.ID, 1, "queued")
	assertSnapshotTombstone(t, db, f.id, false)
	f.observation.Store(snapshotFixtureHTTPNotFound)
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "queued")
	assertSnapshotTombstone(t, db, f.id, false)
	f.observation.Store(snapshotFixtureWrongSource)
	wait(op.ID, "needs_review", true)
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
	assertSnapshotTombstone(t, db, f.id, false)
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{
		Decision: "succeeded", EvidenceRef: "snapshot/manual", ProviderRequestID: pending.ProviderRequestID,
	}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success bypassed absence evidence: %v", err)
	}
	enabled.Store(false)
	f.observation.Store(snapshotFixtureAbsent)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "snapshot/recheck"}); err != nil {
		t.Fatal(err)
	}
	complete := wait(op.ID, "succeeded", true)
	if complete.OutcomeReason != "snapshot_absence_confirmed" || f.writes.Load() != 1 {
		t.Fatalf("deletion settled without exactly one write: %+v count=%d", complete, f.writes.Load())
	}
	assertSnapshotTombstone(t, db, f.id, true)
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='snapshot.operation.succeeded'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("snapshot terminal event: count=%d err=%v", events, err)
	}
	if replay := submit("project", "delete", `"1"`, f.volume, http.StatusAccepted); replay.ID != op.ID || replay.Status != "succeeded" {
		t.Fatalf("successful deletion could not replay after tombstone: %+v", replay)
	}
	inventoryStore := inventory.NewStore(db)
	claim, err := inventoryStore.Claim(ctx, inventory.Key{Tenant: scope.Tenant, Account: f.vendor, Region: f.region, Kind: provider.Snapshot}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stale := provider.Object{ID: f.id, Name: "snapshot", Status: "available", Snapshot: &provider.SnapshotDetails{Class: f.class, SizeGiB: 40}}
	stale.Link(provider.Volume, f.volume, "snapshot_of")
	if _, _, err := inventoryStore.ScanPage(ctx, claim, staleAssetDiscovery{stale}); err != nil {
		t.Fatal(err)
	}
	assertSnapshotTombstone(t, db, f.id, true)
}

func seedSnapshotDeletion(t *testing.T, db *sql.DB, f *snapshotDeletionFixture, remoteIdentity, actor, reviewer string) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
			('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
			('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),('tenant','other','operator','operator');
		INSERT INTO cmp_operation_event_sinks(sink_id,tenant_id,project_id,endpoint_hash)
			VALUES ('snapshot-delete-events','tenant','project',decode(repeat('00',32),'hex'));`)
	for _, role := range []struct{ name, token string }{{"operator", actor}, {"reviewer", reviewer}} {
		digest := sha256.Sum256([]byte(role.token))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,'tenant',$2,now()+interval '10 minutes')`, digest[:], role.name)
	}
	exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant',$1,'project',$1,'fixture/snapshot-delete',$2,'write_enabled')`, f.vendor, remoteIdentity)
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'volume',1,now()),('tenant',$1,$2,'snapshot',1,now())`, f.vendor, f.region)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,removed_at)
		SELECT scope_id,$2,'source volume','available',1,CASE WHEN $3::bool THEN now() END
		FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='volume'`,
		f.vendor, f.volume, f.vendor == provider.AlibabaECS)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links,snapshot)
		SELECT scope_id,$2,'snapshot','available',1,jsonb_build_array(jsonb_build_object('kind','volume','id',$3::text,'relation','snapshot_of')),
			jsonb_build_object('class',$4::text,'size_gib',40)
		FROM cmp_inventory_scopes WHERE account_id=$1 AND kind='snapshot'`, f.vendor, f.id, f.volume, f.class)
}

func waitSnapshotDeletionEvidence(t *testing.T, db *sql.DB, id string, count int, queueState string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var actual int
		var status string
		err := db.QueryRowContext(ctx, `SELECT i.absence_observations,t.status FROM cmp_network_intents AS i
			JOIN cmp_operation_tasks AS t ON t.id=i.operation_id WHERE i.operation_id=$1`, id).Scan(&actual, &status)
		if err != nil {
			t.Fatal(err)
		}
		if actual == count && status == queueState {
			return
		}
		if _, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET available_at=clock_timestamp()
			WHERE id=$1 AND status='queued' AND available_at>clock_timestamp()`, id); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("snapshot absence progress=(%d,%s), want=(%d,%s)", actual, status, count, queueState)
		case <-ticker.C:
		}
	}
}

func assertSnapshotTombstone(t *testing.T, db *sql.DB, id string, want bool) {
	t.Helper()
	var removed, confirmed bool
	err := db.QueryRowContext(t.Context(), `SELECT removed_at IS NOT NULL,deletion_confirmed_at IS NOT NULL
		FROM cmp_inventory_objects WHERE external_id=$1`, id).Scan(&removed, &confirmed)
	if err != nil || removed != want || confirmed != want {
		t.Fatalf("snapshot removed=%t confirmed=%t, want=%t, err=%v", removed, confirmed, want, err)
	}
}

type staleAssetDiscovery struct{ item provider.Object }

func (d staleAssetDiscovery) Discover(context.Context, provider.Scope, string) (provider.Page, error) {
	return provider.Page{Objects: []provider.Object{d.item}}, nil
}

const (
	snapshotFixtureAbsent int32 = iota + 1
	snapshotFixtureHTTPNotFound
	snapshotFixtureWrongSource
)

type snapshotDeletionFixture struct {
	vendor, account, region, volume, id, class string
	reads, writes, observation                 atomic.Int32
	protected                                  atomic.Bool
}

func (f *snapshotDeletionFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	switch f.vendor {
	case provider.TencentCVM:
		raw, err := io.ReadAll(r.Body)
		if err != nil || r.Host != "cbs.tencentcloudapi.com" || r.Header.Get("X-TC-Region") != f.region ||
			r.Header.Get("X-TC-Version") != "2017-03-12" || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
			!validFixtureTencentSignature(r, raw, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		var query struct {
			SnapshotIds      []string
			DeleteBindImages *bool
		}
		if json.Unmarshal(raw, &query) != nil || len(query.SnapshotIds) != 1 || query.SnapshotIds[0] != f.id {
			return false
		}
		switch r.Header.Get("X-TC-Action") {
		case "DeleteSnapshots":
			if query.DeleteBindImages == nil || *query.DeleteBindImages || f.writes.Add(1) != 1 {
				return false
			}
			panic(http.ErrAbortHandler)
		case "DescribeSnapshots":
			return f.read(t, w)
		}
	case provider.AlibabaECS:
		q := r.URL.Query()
		if r.Host != "ecs."+f.region+".aliyuncs.com" || q.Get("RegionId") != f.region ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
			!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		switch r.Header.Get("X-ACS-Action") {
		case "DeleteSnapshot":
			if q.Get("SnapshotId") != f.id || q.Get("Force") != "false" || f.writes.Add(1) != 1 {
				return false
			}
			writeCloudFixtureJSON(t, w, map[string]string{"RequestId": "snapshot-delete-receipt"})
			return true
		case "DescribeSnapshots":
			return q.Get("SnapshotIds") == fmt.Sprintf("[%q]", f.id) && f.read(t, w)
		}
	case provider.HuaweiECS:
		if r.Host != "evs."+f.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			return false
		}
		path := "/v2/" + fixtureHuaweiProject + "/cloudsnapshots"
		if f.class == "standard" {
			path = "/v5/" + fixtureHuaweiProject + "/snapshots"
		}
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == path+"/"+f.id && r.URL.RawQuery == "":
			if f.writes.Add(1) != 1 {
				return false
			}
			w.WriteHeader(http.StatusAccepted)
			return true
		case r.Method == http.MethodGet && r.URL.Path == path+"/detail":
			return r.URL.Query().Get("id") == f.id && f.read(t, w)
		}
	}
	return false
}

func (f *snapshotDeletionFixture) read(t *testing.T, w http.ResponseWriter) bool {
	t.Helper()
	f.reads.Add(1)
	var observation int32
	if f.writes.Load() > 0 {
		observation = f.observation.Load()
	}
	if observation == snapshotFixtureHTTPNotFound {
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	source := f.volume
	if observation == snapshotFixtureWrongSource {
		source += "x"
	}
	rows := []any{}
	switch f.vendor {
	case provider.TencentCVM:
		if observation != snapshotFixtureAbsent {
			rows = append(rows, map[string]any{"SnapshotId": f.id, "SnapshotType": f.class, "SnapshotState": "NORMAL", "DiskId": source,
				"IsLocked": f.protected.Load(), "ImageCount": 0, "ShareReference": 0})
		}
		writeCloudFixtureJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": len(rows), "SnapshotSet": rows}})
	case provider.AlibabaECS:
		if observation != snapshotFixtureAbsent {
			usage := "none"
			if f.protected.Load() {
				usage = "image"
			}
			rows = append(rows, map[string]any{"SnapshotId": f.id, "RegionId": f.region, "SourceDiskId": source,
				"Category": f.class, "Status": "accomplished", "SnapshotType": "user", "Usage": usage})
		}
		writeCloudFixtureJSON(t, w, map[string]any{"TotalCount": len(rows), "Snapshots": map[string]any{"Snapshot": rows}})
	case provider.HuaweiECS:
		if observation != snapshotFixtureAbsent {
			name := "snapshot"
			if f.protected.Load() {
				name = "autobk_snapshot_name"
			}
			row := map[string]any{"id": f.id, "volume_id": source, "status": "available", "name": name}
			if f.class == "standard" {
				row["category"], row["project_id"], row["snapshot_type"] = f.class, fixtureHuaweiProject, "user"
			} else {
				row["os-extended-snapshot-attributes:project_id"] = fixtureHuaweiProject
			}
			rows = append(rows, row)
		}
		writeCloudFixtureJSON(t, w, map[string]any{"count": len(rows), "snapshots": rows})
	}
	return true
}
