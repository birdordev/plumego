//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/internal/kvm"
)

const evacuationFixtureFencePath = fixtureVMHostPath + "/fence"
const evacuationFixturePlansPath = evacuationFixtureFencePath + "/evacuation-plans"

func TestAcceptanceKVMEvacuationPlanDispatch(t *testing.T) {
	db, first, second, _, vm1 := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	vm2 := secondEvacuationPlanVM(t, first, second)
	vm1 = startEvacuationSourceVM(t, first, vm1)
	backup := []byte("planned recovery image")
	input := evacuationPlanInput(t, first, backup, vm1, vm2)
	plan := evacuationPlanCLI(t, first, "arm-evacuation-plan", "1", fixtureKVMAdmin, input)
	if plan.Status != "armed" || plan.ActivatedAt != nil || plan.NextAttemptAt != nil || plan.SourceFenceGeneration != 2 || len(plan.Items) != 2 {
		t.Fatalf("invalid armed plan: %+v", plan)
	}
	location := evacuationFixturePlansPath + "/" + plan.ID
	replay := kvmRequest(t, second, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"1"`, "", input)
	var recorded kvm.EvacuationPlan
	expectKVM(t, replay, 201, &recorded)
	if replay.Header().Get("Location") != location || !recorded.ArmedAt.Equal(plan.ArmedAt) {
		t.Fatalf("registration replay changed the receipt: %+v", recorded)
	}
	overlap := input
	overlap.ID = "another"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"1"`, "", overlap), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", location, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "DELETE", location, "", "", nil), 403, nil)

	store := kvm.NewStore(db)
	stepEvacuationPlan(t, store, false)
	changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"1"`, "request", 200)
	stepEvacuationPlan(t, store, false)
	execEvacuationPlanSQL(t, db, `ALTER TABLE cmp_kvm_evacuation_plans ADD CONSTRAINT fixture_block_activation CHECK (activated_at IS NULL)`)
	changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"2"`, "confirm", 503)
	stepEvacuationPlan(t, store, false)
	execEvacuationPlanSQL(t, db, `ALTER TABLE cmp_kvm_evacuation_plans DROP CONSTRAINT fixture_block_activation`)
	changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"2"`, "confirm", 200)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"3"`, "", overlap), 409, nil)

	changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"3"`, "request-rejoin", 200)
	stepEvacuationPlan(t, store, true)
	checkEvacuationPlanRetry(t, second, location, "source_rejoin_pending")
	changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"4"`, "reject-rejoin", 200)
	makeEvacuationPlanDue(t, db)

	destination, report := evacuationPlanTarget(t, first, second)
	stepEvacuationPlan(t, store, true)
	checkEvacuationPlanRetry(t, second, location, "capacity_unavailable")
	assertNoEvacuationBatch(t, db, vm1, vm2)
	stepEvacuationPlan(t, store, false)

	report.Sequence++
	report.Capacity.VCPU = 2
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	makeEvacuationPlanDue(t, db)
	checkEvacuationBatchRollback(t, db, func() { stepEvacuationPlan(t, store, true) })
	checkEvacuationPlanRetry(t, first, location, "storage_retry")
	assertNoEvacuationBatch(t, db, vm1, vm2)

	makeEvacuationPlanDue(t, db)
	submitted := awaitEvacuationPlanReplicas(t, db, first, location)
	if submitted.ActivatedAt == nil || submitted.FinishedAt == nil || submitted.BatchID == "" ||
		submitted.NextAttemptAt != nil || submitted.LastResult != "" {
		t.Fatalf("invalid submitted plan: %+v", submitted)
	}
	queried := evacuationPlanCLI(t, second, "get-evacuation-plan", plan.ID, fixtureKVMOperator, nil)
	if queried.BatchID != submitted.BatchID {
		t.Fatalf("CLI read a different batch: %+v", queried)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "DELETE", location, "", "", nil), 409, nil)
	var batch kvm.EvacuationBatch
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", evacuationFixtureFencePath+"/evacuation-batches/"+submitted.BatchID, "", "", nil), 200, &batch)
	if batch.HostRevision != 5 || batch.Requester != plan.Requester || len(batch.Items) != 2 ||
		batch.Items[0].VM != vm1.ID || batch.Items[1].VM != vm2.ID ||
		batch.Items[0].Status != "pending" || batch.Items[1].Status != "pending" {
		t.Fatalf("invalid batch from plan: %+v", batch)
	}
	for _, id := range []string{vm1.ID, vm2.ID} {
		if automaticEvacuationHolds(t, db, id) != 1 {
			t.Fatalf("expected one hold for %s", id)
		}
	}
	reviewBatchItem(t, second, batch.Items[0], "approve")
	reviewBatchItem(t, first, batch.Items[1], "reject")
	var moved kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm1.ID, "", "", nil), 200, &moved)
	checkMigrationExecution(t, first, second, destination, moved, backup)
}

