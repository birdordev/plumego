package kvm

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
)

func (s Store) GetHostFence(ctx context.Context, token string, scope identity.Scope, site, id string) (HostFenceHistory, error) {
	if !identifier.MatchString(id) {
		return HostFenceHistory{}, ErrInvalid
	}
	history := HostFenceHistory{Events: []HostFenceEvent{}}
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		host, err := readHost(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		history.Host = host
		rows, err := tx.QueryContext(ctx, `SELECT host_revision,host_generation,actor_id,action,evidence_sha256,replacement_agent,occurred_at
			FROM cmp_kvm_host_fence_events
			WHERE (tenant_id,project_id,site_id,host_id)=($1,$2,$3,$4) AND host_revision<=$5
			ORDER BY host_revision DESC LIMIT 20`, actor.Tenant, actor.Project, site, id, host.Revision)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var event HostFenceEvent
			if err := rows.Scan(&event.Revision, &event.Generation, &event.Actor, &event.Action,
				&event.EvidenceSHA256, &event.ReplacementAgent, &event.OccurredAt); err != nil {
				return err
			}
			history.Events = append(history.Events, event)
		}
		return rows.Err()
	})
	return history, err
}
