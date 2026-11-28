package operations

import (
	"context"
	"database/sql"
	"errors"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const PowerObservationMigration = "0155_power_observation_authority"

const powerObservationRestarted = "power_observation_restarted"

func reviewedPowerObservation(op Operation) bool {
	return op.Status == "observing" && op.ResourceKind == provider.Instance && instancePowerAction(op.Action) &&
		(op.OutcomeReason == observationQueueRestarted || op.OutcomeReason == powerObservationRestarted)
}

func supportsPowerObservation(op Operation, providerName string) bool {
	if op.ResourceKind != provider.Instance || !instancePowerAction(op.Action) {
		return false
	}
	if op.Action != "reboot" {
		return true
	}
	return requestIDPattern.MatchString(op.ProviderRequestID) &&
		(providerName == provider.TencentCVM || providerName == provider.HuaweiECS || providerName == "openstack")
}

func powerDispatchRecorded(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var dispatched bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM cmp_instance_operation_events
		WHERE operation_id=$1 AND status='applying')`, id).Scan(&dispatched)
	return dispatched, err
}

func canRetryPowerObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, op Operation) (bool, error) {
	if !instancePowerAction(op.Action) || (op.Status != "needs_review" && op.QueueIssueReason == "") {
		return false, nil
	}
	var providerName string
	err := tx.QueryRowContext(ctx, `SELECT provider FROM cmp_cloud_accounts WHERE (tenant_id,account_id)=($1,$2)`,
		actor.Tenant, op.Account).Scan(&providerName)
	if err != nil || !supportsPowerObservation(op, providerName) {
		return false, err
	}
	return powerDispatchRecorded(ctx, tx, op.ID)
}

func authorizePowerObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, op Operation, ownership, credential int64) (identity.AccountAccess, error) {
	target, err := inventory.ResolveInstancePowerObservation(ctx, tx, actor, op.Account, op.Region, op.AssetID)
	if err != nil {
		return identity.AccountAccess{}, err
	}
	if !supportsPowerObservation(op, target.Provider) {
		return identity.AccountAccess{}, ErrConflict
	}
	if err := checkPowerObservationOwnership(ctx, tx, actor, op.Account, target, ownership, credential); err != nil {
		return identity.AccountAccess{}, err
	}
	var locked string
	err = tx.QueryRowContext(ctx, `SELECT m.principal_id FROM cmp_memberships AS m JOIN cmp_principals AS p USING (tenant_id,principal_id)
		WHERE (m.tenant_id,m.project_id,m.principal_id)=($1,$2,$3) AND m.active AND p.active FOR SHARE OF m,p`,
		actor.Tenant, actor.Project, actor.Principal).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.AccountAccess{}, identity.ErrForbidden
	}
	if err != nil {
		return identity.AccountAccess{}, err
	}
	return authorizeLaunchObservation(ctx, tx, actor, op.Account)
}

func checkPowerObservationOwnership(ctx context.Context, tx *sql.Tx, actor identity.Scope, account string,
	target inventory.MutationTarget, ownership, credential int64) error {
	delta := target.CredentialRevision - credential
	revision := target.OwnershipRevision
	if target.OwnershipSource != "manual" {
		// 继承或标签归属的版本包含账号代次，仅剔除有审计证明的写入开关变更。
		revision -= delta
	}
	if revision != ownership || delta < 0 {
		return identity.ErrAccountStale
	}
	if delta == 0 {
		return nil
	}
	var modeChangesOnly bool
	err := tx.QueryRowContext(ctx, `SELECT count(*)=$5 AND COALESCE(bool_and(action IN ('disable-write','enable-write') AND project_id=$6),false)
		FROM cmp_cloud_account_events WHERE (tenant_id,account_id)=($1,$2) AND revision>$3 AND revision<=$4`,
		actor.Tenant, account, credential, target.CredentialRevision, delta, actor.Project).Scan(&modeChangesOnly)
	if err == nil && !modeChangesOnly {
		return identity.ErrAccountStale
	}
	return err
}

func bindPowerObservation(ctx context.Context, tx *sql.Tx, id, reviewer, evidence string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_power_observation_authority (operation_id,reviewer_id,evidence_ref)
		VALUES ($1,$2,$3) ON CONFLICT (operation_id) DO UPDATE SET (reviewer_id,evidence_ref)=(EXCLUDED.reviewer_id,EXCLUDED.evidence_ref)`,
		id, reviewer, evidence)
	return err
}

func checkPowerObservation(ctx context.Context, tx *sql.Tx, w *work) error {
	actor := w.actor
	var membership, credential int64
	err := tx.QueryRowContext(ctx, `SELECT a.reviewer_id,r.membership_revision,r.credential_revision
		FROM cmp_power_observation_authority AS a JOIN cmp_instance_observation_retries AS r USING (operation_id,reviewer_id,evidence_ref)
		WHERE a.operation_id=$1 AND r.tenant_id=$2`, w.ID, actor.Tenant).Scan(&actor.Principal, &membership, &credential)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrForbidden
	}
	if err != nil {
		return err
	}
	access, err := authorizePowerObservation(ctx, tx, actor, w.Operation, w.ownership, w.credential)
	if err == nil && (access.MembershipRevision != membership || access.CredentialRevision != credential) {
		return identity.ErrAccountStale
	}
	return err
}

func (s Store) settleRebootObservation(ctx context.Context, task mq.Task, w work, status, reason string) error {
	if !reviewedPowerObservation(w.Operation) {
		return s.transition(ctx, task, w.Status, status, reason, "")
	}
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := checkPowerObservation(ctx, tx, &w); err != nil {
			return err
		}
		return writeTransition(ctx, tx, task.ID, w.Status, status, reason, "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
