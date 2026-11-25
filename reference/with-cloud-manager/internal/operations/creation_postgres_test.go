package operations

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type fixtureCreator struct {
	fixtureOperator
	test                     *testing.T
	expected                 provider.InstanceSpec
	scope                    provider.Scope
	launchToken              string
	launches, discoveries    int
	object                   provider.Object
	receipt                  provider.LaunchReceipt
	observedID               string
	launchFailure, readError error
}

func (f *fixtureCreator) CreateInstance(_ context.Context, scope provider.Scope, spec provider.InstanceSpec, token string) (provider.LaunchReceipt, error) {
	if scope != f.scope || spec != f.expected || token != f.launchToken {
		f.test.Error("invalid durable launch scope, parameters, or token")
	}
	f.launches++
	return f.receipt, f.launchFailure
}

func (f *fixtureCreator) FindCreatedInstance(_ context.Context, scope provider.Scope, spec provider.InstanceSpec, token, instanceID string) (provider.Object, error) {
	if scope != f.scope || spec != f.expected || token != f.launchToken {
		f.test.Error("creation was observed outside its original scope")
	}
	f.discoveries++
	f.observedID = instanceID
	return f.object, f.readError
}

func TestAcceptancePersistentInstanceCreation(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	if err := errors.Join(identity.Migrate(ctx, db), identity.MigrateServiceTokens(ctx, db), inventory.Migrate(ctx, db), Migrate(ctx, db)); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, values ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, values...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
		('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		('tenant','project','operator','operator'),('tenant','project','reviewer','approver');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant','account','us-east-1',kind,1,now() FROM (VALUES ('image'),('subnet'),('security-group')) AS v(kind);
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,asset,'available',1 FROM cmp_inventory_scopes AS s
		JOIN (VALUES ('image','ami-01234567'),('subnet','subnet-01234567'),('security-group','sg-01234567')) AS v(kind,asset) USING (kind);
		INSERT INTO cmp_instance_offerings VALUES ('tenant','account','us-east-1','t3.micro');
		UPDATE cmp_inventory_objects SET links='[{"kind":"network","relation":"in_network","id":"vpc-one"}]'
		WHERE external_id IN ('sg-01234567','subnet-01234567');
		INSERT INTO cmp_instance_operation_budgets (tenant_id,project_id,max_managed_instances) VALUES ('tenant','project',1);
		INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','create');`)
	const token = "fixture-create-operator-bearer-token"
	const reviewer = "fixture-create-reviewer-bearer-token"
	for principal, secret := range map[string]string{"operator": token, "reviewer": reviewer} {
		hash := sha256.Sum256([]byte(secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')", hash[:], principal)
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	store := NewStore(db)
	config := CreateInput{Account: "account", Region: "us-east-1", InstanceSpec: provider.InstanceSpec{
		Name: "fixture VM", ImageID: "ami-01234567", Size: "t3.micro", NetworkID: "subnet-01234567", SecurityGroup: "sg-01234567",
	}}
	create := func(key string, expected error) Operation {
		t.Helper()
		op, err := store.SubmitCreation(ctx, token, scope, key, config)
		if !errors.Is(err, expected) {
			t.Fatalf("create %s got %v want %v", key, err, expected)
		}
		return op
	}
	approve := func(op Operation, expected error) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, Approval{Decision: "approve"}); !errors.Is(err, expected) {
			t.Fatalf("approval got %v want %v", err, expected)
		}
	}
	status := func(id, want string) Operation {
		t.Helper()
		op, err := store.Get(ctx, token, scope, id)
		if err != nil || op.Status != want {
			t.Fatalf("status=%s want=%s error=%v", op.Status, want, err)
		}
		return op
	}
	client := &fixtureCreator{test: t, expected: config.InstanceSpec, scope: provider.Scope{
		Provider: "aws-ec2", RemoteIdentity: "aws:fixture", Region: config.Region, Kind: provider.Instance,
	}}
	var expectedRef string
	connect := func(_ context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		if expectedRef != "" && (binding.CredentialRef != expectedRef || binding.Mode != "read_only") {
			t.Fatalf("unexpected credential binding for read-only retry: %+v", binding)
		}
		return client, nil
	}
	step := func() {
		t.Helper()
		if did, err := store.step(ctx, connect); !did || err != nil {
			t.Fatalf("processed=%t error=%v", did, err)
		}
	}
	setToken := func(op Operation) {
		t.Helper()
		if err := db.QueryRowContext(ctx, "SELECT launch_token FROM cmp_instance_launches WHERE operation_id=$1", op.ID).Scan(&client.launchToken); err != nil || client.launchToken == "" || client.launchToken == op.ID {
			t.Fatalf("missing private launch token: %v", err)
		}
	}
	op := create("first", nil)
	status(op.ID, "awaiting_approval")
	create("second", ErrQuota)
	exec("DELETE FROM cmp_instance_offerings")
	approve(op, identity.ErrAccountStale)
	exec("INSERT INTO cmp_instance_offerings VALUES ('tenant','account','us-east-1','t3.micro')")
	exec("ALTER TABLE cmp_operation_tasks ADD CONSTRAINT fail_create_task CHECK (topic<>'cmp.instance.v1') NOT VALID")
	if _, err := store.Approve(ctx, reviewer, scope, op.ID, Approval{Decision: "approve"}); err == nil {
		t.Fatal("queue failure accepted approval")
	}
	status(op.ID, "awaiting_approval")
	exec("ALTER TABLE cmp_operation_tasks DROP CONSTRAINT fail_create_task")
	approve(op, nil)
	setToken(op)
	client.launchFailure = context.DeadlineExceeded
	step()
	status(op.ID, "observing")
	create("second", ErrQuota)
	client.object = provider.Object{ID: "i-01234567", Name: config.Name, Status: "running"}
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", op.ID)
	exec("ALTER TABLE cmp_instance_operation_events ADD CONSTRAINT fail_create_result CHECK (status<>'succeeded') NOT VALID")
	if did, err := store.step(ctx, connect); !did || err == nil {
		t.Fatalf("injected commit error processed=%t error=%v", did, err)
	}
	status(op.ID, "observing")
	var rows int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_inventory_objects WHERE external_id='i-01234567'").Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("failed completion leaked asset rows=%d error=%v", rows, err)
	}
	exec("ALTER TABLE cmp_instance_operation_events DROP CONSTRAINT fail_create_result")
	exec(`INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash)
		VALUES ('creation-fixture','tenant','project',decode(repeat('00',32),'hex'))`)
	exec("UPDATE cmp_operation_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", op.ID)
	step()
	if current := status(op.ID, "succeeded"); current.AssetID != client.object.ID || client.launches != 1 || client.discoveries != 2 {
		t.Fatalf("unexpected convergence %+v writes=%d observations=%d", current, client.launches, client.discoveries)
	}
	var eventAsset, eventReason string
	if err := db.QueryRowContext(ctx, `SELECT payload->>'asset_id',payload->>'reason' FROM (
		SELECT convert_from(body,'UTF8')::jsonb AS payload FROM cmp_operation_event_outbox
		WHERE sink_id='creation-fixture' AND event_type='instance.operation.succeeded'
	) AS data WHERE payload->>'operation_id'=$1`, op.ID).Scan(&eventAsset, &eventReason); err != nil ||
		eventAsset != client.object.ID || eventReason != "created_instance_observed" {
		t.Fatalf("outbox captured state before asset registration: asset=%q reason=%q error=%v", eventAsset, eventReason, err)
	}
	create("second", ErrQuota)
	var revision int64
	if err := db.QueryRowContext(ctx, "SELECT revision FROM cmp_asset_ownership WHERE external_id='i-01234567'").Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("created instance ownership revision=%d error=%v", revision, err)
	}
	deletion, err := store.Submit(ctx, token, scope, "delete-created", revision,
		Input{Account: config.Account, Region: config.Region, AssetID: client.object.ID, Action: "delete"})
	if err != nil {
		t.Fatal(err)
	}
	approve(deletion, nil)
	step()
	client.state = "terminated"
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", deletion.ID)
	step()
	status(deletion.ID, "succeeded")

	crash := create("second", nil)
	approve(crash, nil)
	setToken(crash)
	now, err := store.dbTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.queue.Reserve(ctx, mq.ReserveOptions{Limit: 1, Topics: []string{topic}, ConsumerID: "old-worker", Lease: time.Minute, Now: now})
	if err != nil || len(tasks) != 1 || tasks[0].ID != crash.ID {
		t.Fatalf("could not claim create: %v", err)
	}
	if _, err := store.prepare(ctx, tasks[0], true); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE cmp_operation_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", crash.ID)
	client.object, client.readError = provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	step()
	status(crash.ID, "observing")
	if client.launches != 2 {
		t.Fatalf("takeover did not replay the original launch exactly once: %d", client.launches)
	}
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", crash.ID)
	step()
	status(crash.ID, "observing")
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp(),attempts=max_attempts-1 WHERE id=$1", crash.ID)
	step()
	status(crash.ID, "needs_review")
	create("third", ErrQuota)
	if client.launches != 2 {
		t.Fatal("an ambiguous launch replayed more than once")
	}
	if err := store.transition(ctx, tasks[0], "applying", "observing", "provider_accepted", ""); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("old creator committed a result: %v", err)
	}
	if _, err := store.Resolve(ctx, reviewer, scope, crash.ID, Resolution{Decision: "succeeded", EvidenceRef: "review/fixture"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("manual success bypassed asset confirmation: %v", err)
	}
	request := ObservationRetry{EvidenceRef: "review/recheck"}
	if _, err := store.RetryCreationObservation(ctx, token, scope, crash.ID, request); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("operator could restart observation: %v", err)
	}
	if retry, err := store.RetryCreationObservation(ctx, reviewer, scope, crash.ID, request); err != nil || retry.Status != "observing" {
		t.Fatalf("unexpected observation retry %+v error=%v", retry, err)
	}
	client.object, client.readError = provider.Object{ID: "i-01234568", Name: config.Name, Status: "running"}, nil
	step()
	if op := status(crash.ID, "succeeded"); op.AssetID != "i-01234568" || client.launches != 2 {
		t.Fatalf("read-only retry did not converge: %+v writes=%d", op, client.launches)
	}
	if retry, err := store.RetryCreationObservation(ctx, reviewer, scope, crash.ID, request); err != nil || retry.Status != "succeeded" {
		t.Fatalf("retry replay did not return current status %+v error=%v", retry, err)
	}

	const novaID = "dddddddd-dddd-dddd-dddd-dddddddddddd"
	config = CreateInput{Account: "nova", Region: "RegionOne", InstanceSpec: provider.InstanceSpec{
		Name: "Nova VM", ImageID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Size: "m1-4",
		NetworkID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", SecurityGroup: "cccccccc-cccc-cccc-cccc-cccccccccccc",
	}}
	client.expected = config.InstanceSpec
	client.scope = provider.Scope{Provider: "openstack", RemoteIdentity: "site:project", Region: config.Region, Kind: provider.Instance}
	exec(`UPDATE cmp_instance_operation_budgets SET max_managed_instances=2;
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','nova','project','openstack','fixture/nova','site:project','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant','nova','RegionOne',kind,1,now() FROM (VALUES ('image'),('network'),('security-group')) AS v(kind);
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,asset,state,1 FROM cmp_inventory_scopes AS s JOIN (VALUES
		('image','aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa','active'),
		('network','bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb','ACTIVE'),
		('security-group','cccccccc-cccc-cccc-cccc-cccccccccccc','')) AS v(kind,asset,state) USING (kind) WHERE s.account_id='nova';
		INSERT INTO cmp_instance_offerings VALUES ('tenant','nova','RegionOne','m1-4');`)
	nova := create("nova", nil)
	approve(nova, nil)
	setToken(nova)
	client.object, client.readError = provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	step()
	status(nova.ID, "observing")
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", nova.ID)
	step()
	if client.launches != 3 {
		t.Fatal("Nova was replayed after an ambiguous send")
	}
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", nova.ID)
	client.object, client.readError = provider.Object{ID: novaID, Name: config.Name, Status: "ERROR"}, nil
	step()
	status(nova.ID, "needs_review")
	create("next-nova", ErrQuota)
	exec(`UPDATE cmp_cloud_accounts SET mode='read_only',credential_ref='fixture/nova-v2' WHERE account_id='nova';
		UPDATE cmp_memberships SET role='viewer' WHERE principal_id='operator'`)
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, nova.ID, request); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE cmp_memberships SET role='viewer' WHERE principal_id='reviewer'")
	before := client.discoveries
	step()
	status(nova.ID, "needs_review")
	if client.discoveries != before {
		t.Fatal("revoked reviewer was allowed to observe Nova")
	}
	exec("UPDATE cmp_memberships SET role='approver' WHERE principal_id='reviewer'")
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, nova.ID, ObservationRetry{EvidenceRef: "review/rotated-read"}); err != nil {
		t.Fatal(err)
	}
	expectedRef = "fixture/nova-v2"
	client.object.Status = "ACTIVE"
	step()
	if op := status(nova.ID, "succeeded"); op.AssetID != novaID || client.observedID != "" || client.launches != 3 {
		t.Fatalf("Nova did not recover by correlation: %+v", op)
	}
	expectedRef = ""
	exec(`UPDATE cmp_instance_operation_budgets SET max_managed_instances=3;
		UPDATE cmp_cloud_accounts SET mode='write_enabled' WHERE account_id='nova';
		UPDATE cmp_memberships SET role='operator' WHERE principal_id='operator'`)
	const pinned = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
	receipted := create("next-nova", nil)
	approve(receipted, nil)
	setToken(receipted)
	client.launchFailure, client.receipt = nil, provider.LaunchReceipt{RequestID: "req-verified", ResourceID: pinned}
	step()
	if op := status(receipted.ID, "observing"); op.ProviderRequestID != "req-verified" || op.AssetID != "" {
		t.Fatalf("bad launch receipt persisted: %+v", op)
	}
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", receipted.ID)
	step()
	if op := status(receipted.ID, "needs_review"); op.OutcomeReason != "creation_receipt_mismatch" || client.observedID != pinned || client.launches != 4 {
		t.Fatalf("Nova accepted a mismatched receipt: %+v", op)
	}
	exec("UPDATE cmp_instance_operation_budgets SET max_managed_instances=4")
	duplicate := create("duplicate-asset", nil)
	approve(duplicate, nil)
	setToken(duplicate)
	client.receipt = provider.LaunchReceipt{}
	step()
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", duplicate.ID)
	step()
	if op := status(duplicate.ID, "needs_review"); op.OutcomeReason != "creation_asset_conflict" || op.AssetID != "" {
		t.Fatalf("asset was assigned to more than one creation: %+v", op)
	}
}
