package operations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

const BareMetalReclaimsMigration = "0121_bare_metal_reclaims"

func MigrateBareMetalReclaims(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BareMetalReclaimsMigration)
}

type BareMetalReclaimInput struct {
	Account   string `json:"account"`
	Site      string `json:"region"`
	SystemID  string `json:"system_id"`
	Sanitizer string `json:"sanitizer_id"`
}

type BareMetalReclaim struct {
	BareMetalReclaimInput
	ID                string                    `json:"id"`
	State             string                    `json:"state"`
	OwnershipRevision int64                     `json:"ownership_revision"`
	Requester         string                    `json:"requester_id"`
	Reviewer          string                    `json:"reviewer_id,omitempty"`
	EvidenceSHA256    string                    `json:"evidence_sha256,omitempty"`
	RequestedAt       time.Time                 `json:"requested_at"`
	ReviewedAt        *time.Time                `json:"reviewed_at,omitempty"`
	ClaimedAt         *time.Time                `json:"claimed_at,omitempty"`
	VerifiedAt        *time.Time                `json:"verified_at,omitempty"`
	ConsumedAt        *time.Time                `json:"consumed_at,omitempty"`
	Recovery          *BareMetalReclaimRecovery `json:"recovery,omitempty"`
}

type reclaimRecord struct {
	BareMetalReclaim
	scope, credential, requesterRevision, reviewerRevision, sanitizerRevision, sanitizerGeneration int64
}

const reclaimColumns = `reclaim_id,account_id,site_id,system_id,sanitizer_id,state,ownership_revision,requester_id,
	COALESCE(reviewer_id,''),COALESCE(evidence_sha256,''),requested_at,reviewed_at,claimed_at,verified_at,consumed_at,
	scope_id,credential_revision,requester_revision,COALESCE(reviewer_revision,0),sanitizer_revision,sanitizer_generation`

func scanReclaim(row *sql.Row) (reclaimRecord, error) {
	var r reclaimRecord
	err := row.Scan(&r.ID, &r.Account, &r.Site, &r.SystemID, &r.Sanitizer, &r.State, &r.OwnershipRevision, &r.Requester,
		&r.Reviewer, &r.EvidenceSHA256, &r.RequestedAt, &r.ReviewedAt, &r.ClaimedAt, &r.VerifiedAt, &r.ConsumedAt,
		&r.scope, &r.credential, &r.requesterRevision, &r.reviewerRevision, &r.sanitizerRevision, &r.sanitizerGeneration)
	return r, err
}

func readReclaim(ctx context.Context, tx *sql.Tx, scope identity.Scope, id string) (reclaimRecord, error) {
	r, err := scanReclaim(tx.QueryRowContext(ctx, `SELECT `+reclaimColumns+
		` FROM cmp_bare_metal_reclaims WHERE (tenant_id,project_id,reclaim_id)=($1,$2,$3)`, scope.Tenant, scope.Project, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, identity.ErrForbidden
	}
	return r, err
}

func (s Store) GetBareMetalReclaim(ctx context.Context, token string, scope identity.Scope, id string) (BareMetalReclaim, error) {
	if !operationKey.MatchString(id) {
		return BareMetalReclaim{}, ErrInvalid
	}
	var r reclaimRecord
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		r, err = readReclaim(ctx, tx, actor, id)
		if err != nil {
			return err
		}
		if r.State == "consumed" {
			_, err = identity.NewStore(tx).AuthorizeAccount(ctx, actor, r.Account, identity.View)
		} else {
			_, err = inventory.ReadBareMetalTarget(ctx, tx, actor, r.Account, r.Site, r.SystemID)
		}
		if err != nil {
			return err
		}
		r.Recovery, err = latestReclaimRecovery(ctx, tx, actor, id)
		return err
	})
	return r.BareMetalReclaim, err
}

type BareMetalReclaimTarget struct {
	Account, Site, SystemID string
}

type BareMetalReclaimView struct {
	Reclaim *BareMetalReclaim `json:"reclaim"`
}

