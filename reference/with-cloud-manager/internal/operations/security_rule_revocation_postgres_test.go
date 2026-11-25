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

type revokeRuleFixture struct {
	fixtureOperator
	t          *testing.T
	target     provider.SecurityRuleTarget
	object     provider.Object
	prepare    func()
	reads      int
	writes     int
	result     provider.ActionResult
	observeErr error
}

func (f *revokeRuleFixture) PrepareSecurityRuleRevocation(_ context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.SecurityRuleRevocation, error) {
	f.checkTarget(scope, target)
	if f.prepare != nil {
		f.prepare()
	}
	return func(context.Context) (string, error) {
		f.writes++
		return "", context.DeadlineExceeded
	}, nil
}

func (f *revokeRuleFixture) ObserveSecurityRuleRevocation(_ context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.ActionResult, error) {
	f.checkTarget(scope, target)
	f.reads++
	return f.result, f.observeErr
}

func (f *revokeRuleFixture) Discover(context.Context, provider.Scope, string) (provider.Page, error) {
	return provider.Page{Objects: []provider.Object{f.object}}, nil
}

func (f *revokeRuleFixture) checkTarget(scope provider.Scope, target provider.SecurityRuleTarget) {
	f.t.Helper()
	if scope.Kind != provider.SecurityGroupRule || scope.RemoteIdentity != "aws:fixture" || scope.Region != "us-east-1" || target != f.target {
		f.t.Fatalf("rule observation or write scope changed: %+v %+v", scope, target)
	}
}

