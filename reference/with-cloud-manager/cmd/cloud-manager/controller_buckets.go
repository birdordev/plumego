package main

import (
	"context"
	"database/sql"
	"net/http"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
)

func prepareScheduledBucketSetting(ctx context.Context, db *sql.DB, task controller.Task, path string, transport http.RoundTripper) (controller.Publish, error) {
	client, err := connectScheduledPublicCloud(ctx, db, task, path, transport)
	if err != nil {
		return nil, err
	}
	store := inventory.NewStore(db)
	return controller.NewStore(db).PrepareBucketSettingSweep(ctx, task, func(ctx context.Context, after string) (inventory.BucketSettingBatch, error) {
		if task.ScheduleKind == controller.BucketEncryptionScheduleKind {
			return store.PrepareBucketEncryptionBatch(ctx, task.Key, task.CredentialRevision, after, client.ReadBucketEncryptionBatch)
		}
		return store.PrepareBucketVersioningBatch(ctx, task.Key, task.CredentialRevision, after, client.ReadBucketVersioningBatch)
	})
}
