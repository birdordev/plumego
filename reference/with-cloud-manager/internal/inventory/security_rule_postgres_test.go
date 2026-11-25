package inventory

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceSecurityRuleInventory(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	if err := errors.Join(identity.Migrate(ctx, db), identity.MigrateServiceTokens(ctx, db), Migrate(ctx, db)); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, params ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, params...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','owner'),('tenant','ops'),('tenant','data');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','admin','service','fixture','admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role)
			VALUES ('tenant','owner','admin','admin'),('tenant','ops','admin','admin'),('tenant','data','admin','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant','account','owner','aws-ec2','profile','aws:fixture','read_only'),
			('tenant','another','owner','aws-ec2','other-profile','aws:other','read_only');
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id) VALUES ('tenant','account','ops'),('tenant','account','data');
		INSERT INTO cmp_asset_tag_rules VALUES ('tenant','account','team','ops','ops'),('tenant','account','unit','data','data');`)
	const token = "fixture-rule-inventory-admin-bearer-value"
	digest := sha256.Sum256([]byte(token))
	exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant','admin',now()+interval '5 minutes')", digest[:])
	store := NewStore(db)
	ruleKey := Key{Tenant: "tenant", Account: "account", Region: "region", Kind: provider.SecurityGroupRule}
	groupKey := ruleKey
	groupKey.Kind = provider.SecurityGroup
	const group = "sg-01234567"
	rule1 := provider.SecurityRule{Direction: "ingress", Protocol: "tcp", FromPort: 443, ToPort: 443, RemoteCIDR: "192.0.2.0/24"}.Asset("sgr-01234567", group)
	rule2 := provider.ObservedSecurityRule("sgr-01234568", group, "egress", "", "IPv6", nil, nil, "::/0")
	claim := func(key Key) Claim {
		t.Helper()
		claim, err := store.Claim(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return claim
	}
	feed := func(current Claim, next string, objects ...provider.Object) (Claim, error) {
		t.Helper()
		result, done, err := store.ScanPage(ctx, current, fakeDiscovery(func(_ context.Context, _ provider.Scope, cursor string) (provider.Page, error) {
			if cursor != current.cursor {
				t.Fatalf("expected durable cursor %q got %q", current.cursor, cursor)
			}
			return provider.Page{Objects: objects, Next: next}, nil
		}))
		if err == nil && done != (next == "") {
			t.Fatal("incorrect scan completion state")
		}
		return result, err
	}
	assertVisible := func(project string, count int) Assets {
		t.Helper()
		query := Query{Tenant: "tenant", Project: project, Kind: provider.SecurityGroupRule, Limit: 10}
		assets, err := store.List(ctx, token, query)
		if err != nil || len(assets.Items) != count {
			t.Fatalf("list %s returned %+v, expected %d rules: %v", project, assets, count, err)
		}
		export, err := store.Export(ctx, token, query)
		if err != nil || len(export) != count {
			t.Fatalf("export %s returned %+v: %v", project, export, err)
		}
		history, err := store.History(ctx, token, query)
		if err != nil || (len(history.Items) == 0) != (count == 0) {
			t.Fatalf("history %s returned %+v: %v", project, history, err)
		}
		topology, err := store.Topology(ctx, token, query)
		if err != nil || len(topology.Nodes) != count {
			t.Fatalf("topology %s returned %+v: %v", project, topology, err)
		}
		for _, node := range topology.Nodes {
			if len(node.Links) != 1 || node.Links[0].ID != group || node.Asset.Project != project || node.Asset.OwnershipSource != "security_group" {
				t.Fatalf("parent visibility or projection diverged: %+v", node)
			}
		}
		return assets
	}
	if _, err := feed(claim(ruleKey), "", rule1, rule2); err != nil {
		t.Fatal(err)
	}
	other := groupKey
	other.Account = "another"
	if _, err := feed(claim(other), "", provider.Object{ID: group}); err != nil {
		t.Fatal(err)
	}
	assertVisible("owner", 0)
	assertVisible("ops", 0)
	parent := provider.Object{ID: group, Name: "network group", Tags: map[string]string{"team": "ops"}}
	if _, err := feed(claim(groupKey), "", parent); err != nil {
		t.Fatal(err)
	}
	ops := assertVisible("ops", 2)
	assertVisible("owner", 0)
	assertVisible("data", 0)
	const legacyOwner = `INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
		SELECT tenant_id,scope_id,$1,'owner',9 FROM cmp_inventory_scopes WHERE kind='security-group-rule'`
	exec(legacyOwner, rule1.ID)
	assertVisible("owner", 0)
	if _, err := store.TransferOwnership(ctx, token, identity.Scope{Tenant: "tenant", Project: "ops"}, ops.Items[0].OwnershipRevision,
		OwnershipTransfer{Account: ruleKey.Account, Region: ruleKey.Region, Kind: ruleKey.Kind, AssetID: rule1.ID, TargetProject: "data"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("rule allowed an independent transfer: %v", err)
	}
	parent.Tags["unit"] = "data"
	if _, err := feed(claim(groupKey), "", parent); err != nil {
		t.Fatal(err)
	}
	assertVisible("owner", 0)
	assertVisible("ops", 0)
	assertVisible("data", 0)
	delete(parent.Tags, "unit")
	if _, err := feed(claim(groupKey), "", parent); err != nil {
		t.Fatal(err)
	}
	ops = assertVisible("ops", 2)
	moved, err := store.TransferOwnership(ctx, token, identity.Scope{Tenant: "tenant", Project: "ops"}, ops.Items[0].OwnershipRevision,
		OwnershipTransfer{Account: ruleKey.Account, Region: ruleKey.Region, Kind: groupKey.Kind, AssetID: group, TargetProject: "data"})
	if err != nil || moved.Revision <= ops.Items[0].OwnershipRevision {
		t.Fatalf("parent transfer failed: %+v %v", moved, err)
	}
	assertVisible("ops", 0)
	data := assertVisible("data", 2)
	if data.Items[0].OwnershipRevision != moved.Revision {
		t.Fatalf("rule version did not follow parent transfer: %+v", data.Items)
	}
	exec("DELETE FROM cmp_cloud_account_grants WHERE project_id='data'")
	assertVisible("data", 0)
	assertVisible("owner", 0)
	exec("INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id) VALUES ('tenant','account','data')")
	progress, err := feed(claim(ruleKey), "resume-token", rule1)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.ScanPage(ctx, progress, fakeDiscovery(func(context.Context, provider.Scope, string) (provider.Page, error) {
		return provider.Page{}, &provider.Failure{Kind: provider.Throttled}
	}))
	if !provider.IsFailure(err, provider.Throttled) || assertVisible("data", 2).Items[1].State != "observed" {
		t.Fatalf("a failed page modified unseen assets: %v", err)
	}
	if err := store.Yield(ctx, progress); err != nil {
		t.Fatal(err)
	}
	resumed := claim(ruleKey)
	if resumed.cursor != "resume-token" {
		t.Fatalf("failed page lost checkpoint %q", resumed.cursor)
	}
	if _, err := feed(resumed, "", rule1, rule1); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("duplicate rules completed a scan: %v", err)
	}
	if _, err := feed(resumed, ""); err != nil {
		t.Fatal(err)
	}
	if got := assertVisible("data", 2).Items[1].State; got != "missing_candidate" {
		t.Fatalf("complete scan returned unexpected state %q", got)
	}
	removed, err := store.ConfirmMissing(ctx, ruleKey, rule2.ID, fakeObserver(func(context.Context, provider.Scope, string) (provider.Object, error) {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}))
	if removed || !provider.IsFailure(err, provider.OutcomeUnknown) || assertVisible("data", 2).Items[1].State != "missing_candidate" {
		t.Fatalf("ambiguous point lookup removed rule: %t %v", removed, err)
	}
	if _, err := feed(claim(groupKey), ""); err != nil {
		t.Fatal(err)
	}
	assertVisible("data", 0)
}
