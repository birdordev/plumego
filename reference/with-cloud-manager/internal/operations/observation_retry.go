package operations

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

type ObservationRetry struct {
	EvidenceRef string `json:"evidence_ref"`
}

func (s Store) RetryCreationObservation(ctx context.Context, token string, scope identity.Scope, id string, request ObservationRetry) (Operation, error) {
	if !operationKey.MatchString(id) || !evidenceReference.MatchString(request.EvidenceRef) {
		return Operation{}, ErrInvalid
	}
	var op Operation
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Approve)
		if err != nil {
			return err
		}
		var queueStatus string
		err = tx.QueryRowContext(ctx, `SELECT t.status FROM cmp_operation_tasks AS t
			JOIN cmp_instance_operations AS o ON (t.tenant_id,t.id)=(o.tenant_id,o.operation_id)
			WHERE (o.tenant_id,o.project_id,o.operation_id)=($1,$2,$3)
			AND o.action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','revoke-security-group-rule') FOR UPDATE OF t`,
			actor.Tenant, actor.Project, id).Scan(&queueStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		var requester string
		err = scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+",principal_id"+
			" FROM cmp_instance_operations WHERE operation_id=$1 FOR UPDATE", id),
			&op, &requester)
		if err != nil {
			return err
		}
		if requester == actor.Principal {
			return identity.ErrForbidden
		}
		var replay bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_instance_observation_retries
			WHERE (operation_id,reviewer_id,evidence_ref)=($1,$2,$3))`, id, actor.Principal, request.EvidenceRef).Scan(&replay); err != nil {
			return err
		}
		if replay {
			return nil
		}
		if op.Status != "needs_review" || queueStatus != "done" {
			return ErrConflict
		}
		var access identity.AccountAccess
		if op.Action == revokeSecurityRule {
			access, err = identity.NewStore(tx).AuthorizeAccount(ctx, actor, op.Account, identity.View)
		} else {
			access, err = authorizeLaunchObservation(ctx, tx, actor, op.Account)
		}
		if err != nil {
			return err
		}
		query := `UPDATE cmp_instance_launches SET observation_reviewer=$2,
			observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		if isNetworkCreation(op.Action) || op.Action == revokeSecurityRule {
			query = `UPDATE cmp_network_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4,absence_observations=0 WHERE operation_id=$1`
		}
		if _, err := tx.ExecContext(ctx, query, id, actor.Principal,
			access.MembershipRevision, access.CredentialRevision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_observation_retries
			(operation_id,tenant_id,reviewer_id,evidence_ref,membership_revision,credential_revision)
			VALUES ($1,$2,$3,$4,$5,$6)`, id, actor.Tenant, actor.Principal, request.EvidenceRef, access.MembershipRevision, access.CredentialRevision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_operation_tasks SET status='queued',attempts=0,available_at=statement_timestamp(),
			updated_at=statement_timestamp(),lease_owner=NULL,lease_until=NULL WHERE id=$1`, id); err != nil {
			return err
		}
		if err := writeTransition(ctx, tx, id, "needs_review", "observing", "observation_restarted", ""); err != nil {
			return err
		}
		return scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+" FROM cmp_instance_operations WHERE operation_id=$1", id), &op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}
