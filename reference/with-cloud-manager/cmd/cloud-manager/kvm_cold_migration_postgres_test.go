//go:build linux || darwin

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

const fixtureMigrationHost = "host-two"
const fixtureMigrationUUID = "feaa8a19-4765-4bb4-a383-3a1011ba0002"

func TestAcceptanceKVMColdMigration(t *testing.T) {
	db, first, second, source, vm := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	const destination = fixtureKVMSite + "/hosts/" + fixtureMigrationHost
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent"
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)

	contents := []byte("cold migration raw backup")
	image := testKVMImage(contents)
	path := fixtureVMSitePath + "/" + vm.ID
	migration := path + "/cold-migration"
	input := kvm.ColdMigrationInput{ID: "move-1", Image: image.ID, Evidence: strings.Repeat("a", 64), Placement: source.ID}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", migration, `"1"`, "", input), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", fixtureVMHostPath+"/vm-work/"+vm.ID+"/backup-plan", "", "",
		kvm.VMCommand{AgentIdentity: kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}, Revision: vm.Revision}), 409, nil)
	vm = retireMigrationSource(t, first, vm)
	contents, input.Evidence = exportMigrationBackup(t, second, vm, contents, image)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	rev := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	checkExpiredMigrationTarget(t, db, first, second, migration, rev, input, source)
	target := reserveMigrationTarget(t, second, source, "move-reservation")
	input.Placement = target.ID
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", migration, rev, "", input), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", migration, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", migration, `"1"`, "", input), 412, nil)
	var submitted, replay kvm.ColdMigration
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", migration, rev, "", input), 202, &submitted)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", migration, rev, "", input), 202, &replay)
	changed := input
	changed.Evidence = strings.Repeat("b", 64)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", migration, rev, "", changed), 409, nil)
	if submitted.SourceHost != vm.Host || replay.TargetHost != fixtureMigrationHost || replay.Status != "pending" ||
		submitted.VM != vm.ID || !submitted.RequestedAt.Equal(replay.RequestedAt) {
		t.Fatalf("cold migration request was not frozen: %+v %+v", submitted, replay)
	}

	decision := kvm.ColdMigrationReview{ID: submitted.ID, Decision: "approve"}
	review := func(handler http.Handler, token string) *httptest.ResponseRecorder {
		t.Helper()
		return kvmRequest(t, handler, token, "POST", migration+"/review", rev, "", decision)
	}
	expectKVM(t, review(second, fixtureKVMAdmin), 403, nil)
	checkMigrationRollback(t, db, first, vm, target, func() *httptest.ResponseRecorder { return review(second, fixtureKVMReviewer) })
	var firstResult, secondResult *httptest.ResponseRecorder
	var wg sync.WaitGroup
	wg.Go(func() { firstResult = review(first, fixtureKVMReviewer) })
	wg.Go(func() { secondResult = review(second, fixtureKVMReviewer) })
	wg.Wait()
	var approved kvm.ColdMigration
	expectKVM(t, firstResult, 200, &approved)
	expectKVM(t, secondResult, 200, &replay)
	if approved.Status != "approved" || replay.ReviewedAt == nil || approved.ReviewedAt == nil ||
		!approved.ReviewedAt.Equal(*replay.ReviewedAt) {
		t.Fatalf("concurrent review did not converge: %+v %+v", approved, replay)
	}
	var queued kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", path, "", "", nil), 200, &queued)
	if queued.ID != vm.ID || queued.State != "queued" || queued.Host != fixtureMigrationHost ||
		queued.Address != vm.Address || queued.MAC != vm.MAC || queued.Revision != vm.Revision+1 ||
		queued.Placement != target.ID || queued.ImageSHA256 != image.SHA256 {
		t.Fatalf("wrong migration handoff: %+v", queued)
	}
	var sourceHold kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", fixtureVMPlacementPath+"/"+source.ID, "", "", nil), 200, &sourceHold)
	if !sourceHold.BootVolumeRetained || sourceHold.State != "released" {
		t.Fatalf("cold migration changed source hold: %+v", sourceHold)
	}
	var replayedVM kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: source.ID, Volume: vm.Volume}), 202, &replayedVM)
	if replayedVM != queued {
		t.Fatalf("original submission lost its identity: %+v", replayedVM)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", fixtureVMHostPath+"/vm-work/"+vm.ID+"/arm", "", "",
		kvm.VMCommand{AgentIdentity: kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}, Revision: queued.Revision}), 403, nil)
	checkMigrationExecution(t, first, second, destination, queued, contents)
}

