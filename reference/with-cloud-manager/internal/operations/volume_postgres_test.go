package operations

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type fixtureVolumeOperator struct {
	fixtureOperator
	writes, reads int
	change        provider.VolumeChange
	writtenError  error
	reached       bool
}

func (f *fixtureVolumeOperator) ApplyVolume(_ context.Context, _ provider.Scope, change provider.VolumeChange) (string, error) {
	f.writes++
	f.change = change
	return "req-volume", f.writtenError
}

func (f *fixtureVolumeOperator) ObserveVolume(_ context.Context, _ provider.Scope, _ provider.VolumeChange) (bool, error) {
	f.reads++
	return f.reached, nil
}

func TestAcceptanceTransactionalVolumeOperations(t *testing.T) {
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
		SELECT 'tenant','account','us-east-1',kind,1,statement_timestamp()-interval '1 minute' FROM (VALUES ('instance'),('volume')) AS v(kind);
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,id,id,state,1 FROM cmp_inventory_scopes AS s JOIN (VALUES
		('instance','vm-a','running'),('instance','vm-b','running'),('volume','vol-a','available'),('volume','vol-b','available')) AS v(kind,id,state) USING (kind);`)
	const token = "fixture-volume-operator-bearer-token"
	const reviewer = "fixture-volume-reviewer-bearer-token"
	for user, value := range map[string]string{"operator": token, "reviewer": reviewer} {
		hash := sha256.Sum256([]byte(value))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')", hash[:], user)
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	store := NewStore(db)
	submit := func(key string, input Input, want error) Operation {
		t.Helper()
		result, err := store.Submit(ctx, token, scope, key, 1, input)
		if !errors.Is(err, want) {
			t.Fatalf("submit %s got %v want %v", key, err, want)
		}
		return result
	}
	status := func(id, want string) Operation {
		t.Helper()
		got, err := store.Get(ctx, token, scope, id)
		if err != nil || got.Status != want {
			t.Fatalf("operation status=%s want=%s error=%v", got.Status, want, err)
		}
		return got
	}
	cloud := &fixtureVolumeOperator{writtenError: context.DeadlineExceeded}
	connect := func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		return cloud, nil
	}
	step := func() {
		t.Helper()
		if processed, err := store.step(ctx, connect); !processed || err != nil {
			t.Fatalf("step processed=%t error=%v", processed, err)
		}
	}
	assertLinks := func(expected int) {
		t.Helper()
		var got int
		err := db.QueryRowContext(ctx, `SELECT COALESCE(sum(jsonb_array_length(links)),0) FROM cmp_inventory_objects
			WHERE external_id IN ('vm-a','vol-a')`).Scan(&got)
		if err != nil || got != expected {
			t.Fatalf("bidirectional links=%d expected=%d error=%v", got, expected, err)
		}
	}
	input := Input{Account: "account", Region: "us-east-1", AssetID: "vm-a", Action: "attach-volume", VolumeID: "vol-a", VolumeRevision: 1, Device: "/dev/sdf"}
	attach := submit("attach", input, nil)
	if replay := submit("attach", input, nil); attach.ID != replay.ID || replay.VolumeID != "vol-a" || replay.Device != "/dev/sdf" {
		t.Fatalf("lost volume operation replay: %+v", replay)
	}
	other := input
	other.AssetID = "vm-b"
	submit("same-volume", other, ErrConflict)
	other = input
	other.VolumeID = "vol-b"
	submit("same-instance", other, ErrConflict)
	other = input
	other.VolumeRevision++
	submit("stale-version", other, identity.ErrAccountStale)

	assets := inventory.NewStore(db)
	claim, err := assets.Claim(ctx, inventory.Key{Tenant: scope.Tenant, Account: input.Account, Region: input.Region, Kind: provider.Volume}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	step()
	if state := status(attach.ID, "observing"); state.OutcomeReason != "submission_outcome_unknown" || cloud.writes != 1 || !cloud.change.Attach {
		t.Fatalf("unexpected write outcome %+v writes=%d", state, cloud.writes)
	}
	cloud.reached = true
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", attach.ID)
	exec(`ALTER TABLE cmp_inventory_objects ADD CONSTRAINT abort_volume_projection
		CHECK (external_id<>'vol-a' OR jsonb_array_length(links)=0) NOT VALID`)
	if processed, err := store.step(ctx, connect); !processed || err == nil {
		t.Fatalf("projection fault processed=%t error=%v", processed, err)
	}
	status(attach.ID, "observing")
	assertLinks(0)
	exec("ALTER TABLE cmp_inventory_objects DROP CONSTRAINT abort_volume_projection")
	exec("UPDATE cmp_operation_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", attach.ID)
	step()
	status(attach.ID, "succeeded")
	assertLinks(2)
	if _, _, err := assets.ScanPage(ctx, claim, lateInventoryPage{}); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("stale scan could overwrite verified relationship: %v", err)
	}
	if cloud.writes != 1 || cloud.reads != 2 {
		t.Fatalf("volume attach retried writes=%d reads=%d", cloud.writes, cloud.reads)
	}

	input.Action, input.Device = "detach-volume", ""
	submit("detach-unconfirmed", input, ErrInvalid)
	input.GuestUnmounted = true
	detach := submit("detach-approved", input, nil)
	status(detach.ID, "awaiting_approval")
	if did, err := store.step(ctx, connect); did || err != nil {
		t.Fatalf("unapproved detach processed=%t error=%v", did, err)
	}
	if _, err := store.Approve(ctx, reviewer, scope, detach.ID, Approval{Decision: "approve"}); err != nil {
		t.Fatal(err)
	}
	cloud.writtenError = nil
	step()
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", detach.ID)
	step()
	status(detach.ID, "succeeded")
	assertLinks(0)
	if cloud.writes != 2 || cloud.change.Attach {
		t.Fatalf("detachment not completed safely: %+v writes=%d", cloud.change, cloud.writes)
	}

	type admission struct {
		op  Operation
		err error
	}
	results := make(chan admission, 2)
	for _, instance := range []string{"vm-a", "vm-b"} {
		go func() {
			request := Input{Account: input.Account, Region: input.Region, AssetID: instance, Action: "attach-volume",
				VolumeID: "vol-b", VolumeRevision: 1, Device: "/dev/sdf"}
			op, err := store.Submit(ctx, token, scope, "concurrent-"+instance, 1, request)
			results <- admission{op, err}
		}()
	}
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || !errors.Is(second.err, ErrConflict) {
		t.Fatalf("concurrent volume admission: %v and %v", first.err, second.err)
	}
	exec("UPDATE cmp_cloud_accounts SET mode='read_only'")
	step()
	status(first.op.ID, "failed")
	if cloud.writes != 2 {
		t.Fatal("a revoked account was allowed to mutate a volume")
	}
}
