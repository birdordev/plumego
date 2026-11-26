package operations

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

type DeletionPreview struct {
	AssetID                 string `json:"asset_id"`
	OwnershipRevision       int64  `json:"ownership_revision"`
	Protected               bool   `json:"protected"`
	KnownVolumeLinks        int    `json:"known_volume_links"`
	ActiveImageCreation     bool   `json:"active_image_creation,omitempty"`
	ActiveBackendAttachment bool   `json:"active_backend_attachment,omitempty"`
	ActiveElasticIPBinding  bool   `json:"active_elastic_ip_binding,omitempty"`
	ApprovalRequired        bool   `json:"approval_required"`
	ProviderSupported       bool   `json:"provider_supported"`
}

const deletionProtection = `SELECT EXISTS(SELECT 1 FROM cmp_protected_instances WHERE scope_id=$1 AND external_id=$2)`

const activeElasticIPBinding = `EXISTS(SELECT 1 FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
	WHERE (i.parent_scope_id,i.parent_id)=($1,$2) AND i.binding_address IS NOT NULL AND o.status IN ` + activeOperationStatuses + `)`

func checkInstanceMutationProtection(ctx context.Context, tx *sql.Tx, scopeID int64, id, action string) error {
	active, err := inventory.InstanceImageCreationActive(ctx, tx, scopeID, id, "")
	if err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	if action != "delete" {
		return nil
	}
	var blocked bool
	if err := tx.QueryRowContext(ctx, deletionProtection+" OR "+activeElasticIPBinding, scopeID, id).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return ErrConflict
	}
	active, err = inventory.InstanceBackendAttachmentActive(ctx, tx, scopeID, id)
	if err != nil {
		return err
	}
	if active {
		return ErrConflict
	}
	return nil
}

func (s Store) PreviewDeletion(ctx context.Context, token string, scope identity.Scope, input Input) (DeletionPreview, error) {
	if !operationKey.MatchString(input.Account) || !operationKey.MatchString(input.Region) || !operationKey.MatchString(input.AssetID) {
		return DeletionPreview{}, ErrInvalid
	}
	var result DeletionPreview
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		target, err := inventory.ResolveInstanceMutation(ctx, tx, actor, input.Account, input.Region, input.AssetID)
		if err != nil {
			return err
		}
		result = DeletionPreview{
			AssetID: input.AssetID, OwnershipRevision: target.OwnershipRevision, ApprovalRequired: true,
			ProviderSupported: supportsInstanceAction(target.Provider, "delete"),
		}
		if err := tx.QueryRowContext(ctx, deletionProtection+", "+activeElasticIPBinding, target.ScopeID, input.AssetID).
			Scan(&result.Protected, &result.ActiveElasticIPBinding); err != nil {
			return err
		}
		result.ActiveImageCreation, err = inventory.InstanceImageCreationActive(ctx, tx, target.ScopeID, input.AssetID, "")
		if err != nil {
			return err
		}
		result.ActiveBackendAttachment, err = inventory.InstanceBackendAttachmentActive(ctx, tx, target.ScopeID, input.AssetID)
		if err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT count(DISTINCT link->>'id')
			FROM cmp_inventory_objects AS o CROSS JOIN LATERAL jsonb_array_elements(o.links) AS link
			WHERE o.scope_id=$1 AND o.external_id=$2 AND link->>'kind'='volume'`, target.ScopeID, input.AssetID).Scan(&result.KnownVolumeLinks)
	})
	return result, err
}

func (s Store) confirmDeletion(ctx context.Context, task mq.Task, w work, reason string) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := writeTransition(ctx, tx, task.ID, w.Status, "succeeded", reason, ""); err != nil {
			return err
		}
		target, err := inventory.ResolveInstanceObservation(ctx, tx, w.actor, w.Account, w.Region, w.AssetID)
		if err != nil {
			return err
		}
		if target.OwnershipRevision != w.ownership || target.CredentialRevision != w.credential || target.MembershipRevision != w.membership {
			return identity.ErrForbidden
		}
		if err := inventory.MarkResourceDeleted(ctx, tx, target.ScopeID, w.AssetID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE cmp_instance_launches AS l SET released_at=statement_timestamp()
			FROM cmp_instance_operations AS o WHERE o.operation_id=l.operation_id AND o.action='create'
			AND (o.scope_id,o.external_id)=($1,$2) AND l.released_at IS NULL`, target.ScopeID, w.AssetID)
		return err
	})
	if errors.Is(err, identity.ErrForbidden) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
