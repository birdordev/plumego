package kvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"with-cloud-manager/internal/identity"
)

const MaxEvacuationBatchItems = 16

type EvacuationBatchItemInput struct {
	VM                    string `json:"vm_id"`
	Revision              int64  `json:"vm_revision"`
	Image                 string `json:"image_id"`
	Evidence              string `json:"evidence_sha256"`
	RequireVerifiedBackup bool   `json:"require_verified_backup,omitempty"`
}

type EvacuationBatchInput struct {
	ID                    string                     `json:"batch_id"`
	SourceFenceGeneration int64                      `json:"source_fence_generation"`
	Items                 []EvacuationBatchItemInput `json:"items"`
}

type EvacuationBatch struct {
	ID                    string          `json:"batch_id"`
	SourceHost            string          `json:"source_host"`
	SourceFenceGeneration int64           `json:"source_fence_generation"`
	HostRevision          int64           `json:"host_revision"`
	Requester             string          `json:"requester"`
	RequestedAt           time.Time       `json:"requested_at"`
	Items                 []ColdMigration `json:"items"`
}

func (input EvacuationBatchInput) valid() bool {
	return identifier.MatchString(input.ID) && input.SourceFenceGeneration > 0 && validEvacuationItems(input.Items)
}

func validEvacuationItems(items []EvacuationBatchItemInput) bool {
	if len(items) == 0 || len(items) > MaxEvacuationBatchItems {
		return false
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if !hostUUID.MatchString(item.VM) || item.Revision < 1 || !identifier.MatchString(item.Image) ||
			!fenceEvidence.MatchString(item.Evidence) {
			return false
		}
		if _, duplicate := seen[item.VM]; duplicate {
			return false
		}
		seen[item.VM] = struct{}{}
	}
	return true
}

func (s Store) RequestEvacuationBatch(ctx context.Context, token string, scope identity.Scope, site, host string, revision int64, input EvacuationBatchInput) (EvacuationBatch, error) {
	if !identifier.MatchString(host) || revision < 1 || !input.valid() {
		return EvacuationBatch{}, ErrInvalid
	}
	body, err := json.Marshal(input)
	if err != nil {
		return EvacuationBatch{}, err
	}
	hash := sha256.Sum256(body)
	var result EvacuationBatch
	err = s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		prior, found, err := replayEvacuationBatch(ctx, tx, actor, site, host, revision, input.ID, hash)
		if found || err != nil {
			result = prior
			return err
		}
		if err := lockEvacuationBatchNetworks(ctx, tx, actor, site, input.Items); err != nil {
			return err
		}
		source, err := readHost(ctx, tx, actor, site, host, true)
		if err != nil {
			return err
		}
		prior, found, err = replayEvacuationBatch(ctx, tx, actor, site, host, revision, input.ID, hash)
		if found || err != nil {
			result = prior
			return err
		}
		if source.Revision != revision || source.Generation != input.SourceFenceGeneration {
			return ErrStale
		}
		if source.FenceState != "fenced" {
			return ErrConflict
		}
		if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
			return err
		}
		result, err = insertEvacuationBatch(ctx, tx, actor, member, source, input, hash)
		return err
	})
	if err != nil {
		return EvacuationBatch{}, err
	}
	return result, nil
}

func insertEvacuationBatch(ctx context.Context, tx *sql.Tx, actor identity.Scope, member identity.Membership,
	source Host, input EvacuationBatchInput, hash [32]byte) (EvacuationBatch, error) {
	result := EvacuationBatch{ID: input.ID, SourceHost: source.ID, SourceFenceGeneration: source.Generation,
		HostRevision: source.Revision, Requester: actor.Principal, Items: make([]ColdMigration, 0, len(input.Items))}
	if err := tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_evacuation_batches
		(tenant_id,project_id,site_id,source_host,batch_id,source_fence_generation,host_revision,requester_id,requester_revision,request_sha256)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING requested_at`,
		actor.Tenant, actor.Project, source.Site, source.ID, input.ID, source.Generation, source.Revision,
		actor.Principal, member.Revision, hash[:]).Scan(&result.RequestedAt); err != nil {
		return EvacuationBatch{}, err
	}
	for i, item := range input.Items {
		migration, err := requestEvacuationBatchItem(ctx, tx, actor, member, source.Site, source, input.ID, i+1, item)
		if err != nil {
			return EvacuationBatch{}, err
		}
		result.Items = append(result.Items, migration)
	}
	return result, nil
}

func lockEvacuationBatchNetworks(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, items []EvacuationBatchItemInput) error {
	networks := make([]string, 0, len(items))
	for _, item := range items {
		vm, err := readVM(ctx, tx, actor, site, item.VM, false)
		if err != nil {
			return err
		}
		networks = append(networks, vm.Network)
	}
	// 一次锁定涉及的网络，再锁源宿主机。单项审核也使用网络到宿主机的顺序。
	slices.Sort(networks)
	for _, network := range slices.Compact(networks) {
		if err := lockIPv4Network(ctx, tx, actor, site, network, false); err != nil {
			return err
		}
	}
	return nil
}

func requestEvacuationBatchItem(ctx context.Context, tx *sql.Tx, actor identity.Scope, member identity.Membership, site string,
	source Host, batch string, position int, item EvacuationBatchItemInput) (ColdMigration, error) {
	vm, err := readVM(ctx, tx, actor, site, item.VM, false)
	if err != nil {
		return ColdMigration{}, err
	}
	if vm.Host != source.ID {
		return ColdMigration{}, ErrConflict
	}
	key := sha256.Sum256([]byte(source.ID + "/" + batch + "/" + item.VM))
	input := ColdMigrationInput{ID: "batch-" + hex.EncodeToString(key[:16]), Image: item.Image,
		Evidence: item.Evidence, SourceFenceGeneration: source.Generation, RequireVerifiedBackup: item.RequireVerifiedBackup}
	if _, err := readColdMigration(ctx, tx, actor, site, item.VM, input.ID); err == nil {
		return ColdMigration{}, ErrConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ColdMigration{}, err
	}
	migration, err := requestAutomaticEvacuation(ctx, tx, actor, member, site, vm, item.Revision, input)
	if err != nil {
		return ColdMigration{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cmp_kvm_evacuation_batch_items
		(tenant_id,project_id,site_id,source_host,batch_id,position,vm_id,migration_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7::uuid,$8)`,
		actor.Tenant, actor.Project, site, source.ID, batch, position, item.VM, migration.ID)
	return migration, err
}

func replayEvacuationBatch(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, host string, revision int64,
	id string, hash [32]byte) (EvacuationBatch, bool, error) {
	batch, stored, err := readEvacuationBatch(ctx, tx, actor, site, host, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EvacuationBatch{}, false, nil
	}
	if err != nil {
		return EvacuationBatch{}, false, err
	}
	if batch.Requester != actor.Principal || batch.HostRevision != revision || !bytes.Equal(stored, hash[:]) {
		return EvacuationBatch{}, true, ErrConflict
	}
	return batch, true, nil
}