func checkExpiredMigrationTarget(t *testing.T, db *sql.DB, first, second http.Handler, path, revision string, input kvm.ColdMigrationInput, source kvm.Placement) {
	t.Helper()
	expired := reserveMigrationTarget(t, first, source, "expired-target")
	input.ID, input.Placement = "expired-migration", expired.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", path, revision, "", input), 202, nil)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_placements SET created_at=clock_timestamp()-interval '3 minutes',
		expires_at=clock_timestamp()-interval '2 minutes' WHERE placement_id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	decision := kvm.ColdMigrationReview{ID: input.ID, Decision: "approve"}
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", path+"/review", revision, "", decision), 409, nil)
	decision.Decision = "reject"
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", path+"/review", revision, "", decision), 200, nil)
}

func reserveMigrationTarget(t *testing.T, handler http.Handler, source kvm.Placement, key string) kvm.Placement {
	t.Helper()
	spec := fixtureKVMPlacement()
	spec.RequiredHost, spec.IPv4 = fixtureMigrationHost, source.Address
	var result kvm.Placement
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "POST", fixtureVMPlacementPath, "", key, spec), 201, &result)
	return result
}

func retireMigrationSource(t *testing.T, handler http.Handler, vm kvm.VM) kvm.VM {
	t.Helper()
	vm = approveKVMVM(t, handler, vm, 200)
	path, work := fixtureVMSitePath+"/"+vm.ID, fixtureVMHostPath+"/vm-work/"+vm.ID
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	command := func() kvm.VMCommand { return kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision} }
	arm := func() {
		t.Helper()
		expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "POST", work+"/arm", "", "", command()), 200, &vm)
	}
	observe := func(evidence string) {
		t.Helper()
		expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "PUT", work+"/observation", "", "",
			kvm.VMObservation{VMCommand: command(), Observed: evidence}), 200, &vm)
	}
	request := func(token, operation string) {
		t.Helper()
		revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
		expectKVM(t, kvmRequest(t, handler, token, "POST", path+"/"+operation, revision, "", nil), 202, &vm)
		vm = approveKVMVM(t, handler, vm, 200)
	}
	arm()
	observe("defined")
	arm()
	observe("running")
	request(fixtureKVMOperator, "shutdown")
	arm()
	observe("defined")
	request(fixtureKVMAdmin, "retirement")
	arm()
	observe("absent")
	observe("absent")
	if vm.State != "retired" {
		t.Fatalf("source was not retired: %+v", vm)
	}
	return vm
}

func checkMigrationRollback(t *testing.T, db *sql.DB, handler http.Handler, vm kvm.VM, target kvm.Placement, review func() *httptest.ResponseRecorder) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_cold_migrations ADD CONSTRAINT fixture_no_migration CHECK (status<>'approved')`); err != nil {
		t.Fatal(err)
	}
	expectKVM(t, review(), 503, nil)
	var current kvm.VM
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &current)
	var hold kvm.Placement
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "GET", fixtureVMPlacementPath+"/"+target.ID, "", "", nil), 200, &hold)
	if current != vm || hold.State != "held" {
		t.Fatalf("failed ledger write changed state: vm=%+v target=%+v", current, hold)
	}
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_cold_migrations DROP CONSTRAINT fixture_no_migration`); err != nil {
		t.Fatal(err)
	}
}

func checkMigrationExecution(t *testing.T, first, second http.Handler, destination string, vm kvm.VM, contents []byte) {
	t.Helper()
	server := httptest.NewTLSServer(second)
	defer server.Close()
	var output bytes.Buffer
	args := []string{"get-cold-migration", "lab", "compute", "site-a", vm.ID}
	if err := runKVMClient(t.Context(), args, server.URL, fixtureKVMOperator, nil, &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var view kvm.ColdMigrationView
	if err := json.Unmarshal(output.Bytes(), &view); err != nil || view.Migration == nil || view.Migration.Status != "approved" {
		t.Fatalf("invalid CLI migration receipt: %s, %v", output.String(), err)
	}
	daemon, runner, api, profile := migrationAgentFixture(t, server, destination, vm, contents)
	for step, want := range []string{"staged", "defining", "defined", "starting", "running"} {
		output.Reset()
		err := runKVMVMJob(t.Context(), api, fixtureMigrationHost, "1", strings.NewReader(profile), &output, runner)
		if (err != nil) != (step == 1 || step == 3) {
			t.Fatalf("unexpected error in %s: %v", want, err)
		}
		var current kvm.VM
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &current)
		if current.State != want {
			t.Fatalf("wanted %s, found %+v", want, current)
		}
	}
	if daemon.id != vm.ID || !daemon.running || !slices.Equal(daemon.writes, []string{"define", "start"}) {
		t.Fatalf("target effects did not converge once: %+v", daemon)
	}
}
