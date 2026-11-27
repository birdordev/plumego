//go:build linux || darwin

package main

import (
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMVolumeCleanupHistoricalSource(t *testing.T) {
	_, first, second, source, vm := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	vm = retireMigrationSource(t, first, vm)
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent"
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", fixtureKVMSite+"/hosts/"+host.ID+"/capacity", "", "", report), 200, nil)
	target := reserveMigrationTarget(t, first, source, "cleanup-historical-target")
	backup := testKVMImage([]byte("frozen historical volume"))
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", backup), 201, nil)
	input := kvm.ColdMigrationInput{ID: "restore-historical", Placement: target.ID, Image: backup.ID, Evidence: strings.Repeat("c", 64)}
	revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	var migration kvm.ColdMigration
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureVMSitePath+"/"+vm.ID+"/cold-migration",
		revision, "", input), 202, &migration)
	cleanupPath := fixtureVMPlacementPath + "/" + source.ID + "/volume-cleanups"
	cleanup := kvm.VolumeCleanupInput{ID: testCleanupID, Backup: backup.ID, Evidence: strings.Repeat("a", 64)}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", cleanupPath, `"1"`, "", cleanup), 409, nil)
	reviewBatchItem(t, second, migration, "approve")
	var old kvm.VolumeCleanup
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", cleanupPath, `"1"`, "", cleanup), 202, &old)
	if old.VM != vm.ID || old.Placement != source.ID || old.Host != source.Host {
		t.Fatalf("cleanup selected the current disk instead of its historical predecessor: %+v", old)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureVMPlacementPath+"/"+target.ID+"/volume-cleanups",
		`"1"`, "", cleanup), 409, nil)
}

func TestAcceptanceKVMVolumeCleanupFencedResolution(t *testing.T) {
	for _, outcome := range []struct {
		state, status string
		retained      bool
	}{
		{"present", "aborted", true},
		{"absent", "completed", false},
	} {
		t.Run(outcome.state, func(t *testing.T) {
			_, first, second, placement, vm := prepareKVMVM(t)
			vm = retireMigrationSource(t, first, vm)
			backup := testKVMImage([]byte("retained offline raw backup"))
			expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", backup), 201, nil)
			path := fixtureVMPlacementPath + "/" + placement.ID + "/volume-cleanups"
			input := kvm.VolumeCleanupInput{ID: testCleanupID, Backup: backup.ID, Evidence: strings.Repeat("a", 64)}
			expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", path, `"1"`, "", input), 202, nil)
			location := path + "/" + input.ID
			var approved kvm.VolumeCleanup
			expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", location+"/review", `"1"`, "",
				kvm.VMReview{Decision: "approve"}), 200, &approved)
			command := kvm.VolumeCleanupCommand{AgentIdentity: kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1},
				Action: "arm", Revision: approved.Revision, PoolSHA256: strings.Repeat("b", 64)}
			agentPath := fixtureVMHostPath + "/volume-cleanups/" + placement.ID + "/" + input.ID
			var armed kvm.VolumeCleanupWork
			expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", agentPath, "", "", command), 200, &armed)
			rev := strconv.Quote(strconv.FormatInt(armed.Cleanup.Revision, 10))
			proof := kvm.VolumeCleanupResolution{Observed: outcome.state, Evidence: strings.Repeat("c", 64)}
			if outcome.state == "absent" {
				proof.SecondEvidence = strings.Repeat("d", 64)
			}
			expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", location+"/resolve", rev, "", proof), 409, nil)
			changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"1"`, "request", 200)
			changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"2"`, "confirm", 200)
			var blockers kvm.HostFenceWorkPage
			expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMHostPath+"/fence/work", "", "", nil), 200, &blockers)
			if blockers.DispatchedVolumeCleanups != 1 {
				t.Fatalf("fence work omitted dispatched cleanup: %+v", blockers)
			}
			var active kvm.VolumeCleanupPage
			expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMHostPath+"/volume-cleanups", "", "", nil), 200, &active)
			if len(active.Items) != 1 || active.Items[0].Placement != placement.ID || active.Items[0].ID != input.ID {
				t.Fatalf("recovery listing omitted the dispatched job: %+v", active)
			}
			changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"3"`, "request-rejoin", 200)
			changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"4"`, "approve-rejoin", 409)
			changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"4"`, "reject-rejoin", 200)
			if outcome.state == "absent" {
				invalid := proof
				invalid.SecondEvidence = proof.Evidence
				expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", location+"/resolve", rev, "", invalid), 400, nil)
			}
			var done, replay kvm.VolumeCleanup
			expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", location+"/resolve", rev, "", proof), 200, &done)
			expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", location+"/resolve", rev, "", proof), 200, &replay)
			if done.Status != outcome.status || done.FinishedAt == nil || replay.FinishedAt == nil ||
				!done.FinishedAt.Equal(*replay.FinishedAt) || done.ResolvedBy != "reviewer" {
				t.Fatalf("wrong fenced resolution: %+v %+v", done, replay)
			}
			var resultingPlacement kvm.Placement
			expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+placement.ID, "", "", nil), 200, &resultingPlacement)
			if resultingPlacement.BootVolumeRetained != outcome.retained || resultingPlacement.State != "released" || done.VM != vm.ID {
				t.Fatalf("resolution incorrectly changed placement: %+v", resultingPlacement)
			}
			expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMHostPath+"/volume-cleanups", "", "", nil), 200, &active)
			if len(active.Items) != 0 {
				t.Fatalf("resolved job remained active: %+v", active)
			}
			changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"5"`, "request-rejoin", 200)
			changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"6"`, "approve-rejoin", 200)
			expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", agentPath, "", "", command), 403, nil)
		})
	}
}
