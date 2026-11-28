package alertservice

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/inventory"
)

func EvaluateProjectAlerts(ctx context.Context, tx *sql.Tx, tenant, project string) error {
	if err := billing.EvaluateProjectBudgetAlerts(ctx, tx, tenant, project); err != nil {
		return err
	}
	if err := inventory.EvaluateBucketProtectionAlerts(ctx, tx, tenant, project); err != nil {
		return err
	}
	if err := inventory.EvaluateDatabaseProtectionAlerts(ctx, tx, tenant, project); err != nil {
		return err
	}
	if err := inventory.EvaluateTagPolicyAlerts(ctx, tx, tenant, project); err != nil {
		return err
	}
	return alerts.EvaluateOperationQueueAlerts(ctx, tx, tenant, project)
}
