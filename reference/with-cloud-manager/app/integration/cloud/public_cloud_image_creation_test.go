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
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudImageCreation(t *testing.T) {
	for _, f := range []*cloudImageCreateFixture{
		{cloud: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"}, source: "ins-fixture", image: "img-fixture", stopped: "STOPPED", ready: "NORMAL"},
		{cloud: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"}, source: "i-fixture", image: "m-fixture", stopped: "Stopped", ready: "Available"},
		{cloud: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			source: "30000000-0000-4000-8000-000000000010", image: "40000000-0000-4000-8000-000000000010", stopped: "SHUTOFF", ready: "active"},
	} {
		t.Run(f.cloud.vendor, func(t *testing.T) { testPublicCloudImageCreation(t, f) })
		if f.cloud.vendor == provider.HuaweiECS {
			lost := &cloudImageCreateFixture{cloud: vpcCreationFixture{vendor: f.cloud.vendor, account: f.cloud.account, region: f.cloud.region},
				source: f.source, image: f.image, stopped: f.stopped, ready: f.ready, loseJob: true}
			t.Run(f.cloud.vendor+"/lost-job", func(t *testing.T) { testPublicCloudImageCreation(t, lost) })
		}
	}
}

const fixtureCreatedImageName = "fixture-system-image"
const fixtureImageMarkerKey = "plumego-cmp-image"
const fixtureImageJob = "8a12fc664fb4daa3014fb4e581380005"

func testPublicCloudImageCreation(t *testing.T, f *cloudImageCreateFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-image-create-operator-token", "fixture-image-create-reviewer-token"
	cloud := &f.cloud
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cloud.vendor, region: cloud.region,
		volume: "unused-volume", id: "unused-snapshot", class: "standard"}, cloud.remoteIdentity(), actor, reviewer)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'instance',1,now()),('tenant',$1,$2,'image',1,now())`, cloud.vendor, cloud.region)
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$1,'source instance',$2,1 FROM cmp_inventory_scopes WHERE kind='instance'`, f.source, f.stopped)
	exec("INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','start')")

	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
			cloud.serve(t, w, r)
			return
		}
		if !f.serve(t, w, r) {
			t.Errorf("unexpected image creation fixture request: %s %s %s", r.Method, r.Host, r.URL.Path)
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
			profile.AllowImageCreation = enabled.Load()
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
			t.Error("image creation worker did not stop")
		}
	}()
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	input := operations.ImageCreation{Account: cloud.vendor, Region: cloud.region,
		ImageSpec: provider.ImageSpec{InstanceID: f.source, Name: fixtureCreatedImageName}}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/tenants/tenant/projects/"
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	submit := func(project, key, revision string, want int) operations.Operation {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, base+project+"/images", bytes.NewReader(body))
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
			if op.Action != "create-image" || op.ResourceKind != provider.Image ||
				w.Header().Get("Location") != base+project+"/operations/"+op.ID {
				t.Fatalf("wrong image creation receipt: %+v", op)
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
	power := operations.Input{Account: cloud.vendor, Region: cloud.region, AssetID: f.source, Action: "start"}
	submit("other", "wrong-project", `"1"`, http.StatusForbidden)
	submit("project", "stale-source", `"2"`, http.StatusPreconditionFailed)
	submit("project", "missing-version", "", http.StatusPreconditionFailed)
	pendingPower, err := store.Submit(ctx, actor, scope, "power-before-image", 1, power)
	if err != nil {
		t.Fatal(err)
	}
	submit("project", "instance-busy", `"1"`, http.StatusConflict)
	approve(pendingPower, "reject")
	if cloud.vendor == provider.TencentCVM {
		type submission struct {
			op  operations.Operation
			err error
		}
		concurrent := make(chan submission, 2)
		begin := make(chan struct{})
		submitCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		go func() {
			<-begin
			op, err := store.SubmitImageCreation(submitCtx, actor, scope, "concurrent-image", 1, input)
			concurrent <- submission{op, err}
		}()
		go func() {
			<-begin
			op, err := store.Submit(submitCtx, actor, scope, "concurrent-start", 1, power)
			concurrent <- submission{op, err}
		}()
		close(begin)
		var winner operations.Operation
		var conflicts int
		for range 2 {
			result := <-concurrent
			switch {
			case errors.Is(result.err, operations.ErrConflict):
				conflicts++
			case result.err != nil:
				t.Fatal(result.err)
			default:
				winner = result.op
			}
		}
		if conflicts != 1 || winner.Status != "awaiting_approval" {
			t.Fatalf("overlapping image and instance submissions: conflicts=%d winner=%+v", conflicts, winner)
		}
		approve(winner, "reject")
	}

	disabled := submit("project", "disabled", `"1"`, http.StatusAccepted)
	approve(disabled, "approve")
	wait(disabled, "failed")
	enabled.Store(true)
	f.unsafeSource.Store(true)
	unsafe := submit("project", "unsafe-source", `"1"`, http.StatusAccepted)
	approve(unsafe, "approve")
	if stopped := wait(unsafe, "failed"); stopped.OutcomeReason != "provider_creation_blocked" || f.writes.Load() != 0 {
		t.Fatalf("unsafe source was dispatched: %+v", stopped)
	}
	f.unsafeSource.Store(false)

	op := submit("project", "image", `"1"`, http.StatusAccepted)
	submit("project", "source-reserved", `"1"`, http.StatusConflict)
	if _, err := store.Submit(ctx, actor, scope, "power-during-image", 1, power); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("power operation accepted on reserved source: %v", err)
	}
	if preview, err := store.PreviewDeletion(ctx, actor, scope, power); err != nil || !preview.ActiveImageCreation {
		t.Fatalf("instance preview missed reservation: %+v %v", preview, err)
	}
	if _, err := store.Approve(ctx, actor, scope, op.ID, operations.Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("operator approved own image: %v", err)
	}
	detail := wait(op, "awaiting_approval")
	if detail.ImageCreationIntent == nil || detail.ImageCreationIntent.InstanceID != f.source || detail.ImageCreationIntent.InstanceRevision != 1 {
		t.Fatalf("incomplete approval detail: %+v", detail)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var reply bytes.Buffer
	args := []string{"create-image", "tenant", "project", "--idempotency-key", "image", "--if-match", "1"}
	if err := commands.RunOperationsClient(ctx, args, server.URL, actor, bytes.NewReader(body), &reply, server.Client().Transport); err != nil || !strings.Contains(reply.String(), op.ID) {
		t.Fatalf("CLI replay: %v %s", err, &reply)
	}

	// 盘点可能在云端回执确认前先发现镜像。
	exec(`INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$1,$2,'creating',1 FROM cmp_inventory_scopes WHERE kind='image'`, f.image, fixtureCreatedImageName)
	approve(op, "approve")
	review := wait(op, "needs_review")
	waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
	if review.AssetID != "" || f.writes.Load() != 1 {
		t.Fatalf("mismatched proof was adopted or mutation was resent: %+v", review)
	}
	if _, err := store.Submit(ctx, actor, scope, "power-during-review", 1, power); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("unknown image outcome released the source: %v", err)
	}
	f.validProof.Store(true)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "incident/created-image"}); err != nil {
		t.Fatal(err)
	}
	if created := wait(op, "succeeded"); created.AssetID != f.image || f.writes.Load() != 1 {
		t.Fatalf("read-only recovery did not confirm image: %+v", created)
	}
	itemStore := inventory.NewStore(db)
	inventoryKey := inventory.Key{Tenant: "tenant", Account: cloud.vendor, Region: cloud.region, Kind: provider.Image}
	claim, err := itemStore.Claim(ctx, inventoryKey, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	discovery := imageDiscovery(func() provider.Page {
		return provider.Page{Objects: []provider.Object{{ID: f.image, Name: fixtureCreatedImageName, Status: f.ready}}}
	})
	if _, _, err := itemStore.ScanPage(ctx, claim, discovery); err != nil {
		t.Fatal(err)
	}
	var status, sourceID string
	var events, reserved int
	if err := db.QueryRowContext(ctx, `SELECT o.status,o.links->0->>'id',
		(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='image.operation.succeeded'),
		(SELECT count(*) FROM cmp_instance_image_reservations)
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes USING(scope_id)
		WHERE kind='image' AND external_id=$1`, f.image).Scan(&status, &sourceID, &events, &reserved); err != nil || status != f.ready || sourceID != f.source || events != 1 || reserved != 0 {
		t.Fatalf("image evidence lost: status=%q source=%q events=%d reservations=%d err=%v", status, sourceID, events, reserved, err)
	}
}

