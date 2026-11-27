//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

const testLiveMigrationID = "maintenance-1"
const testLiveDestination = "qemu+tls://target.example.test/system"
const testLiveMAC = "52:54:00:ab:cd:ef"
const testLiveBridge = `<network><name>app</name><uuid>feaa8a19-4765-4bb4-a383-3a1011ba0088</uuid><forward mode="bridge"/><bridge name="br-prod"/></network>`

func TestAcceptanceKVMLiveMigration(t *testing.T) {
	db, first, second, storage, vm := prepareLiveMigration(t)
	path := fixtureVMSitePath + "/" + vm.ID + "/live-migrations"
	location := path + "/" + testLiveMigrationID
	revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	input := kvm.LiveMigrationInput{ID: testLiveMigrationID, Target: fixtureMigrationHost}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", path, revision, "", input), 403, nil)
	var pending kvm.LiveMigration
	register := kvmRequest(t, first, fixtureKVMAdmin, "POST", path, revision, "", input)
	expectKVM(t, register, 202, &pending)
	if pending.Status != "pending" || register.Header().Get("Location") != location {
		t.Fatalf("unexpected submission: %+v", pending)
	}
	var replay kvm.LiveMigration
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", path, revision, "", input), 202, &replay)
	if !replay.RequestedAt.Equal(pending.RequestedAt) {
		t.Fatal("migration submission was not idempotent")
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", location, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath+"/"+vm.ID+"/shutdown", revision, "", nil), 409, nil)
	decision := kvm.VMReview{Decision: "approve"}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", location+"/review", revision, "", decision), 403, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", location+"/review", revision, "", decision), 200, nil)
	var before kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+vm.Placement, "", "", nil), 200, &before)
	checkLiveTargetCompute(t, second, storage, "reserved-by-live")

	source, target, fixture := liveMigrationAgents(t, first, second, storage, vm)
	get := func(status string) kvm.LiveMigration {
		t.Helper()
		var current kvm.LiveMigration
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", location, "", "", nil), 200, &current)
		if current.Status != status {
			t.Fatalf("got migration %s, want %s: %+v", current.Status, status, current)
		}
		return current
	}
	if err := source(); err != nil {
		t.Fatal(err)
	}
	if get("approved").PreparedAt != nil {
		t.Fatal("source made a preparation receipt")
	}
	fixture.targetBridge = strings.Replace(testLiveBridge, `mode="bridge"`, `mode="nat"`, 1)
	if err := target(); err == nil || get("approved").PreparedAt != nil {
		t.Fatalf("target presented an untrusted network: %v", err)
	}
	fixture.targetBridge = testLiveBridge
	if err := target(); err != nil {
		t.Fatal(err)
	}
	if get("approved").PreparedAt == nil {
		t.Fatal("target did not prepare")
	}
	fixture.wrongDestination = true
	if err := source(); err == nil || fixture.writes != 0 || get("approved").DispatchedAt != nil {
		t.Fatalf("wrong TLS peer obtained dispatch: %v", err)
	}
	fixture.wrongDestination = false
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_live_migrations SET prepared_at=statement_timestamp()-interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	if err := source(); err == nil || fixture.writes != 0 {
		t.Fatalf("expired target evidence obtained dispatch: %v", err)
	}
	if err := target(); err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	start := make(chan struct{})
	for range 6 {
		group.Go(func() {
			<-start
			// 部分并发请求因版本变化或 libvirt 回执丢失而失败。
			_ = source()
		})
	}
	close(start)
	group.Wait()
	if fixture.writes != 1 {
		t.Fatalf("expected a single libvirt migration, got %d", fixture.writes)
	}
	get("dispatched")
	if err := target(); err != nil {
		t.Fatal(err)
	}
	get("dispatched")
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", location+"/review", revision, "", kvm.VMReview{Decision: "reject"}), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 409, nil)
	fixture.allowDeparture()
	if err := source(); err != nil {
		t.Fatal(err)
	}
	get("source_departed")
	execEvacuationPlanSQL(t, db, `ALTER TABLE cmp_kvm_live_migrations ADD CONSTRAINT fixture_block_live_commit CHECK (status<>'completed')`)
	if err := target(); err == nil {
		t.Fatal("database denied completion but the agent reported success")
	}
	var unchanged kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &unchanged)
	if unchanged != vm {
		t.Fatalf("failed completion changed original placement: %+v", unchanged)
	}
	get("source_departed")
	execEvacuationPlanSQL(t, db, `ALTER TABLE cmp_kvm_live_migrations DROP CONSTRAINT fixture_block_live_commit`)
	if err := target(); err != nil {
		t.Fatal(err)
	}
	result := get("completed")
	if result.MAC != testLiveMAC || result.CompletedAt == nil || result.DepartedAt == nil {
		t.Fatalf("missing completion evidence: %+v", result)
	}
	var moved kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &moved)
	if moved.Host != fixtureMigrationHost || moved.Placement != vm.Placement || moved.Volume != vm.Volume ||
		moved.SharedStoragePool != storage.ID || moved.Revision != vm.Revision+1 || moved.State != "running" {
		t.Fatalf("unexpected live handoff: %+v", moved)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: before.ID, ImageID: vm.ImageID}), 202, &unchanged)
	if unchanged != moved {
		t.Fatal("replay of the original VM lost the migrated identity")
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", path, revision, "", input), 202, &replay)
	if replay.Status != "completed" {
		t.Fatal("migration replay lost the completed receipt")
	}
	checkLiveTargetCompute(t, second, storage, "charged-after-handoff")
	checkLiveMigrationCLI(t, second, vm.ID)
}

