package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
)

//go:embed 0013_operation_approval.sql
var approvalSchema string

type Approval struct {
	Decision string `json:"decision"`
}

func migrateApproval(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0013_operation_approval", approvalSchema)
}

func (s Store) Approve(ctx context.Context, token string, scope identity.Scope, id string, input Approval) (Operation, error) {
	if !operationKey.MatchString(id) || (input.Decision != "approve" && input.Decision != "reject") {
		return Operation{}, ErrInvalid
	}
	var op Operation
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		reviewer, err := operationActor(ctx, tx, token, scope, identity.Approve)
		if err != nil {
			return err
		}
		var original work
		err = scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+
			",principal_id,ownership_revision,credential_revision,membership_revision,volume_revision FROM cmp_instance_operations WHERE tenant_id=$1 AND project_id=$2 AND operation_id=$3 FOR UPDATE",
			reviewer.Tenant, reviewer.Project, id), &op, &original.actor.Principal, &original.ownership, &original.credential, &original.membership, &original.volumeRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		if reviewer.Principal == original.actor.Principal {
			return identity.ErrForbidden
		}
		if op.Status != "awaiting_approval" {
			var recorded, decision string
			err := tx.QueryRowContext(ctx, "SELECT reviewer_id,decision FROM cmp_instance_operation_approvals WHERE operation_id=$1", id).Scan(&recorded, &decision)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			if recorded != reviewer.Principal || decision != input.Decision {
				return ErrConflict
			}
			return nil
		}
		state, reason := "failed", "approval_rejected"
		var reviewerRevision int64
		if input.Decision == "approve" {
			if err := requireChangeTicket(ctx, tx, reviewer, op); err != nil {
				return err
			}
			original.actor.Tenant, original.actor.Project = reviewer.Tenant, reviewer.Project
			original.Operation = op
			if err := checkOperationTarget(ctx, tx, &original); err != nil {
				return err
			}
			member, err := identity.NewStore(tx).AuthorizeProject(ctx, reviewer, identity.Approve)
			if err != nil {
				return err
			}
			reviewerRevision = member.Revision
			state, reason = "queued", "approval_granted"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_operation_approvals
			(operation_id,tenant_id,reviewer_id,decision,reviewer_membership_revision)
			VALUES ($1,$2,$3,$4,NULLIF($5::bigint,0))`, id, reviewer.Tenant, reviewer.Principal, input.Decision, reviewerRevision); err != nil {
			return err
		}
		if err := writeTransition(ctx, tx, id, "awaiting_approval", state, reason, ""); err != nil {
			return err
		}
		if err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+" FROM cmp_instance_operations WHERE operation_id=$1", id), &op); err != nil {
			return err
		}
		if state == "queued" {
			return s.enqueue(ctx, tx, reviewer.Tenant, op)
		}
		return nil
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func checkDispatchApproval(ctx context.Context, tx *sql.Tx, w *work, mandatory bool) error {
	reviewer := w.actor
	var revision sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT reviewer_id,reviewer_membership_revision FROM cmp_instance_operation_approvals
		WHERE operation_id=$1 AND tenant_id=$2 AND decision='approve'`,
		w.ID, reviewer.Tenant).Scan(&reviewer.Principal, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		if mandatory {
			return identity.ErrForbidden
		}
		status, err := initialStatus(ctx, tx, w.actor, w.Action)
		if err == nil && status == "awaiting_approval" {
			return identity.ErrForbidden
		}
		return err
	}
	if err != nil {
		return err
	}
	if !revision.Valid || reviewer.Principal == w.actor.Principal {
		return identity.ErrForbidden
	}
	member, err := identity.NewStore(tx).AuthorizeProject(ctx, reviewer, identity.Approve)
	if err == nil && member.Revision != revision.Int64 {
		return identity.ErrAccountStale
	}
	return err
}
