package inventory

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceTagOwnership(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
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
		INSERT INTO cmp_projects VALUES ('tenant','owner'),('tenant','ops'),('tenant','data');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
			VALUES ('tenant','admin','service','fixture','admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role)
			VALUES ('tenant','owner','admin','admin'),('tenant','ops','admin','admin'),('tenant','data','admin','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant','account','owner','aws-ec2','profile','aws:fixture','read_only');
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id)
			VALUES ('tenant','account','ops'),('tenant','account','data');
		INSERT INTO cmp_asset_tag_rules VALUES
			('tenant','account','team','ops','ops'),('tenant','account','unit','data','data');`)
	const token = "fixture-automatic-ownership-bearer-value"
	digest := sha256.Sum256([]byte(token))
	exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant','admin',now()+interval '5 minutes')", digest[:])
	store := NewStore(db)
	key := Key{"tenant", "account", "test-region", provider.Instance}
	observe := func(tags map[string]string) {
		t.Helper()
		claim, err := store.Claim(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		_, done, err := store.ScanPage(ctx, claim, fakeDiscovery(func(context.Context, provider.Scope, string) (provider.Page, error) {
			return provider.Page{Objects: []provider.Object{{ID: "vm", Name: "fixture-vm", Tags: tags}}}, nil
		}))
		if err != nil || !done {
			t.Fatalf("inventory page done=%v error=%v", done, err)
		}
	}
	inspect := func(project, source string, count int) Asset {
		t.Helper()
		page, err := store.List(ctx, token, Query{Tenant: "tenant", Project: project, Limit: 10})
		if err != nil || len(page.Items) != count {
			t.Fatalf("project %s has %d assets, expected %d: %v", project, len(page.Items), count, err)
		}
		if count == 0 {
			return Asset{}
		}
		asset := page.Items[0]
		if asset.OwnershipSource != source {
			t.Fatalf("unexpected ownership source %q", asset.OwnershipSource)
		}
		return asset
	}
	observe(map[string]string{"team": "ops"})
	mapped := inspect("ops", "tag_rule", 1)
	inspect("owner", "", 0)
	inspect("data", "", 0)
	observe(nil)
	if got := inspect("ops", "tag_rule", 1); got.OwnershipRevision != mapped.OwnershipRevision {
		t.Fatal("an unavailable tag set changed the recorded assignment")
	}
	observe(map[string]string{"team": "ops", "unit": "data"})
	pending := inspect("owner", "pending", 1)
	inspect("ops", "", 0)
	inspect("data", "", 0)
	if pending.OwnershipRevision <= mapped.OwnershipRevision {
		t.Fatal("assignment change did not advance revision")
	}
	observe(map[string]string{})
	inspect("owner", "account", 1)
	inspect("ops", "account", 1)
	observe(map[string]string{"team": "ops"})
	exec("DELETE FROM cmp_cloud_account_grants WHERE project_id='ops'")
	inspect("ops", "", 0)
	pending = inspect("owner", "pending", 1)
	_, err := store.TransferOwnership(ctx, token, identity.Scope{Tenant: "tenant", Project: "owner"}, pending.OwnershipRevision,
		OwnershipTransfer{Account: "account", Region: "test-region", Kind: provider.Instance, AssetID: "vm", TargetProject: "data"})
	if err != nil {
		t.Fatal(err)
	}
	observe(map[string]string{"team": "ops"})
	inspect("data", "manual", 1)
	inspect("owner", "", 0)
}
