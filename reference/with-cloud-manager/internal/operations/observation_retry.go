package operations

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type ObservationRetry struct {
	EvidenceRef        string `json:"evidence_ref"`
	ProviderResourceID string `json:"provider_resource_id,omitempty"`
	RepairQueue        bool   `json:"repair_queue,omitempty"`
}

func (s Store) RetryCreationObservation(ctx context.Context, token string, scope identity.Scope, id string, request ObservationRetry) (Operation, error) {
	if !operationKey.MatchString(id) || !evidenceReference.MatchString(request.EvidenceRef) ||
		(request.ProviderResourceID != "" && !provider.ValidDNSRecordID(request.ProviderResourceID)) {
		return Operation{}, ErrInvalid
	}
	var op Operation
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Approve)
		if err != nil {
			return err
		}
		queueStatus, err := lockObservationTask(ctx, tx, actor, id)
		if err != nil {
			return err
		}
		var requester string
		var ownership, credential int64
		err = scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+",principal_id,ownership_revision,credential_revision"+
			" FROM cmp_instance_operations WHERE (operation_id,tenant_id,project_id)=($1,$2,$3) FOR UPDATE", id, actor.Tenant, actor.Project),
			&op, &requester, &ownership, &credential)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		if requester == actor.Principal {
			return identity.ErrForbidden
		}
		if request.ProviderResourceID != "" && op.Action != createDNSRecord {
			return ErrInvalid
		}
		var priorResourceID string
		var priorRepair bool
		err = tx.QueryRowContext(ctx, `SELECT provider_resource_id,repair_queue FROM cmp_instance_observation_retries
			WHERE (operation_id,reviewer_id,evidence_ref)=($1,$2,$3)`, id, actor.Principal, request.EvidenceRef).Scan(&priorResourceID, &priorRepair)
		if err == nil {
			if priorResourceID != request.ProviderResourceID || priorRepair != request.RepairQueue {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if !supportsObservationRetry(op.Action) || (queueStatus == "" && !request.RepairQueue) {
			return identity.ErrForbidden
		}
		if op.Action == retireInstance && !requestIDPattern.MatchString(op.ProviderRequestID) {
			return ErrConflict
		}
		if request.RepairQueue {
			issue, err := observationQueueIssue(ctx, tx, actor, op)
			if err != nil {
				return err
			}
			if issue == "" {
				return ErrConflict
			}
		} else if op.Status != "needs_review" || queueStatus != "done" {
			return ErrConflict
		}
		var access identity.AccountAccess
		power := instancePowerAction(op.Action)
		if power {
			dispatched, dispatchErr := powerDispatchRecorded(ctx, tx, id)
			if dispatchErr != nil {
				return dispatchErr
			}
			if !dispatched {
				return ErrConflict
			}
			access, err = authorizePowerObservation(ctx, tx, actor, op, ownership, credential)
		} else if op.ResourceKind == provider.BareMetal || op.Action == retireInstance || op.Action == revokeSecurityRule || op.Action == deleteImage || isNetworkDeletion(op.Action) || op.Action == deleteSnapshot || op.Action == rollbackSnapshot || op.Action == releaseElasticIP || isElasticIPBinding(op.Action) || isLoadBalancerMutation(op.Action) || isDNSRecordMutation(op.Action) || op.Action == scaleWorkload || op.Action == renameDatabaseInstance || op.Action == renameCacheInstance || op.Action == resizeVolume || op.Action == releaseVolume {
			access, err = identity.NewStore(tx).AuthorizeAccount(ctx, actor, op.Account, identity.View)
		} else {
			access, err = authorizeLaunchObservation(ctx, tx, actor, op.Account)
		}
		if err != nil {
			return err
		}
		if op.Action == rollbackSnapshot && (access.Provider != provider.HuaweiECS || op.ProviderRequestID == "") {
			return ErrConflict
		}
		if op.Action == copyImage {
			source, resourceID, err := imageCopyReviewTarget(ctx, tx, actor, op.ID)
			if err != nil {
				return err
			}
			if source.Provider == provider.TencentCVM && resourceID == "" {
				return ErrConflict
			}
			if _, err := tx.ExecContext(ctx, `UPDATE cmp_image_copy_intents SET observation_credential_revision=$2
				WHERE operation_id=$1`, op.ID, source.CredentialRevision); err != nil {
				return err
			}
		}
		query := `UPDATE cmp_instance_launches SET observation_reviewer=$2,
			observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		if isResourceCreation(op.Action) || op.Action == releaseVolume || op.Action == revokeSecurityRule || op.Action == deleteImage || isNetworkDeletion(op.Action) || op.Action == deleteSnapshot || op.Action == rollbackSnapshot || op.Action == releaseElasticIP || isElasticIPBinding(op.Action) {
			query = `UPDATE cmp_network_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4,absence_observations=0,binding_observations=0 WHERE operation_id=$1`
		}
		if op.Action == retireInstance {
			query = `UPDATE cmp_instance_retirement_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4,absence_observations=0,last_absence_attempt=0 WHERE operation_id=$1`
		}
		if op.Action == resizeVolume {
			query = `UPDATE cmp_volume_resize_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.Action == renameDatabaseInstance {
			query = `UPDATE cmp_database_rename_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.Action == renameCacheInstance {
			query = `UPDATE cmp_cache_rename_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if isLoadBalancerRename(op.Action) {
			query = `UPDATE cmp_load_balancer_rename_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.Action == setLoadBalancerBackendWeight {
			query = `UPDATE cmp_load_balancer_backend_weight_intents SET observation_reviewer=$2,
      observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.Action == detachLoadBalancerBackend {
			query = `UPDATE cmp_load_balancer_backend_detach_intents SET observation_reviewer=$2,
    observation_membership_revision=$3,observation_credential_revision=$4,absence_observations=0 WHERE operation_id=$1`
		}
		if op.Action == attachLoadBalancerBackend {
			query = `UPDATE cmp_load_balancer_backend_attach_intents SET observation_reviewer=$2,
    observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.Action == deleteDNSRecord {
			query = `UPDATE cmp_dns_record_deletion_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4,absence_observations=0,last_absence_attempt=0 WHERE operation_id=$1`
		}
		if op.Action == setDNSRecordStatus {
			query = `UPDATE cmp_dns_record_status_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.Action == scaleWorkload {
			query = `UPDATE cmp_workload_scale_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.Action == createDNSRecord {
			var evidenceID string
			if err := tx.QueryRowContext(ctx, `SELECT record_id FROM cmp_dns_record_creation_intents
				WHERE operation_id=$1 FOR UPDATE`, id).Scan(&evidenceID); err != nil {
				return err
			}
			if request.ProviderResourceID != "" && (access.Provider != provider.AlibabaECS ||
				(evidenceID != "" && evidenceID != request.ProviderResourceID)) {
				return ErrConflict
			}
			if access.Provider == provider.AlibabaECS && evidenceID == "" {
				if request.ProviderResourceID == "" {
					return ErrConflict
				}
				if _, err := tx.ExecContext(ctx, `UPDATE cmp_dns_record_creation_intents SET reviewed_record_id=$2
					WHERE operation_id=$1`, id, request.ProviderResourceID); err != nil {
					return err
				}
			}
			query = `UPDATE cmp_dns_record_creation_intents SET observation_reviewer=$2,
				observation_membership_revision=$3,observation_credential_revision=$4 WHERE operation_id=$1`
		}
		if op.ResourceKind == provider.BareMetal {
			if _, err := inventory.ResolveBareMetalObservation(ctx, tx, actor, op.Account, op.Region, op.AssetID); err != nil {
				return err
			}
		} else if !power {
			if _, err := tx.ExecContext(ctx, query, id, actor.Principal,
				access.MembershipRevision, access.CredentialRevision); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_observation_retries
			(operation_id,tenant_id,reviewer_id,evidence_ref,membership_revision,credential_revision,provider_resource_id,repair_queue)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, actor.Tenant, actor.Principal, request.EvidenceRef,
			access.MembershipRevision, access.CredentialRevision, request.ProviderResourceID, request.RepairQueue); err != nil {
			return err
		}
		reason := "observation_restarted"
		if power {
			if err := bindPowerObservation(ctx, tx, id, actor.Principal, request.EvidenceRef); err != nil {
				return err
			}
			reason = powerObservationRestarted
		}
		if request.RepairQueue || power {
			if err := s.repairObservationTask(ctx, tx, actor, op, queueStatus == ""); err != nil {
				return err
			}
			if request.RepairQueue {
				reason = observationQueueRestarted
			}
		} else if _, err := tx.ExecContext(ctx, `UPDATE cmp_operation_tasks SET status='queued',attempts=0,available_at=statement_timestamp(),
			updated_at=statement_timestamp(),lease_owner=NULL,lease_until=NULL WHERE id=$1`, id); err != nil {
			return err
		}
		if err := writeTransition(ctx, tx, id, op.Status, "observing", reason, ""); err != nil {
			return err
		}
		return scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+" FROM cmp_instance_operations WHERE operation_id=$1", id), &op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}
