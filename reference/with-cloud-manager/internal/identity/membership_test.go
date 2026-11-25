package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"with-cloud-manager/internal/dbtest"
)

func TestAcceptancePostgresAuthorization(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for range 2 {
		if err := Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	exec(t, ctx, db, `
		INSERT INTO cmp_tenants VALUES ('one'), ('two');
		INSERT INTO cmp_projects VALUES ('one', 'owner'), ('one', 'shared'), ('one', 'private'), ('two', 'owner');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
			('one','agent','service','internal','agent-1'), ('one','viewer','human','oidc','viewer-1'),
			('one','approver','human','oidc','approver-1'), ('two','agent','service','internal','agent-2');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
			('one','owner','agent','operator'), ('one','shared','agent','operator'),
			('one','private','agent','operator'), ('one','owner','viewer','viewer'),
			('one','owner','approver','approver'), ('two','owner','agent','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
			VALUES ('one','account','owner','aws-ec2','aws:123','test/account','read_only');
		INSERT INTO cmp_cloud_account_grants VALUES ('one','account','shared',false);
	`)
	store := NewStore(db)
	owner := Scope{"one", "owner", "agent"}
	shared := Scope{"one", "shared", "agent"}
	foreign := Scope{"two", "owner", "agent"}
	checkAccount := func(scope Scope, permission Permission, expected error) {
		t.Helper()
		if _, err := store.AuthorizeAccount(ctx, scope, "account", permission); !errors.Is(err, expected) {
			t.Errorf("authorize account %v %s: got %v, want %v", scope, permission, err, expected)
		}
	}
	checkAccount(owner, View, nil)
	checkAccount(shared, View, nil)
	checkAccount(owner, Execute, ErrForbidden)
	checkAccount(foreign, View, ErrForbidden)
	checkAccount(Scope{"one", "private", "agent"}, View, ErrForbidden)
	exec(t, ctx, db, `UPDATE cmp_cloud_accounts SET mode='write_enabled' WHERE tenant_id='one'`)
	checkAccount(owner, Execute, nil)
	checkAccount(shared, Execute, ErrForbidden)
	checkAccount(Scope{"one", "owner", "viewer"}, Execute, ErrForbidden)
	if _, err := store.AuthorizeProject(ctx, Scope{"one", "owner", "approver"}, Approve); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthorizeProject(ctx, owner, Approve); !errors.Is(err, ErrForbidden) {
		t.Fatalf("operator approval: %v", err)
	}
	exec(t, ctx, db, `UPDATE cmp_cloud_account_grants SET allow_write=true WHERE tenant_id='one'`)
	checkAccount(shared, Execute, nil)
	exec(t, ctx, db, `UPDATE cmp_memberships SET active=false WHERE (tenant_id,project_id,principal_id)=('one','shared','agent')`)
	checkAccount(shared, View, ErrForbidden)
	exec(t, ctx, db, `
		UPDATE cmp_memberships SET active=true WHERE (tenant_id,project_id,principal_id)=('one','shared','agent');
		UPDATE cmp_cloud_accounts SET credential_ref='test/rotated' WHERE tenant_id='one';
	`)
	access, err := store.AuthorizeAccount(ctx, shared, "account", Execute)
	if err != nil || access.MembershipRevision != 3 || access.CredentialRevision != 3 {
		t.Fatalf("restored access revisions: %+v, %v", access, err)
	}
	exec(t, ctx, db, `UPDATE cmp_cloud_accounts SET mode='suspended' WHERE tenant_id='one'`)
	checkAccount(owner, View, ErrForbidden)
	if _, err := store.AuthorizeProject(ctx, owner, View); err != nil {
		t.Fatalf("suspended credential should preserve local project access: %v", err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO cmp_cloud_account_grants VALUES ('two','account','owner',true)"); err == nil {
		t.Fatal("cross-tenant reference succeeded")
	}
}

func exec(t *testing.T, ctx context.Context, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query); err != nil {
		t.Fatal(err)
	}
}
