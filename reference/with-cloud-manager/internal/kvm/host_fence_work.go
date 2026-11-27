package kvm

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"with-cloud-manager/internal/identity"
)

type HostFenceWorkItem struct {
	VM              string `json:"vm_id"`
	Placement       string `json:"placement_id"`
	Generation      int64  `json:"vm_generation"`
	Revision        int64  `json:"vm_revision"`
	State           string `json:"state"`
	Observation     string `json:"observation"`
	PendingRecovery string `json:"pending_recovery_id,omitempty"`
	EvacuationID    string `json:"evacuation_id,omitempty"`
}

type HostFenceWorkPage struct {
	Host       string              `json:"host"`
	HostUUID   string              `json:"host_uuid"`
	Revision   int64               `json:"host_revision"`
	Generation int64               `json:"host_generation"`
	FenceState string              `json:"fence_state"`
	Items      []HostFenceWorkItem `json:"items"`
	Next       string              `json:"next,omitempty"`
}

const fenceBlockingVMs = ` FROM cmp_kvm_vms AS v JOIN cmp_kvm_placements AS p USING (placement_id)
	WHERE (p.tenant_id,p.project_id,p.site_id,p.host_id)=($1,$2,$3,$4)
	  AND v.state IN ('awaiting_approval','queued','staging','staged','defining','defined','starting','stopping','undefining')`

// 单条查询读取宿主机、任务和待审批记录，避免隔离状态切换时返回混合代次。
const hostFenceWorkQuery = `SELECT h.host_uuid::text,h.revision,h.generation,h.fence_state,
	COALESCE(work.vm_id::text,''),COALESCE(work.placement_id,''),COALESCE(work.generation,0),
	COALESCE(work.revision,0),COALESCE(work.state,''),COALESCE(work.observation,''),COALESCE(r.recovery_id,''),
	COALESCE(work.evacuation_id,'')
	FROM cmp_kvm_hosts AS h
	LEFT JOIN LATERAL (
		SELECT * FROM (
			SELECT v.vm_id,p.placement_id,p.generation,v.revision,v.state,v.observation,'' AS evacuation_id` + fenceBlockingVMs + `
			UNION ALL
			SELECT m.vm_id,m.source_placement_id,(SELECT generation FROM cmp_kvm_placements WHERE placement_id=m.source_placement_id),
				m.vm_revision,'cleanup_required','',m.migration_id` + pendingEvacuationCleanup + `
		) AS blockers
		WHERE h.fence_state<>'' AND ($5::bigint=0 OR $5=h.generation)
			AND ($6='' OR blockers.vm_id>NULLIF($6,'')::uuid)
		ORDER BY blockers.vm_id LIMIT $7
	) AS work ON true
	LEFT JOIN cmp_kvm_fenced_vm_recoveries AS r ON r.vm_id=work.vm_id AND r.status='pending' AND work.evacuation_id=''
		AND r.fence_generation=h.generation AND r.vm_revision=work.revision
		AND (r.tenant_id,r.project_id,r.site_id,r.host_id)=(h.tenant_id,h.project_id,h.site_id,h.host_id)
	WHERE (h.tenant_id,h.project_id,h.site_id,h.host_id)=($1,$2,$3,$4)
	ORDER BY work.vm_id`

func (s Store) ListHostFenceWork(ctx context.Context, token string, scope identity.Scope, site, host, after string, limit int) (HostFenceWorkPage, error) {
	if !identifier.MatchString(host) || limit < 1 || limit > 100 {
		return HostFenceWorkPage{}, ErrInvalid
	}
	uuid, generation, vm, err := parseHostFenceWorkCursor(after)
	if err != nil {
		return HostFenceWorkPage{}, err
	}
	result := HostFenceWorkPage{Host: host, Items: make([]HostFenceWorkItem, 0, limit+1)}
	err = s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		rows, err := tx.QueryContext(ctx, hostFenceWorkQuery, actor.Tenant, actor.Project, site, host, generation, vm, limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var item HostFenceWorkItem
			if err := rows.Scan(&result.HostUUID, &result.Revision, &result.Generation, &result.FenceState,
				&item.VM, &item.Placement, &item.Generation, &item.Revision, &item.State, &item.Observation,
				&item.PendingRecovery, &item.EvacuationID); err != nil {
				return err
			}
			if item.VM != "" {
				result.Items = append(result.Items, item)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if result.Generation == 0 {
			return identity.ErrForbidden
		}
		if after != "" && (uuid != result.HostUUID || generation != result.Generation) {
			return ErrStale
		}
		return nil
	})
	if err != nil {
		return HostFenceWorkPage{}, err
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.Next = result.HostUUID + "." + strconv.FormatInt(result.Generation, 10) + "." + result.Items[limit-1].VM
	}
	return result, nil
}

func parseHostFenceWorkCursor(after string) (string, int64, string, error) {
	if after == "" {
		return "", 0, "", nil
	}
	host, rest, _ := strings.Cut(after, ".")
	raw, id, _ := strings.Cut(rest, ".")
	generation, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || generation < 1 || raw != strconv.FormatInt(generation, 10) ||
		!hostUUID.MatchString(host) || !hostUUID.MatchString(id) {
		return "", 0, "", ErrInvalid
	}
	return host, generation, id, nil
}
