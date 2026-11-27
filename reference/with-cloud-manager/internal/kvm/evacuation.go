package kvm

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

func checkEvacuationPlacement(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, input PlacementInput) error {
	v, err := readVM(ctx, tx, actor, site, input.EvacuationVM, false)
	if err != nil {
		return err
	}
	host, err := readHost(ctx, tx, actor, site, v.Host, false)
	if err != nil {
		return err
	}
	if input.RequiredHost == v.Host || input.Network != v.Network || input.Capacity != v.Capacity || v.Capacity.GPU != 0 {
		return ErrConflict
	}
	return checkFencedEvacuationSource(ctx, tx, v, host, host.Generation)
}

func checkFencedEvacuationSource(ctx context.Context, tx *sql.Tx, v vmRecord, host Host, generation int64) error {
	if generation != host.Generation {
		return ErrStale
	}
	if host.ID != v.Host || host.FenceState != "fenced" || v.Generation >= generation {
		return ErrConflict
	}
	switch v.State {
	case "running", "shutdown_requested", "shutdown_queued", "stopping", "stopped", "retire_requested", "retire_queued", "undefining":
	default:
		return ErrConflict
	}
	var bound bool
	err := tx.QueryRowContext(ctx, `SELECT consumed_at IS NOT NULL AND released_at IS NULL
		FROM cmp_kvm_placements WHERE placement_id=$1`, v.Placement).Scan(&bound)
	if err == nil && !bound {
		return ErrConflict
	}
	return err
}

func transferEvacuationAddress(ctx context.Context, tx *sql.Tx, v vmRecord, target Placement) error {
	if err := retainReleasedBootVolume(ctx, tx, v.Placement); err != nil {
		return err
	}
	var id string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_kvm_placements AS target
		SET ipv4=source.ipv4,ipam_network_id=source.ipam_network_id
		FROM cmp_kvm_placements AS source
		WHERE target.placement_id=$1 AND source.placement_id=$2 AND target.evacuation_vm_id=$3::uuid
		  AND target.ipv4 IS NULL AND target.consumed_at IS NULL AND target.released_at IS NULL AND target.expired_at IS NULL
		  AND target.expires_at>clock_timestamp()
		RETURNING target.placement_id`, target.ID, v.Placement, v.ID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || uniqueViolation(err) {
		return ErrConflict
	}
	return err
}
