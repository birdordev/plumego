package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0107_instance_retirement.sql
var instanceRetirementSchema string

const retireInstance = "retire-instance"

const retirementDiskReserved = `EXISTS(SELECT 1 FROM cmp_instance_retirement_reservations WHERE (disk_scope,disk_id)=($1,$2))`

type InstanceRetirementInput struct {
	Account      string `json:"account"`
	Region       string `json:"region"`
	AssetID      string `json:"asset_id"`
	SystemDiskID string `json:"system_disk_id"`
	ConfirmID    string `json:"confirm_asset_id"`
}

type InstanceRetirementPreview struct {
	SystemDiskID      string `json:"system_disk_id"`
	OwnershipRevision int64  `json:"ownership_revision"`
}

type InstanceRetirementView struct {
	SystemDiskID          string `json:"system_disk_id"`
	DiskOwnershipRevision int64  `json:"disk_ownership_revision"`
}

func (s Store) PreviewInstanceRetirement(ctx context.Context, token string, scope identity.Scope, account, region, id string) (InstanceRetirementPreview, error) {
	if !operationKey.MatchString(account) || !operationKey.MatchString(region) || !operationKey.MatchString(id) {
		return InstanceRetirementPreview{}, ErrInvalid
	}
	var result InstanceRetirementPreview
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		instance, err := inventory.ResolveInstanceMutation(ctx, tx, actor, account, region, id)
		if err != nil {
			return err
		}
		systemDisk, err := retirementDiskID(ctx, tx, instance, id)
		if err != nil {
			return err
		}
		if _, err := resolveRetirementDisk(ctx, tx, actor, account, region, systemDisk); err != nil {
			return err
		}
		result = InstanceRetirementPreview{SystemDiskID: systemDisk, OwnershipRevision: instance.OwnershipRevision}
		return nil
	})
	return result, err
}

func (s Store) SubmitInstanceRetirement(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input InstanceRetirementInput) (Operation, error) {
	target := provider.InstanceRetirement{InstanceID: input.AssetID, SystemDiskID: input.SystemDiskID}
	if input.ConfirmID != input.AssetID || !target.ValidFor(provider.TencentCVM) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		InstanceRetirementInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(retireInstance+"\x00"), data...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.Instance, input.Account, input.Region, input.AssetID,
		retireInstance, "", digest, func(ctx context.Context, tx *sql.Tx, actor identity.Scope, instance inventory.MutationTarget) (func(string) error, error) {
			id, err := retirementDiskID(ctx, tx, instance, input.AssetID)
			if err != nil {
				return nil, err
			}
			if id != input.SystemDiskID {
				return nil, identity.ErrAccountStale
			}
			disk, err := resolveRetirementDisk(ctx, tx, actor, input.Account, input.Region, id)
			if err != nil {
				return nil, err
			}
			return func(operation string) error {
				reserved, err := inventory.ReserveInstanceRetirement(ctx, tx, actor, operation, instance, disk, input.AssetID, id)
				if err != nil {
					return err
				}
				if !reserved {
					return ErrConflict
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO cmp_instance_retirement_intents(operation_id,system_disk_id,disk_ownership_revision)
					VALUES ($1,$2,$3)`, operation, id, disk.OwnershipRevision)
				return err
			}, nil
		})
}

func retirementDiskID(ctx context.Context, tx *sql.Tx, target inventory.MutationTarget, id string) (string, error) {
	if target.Provider != provider.TencentCVM {
		return "", ErrInvalid
	}
	if err := checkInstanceMutationProtection(ctx, tx, target.ScopeID, id, retireInstance); err != nil {
		return "", err
	}
	var status, disk string
	var disks int
	err := tx.QueryRowContext(ctx, `SELECT o.status, count(DISTINCT link->>'id'), COALESCE(min(link->>'id'),'')
		FROM cmp_inventory_objects AS o LEFT JOIN LATERAL jsonb_array_elements(o.links) AS link ON link->>'kind'='volume'
		WHERE (o.scope_id,o.external_id)=($1,$2) GROUP BY o.status`, target.ScopeID, id).Scan(&status, &disks, &disk)
	if errors.Is(err, sql.ErrNoRows) {
		return "", identity.ErrAccountStale
	}
	if err != nil {
		return "", err
	}
	if disks != 1 || !strings.EqualFold(status, "stopped") ||
		!(provider.InstanceRetirement{InstanceID: id, SystemDiskID: disk}).ValidFor(target.Provider) {
		return "", ErrConflict
	}
	return disk, nil
}

func resolveRetirementDisk(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, id string) (inventory.MutationTarget, error) {
	disk, err := inventory.ResolveVolumeMutation(ctx, tx, actor, account, region, id)
	if err != nil {
		return disk, err
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM cmp_instance_operations WHERE status IN `+activeOperationStatuses+`
		  AND (volume_scope_id,volume_id)=($1,$2)) OR `+snapshotWriteConflict, disk.ScopeID, id, "").Scan(&active)
	if err == nil && active {
		return disk, ErrConflict
	}
	return disk, err
}
