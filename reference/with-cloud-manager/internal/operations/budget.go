package operations

import (
	"context"
	"database/sql"
	"fmt"
	"math"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
)

const BudgetMigration = "0173_operation_budget"
const defaultProjectBudget = 100

type ProjectBudgetInput struct {
	MaxActive           *int `json:"max_active"`
	MaxManagedInstances *int `json:"max_managed_instances"`
}

type ProjectBudget struct {
	Revision            int64 `json:"revision,string"`
	MaxActive           int   `json:"max_active"`
	ActiveOperations    int64 `json:"active_operations"`
	MaxManagedInstances int   `json:"max_managed_instances"`
	ReservedInstances   int64 `json:"reserved_instances"`
}

func MigrateBudget(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BudgetMigration)
}

const projectActiveCount = `(SELECT count(*) FROM cmp_instance_operations
	WHERE (tenant_id,project_id)=($1,$2) AND status IN ` + activeOperationStatuses + `)`

const projectInstanceReservations = `(SELECT count(*) FROM cmp_instance_launches AS l
	JOIN cmp_instance_operations AS o USING (operation_id)
	WHERE (o.tenant_id,o.project_id)=($1,$2) AND l.released_at IS NULL)`

func readProjectBudget(ctx context.Context, tx *sql.Tx, actor identity.Scope) (ProjectBudget, error) {
	var budget ProjectBudget
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(b.revision,1),COALESCE(b.max_active,$3),COALESCE(b.max_managed_instances,$3),`+
		projectActiveCount+`,`+projectInstanceReservations+`
		FROM cmp_projects AS p LEFT JOIN cmp_instance_operation_budgets AS b USING (tenant_id,project_id)
		WHERE (p.tenant_id,p.project_id)=($1,$2)`, actor.Tenant, actor.Project, defaultProjectBudget).
		Scan(&budget.Revision, &budget.MaxActive, &budget.MaxManagedInstances, &budget.ActiveOperations, &budget.ReservedInstances)
	return budget, err
}

func (s Store) ReadProjectBudget(ctx context.Context, token string, scope identity.Scope) (budget ProjectBudget, err error) {
	err = dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		budget, err = readProjectBudget(ctx, tx, actor)
		return err
	})
	return budget, err
}

func (s Store) ReplaceProjectBudget(ctx context.Context, token string, scope identity.Scope, expected int64, limits ProjectBudgetInput) (ProjectBudget, error) {
	if limits.MaxActive == nil || *limits.MaxActive < 1 || *limits.MaxActive > 10000 ||
		limits.MaxManagedInstances == nil || *limits.MaxManagedInstances < 0 || *limits.MaxManagedInstances > 100000 {
		return ProjectBudget{}, identity.ErrAccountInvalid
	}
	if expected < 1 {
		return ProjectBudget{}, identity.ErrAccountStale
	}
	var budget ProjectBudget
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		if _, err := operationActor(ctx, tx, token, scope, identity.Manage); err != nil {
			return err
		}
		if _, err := lockProjectRole(ctx, tx, actor, actor.Principal, "admin"); err != nil {
			return err
		}
		current, err := readProjectBudget(ctx, tx, actor)
		if err != nil {
			return err
		}
		if current.Revision != expected {
			return identity.ErrAccountStale
		}
		if current.MaxActive == *limits.MaxActive && current.MaxManagedInstances == *limits.MaxManagedInstances {
			budget = current
			return nil
		}
		if current.Revision == math.MaxInt64 {
			return identity.ErrAccountConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_instance_operation_budgets
			SET max_active=$3,max_managed_instances=$4,revision=revision+1 WHERE (tenant_id,project_id)=($1,$2)`,
			actor.Tenant, actor.Project, *limits.MaxActive, *limits.MaxManagedInstances); err != nil {
			return err
		}
		change := fmt.Sprintf("max_active=%d→%d, max_managed_instances=%d→%d",
			current.MaxActive, *limits.MaxActive, current.MaxManagedInstances, *limits.MaxManagedInstances)
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
			(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,actor_id,outcome,revision,occurred_at)
			VALUES ($1,$2,'operation_budget','replace-operation-budget',$2,'project','',$3,$4,$5,clock_timestamp())`,
			actor.Tenant, actor.Project, actor.Principal, change, expected+1); err != nil {
			return err
		}
		budget, err = readProjectBudget(ctx, tx, actor)
		return err
	})
	if err != nil {
		return ProjectBudget{}, err
	}
	return budget, nil
}