func TestAcceptanceKVMEvacuationPlanSafety(t *testing.T) {
	for _, scenario := range []struct {
		name, update, result string
	}{
		{"changed grant", `UPDATE cmp_memberships SET revision=revision+1 WHERE (tenant_id,project_id,principal_id)=('lab','compute','administrator')`, "authorization_changed"},
		{"stale VM", "", "vm_or_backup_changed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			db, first, second, _, vm := prepareKVMVM(t)
			other := secondEvacuationPlanVM(t, first, second)
			vm = startEvacuationSourceVM(t, first, vm)
			input := evacuationPlanInput(t, first, []byte("backup"), vm)
			expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"1"`, "", input), 201, nil)
			cancelled := evacuationPlanCLI(t, second, "cancel-evacuation-plan", input.ID, fixtureKVMAdmin, nil)
			if cancelled.Status != "cancelled" || cancelled.CancelledBy == "" || cancelled.FinishedAt == nil {
				t.Fatalf("invalid cancelled plan: %+v", cancelled)
			}
			changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"1"`, "request", 200)
			next := input
			next.ID = "second-plan"
			var armed kvm.EvacuationPlan
			expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"2"`, "", next), 201, &armed)
			if armed.SourceFenceGeneration != 2 || armed.ActivatedAt != nil {
				t.Fatalf("pending fence plan bound to wrong generation: %+v", armed)
			}
			disjoint := kvm.EvacuationPlanInput{ID: "disjoint", Items: []kvm.EvacuationBatchItemInput{input.Items[0]}}
			disjoint.Items[0].VM = other.ID
			disjoint.Items[0].Revision = other.Revision
			expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"2"`, "", disjoint), 201, nil)
			if scenario.update != "" {
				execEvacuationPlanSQL(t, db, scenario.update)
			} else if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_vms SET revision=revision+1 WHERE vm_id=$1`, vm.ID); err != nil {
				t.Fatal(err)
			}
			changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"2"`, "confirm", 200)
			store := kvm.NewStore(db)
			stepEvacuationPlan(t, store, true)
			stepEvacuationPlan(t, store, true)
			stepEvacuationPlan(t, store, false)
			var result kvm.EvacuationPlan
			expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", evacuationFixturePlansPath+"/"+next.ID, "", "", nil), 200, &result)
			if result.Status != "failed" || result.LastResult != scenario.result ||
				result.ActivatedAt == nil || result.FinishedAt == nil || result.NextAttemptAt != nil || result.BatchID != "" {
				t.Fatalf("invalid failed plan: %+v", result)
			}
			result = kvm.EvacuationPlan{}
			expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", evacuationFixturePlansPath+"/"+input.ID, "", "", nil), 200, &result)
			if result.Status != "cancelled" || result.ActivatedAt != nil {
				t.Fatalf("cancelled plan was activated: %+v", result)
			}
			if scenario.update == "" {
				checkEvacuationPlanRetry(t, first, evacuationFixturePlansPath+"/"+disjoint.ID, "capacity_unavailable")
			}
			assertNoEvacuationBatch(t, db, vm, other)
		})
	}
}

func TestAcceptanceKVMEvacuationPlanRebind(t *testing.T) {
	db, first, second, _, vm := prepareKVMVM(t)
	vm = startEvacuationSourceVM(t, first, vm)
	input := evacuationPlanInput(t, first, []byte("backup"), vm)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", evacuationFixturePlansPath, `"1"`, "", input), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 200, nil)
	var plan kvm.EvacuationPlan
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", evacuationFixturePlansPath+"/"+input.ID, "", "", nil), 200, &plan)
	if plan.Status != "failed" || plan.LastResult != "source_changed" || plan.ActivatedAt != nil || plan.FinishedAt == nil {
		t.Fatalf("rebind left the previous generation armed: %+v", plan)
	}
	changeEvacuationPlanFence(t, first, fixtureKVMAdmin, `"2"`, "request", 200)
	changeEvacuationPlanFence(t, second, fixtureKVMReviewer, `"3"`, "confirm", 200)
	stepEvacuationPlan(t, kvm.NewStore(db), false)
	assertNoEvacuationBatch(t, db, vm)
}

