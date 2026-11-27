package dbschema

import (
	"context"
	"database/sql"
	"fmt"

	dbutil "github.com/birdordev/plumego/store/db"
)

const migrationLockID int64 = 5900782301741251

const migrationLedger = `CREATE TABLE IF NOT EXISTS cmp_migrations (
	version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT statement_timestamp()
)`

const migrationExists = "SELECT EXISTS (SELECT 1 FROM cmp_migrations WHERE version=$1)"
const recordMigration = "INSERT INTO cmp_migrations(version) VALUES ($1)"

func Apply(ctx context.Context, db *sql.DB, version, script string) error {
	return dbutil.WithTransaction(ctx, db, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrationLedger); err != nil {
			return err
		}
		var applied bool
		if err := tx.QueryRowContext(ctx, migrationExists, version).Scan(&applied); err != nil {
			return err
		}
		if !applied {
			if _, err := tx.ExecContext(ctx, script); err != nil {
				return fmt.Errorf("apply migration %s: %w", version, err)
			}
			if _, err := tx.ExecContext(ctx, recordMigration, version); err != nil {
				return err
			}
		}
		return nil
	})
}
