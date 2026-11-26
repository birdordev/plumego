package kvm

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"with-cloud-manager/internal/identity"
)

type IPv4PoolSpec struct {
	Network string `json:"network"`
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
	First   string `json:"first_ip"`
	Last    string `json:"last_ip"`
}

type IPv4Pool struct {
	IPv4PoolSpec
	Site      string    `json:"site"`
	CreatedAt time.Time `json:"created_at"`
}

type IPv4Assignment struct {
	Address string `json:"ipv4,omitempty"`
	MAC     string `json:"mac,omitempty"`
}

func IPv4MAC(address string) string {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() {
		return ""
	}
	b := ip.As4()
	return fmt.Sprintf("02:00:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3])
}

func canonicalIPv4(value string) netip.Addr {
	ip, err := netip.ParseAddr(value)
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.String() != value {
		return netip.Addr{}
	}
	return ip
}

func ipv4Number(ip netip.Addr) uint32 {
	raw := ip.As4()
	return binary.BigEndian.Uint32(raw[:])
}

func (spec IPv4PoolSpec) Valid() bool {
	subnet, err := netip.ParsePrefix(spec.Subnet)
	if err != nil || !identifier.MatchString(spec.Network) || !subnet.Addr().Is4() ||
		subnet != subnet.Masked() || subnet.String() != spec.Subnet || subnet.Bits() < 16 || subnet.Bits() > 30 {
		return false
	}
	first, last, gateway := canonicalIPv4(spec.First), canonicalIPv4(spec.Last), canonicalIPv4(spec.Gateway)
	if !subnet.Contains(first) || !subnet.Contains(last) || !subnet.Contains(gateway) {
		return false
	}
	base := ipv4Number(subnet.Addr())
	broadcast := base | ((1 << (32 - subnet.Bits())) - 1)
	a, b, g := ipv4Number(first), ipv4Number(last), ipv4Number(gateway)
	return a > base && b < broadcast && b >= a && b-a < 4096 && g > base && g < broadcast && (g < a || g > b)
}

const ipv4PoolColumns = `network_id,subnet::text,host(gateway),host(first_ip),host(last_ip),site_id,created_at`

func scanIPv4Pool(row *sql.Row) (IPv4Pool, error) {
	var pool IPv4Pool
	err := row.Scan(&pool.Network, &pool.Subnet, &pool.Gateway, &pool.First, &pool.Last, &pool.Site, &pool.CreatedAt)
	return pool, err
}

func lockIPv4Network(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, network string, exclusive bool) error {
	lock := "pg_advisory_xact_lock_shared"
	if exclusive {
		lock = "pg_advisory_xact_lock"
	}
	_, err := tx.ExecContext(ctx, "SELECT "+lock+`(hashtextextended(
		jsonb_build_array($1::text,$2::text,$3::text,$4::text)::text,4001159))`, actor.Tenant, actor.Project, site, network)
	return err
}

func (s Store) RegisterIPv4Pool(ctx context.Context, token string, scope identity.Scope, site string, spec IPv4PoolSpec) (IPv4Pool, error) {
	if !spec.Valid() {
		return IPv4Pool{}, ErrInvalid
	}
	var result IPv4Pool
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if err := lockIPv4Network(ctx, tx, actor, site, "", true); err != nil {
			return err
		}
		if err := lockIPv4Network(ctx, tx, actor, site, spec.Network, true); err != nil {
			return err
		}
		var active bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_kvm_ipv4_pools
			WHERE (tenant_id,project_id,site_id)=($1,$2,$3) AND (network_id=$4 OR subnet && $5::cidr))
			OR EXISTS (SELECT 1 FROM cmp_kvm_placements
			WHERE (tenant_id,project_id,site_id,network_id)=($1,$2,$3,$4)
			  AND released_at IS NULL AND expired_at IS NULL AND (consumed_at IS NOT NULL OR expires_at>statement_timestamp()))`,
			actor.Tenant, actor.Project, site, spec.Network, spec.Subnet).Scan(&active)
		if err != nil {
			return err
		}
		if active {
			return ErrConflict
		}
		// 网络启用 IPAM 后，服务器时钟回拨也不能恢复不带地址的旧预留。
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_kvm_placements SET expired_at=statement_timestamp()
			WHERE (tenant_id,project_id,site_id,network_id)=($1,$2,$3,$4)
			  AND released_at IS NULL AND expired_at IS NULL AND consumed_at IS NULL AND expires_at<=statement_timestamp()`,
			actor.Tenant, actor.Project, site, spec.Network); err != nil {
			return err
		}
		result, err = scanIPv4Pool(tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_ipv4_pools
			(tenant_id,project_id,site_id,network_id,subnet,gateway,first_ip,last_ip,created_by)
			VALUES ($1,$2,$3,$4,$5::cidr,$6::inet,$7::inet,$8::inet,$9) RETURNING `+ipv4PoolColumns,
			actor.Tenant, actor.Project, site, spec.Network, spec.Subnet, spec.Gateway, spec.First, spec.Last, actor.Principal))
		if uniqueViolation(err) {
			return ErrConflict
		}
		return err
	})
	return result, err
}

func (s Store) GetIPv4Pool(ctx context.Context, token string, scope identity.Scope, site, network string) (IPv4Pool, error) {
	if !identifier.MatchString(network) {
		return IPv4Pool{}, ErrInvalid
	}
	var result IPv4Pool
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, err = readIPv4Pool(ctx, tx, actor, site, network, false)
		return hiddenMissing(err)
	})
	return result, err
}

func (s Store) HostIPv4Pool(ctx context.Context, token string, scope identity.Scope, site, hostID, network string, agent AgentIdentity) (IPv4Pool, error) {
	if !identifier.MatchString(network) {
		return IPv4Pool{}, ErrInvalid
	}
	var pool IPv4Pool
	err := s.withVMHost(ctx, token, scope, site, hostID, agent, func(tx *sql.Tx, actor identity.Scope, host Host) error {
		if !slices.Contains(host.Networks, network) {
			return identity.ErrForbidden
		}
		var err error
		pool, err = readIPv4Pool(ctx, tx, actor, site, network, false)
		return hiddenMissing(err)
	})
	return pool, err
}

func readIPv4Pool(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, network string, lock bool) (IPv4Pool, error) {
	query := "SELECT " + ipv4PoolColumns + ` FROM cmp_kvm_ipv4_pools WHERE (tenant_id,project_id,site_id,network_id)=($1,$2,$3,$4)`
	if lock {
		query += " FOR UPDATE"
	}
	return scanIPv4Pool(tx.QueryRowContext(ctx, query, actor.Tenant, actor.Project, site, network))
}

func reserveIPv4(ctx context.Context, tx *sql.Tx, actor identity.Scope, pool IPv4Pool, requested string) (string, error) {
	var result string
	err := tx.QueryRowContext(ctx, `WITH range AS (
			SELECT $5::inet + offset_ip AS addr
			FROM generate_series(0::bigint,$6::inet-$5::inet) AS offset_ip
			WHERE $7='' OR $5::inet+offset_ip=NULLIF($7,'')::inet
		)
		SELECT host(addr) FROM range
		WHERE NOT EXISTS (SELECT 1 FROM cmp_kvm_placements
			WHERE (tenant_id,project_id,site_id,ipam_network_id,ipv4)=($1,$2,$3,$4,addr)
			  AND released_at IS NULL AND expired_at IS NULL AND (consumed_at IS NOT NULL OR expires_at>statement_timestamp()))
		ORDER BY addr LIMIT 1`, actor.Tenant, actor.Project, pool.Site, pool.Network, pool.First, pool.Last, requested).Scan(&result)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoIPv4
	}
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cmp_kvm_placements SET expired_at=statement_timestamp()
		WHERE (tenant_id,project_id,site_id,ipam_network_id,ipv4)=($1,$2,$3,$4,$5::inet)
		  AND released_at IS NULL AND expired_at IS NULL AND consumed_at IS NULL AND expires_at<=statement_timestamp()`,
		actor.Tenant, actor.Project, pool.Site, pool.Network, result)
	return result, err
}
