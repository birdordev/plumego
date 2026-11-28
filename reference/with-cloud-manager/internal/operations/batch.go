package operations

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const MaxBatchItems = 25

type BatchItem struct {
	Input
	Revision int64 `json:"ownership_revision"`
}

type BatchInput struct {
	Items []BatchItem `json:"items"`
}

type BatchPreviewItem struct {
	Index             int    `json:"index"`
	Reason            string `json:"reason"`
	OwnershipRevision int64  `json:"ownership_revision,omitempty"`
	VolumeRevision    int64  `json:"volume_revision,omitempty"`
	ApprovalRequired  bool   `json:"approval_required"`
	TicketRequired    bool   `json:"ticket_required,omitempty"`
}

type BatchPreview struct {
	Items     []BatchPreviewItem `json:"items"`
	Remaining int                `json:"remaining"`
	CanSubmit bool               `json:"can_submit"`
}

type BatchReceipt struct {
	ID         string      `json:"id"`
	Operations []Operation `json:"operations"`
}

type BatchItemError struct {
	Index int
	Cause error
}

func (e BatchItemError) Error() string { return e.Cause.Error() }
func (e BatchItemError) Unwrap() error { return e.Cause }

func batchCoordinate(input Input, id string) string {
	return input.Account + "\x00" + input.Region + "\x00" + id
}

func validateBatch(input BatchInput) error {
	if len(input.Items) == 0 || len(input.Items) > MaxBatchItems {
		return ErrInvalid
	}
	targets := make(map[string]bool, len(input.Items))
	volumes := make(map[string]bool, len(input.Items))
	for index, item := range input.Items {
		target := batchCoordinate(item.Input, item.AssetID)
		volume := batchCoordinate(item.Input, item.VolumeID)
		if !validOperationInput(item.Input, item.Revision) || targets[target] || (item.VolumeID != "" && volumes[volume]) {
			return BatchItemError{Index: index, Cause: ErrInvalid}
		}
		targets[target] = true
		if item.VolumeID != "" {
			volumes[volume] = true
		}
	}
	return nil
}

func (s Store) PreviewBatch(ctx context.Context, token string, scope identity.Scope, input BatchInput) (BatchPreview, error) {
	if err := validateBatch(input); err != nil {
		return BatchPreview{}, err
	}
	result := BatchPreview{Items: make([]BatchPreviewItem, len(input.Items))}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		result.Remaining, err = remainingBudget(ctx, tx, actor)
		if err != nil {
			return err
		}
		ticketActions, err := currentTicketActions(ctx, tx, actor)
		if err != nil {
			return err
		}
		ticketAll := slices.Contains(ticketActions, "*")
		result.CanSubmit = result.Remaining >= len(input.Items)
		for index, item := range input.Items {
			ticketRequired := ticketAll || slices.Contains(ticketActions, item.Action)
			preview, err := previewBatchItem(ctx, tx, actor, item, ticketRequired)
			if err != nil {
				return err
			}
			preview.Index = index
			result.Items[index] = preview
			result.CanSubmit = result.CanSubmit && preview.Reason == "ready"
		}
		return nil
	})
	if err != nil {
		return BatchPreview{}, err
	}
	return result, nil
}

