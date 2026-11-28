package operations

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func checkReclaimAuthorities(ctx context.Context, tx *sql.Tx, actor identity.Scope, r reclaimRecord) error {
	requester, err := lockProjectRole(ctx, tx, actor, r.Requester, "admin")
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
	reviewer, err := lockProjectRole(ctx, tx, actor, r.Reviewer, "approver")
	if err == nil && reviewer != r.reviewerRevision {
		return identity.ErrAccountStale
	}
	return err
}
