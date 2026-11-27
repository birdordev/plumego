package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type fixtureOperator struct {
	sends, observations int
	fail                error
	state               string
}

func (f *fixtureOperator) ApplyInstance(_ context.Context, _ provider.Scope, _, _ string) (string, error) {
	f.sends++
	return "fixture-cloud-request", f.fail
}

func (f *fixtureOperator) Observe(_ context.Context, _ provider.Scope, id string) (provider.Object, error) {
	f.observations++
	return provider.Object{ID: id, Status: f.state}, nil
}

type lateInventoryPage struct{}

func (lateInventoryPage) Discover(context.Context, provider.Scope, string) (provider.Page, error) {
	return provider.Page{Objects: []provider.Object{
		{ID: "terminated", Name: "stale provider result", Status: "running", Tags: map[string]string{"team": "ops"}},
	}}, nil
}

type fixtureRebootEvidence struct {
	*fixtureOperator
	result  provider.ActionResult
	checked int
}

func (f *fixtureRebootEvidence) ObserveReboot(_ context.Context, _ provider.Scope, _, request string) (provider.ActionResult, error) {
	if request != "fixture-cloud-request" {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	f.checked++
	return f.result, nil
}

type fixtureDeletionEvidence struct {
	*fixtureOperator
	checked int
}

func (f *fixtureDeletionEvidence) ObserveDeletion(_ context.Context, _ provider.Scope, _, request string) (provider.ActionResult, error) {
	if request != "fixture-cloud-request" {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	f.checked++
	return provider.ActionSucceeded, nil
}

func TestAcceptanceRecoverableInstanceWorker(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
		func() error { return identity.MigrateAccounts(ctx, db) },
		func() error { return identity.MigrateAccountWriteMode(ctx, db) },
		func() error { return inventory.Migrate(ctx, db) },
		func() error { return Migrate(ctx, db) },
	} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
		  ('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer'),
		  ('tenant','administrator','service','fixture','administrator');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		  ('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),
		  ('tenant','project','administrator','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		  VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		  VALUES ('tenant','account','us-east-1','instance',1,now());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		  SELECT scope_id,asset,asset,'stopped',1 FROM cmp_inventory_scopes
		  CROSS JOIN (VALUES ('online'),('crash'),('revoked'),('reboot'),('reboot-evidence'),
		  ('delete'),('delete-protected'),('delete-unsupported'),('terminated'),('delete-unconfirmed')) AS v(asset);`)
	const token = "fixture-worker-operation-bearer-token"
	hash := sha256.Sum256([]byte(token))
	exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant','operator',now()+interval '5 minutes')", hash[:])
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	store := NewStore(db)
	submit := func(asset, action string) Operation {
		t.Helper()
		op, err := store.Submit(ctx, token, scope, asset, 1, Input{Account: "account", Region: "us-east-1", AssetID: asset, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	client := &fixtureOperator{fail: context.DeadlineExceeded, state: "running"}
	connects := 0
	var selected provider.InstanceOperator = client
	connect := func(_ context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		connects++
		if binding.CredentialRef != "fixture/ref" || binding.RemoteIdentity != "aws:fixture" {
			t.Fatalf("wrong provider credentials: %+v", binding)
		}
		return selected, nil
	}
	step := func() {
		t.Helper()
		if processed, err := store.step(ctx, connect); !processed || err != nil {
			t.Fatalf("worker processed=%t error=%v", processed, err)
		}
	}
	status := func(id, want string) Operation {
		t.Helper()
		op, err := store.Get(ctx, token, scope, id)
		if err != nil || op.Status != want {
			t.Fatalf("operation state=%s want=%s error=%v", op.Status, want, err)
		}
		return op
	}
	online := submit("online", "start")
	exec(`ALTER TABLE cmp_operation_tasks ADD CONSTRAINT block_lease_for_test CHECK (status<>'leased')`)
	if _, err := store.step(ctx, connect); err == nil {
		t.Fatal("rejected lease unexpectedly succeeded")
	}
	var unclaimed bool
	if err := db.QueryRowContext(ctx, `SELECT first_lease_at IS NULL AND attempts=0 FROM cmp_operation_tasks WHERE id=$1`, online.ID).
		Scan(&unclaimed); err != nil || !unclaimed {
		t.Fatalf("failed lease left timing evidence: %t, %v", unclaimed, err)
	}
	exec(`ALTER TABLE cmp_operation_tasks DROP CONSTRAINT block_lease_for_test`)
	step()
	firstLease := func(operation string) time.Time {
		t.Helper()
		var enqueued, leased time.Time
		err := db.QueryRowContext(ctx, `SELECT created_at,first_lease_at FROM cmp_operation_tasks WHERE id=$1`, operation).Scan(&enqueued, &leased)
		if err != nil || leased.IsZero() || leased.Before(enqueued) {
			t.Fatalf("invalid first lease evidence: enqueued=%s lease=%s err=%v", enqueued, leased, err)
		}
		return leased
	}
	onlineLease := firstLease(online.ID)
	if op := status(online.ID, "observing"); op.OutcomeReason != "submission_outcome_unknown" {
		t.Fatalf("wrong outcome: %+v", op)
	}
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", online.ID)
	step()
	status(online.ID, "succeeded")
	if !firstLease(online.ID).Equal(onlineLease) {
		t.Fatal("observation retry replaced the first lease timestamp")
	}
	if client.sends != 1 || client.observations != 1 {
		t.Fatal("timeouts were not reconciled by observation")
	}

	crashed := submit("crash", "start")
	now, err := store.dbTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	leases, err := store.queue.Reserve(ctx, mq.ReserveOptions{Limit: 1, Topics: []string{topic}, ConsumerID: "old", Lease: time.Minute, Now: now})
	if err != nil || len(leases) != 1 || leases[0].ID != crashed.ID {
		t.Fatalf("failed to claim operation: %v", err)
	}
	crashedLease := firstLease(crashed.ID)
	old := leases[0]
	if armed, err := store.prepare(ctx, old, true); err != nil || armed.Status != "applying" {
		t.Fatalf("failed to persist send boundary: %v", err)
	}
	exec("UPDATE cmp_operation_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", crashed.ID)
	step()
	status(crashed.ID, "succeeded")
	if !firstLease(crashed.ID).Equal(crashedLease) {
		t.Fatal("lease takeover replaced the first lease timestamp")
	}
	if client.sends != 1 || client.observations != 2 {
		t.Fatal("recovered operation resent the provider action")
	}
	if err := store.transition(ctx, old, "applying", "observing", "provider_accepted", ""); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("expired worker changed state: %v", err)
	}

	revoked := submit("revoked", "start")
	before := connects
	exec("UPDATE cmp_memberships SET role='viewer' WHERE principal_id='operator'")
	step()
	status(revoked.ID, "failed")
	if connects != before {
		t.Fatal("revoked identity contacted provider")
	}
	exec("UPDATE cmp_memberships SET role='operator' WHERE principal_id='operator'")
	reboot := submit("reboot", "reboot")
	client.fail = nil
	step()
	if op := status(reboot.ID, "needs_review"); op.ProviderRequestID != "fixture-cloud-request" {
		t.Fatalf("reboot evidence missing: %+v", op)
	}
	if client.sends != 2 || client.observations != 2 {
		t.Fatal("reboot was retried or assumed complete")
	}
	withEvidence := &fixtureRebootEvidence{fixtureOperator: client, result: provider.ActionPending}
	selected = withEvidence
	reported := submit("reboot-evidence", "reboot")
	step()
	status(reported.ID, "observing")
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", reported.ID)
	step()
	status(reported.ID, "observing")
	withEvidence.result = provider.ActionSucceeded
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", reported.ID)
	step()
	if op := status(reported.ID, "succeeded"); op.OutcomeReason != "provider_reboot_confirmed" || withEvidence.checked != 2 || client.sends != 3 {
		t.Fatalf("unexpected proof result %+v, sends=%d observations=%d", op, client.sends, withEvidence.checked)
	}
	const reviewer = "fixture-worker-reviewer-bearer-token"
	reviewerHash := sha256.Sum256([]byte(reviewer))
	exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant','reviewer',now()+interval '5 minutes')", reviewerHash[:])
	queueDelete := func(asset string) Operation {
		t.Helper()
		op := submit(asset, "delete")
		if op.Status != "awaiting_approval" {
			t.Fatalf("deletion skipped approval: %+v", op)
		}
		approved, err := store.Approve(ctx, reviewer, scope, op.ID, Approval{Decision: "approve"})
		if err != nil || approved.Status != "queued" {
			t.Fatalf("deletion approval=%+v error=%v", approved, err)
		}
		return op
	}
	protected := queueDelete("delete-protected")
	exec(`INSERT INTO cmp_protected_instances (scope_id,external_id)
		SELECT scope_id,external_id FROM cmp_inventory_objects WHERE external_id='delete-protected'`)
	step()
	status(protected.ID, "failed")
	if client.sends != 3 {
		t.Fatal("instance protected after approval was sent to the provider")
	}
	unsupported := queueDelete("delete-unsupported")
	client.fail = &provider.Failure{Kind: provider.DeletionBlocked}
	step()
	if op := status(unsupported.ID, "failed"); op.OutcomeReason != "deletion_preconditions_unconfirmed" {
		t.Fatalf("unexpected deletion blocker %+v", op)
	}
	deletion := queueDelete("delete")
	client.fail = nil
	deleteProof := &fixtureDeletionEvidence{fixtureOperator: client}
	selected = deleteProof
	step()
	status(deletion.ID, "observing")
	exec("UPDATE cmp_inventory_objects SET removed_at=clock_timestamp() WHERE external_id='delete'")
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", deletion.ID)
	step()
	if op := status(deletion.ID, "succeeded"); op.OutcomeReason != "provider_deletion_confirmed" || deleteProof.checked != 1 || client.sends != 5 {
		t.Fatalf("unexpected deletion evidence %+v, writes=%d, observations=%d", op, client.sends, deleteProof.checked)
	}
	selected = client
	terminated := queueDelete("terminated")
	step()
	status(terminated.ID, "observing")
	client.state = "terminated"
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", terminated.ID)
	exec(`ALTER TABLE cmp_inventory_objects ADD CONSTRAINT force_tombstone_error
		CHECK (external_id<>'terminated' OR deletion_confirmed_at IS NULL) NOT VALID`)
	if processed, err := store.step(ctx, connect); !processed || err == nil {
		t.Fatalf("expected tombstone transaction to fail: processed=%t error=%v", processed, err)
	}
	status(terminated.ID, "observing")
	exec("ALTER TABLE cmp_inventory_objects DROP CONSTRAINT force_tombstone_error")
	exec("UPDATE cmp_operation_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", terminated.ID)
	step()
	status(terminated.ID, "succeeded")
	unknown := queueDelete("delete-unconfirmed")
	client.fail, client.state = context.DeadlineExceeded, "stopped"
	step()
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp(),attempts=max_attempts-1 WHERE id=$1", unknown.ID)
	step()
	if op := status(unknown.ID, "needs_review"); op.OutcomeReason != "observation_budget_exhausted" || client.sends != 7 {
		t.Fatalf("ambiguous deletion was resent or assumed complete: %+v writes=%d", op, client.sends)
	}
	inv := inventory.NewStore(db)
	claim, err := inv.Claim(ctx, inventory.Key{Tenant: "tenant", Account: "account", Region: "us-east-1", Kind: provider.Instance}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, done, err := inv.ScanPage(ctx, claim, lateInventoryPage{}); !done || err != nil {
		t.Fatalf("late scan completed=%t error=%v", done, err)
	}
	var confirmed, history int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM cmp_inventory_objects WHERE deletion_confirmed_at IS NOT NULL AND removed_at IS NOT NULL),
		(SELECT count(*) FROM cmp_inventory_history WHERE external_id='terminated' AND action='removed')`).Scan(&confirmed, &history); err != nil || confirmed != 2 || history != 1 {
		t.Fatalf("confirmed deletions=%d removal events=%d error=%v", confirmed, history, err)
	}
	var done int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_operation_tasks WHERE status='done'").Scan(&done); err != nil || done != 10 {
		t.Fatalf("queue acknowledgments %d error=%v", done, err)
	}
	exec(`INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,asset,'stopped',generation FROM cmp_inventory_scopes
		CROSS JOIN (VALUES ('frozen-queue'),('frozen-during-connect')) AS v(asset)`)
	registerWriteModeAdministrator(t, db)
	frozen := submit("frozen-queue", "start")
	changeFixtureWriteMode(t, db, scope, "account", "disable-write")
	_, err = store.Submit(ctx, token, scope, "blocked-new", 1, Input{Account: "account", Region: "us-east-1", AssetID: "frozen-queue", Action: "start"})
	if !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("disabled account admitted new work: %v", err)
	}
	reopened := changeFixtureWriteMode(t, db, scope, "account", "enable-write")
	previousSends, previousConnects := client.sends, connects
	step()
	status(frozen.ID, "failed")
	if client.sends != previousSends || connects != previousConnects {
		t.Fatal("re-enabled account revived work admitted under the old version")
	}
	preflight, err := store.Submit(ctx, token, scope, "frozen-during-connect", reopened.Revision,
		Input{Account: "account", Region: "us-east-1", AssetID: "frozen-during-connect", Action: "start"})
	if err != nil {
		t.Fatal(err)
	}
	if processed, err := store.step(ctx, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		changeFixtureWriteMode(t, db, scope, "account", "disable-write")
		return connect(ctx, binding)
	}); !processed || err != nil {
		t.Fatalf("worker processed=%t error=%v", processed, err)
	}
	if op := status(preflight.ID, "failed"); op.OutcomeReason != "dispatch_unavailable" || client.sends != previousSends {
		t.Fatalf("freeze did not block a worker between claim and dispatch: %+v", op)
	}
}

const writeModeAdministratorToken = "fixture-worker-write-mode-admin-token"

func registerWriteModeAdministrator(t *testing.T, db *sql.DB) {
	t.Helper()
	digest := sha256.Sum256([]byte(writeModeAdministratorToken))
	if _, err := db.ExecContext(t.Context(), `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','administrator',now()+interval '5 minutes')`, digest[:]); err != nil {
		t.Fatal(err)
	}
}

func changeFixtureWriteMode(t *testing.T, db *sql.DB, scope identity.Scope, account, action string) identity.Account {
	t.Helper()
	binding, err := identity.NewStore(db).CloudAccountBinding(t.Context(), scope.Tenant, account)
	if err != nil {
		t.Fatal(err)
	}
	result, err := identity.NewAccountAdmin(db).Change(t.Context(), writeModeAdministratorToken, scope, account, binding.Revision,
		identity.AccountChange{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
