package billing

import (
	"context"
	"database/sql"

	"with-cloud-manager/migrations"
)

func EvaluateProjectBudgetAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	_, err := tx.ExecContext(ctx, migrations.ProjectBudgetAlerts(), tenant, project)
	return err
}
