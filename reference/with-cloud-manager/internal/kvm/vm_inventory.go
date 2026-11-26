package kvm

import (
	"context"
	"database/sql"
	"encoding/json"

	"with-cloud-manager/internal/identity"
)

const MaxObservedDomains = 256

type VMInventoryQuery struct {
	AgentIdentity
	Domains []string `json:"domains"`
}

type VMInventory struct {
	VMs []VM `json:"vms"`
}

type CapacityInventory struct {
	HostRevision int64    `json:"host_revision"`
	Domains      []string `json:"domains"`
}

func validObservedDomains(domains []string) bool {
	if len(domains) > MaxObservedDomains {
		return false
	}
	seen := make(map[string]struct{}, len(domains))
	for _, id := range domains {
		if !hostUUID.MatchString(id) {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

const observedVMFilter = `
	WHERE (p.tenant_id,p.project_id,p.site_id,p.host_id)=($1,$2,$3,$4)
		AND p.generation<=$5 AND p.consumed_at IS NOT NULL
		AND p.released_at IS NULL AND p.expired_at IS NULL
		AND v.state IN ('defining','defined','starting','running','shutdown_requested','shutdown_queued','stopping','stopped',
		               'retire_requested','retire_queued','undefining')
		AND v.vm_id IN (SELECT value::uuid FROM jsonb_array_elements_text($6::jsonb))`

func (s Store) LookupVMInventory(ctx context.Context, token string, scope identity.Scope, site, id string, query VMInventoryQuery) (VMInventory, error) {
	if !validObservedDomains(query.Domains) || len(query.Domains) == 0 {
		return VMInventory{}, ErrInvalid
	}
	encoded, err := json.Marshal(query.Domains)
	if err != nil {
		return VMInventory{}, err
	}
	result := VMInventory{VMs: make([]VM, 0, len(query.Domains))}
	err = s.withVMHost(ctx, token, scope, site, id, query.AgentIdentity, func(tx *sql.Tx, actor identity.Scope, host Host) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+vmColumns+vmFrom+observedVMFilter,
			actor.Tenant, actor.Project, site, id, host.Generation, string(encoded))
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			v, err := scanVM(rows)
			if err != nil {
				return err
			}
			result.VMs = append(result.VMs, v.VM)
		}
		return rows.Err()
	})
	return result, err
}

func verifyCapacityInventory(ctx context.Context, tx *sql.Tx, actor identity.Scope, host Host, inventory CapacityInventory) error {
	if inventory.HostRevision != host.Revision {
		return ErrStale
	}
	if len(inventory.Domains) == 0 {
		return nil
	}
	encoded, err := json.Marshal(inventory.Domains)
	if err != nil {
		return err
	}
	var matched int
	err = tx.QueryRowContext(ctx, "SELECT count(*)"+vmFrom+observedVMFilter,
		actor.Tenant, actor.Project, host.Site, host.ID, host.Generation, string(encoded)).Scan(&matched)
	if err != nil {
		return err
	}
	if matched != len(inventory.Domains) {
		return ErrStale
	}
	return nil
}
