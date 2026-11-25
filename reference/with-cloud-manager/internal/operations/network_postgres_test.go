package operations

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type networkFixture struct {
	fixtureOperator
	t                    *testing.T
	expect               provider.NetworkSpec
	expectedKind         provider.Kind
	token, expectedID    string
	writes, reads        int
	receipt              provider.LaunchReceipt
	object               provider.Object
	writeErr, observeErr error
}

func (f *networkFixture) check(scope provider.Scope, spec provider.NetworkSpec, token string) {
	f.t.Helper()
	if scope.Provider != "aws-ec2" || scope.RemoteIdentity != "aws:fixture" || scope.Region != "us-east-1" ||
		scope.Kind != f.expectedKind || !reflect.DeepEqual(spec, f.expect) || token == "" || token != f.token {
		f.t.Error("provider request did not match persisted network intent")
	}
}

func (f *networkFixture) CreateNetwork(_ context.Context, scope provider.Scope, spec provider.NetworkSpec, token string) (provider.LaunchReceipt, error) {
	f.check(scope, spec, token)
	f.writes++
	return f.receipt, f.writeErr
}

func (f *networkFixture) FindCreatedNetwork(_ context.Context, scope provider.Scope, spec provider.NetworkSpec, token, id string) (provider.Object, error) {
	f.check(scope, spec, token)
	if id != f.expectedID {
		f.t.Error("observation failed to use the durable resource ID")
	}
	f.reads++
	return f.object, f.observeErr
}

