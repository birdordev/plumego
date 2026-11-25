package identity

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"regexp"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/provider"
)

//go:embed 0007_account_events.sql
var accountEventSchema string

var (
	ErrAccountInvalid  = errors.New("invalid account administration request")
	ErrAccountConflict = errors.New("account action conflicts with current state")
	ErrAccountStale    = errors.New("account revision changed")
)

var accountIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var credentialReference = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,255}$`)

type Account struct {
	ID       string `json:"id"`
	Project  string `json:"project"`
	Provider string `json:"provider"`
	Mode     string `json:"mode"`
	Revision int64  `json:"revision"`
}

type AccountInput struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	CredentialRef string `json:"credential_ref"`
}

type AccountChange struct {
	Action        string `json:"action"`
	CredentialRef string `json:"credential_ref,omitempty"`
	TargetProject string `json:"target_project,omitempty"`
}

type AccountAdmin struct{ db *sql.DB }

func NewAccountAdmin(db *sql.DB) AccountAdmin { return AccountAdmin{db: db} }

func MigrateAccounts(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0007_account_events", accountEventSchema)
}

func (a AccountAdmin) project(ctx context.Context, token string, scope Scope, write bool, fn func(*sql.Tx, Scope) error) error {
	return dbutil.WithTransaction(ctx, a.db, &sql.TxOptions{ReadOnly: !write}, func(tx *sql.Tx) error {
		store := NewStore(tx)
		actor, err := store.AuthenticateRequest(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != scope.Tenant {
			return ErrForbidden
		}
		actor.Project = scope.Project
		if _, err := store.AuthorizeProject(ctx, actor, Manage); err != nil {
			return err
		}
		return fn(tx, actor)
	})
}

const accountFields = "account_id,project_id,provider,mode,credential_revision"

func scanAccount(row *sql.Row) (Account, error) {
	var value Account
	err := row.Scan(&value.ID, &value.Project, &value.Provider, &value.Mode, &value.Revision)
	return value, err
}

func (a AccountAdmin) Get(ctx context.Context, token string, scope Scope, id string) (Account, error) {
	var value Account
	err := a.project(ctx, token, scope, false, func(tx *sql.Tx, actor Scope) error {
		var err error
		value, err = scanAccount(tx.QueryRowContext(ctx, "SELECT "+accountFields+
			" FROM cmp_cloud_accounts WHERE tenant_id=$1 AND project_id=$2 AND account_id=$3", actor.Tenant, actor.Project, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		return err
	})
	return value, err
}

func (a AccountAdmin) Register(ctx context.Context, token string, scope Scope, input AccountInput) (Account, error) {
	if !accountIdentifier.MatchString(input.ID) || !credentialReference.MatchString(input.CredentialRef) ||
		!supportedAccountProvider(input.Provider) {
		return Account{}, ErrAccountInvalid
	}
	var value Account
	err := a.project(ctx, token, scope, true, func(tx *sql.Tx, actor Scope) error {
		var err error
		value, err = scanAccount(tx.QueryRowContext(ctx, `INSERT INTO cmp_cloud_accounts
			(tenant_id,project_id,account_id,provider,credential_ref) VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (tenant_id,account_id) DO NOTHING RETURNING `+accountFields,
			actor.Tenant, actor.Project, input.ID, input.Provider, input.CredentialRef))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAccountConflict
		}
		if err != nil {
			return err
		}
		return recordAccountEvent(ctx, tx, actor, value, "register", "")
	})
	return value, err
}

func supportedAccountProvider(name string) bool {
	switch name {
	case "aws-ec2", "openstack", provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
		return true
	default:
		return false
	}
}

func (a AccountAdmin) Change(ctx context.Context, token string, scope Scope, id string, revision int64, change AccountChange) (Account, error) {
	if !validAccountChange(change) {
		return Account{}, ErrAccountInvalid
	}
	var value Account
	err := a.project(ctx, token, scope, true, func(tx *sql.Tx, actor Scope) error {
		current, err := scanAccount(tx.QueryRowContext(ctx, "SELECT "+accountFields+
			" FROM cmp_cloud_accounts WHERE tenant_id=$1 AND project_id=$2 AND account_id=$3 FOR UPDATE", actor.Tenant, actor.Project, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if current.Revision != revision {
			return ErrAccountStale
		}
		value, err = updateAccount(ctx, tx, actor, current, change)
		if err != nil {
			return err
		}
		return recordAccountEvent(ctx, tx, actor, value, change.Action, change.TargetProject)
	})
	return value, err
}

func validAccountChange(change AccountChange) bool {
	switch change.Action {
	case "rotate":
		return change.TargetProject == "" && credentialReference.MatchString(change.CredentialRef)
	case "suspend", "resume":
		return change.TargetProject == "" && change.CredentialRef == ""
	case "transfer", "share", "unshare":
		return accountIdentifier.MatchString(change.TargetProject) && change.CredentialRef == ""
	default:
		return false
	}
}

func updateAccount(ctx context.Context, tx *sql.Tx, actor Scope, account Account, change AccountChange) (Account, error) {
	if account.Mode == "revoked" {
		return Account{}, ErrAccountConflict
	}
	mode, project := account.Mode, account.Project
	switch change.Action {
	case "rotate":
		if mode != "suspended" {
			mode = "pending"
		}
	case "suspend":
		if mode == "suspended" {
			return Account{}, ErrAccountConflict
		}
		mode = "suspended"
	case "resume":
		if mode != "suspended" {
			return Account{}, ErrAccountConflict
		}
		mode = "pending"
	case "transfer", "share", "unshare":
		if err := changeAccountProjects(ctx, tx, actor, account.ID, change); err != nil {
			return Account{}, err
		}
		if change.Action == "transfer" {
			project = change.TargetProject
		}
	}
	return scanAccount(tx.QueryRowContext(ctx, `UPDATE cmp_cloud_accounts SET mode=$3,project_id=$4,
		credential_ref=COALESCE(NULLIF($5,''),credential_ref),credential_revision=credential_revision+1
		WHERE tenant_id=$1 AND account_id=$2 RETURNING `+accountFields,
		actor.Tenant, account.ID, mode, project, change.CredentialRef))
}

func changeAccountProjects(ctx context.Context, tx *sql.Tx, actor Scope, id string, change AccountChange) error {
	if change.TargetProject == actor.Project {
		return ErrAccountInvalid
	}
	switch change.Action {
	case "unshare":
		_, err := tx.ExecContext(ctx, "DELETE FROM cmp_cloud_account_grants WHERE tenant_id=$1 AND account_id=$2 AND project_id=$3",
			actor.Tenant, id, change.TargetProject)
		return err
	case "transfer", "share":
		target := actor
		target.Project = change.TargetProject
		if _, err := NewStore(tx).AuthorizeProject(ctx, target, Manage); err != nil {
			return err
		}
		if change.Action == "share" {
			_, err := tx.ExecContext(ctx, `INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id)
				VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, actor.Tenant, id, target.Project)
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM cmp_cloud_account_grants WHERE tenant_id=$1 AND account_id=$2", actor.Tenant, id)
		return err
	default:
		return ErrAccountInvalid
	}
}

func recordAccountEvent(ctx context.Context, tx *sql.Tx, actor Scope, account Account, action, target string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_cloud_account_events
		(tenant_id,account_id,actor_id,action,project_id,target_project,revision) VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7)`,
		actor.Tenant, account.ID, actor.Principal, action, actor.Project, target, account.Revision)
	return err
}