type imageDiscovery func() provider.Page

func (d imageDiscovery) Discover(context.Context, provider.Scope, string) (provider.Page, error) {
	return d(), nil
}

type cloudImageCreateFixture struct {
	cloud                         vpcCreationFixture
	source, image, stopped, ready string
	unsafeSource, validProof      atomic.Bool
	writes                        atomic.Int32
	marker                        atomic.Value
	loseJob                       bool
}

func (f *cloudImageCreateFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
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

func (f *cloudImageCreateFixture) tencent(t *testing.T, w http.ResponseWriter, r *http.Request, raw []byte) bool {
	t.Helper()
	if r.Host != "cvm.tencentcloudapi.com" || r.Method != http.MethodPost || r.Header.Get("X-TC-Region") != f.cloud.region ||
		r.Header.Get("X-TC-Version") != "2017-03-12" || r.Header.Get("X-TC-Token") != fixtureLaunchSession ||
		!validFixtureTencentSignature(r, raw, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	send := func(value map[string]any) { testsupport.WriteJSON(t, w, map[string]any{"Response": value}) }
	switch r.Header.Get("X-TC-Action") {
	case "DescribeInstances":
		if string(raw) != fmt.Sprintf(`{"InstanceIds":[%q]}`, f.source) {
			return false
		}
		status := f.stopped
		if f.unsafeSource.Load() {
			status = "RUNNING"
		}
		send(map[string]any{"TotalCount": 1, "InstanceSet": []any{map[string]any{"InstanceId": f.source,
			"InstanceState": status, "SystemDisk": map[string]string{"DiskId": "disk-fixture"}}}})
	case "CreateImage":
		var input struct {
			InstanceId, ImageName, ImageDescription, ForcePoweroff string
			TagSpecification                                       []struct {
				ResourceType string
				Tags         []struct{ Key, Value string }
			}
		}
		if json.Unmarshal(raw, &input) != nil || input.InstanceId != f.source || input.ImageName != fixtureCreatedImageName ||
			input.ForcePoweroff != "false" || len(input.TagSpecification) != 1 || input.TagSpecification[0].ResourceType != "image" ||
			len(input.TagSpecification[0].Tags) != 1 || input.TagSpecification[0].Tags[0].Key != fixtureImageMarkerKey ||
			input.TagSpecification[0].Tags[0].Value != input.ImageDescription || !f.recordWrite(input.ImageDescription) {
			return false
		}
		send(map[string]any{"ImageId": f.image, "RequestId": "image-fixture-receipt"})
	case "DescribeImages":
		marker, _ := f.marker.Load().(string)
		expected := fmt.Sprintf(`{"Filters":[{"Name":"image-type","Values":["PRIVATE_IMAGE"]},{"Name":"tag:%s","Values":[%q]}],"Limit":2}`, fixtureImageMarkerKey, marker)
		if marker == "" || string(raw) != expected {
			return false
		}
		send(map[string]any{"TotalCount": 1, "ImageSet": []any{map[string]any{"ImageId": f.image, "ImageName": fixtureCreatedImageName,
			"ImageDescription": f.description(marker), "ImageType": "PRIVATE_IMAGE", "ImageState": f.ready,
			"Tags": []any{map[string]string{"Key": fixtureImageMarkerKey, "Value": marker}}}}})
	default:
		return false
	}
	return true
}

func (f *cloudImageCreateFixture) alibaba(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	region := f.cloud.region
	query := r.URL.Query()
	if r.Host != "ecs."+region+".aliyuncs.com" || r.Method != http.MethodPost || r.Header.Get("X-ACS-Version") != "2014-05-26" ||
		r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession || query.Get("RegionId") != region ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	switch r.Header.Get("X-ACS-Action") {
	case "DescribeInstances":
		if query.Get("InstanceIds") != fmt.Sprintf("[%q]", f.source) {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"Instances": map[string]any{"Instance": []any{
			map[string]string{"InstanceId": f.source, "Status": f.stopped, "RegionId": region}}}})
	case "DescribeDisks":
		if query.Get("InstanceId") != f.source {
			return false
		}
		disks := []any{map[string]string{"DiskId": "d-system", "Type": "system", "Status": "In_use", "Category": "cloud_essd",
			"RegionId": region, "InstanceId": f.source}}
		if f.unsafeSource.Load() {
			disks = append(disks, map[string]string{"DiskId": "d-data", "Type": "data", "InstanceId": f.source})
		}
		testsupport.WriteJSON(t, w, map[string]any{"Disks": map[string]any{"Disk": disks}})
	case "CreateImage":
		marker := query.Get("Description")
		if len(query) != 7 || query.Get("InstanceId") != f.source || query.Get("ImageName") != fixtureCreatedImageName ||
			query.Get("ClientToken") == "" || query.Get("Tag.1.Key") != fixtureImageMarkerKey || query.Get("Tag.1.Value") != marker ||
			!f.recordWrite(marker) {
			return false
		}
		return dropImageCreationReply(t, w)
	case "DescribeImages":
		marker, _ := f.marker.Load().(string)
		if marker == "" || len(query) != 6 || query.Get("ImageOwnerAlias") != "self" || query.Get("PageNumber") != "1" ||
			query.Get("PageSize") != "10" || query.Get("Tag.1.Key") != fixtureImageMarkerKey || query.Get("Tag.1.Value") != marker {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"RegionId": region, "PageNumber": 1, "PageSize": 10, "TotalCount": 1,
			"Images": map[string]any{"Image": []any{map[string]any{"ImageId": f.image, "ImageName": fixtureCreatedImageName,
				"Description": f.description(marker), "ImageOwnerAlias": "self", "Status": f.ready,
				"Tags": map[string]any{"Tag": []any{map[string]string{"TagKey": fixtureImageMarkerKey, "TagValue": marker}}}}}}})
	default:
		return false
	}
	return true
}

