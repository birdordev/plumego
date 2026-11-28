package cloud

import (
	"context"
	"database/sql"
	"net/http"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
)

func prepareScheduledDatabaseObservation(ctx context.Context, db *sql.DB, task controller.Task, path string, transport http.RoundTripper) (controller.Publish, error) {
	client, err := connectScheduledPublicCloud(ctx, db, task, path, transport)
	if err != nil {
		return nil, err
	}
	store := inventory.NewStore(db)
	if task.ScheduleKind == controller.DatabaseBackupScheduleKind {
		return store.PrepareDatabaseBackups(ctx, task.Key, task.TargetID, task.CredentialRevision, client)
	}
	return store.PrepareDatabaseRestoreWindows(ctx, task.Key, task.TargetID, task.CredentialRevision, client)
}