func TestAcceptanceSecurityRuleRevocation(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	if err := errors.Join(identity.Migrate(ctx, db), identity.MigrateServiceTokens(ctx, db), inventory.Migrate(ctx, db), Migrate(ctx, db)); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
	    INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
	    INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
	    ('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
	    INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
	    ('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),('tenant','other','operator','operator');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
	    VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
	    INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id,allow_write) VALUES ('tenant','account','other',true);
	    INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash)
	    VALUES ('fixture-revocation-sink','tenant','project',decode(repeat('00',32),'hex'));
	    INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
	    VALUES ('tenant','account','us-east-1','security-group',1,now()),('tenant','account','us-east-1','security-group-rule',1,now());
	    INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
	    SELECT scope_id,value,'private','available',1 FROM cmp_inventory_scopes
	    CROSS JOIN (VALUES ('sg-01234567'),('sg-01234568')) AS groups(value) WHERE kind='security-group';`)
	const operator, reviewer = "fixture-rule-revoke-operator-access-token", "fixture-rule-revoke-reviewer-access-token"
	for principal, secret := range map[string]string{"operator": operator, "reviewer": reviewer} {
		hash := sha256.Sum256([]byte(secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')", hash[:], principal)
	}
	input := RuleRevocation{Account: "account", Region: "us-east-1", ParentID: "sg-01234567", RuleID: "sgr-01234567"}
	object := provider.SecurityRule{Direction: "ingress", Protocol: "tcp", FromPort: 443, ToPort: 443, RemoteCIDR: "192.0.2.0/24"}.Asset(input.RuleID, input.ParentID)
	exec(`INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation,links)
	    SELECT scope_id,$1,$2,'available',1,jsonb_build_array(jsonb_build_object('kind','security-group','relation','in_security_group','id',$3::text))
	    FROM cmp_inventory_scopes WHERE kind='security-group-rule'`, input.RuleID, object.Name, input.ParentID)
	store := NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	client := &revokeRuleFixture{t: t, target: provider.SecurityRuleTarget{ID: input.RuleID, GroupID: input.ParentID, Name: object.Name}, object: object}
	step := func(op Operation) {
		t.Helper()
		exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", op.ID)
		worked, err := store.step(ctx, func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			return client, nil
		})
		if !worked || err != nil {
			t.Fatalf("worker worked=%v error=%v", worked, err)
		}
	}
	submit := func(key string) Operation {
		t.Helper()
		op, err := store.SubmitSecurityRuleRevocation(ctx, operator, scope, key, 1, input)
		if err != nil || op.Status != "awaiting_approval" || op.Action != revokeSecurityRule || op.AssetID != input.RuleID {
			t.Fatalf("operation not accepted: %+v %v", op, err)
		}
		return op
	}
	assertStatus := func(op Operation, status string) {
		t.Helper()
		detail, err := store.Get(ctx, reviewer, scope, op.ID)
		if err != nil || detail.Status != status || detail.NetworkIntent == nil || detail.NetworkIntent.Name != object.Name ||
			detail.NetworkIntent.ParentID != input.ParentID {
			t.Fatalf("operation detail %+v wanted %s: %v", detail, status, err)
		}
	}
	approve := func(op Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	wrong := input
	wrong.ParentID = "sg-01234568"
	if _, err := store.SubmitSecurityRuleRevocation(ctx, operator, scope, "wrong-parent", 1, wrong); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("unrelated parent accepted: %v", err)
	}
	if _, err := store.SubmitSecurityRuleRevocation(ctx, operator, scope, "stale-parent", 2, input); !errors.Is(err, identity.ErrAccountStale) {
		t.Fatalf("stale parent version accepted: %v", err)
	}
	if _, err := store.SubmitSecurityRuleRevocation(ctx, operator, identity.Scope{Tenant: "tenant", Project: "other"}, "wrong-project", 1, input); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("account sharing exposed a rule owned by another project: %v", err)
	}
	stale := submit("revoke-stale-reviewer")
	if _, err := store.Approve(ctx, operator, scope, stale.ID, Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("requester approved own revoke: %v", err)
	}
	approve(stale)
	client.prepare = func() { exec("UPDATE cmp_memberships SET revision=revision+1 WHERE principal_id='reviewer'") }
	step(stale)
	assertStatus(stale, "failed")
	if client.writes != 0 {
		t.Fatal("approval invalidated during provider preflight was dispatched")
	}
	client.prepare = nil
	op := submit("revoke-rule")
	if replay := submit("revoke-rule"); replay.ID != op.ID {
		t.Fatal("replay allocated a second operation")
	}
	if _, err := store.SubmitSecurityRuleRevocation(ctx, operator, scope, "overlap", 1, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("simultaneous operation on same rule accepted: %v", err)
	}
	approve(op)
	step(op)
	assertStatus(op, "observing")
	client.observeErr = &provider.Failure{Kind: provider.InvalidResponse}
	step(op)
	assertStatus(op, "needs_review")
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, Resolution{Decision: "succeeded", EvidenceRef: "fixture/rule"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("asserted success without scoped observations: %v", err)
	}
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, ObservationRetry{EvidenceRef: "fixture/rule"}); err != nil {
		t.Fatal(err)
	}
	client.observeErr, client.result = nil, provider.ActionSucceeded
	step(op)
	assertStatus(op, "observing")
	client.result = provider.ActionPending
	step(op)
	client.result = provider.ActionSucceeded
	step(op)
	assertStatus(op, "observing")
	step(op)
	assertStatus(op, "succeeded")
	if client.writes != 1 || client.reads != 5 {
		t.Fatalf("received writes=%d reads=%d", client.writes, client.reads)
	}
	if replay, err := store.SubmitSecurityRuleRevocation(ctx, operator, scope, "revoke-rule", 1, input); err != nil || replay.ID != op.ID || replay.Status != "succeeded" {
		t.Fatalf("lost success receipt could not be replayed: %+v %v", replay, err)
	}
	inv := inventory.NewStore(db)
	claim, err := inv.Claim(ctx, inventory.Key{Tenant: scope.Tenant, Account: input.Account, Region: input.Region, Kind: provider.SecurityGroupRule}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, done, err := inv.ScanPage(ctx, claim, client); err != nil || !done {
		t.Fatalf("late inventory completion=%v: %v", done, err)
	}
	assets, err := inv.List(ctx, operator, inventory.Query{Tenant: scope.Tenant, Project: scope.Project, Kind: provider.SecurityGroupRule, Limit: 10})
	if err != nil || len(assets.Items) != 0 {
		t.Fatalf("late scan restored the revoked asset: %+v %v", assets, err)
	}
	var events int
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='security-group-rule.operation.succeeded'
	    AND (convert_from(body,'UTF8')::jsonb->>'operation_id')=$1`, op.ID).Scan(&events)
	if err != nil || events != 1 {
		t.Fatalf("outbox events=%d error=%v", events, err)
	}
}