func awaitEvacuationPlanReplicas(t *testing.T, db *sql.DB, handler http.Handler, location string) kvm.EvacuationPlan {
	t.Helper()
	var schema string
	if err := db.QueryRowContext(t.Context(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	dsn := os.Getenv("CLOUD_MANAGER_TEST_DATABASE_URL")
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		target, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("invalid PostgreSQL test URL")
		}
		query := target.Query()
		query.Set("search_path", schema)
		target.RawQuery = query.Encode()
		dsn = target.String()
	} else {
		dsn += " search_path=" + schema
	}
	t.Setenv("CLOUD_MANAGER_DATABASE_URL", dsn)
	t.Setenv("CLOUD_MANAGER_CONTROLLER_PROFILES", "")
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	results := make(chan error, 2)
	var controllers sync.WaitGroup
	for range 2 {
		controllers.Go(func() { results <- run(ctx, []string{"kvm-evacuation-controller"}) })
	}
	defer func() {
		cancel()
		controllers.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Error(err)
			}
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var plan kvm.EvacuationPlan
		expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", location, "", "", nil), 200, &plan)
		if plan.Status == "submitted" {
			return plan
		}
		select {
		case <-ctx.Done():
			t.Fatalf("controller did not submit the plan: %+v", plan)
		case <-ticker.C:
		}
	}
}

func evacuationPlanInput(t *testing.T, handler http.Handler, backup []byte, vms ...kvm.VM) kvm.EvacuationPlanInput {
	t.Helper()
	image := testKVMImage(backup)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	input := kvm.EvacuationPlanInput{ID: "rack-recovery"}
	for _, vm := range vms {
		input.Items = append(input.Items, kvm.EvacuationBatchItemInput{
			VM: vm.ID, Revision: vm.Revision, Image: image.ID, Evidence: strings.Repeat("a", 64),
		})
	}
	return input
}

func secondEvacuationPlanVM(t *testing.T, first, second http.Handler) kvm.VM {
	t.Helper()
	var hold kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "plan-vm-two", fixtureKVMPlacement()), 201, &hold)
	var vm kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: hold.ID, Volume: "boot-plan-second"}), 202, &vm)
	return startEvacuationSourceVM(t, second, vm)
}

func evacuationPlanTarget(t *testing.T, first, second http.Handler) (string, kvm.Report) {
	t.Helper()
	const destination = fixtureKVMSite + "/hosts/" + fixtureMigrationHost
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent, host.FailureDomain = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent", "rack-2"
	report.UUID, report.Capacity.VCPU = host.UUID, 1
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	return destination, report
}

func changeEvacuationPlanFence(t *testing.T, handler http.Handler, token, revision, action string, status int) {
	t.Helper()
	change := kvm.HostFenceChange{Action: action, UUID: fixtureKVMUUID}
	if action == "confirm" || action == "request-rejoin" {
		change.EvidenceSHA256 = strings.Repeat("c", 64)
	}
	if action == "request-rejoin" {
		change.ReplacementAgent = "replacement-agent"
	}
	expectKVM(t, kvmRequest(t, handler, token, "POST", evacuationFixtureFencePath, revision, "", change), status, nil)
}

func evacuationPlanCLI(t *testing.T, handler http.Handler, command, argument, token string, input any) kvm.EvacuationPlan {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{command, "lab", "compute", "site-a", "host-one", argument}
	if err := runKVMClient(t.Context(), args, server.URL, token, bytes.NewReader(body), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var plan kvm.EvacuationPlan
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func stepEvacuationPlan(t *testing.T, store kvm.Store, want bool) {
	t.Helper()
	worked, err := store.DispatchEvacuationPlan(t.Context())
	if err != nil || worked != want {
		t.Fatalf("dispatch worked=%t want=%t err=%v", worked, want, err)
	}
}

func checkEvacuationPlanRetry(t *testing.T, handler http.Handler, location, reason string) {
	t.Helper()
	var plan kvm.EvacuationPlan
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", location, "", "", nil), 200, &plan)
	if plan.Status != "armed" || plan.LastResult != reason || plan.ActivatedAt == nil ||
		plan.NextAttemptAt == nil || plan.FinishedAt != nil || plan.BatchID != "" {
		t.Fatalf("invalid retry state: %+v", plan)
	}
}

func makeEvacuationPlanDue(t *testing.T, db *sql.DB) {
	t.Helper()
	execEvacuationPlanSQL(t, db, `UPDATE cmp_kvm_evacuation_plans SET next_attempt_at=clock_timestamp() WHERE next_attempt_at IS NOT NULL`)
}

func execEvacuationPlanSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement); err != nil {
		t.Fatal(err)
	}
}