func TestAcceptanceKVMLiveMigrationFencedAbort(t *testing.T) {
	_, first, second, _, vm := prepareLiveMigration(t)
	path := fixtureVMSitePath + "/" + vm.ID + "/live-migrations"
	revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	migration := kvm.LiveMigrationInput{ID: testLiveMigrationID, Target: fixtureMigrationHost}
	location := path + "/" + migration.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", path, revision, "", migration), 202, nil)
	var approved kvm.LiveMigration
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", location+"/review", revision, "",
		kvm.VMReview{Decision: "approve"}), 200, &approved)
	target := fixtureKVMSite + "/hosts/" + fixtureMigrationHost
	sourceAgent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	targetAgent := kvm.AgentIdentity{UUID: fixtureMigrationUUID, Generation: 1}
	workPath := "/live-migrations/" + vm.ID + "/" + migration.ID
	digest := strings.Repeat("a", 64)
	prepare := kvm.LiveMigrationCommand{AgentIdentity: sourceAgent, Action: "prepare", NetworkSHA256: digest, Revision: approved.Revision}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", fixtureVMHostPath+workPath, "", "", prepare), 409, nil)
	prepare.AgentIdentity = targetAgent
	var work kvm.LiveMigrationWork
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "POST", target+workPath, "", "", prepare), 200, &work)
	arm := kvm.LiveMigrationCommand{AgentIdentity: sourceAgent, Action: "arm", NetworkSHA256: digest, MAC: testLiveMAC, Revision: work.Migration.Revision}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", fixtureVMHostPath+workPath, "", "", arm), 200, &work)
	abortRevision := strconv.Quote(strconv.FormatInt(work.Migration.Revision, 10))
	evidence := map[string]string{"evidence_sha256": strings.Repeat("b", 64)}
	abort := func(token, version string, status int, data any) {
		t.Helper()
		expectKVM(t, kvmRequest(t, second, token, "POST", location+"/abort", version, "", evidence), status, data)
	}
	abort(fixtureKVMReviewer, abortRevision, 409, nil)
	changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"1"`, "request", 200)
	changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"2"`, "confirm", 200)
	abort(fixtureKVMReviewer, abortRevision, 409, nil)
	var blockers kvm.HostFenceWorkPage
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMHostPath+"/fence/work", "", "", nil), 200, &blockers)
	if len(blockers.Items) != 1 || blockers.Items[0].LiveMigrationID != migration.ID || blockers.Items[0].Observation != "dispatched" {
		t.Fatalf("fenced source omitted outstanding migration: %+v", blockers)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", target+"/fence", `"1"`, "",
		kvm.HostFenceChange{Action: "request", UUID: targetAgent.UUID}), 200, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", target+"/fence", `"2"`, "",
		kvm.HostFenceChange{Action: "confirm", UUID: targetAgent.UUID, EvidenceSHA256: strings.Repeat("c", 64)}), 200, nil)
	abort(fixtureKVMAdmin, abortRevision, 403, nil)
	abort(fixtureKVMReviewer, `"1"`, 412, nil)
	var aborted, replay kvm.LiveMigration
	abort(fixtureKVMReviewer, abortRevision, 200, &aborted)
	abort(fixtureKVMReviewer, abortRevision, 200, &replay)
	if aborted.Status != "aborted" || aborted.AbortEvidence != evidence["evidence_sha256"] ||
		aborted.AbortedBy != "reviewer" || aborted.AbortedAt == nil || !aborted.AbortedAt.Equal(*replay.AbortedAt) {
		t.Fatalf("unexpected fenced abort receipt: %+v %+v", aborted, replay)
	}
	var current kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &current)
	if current != vm {
		t.Fatalf("fenced abort changed ownership: %+v", current)
	}
	expectedAgent := kvm.LiveMigrationCommand{AgentIdentity: sourceAgent, Action: "inspect"}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", fixtureVMHostPath+workPath, "", "", expectedAgent), 412, nil)
	blockers = kvm.HostFenceWorkPage{}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMHostPath+"/fence/work", "", "", nil), 200, &blockers)
	if len(blockers.Items) != 0 {
		t.Fatalf("aborted migration still blocks site recovery: %+v", blockers)
	}
}

