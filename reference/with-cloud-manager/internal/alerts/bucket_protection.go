package alerts

import (
	"context"
	"database/sql"
	_ "embed"

	"with-cloud-manager/internal/dbschema"
)

const BucketProtectionMigration = "0147_bucket_protection_alerts"

//go:embed 0147_bucket_protection_alerts.sql
var bucketProtectionSchema string

func MigrateBucketProtection(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BucketProtectionMigration, bucketProtectionSchema)
}
