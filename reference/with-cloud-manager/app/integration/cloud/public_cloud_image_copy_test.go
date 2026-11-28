package cloudtest

import (
	"bytes"
	"context"
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
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

const (
	copyImageName     = "fixture-image-copy"
	copyTargetProject = "0987654321abcdef0987654321abcdef"
	copyTargetToken   = "fixture-huawei-image-target-token"
	copyAgency        = "fixture_ims_copy"
	copyJob           = "8a12fc664fb4daa3014fb4e581380006"
	copyReceipt       = "image-copy-fixture-receipt"
)

func TestAcceptancePublicCloudImageCopy(t *testing.T) {
	for _, f := range []*imageCopyFixture{
		{cloud: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"}, to: "ap-shanghai", source: "img-source", target: "img-target", state: "NORMAL"},
		{cloud: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"}, to: "ap-shanghai", source: "img-source", target: "img-target", state: "NORMAL", lost: true},
		{cloud: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"}, to: "cn-shanghai", source: "m-source", target: "m-target", state: "Available", lost: true},
		{cloud: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"}, to: "cn-east-3",
			source: "40000000-0000-4000-8000-000000000010", target: "40000000-0000-4000-8000-000000000020", state: "active"},
		{cloud: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"}, to: "cn-east-3",
			source: "40000000-0000-4000-8000-000000000010", target: "40000000-0000-4000-8000-000000000020", state: "active", lost: true},
	} {
		name := f.cloud.vendor
		if f.lost {
			name += "/lost-receipt"
		}
		t.Run(name, func(t *testing.T) { testPublicCloudImageCopy(t, f) })
	}
}

func testPublicCloudImageCopy(t *testing.T, f *imageCopyFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-image-copy-operator-token", "fixture-image-copy-reviewer-token"
	cloud := &f.cloud
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cloud.vendor, region: cloud.region,
		volume: "unused-volume", id: "unused-snapshot", class: "standard"}, cloud.remoteIdentity(), actor, reviewer)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	targetAccount := cloud.vendor
	targetIdentity := cloud.remoteIdentity()
	if cloud.vendor == provider.HuaweiECS {
		targetAccount, targetIdentity = "destination-account", cloud.account+":"+copyTargetProject
		exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant',$1,'project',$2,'fixture/copy-target',$3,'write_enabled')`, targetAccount, cloud.vendor, targetIdentity)
	}
	exec(`INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','outside','other',$1,'fixture/copy-outside','outside-identity','write_enabled')`, cloud.vendor)
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'image',1,now())`, cloud.vendor, cloud.region)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$1,'source image',$2,1 FROM cmp_inventory_scopes WHERE kind='image'`, f.source, f.state)

	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" {
			cloud.serve(t, w, r)
			return
		}
		if !f.serve(t, w, r) {
			t.Errorf("unexpected image copy fixture request: %s %s %s", r.Method, r.Host, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer remote.Close()
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	var enabled atomic.Bool
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if binding.Provider != cloud.vendor || (binding.RemoteIdentity != cloud.remoteIdentity() && binding.RemoteIdentity != targetIdentity) {
				return nil, identity.ErrForbidden
			}
			profile := publiccloud.Config{AccountID: cloud.account, Regions: []string{cloud.region, f.to}, AllowImageCopy: enabled.Load()}
			if cloud.vendor == provider.HuaweiECS {
				profile.Regions = []string{cloud.region}
				profile.ProjectID, profile.IAMToken, profile.HuaweiImageCopyAgency = fixtureHuaweiProject, fixtureHuaweiIAM, copyAgency
				if binding.RemoteIdentity == targetIdentity {
					profile.Regions = []string{f.to}
					profile.ProjectID, profile.IAMToken, profile.HuaweiImageCopyAgency = copyTargetProject, copyTargetToken, ""
				}
			} else {
				profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
			}
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
			t.Error("image copy worker did not stop")
		}
	}()

	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := operations.ImageCopy{Account: cloud.vendor, Region: cloud.region, DestinationAccount: targetAccount,
		DestinationRegion: f.to, ImageCopySpec: provider.ImageCopySpec{ImageID: f.source, Name: copyImageName}}
	body := func(data operations.ImageCopy) []byte {
		t.Helper()
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	const route = "/v1/tenants/tenant/projects/project"
	submit := func(key, revision string, data operations.ImageCopy, expected int) operations.Operation {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, route+"/images/copy", bytes.NewReader(body(data)))
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", revision)
		res := httptest.NewRecorder()
		app.ServeHTTP(res, r)
		if res.Code != expected {
			t.Fatalf("copy %s: status=%d want=%d: %s", key, res.Code, expected, res.Body)
		}
		var op operations.Operation
		if expected == http.StatusAccepted {
			testsupport.DecodeProjectData(t, res, &op)
			if op.ResourceKind != provider.Image || op.Action != "copy-image" || op.Account != targetAccount || op.Region != f.to ||
				res.Header().Get("Location") != route+"/operations/"+op.ID {
				t.Fatalf("copy operation receipt: %+v", op)
			}
		}
		return op
	}
	wait := func(op operations.Operation, next string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, next, true)
	}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	submit("stale", `"2"`, input, http.StatusPreconditionFailed)
	foreign := input
	foreign.DestinationAccount = "outside"
	submit("foreign", `"1"`, foreign, http.StatusForbidden)
	same := input
	same.DestinationRegion = input.Region
	submit("same-region", `"1"`, same, http.StatusBadRequest)
	deleteInput := operations.ImageDeletion{Account: input.Account, Region: input.Region, AssetID: input.ImageID}
	pendingDelete, err := store.SubmitImageDeletion(ctx, actor, scope, "delete-before-copy", 1, deleteInput)
	if err != nil {
		t.Fatal(err)
	}
	submit("copy-during-delete", `"1"`, input, http.StatusConflict)
	if _, err := store.Approve(ctx, reviewer, scope, pendingDelete.ID, operations.Approval{Decision: "reject"}); err != nil {
		t.Fatal(err)
	}
	disabled := submit("disabled", `"1"`, input, http.StatusAccepted)
	approve(disabled)
	if blocked := wait(disabled, "failed"); blocked.OutcomeReason != "provider_creation_blocked" {
		t.Fatalf("disabled copy not blocked: %+v", blocked)
	}
	enabled.Store(true)
	op := submit("copy", `"1"`, input, http.StatusAccepted)
	submit("concurrent", `"1"`, input, http.StatusConflict)
	if _, err := store.SubmitImageDeletion(ctx, actor, scope, "delete", 1, deleteInput); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("source deletion not blocked: %v", err)
	}
	if _, err := store.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester approved own copy: %v", err)
	}
	detail := wait(op, "awaiting_approval")
	if detail.ImageCopyIntent == nil || detail.ImageCopyIntent.SourceRevision != 1 || detail.ImageCopyIntent.ImageCopy != input {
		t.Fatalf("unexpected approval copy intent: %+v", detail)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var reply bytes.Buffer
	args := []string{"copy-image", "tenant", "project", "--if-match", "1", "--idempotency-key", "copy"}
	if err := commands.RunOperationsClient(ctx, args, server.URL, actor, bytes.NewReader(body(input)), &reply, server.Client().Transport); err != nil || !strings.Contains(reply.String(), op.ID) {
		t.Fatalf("CLI durable replay: %v %s", err, &reply)
	}
	approve(op)
	review := wait(op, "needs_review")
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
	noTencentProof := cloud.vendor == provider.TencentCVM && f.lost
	if review.AssetID != "" || review.ImageCopyIntent == nil || review.ImageCopyIntent.CanRetryObservation == noTencentProof || f.writes.Load() != 1 {
		t.Fatalf("unknown operation state: %+v", review)
	}
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "succeeded", EvidenceRef: "case/manual",
		ProviderRequestID: review.ProviderRequestID}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("manual success without vendor proof accepted: %v", err)
	}

	retry := operations.ObservationRetry{EvidenceRef: "case/verified"}
	if noTencentProof {
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, retry); !errors.Is(err, operations.ErrConflict) {
			t.Fatalf("receipt-less Tencent operation was requeued: %v", err)
		}
		if _, err := store.Resolve(ctx, reviewer, scope, op.ID, operations.Resolution{Decision: "failed", EvidenceRef: "case/no-evidence"}); err != nil {
			t.Fatal(err)
		}
		wait(op, "failed")
	} else {
		// 两端各自轮换后，独立审批人可授权用新凭据继续读取。
		if cloud.vendor == provider.HuaweiECS {
			exec(`UPDATE cmp_cloud_accounts SET credential_ref=credential_ref||'-rotated',credential_revision=credential_revision+1
				WHERE account_id IN ($1,$2)`, input.Account, targetAccount)
			if f.lost {
				exec(`INSERT INTO cmp_asset_tag_assignments(tenant_id,scope_id,external_id,state,revision)
					SELECT tenant_id,scope_id,$1,'inherited',2 FROM cmp_inventory_scopes WHERE kind='image' AND account_id=$2`, f.source, input.Account)
				if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "case/tag-drift"}); err != nil {
					t.Fatal(err)
				}
				if blocked := wait(op, "needs_review"); blocked.OutcomeReason != "observation_unavailable" {
					t.Fatalf("changed tag mapping accepted after rotation: %+v", blocked)
				}
				waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
				exec("DELETE FROM cmp_asset_tag_assignments WHERE external_id=$1", f.source)
			}
		}
		f.correct.Store(true)
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, retry); err != nil {
			t.Fatal(err)
		}
		success := wait(op, "succeeded")
		if success.AssetID != f.target || f.writes.Load() != 1 {
			t.Fatalf("copy result mismatch: %+v", success)
		}
		var created, events int
		if err := db.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM cmp_inventory_scopes AS s JOIN cmp_inventory_objects AS o USING(scope_id)
			 JOIN cmp_asset_ownership AS own USING(scope_id,external_id)
			 WHERE (s.account_id,s.region,s.kind,o.external_id,own.project_id)=($1,$2,'image',$3,'project') AND o.links='[]'::jsonb),
			(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='image.operation.succeeded')`,
			targetAccount, f.to, f.target).Scan(&created, &events); err != nil || created != 1 || events != 1 {
			t.Fatalf("destination asset or outbox missing: assets=%d events=%d err=%v", created, events, err)
		}
	}
	var reservations int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_image_copy_reservations`).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatalf("terminal copy held reserve: count=%d err=%v", reservations, err)
	}
	if replay := submit("copy", `"1"`, input, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("terminal request replayed a new operation: %+v", replay)
	}
}

