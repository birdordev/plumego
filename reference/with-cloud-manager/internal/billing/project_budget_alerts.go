package billing

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed project_budget_alerts.sql
var projectBudgetAlerts string

func EvaluateProjectBudgetAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	_, err := tx.ExecContext(ctx, projectBudgetAlerts, tenant, project)
	return err
}
