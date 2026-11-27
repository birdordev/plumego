package kvm

import (
	"context"
	"database/sql"
	"time"

	"with-cloud-manager/internal/identity"
)

type EvacuationCandidate struct {
	VM       string `json:"vm_id"`
	Revision int64  `json:"vm_revision"`
	State    string `json:"state"`
	Network  string `json:"network"`
	DiskMiB  int64  `json:"disk_mib"`
	Address  string `json:"ipv4,omitempty"`
}

type EvacuationCandidatePage struct {
	InventoryPage[EvacuationCandidate]
	Mode       string `json:"mode"`
	Revision   int64  `json:"host_revision"`
	Generation int64  `json:"source_fence_generation"`
}

func (s Store) ListEvacuationCandidates(ctx context.Context, token string, scope identity.Scope, site, host, after string,
	limit int) (EvacuationCandidatePage, error) {
	if !identifier.MatchString(host) || (after != "" && !hostUUID.MatchString(after)) || limit < 1 || limit > 100 {
		return EvacuationCandidatePage{}, ErrInvalid
	}
	page := EvacuationCandidatePage{Mode: "unavailable", InventoryPage: InventoryPage[EvacuationCandidate]{Items: make([]EvacuationCandidate, 0, limit+1)}}
	err := s.inProject(ctx, token, scope, site, identity.Manage, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		source, err := readHost(ctx, tx, actor, site, host, false)
		if err != nil {
			return err
		}
		page.Revision, page.Generation = source.Revision, source.Generation
		switch source.FenceState {
		case "":
			page.Generation++
			page.Mode = "plan"
		case "pending":
			page.Mode = "plan"
		case "fenced":
			page.Mode = "batch"
		default:
			return nil
		}
		rows, err := tx.QueryContext(ctx, `SELECT v.vm_id::text,v.revision,v.state,p.network_id,p.disk_mib,COALESCE(host(p.ipv4),'')
			FROM cmp_kvm_placements p JOIN cmp_kvm_vms v USING (placement_id)
			WHERE (p.tenant_id,p.project_id,p.site_id,p.host_id)=($1,$2,$3,$4) AND p.generation<$5
			AND p.consumed_at IS NOT NULL AND p.released_at IS NULL AND p.gpu=0 AND v.state IN `+evacuationVMStates+`
			AND ($6='' OR v.vm_id>NULLIF($6,'')::uuid)
			AND NOT EXISTS (SELECT 1 FROM cmp_kvm_cold_migrations WHERE vm_id=v.vm_id AND status='pending')
			AND NOT EXISTS (SELECT 1 FROM cmp_kvm_live_migrations WHERE vm_id=v.vm_id AND `+activeLiveMigration+`)
			AND ($8<>'plan' OR NOT EXISTS (SELECT 1 FROM cmp_kvm_evacuation_plans AS plan
				CROSS JOIN LATERAL jsonb_array_elements(plan.items) AS member(item)
				WHERE (plan.tenant_id,plan.project_id,plan.site_id,plan.source_host,plan.source_fence_generation)=($1,$2,$3,$4,$5)
				AND plan.status='armed' AND member.item->>'vm_id'=v.vm_id::text))
			ORDER BY v.vm_id LIMIT $7`, actor.Tenant, actor.Project, site, host, page.Generation, after, limit+1, page.Mode)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item EvacuationCandidate
			if err := rows.Scan(&item.VM, &item.Revision, &item.State, &item.Network, &item.DiskMiB, &item.Address); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return EvacuationCandidatePage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		page.Next = page.Items[limit-1].VM
	}
	return page, nil
}

type EvacuationRecord struct {
	ID         string    `json:"id"`
	Generation int64     `json:"source_fence_generation"`
	Requester  string    `json:"requester"`
	Date       time.Time `json:"recorded_at"`
	Status     string    `json:"status,omitempty"`
	BatchID    string    `json:"batch_id,omitempty"`
}

func (s Store) ListEvacuationBatches(ctx context.Context, token string, scope identity.Scope, site, host, after string,
	limit int) (InventoryPage[EvacuationRecord], error) {
	return s.listEvacuationRecords(ctx, token, scope, site, host, after, limit, `
		SELECT batch_id AS id,source_fence_generation,requester_id,requested_at AS recorded_at,'' AS status,'' AS batch_id
		FROM cmp_kvm_evacuation_batches`)
}

func (s Store) ListEvacuationPlans(ctx context.Context, token string, scope identity.Scope, site, host, after string,
	limit int) (InventoryPage[EvacuationRecord], error) {
	return s.listEvacuationRecords(ctx, token, scope, site, host, after, limit, `
		SELECT plan_id AS id,source_fence_generation,requester_id,armed_at AS recorded_at,status,COALESCE(batch_id,'') AS batch_id
		FROM cmp_kvm_evacuation_plans`)
}

func (s Store) listEvacuationRecords(ctx context.Context, token string, scope identity.Scope, site, host, after string,
	limit int, selectSQL string) (InventoryPage[EvacuationRecord], error) {
	if !identifier.MatchString(host) || limit < 1 || limit > 100 {
		return InventoryPage[EvacuationRecord]{}, ErrInvalid
	}
	before, id, err := parseTimedInventoryCursor(after)
	if err != nil {
		return InventoryPage[EvacuationRecord]{}, err
	}
	page := InventoryPage[EvacuationRecord]{Items: make([]EvacuationRecord, 0, limit+1)}
	err = s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		if _, err := readHost(ctx, tx, actor, site, host, false); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT * FROM (`+selectSQL+`
			WHERE (tenant_id,project_id,site_id,source_host)=($1,$2,$3,$4)) AS records
			WHERE ($5::timestamptz IS NULL OR (recorded_at,id)<($5,$6)) ORDER BY recorded_at DESC,id DESC LIMIT $7`,
			actor.Tenant, actor.Project, site, host, before, id, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item EvacuationRecord
			if err := rows.Scan(&item.ID, &item.Generation, &item.Requester, &item.Date, &item.Status, &item.BatchID); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[EvacuationRecord]{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.Next = last.Date.UTC().Format(time.RFC3339Nano) + "|" + last.ID
	}
	return page, nil
}