type imageCopyFixture struct {
	cloud                     vpcCreationFixture
	to, source, target, state string
	lost                      bool
	correct                   atomic.Bool
	writes                    atomic.Int32
	marker                    atomic.Value
}

func (f *imageCopyFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	switch f.cloud.vendor {
	case provider.TencentCVM:
		return f.tencent(t, w, r, raw)
	case provider.AlibabaECS:
		return f.alibaba(t, w, r)
	default:
		return f.huawei(t, w, r, raw)
	}
}

func (f *imageCopyFixture) tencent(t *testing.T, w http.ResponseWriter, r *http.Request, raw []byte) bool {
	t.Helper()
	region := r.Header.Get("X-TC-Region")
	if r.Host != "cvm.tencentcloudapi.com" || r.Method != http.MethodPost || r.Header.Get("X-TC-Version") != "2017-03-12" ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, raw, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var payload map[string]any
	switch r.Header.Get("X-TC-Action") {
	case "DescribeImages":
		id, name := f.source, "source image"
		if region == f.to && !f.lost && f.writes.Load() == 1 {
			id, name = f.target, copyImageName
			if !f.correct.Load() {
				name = "wrong-image"
			}
		} else if region != f.cloud.region {
			return false
		}
		if string(raw) != fmt.Sprintf(`{"ImageIds":[%q]}`, id) {
			return false
		}
		payload = map[string]any{"TotalCount": 1, "ImageSet": []any{map[string]string{
			"ImageId": id, "ImageName": name, "ImageType": "PRIVATE_IMAGE", "ImageState": f.state}}}
	case "SyncImages":
		expected := fmt.Sprintf(`{"ImageIds":[%q],"DestinationRegions":[%q],"ImageName":%q,"ImageSetRequired":true}`, f.source, f.to, copyImageName)
		if region != f.cloud.region || string(raw) != expected || f.writes.Add(1) != 1 {
			return false
		}
		if f.lost {
			return dropImageCreationReply(t, w)
		}
		payload = map[string]any{"RequestId": copyReceipt, "ImageSet": []any{map[string]string{"Region": f.to, "ImageId": f.target}}}
	default:
		return false
	}
	testsupport.WriteJSON(t, w, map[string]any{"Response": payload})
	return true
}