func (s Store) CurrentBareMetalReclaim(ctx context.Context, token string, scope identity.Scope, input BareMetalReclaimTarget) (BareMetalReclaimView, error) {
	if !operationKey.MatchString(input.Account) || !operationKey.MatchString(input.Site) || !bareMetalID.MatchString(input.SystemID) {
		return BareMetalReclaimView{}, ErrInvalid
	}
	var view BareMetalReclaimView
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		target, err := inventory.ReadBareMetalTarget(ctx, tx, actor, input.Account, input.Site, input.SystemID)
		if err != nil {
			return err
		}
		r, err := scanReclaim(tx.QueryRowContext(ctx, `SELECT `+reclaimColumns+` FROM cmp_bare_metal_reclaims
			WHERE (tenant_id,project_id,scope_id,system_id)=($1,$2,$3,$4)
			  AND state IN ('requested','approved','claimed','verified')`,
			actor.Tenant, actor.Project, target.ScopeID, input.SystemID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		r.Recovery, err = latestReclaimRecovery(ctx, tx, actor, r.ID)
		if err == nil {
			view.Reclaim = &r.BareMetalReclaim
		}
		return err
	})
	return view, err
}

func (s Store) RequestBareMetalReclaim(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input BareMetalReclaimInput) (BareMetalReclaim, error) {
	if revision < 1 || !operationKey.MatchString(key) || !operationKey.MatchString(input.Account) ||
		!operationKey.MatchString(input.Site) || !bareMetalID.MatchString(input.SystemID) || !operationKey.MatchString(input.Sanitizer) {
		return BareMetalReclaim{}, ErrInvalid
	}
	var r reclaimRecord
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		if actor.Principal == input.Sanitizer {
			return identity.ErrForbidden
		}
		target, err := inventory.ResolveBareMetalObservation(ctx, tx, actor, input.Account, input.Site, input.SystemID)
		if err != nil {
			return err
		}
		r, err = requestReclaim(ctx, tx, actor, target, key, revision, input)
		return err
	})
	return r.BareMetalReclaim, err
}

func requestReclaim(ctx context.Context, tx *sql.Tx, actor identity.Scope, target inventory.MutationTarget, key string, revision int64, input BareMetalReclaimInput) (reclaimRecord, error) {
	r, err := scanReclaim(tx.QueryRowContext(ctx, `SELECT `+reclaimColumns+
		` FROM cmp_bare_metal_reclaims WHERE (tenant_id,project_id,requester_id,request_key)=($1,$2,$3,$4)`,
		actor.Tenant, actor.Project, actor.Principal, key))
	if err == nil {
		if r.BareMetalReclaimInput != input || r.OwnershipRevision != revision {
			return r, ErrConflict
		}
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if target.OwnershipRevision != revision {
		return r, identity.ErrAccountStale
	}
	if err := inventory.CheckBareMetalIdle(ctx, tx, input.SystemID); err != nil {
		return r, err
	}
	if err := inventory.CheckBareMetalOffline(ctx, tx, target.ScopeID, target.CredentialRevision, input.SystemID, nil); err != nil {
		return r, err
	}
	requesterRevision, err := lockProjectRole(ctx, tx, actor, actor.Principal, "admin")
	if err != nil {
		return r, err
	}
	sanitizerRevision, sanitizerGeneration, err := lockTicketVerifier(ctx, tx, actor, input.Sanitizer)
	if err != nil {
		return r, err
	}
	r, err = scanReclaim(tx.QueryRowContext(ctx, `INSERT INTO cmp_bare_metal_reclaims
		(reclaim_id,tenant_id,project_id,scope_id,account_id,site_id,system_id,ownership_revision,credential_revision,
		requester_id,requester_revision,request_key,sanitizer_id,sanitizer_revision,sanitizer_generation,state)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'requested')
		ON CONFLICT DO NOTHING RETURNING `+reclaimColumns, rand.Text(), actor.Tenant, actor.Project, target.ScopeID,
		input.Account, input.Site, input.SystemID, target.OwnershipRevision, target.CredentialRevision,
		actor.Principal, requesterRevision, key, input.Sanitizer, sanitizerRevision, sanitizerGeneration))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrConflict
	}
	if err != nil {
		return r, err
	}
	return r, auditReclaim(ctx, tx, actor, r)
}

func auditReclaim(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) error {
	return recordReclaimAudit(ctx, tx, actor, r, "bare_metal_reclaim_"+r.State, r.State)
}

func recordReclaimAudit(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord, action, outcome string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
		(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,outcome,revision,occurred_at)
		VALUES ($1,$2,'operation',$3,$4,'bare-metal',$5,$6,$7,$8,$9,statement_timestamp())`,
		actor.Tenant, actor.Project, action, r.ID, r.Account, r.Site, actor.Principal, outcome, r.OwnershipRevision)
	return err
}
