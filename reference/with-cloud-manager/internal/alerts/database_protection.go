package alerts

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/dbschema"
)

const DatabaseProtectionMigration = "0172_database_protection_alerts"

func MigrateDatabaseProtection(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, DatabaseProtectionMigration)
}