func (f *cloudImageCreateFixture) huawei(t *testing.T, w http.ResponseWriter, r *http.Request, raw []byte) bool {
	t.Helper()
	region := f.cloud.region
	if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	if r.Method == http.MethodGet && r.URL.Path == "/v1/"+fixtureHuaweiProject+"/cloudservers/"+f.source {
		if r.Host != "ecs."+region+".myhuaweicloud.com" {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"server": map[string]any{
			"id": f.source, "tenant_id": fixtureHuaweiProject, "status": f.stopped, "locked": f.unsafeSource.Load(),
			"os-extended-volumes:volumes_attached": []any{map[string]string{"id": "60000000-0000-4000-8000-000000000010", "bootIndex": "0"}},
		}})
		return true
	}
	if r.Host != "ims."+region+".myhuaweicloud.com" {
		return false
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v2.1/cloudimages/action":
		var input struct {
			Name, Description string
			InstanceID        string                        `json:"instance_id"`
			Tags              []struct{ Key, Value string } `json:"image_tags"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil || input.Name != fixtureCreatedImageName || input.InstanceID != f.source ||
			len(input.Tags) != 1 || input.Tags[0].Key != fixtureImageMarkerKey || input.Tags[0].Value != input.Description ||
			!f.recordWrite(input.Description) {
			return false
		}
		if f.loseJob {
			return dropImageCreationReply(t, w)
		}
		testsupport.WriteJSON(t, w, map[string]string{"job_id": fixtureImageJob})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/cloudimages/job/"+fixtureImageJob:
		jobType := "imsCreateImageByInstance"
		if !f.validProof.Load() {
			jobType = "imsCreateWholeImageByInstanceJob"
		}
		testsupport.WriteJSON(t, w, map[string]any{"job_id": fixtureImageJob, "job_type": jobType, "status": "SUCCESS",
			"entities": map[string]string{"image_id": f.image, "image_name": fixtureCreatedImageName}})
	case r.Method == http.MethodGet && r.URL.Path == "/v2/cloudimages":
		query := r.URL.Query()
		marker, _ := f.marker.Load().(string)
		if marker == "" || query.Get("owner") != fixtureHuaweiProject ||
			query.Get("tag") != fixtureImageMarkerKey+"="+marker || query.Get("limit") != "2" {
			return false
		}
		if f.loseJob {
			if query.Has("id") {
				return false
			}
			if query.Get("marker") == f.image {
				testsupport.WriteJSON(t, w, map[string]any{"images": []any{}})
				return true
			}
			if len(query) != 3 {
				return false
			}
		} else if len(query) != 4 || query.Get("id") != f.image {
			return false
		}
		testsupport.WriteJSON(t, w, map[string]any{"images": []any{map[string]string{"id": f.image, "name": fixtureCreatedImageName,
			"__description": f.description(marker), "owner": fixtureHuaweiProject, "__imagetype": "private", "visibility": "private", "status": f.ready}},
			"page_info": map[string]string{"next_marker": f.image}})
	default:
		return false
	}
	return true
}

func (f *cloudImageCreateFixture) description(marker string) string {
	if f.validProof.Load() {
		return marker
	}
	return "wrong-marker"
}

func (f *cloudImageCreateFixture) recordWrite(marker string) bool {
	if len(marker) != 64 || f.writes.Add(1) != 1 {
		return false
	}
	f.marker.Store(marker)
	return true
}

func dropImageCreationReply(t *testing.T, w http.ResponseWriter) bool {
	t.Helper()
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
