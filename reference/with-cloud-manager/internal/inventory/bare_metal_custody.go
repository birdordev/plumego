package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
)

func lockBareMetalCustody(ctx context.Context, tx *sql.Tx, system string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('cmp-bare-metal:'||$1,0))`, system)
	return err
}

func CheckBareMetalIdle(ctx context.Context, tx *sql.Tx, system string) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_instance_operations
		WHERE resource_kind='bare-metal' AND external_id=$1
		  AND status IN ('awaiting_approval','queued','applying','observing','needs_review'))
		OR EXISTS (SELECT 1 FROM cmp_bare_metal_deployments AS d JOIN cmp_instance_operations AS b ON b.operation_id=d.boot_operation
			WHERE d.system_id=$1 AND d.result IS NULL AND d.expires_at>statement_timestamp() AND b.status<>'failed')`, system).Scan(&active)
	if err != nil {
		return err
	}
	if active {
		return identity.ErrAccountConflict
	}
	return nil
}

func CheckBareMetalOffline(ctx context.Context, tx *sql.Tx, scopeID, credential int64, system string, after *time.Time) error {
	var confirmed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_inventory_objects AS o
		JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (o.scope_id,o.external_id)=($1,$2) AND s.credential_revision=$3
		  AND NOT s.running AND s.completed_at IS NOT NULL AND o.seen_generation=s.generation
		  AND o.removed_at IS NULL AND o.missing_generation IS NULL AND o.status='off'
		  AND o.observed_at>statement_timestamp()-interval '10 minutes'
		  AND ($4::timestamptz IS NULL OR (o.observed_at>$4 AND s.completed_at>$4)))`, scopeID, system, credential, after).Scan(&confirmed)
	if err == nil && !confirmed {
		return identity.ErrAccountConflict
	}
	return err
}

func LatestBareMetalBoot(ctx context.Context, tx *sql.Tx, system string) (int64, error) {
	var event int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(e.event_id),0)
		FROM cmp_instance_operations AS b JOIN cmp_instance_operation_events AS e USING(operation_id)
		WHERE b.resource_kind='bare-metal' AND b.action='power-on-bare-metal'
		  AND b.external_id=$1 AND e.status IN ('applying','succeeded')`, system).Scan(&event)
	return event, err
}

func checkBareMetalTransfer(ctx context.Context, tx *sql.Tx, actor identity.Scope, scopeID, revision int64, account, site, system string) error {
	if err := lockBareMetalCustody(ctx, tx, system); err != nil {
		return err
	}
	if err := CheckBareMetalIdle(ctx, tx, system); err != nil {
		return err
	}
	var id, state string
	var usable bool
	var credential, event int64
	err := tx.QueryRowContext(ctx, `SELECT r.reclaim_id,r.state,r.credential_revision,COALESCE(r.verified_boot_event,0),
		(r.tenant_id,r.project_id,r.scope_id,r.ownership_revision,r.credential_revision)=($2,$3,$4,$5,a.credential_revision)
		FROM cmp_bare_metal_reclaims AS r JOIN cmp_cloud_accounts AS a USING(tenant_id,account_id)
		WHERE system_id=$1 AND state IN ('requested','approved','claimed','verified')`, system, actor.Tenant, actor.Project, scopeID, revision).
		Scan(&id, &state, &credential, &event, &usable)
	if errors.Is(err, sql.ErrNoRows) {
		event, err := LatestBareMetalBoot(ctx, tx, system)
		if err == nil && event != 0 {
			return identity.ErrAccountConflict
		}
		return err
	}
	if err != nil {
		return err
	}
	if state != "verified" || !usable {
		return identity.ErrAccountConflict
	}
	latest, err := LatestBareMetalBoot(ctx, tx, system)
	if err != nil {
		return err
	}
	if latest != event {
		return identity.ErrAccountConflict
	}
	if err := CheckBareMetalOffline(ctx, tx, scopeID, credential, system, nil); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cmp_bare_metal_reclaims SET state='consumed',consumed_at=statement_timestamp()
		WHERE reclaim_id=$1`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
		(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,region,actor_id,outcome,revision,occurred_at)
		VALUES ($1,$2,'operation','bare_metal_reclaim_consumed',$3,'bare-metal',$4,$5,$6,'consumed',$7,statement_timestamp())`,
		actor.Tenant, actor.Project, id, account, site, actor.Principal, revision)
	return err
}
