//go:build linux || darwin

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMHostImageReceipts(t *testing.T) {
	_, first, second := prepareKVM(t)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", fixtureKVMHost()), 201, nil)
	image := testKVMImage([]byte("receipt test"))
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	path := fixtureVMHostPath + "/images/" + image.ID
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	var initial kvm.HostImageReceipt
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", path, "", "", nil), 200, &initial)
	if initial.State != "unknown" || initial.Revision != 0 || initial.Available || initial.BootImageSpec != image {
		t.Fatalf("unexpected initial receipt: %+v", initial)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", path, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "POST", path+"/check", "", "", agent), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", path+"/check", "", "", agent), 200, nil)

	report := kvm.HostImageReport{AgentIdentity: agent, State: "verified", SHA256: image.SHA256, SizeBytes: image.SizeBytes}
	mismatch := report
	mismatch.SHA256 = strings.Repeat("a", 64)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "PUT", path, "", "", mismatch), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PUT", path, "", "", report), 403, nil)
	var replies [4]*httptest.ResponseRecorder
	var group sync.WaitGroup
	for i := range replies {
		handler := first
		if i%2 == 1 {
			handler = second
		}
		group.Go(func() { replies[i] = kvmRequest(t, handler, fixtureKVMAgent, "PUT", path, "", "", report) })
	}
	group.Wait()
	var firstReceipt kvm.HostImageReceipt
	expectKVM(t, replies[0], 200, &firstReceipt)
	if firstReceipt.Revision != 1 || !firstReceipt.Available || firstReceipt.ReportedAt == nil ||
		firstReceipt.ExpiresAt == nil || firstReceipt.ExpiresAt.Sub(*firstReceipt.ReportedAt) != kvm.HostImageTTL {
		t.Fatalf("unexpected receipt after recording: %+v", firstReceipt)
	}
	for _, reply := range replies[1:] {
		var replay kvm.HostImageReceipt
		expectKVM(t, reply, 200, &replay)
		if replay.Revision != firstReceipt.Revision || !firstReceipt.ExpiresAt.Equal(*replay.ExpiresAt) {
			t.Fatalf("report replay extended the receipt: %+v", replay)
		}
	}
	missing := report
	missing.Revision, missing.State = 1, "missing"
	var invalidated kvm.HostImageReceipt
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", path, "", "", missing), 200, &invalidated)
	if invalidated.Revision != 2 || invalidated.Available || invalidated.ExpiresAt != nil {
		t.Fatalf("missing report did not revoke availability: %+v", invalidated)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "PUT", path, "", "", report), 412, nil)
	report.Revision = 2
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", path, "", "", report), 200, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 200, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "PUT", path, "", "", report), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", path, "", "", report), 412, nil)
	var replaced kvm.HostImageReceipt
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", path, "", "", nil), 200, &replaced)
	if replaced.Generation != 2 || replaced.Revision != 0 || replaced.State != "unknown" || replaced.Available {
		t.Fatalf("rebind retained previous agent evidence: %+v", replaced)
	}
}