func previewBatchItem(ctx context.Context, tx *sql.Tx, actor identity.Scope, item BatchItem, ticketRequired bool) (BatchPreviewItem, error) {
	var result BatchPreviewItem
	target, err := inventory.ResolveMutationPreview(ctx, tx, actor, item.Account, item.Region, item.AssetID, provider.Instance)
	if errors.Is(err, identity.ErrForbidden) {
		result.Reason = "unavailable"
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.OwnershipRevision = target.OwnershipRevision
	status, err := initialStatus(ctx, tx, actor, item.Action)
	if err != nil {
		return result, err
	}
	result.TicketRequired = ticketRequired
	result.ApprovalRequired = status == "awaiting_approval" || result.TicketRequired
	if result.OwnershipRevision != item.Revision {
		result.Reason = "stale"
		return result, nil
	}
	if !supportsInstanceAction(target.Provider, item.Action) {
		result.Reason = "unsupported_parameters"
		return result, nil
	}
	volume, err := inspectOperationVolume(ctx, tx, actor, item.Input, target.Provider, false)
	switch {
	case errors.Is(err, identity.ErrForbidden):
		result.Reason = "unavailable"
	case errors.Is(err, ErrConflict):
		result.VolumeRevision = volume.OwnershipRevision
		result.Reason = "busy"
	case errors.Is(err, ErrVolumeStale):
		result.VolumeRevision = volume.OwnershipRevision
		result.Reason = "volume_stale"
	case errors.Is(err, ErrInvalid):
		result.Reason = "unsupported_parameters"
	case err != nil:
		return result, err
	}
	if result.Reason != "" {
		return result, nil
	}
	result.VolumeRevision = volume.OwnershipRevision
	if err := checkInstanceMutationProtection(ctx, tx, target.ScopeID, item.AssetID, item.Action); err != nil {
		if errors.Is(err, ErrConflict) {
			result.Reason = "protected"
			return result, nil
		}
		return result, err
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_instance_operations
		WHERE (scope_id,external_id)=($1,$2) AND status IN `+activeOperationStatuses+`)
		OR ($3::bigint>0 AND EXISTS (SELECT 1 FROM cmp_instance_operations
		WHERE (volume_scope_id,volume_id)=($3,$4) AND volume_scope_id IS NOT NULL
		AND status IN `+activeOperationStatuses+`))`, target.ScopeID, item.AssetID, volume.ScopeID, item.VolumeID).Scan(&busy)
	result.Reason = "ready"
	if busy {
		result.Reason = "busy"
	}
	return result, err
}

func (s Store) SubmitBatch(ctx context.Context, token string, scope identity.Scope, key string, input BatchInput) (BatchReceipt, error) {
	if !operationKey.MatchString(key) {
		return BatchReceipt{}, ErrInvalid
	}
	if err := validateBatch(input); err != nil {
		return BatchReceipt{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return BatchReceipt{}, err
	}
	digest := sha256.Sum256(raw)
	var result BatchReceipt
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		claimed, err := claimBatch(ctx, tx, actor, key, digest, len(input.Items), &result)
		if err != nil {
			return err
		}
		if !claimed {
			return readBatchReplay(ctx, tx, actor, key, digest, &result)
		}
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		targets, err := lockBatchInstances(ctx, tx, actor, input.Items)
		if err != nil {
			return err
		}
		remaining, err := remainingBudget(ctx, tx, actor)
		if err != nil {
			return err
		}
		if remaining < len(input.Items) {
			return ErrQuota
		}
		volumes, err := lockBatchVolumes(ctx, tx, actor, input.Items, targets)
		if err != nil {
			return err
		}
		return s.insertBatchItems(ctx, tx, actor, key, digest, input.Items, targets, volumes, &result)
	})
	if err != nil {
		return BatchReceipt{}, err
	}
	return result, nil
}

func lockBatchInstances(ctx context.Context, tx *sql.Tx, actor identity.Scope, items []BatchItem) ([]inventory.MutationTarget, error) {
	order := make([]int, len(items))
	for index := range order {
		order[index] = index
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(batchCoordinate(items[a].Input, items[a].AssetID), batchCoordinate(items[b].Input, items[b].AssetID))
	})
	targets := make([]inventory.MutationTarget, len(items))
	for _, index := range order {
		item := items[index]
		target, err := inventory.ResolveInstanceMutation(ctx, tx, actor, item.Account, item.Region, item.AssetID)
		if err != nil {
			return nil, BatchItemError{Index: index, Cause: err}
		}
		targets[index] = target
	}
	for index, item := range items {
		if targets[index].OwnershipRevision != item.Revision {
			return nil, BatchItemError{Index: index, Cause: identity.ErrAccountStale}
		}
		if !supportsInstanceAction(targets[index].Provider, item.Action) {
			return nil, BatchItemError{Index: index, Cause: ErrInvalid}
		}
		if err := checkInstanceMutationProtection(ctx, tx, targets[index].ScopeID, item.AssetID, item.Action); err != nil {
			return nil, BatchItemError{Index: index, Cause: err}
		}
	}
	return targets, nil
}

func lockBatchVolumes(ctx context.Context, tx *sql.Tx, actor identity.Scope, items []BatchItem, targets []inventory.MutationTarget) ([]inventory.MutationTarget, error) {
	order := make([]int, 0, len(items))
	for index, item := range items {
		if volumeAction(item.Action) {
			order = append(order, index)
		}
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(batchCoordinate(items[a].Input, items[a].VolumeID), batchCoordinate(items[b].Input, items[b].VolumeID))
	})
	volumes := make([]inventory.MutationTarget, len(items))
	for _, index := range order {
		volume, err := resolveOperationVolume(ctx, tx, actor, items[index].Input, targets[index].Provider)
		if err != nil {
			return nil, BatchItemError{Index: index, Cause: err}
		}
		volumes[index] = volume
	}
	return volumes, nil
}

func claimBatch(ctx context.Context, tx *sql.Tx, actor identity.Scope, key string, digest [32]byte, count int, result *BatchReceipt) (bool, error) {
	err := tx.QueryRowContext(ctx, `INSERT INTO cmp_operation_batches
		(tenant_id,project_id,principal_id,idempotency_key,batch_id,request_hash,item_count) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (tenant_id,project_id,principal_id,idempotency_key) DO NOTHING RETURNING batch_id`,
		actor.Tenant, actor.Project, actor.Principal, key, rand.Text(), digest[:], count).Scan(&result.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s Store) insertBatchItems(ctx context.Context, tx *sql.Tx, actor identity.Scope, key string, digest [32]byte,
	items []BatchItem, targets, volumes []inventory.MutationTarget, result *BatchReceipt) error {
	result.Operations = make([]Operation, len(items))
	for index, item := range items {
		itemKey := "batch-item:" + result.ID + ":" + strconv.Itoa(index)
		itemHash := sha256.Sum256(append([]byte(itemKey+"\x00"), digest[:]...))
		if err := s.insert(ctx, tx, actor, item.Input, targets[index], volumes[index], itemKey, itemHash, &result.Operations[index]); err != nil {
			return BatchItemError{Index: index, Cause: err}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_operation_batch_items
			(tenant_id,project_id,principal_id,batch_key,position,operation_key) VALUES ($1,$2,$3,$4,$5,$6)`,
			actor.Tenant, actor.Project, actor.Principal, key, index, itemKey); err != nil {
			return err
		}
	}
	return nil
}