func TestAcceptanceNetworkCreation(t *testing.T) {
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
	    ('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),
	    ('tenant','other','operator','operator');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
	    VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
	    INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id,allow_write)
	    VALUES ('tenant','account','other',true);
	    INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','create-network'),('tenant','project','create-subnet'),
	    ('tenant','project','create-security-group');
	    INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash)
	    VALUES ('network-fixture','tenant','project',decode(repeat('00',32),'hex'));`)
	const token, reviewer = "fixture-network-operator-bearer-token", "fixture-network-reviewer-bearer-token"
	for principal, secret := range map[string]string{"operator": token, "reviewer": reviewer} {
		hash := sha256.Sum256([]byte(secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')", hash[:], principal)
	}
	store := NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	other := identity.Scope{Tenant: "tenant", Project: "other"}
	const networkID, subnetID = "vpc-12345678", "subnet-12345678"
	network := NetworkInput{Account: "account", Region: "us-east-1", Kind: provider.Network,
		NetworkSpec: provider.NetworkSpec{Name: "fixture network", CIDR: "10.8.0.0/16"}}
	client := &networkFixture{t: t, expectedKind: provider.Network, expect: network.NetworkSpec, writeErr: context.DeadlineExceeded}
	step := func() {
		t.Helper()
		if did, err := store.step(ctx, func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			return client, nil
		}); !did || err != nil {
			t.Fatalf("worker advanced=%t error=%v", did, err)
		}
	}
	status := func(op Operation, want string) Operation {
		t.Helper()
		got, err := store.Get(ctx, token, scope, op.ID)
		if err != nil || got.Status != want || got.ResourceKind != op.ResourceKind {
			t.Fatalf("operation=%+v expected status %s error=%v", got, want, err)
		}
		return got
	}
	approve := func(op Operation, want error) {
		t.Helper()
		_, err := store.Approve(ctx, reviewer, scope, op.ID, Approval{Decision: "approve"})
		if !errors.Is(err, want) {
			t.Fatalf("approval error=%v want=%v", err, want)
		}
	}
	loadToken := func(op Operation) {
		t.Helper()
		err := db.QueryRowContext(ctx, "SELECT launch_token FROM cmp_network_intents WHERE operation_id=$1", op.ID).Scan(&client.token)
		if err != nil || client.token == op.ID || client.token == "" {
			t.Fatalf("absent private creation marker: %v", err)
		}
	}
	release := func(op Operation) {
		exec("UPDATE cmp_operation_tasks SET available_at=clock_timestamp() WHERE id=$1", op.ID)
	}
	checkOutbox := func(op Operation, expectedType, expectedAsset string) {
		t.Helper()
		var kind, asset string
		err := db.QueryRowContext(ctx, `SELECT payload->>'resource_kind',payload->>'asset_id' FROM (
		    SELECT convert_from(body,'UTF8')::jsonb AS payload FROM cmp_operation_event_outbox
		    WHERE sink_id='network-fixture' AND event_type=$1
		    ) AS data WHERE payload->>'operation_id'=$2`, expectedType, op.ID).Scan(&kind, &asset)
		if err != nil || kind != string(op.ResourceKind) || asset != expectedAsset {
			t.Fatalf("outbox kind=%q asset=%q error=%v", kind, asset, err)
		}
	}

	op, err := store.SubmitNetworkCreation(ctx, token, scope, "network", 0, network)
	if err != nil || op.Status != "awaiting_approval" || op.ResourceKind != provider.Network {
		t.Fatalf("submit network: %+v %v", op, err)
	}
	if replay, err := store.SubmitNetworkCreation(ctx, token, scope, "network", 0, network); err != nil || replay.ID != op.ID {
		t.Fatalf("idempotent replay=%+v error=%v", replay, err)
	}
	changed := network
	changed.CIDR = "10.9.0.0/16"
	if _, err := store.SubmitNetworkCreation(ctx, token, scope, "network", 0, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("idempotency conflict=%v", err)
	}
	loadToken(op)
	approve(op, nil)
	step()
	status(op, "observing")
	client.observeErr = &provider.Failure{Kind: provider.InvalidResponse}
	release(op)
	step()
	status(op, "needs_review")
	if _, err := store.Resolve(ctx, reviewer, scope, op.ID, Resolution{Decision: "succeeded", EvidenceRef: "incident/network"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("operator could assert creation without matching evidence: %v", err)
	}
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, op.ID, ObservationRetry{EvidenceRef: "incident/network"}); err != nil {
		t.Fatal(err)
	}
	client.observeErr = nil
	client.object = provider.Object{ID: networkID, Name: network.Name, Status: "available"}
	step()
	if observed := status(op, "succeeded"); observed.AssetID != networkID || client.writes != 1 || client.reads != 2 {
		t.Fatalf("network not recovered safely: %+v writes=%d reads=%d", observed, client.writes, client.reads)
	}
	checkOutbox(op, "network.operation.succeeded", networkID)
	var revision int64
	if err := db.QueryRowContext(ctx, "SELECT revision FROM cmp_asset_ownership WHERE external_id=$1", networkID).Scan(&revision); err != nil || revision < 1 {
		t.Fatalf("parent ownership version=%d error=%v", revision, err)
	}
	subnet := network
	subnet.Kind, subnet.Name, subnet.ParentID, subnet.CIDR = provider.Subnet, "fixture subnet", networkID, "10.8.7.0/24"
	if _, err := store.SubmitNetworkCreation(ctx, token, other, "foreign-subnet", revision, subnet); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("foreign parent accepted: %v", err)
	}
	if _, err := store.SubmitNetworkCreation(ctx, token, scope, "stale-subnet", revision+1, subnet); !errors.Is(err, identity.ErrAccountStale) {
		t.Fatalf("stale version accepted: %v", err)
	}
	subnetOp, err := store.SubmitNetworkCreation(ctx, token, scope, "subnet", revision, subnet)
	if err != nil || subnetOp.Status != "awaiting_approval" || subnetOp.ResourceKind != provider.Subnet {
		t.Fatalf("submit subnet: %+v %v", subnetOp, err)
	}
	overlap := subnet
	overlap.CIDR = "10.8.7.128/25"
	if _, err := store.SubmitNetworkCreation(ctx, token, scope, "overlap", revision, overlap); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlapping subnet reserved: %v", err)
	}
	client.expectedKind, client.expect, client.writeErr = provider.Subnet, subnet.NetworkSpec, nil
	client.receipt, client.expectedID = provider.LaunchReceipt{ResourceID: subnetID, RequestID: "req-subnet"}, subnetID
	client.object = provider.Object{ID: subnetID, Name: subnet.Name, Status: "available"}
	client.object.Link(provider.Network, networkID, "in_network")
	loadToken(subnetOp)
	approve(subnetOp, nil)
	step()
	status(subnetOp, "observing")
	release(subnetOp)
	step()
	if observed := status(subnetOp, "succeeded"); observed.AssetID != subnetID || client.writes != 2 || client.reads != 3 {
		t.Fatalf("subnet confirmation=%+v writes=%d reads=%d", observed, client.writes, client.reads)
	}
	checkOutbox(subnetOp, "subnet.operation.succeeded", subnetID)
	var link, owner string
	err = db.QueryRowContext(ctx, `SELECT o.links->0->>'id',own.project_id FROM cmp_inventory_objects AS o
	    JOIN cmp_asset_ownership AS own USING (scope_id,external_id) WHERE o.external_id=$1`, subnetID).Scan(&link, &owner)
	if err != nil || link != networkID || owner != scope.Project {
		t.Fatalf("asset link=%q owner=%q error=%v", link, owner, err)
	}
	second := subnet
	second.CIDR = "10.8.8.0/24"
	stale, err := store.SubmitNetworkCreation(ctx, token, scope, "stale-on-observation", revision, second)
	if err != nil {
		t.Fatal(err)
	}
	client.expect = second.NetworkSpec
	loadToken(stale)
	approve(stale, nil)
	step()
	exec("UPDATE cmp_asset_ownership SET revision=revision+1 WHERE external_id=$1", networkID)
	release(stale)
	step()
	if got := status(stale, "needs_review"); got.AssetID != "" || client.writes != 3 || client.reads != 3 {
		t.Fatalf("changed parent was accepted: %+v writes=%d reads=%d", got, client.writes, client.reads)
	}

	const groupID = "sg-12345678"
	group := NetworkInput{Account: network.Account, Region: network.Region, Kind: provider.SecurityGroup,
		NetworkSpec: provider.NetworkSpec{Name: "fixture group", ParentID: networkID}}
	if _, err := store.SubmitNetworkCreation(ctx, token, scope, "group-stale-parent", revision, group); !errors.Is(err, identity.ErrAccountStale) {
		t.Fatalf("stale group parent accepted: %v", err)
	}
	groupOp, err := store.SubmitNetworkCreation(ctx, token, scope, "group", revision+1, group)
	if err != nil || groupOp.Status != "awaiting_approval" || groupOp.Action != "create-security-group" {
		t.Fatalf("submit security group: %+v %v", groupOp, err)
	}
	client.expectedKind, client.expect = provider.SecurityGroup, group.NetworkSpec
	client.receipt, client.expectedID = provider.LaunchReceipt{}, ""
	client.writeErr = context.DeadlineExceeded
	client.object = provider.Object{ID: groupID, Name: group.Name, Status: "available"}
	client.object.Link(provider.Network, "vpc-87654321", "in_network")
	loadToken(groupOp)
	approve(groupOp, nil)
	step()
	status(groupOp, "observing")
	release(groupOp)
	step()
	status(groupOp, "needs_review")
	if _, err := store.RetryCreationObservation(ctx, reviewer, scope, groupOp.ID, ObservationRetry{EvidenceRef: "incident/group"}); err != nil {
		t.Fatal(err)
	}
	client.object.Links[0].ID = networkID
	step()
	if got := status(groupOp, "succeeded"); got.AssetID != groupID || client.writes != 4 || client.reads != 5 {
		t.Fatalf("group recovered=%+v writes=%d reads=%d", got, client.writes, client.reads)
	}
	checkOutbox(groupOp, "security-group.operation.succeeded", groupID)

	const ruleID = "sgr-12345678"
	if err := db.QueryRowContext(ctx, "SELECT revision FROM cmp_asset_ownership WHERE external_id=$1", groupID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	rule := NetworkInput{Account: network.Account, Region: network.Region, Kind: provider.SecurityGroupRule,
		NetworkSpec: provider.NetworkSpec{ParentID: groupID, Rule: &provider.SecurityRule{
			Direction: "ingress", Protocol: "tcp", FromPort: 443, ToPort: 443, RemoteCIDR: "192.0.2.0/24",
		}}}
	if _, err := store.SubmitNetworkCreation(ctx, token, other, "foreign-rule", revision, rule); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("foreign security group accepted: %v", err)
	}
	if _, err := store.SubmitNetworkCreation(ctx, token, scope, "stale-rule", revision+1, rule); !errors.Is(err, identity.ErrAccountStale) {
		t.Fatalf("stale security group version accepted: %v", err)
	}
	ruleOp, err := store.SubmitNetworkCreation(ctx, token, scope, "rule", revision, rule)
	if err != nil || ruleOp.Status != "awaiting_approval" || ruleOp.Action != "create-security-group-rule" {
		t.Fatalf("rule submission: %+v %v", ruleOp, err)
	}
	detail := status(ruleOp, "awaiting_approval").NetworkIntent
	if detail == nil || detail.ParentID != groupID || detail.ParentRevision != revision || !reflect.DeepEqual(detail.Rule, rule.Rule) {
		t.Fatalf("reviewer cannot inspect exact rule intent: %+v", detail)
	}
	if replay, err := store.SubmitNetworkCreation(ctx, token, scope, "rule", revision, rule); err != nil || replay.ID != ruleOp.ID {
		t.Fatalf("rule replay op=%+v error=%v", replay, err)
	}
	if _, err := store.SubmitNetworkCreation(ctx, token, scope, "another-rule-key", revision, rule); !errors.Is(err, ErrConflict) {
		t.Fatalf("same rule under a second key accepted: %v", err)
	}
	if _, err := store.Approve(ctx, token, scope, ruleOp.ID, Approval{Decision: "approve"}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-approved rule: %v", err)
	}
	approve(ruleOp, nil)
	exec("UPDATE cmp_memberships SET revision=revision+1 WHERE principal_id='reviewer'")
	step()
	if got := status(ruleOp, "failed"); client.writes != 4 || got.AssetID != "" {
		t.Fatalf("stale approval reached vendor: %+v writes=%d", got, client.writes)
	}
	type submission struct {
		op  Operation
		err error
	}
	results := make(chan submission, 2)
	for _, key := range []string{"authorized-rule-1", "authorized-rule-2"} {
		go func() {
			op, err := store.SubmitNetworkCreation(ctx, token, scope, key, revision, rule)
			results <- submission{op, err}
		}()
	}
	accepted := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			accepted++
			ruleOp = result.op
		} else if !errors.Is(result.err, ErrConflict) {
			t.Fatal(result.err)
		}
	}
	if accepted != 1 {
		t.Fatalf("expected one accepted rule across simultaneous keys, got %d", accepted)
	}
	client.expectedKind, client.expect = provider.SecurityGroupRule, rule.NetworkSpec
	client.object = rule.Rule.Asset(ruleID, groupID)
	loadToken(ruleOp)
	approve(ruleOp, nil)
	step()
	status(ruleOp, "observing")
	release(ruleOp)
	step()
	if got := status(ruleOp, "succeeded"); got.AssetID != ruleID || client.writes != 5 || client.reads != 6 {
		t.Fatalf("rule recovery attempted more than one send: %+v writes=%d reads=%d", got, client.writes, client.reads)
	}
	checkOutbox(ruleOp, "security-group-rule.operation.succeeded", ruleID)
	graph, err := inventory.NewStore(db).Topology(ctx, token, inventory.Query{Tenant: scope.Tenant, Project: scope.Project, Kind: provider.SecurityGroupRule, Limit: 2})
	if err != nil || len(graph.Nodes) != 1 || graph.Nodes[0].Asset.OwnershipSource != "security_group" ||
		len(graph.Nodes[0].Links) != 1 || graph.Nodes[0].Links[0].ID != groupID {
		t.Fatalf("rule didn't inherit parent ownership and topology: %+v error=%v", graph, err)
	}
}
