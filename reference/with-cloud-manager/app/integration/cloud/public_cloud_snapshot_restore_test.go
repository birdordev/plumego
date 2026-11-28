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

const fixtureRestoreTag = "plumego-cmp-restore"
const fixtureRestoreName = "restored-volume"

func TestAcceptancePublicCloudSnapshotRestore(t *testing.T) {
	fixtures := []*snapshotRestoreFixture{
		{source: snapshotDeletionFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou",
			volume: "disk-fixture", id: "snap-fixture", class: "PRIVATE_SNAPSHOT"},
			zone: "ap-guangzhou-2", diskType: "CLOUD_SSD", id: "disk-restored", encrypted: true, loseReceipt: true},
		{source: snapshotDeletionFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou",
			volume: "d-fixture", id: "s-fixture", class: "standard"},
			zone: "cn-hangzhou-g", diskType: "cloud_essd", id: "d-restored"},
		{source: snapshotDeletionFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010", class: "flash"},
			zone: "cn-north-4a", diskType: "SSD", id: "50000000-0000-4000-8000-000000000010", loseReceipt: true},
		{source: snapshotDeletionFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4",
			volume: "30000000-0000-4000-8000-000000000010", id: "40000000-0000-4000-8000-000000000010", class: "standard"},
			zone: "cn-north-4a", diskType: "GPSSD", id: "50000000-0000-4000-8000-000000000010", encrypted: true},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.source.vendor+"/"+fixture.source.class, func(t *testing.T) { testPublicCloudSnapshotRestore(t, fixture) })
	}
}

