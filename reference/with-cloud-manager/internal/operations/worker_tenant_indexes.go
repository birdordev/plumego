package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"with-cloud-manager/internal/dbschema"
)

const WorkerTenantMigration = "0149_operation_worker_tenant"

var ErrWorkerTenantIndexConflict = errors.New("tenant worker index definition conflicts with migration")

// MigrateWorkerTenantIndexes 在线建立按租户领取的两个部分索引。
func MigrateWorkerTenantIndexes(ctx context.Context, db *sql.DB) error {
	return dbschema.ApplyOnline(ctx, db, WorkerTenantMigration, func(ctx context.Context, conn *sql.Conn) error {
		for _, index := range []workerTenantIndex{
			{name: "cmp_operation_tasks_tenant_ready", timeColumn: "available_at", status: "queued"},
			{name: "cmp_operation_tasks_tenant_lease", timeColumn: "lease_until", status: "leased"},
		} {
			if err := index.ensure(ctx, conn); err != nil {
				return fmt.Errorf("index %s: %w", index.name, err)
			}
		}
		return nil
	})
}

type workerTenantIndex struct {
	name, timeColumn, status string
}

type workerTenantIndexState struct {
	schema                  string
	exists, matches, usable bool
}

func (index workerTenantIndex) ensure(ctx context.Context, conn *sql.Conn) error {
	state, err := index.readState(ctx, conn)
	if err != nil {
		return err
	}
	if state.exists && !state.matches {
		return ErrWorkerTenantIndexConflict
	}
	if state.usable {
		return nil
	}
	if state.exists {
		// 构建中断后暂存索引可能已进入系统目录，重跑时重建该索引。
		qualified := pgx.Identifier{state.schema, index.name}.Sanitize()
		if _, err := conn.ExecContext(ctx, "DROP INDEX CONCURRENTLY "+qualified); err != nil {
			return err
		}
	}
	table := pgx.Identifier{state.schema, "cmp_operation_tasks"}.Sanitize()
	name := pgx.Identifier{index.name}.Sanitize()
	column := pgx.Identifier{index.timeColumn}.Sanitize()
	statement := fmt.Sprintf("CREATE INDEX CONCURRENTLY %s ON %s USING btree (tenant_id, topic, %s) WHERE status = '%s'",
		name, table, column, index.status)
	if _, err := conn.ExecContext(ctx, statement); err != nil {
		return err
	}
	state, err = index.readState(ctx, conn)
	if err == nil && (!state.matches || !state.usable) {
		return errors.New("tenant worker index did not become usable")
	}
	return err
}

func (index workerTenantIndex) readState(ctx context.Context, conn *sql.Conn) (workerTenantIndexState, error) {
	var state workerTenantIndexState
	err := conn.QueryRowContext(ctx, `SELECT ns.nspname, candidate.oid IS NOT NULL,
		COALESCE(i.indrelid=target.oid AND NOT i.indisunique
			AND right(pg_get_indexdef(i.indexrelid),length(shape.definition))=shape.definition,false),
		COALESCE(i.indisvalid AND i.indisready AND i.indislive,false)
		FROM pg_class target JOIN pg_namespace ns ON ns.oid=target.relnamespace
		LEFT JOIN pg_class candidate ON candidate.relnamespace=target.relnamespace AND candidate.relname=$1
		LEFT JOIN pg_index i ON i.indexrelid=candidate.oid
		CROSS JOIN (SELECT format(' USING btree (tenant_id, topic, %I) WHERE (status = %L::text)', $2::text,$3::text) AS definition) AS shape
		WHERE target.oid=to_regclass('cmp_operation_tasks')`, index.name, index.timeColumn, index.status).
		Scan(&state.schema, &state.exists, &state.matches, &state.usable)
	return state, err
}
