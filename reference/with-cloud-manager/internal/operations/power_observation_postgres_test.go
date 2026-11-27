package operations

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type rebootDuringReview struct {
	fixtureRebootEvidence
	beforeResult func()
}

func (f *rebootDuringReview) ObserveReboot(ctx context.Context, scope provider.Scope, instance, request string) (provider.ActionResult, error) {
	if f.beforeResult != nil {
		f.beforeResult()
	}
	return f.fixtureRebootEvidence.ObserveReboot(ctx, scope, instance, request)
}

func TestAcceptancePowerObservationAuthority(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	if err := errors.Join(identity.Migrate(ctx, db), identity.MigrateServiceTokens(ctx, db), identity.MigrateAccounts(ctx, db),
		identity.MigrateAccountWriteMode(ctx, db), inventory.Migrate(ctx, db), Migrate(ctx, db)); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject)
		SELECT 'tenant',id,'service','fixture',id FROM (VALUES ('operator'),('reviewer'),('alternate'),('administrator')) AS p(id);
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role)
		VALUES ('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),
		('tenant','project','alternate','approver'),('tenant','project','administrator','admin');
		INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
	VALUES ('tenant','account','project','openstack','fixture/ref','nova:fixture','write_enabled');
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant','account','us-east-1','instance',1,now());
		INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,id,id,'stopped',1 FROM cmp_inventory_scopes CROSS JOIN (VALUES ('start'),('reboot'),('rotated')) AS f(id);`)
	const operator = "fixture-power-recovery-requester-token"
	const reviewer = "fixture-power-recovery-reviewer-token"
	const alternate = "fixture-power-recovery-alternate-token"
	for principal, token := range map[string]string{"operator": operator, "reviewer": reviewer, "alternate": alternate} {
		digest := sha256.Sum256([]byte(token))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,'tenant',$2,now()+interval '5 minutes')`, digest[:], principal)
	}
	registerWriteModeAdministrator(t, db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	store := NewStore(db)
	client := &rebootDuringReview{fixtureRebootEvidence: fixtureRebootEvidence{
		fixtureOperator: &fixtureOperator{state: "running"}, result: provider.ActionSucceeded,
	}}
	var readsUseFrozenAccount bool
	connect := func(_ context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		if readsUseFrozenAccount && (binding.Mode != "read_only" || binding.Revision != 2) {
			t.Errorf("unexpected binding in recovery: %+v", binding)
		}
		return client, nil
	}
	step := func(worker Store) {
		t.Helper()
		if did, err := worker.step(ctx, connect); !did || err != nil {
			t.Fatalf("worker processed=%t error=%v", did, err)
		}
	}
	dispatch := func(asset, action string) Operation {
		t.Helper()
		op, err := store.Submit(ctx, operator, scope, asset, 1, Input{Account: "account", Region: "us-east-1", AssetID: asset, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		step(store)
		exec("UPDATE cmp_operation_tasks SET status='dead' WHERE id=$1", op.ID)
		return op
	}
	start, reboot, rotated := dispatch("start", "start"), dispatch("reboot", "reboot"), dispatch("rotated", "start")
	changeFixtureWriteMode(t, db, scope, "account", "disable-write")
	exec("UPDATE cmp_memberships SET active=false,revision=revision+1 WHERE principal_id='operator'")
	readsUseFrozenAccount = true
	observer := store.WithObservationOnly()
	status := func(id, want, reason string) {
		t.Helper()
		op, err := store.Get(ctx, alternate, scope, id)
		if err != nil || op.Status != want || op.OutcomeReason != reason {
			t.Fatalf("operation=%+v want status=%s reason=%s, %v", op, want, reason, err)
		}
	}
	approve := func(token, id, evidence string, repair bool) {
		t.Helper()
		op, err := store.RetryCreationObservation(ctx, token, scope, id, ObservationRetry{EvidenceRef: evidence, RepairQueue: repair})
		if err != nil || op.Status != "observing" || op.ProviderRequestID != "fixture-cloud-request" {
			t.Fatalf("approval=%+v error=%v", op, err)
		}
	}
	approve(reviewer, start.ID, "case/frozen-start", true)
	step(observer)
	status(start.ID, "succeeded", "desired_state_observed")
	approve(reviewer, reboot.ID, "case/first-review", true)
	// 旧版本迁移仅有证据历史，回填当前绑定。较晚的历史行随后不得替代该绑定。
	exec(`DROP TABLE cmp_power_observation_authority;
		DELETE FROM cmp_migrations WHERE version='0155_power_observation_authority'`)
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO cmp_instance_observation_retries
		(operation_id,tenant_id,reviewer_id,evidence_ref,membership_revision,credential_revision,repair_queue,created_at)
		VALUES ($1,'tenant','alternate','case/historical-review',1,2,true,now()+interval '1 hour')`, reboot.ID)
	client.beforeResult = func() {
		exec("UPDATE cmp_memberships SET active=false,revision=revision+1 WHERE principal_id='reviewer'")
	}
	step(observer)
	status(reboot.ID, "needs_review", "observation_unavailable")
	client.beforeResult = nil
	approve(alternate, reboot.ID, "case/second-review", false)
	step(observer)
	status(reboot.ID, "succeeded", "provider_reboot_confirmed")
	if client.sends != 3 || client.observations != 1 || client.checked != 2 {
		t.Fatalf("unexpected dispatch or observation counts: %d/%d/%d", client.sends, client.observations, client.checked)
	}
	account := identity.NewAccountAdmin(db)
	changed, err := account.Change(ctx, writeModeAdministratorToken, scope, "account", 2, identity.AccountChange{Action: "rotate", CredentialRef: "fixture/new"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := identity.NewStore(db).CloudAccountBinding(ctx, scope.Tenant, "account")
	if err != nil || binding.Revision != changed.Revision {
		t.Fatalf("rotation binding=%+v error=%v", binding, err)
	}
	if err := identity.NewStore(db).VerifyCloudAccount(ctx, scope.Tenant, "account", binding, "nova:fixture"); err != nil {
		t.Fatal(err)
	}
	_, err = store.RetryCreationObservation(ctx, alternate, scope, rotated.ID, ObservationRetry{EvidenceRef: "case/rotated", RepairQueue: true})
	if !errors.Is(err, identity.ErrAccountStale) {
		t.Fatalf("credential rotation was accepted as a write-mode change: %v", err)
	}
}
