package nativeconsole

import (
	"context"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"errors"
	"regexp"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0029_native_console.sql
var schema string

//go:embed 0030_aws_serial_console.sql
var serialSchema string

const SessionLimit = 5 * time.Minute

var (
	ErrInvalid     = errors.New("invalid instance console request")
	ErrConflict    = errors.New("console request conflicts with the existing request")
	ErrStale       = errors.New("console grant is expired, unavailable or changed")
	ErrUnsupported = errors.New("instance console is unavailable for this provider")
	identifier     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	instanceID     = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
	ec2InstanceID  = regexp.MustCompile(`^i-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)
)

type Grant struct {
	ID                string    `json:"id"`
	Requester         string    `json:"requester"`
	Reviewer          string    `json:"reviewer,omitempty"`
	Account           string    `json:"account"`
	Region            string    `json:"region"`
	AssetID           string    `json:"asset_id"`
	Purpose           string    `json:"purpose"`
	Status            string    `json:"status"`
	Revision          int64     `json:"revision"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	Provider          string    `json:"provider"`
	KeyFingerprint    string    `json:"key_fingerprint,omitempty"`
	ProviderRequestID string    `json:"provider_request_id,omitempty"`
}

type Request struct {
	Account      string `json:"account"`
	Region       string `json:"region"`
	AssetID      string `json:"asset_id"`
	Purpose      string `json:"purpose"`
	SSHPublicKey string `json:"ssh_public_key,omitempty"`
}

type Page struct {
	Items []Grant `json:"items"`
	Next  string  `json:"next,omitempty"`
}

type record struct {
	Grant
	scopeID, assetRevision, credentialRevision, requesterRevision, reviewerRevision int64
	keyHash                                                                         []byte
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) Store { return Store{db: db} }

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0029_native_console", schema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0030_aws_serial_console", serialSchema)
}

func authorize(ctx context.Context, tx *sql.Tx, token string, scope identity.Scope, permission identity.Permission) (identity.Scope, error) {
	auth := identity.NewStore(tx)
	actor, err := auth.AuthenticateRequest(ctx, token)
	if err != nil {
		return identity.Scope{}, err
	}
	if actor.Tenant != scope.Tenant {
		return identity.Scope{}, identity.ErrForbidden
	}
	actor.Project = scope.Project
	_, err = auth.AuthorizeProject(ctx, actor, permission)
	return actor, err
}

const grantFields = `grant_id::text,requester_id,COALESCE(reviewer_id,''),account_id,region,asset_id,purpose,
	CASE WHEN status IN ('awaiting_approval','approved','in_use') AND expires_at<=statement_timestamp() THEN 'expired' ELSE status END,
	revision,created_at,expires_at,provider,ssh_key_hash,COALESCE(provider_request_id,'')`
const privateFields = ",scope_id,asset_revision,credential_revision,requester_revision,COALESCE(reviewer_revision,0)"

func scanGrant(row interface{ Scan(...any) error }, g *Grant) error {
	return scanGrantFields(row, g, new([]byte))
}

func scanGrantFields(row interface{ Scan(...any) error }, g *Grant, keyHash *[]byte, extra ...any) error {
	fields := []any{&g.ID, &g.Requester, &g.Reviewer, &g.Account, &g.Region, &g.AssetID, &g.Purpose,
		&g.Status, &g.Revision, &g.CreatedAt, &g.ExpiresAt, &g.Provider, keyHash, &g.ProviderRequestID}
	if err := row.Scan(append(fields, extra...)...); err != nil {
		return err
	}
	if len(*keyHash) > 0 {
		g.KeyFingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(*keyHash)
	}
	return nil
}

func scanRecord(row *sql.Row, rec *record) error {
	return scanGrantFields(row, &rec.Grant, &rec.keyHash, &rec.scopeID, &rec.assetRevision, &rec.credentialRevision, &rec.requesterRevision, &rec.reviewerRevision)
}

