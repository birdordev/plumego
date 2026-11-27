package main

import (
	"context"
	"database/sql"
	"net/http"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func prepareScheduledBucketSetting(ctx context.Context, db *sql.DB, task controller.Task, path string, transport http.RoundTripper) (controller.Publish, error) {
	var profile publicCloudProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return nil, err
	}
	binding, err := identity.NewStore(db).CloudAccountBinding(ctx, task.Key.Tenant, task.Key.Account)
	if err != nil || (binding.Mode != "read_only" && binding.Mode != "write_enabled") ||
		binding.Provider != task.Provider || binding.Revision != task.CredentialRevision ||
		task.CredentialRef == "" || binding.CredentialRef != task.CredentialRef || profile.Ref != task.CredentialRef ||
		binding.RemoteIdentity == "" {
		return nil, inventory.ErrStale
	}
	client, err := publiccloud.Connect(ctx, binding.Provider, profile.Config, transport)
	if err != nil {
		return nil, err
	}
	if client.Identity() != binding.RemoteIdentity {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	store := inventory.NewStore(db)
	return controller.NewStore(db).PrepareBucketSettingSweep(ctx, task, func(ctx context.Context, after string) (inventory.BucketSettingBatch, error) {
		if task.ScheduleKind == controller.BucketEncryptionScheduleKind {
			return store.PrepareBucketEncryptionBatch(ctx, task.Key, binding.Revision, after, client.ReadBucketEncryptionBatch)
		}
		return store.PrepareBucketVersioningBatch(ctx, task.Key, binding.Revision, after, client.ReadBucketVersioningBatch)
	})
}