func readBatchReplay(ctx context.Context, tx *sql.Tx, actor identity.Scope, key string, digest [32]byte, result *BatchReceipt) error {
	var count int
	var saved []byte
	err := tx.QueryRowContext(ctx, `SELECT batch_id,request_hash,item_count FROM cmp_operation_batches
		WHERE (tenant_id,project_id,principal_id,idempotency_key)=($1,$2,$3,$4)`,
		actor.Tenant, actor.Project, actor.Principal, key).Scan(&result.ID, &saved, &count)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(digest[:], saved) != 1 {
		return ErrConflict
	}
	return readBatchItems(ctx, tx, actor, key, count, result)
}

func (s Store) GetBatch(ctx context.Context, token string, scope identity.Scope, id string) (BatchReceipt, error) {
	if !operationKey.MatchString(id) {
		return BatchReceipt{}, ErrInvalid
	}
	result := BatchReceipt{ID: id}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		var key string
		var count int
		owner := actor
		err = tx.QueryRowContext(ctx, `SELECT principal_id,idempotency_key,item_count FROM cmp_operation_batches
			WHERE (tenant_id,project_id,batch_id)=($1,$2,$3)`, actor.Tenant, actor.Project, id).Scan(&owner.Principal, &key, &count)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		return readBatchItems(ctx, tx, owner, key, count, &result)
	})
	if err != nil {
		return BatchReceipt{}, err
	}
	return result, nil
}

func readBatchItems(ctx context.Context, tx *sql.Tx, actor identity.Scope, key string, count int, result *BatchReceipt) (retErr error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+operationFields+` FROM cmp_operation_batch_items AS item
		JOIN cmp_instance_operations AS op
		ON (op.tenant_id,op.project_id,op.principal_id,op.idempotency_key)=(item.tenant_id,item.project_id,item.principal_id,item.operation_key)
		WHERE (item.tenant_id,item.project_id,item.principal_id,item.batch_key)=($1,$2,$3,$4)
		ORDER BY item.position`, actor.Tenant, actor.Project, actor.Principal, key)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	result.Operations = make([]Operation, 0, count)
	for rows.Next() {
		var item Operation
		if err := scanOperation(rows, &item); err != nil {
			return err
		}
		result.Operations = append(result.Operations, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(result.Operations) != count {
		return errors.New("incomplete operation batch receipt")
	}
	return nil
}