func (f *imageCopyFixture) alibaba(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	q := r.URL.Query()
	region := q.Get("RegionId")
	if r.Host != "ecs."+region+".aliyuncs.com" || r.Method != http.MethodPost ||
		r.Header.Get("X-ACS-Version") != "2014-05-26" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	switch r.Header.Get("X-ACS-Action") {
	case "DescribeImages":
		row := map[string]any{"ImageId": f.source, "Status": f.state, "ImageOwnerAlias": "self", "Usage": "instance"}
		if region == f.to {
			marker, _ := f.marker.Load().(string)
			if marker == "" || q.Get("Tag.1.Key") != fixtureImageMarkerKey || q.Get("Tag.1.Value") != marker {
				return false
			}
			row = map[string]any{"ImageId": f.target, "ImageName": copyImageName, "Status": f.state, "ImageOwnerAlias": "self",
				"Description": f.proof(marker), "Tags": map[string]any{"Tag": []any{map[string]string{"TagKey": fixtureImageMarkerKey, "TagValue": marker}}}}
		} else if region != f.cloud.region || q.Get("ImageId") != f.source || q.Get("ImageOwnerAlias") != "self" {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"RegionId": region, "PageNumber": 1, "PageSize": 10, "TotalCount": 1,
			"Images": map[string]any{"Image": []any{row}}})
	case "CopyImage":
		marker := q.Get("DestinationDescription")
		if region != f.cloud.region || q.Get("DestinationRegionId") != f.to || q.Get("ImageId") != f.source ||
			q.Get("DestinationImageName") != copyImageName || q.Get("Tag.1.Key") != fixtureImageMarkerKey || q.Get("Tag.1.Value") != marker ||
			q.Get("ClientToken") == "" || q.Has("Encrypted") || len(q) != 8 || !f.recordMarker(marker) {
			return false
		}
		return dropImageCreationReply(t, w)
	default:
		return false
	}
	return true
}