func prepareLiveMigration(t *testing.T) (*sql.DB, http.Handler, http.Handler, kvm.SharedStorageSpec, kvm.VM) {
	t.Helper()
	db, first, second := prepareKVM(t)
	storage := fixtureSharedStorage
	storage.CapacityMiB = 2048
	registerSharedStorageFixture(t, first, storage)
	source, target := fixtureKVMHost(), fixtureKVMHost()
	source.SharedStoragePool, target.SharedStoragePool = storage.ID, storage.ID
	target.ID, target.UUID, target.Agent = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent"
	targetPath := fixtureKVMSite + "/hosts/" + target.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", source), 201, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", target), 201, nil)
	report := fixtureKVMReport()
	report.SharedStorage = &kvm.SharedStorageEvidence{ID: storage.ID, SharedStorageSource: storage.SharedStorageSource}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", report), 200, nil)
	report.UUID, report.Capacity.VCPU = target.UUID, 1
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", targetPath+"/capacity", "", "", report), 200, nil)
	image := testKVMImage([]byte("shared live guest disk"))
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	var hold kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "live-guest",
		sharedStoragePlacement(storage.ID, source.ID, 1024)), 201, &hold)
	var vm kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: hold.ID, ImageID: image.ID}), 202, &vm)
	vm = approveKVMVM(t, first, vm, 200)
	work := fixtureVMHostPath + "/vm-work/" + vm.ID
	agent := kvm.AgentIdentity{UUID: source.UUID, Generation: 1}
	for _, evidence := range []string{"staged", "defined", "running"} {
		expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", work+"/arm", "", "",
			kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}), 200, &vm)
		expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", work+"/observation", "", "",
			kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}, Observed: evidence}), 200, &vm)
	}
	return db, first, second, storage, vm
}

func checkLiveTargetCompute(t *testing.T, handler http.Handler, storage kvm.SharedStorageSpec, key string) {
	t.Helper()
	assertKVMSharedUsage(t, handler, storage, 1024, 0)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", key,
		sharedStoragePlacement(storage.ID, fixtureMigrationHost, 1)), 409, nil)
}

