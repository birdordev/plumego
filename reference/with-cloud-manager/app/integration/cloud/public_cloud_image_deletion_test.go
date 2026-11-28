package cloudtest

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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cloudmanager "with-cloud-manager/app"
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

func TestAcceptancePublicCloudImageDeletion(t *testing.T) {
	for _, cloud := range []struct{ vendor, account, region, id, status string }{
		{provider.TencentCVM, "100000000001", "ap-guangzhou", "img-fixture", "NORMAL"},
		{provider.AlibabaECS, "1000000000000002", "cn-hangzhou", "m-fixture", "Available"},
		{provider.HuaweiECS, "1234567890abcdef1234567890abcdef", "cn-north-4", "40000000-0000-4000-8000-000000000010", "active"},
	} {
		t.Run(cloud.vendor, func(t *testing.T) {
			fixture := vpcCreationFixture{vendor: cloud.vendor, account: cloud.account, region: cloud.region}
			testPublicCloudImageDeletion(t, &fixture, cloud.id, cloud.status)
		})
	}
}

func testPublicCloudImageDeletion(t *testing.T, cloud *vpcCreationFixture, id, imageStatus string) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-image-delete-operator-token", "fixture-image-delete-reviewer-token"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cloud.vendor, region: cloud.region,
		volume: "source-volume", id: "source-snapshot", class: "standard"}, cloud.remoteIdentity(), actor, reviewer)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const admin = "fixture-image-delete-admin-token-000"
	exec(`INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','admin','service','fixture','admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
			('tenant','project','admin','admin'),('tenant','other','admin','admin');`)
	digest := sha256.Sum256([]byte(admin))
	exec(`INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant','admin',now()+interval '10 minutes')`, digest[:])
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant',$1,$2,kind,1,now() FROM unnest(ARRAY['image','instance','subnet','security-group']) AS kind`, cloud.vendor, cloud.region)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$1,'fixture image',$2,1 FROM cmp_inventory_scopes WHERE kind='image'`, id, imageStatus)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links)
		SELECT scope_id,CASE WHEN kind='subnet' THEN 'subnet-fixture' ELSE 'sg-fixture' END,'network dependency','available',1,
		'[{"kind":"network","id":"vpc-fixture","relation":"in_network"}]'
		FROM cmp_inventory_scopes WHERE kind IN ('subnet','security-group')`)
	exec("INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','create')")
	exec(`INSERT INTO cmp_instance_offerings (tenant_id,account_id,region,size) VALUES('tenant',$1,$2,'size-fixture')`, cloud.vendor, cloud.region)

	f := &imageDeleteFixture{cloud: cloud, id: id, status: imageStatus}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
			cloud.serve(t, w, r)
			return
		}
		if !f.serve(t, w, r) {
			t.Errorf("unexpected image request: %s %s %s", r.Method, r.Host, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
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
			profile.AllowImageDeletion = enabled.Load()
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
			t.Error("image deletion worker did not stop")
		}
	}()

	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := operations.ImageDeletion{Account: cloud.vendor, Region: cloud.region, AssetID: id}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/tenants/tenant/projects/"
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	submit := func(project, key, revision string, want int) operations.Operation {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, base+project+"/images/delete", bytes.NewReader(body))
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
			testsupport.DecodeProjectData(t, w, &op)
			if op.AssetID != id || op.Action != "delete-image" || op.ResourceKind != provider.Image ||
				w.Header().Get("Location") != base+project+"/operations/"+op.ID {
				t.Fatalf("wrong image deletion receipt: %+v", op)
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
	submit("project", "stale-image", `"2"`, http.StatusPreconditionFailed)
	submit("project", "missing-revision", "", http.StatusPreconditionFailed)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,links)
		SELECT scope_id,'instance-fixture','uses image','stopped',1,jsonb_build_array(jsonb_build_object('kind','image','id',$1::text,'relation','uses_image'))
		FROM cmp_inventory_scopes WHERE kind='instance'`, id)
	submit("project", "blocked-by-instance", `"1"`, http.StatusConflict)
	exec(`UPDATE cmp_inventory_objects SET removed_at=now() WHERE external_id='instance-fixture'`)

	launch := operations.CreateInput{Account: cloud.vendor, Region: cloud.region, InstanceSpec: provider.InstanceSpec{
		ImageID: id, Name: "uses-image", Size: "size-fixture", NetworkID: "subnet-fixture", SecurityGroup: "sg-fixture"}}
	pendingLaunch, err := store.SubmitCreation(ctx, actor, scope, "launch-before-delete", launch)
	if err != nil {
		t.Fatal(err)
	}
	submit("project", "blocked-by-launch", `"1"`, http.StatusConflict)
	approve(pendingLaunch, "reject")
	exec(`WITH published AS (
		INSERT INTO cmp_instance_catalog_items (tenant_id,project_id,template_id,version,revision,enabled)
		VALUES('tenant','project','fixture-template',1,1,true) RETURNING tenant_id,project_id,template_id,version
	) INSERT INTO cmp_instance_catalog_versions (tenant_id,project_id,template_id,version,title,account_id,region,image_id,size,network_id,security_group_id,published_by)
	SELECT tenant_id,project_id,template_id,version,'fixture template',$1,$2,$3,'size-fixture','subnet-fixture','sg-fixture','operator' FROM published`, cloud.vendor, cloud.region, id)
	submit("project", "blocked-by-catalog", `"1"`, http.StatusConflict)
	exec("UPDATE cmp_instance_catalog_items SET enabled=false WHERE template_id='fixture-template'")

	disabled := submit("project", "disabled", `"1"`, http.StatusAccepted)
	approve(disabled, "approve")
	if stopped := wait(disabled, "failed"); stopped.OutcomeReason != "image_deletion_disabled" || f.writes.Load() != 0 {
		t.Fatalf("disabled image deletion: %+v", stopped)
	}
	enabled.Store(true)
	f.unsafe.Store(true)
	protected := submit("project", "unsafe-image", `"1"`, http.StatusAccepted)
	approve(protected, "approve")
	if stopped := wait(protected, "failed"); stopped.OutcomeReason != "image_deletion_preconditions_unconfirmed" || f.writes.Load() != 0 {
		t.Fatalf("unsafe image deletion: %+v", stopped)
	}
	f.unsafe.Store(false)

	op := submit("project", "delete", `"1"`, http.StatusAccepted)
	transfer := inventory.OwnershipTransfer{Account: cloud.vendor, Region: cloud.region, Kind: provider.Image, AssetID: id, TargetProject: "other"}
	if _, err := inventory.NewStore(db).TransferOwnership(ctx, admin, scope, 1, transfer); !errors.Is(err, identity.ErrAccountConflict) {
		t.Fatalf("ownership changed for an image reserved for deletion: %v", err)
	}
	if _, err := store.SubmitCreation(ctx, actor, scope, "launch-during-delete", launch); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("launch used reserved image: %v", err)
	}
	submit("project", "duplicate", `"1"`, http.StatusConflict)
	if _, err := store.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("operator approved own deletion: %v", err)
	}
	detail := wait(op, "awaiting_approval")
	if detail.ImageDeletionIntent == nil || detail.ImageDeletionIntent.Name != "fixture image" || detail.ImageDeletionIntent.OwnershipRevision != 1 {
		t.Fatalf("incomplete approval detail: %+v", detail)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var reply bytes.Buffer
	args := []string{"delete-image", "tenant", "project", "--idempotency-key", "delete", "--if-match", "1"}
	if err := commands.RunOperationsClient(ctx, args, server.URL, actor, bytes.NewReader(body), &reply, server.Client().Transport); err != nil || !strings.Contains(reply.String(), op.ID) {
		t.Fatalf("CLI replay: %v %s", err, &reply)
	}
	approve(op, "approve")
	review := wait(op, "needs_review")
	wantReceipt := ""
	if cloud.vendor == provider.TencentCVM {
		wantReceipt = "fixture-image-receipt"
	}
	if f.writes.Load() != 1 || review.ProviderRequestID != wantReceipt {
		t.Fatalf("unexpected image deletion evidence: %+v", review)
	}
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
	assertSnapshotTombstone(t, db, id, false)
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "incident/image", ProviderRequestID: wantReceipt}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("accepted success without evidence: %v", err)
	}
	f.absent.Store(true)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "incident/image"}); err != nil {
		t.Fatal(err)
	}
	if confirmed := wait(op, "succeeded"); confirmed.OutcomeReason != "image_absence_confirmed" || f.writes.Load() != 1 || f.absences.Load() != 2 {
		t.Fatalf("absence observations or writes did not match: %+v", confirmed)
	}
	assertSnapshotTombstone(t, db, id, true)
	assertSnapshotTombstone(t, db, "source-snapshot", false)
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='image.operation.succeeded'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("image deletion outbox: count=%d err=%v", events, err)
	}
	if replay := submit("project", "delete", `"1"`, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("replay after deletion: %+v", replay)
	}
}

