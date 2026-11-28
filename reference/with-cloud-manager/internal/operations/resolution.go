package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"regexp"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0012_operation_resolution.sql
var resolutionSchema string

var evidenceReference = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,199}$`)

type Resolution struct {
	Decision          string `json:"decision"`
	EvidenceRef       string `json:"evidence_ref"`
	ProviderRequestID string `json:"provider_request_id,omitempty"`
}

func migrateResolution(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0012_operation_resolution", resolutionSchema)
}

func (s Store) Resolve(ctx context.Context, token string, scope identity.Scope, id string, input Resolution) (Operation, error) {
	if !operationKey.MatchString(id) || !evidenceReference.MatchString(input.EvidenceRef) ||
		(input.Decision != "succeeded" && input.Decision != "failed") ||
		(input.ProviderRequestID != "" && !requestIDPattern.MatchString(input.ProviderRequestID)) {
		return Operation{}, ErrInvalid
	}
	var op Operation
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Approve)
		if err != nil {
			return err
		}
		var submitter string
		err = scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+
			",principal_id FROM cmp_instance_operations WHERE tenant_id=$1 AND project_id=$2 AND operation_id=$3 FOR UPDATE",
			actor.Tenant, actor.Project, id), &op, &submitter)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		if submitter == actor.Principal {
			return identity.ErrForbidden
		}
		if op.Status != "needs_review" {
			return replayResolution(ctx, tx, id, actor.Principal, input)
		}
		if op.ProviderRequestID != "" && input.ProviderRequestID != op.ProviderRequestID {
			return ErrConflict
		}
		if op.ResourceKind == provider.BareMetal || op.Action == retireInstance || op.Action == rollbackSnapshot || op.Action == deleteImage || isNetworkDeletion(op.Action) || isLoadBalancerMutation(op.Action) || isDNSRecordMutation(op.Action) || op.Action == scaleWorkload || op.Action == renameDatabaseInstance || op.Action == renameCacheInstance || op.Action == resizeVolume || op.Action == resizeInstance || isBucketSettingMutation(op.Action) || op.Action == releaseVolume {
			if _, err := identity.NewStore(tx).AuthorizeAccount(ctx, actor, op.Account, identity.View); err != nil {
				return err
			}
		}
		if op.Action == copyImage {
			if _, err := authorizeLaunchObservation(ctx, tx, actor, op.Account); err != nil {
				return err
			}
			if _, _, err := imageCopyReviewTarget(ctx, tx, actor, op.ID); err != nil {
				return err
			}
		}
		if (op.ResourceKind == provider.BareMetal || op.Action == "create" || isResourceCreation(op.Action) || op.Action == revokeSecurityRule || op.Action == deleteImage || isNetworkDeletion(op.Action) || op.Action == deleteSnapshot || op.Action == releaseElasticIP || isElasticIPBinding(op.Action) || isLoadBalancerMutation(op.Action) || isDNSRecordMutation(op.Action) || op.Action == scaleWorkload || op.Action == renameDatabaseInstance || op.Action == renameCacheInstance || op.Action == resizeVolume || op.Action == resizeInstance || isBucketSettingMutation(op.Action) || op.Action == releaseVolume) &&
			input.Decision == "succeeded" {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_operation_resolutions
			(operation_id,tenant_id,reviewer_id,decision,evidence_ref,request_id) VALUES ($1,$2,$3,$4,$5,$6)`,
			id, actor.Tenant, actor.Principal, input.Decision, input.EvidenceRef, input.ProviderRequestID); err != nil {
			return err
		}
		if err := writeTransition(ctx, tx, id, "needs_review", input.Decision, "manual_review", input.ProviderRequestID); err != nil {
			return err
		}
		return scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+
			" FROM cmp_instance_operations WHERE operation_id=$1", id), &op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func replayResolution(ctx context.Context, tx *sql.Tx, id, actor string, input Resolution) error {
	var prior Resolution
	var reviewer string
	err := tx.QueryRowContext(ctx, `SELECT reviewer_id,decision,evidence_ref,request_id FROM cmp_instance_operation_resolutions
		WHERE operation_id=$1`, id).Scan(&reviewer, &prior.Decision, &prior.EvidenceRef, &prior.ProviderRequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if reviewer != actor || prior != input {
		return ErrConflict
	}
	return nil
}