func checkLiveMigrationCLI(t *testing.T, handler http.Handler, vm string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	var output bytes.Buffer
	args := []string{"get-live-migration", "lab", "compute", "site-a", vm, testLiveMigrationID}
	if err := runKVMClient(t.Context(), args, server.URL, fixtureKVMOperator, nil, &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var result kvm.LiveMigration
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "completed" {
		t.Fatalf("unexpected CLI receipt: %s %v", output.String(), err)
	}
}

type liveMigrationFixture struct {
	mu               sync.Mutex
	vm               string
	domain           []byte
	targetBridge     string
	wrongDestination bool
	blockDeparture   bool
	writes           int
}

func liveMigrationAgents(t *testing.T, first, second http.Handler, storage kvm.SharedStorageSpec, vm kvm.VM) (
	func() error, func() error, *liveMigrationFixture) {
	t.Helper()
	root := imageFixtureDir(t)
	disk := filepath.Join(root, vm.Volume)
	if err := os.WriteFile(disk, []byte("shared live guest disk"), 0600); err != nil {
		t.Fatal(err)
	}
	domain := desiredKVMDomain(vm)
	domain.Networks[0].MAC = &kvmMAC{Address: testLiveMAC}
	spec, err := xml.Marshal(domain)
	if err != nil {
		t.Fatal(err)
	}
	f := &liveMigrationFixture{vm: vm.ID, domain: spec, targetBridge: testLiveBridge, blockDeparture: true}
	source := f.runner(t, root, storage, disk, true)
	target := f.runner(t, root, storage, disk, false)
	source.remote = func(ctx context.Context, uri *url.URL, args ...string) ([]byte, error) {
		if uri.String() != testLiveDestination {
			t.Fatal("selected a destination outside the site allowlist")
		}
		if f.wrongDestination && slices.Equal(args, []string{"capabilities"}) {
			return source.read(ctx, args...)
		}
		return target.read(ctx, args...)
	}
	return liveTestAgent(t, first, vm.ID, "host-one", fixtureKVMAgent, `{"destinations":{"host-two":"`+testLiveDestination+`"}}`, source),
		liveTestAgent(t, second, vm.ID, fixtureMigrationHost, fixtureKVMReplacement, "{}", target), f
}

func (f *liveMigrationFixture) runner(t *testing.T, root string, storage kvm.SharedStorageSpec, disk string, source bool) kvmLiveMigrationRunner {
	t.Helper()
	uuid := fixtureMigrationUUID
	if source {
		uuid = fixtureKVMUUID
	}
	probe := fixtureKVMProbe(t, uuid, "", 4096)
	setSharedKVMProbe(t, &probe, root, storage, "local")
	original := probe.read
	probe.read = func(ctx context.Context, args ...string) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch command := strings.Join(args, " "); command {
		case "list --all --uuid", "list --all --uuid --persistent":
			if source && f.writes > 0 && f.blockDeparture {
				return nil, errors.New("fixture lost source observation")
			}
			if source == (f.writes == 0) {
				return []byte(f.vm), nil
			}
			return nil, nil
		case "net-dumpxml --network app", "net-dumpxml --network app --inactive":
			if source {
				return []byte(testLiveBridge), nil
			}
			return []byte(f.targetBridge), nil
		case "domstate " + f.vm:
			return []byte("running"), nil
		case "dumpxml " + f.vm + " --inactive", "dumpxml " + f.vm:
			return f.domain, nil
		default:
			return original(ctx, args...)
		}
	}
	return kvmLiveMigrationRunner{kvmProbe: probe,
		bootFile: func(path string, size int64) error {
			info, err := os.Stat(path)
			if err != nil || path != disk || info.Size() != size {
				return errKVMUnsafeTarget
			}
			return nil
		},
		write: func(_ context.Context, args ...string) ([]byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			want := []string{"migrate", "--live", "--p2p", "--tunnelled", "--persistent", "--undefinesource", "--change-protection",
				"--abort-on-error", f.vm, testLiveDestination}
			if !source || f.writes > 0 || !slices.Equal(args, want) {
				t.Errorf("unexpected libvirt migration: %v", args)
				return nil, errKVMUnsafeTarget
			}
			f.writes++
			return nil, errors.New("fixture lost the migration reply")
		},
	}
}

func (f *liveMigrationFixture) allowDeparture() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockDeparture = false
}

func liveTestAgent(t *testing.T, handler http.Handler, vm, host, token, profile string, runner kvmLiveMigrationRunner) func() error {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"run-live-migration", "lab", "compute", "site-a", host, "1", vm, testLiveMigrationID}
	target, ok := parseKVMLiveMigrationTarget(args)
	if !ok {
		t.Fatal("invalid migration agent target")
	}
	plan, err := planKVMRequest(base, args)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmVMAPI{target: plan.target, token: token, transport: server.Client().Transport}
	return func() error {
		var output bytes.Buffer
		return runKVMLiveMigration(t.Context(), api, target, strings.NewReader(profile), &output, runner)
	}
}
