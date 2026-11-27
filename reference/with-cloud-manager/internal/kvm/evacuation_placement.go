package kvm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"

	"with-cloud-manager/internal/identity"
)

func requestAutomaticEvacuation(ctx context.Context, tx *sql.Tx, actor identity.Scope, member identity.Membership, site string,
	vm vmRecord, revision int64, input ColdMigrationInput) (ColdMigration, error) {
	if err := lockIPv4Network(ctx, tx, actor, site, vm.Network, false); err != nil {
		return ColdMigration{}, err
	}
	source, err := readHost(ctx, tx, actor, site, vm.Host, true)
	if err != nil {
		return ColdMigration{}, err
	}
	prior, found, err := replayColdMigration(ctx, tx, actor, site, vm.ID, revision, input)
	if found || err != nil {
		return prior, err
	}
	vm, err = readVM(ctx, tx, actor, site, vm.ID, false)
	if err != nil {
		return ColdMigration{}, err
	}
	if err := checkColdMigrationSource(ctx, tx, vm, source, revision, input.SourceFenceGeneration); err != nil {
		return ColdMigration{}, err
	}
	image, err := readBootImage(ctx, tx, actor, site, input.Image)
	if err != nil {
		return ColdMigration{}, err
	}
	if image.SizeBytes > vm.Capacity.DiskMiB*(1<<20) {
		return ColdMigration{}, ErrConflict
	}
	spec := PlacementInput{Capacity: vm.Capacity, Network: vm.Network, ExcludedFailureDomain: source.FailureDomain,
		TTLSeconds: maxPlacementTTL, EvacuationVM: vm.ID}
	// 已持有源主机锁，跳过正在执行其他事务的目标，避免反向等待。
	target, err := selectHost(ctx, tx, actor, site, spec, true)
	if err != nil {
		return ColdMigration{}, err
	}
	vm, err = readVM(ctx, tx, actor, site, vm.ID, true)
	if err != nil {
		return ColdMigration{}, err
	}
	if err := checkColdMigrationSource(ctx, tx, vm, source, revision, input.SourceFenceGeneration); err != nil {
		return ColdMigration{}, err
	}
	if err := checkVMGrant(ctx, tx, actor, actor.Principal, string(identity.Manage), member.Revision); err != nil {
		return ColdMigration{}, err
	}
	spec.StoragePool, spec.RequiredHost = target.StoragePool, target.ID
	body, err := json.Marshal(spec)
	if err != nil {
		return ColdMigration{}, err
	}
	hold, err := insertPlacement(ctx, tx, actor, target, "evacuate-"+rand.Text(), sha256.Sum256(body), spec, "")
	if err != nil {
		return ColdMigration{}, err
	}
	if err := recordEvent(ctx, tx, actor, site, target.ID, "reserve", hold.ID); err != nil {
		return ColdMigration{}, err
	}
	return insertColdMigration(ctx, tx, actor, member, site, vm, hold, input)
}

func releaseAutomaticEvacuationHold(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, migration ColdMigration) error {
	result, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_placements SET released_at=clock_timestamp()
		WHERE placement_id=$1 AND evacuation_vm_id=$2::uuid
		  AND consumed_at IS NULL AND released_at IS NULL AND expired_at IS NULL AND expires_at>clock_timestamp()`,
		migration.Placement, migration.VM)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return err
	}
	return recordEvent(ctx, tx, actor, site, migration.TargetHost, "release", migration.Placement)
}