func testPublicCloudSnapshotRestore(t *testing.T, f *snapshotRestoreFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-snapshot-restore-operator-token", "fixture-snapshot-restore-reviewer-token"
	source := &f.source
	identityFixture := vpcCreationFixture{vendor: source.vendor, account: source.account, region: source.region, parent: source.volume}
	seedSnapshotDeletion(t, db, source, identityFixture.remoteIdentity(), actor, reviewer)
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	cloud := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "sts.tencentcloudapi.com" || r.Host == "sts.aliyuncs.com" || r.Host == "iam.myhuaweicloud.com" {
			identityFixture.serve(t, w, r)
			return
		}
		if !f.serve(t, w, r) {
			t.Errorf("unexpected %s %s %s %s request", source.vendor, r.Method, r.Host, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer cloud.Close()
	endpoint, err := url.Parse(cloud.URL)
	if err != nil {
		t.Fatal(err)
	}
	config := publiccloud.Config{AccountID: source.account, Regions: []string{source.region}}
	if source.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	var maxGiB atomic.Int32
	store := operations.NewStore(db)
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			if binding.Provider != source.vendor || binding.RemoteIdentity != identityFixture.remoteIdentity() {
				return nil, identity.ErrForbidden
			}
			profile := config
			profile.SnapshotRestoreMaxGiB = int(maxGiB.Load())
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
			t.Error("snapshot restore worker did not stop")
		}
	}()
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	base := "/v1/tenants/tenant/projects/project"
	requestBody := func(size int, encrypted bool) string {
		return fmt.Sprintf(`{"account":%q,"region":%q,"asset_id":%q,"volume_id":%q,"name":%q,"zone":%q,"disk_type":%q,"size_gib":%d,"encrypted":%t}`,
			source.vendor, source.region, source.id, source.volume, fixtureRestoreName, f.zone, f.diskType, size, encrypted)
	}
	submit := func(path, key, revision string, size int, encrypted bool, want int) operations.Operation {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path+"/snapshots/restore", strings.NewReader(requestBody(size, encrypted)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+actor)
		r.Header.Set("Idempotency-Key", key)
		r.Header.Set("If-Match", revision)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("restore request %s: got %d, want %d: %s", key, w.Code, want, w.Body)
		}
		var op operations.Operation
		if want == http.StatusAccepted {
			testsupport.DecodeProjectData(t, w, &op)
			if op.AssetID != "" || op.Action != "restore-snapshot" || op.ResourceKind != provider.Volume ||
				w.Header().Get("Location") != base+"/operations/"+op.ID {
				t.Fatalf("incorrect snapshot restore receipt: %+v", op)
			}
		}
		return op
	}
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
	submit(base, "stale", `"2"`, 40, f.encrypted, http.StatusPreconditionFailed)
	submit("/v1/tenants/tenant/projects/other", "foreign", `"1"`, 40, f.encrypted, http.StatusForbidden)
	disabled := submit(base, "disabled", `"1"`, 40, f.encrypted, http.StatusAccepted)
	approve(disabled.ID)
	if result := wait(disabled.ID, "failed", false); result.OutcomeReason != "provider_creation_blocked" || f.reads.Load() != 0 {
		t.Fatalf("disabled restore reached snapshot API: %+v", result)
	}
	maxGiB.Store(50)
	for _, request := range []struct {
		key       string
		size      int
		encrypted bool
	}{{"over-limit", 60, f.encrypted}, {"smaller-than-snapshot", 20, f.encrypted}, {"encryption-mismatch", 40, !f.encrypted}} {
		blocked := submit(base, request.key, `"1"`, request.size, request.encrypted, http.StatusAccepted)
		approve(blocked.ID)
		if result := wait(blocked.ID, "failed", false); result.OutcomeReason != "provider_creation_blocked" || f.writes.Load() != 0 {
			t.Fatalf("unapproved restore reached create: %+v", result)
		}
	}
	op := submit(base, "restore", `"1"`, 40, f.encrypted, http.StatusAccepted)
	if op.Status != "awaiting_approval" || f.writes.Load() != 0 {
		t.Fatalf("restore bypassed approval: %+v", op)
	}
	if replay := submit(base, "restore", `"1"`, 40, f.encrypted, http.StatusAccepted); replay.ID != op.ID {
		t.Fatalf("restore replay created another operation: %+v", replay)
	}
	submit(base, "restore", `"1"`, 50, f.encrypted, http.StatusConflict)
	if _, err := store.SubmitSnapshotDeletion(ctx, actor, scope, "delete-during-restore", 1,
		operations.SnapshotDeletion{Account: source.vendor, Region: source.region, AssetID: source.id, VolumeID: source.volume}); !errors.Is(err, operations.ErrConflict) {
		t.Fatalf("restore did not serialize concurrent removal: %v", err)
	}
	if source.vendor == provider.TencentCVM {
		server := httptest.NewTLSServer(app)
		defer server.Close()
		var reply bytes.Buffer
		args := []string{"restore-snapshot", "tenant", "project", "--idempotency-key", "restore", "--if-match", "1"}
		err := commands.RunOperationsClient(ctx, args, server.URL, actor, strings.NewReader(requestBody(40, f.encrypted)), &reply, server.Client().Transport)
		if err != nil || !strings.Contains(reply.String(), op.ID) {
			t.Fatalf("restore CLI replay: reply=%s err=%v", &reply, err)
		}
	}
	approve(op.ID)
	pending := wait(op.ID, "observing", false)
	intent := pending.SnapshotRestoreIntent
	if intent == nil || intent.SizeGiB != 40 || intent.VolumeRevision != 1 || intent.SnapshotID != source.id ||
		intent.Encrypted != f.encrypted || (f.loseReceipt && pending.OutcomeReason != "submission_outcome_unknown") {
		t.Fatalf("unexpected restore intent: %+v", pending)
	}
	waitPendingCloudObservation(t, db, op.ID)
	if source.vendor == provider.AlibabaECS {
		f.wrongSize.Store(true)
		f.ready.Store(true)
		wait(op.ID, "needs_review", true)
		waitSnapshotDeletionEvidence(t, db, op.ID, 0, "done")
		f.wrongSize.Store(false)
		if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, operations.ObservationRetry{EvidenceRef: "restore/size-confirmed"}); err != nil {
			t.Fatal(err)
		}
	}
	maxGiB.Store(0)
	f.ready.Store(true)
	complete := wait(op.ID, "succeeded", true)
	if complete.AssetID != f.id || f.writes.Load() != 1 {
		t.Fatalf("restore did not settle once: %+v writes=%d", complete, f.writes.Load())
	}
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='volume.operation.succeeded'`).Scan(&events); err != nil || events != 1 {
		t.Fatalf("missing volume creation event: count=%d err=%v", events, err)
	}
	request := httptest.NewRequest(http.MethodGet, base+"/operations?kind=volume&action=restore-snapshot&status=succeeded", nil)
	request.Header.Set("Authorization", "Bearer "+actor)
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("volume operation filter: status=%d body=%s", recorder.Code, recorder.Body)
	}
	var page operations.OperationPage
	testsupport.DecodeProjectData(t, recorder, &page)
	if len(page.Items) != 1 || page.Items[0].ID != op.ID || page.Items[0].AssetID != f.id {
		t.Fatalf("volume operation filter omitted the restored volume: %+v", page)
	}
	inventoryStore := inventory.NewStore(db)
	claim, err := inventoryStore.Claim(ctx, inventory.Key{Tenant: scope.Tenant, Account: source.vendor, Region: source.region, Kind: provider.Volume}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observed := provider.Object{ID: f.id, Name: fixtureRestoreName, Status: "available"}
	observed.Link(provider.Zone, f.zone, "in_zone")
	if _, _, err := inventoryStore.ScanPage(ctx, claim, staleAssetDiscovery{observed}); err != nil {
		t.Fatal(err)
	}
	var project, origin string
	if err := db.QueryRowContext(ctx, `SELECT own.project_id,o.links->1->>'id'
		FROM cmp_inventory_objects AS o JOIN cmp_asset_ownership AS own USING(scope_id,external_id)
		WHERE o.external_id=$1 AND o.links->1->>'relation'='restored_from'`, f.id).Scan(&project, &origin); err != nil || project != "project" || origin != source.id {
		t.Fatalf("wrong restored volume ownership or provenance after scan: %s %s %v", project, origin, err)
	}
}

type snapshotRestoreFixture struct {
	source                 snapshotDeletionFixture
	zone, diskType, id     string
	encrypted, loseReceipt bool
	reads, writes          atomic.Int32
	ready, wrongSize       atomic.Bool
	marker                 atomic.Pointer[string]
}

func (f *snapshotRestoreFixture) remember(marker string) bool {
	return len(marker) == 64 && f.writes.Add(1) == 1 && f.marker.CompareAndSwap(nil, &marker)
}

func (f *snapshotRestoreFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	source := &f.source
	send := func(value any) { testsupport.WriteJSON(t, w, value) }
	switch source.vendor {
	case provider.TencentCVM:
		raw, err := io.ReadAll(r.Body)
		if err != nil || r.Host != "cbs.tencentcloudapi.com" || r.Header.Get("X-TC-Region") != source.region ||
			r.Header.Get("X-TC-Token") != fixtureLaunchSession || r.Header.Get("X-TC-Version") != "2017-03-12" ||
			!validFixtureTencentSignature(r, raw, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		return f.tencent(raw, r.Header.Get("X-TC-Action"), func(value any) { send(map[string]any{"Response": value}) })
	case provider.AlibabaECS:
		if r.Host != "ecs."+source.region+".aliyuncs.com" || r.Header.Get("X-ACS-Security-Token") != fixtureLaunchSession ||
			r.Header.Get("X-ACS-Version") != "2014-05-26" || !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
			return false
		}
		return f.alibaba(r.Header.Get("X-ACS-Action"), r.URL.Query(), send)
	default:
		if r.Host != "evs."+source.region+".myhuaweicloud.com" || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			return false
		}
		return f.huawei(w, r, send)
	}
}

func (f *snapshotRestoreFixture) tencent(raw []byte, action string, send func(any)) bool {
	var input struct {
		Placement                                                            struct{ Zone string }
		SnapshotIds, DiskIds                                                 []string
		SnapshotId, DiskName, DiskType, DiskChargeType, ClientToken, Encrypt string
		DiskSize, DiskCount                                                  int
		Tags                                                                 []struct{ Key, Value string }
		Filters                                                              []struct {
			Name   string
			Values []string
		}
	}
	if json.Unmarshal(raw, &input) != nil {
		return false
	}
	switch action {
	case "DescribeSnapshots":
		if len(input.SnapshotIds) != 1 || input.SnapshotIds[0] != f.source.id {
			return false
		}
		f.reads.Add(1)
		send(map[string]any{"TotalCount": 1, "SnapshotSet": []any{map[string]any{"SnapshotId": f.source.id,
			"SnapshotType": "PRIVATE_SNAPSHOT", "SnapshotState": "NORMAL", "DiskId": f.source.volume, "DiskSize": 40,
			"DiskUsage": "DATA_DISK", "Encrypt": f.encrypted}}})
	case "CreateDisks":
		if input.DiskCount != 1 || input.DiskSize != 40 || input.SnapshotId != f.source.id || input.DiskName != fixtureRestoreName ||
			input.Placement.Zone != f.zone || input.DiskType != f.diskType || input.DiskChargeType != "POSTPAID_BY_HOUR" ||
			len(input.ClientToken) < 16 || input.Encrypt != "ENCRYPT" || len(input.Tags) != 1 ||
			input.Tags[0].Key != fixtureRestoreTag || !f.remember(input.Tags[0].Value) {
			return false
		}
		panic(http.ErrAbortHandler)
	case "DescribeDisks":
		marker := f.marker.Load()
		if marker == nil || len(input.DiskIds) != 0 || len(input.Filters) != 1 || input.Filters[0].Name != "tag:"+fixtureRestoreTag ||
			len(input.Filters[0].Values) != 1 || input.Filters[0].Values[0] != *marker {
			return false
		}
		status := "CREATING"
		if f.ready.Load() {
			status = "UNATTACHED"
		}
		send(map[string]any{"TotalCount": 1, "DiskSet": []any{map[string]any{"DiskId": f.id, "DiskName": fixtureRestoreName,
			"DiskState": status, "DiskType": f.diskType, "DiskUsage": "DATA_DISK", "DiskChargeType": "POSTPAID_BY_HOUR",
			"Encrypt": f.encrypted, "DiskSize": 40, "Placement": map[string]string{"Zone": f.zone},
			"Tags": []map[string]string{{"Key": fixtureRestoreTag, "Value": *marker}}}}})
	default:
		return false
	}
	return true
}

func (f *snapshotRestoreFixture) alibaba(action string, query url.Values, send func(any)) bool {
	if query.Get("RegionId") != f.source.region {
		return false
	}
	switch action {
	case "DescribeSnapshots":
		if query.Get("SnapshotIds") != fmt.Sprintf("[%q]", f.source.id) {
			return false
		}
		f.reads.Add(1)
		send(map[string]any{"Snapshots": map[string]any{"Snapshot": []any{map[string]any{"RegionId": f.source.region, "Category": f.source.class,
			"SnapshotId": f.source.id, "SourceDiskId": f.source.volume, "Status": "accomplished", "SourceDiskSize": "40",
			"Encrypted": f.encrypted, "Available": "true"}}}})
	case "CreateDisk":
		if query.Get("SnapshotId") != f.source.id || query.Get("ZoneId") != f.zone || query.Get("DiskCategory") != f.diskType ||
			query.Get("DiskName") != fixtureRestoreName || query.Get("Size") != "40" || query.Get("DiskChargeType") != "PostPaid" ||
			len(query.Get("ClientToken")) < 16 || query.Get("Encrypted") != "" || query.Get("Tag.1.Key") != fixtureRestoreTag ||
			!f.remember(query.Get("Tag.1.Value")) {
			return false
		}
		send(map[string]string{"DiskId": f.id, "RequestId": "fixture-restore"})
	case "DescribeDisks":
		marker := f.marker.Load()
		if marker == nil || query.Get("DiskIds") != fmt.Sprintf("[%q]", f.id) {
			return false
		}
		status, size := "Creating", 40
		if f.ready.Load() {
			status = "Available"
		}
		if f.wrongSize.Load() {
			size = 50
		}
		send(map[string]any{"Disks": map[string]any{"Disk": []any{map[string]any{"DiskId": f.id, "DiskName": fixtureRestoreName,
			"RegionId": f.source.region, "ZoneId": f.zone, "Type": "data", "Category": f.diskType, "Status": status,
			"SourceSnapshotId": f.source.id, "Size": size, "Encrypted": f.encrypted, "DiskChargeType": "PostPaid",
			"Tags": map[string]any{"Tag": []map[string]string{{"TagKey": fixtureRestoreTag, "TagValue": *marker}}}}}}})
	default:
		return false
	}
	return true
}

func (f *snapshotRestoreFixture) huawei(w http.ResponseWriter, r *http.Request, send func(any)) bool {
	v2 := "/v2/" + fixtureHuaweiProject
	snapshotPath := v2 + "/cloudsnapshots/detail"
	if f.source.class == "standard" {
		snapshotPath = "/v5/" + fixtureHuaweiProject + "/snapshots/detail"
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == snapshotPath:
		if r.URL.Query().Get("id") != f.source.id {
			return false
		}
		f.reads.Add(1)
		row := map[string]any{"id": f.source.id, "volume_id": f.source.volume, "name": "fixture-snapshot",
			"status": "available", "availability_zone": f.zone, "size": 40}
		if f.source.class == "flash" {
			row["os-extended-snapshot-attributes:project_id"] = fixtureHuaweiProject
			row["metadata"] = map[string]string{}
		} else {
			row["category"], row["project_id"], row["encrypted"] = f.source.class, fixtureHuaweiProject, f.encrypted
		}
		send(map[string]any{"count": 1, "snapshots": []any{row}})
	case r.Method == http.MethodPost && r.URL.Path == "/v2.1/"+fixtureHuaweiProject+"/cloudvolumes":
		var payload struct {
			Volume struct {
				Snapshot          string `json:"snapshot_id"`
				Name, Description string
				Type              string `json:"volume_type"`
				Zone              string `json:"availability_zone"`
				Size, Count       int
			}
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&payload) != nil || len(r.Header.Get("X-Client-Token")) != 36 {
			return false
		}
		volume := payload.Volume
		marker, ok := strings.CutPrefix(volume.Description, fixtureRestoreTag+":")
		if !ok || volume.Snapshot != f.source.id || volume.Name != fixtureRestoreName || volume.Type != f.diskType ||
			volume.Zone != f.zone || volume.Size != 40 || volume.Count != 1 || !f.remember(marker) {
			return false
		}
		if f.loseReceipt {
			panic(http.ErrAbortHandler)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		send(map[string]any{"job_id": "fixture-restore-job", "volume_ids": []string{f.id}})
	case r.Method == http.MethodGet && r.URL.Path == v2+"/cloudvolumes/"+f.id && !f.loseReceipt:
		send(map[string]any{"volume": f.huaweiVolume()})
	case r.Method == http.MethodGet && r.URL.Path == v2+"/cloudvolumes/detail" && f.loseReceipt:
		query := r.URL.Query()
		if query.Get("name") != fixtureRestoreName || query.Get("sort_key") != "id" || query.Get("sort_dir") != "asc" {
			return false
		}
		disks := []any{}
		switch query.Get("marker") {
		case "":
			disks = append(disks, f.huaweiVolume())
		case f.id:
		default:
			return false
		}
		send(map[string]any{"volumes": disks})
	default:
		return false
	}
	return true
}

func (f *snapshotRestoreFixture) huaweiVolume() map[string]any {
	status := "creating"
	if f.ready.Load() {
		status = "available"
	}
	metadata := map[string]string{}
	if f.encrypted {
		metadata["__system__encrypted"] = "1"
	}
	return map[string]any{"id": f.id, "name": fixtureRestoreName, "os-vol-tenant-attr:tenant_id": fixtureHuaweiProject, "snapshot_id": f.source.id,
		"description": fixtureRestoreTag + ":" + *f.marker.Load(), "status": status, "availability_zone": f.zone,
		"volume_type": f.diskType, "size": 40, "metadata": metadata, "bootable": "false", "attachments": []any{}}
}