func TestAcceptanceKVMEvacuationVerifiedBackup(t *testing.T) {
	db, first, second, _, vm := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	vm = startEvacuationSourceVM(t, first, vm)
	raw := []byte("verified evacuation backup")
	input := evacuationPlanInput(t, first, raw, vm)
	input.Items[0].RequireVerifiedBackup = true
	image := testKVMImage(raw)
	_, _ = evacuationPlanTarget(t, first, second)
	const verifiedHost = "z-verified"
	const verifiedUUID = "feaa8a19-4765-4bb4-a383-3a1011ba0099"
	const verifiedToken = "fixture-kvm-verified-agent-credential-0001"
	destination := fixtureKVMSite + "/hosts/" + verifiedHost
	if _, err := db.ExecContext(t.Context(), `INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject)
		VALUES ('lab','verified-agent','service','kvm-test','verified-agent');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES ('lab','compute','verified-agent','viewer')`); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(verifiedToken))
	if _, err := db.ExecContext(t.Context(), `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'lab','verified-agent',now()+interval '15 minutes')`, hash[:]); err != nil {
		t.Fatal(err)
	}
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent, host.FailureDomain = verifiedHost, verifiedUUID, "verified-agent", "rack-3"
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, verifiedToken, "PUT", destination+"/capacity", "", "", report), 200, nil)

	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"1"`, "", input), 201, nil)
	changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"1"`, "request", 200)
	changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"2"`, "confirm", 200)
	store := kvm.NewStore(db)
	stepEvacuationPlan(t, store, true)
	location := evacuationFixturePlansPath + "/" + input.ID
	checkEvacuationPlanRetry(t, first, location, "verified_target_unavailable")
	assertNoEvacuationBatch(t, db, vm)

	var downloads atomic.Int32
	var corrupt atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mirror/"+image.SHA256+".raw" {
			first.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("mirror received an API credential")
		}
		downloads.Add(1)
		body := raw
		if corrupt.Load() {
			body = bytes.Repeat([]byte{'x'}, len(raw))
		}
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	root := imageFixtureDir(t)
	publish := fixtureHostImageMirror(t, server, root, verifiedHost, verifiedToken, image.ID)
	if _, err := publish(fixtureMigrationUUID); err == nil || downloads.Load() != 0 {
		t.Fatalf("foreign libvirt host could download: %v, downloads=%d", err, downloads.Load())
	}
	corrupt.Store(true)
	if _, err := publish(verifiedUUID); err == nil {
		t.Fatal("published a corrupt recovery image")
	}
	imagePath := destination + "/images/" + image.ID
	var receipt kvm.HostImageReceipt
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", imagePath, "", "", nil), 200, &receipt)
	if receipt.Revision != 0 || receipt.Available {
		t.Fatalf("corrupt download created receipt: %+v", receipt)
	}
	corrupt.Store(false)
	published, err := publish(verifiedUUID)
	if err != nil || !published.Available || published.Revision != 1 {
		t.Fatalf("could not publish verified backup: %+v, %v", published, err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, image.SHA256+".raw")); err != nil || !bytes.Equal(contents, raw) {
		t.Fatalf("host published wrong bytes: %q, %v", contents, err)
	}
	makeEvacuationPlanDue(t, db)
	stepEvacuationPlan(t, store, true)
	var plan kvm.EvacuationPlan
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", location, "", "", nil), 200, &plan)
	if plan.Status != "submitted" || plan.BatchID == "" {
		t.Fatalf("plan did not consume verified inventory: %+v", plan)
	}
	var batch kvm.EvacuationBatch
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", evacuationFixtureFencePath+"/evacuation-batches/"+plan.BatchID, "", "", nil), 200, &batch)
	if len(batch.Items) != 1 || batch.Items[0].TargetHost != verifiedHost || !batch.Items[0].RequireVerifiedBackup {
		t.Fatalf("plan did not skip earlier unverified host: %+v", batch)
	}
	migration := batch.Items[0]
	execEvacuationPlanSQL(t, db, `UPDATE cmp_kvm_host_images SET
		reported_at=statement_timestamp()-interval '1 hour',expires_at=statement_timestamp()-interval '30 minutes'`)
	path := fixtureVMSitePath + "/" + vm.ID + "/cold-migration/review"
	revision := strconv.Quote(strconv.FormatInt(migration.VMRevision, 10))
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", path, revision, "",
		kvm.ColdMigrationReview{ID: migration.ID, Decision: "approve"}), 409, nil)
	var unchanged kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &unchanged)
	if unchanged != vm {
		t.Fatalf("approval with expired evidence changed VM: %+v", unchanged)
	}
	refreshed, err := publish(verifiedUUID)
	if err != nil || !refreshed.Available || refreshed.Revision != 2 || downloads.Load() != 2 {
		t.Fatalf("cached verification failed or downloaded again: %+v, %v, downloads=%d", refreshed, err, downloads.Load())
	}
	cached := filepath.Join(root, image.SHA256+".raw")
	if err := os.WriteFile(cached, bytes.Repeat([]byte{'x'}, len(raw)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := publish(verifiedUUID); err == nil {
		t.Fatal("corrupt cached file was reverified")
	}
	receipt = kvm.HostImageReceipt{}
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", imagePath, "", "", nil), 200, &receipt)
	if receipt.State != "missing" || receipt.Available || receipt.Revision != 3 {
		t.Fatalf("corruption did not invalidate receipt: %+v", receipt)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", path, revision, "",
		kvm.ColdMigrationReview{ID: migration.ID, Decision: "approve"}), 409, nil)
	if err := os.Remove(cached); err != nil {
		t.Fatal(err)
	}
	republished, err := publish(verifiedUUID)
	if err != nil || !republished.Available || republished.Revision != 4 || downloads.Load() != 3 {
		t.Fatalf("replacement backup failed: %+v, %v, downloads=%d", republished, err, downloads.Load())
	}
	reviewBatchItem(t, second, migration, "approve")
	var moved kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &moved)
	if moved.Host != verifiedHost || moved.Address != vm.Address || moved.State != "queued" || moved.ImageSHA256 != image.SHA256 {
		t.Fatalf("approved recovery used unexpected VM or image: %+v", moved)
	}
	_, runner, api, profile := recoveryAgentFixture(t, server, destination, verifiedUUID, verifiedToken, root, moved)
	var output bytes.Buffer
	if err := runKVMVMJob(t.Context(), api, verifiedHost, "1", strings.NewReader(profile), &output, runner); err != nil {
		t.Fatal(err)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &moved)
	if moved.State != "staged" {
		t.Fatalf("agent did not stage the mirrored bytes: %+v", moved)
	}
}

func fixtureHostImageMirror(t *testing.T, server *httptest.Server, root, host, token, image string) func(string) (kvm.HostImageReceipt, error) {
	t.Helper()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"mirror-host-image", "lab", "compute", "site-a", host, "1", image}
	target, ok := parseKVMHostImageTarget(args)
	if !ok {
		t.Fatal("host mirror target is invalid")
	}
	plan, err := planKVMRequest(base, args)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmVMAPI{target: plan.target, token: token, transport: server.Client().Transport}
	profile, err := json.Marshal(kvmImageMirrorProfile{MirrorURL: server.URL + "/mirror", ImageRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	return func(uuid string) (kvm.HostImageReceipt, error) {
		t.Helper()
		var output bytes.Buffer
		probe := fixtureKVMProbe(t, uuid, "", 0)
		if err := runKVMHostImageMirror(t.Context(), api, target, bytes.NewReader(profile), &output, probe.read); err != nil {
			return kvm.HostImageReceipt{}, err
		}
		var receipt kvm.HostImageReceipt
		err := json.Unmarshal(output.Bytes(), &receipt)
		return receipt, err
	}
}
