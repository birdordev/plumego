package operations

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceChangeTicketDispatch(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
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
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('outside');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other'),('outside','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
		SELECT 'tenant',name,'service','fixture',name FROM unnest(ARRAY['admin','reviewer','verifier','other-viewer']) AS names(name);
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		 ('tenant','project','admin','admin'),('tenant','project','reviewer','approver'),
		 ('tenant','project','verifier','viewer'),('tenant','project','other-viewer','viewer'),
		 ('tenant','other','verifier','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		 VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		 VALUES ('tenant','account','us-east-1','instance',1,now());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		 SELECT scope_id,asset,asset,'stopped',1 FROM cmp_inventory_scopes
		 CROSS JOIN (VALUES ('ready'),('revoked'),('expired'),('late'),('plain')) AS objects(asset);`)
	token := func(name string) string { return "fixture-change-ticket-access-token-" + name }
	for _, name := range []string{"admin", "reviewer", "verifier", "other-viewer"} {
		digest := sha256.Sum256([]byte(token(name)))
		exec(`INSERT INTO cmp_service_tokens (tenant_id,principal_id,token_hash,expires_at)
			VALUES ('tenant',$1,$2,clock_timestamp()+interval '5 minutes')`, name, digest[:])
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	store := NewStore(db)
	approve := func(id string) (Operation, error) {
		return store.Approve(ctx, token("reviewer"), scope, id, Approval{Decision: "approve"})
	}
	submit := func(asset string) Operation {
		t.Helper()
		op, err := store.Submit(ctx, token("admin"), scope, asset, 1,
			Input{Account: "account", Region: "us-east-1", AssetID: asset, Action: "start"})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	policy := TicketPolicyInput{Actions: []string{"start"}, Verifier: "verifier"}
	if got, err := store.PublishTicketPolicy(ctx, token("admin"), scope, 0, policy); err != nil || got.Revision != 1 {
		t.Fatalf("publish initial policy: %+v %v", got, err)
	}
	preview, err := store.PreviewBatch(ctx, token("admin"), scope, BatchInput{Items: []BatchItem{
		{Revision: 1, Input: Input{Account: "account", Region: "us-east-1", AssetID: "ready", Action: "start"}},
		{Revision: 1, Input: Input{Account: "account", Region: "us-east-1", AssetID: "revoked", Action: "reboot"}},
	}})
	if err != nil || len(preview.Items) != 2 || !preview.Items[0].TicketRequired || !preview.Items[0].ApprovalRequired ||
		preview.Items[1].TicketRequired || preview.Items[1].ApprovalRequired {
		t.Fatalf("preview did not select ticket policy by operation: %+v %v", preview, err)
	}
	ready, revoked, expired, late := submit("ready"), submit("revoked"), submit("expired"), submit("late")
	for _, op := range []Operation{ready, revoked, expired, late} {
		if op.Status != "awaiting_approval" || op.TicketPolicyRevision != 1 {
			t.Fatalf("operation did not pin policy: %+v", op)
		}
		if _, err := approve(op.ID); !errors.Is(err, ErrTicketRequired) {
			t.Fatalf("approval without evidence: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, "UPDATE cmp_instance_operations SET status='queued' WHERE operation_id=$1", ready.ID); err == nil {
		t.Fatal("legacy process bypassed the database approval guard")
	}
	if got, err := store.PublishTicketPolicy(ctx, token("admin"), scope, 1, TicketPolicyInput{Actions: []string{}}); err != nil || got.Revision != 2 {
		t.Fatalf("disable policy: %+v %v", got, err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	evidence := TicketEvidenceInput{TicketID: "CHG-57", SourceRevision: 5, Decision: "approved", ValidUntil: &expires}
	if _, err := store.RecordChangeTicket(ctx, token("other-viewer"), scope, ready.ID, evidence); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("wrong verifier: %v", err)
	}
	if _, err := store.RecordChangeTicket(ctx, token("verifier"), identity.Scope{Tenant: "tenant", Project: "other"}, ready.ID, evidence); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("cross-project receipt: %v", err)
	}
	var group sync.WaitGroup
	for range 5 {
		group.Go(func() {
			ticket, err := store.RecordChangeTicket(ctx, token("verifier"), scope, ready.ID, evidence)
			if err != nil || !ticket.Ready {
				t.Errorf("identical receipt failed: %+v %v", ticket, err)
			}
		})
	}
	group.Wait()
	var receipts int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_change_ticket_receipts WHERE operation_id=$1", ready.ID).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipt replay wrote %d records: %v", receipts, err)
	}
	if _, err := store.RecordChangeTicket(ctx, token("verifier"), scope, revoked.ID, evidence); !errors.Is(err, ErrConflict) {
		t.Fatalf("ticket ID reused for a different operation: %v", err)
	}
	record := func(id string, input TicketEvidenceInput) ChangeTicket {
		t.Helper()
		ticket, err := store.RecordChangeTicket(ctx, token("verifier"), scope, id, input)
		if err != nil {
			t.Fatal(err)
		}
		return ticket
	}
	exec("UPDATE cmp_principals SET active=false WHERE principal_id='verifier'")
	exec("UPDATE cmp_principals SET active=true WHERE principal_id='verifier'")
	if record(ready.ID, evidence).Ready {
		t.Fatal("restoring a principal resurrected its old receipt")
	}
	evidence.SourceRevision++
	if !record(ready.ID, evidence).Ready {
		t.Fatal("fresh receipt did not restore eligibility")
	}
	revocable := evidence
	revocable.TicketID = "CHG-58"
	record(revoked.ID, revocable)
	expirable := evidence
	expirable.TicketID = "CHG-59"
	record(expired.ID, expirable)
	for _, op := range []Operation{ready, revoked, expired} {
		if saved, err := approve(op.ID); err != nil || saved.Status != "queued" {
			t.Fatalf("independent approval: %+v %v", saved, err)
		}
	}
	verifierActor := token("verifier")
	cancelled := TicketEvidenceInput{TicketID: revocable.TicketID, SourceRevision: 7, Decision: "revoked"}
	if record(revoked.ID, cancelled).Ready {
		t.Fatal("revoked approval still ready")
	}
	if _, err := store.RecordChangeTicket(ctx, verifierActor, scope, revoked.ID, revocable); !errors.Is(err, ErrConflict) {
		t.Fatalf("out-of-order approval resurrected revoked receipt: %v", err)
	}
	exec(`UPDATE cmp_change_ticket_receipts SET valid_until=clock_timestamp()-interval '1 second'
		WHERE operation_id=$1`, expired.ID)
	fixture := &fixtureOperator{state: "running"}
	connect := func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		return fixture, nil
	}
	for range 3 {
		if processed, err := store.step(ctx, connect); err != nil || !processed {
			t.Fatalf("process operation: %v %v", processed, err)
		}
	}
	for _, op := range []Operation{revoked, expired} {
		if got, err := store.Get(ctx, token("admin"), scope, op.ID); err != nil || got.Status != "failed" {
			t.Fatalf("invalid approval left runnable: %+v %v", got, err)
		}
	}
	if fixture.sends != 1 {
		t.Fatalf("expected exactly one cloud send, got %d", fixture.sends)
	}
	lateEvidence := evidence
	lateEvidence.TicketID = "CHG-60"
	record(late.ID, lateEvidence)
	if _, err := approve(late.ID); err != nil {
		t.Fatal(err)
	}
	if processed, err := store.step(ctx, func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error) {
		record(late.ID, TicketEvidenceInput{TicketID: lateEvidence.TicketID, SourceRevision: 7, Decision: "revoked"})
		return fixture, nil
	}); !processed || err != nil || fixture.sends != 1 {
		t.Fatalf("revocation between prepare and arm did not block send: processed=%v error=%v sends=%d", processed, err, fixture.sends)
	}
	if plain := submit("plain"); plain.TicketPolicyRevision != 0 || plain.Status != "queued" {
		t.Fatalf("disabled policy still matched new operations: %+v", plain)
	}
	if processed, err := store.step(ctx, connect); !processed || err != nil {
		t.Fatalf("process operation without policy: %v %v", processed, err)
	}
	exec(`INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant','account','us-east-1',kind,1,now() FROM (VALUES ('image'),('subnet'),('security-group')) AS v(kind);
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation,links)
		SELECT scope_id,asset,asset,'available',1,links::jsonb FROM cmp_inventory_scopes AS s
		JOIN (VALUES ('image','ami-01234567','[]'),
			('subnet','subnet-01234567','[{"kind":"network","relation":"in_network","id":"vpc-one"}]'),
			('security-group','sg-01234567','[{"kind":"network","relation":"in_network","id":"vpc-one"}]')) AS v(kind,asset,links) USING (kind);
		INSERT INTO cmp_instance_offerings VALUES ('tenant','account','us-east-1','t3.micro');`)
	creation := TicketPolicyInput{Actions: []string{"create"}, Verifier: "verifier"}
	if _, err := store.PublishTicketPolicy(ctx, token("admin"), scope, 2, creation); err != nil {
		t.Fatal(err)
	}
	spec := provider.InstanceSpec{Name: "ticket-fixture", Size: "t3.micro", ImageID: "ami-01234567",
		NetworkID: "subnet-01234567", SecurityGroup: "sg-01234567"}
	op, err := store.SubmitCreation(ctx, token("admin"), scope, "ticket-create", CreateInput{
		Account: "account", Region: "us-east-1", InstanceSpec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	launchEvidence := evidence
	launchEvidence.TicketID = "CHG-61"
	record(op.ID, launchEvidence)
	if _, err := approve(op.ID); err != nil {
		t.Fatal(err)
	}
	creator := &fixtureCreator{test: t, expected: spec, scope: provider.Scope{
		Provider: "aws-ec2", RemoteIdentity: "aws:fixture", Region: "us-east-1", Kind: provider.Instance,
	}, launchFailure: context.DeadlineExceeded, readError: &provider.Failure{Kind: provider.OutcomeUnknown}}
	if err := db.QueryRowContext(ctx, "SELECT launch_token FROM cmp_instance_launches WHERE operation_id=$1", op.ID).Scan(&creator.launchToken); err != nil {
		t.Fatal(err)
	}
	stepCreator := func() {
		t.Helper()
		if processed, err := store.step(ctx, func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			return creator, nil
		}); !processed || err != nil {
			t.Fatalf("process creation: %v %v", processed, err)
		}
	}
	stepCreator()
	record(op.ID, TicketEvidenceInput{TicketID: launchEvidence.TicketID, SourceRevision: 7, Decision: "revoked"})
	exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", op.ID)
	stepCreator()
	if creator.launches != 1 || creator.discoveries != 1 {
		t.Fatalf("revoked ticket changed creation recovery: launches=%d observations=%d", creator.launches, creator.discoveries)
	}
}
