package operations

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

func lockReclaimMember(ctx context.Context, tx *sql.Tx, actor identity.Scope, id, role string) (int64, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT m.revision FROM cmp_memberships AS m
		JOIN cmp_principals AS p USING (tenant_id,principal_id)
		WHERE (m.tenant_id,m.project_id,m.principal_id)=($1,$2,$3)
		  AND m.active AND p.active AND m.role=$4 FOR SHARE OF m,p`,
		actor.Tenant, actor.Project, id, role).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = identity.ErrForbidden
	}
	return revision, err
}

func checkReclaimAuthorities(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) error {
	requester, err := lockReclaimMember(ctx, tx, actor, r.Requester, "admin")
	if err != nil {
		return err
	}
	sanitizer, generation, err := lockTicketVerifier(ctx, tx, actor, r.Sanitizer)
	if err != nil {
		return err
	}
	if requester != r.requesterRevision || sanitizer != r.sanitizerRevision || generation != r.sanitizerGeneration {
		return identity.ErrAccountStale
	}
	if r.Reviewer == "" {
		return nil
	}
	reviewer, err := lockReclaimMember(ctx, tx, actor, r.Reviewer, "approver")
	if err == nil && reviewer != r.reviewerRevision {
		return identity.ErrAccountStale
	}
	return err
}
