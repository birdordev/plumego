package kvm

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"

	"with-cloud-manager/internal/identity"
)

var placementKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

const placementColumns = `placement_id,site_id,host_id,generation,network_id,storage_pool,gpu_model,
	vcpu,memory_mib,disk_mib,gpu,
	CASE WHEN released_at IS NOT NULL THEN 'released' WHEN consumed_at IS NOT NULL THEN 'bound' WHEN expired_at IS NOT NULL OR expires_at<=clock_timestamp() THEN 'expired' ELSE 'held' END,
	expires_at,boot_volume_retained,COALESCE(host(ipv4),'')`

func scanPlacement(row *sql.Row, tail ...any) (Placement, error) {
	var p Placement
	targets := []any{&p.ID, &p.Site, &p.Host, &p.Generation, &p.Network, &p.StoragePool, &p.GPUModel,
		&p.Capacity.VCPU, &p.Capacity.MemoryMiB, &p.Capacity.DiskMiB, &p.Capacity.GPU, &p.State, &p.ExpiresAt, &p.BootVolumeRetained, &p.Address}
	err := row.Scan(append(targets, tail...)...)
	p.MAC = IPv4MAC(p.Address)
	return p, err
}

func (s Store) Reserve(ctx context.Context, token string, scope identity.Scope, site, key string, input PlacementInput) (Placement, error) {
	if !placementKey.MatchString(key) || !input.valid() {
		return Placement{}, ErrInvalid
	}
	body, err := json.Marshal(input)
	if err != nil {
		return Placement{}, err
	}
	digest := sha256.Sum256(body)

	var result Placement
	err = s.inProject(ctx, token, scope, site, identity.Execute, false, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		prior, found, err := replayPlacement(ctx, tx, actor, site, key, digest)
		if found || err != nil {
			result = prior
			return err
		}
		if err := lockIPv4Network(ctx, tx, actor, site, input.Network, false); err != nil {
			return err
		}
		pool, err := readIPv4Pool(ctx, tx, actor, site, input.Network, true)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if pool.Network == "" && input.IPv4 != "" {
			return ErrNoIPv4
		}
		host, err := selectHost(ctx, tx, actor, site, input)
		if err != nil {
			return err
		}
		var address string
		if pool.Network != "" {
			address, err = reserveIPv4(ctx, tx, actor, pool, input.IPv4)
			if err != nil {
				return err
			}
		}
		result, err = insertPlacement(ctx, tx, actor, host, key, digest, input, address)
		if uniqueViolation(err) && address != "" {
			return ErrNoIPv4
		}
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, host.ID, "reserve", result.ID)
	})
	return result, err
}

func replayPlacement(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, key string, digest [32]byte) (Placement, bool, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(
		jsonb_build_array($1::text,$2::text,$3::text,$4::text,$5::text)::text,4001129))`,
		actor.Tenant, actor.Project, site, actor.Principal, key); err != nil {
		return Placement{}, false, err
	}
	var stored []byte
	p, err := scanPlacement(tx.QueryRowContext(ctx, "SELECT "+placementColumns+`,request_hash FROM cmp_kvm_placements
		WHERE (tenant_id,project_id,site_id,requester_id,idempotency_key)=($1,$2,$3,$4,$5)`,
		actor.Tenant, actor.Project, site, actor.Principal, key), &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Placement{}, false, nil
	}
	if err != nil {
		return Placement{}, false, err
	}
	if !bytes.Equal(stored, digest[:]) {
		return Placement{}, true, ErrConflict
	}
	return p, true, nil
}

func insertPlacement(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, key string, digest [32]byte, p PlacementInput, address string) (Placement, error) {
	return scanPlacement(tx.QueryRowContext(ctx, `WITH stamp AS (SELECT clock_timestamp() AS at)
		INSERT INTO cmp_kvm_placements
			(placement_id,tenant_id,project_id,site_id,host_id,generation,requester_id,idempotency_key,request_hash,
			network_id,storage_pool,gpu_model,vcpu,memory_mib,disk_mib,gpu,created_at,expires_at,ipv4,ipam_network_id)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,at,at+$17*interval '1 second',
			NULLIF($18,'')::inet,CASE WHEN $18<>'' THEN $10::text END
		FROM stamp RETURNING `+placementColumns, rand.Text(), actor.Tenant, actor.Project, host.Site, host.ID, host.Generation,
		actor.Principal, key, digest[:], p.Network, p.StoragePool, p.GPUModel, p.VCPU, p.MemoryMiB, p.DiskMiB, p.GPU, p.TTLSeconds, address))
}

func (s Store) GetPlacement(ctx context.Context, token string, scope identity.Scope, site, id string) (Placement, error) {
	if !placementKey.MatchString(id) {
		return Placement{}, ErrInvalid
	}
	var result Placement
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, err = scanPlacement(tx.QueryRowContext(ctx, "SELECT "+placementColumns+` FROM cmp_kvm_placements
			WHERE (tenant_id,project_id,site_id,placement_id)=($1,$2,$3,$4)`, actor.Tenant, actor.Project, site, id))
		return hiddenMissing(err)
	})
	return result, err
}

func (s Store) Release(ctx context.Context, token string, scope identity.Scope, site, id string) (Placement, error) {
	if !placementKey.MatchString(id) {
		return Placement{}, ErrInvalid
	}
	var result Placement
	err := s.inProject(ctx, token, scope, site, identity.Execute, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		var requester string
		current, err := scanPlacement(tx.QueryRowContext(ctx, "SELECT "+placementColumns+`,requester_id FROM cmp_kvm_placements
			WHERE (tenant_id,project_id,site_id,placement_id)=($1,$2,$3,$4) FOR UPDATE`, actor.Tenant, actor.Project, site, id), &requester)
		if err != nil {
			return hiddenMissing(err)
		}
		if requester != actor.Principal && member.Role != "admin" {
			return identity.ErrForbidden
		}
		if current.State == "bound" {
			return ErrConflict
		}
		if current.State != "held" {
			result = current
			return nil
		}
		result, err = scanPlacement(tx.QueryRowContext(ctx, `UPDATE cmp_kvm_placements SET released_at=clock_timestamp()
			WHERE placement_id=$1 RETURNING `+placementColumns, current.ID))
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, current.Host, "release", current.ID)
	})
	return result, err
}
