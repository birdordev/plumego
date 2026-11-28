package apitest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceOperationCLIChanges(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
		func() error { return inventory.Migrate(ctx, db) },
		func() error { return operations.Migrate(ctx, db) },
	} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, params ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, params...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
	    INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
	    INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
	    ('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
	    INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
	    ('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),('tenant','other','reviewer','approver');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode) VALUES
	    ('tenant','aws','project','aws-ec2','fixture/aws','aws:fixture','write_enabled'),
	    ('tenant','neutron','project','openstack','fixture/stack','site:project','write_enabled');
	    INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
	    SELECT 'tenant','aws','us-east-1',kind,1,now() FROM (VALUES ('instance'),('image'),('subnet'),('security-group'),('security-group-rule')) AS v(kind);
	    INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
	    SELECT scope_id,asset,asset,status,1 FROM cmp_inventory_scopes AS s
	    JOIN (VALUES ('instance','i-01234567','running'),('image','ami-01234567','available'),
	    ('subnet','subnet-01234567','available'),('security-group','sg-01234567','')) AS v(kind,asset,status) USING (kind);
	    UPDATE cmp_inventory_objects SET links='[{"kind":"network","id":"vpc-01234567","relation":"in_network"}]'
	    WHERE external_id IN ('sg-01234567','subnet-01234567');
	    INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation,links)
	    SELECT scope_id,'sgr-01234567','ingress tcp 443-443 192.0.2.0/24','available',1,
	    '[{"kind":"security-group","id":"sg-01234567","relation":"in_security_group"}]'
	    FROM cmp_inventory_scopes WHERE kind='security-group-rule';
	    INSERT INTO cmp_instance_offerings VALUES ('tenant','aws','us-east-1','t3.micro');`)
	const operator, reviewer = "fixture-operation-cli-operator-token", "fixture-operation-cli-reviewer-token"
	for principal, secret := range map[string]string{"operator": operator, "reviewer": reviewer} {
		digest := sha256.Sum256([]byte(secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')",
			digest[:], principal)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var lossCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") == "cli-lost-response" && lossCalls.Add(1) == 1 {
			receipt := httptest.NewRecorder()
			app.ServeHTTP(receipt, r)
			if receipt.Code != http.StatusAccepted {
				t.Errorf("expected successful durable submission: %d", receipt.Code)
			}
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return
		}
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	call := func(secret, input, errorText string, args ...string) []byte {
		t.Helper()
		var out bytes.Buffer
		err := commands.RunOperationsClient(ctx, args, server.URL, secret, strings.NewReader(input), &out, nil)
		if errorText != "" {
			if err == nil || !strings.Contains(err.Error(), errorText) || out.Len() != 0 {
				t.Fatalf("CLI %v expected error %q output=%q error=%v", args, errorText, out.String(), err)
			}
		} else if err != nil || !json.Valid(out.Bytes()) {
			t.Fatalf("CLI %v output=%q error=%v", args, out.String(), err)
		}
		return out.Bytes()
	}
	decode := func(data []byte, action, status string) operations.Operation {
		t.Helper()
		var op operations.Operation
		if err := json.Unmarshal(data, &op); err != nil || op.ID == "" || op.Action != action || op.Status != status {
			t.Fatalf("invalid %s extended receipt: %s error=%v", action, data, err)
		}
		return op
	}
	preview := call(operator, "", "", "preview-delete", "tenant", "project", "--account", "aws", "--region", "us-east-1", "--asset-id", "i-01234567")
	var impact operations.DeletionPreview
	if err := json.Unmarshal(preview, &impact); err != nil || impact.OwnershipRevision < 1 || impact.Protected || !impact.ApprovalRequired {
		t.Fatalf("invalid deletion preview: %s error=%v", preview, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_instance_operations").Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview submitted %d operation(s), error=%v", count, err)
	}
	const deletion = `{"account":"aws","region":"us-east-1","asset_id":"i-01234567","action":"delete"}`
	call(operator, deletion, "HTTP 412", "submit", "tenant", "project", "--idempotency-key", "cli-delete", "--if-match", "999")
	submit := []string{"submit", "tenant", "project", "--idempotency-key", "cli-delete", "--if-match", strconv.FormatInt(impact.OwnershipRevision, 10)}
	op := decode(call(operator, deletion, "", submit...), "delete", "awaiting_approval")
	if replay := decode(call(operator, deletion, "", submit...), "delete", "awaiting_approval"); replay.ID != op.ID {
		t.Fatal("submission replay created a different operation")
	}
	call(operator, strings.ReplaceAll(deletion, "delete", "stop"), "HTTP 409", submit...)
	approve := []string{"approve", "tenant", "project", op.ID, "--decision", "approve"}
	call(operator, "", "HTTP 403", approve...)
	call(reviewer, "", "HTTP 403", "approve", "tenant", "other", op.ID, "--decision", "approve")
	decode(call(reviewer, "", "", approve...), "delete", "queued")
	decode(call(reviewer, "", "", approve...), "delete", "queued")
	var queueRows int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_operation_tasks WHERE id=$1", op.ID).Scan(&queueRows); err != nil || queueRows != 1 {
		t.Fatalf("approval replay scheduled %d jobs: %v", queueRows, err)
	}
	exec("UPDATE cmp_instance_operations SET status='needs_review',provider_request_id='req-delete' WHERE operation_id=$1", op.ID)
	exec("UPDATE cmp_operation_tasks SET status='done' WHERE id=$1", op.ID)
	resolve := []string{"resolve", "tenant", "project", op.ID, "--decision", "failed", "--evidence-ref", "incident/delete"}
	call(reviewer, "", "HTTP 409", append(resolve, "--provider-request-id", "req-wrong")...)
	decode(call(reviewer, "", "", append(resolve, "--provider-request-id", "req-delete")...), "delete", "failed")

	const instance = `{"account":"aws","region":"us-east-1","name":"app","image_id":"ami-01234567","size":"t3.micro",
	    "network_id":"subnet-01234567","security_group_id":"sg-01234567"}`
	decode(call(operator, instance, "", "create-instance", "tenant", "project", "--idempotency-key", "cli-create"), "create", "queued")
	const group = `{"account":"neutron","region":"RegionOne","kind":"security-group","name":"private"}`
	create := []string{"create-network", "tenant", "project", "--idempotency-key", "cli-lost-response"}
	call(operator, group, "operations API request failed", create...)
	if lossCalls.Load() != 1 {
		t.Fatalf("CLI resent a submitted request %d times", lossCalls.Load())
	}
	var durableID string
	if err := db.QueryRowContext(ctx, "SELECT operation_id FROM cmp_instance_operations WHERE idempotency_key='cli-lost-response'").Scan(&durableID); err != nil {
		t.Fatal(err)
	}
	groupOp := decode(call(operator, group, "", create...), "create-security-group", "queued")
	if groupOp.ID != durableID || lossCalls.Load() != 2 {
		t.Fatalf("group receipt %q does not match durable intent %q", groupOp.ID, durableID)
	}
	exec("UPDATE cmp_instance_operations SET status='needs_review' WHERE operation_id=$1", groupOp.ID)
	exec("UPDATE cmp_operation_tasks SET status='done' WHERE id=$1", groupOp.ID)
	retry := []string{"retry-observation", "tenant", "project", groupOp.ID, "--evidence-ref", "incident/group"}
	call(operator, "", "HTTP 403", retry...)
	decode(call(reviewer, "", "", retry...), "create-security-group", "observing")
	decode(call(reviewer, "", "", retry...), "create-security-group", "observing")

	const rule = `{"account":"aws","region":"us-east-1","kind":"security-group-rule","parent_id":"sg-01234567",
	    "rule":{"direction":"ingress","protocol":"tcp","from_port":443,"to_port":443,"remote_cidr":"192.0.2.0/24"}}`
	createRule := []string{"create-network", "tenant", "project", "--idempotency-key", "cli-rule"}
	call(operator, rule, "HTTP 412", createRule...)
	createRule = append(createRule, "--if-match", "1")
	call(operator, strings.Replace(rule, "192.0.2.0/24", "192.0.2.1/24", 1), "HTTP 400", createRule...)
	ruleOp := decode(call(operator, rule, "", createRule...), "create-security-group-rule", "awaiting_approval")
	result := decode(call(reviewer, "", "", "get", "tenant", "project", ruleOp.ID), "create-security-group-rule", "awaiting_approval")
	intent := result.NetworkIntent
	if intent == nil || intent.Rule == nil || intent.Rule.RemoteCIDR != "192.0.2.0/24" ||
		intent.Rule.FromPort != 443 || intent.ParentID != "sg-01234567" || intent.ParentRevision != 1 {
		t.Fatalf("CLI did not expose the exact rule for approval: %+v", intent)
	}
	decode(call(reviewer, "", "", "approve", "tenant", "project", ruleOp.ID, "--decision", "approve"), "create-security-group-rule", "queued")
	const revoke = `{"account":"aws","region":"us-east-1","parent_id":"sg-01234567","rule_id":"sgr-01234567"}`
	revokeCLI := []string{"revoke-security-rule", "tenant", "project", "--idempotency-key", "cli-revoke", "--if-match", "1"}
	revocation := decode(call(operator, revoke, "", revokeCLI...), "revoke-security-group-rule", "awaiting_approval")
	gotRevocation := decode(call(reviewer, "", "", "get", "tenant", "project", revocation.ID), "revoke-security-group-rule", "awaiting_approval")
	if gotRevocation.NetworkIntent == nil || gotRevocation.NetworkIntent.Name != "ingress tcp 443-443 192.0.2.0/24" ||
		gotRevocation.NetworkIntent.ParentID != "sg-01234567" || gotRevocation.AssetID != "sgr-01234567" {
		t.Fatalf("reviewer did not receive the frozen rule: %+v", gotRevocation)
	}
	decode(call(reviewer, "", "", "approve", "tenant", "project", revocation.ID, "--decision", "approve"), "revoke-security-group-rule", "queued")
}