type imageDeleteFixture struct {
	cloud    *vpcCreationFixture
	id       string
	status   string
	unsafe   atomic.Bool
	absent   atomic.Bool
	writes   atomic.Int32
	absences atomic.Int32
}

func (f *imageDeleteFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	q := r.URL.Query()
	region := f.cloud.region
	read := false
	switch f.cloud.vendor {
	case provider.TencentCVM:
		if r.Host != "cvm.tencentcloudapi.com" || r.Method != http.MethodPost || r.Header.Get("X-TC-Region") != region ||
			r.Header.Get("X-TC-Version") != "2017-03-12" || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
			!validFixtureTencentSignature(r, raw, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		switch r.Header.Get("X-TC-Action") {
		case "DescribeImages":
			read = string(raw) == fmt.Sprintf(`{"ImageIds":[%q]}`, f.id)
		case "DeleteImages":
			return string(raw) == fmt.Sprintf(`{"ImageIds":[%q],"DeleteBindedSnap":false}`, f.id) && f.respondToDelete(t, w)
		}
	case provider.AlibabaECS:
		if r.Host != "ecs."+region+".aliyuncs.com" || r.Method != http.MethodPost ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
			!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) || q.Get("RegionId") != region || q.Get("ImageId") != f.id {
			return false
		}
		switch r.Header.Get("X-ACS-Action") {
		case "DescribeImages":
			read = len(q) == 5 && q.Get("ImageOwnerAlias") == "self" && q.Get("PageNumber") == "1" && q.Get("PageSize") == "10"
		case "DeleteImage":
			return len(q) == 3 && q.Get("Force") == "false" && f.respondToDelete(t, w)
		}
	case provider.HuaweiECS:
		if r.Host != "ims."+region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			return false
		}
		switch r.Method {
		case http.MethodGet:
			read = r.URL.Path == "/v2/cloudimages" && len(q) == 2 && q.Get("id") == f.id && q.Get("limit") == "2"
		case http.MethodDelete:
			return r.URL.Path == "/v2/images/"+f.id && len(q) == 0 && string(raw) == `{"delete_backup":false}` && f.respondToDelete(t, w)
		}
	}
	if !read {
		return false
	}

	afterWrite, absent := f.writes.Load() > 0, f.absent.Load()
	if afterWrite && absent {
		f.absences.Add(1)
	}
	switch f.cloud.vendor {
	case provider.TencentCVM:
		status, imageType := f.status, "PRIVATE_IMAGE"
		if f.unsafe.Load() {
			status = "CREATING"
		}
		if afterWrite {
			imageType = "SHARED_IMAGE"
		}
		if absent {
			testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"Error": map[string]string{"Code": "InvalidImageId.NotFound"}}})
			return true
		}
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"TotalCount": 1,
			"ImageSet": []any{map[string]string{"ImageId": f.id, "ImageType": imageType, "ImageState": status}}}})
	case provider.AlibabaECS:
		owner, usage := "self", "none"
		if f.unsafe.Load() {
			usage = "instance"
		}
		if afterWrite {
			owner = "others"
		}
		rows := []any{}
		if !absent {
			rows = append(rows, map[string]string{"ImageId": f.id, "ImageOwnerAlias": owner, "Status": f.status, "Usage": usage, "IsSelfShared": "false"})
		}
		testsupport.WriteJSON(t, w, map[string]any{"RegionId": region, "PageNumber": 1, "PageSize": 10, "TotalCount": len(rows),
			"Images": map[string]any{"Image": rows}})
	case provider.HuaweiECS:
		owner := fixtureHuaweiProject
		if afterWrite {
			owner = "foreign-project"
		}
		rows := []any{}
		if !absent {
			rows = append(rows, map[string]any{"id": f.id, "owner": owner, "status": f.status, "protected": f.unsafe.Load(), "visibility": "private", "__imagetype": "private"})
		}
		next := ""
		if !absent {
			next = f.id
		}
		testsupport.WriteJSON(t, w, map[string]any{"images": rows, "page_info": map[string]string{"next_marker": next}})
	}
	return true
}

func (f *imageDeleteFixture) respondToDelete(t *testing.T, w http.ResponseWriter) bool {
	t.Helper()
	if f.writes.Add(1) != 1 {
		return false
	}
	switch f.cloud.vendor {
	case provider.TencentCVM:
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]string{"RequestId": "fixture-image-receipt"}})
		return true
	case provider.HuaweiECS:
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Error(err)
		return false
	}
	if err := conn.Close(); err != nil {
		t.Error(err)
	}
	return true
}