func (f *imageCopyFixture) huawei(t *testing.T, w http.ResponseWriter, r *http.Request, raw []byte) bool {
	t.Helper()
	auth := r.Header.Get("X-Auth-Token")
	if r.Host == "iam.myhuaweicloud.com" {
		project, name := fixtureHuaweiProject, f.cloud.region
		if auth == copyTargetToken {
			project, name = copyTargetProject, "project-east"
		} else if auth != fixtureHuaweiIAM {
			return false
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v3/auth/tokens" || r.Header.Get("X-Subject-Token") != auth {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"token": map[string]any{"expires_at": time.Now().Add(time.Hour).UTC(),
			"project": map[string]any{"id": project, "name": name, "domain": map[string]string{"id": f.cloud.account}}}})
		return true
	}
	source := r.Host == "ims."+f.cloud.region+".myhuaweicloud.com" && auth == fixtureHuaweiIAM
	destination := r.Host == "ims."+f.to+".myhuaweicloud.com" && auth == copyTargetToken
	q := r.URL.Query()
	switch {
	case source && r.Method == http.MethodGet && r.URL.Path == "/v2/cloudimages":
		if q.Get("id") != f.source {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"images": []any{map[string]any{"id": f.source, "owner": fixtureHuaweiProject,
			"__imagetype": "private", "visibility": "private", "status": "active", "__whole_image": "false", "disk_format": "zvhd2", "virtual_env_type": "FusionCompute"}}})
	case source && r.Method == http.MethodPost && r.URL.Path == "/v1/cloudimages/"+f.source+"/cross_region_copy":
		var input map[string]string
		if json.Unmarshal(raw, &input) != nil || len(input) != 5 || input["name"] != copyImageName ||
			input["region"] != f.to || input["project_name"] != "project-east" || input["agency_name"] != copyAgency || !f.recordMarker(input["description"]) {
			return false
		}
		if f.lost {
			return dropImageCreationReply(t, w)
		}
		testsupport.WriteJSON(t, w, map[string]string{"job_id": copyJob})
	case source && !f.lost && r.Method == http.MethodGet && r.URL.Path == "/v1/cloudimages/job/"+copyJob:
		testsupport.WriteJSON(t, w, map[string]any{"job_id": copyJob, "job_type": "imsCrossRegionCopyImageJob", "status": "SUCCESS",
			"entities": map[string]string{"image_name": copyImageName, "image_id": f.target}})
	case destination && r.Method == http.MethodGet && r.URL.Path == "/v2/cloudimages":
		marker, _ := f.marker.Load().(string)
		if marker == "" || q.Get("owner") != copyTargetProject || q.Get("name") != copyImageName || q.Get("limit") != "100" ||
			(f.lost && q.Has("id")) || (!f.lost && q.Get("id") != f.target) {
			return false
		}
		items := []any{map[string]string{"id": f.target, "owner": copyTargetProject, "__imagetype": "private", "visibility": "private",
			"status": f.state, "name": copyImageName, "__description": f.proof(marker)}}
		if f.lost && !f.correct.Load() {
			items = append(items, map[string]string{"id": "40000000-0000-4000-8000-000000000030", "owner": fixtureHuaweiProject, "name": copyImageName})
		}
		if q.Get("marker") == f.target {
			testsupport.WriteJSON(t, w, map[string]any{"images": []any{}})
			return true
		}
		next := ""
		if f.lost && f.correct.Load() {
			next = f.target
		}
		testsupport.WriteJSON(t, w, map[string]any{"images": items, "page_info": map[string]string{"next_marker": next}})
	default:
		return false
	}
	return true
}

func (f *imageCopyFixture) recordMarker(marker string) bool {
	if len(marker) != 64 || f.writes.Add(1) != 1 {
		return false
	}
	f.marker.Store(marker)
	return true
}

func (f *imageCopyFixture) proof(marker string) string {
	if f.correct.Load() {
		return marker
	}
	return "wrong-marker"
}