func getRecord(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string, lock bool) (record, error) {
	statement := "SELECT " + grantFields + privateFields + " FROM cmp_instance_console_grants WHERE (tenant_id,project_id,grant_id)=($1,$2,$3::uuid)"
	if lock {
		statement += " FOR UPDATE"
	}
	var rec record
	err := scanRecord(tx.QueryRowContext(ctx, statement, actor.Tenant, actor.Project, id), &rec)
	if errors.Is(err, sql.ErrNoRows) {
		return record{}, identity.ErrForbidden
	}
	return rec, err
}

func (s Store) Get(ctx context.Context, token string, scope identity.Scope, id string) (Grant, error) {
	if !instanceID.MatchString(id) {
		return Grant{}, ErrInvalid
	}
	var rec record
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := authorize(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		rec, err = getRecord(ctx, tx, actor, id, false)
		return err
	})
	return rec.Grant, err
}

func (s Store) List(ctx context.Context, token string, scope identity.Scope, after string, limit int) (Page, error) {
	if limit < 1 || limit > 100 || (after != "" && !instanceID.MatchString(after)) {
		return Page{}, ErrInvalid
	}
	page := Page{Items: make([]Grant, 0)}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) (resultErr error) {
		actor, err := authorize(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		var bound time.Time
		if after != "" {
			anchor, err := getRecord(ctx, tx, actor, after, false)
			if err != nil {
				return err
			}
			bound = anchor.CreatedAt
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+grantFields+` FROM cmp_instance_console_grants
			WHERE (tenant_id,project_id)=($1,$2) AND ($4='' OR (created_at,grant_id)<($5,NULLIF($4,'')::uuid))
			ORDER BY created_at DESC,grant_id DESC LIMIT $3`, actor.Tenant, actor.Project, limit+1, after, bound)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
		for rows.Next() {
			if len(page.Items) == limit {
				page.Next = page.Items[limit-1].ID
				break
			}
			var item Grant
			if err := scanGrant(rows, &item); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func (s Store) Request(ctx context.Context, token string, scope identity.Scope, key string, assetRevision int64, req Request) (Grant, error) {
	if !identifier.MatchString(key) || !identifier.MatchString(req.Account) || !identifier.MatchString(req.Region) ||
		(!instanceID.MatchString(req.AssetID) && !ec2InstanceID.MatchString(req.AssetID)) || assetRevision < 1 ||
		(req.Purpose != "troubleshoot" && req.Purpose != "maintenance" && req.Purpose != "incident") {
		return Grant{}, ErrInvalid
	}
	var grant Grant
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := authorize(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		target, err := inventory.ResolveInstanceMutation(ctx, tx, actor, req.Account, req.Region, req.AssetID)
		if err != nil {
			return err
		}
		keyHash, err := grantKeyHash(ctx, target.Provider, req)
		if err != nil {
			return err
		}
		if target.OwnershipRevision != assetRevision {
			return ErrStale
		}
		err = scanGrant(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_console_grants
			(tenant_id,project_id,requester_id,account_id,region,scope_id,asset_id,purpose,idempotency_key,
			 asset_revision,credential_revision,requester_revision,actor_id,provider,ssh_key_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$3,$13,$14)
			ON CONFLICT (tenant_id,project_id,requester_id,idempotency_key) DO NOTHING RETURNING `+grantFields,
			actor.Tenant, actor.Project, actor.Principal, req.Account, req.Region, target.ScopeID, req.AssetID, req.Purpose,
			key, assetRevision, target.CredentialRevision, target.MembershipRevision, target.Provider, keyHash), &grant)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var prior record
		err = scanRecord(tx.QueryRowContext(ctx, "SELECT "+grantFields+privateFields+` FROM cmp_instance_console_grants
			WHERE (tenant_id,project_id,requester_id,idempotency_key)=($1,$2,$3,$4)`, actor.Tenant, actor.Project, actor.Principal, key), &prior)
		if err != nil {
			return err
		}
		if prior.Account != req.Account || prior.Region != req.Region || prior.AssetID != req.AssetID ||
			prior.Purpose != req.Purpose || prior.assetRevision != assetRevision || prior.Provider != target.Provider ||
			subtle.ConstantTimeCompare(prior.keyHash, keyHash) != 1 {
			return ErrConflict
		}
		grant = prior.Grant
		return nil
	})
	if err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func grantKeyHash(ctx context.Context, providerName string, req Request) ([]byte, error) {
	switch providerName {
	case "openstack":
		if _, ok := identity.BrowserSessionFromContext(ctx); !ok {
			return nil, identity.ErrForbidden
		}
		if !instanceID.MatchString(req.AssetID) || req.SSHPublicKey != "" {
			return nil, ErrInvalid
		}
		return nil, nil
	case "aws-ec2":
		_, keyHash, err := provider.NormalizeSerialPublicKey(req.SSHPublicKey)
		if err != nil || !ec2InstanceID.MatchString(req.AssetID) {
			return nil, ErrInvalid
		}
		return keyHash[:], nil
	default:
		return nil, ErrUnsupported
	}
}

func (s Store) Decide(ctx context.Context, token string, scope identity.Scope, id string, revision int64, decision string) (Grant, error) {
	if !instanceID.MatchString(id) || revision < 1 || (decision != "approve" && decision != "reject") {
		return Grant{}, ErrInvalid
	}
	var grant Grant
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		reviewer, err := authorize(ctx, tx, token, scope, identity.Approve)
		if err != nil {
			return err
		}
		rec, err := getRecord(ctx, tx, reviewer, id, true)
		if err != nil {
			return err
		}
		if rec.Requester == reviewer.Principal {
			return identity.ErrForbidden
		}
		if rec.Status != "awaiting_approval" || rec.Revision != revision {
			return ErrStale
		}
		status := "rejected"
		if decision == "approve" {
			if _, err := validateTarget(ctx, tx, reviewer, rec, true); err != nil {
				return err
			}
			status = "approved"
		}
		member, err := identity.NewStore(tx).AuthorizeProject(ctx, reviewer, identity.Approve)
		if err != nil {
			return err
		}
		err = scanGrant(tx.QueryRowContext(ctx, `UPDATE cmp_instance_console_grants SET
			reviewer_id=$2,reviewer_revision=$3,actor_id=$2,status=$4,revision=revision+1,
			expires_at=CASE WHEN $4='approved' THEN statement_timestamp()+interval '5 minutes' ELSE expires_at END
			WHERE grant_id=$1::uuid AND expires_at>statement_timestamp() RETURNING `+grantFields, id, reviewer.Principal, member.Revision, status), &grant)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		return err
	})
	if err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func (s Store) Revoke(ctx context.Context, token string, scope identity.Scope, id string, revision int64) (Grant, error) {
	if !instanceID.MatchString(id) || revision < 1 {
		return Grant{}, ErrInvalid
	}
	var grant Grant
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := authorize(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		rec, err := getRecord(ctx, tx, actor, id, true)
		if err != nil {
			return err
		}
		if rec.Requester != actor.Principal {
			if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Manage); err != nil {
				return err
			}
		}
		if rec.Revision != revision {
			return ErrStale
		}
		err = scanGrant(tx.QueryRowContext(ctx, `UPDATE cmp_instance_console_grants SET status='revoked',revision=revision+1,actor_id=$2
			WHERE grant_id=$1::uuid AND (status IN ('awaiting_approval','approved') OR (provider='openstack' AND status='in_use'))
			AND expires_at>statement_timestamp()
			RETURNING `+grantFields, id, actor.Principal), &grant)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		return err
	})
	if err != nil {
		return Grant{}, err
	}
	return grant, nil
}

func validateTarget(ctx context.Context, tx *sql.Tx, scope identity.Scope, rec record, lock bool) (inventory.MutationTarget, error) {
	scope.Principal = rec.Requester
	var target inventory.MutationTarget
	var err error
	if lock {
		target, err = inventory.ResolveInstanceMutation(ctx, tx, scope, rec.Account, rec.Region, rec.AssetID)
	} else {
		target, err = inventory.ResolveMutationPreview(ctx, tx, scope, rec.Account, rec.Region, rec.AssetID, provider.Instance)
	}
	if err != nil {
		return target, err
	}
	if target.ScopeID != rec.scopeID || target.OwnershipRevision != rec.assetRevision ||
		target.MembershipRevision != rec.requesterRevision || target.CredentialRevision != rec.credentialRevision || target.Provider != rec.Provider {
		return target, ErrStale
	}
	return target, nil
}
