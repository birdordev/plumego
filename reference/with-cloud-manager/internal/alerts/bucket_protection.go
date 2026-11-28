package alerts

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/dbschema"
)

const BucketProtectionMigration = "0147_bucket_protection_alerts"

func MigrateBucketProtection(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BucketProtectionMigration)
}
