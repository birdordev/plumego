package billing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
)

var ErrBudgetExists = errors.New("billing budget already exists")
var ErrBudgetStale = errors.New("billing budget requires the current revision")

const BudgetBilled = "billed"
const BudgetEffective = "effective"

type BudgetKey struct {
	Key
	Currency, Basis string
}

func (key BudgetKey) Valid() bool {
	return key.Key.Valid() && currencyCode.MatchString(key.Currency) &&
		(key.Basis == BudgetBilled || key.Basis == BudgetEffective)
}

type BudgetInput struct {
	Limit          string `json:"limit"`
	WarningPercent int    `json:"warning_percent"`
	Enabled        *bool  `json:"enabled"`
}

func (input BudgetInput) valid() bool {
	return input.Enabled != nil && input.WarningPercent >= 1 && input.WarningPercent <= 99 &&
		decimalFormat.MatchString(input.Limit) && !strings.HasPrefix(input.Limit, "-") &&
		strings.ContainsAny(input.Limit, "123456789")
}

type Budget struct {
	ID             string    `json:"id"`
	Account        string    `json:"account"`
	Period         string    `json:"period"`
	Source         string    `json:"source"`
	Currency       string    `json:"currency"`
	Basis          string    `json:"basis"`
	Limit          string    `json:"limit"`
	WarningPercent int       `json:"warning_percent"`
	Enabled        bool      `json:"enabled"`
	Revision       int64     `json:"revision"`
	UpdatedAt      time.Time `json:"updated_at"`
	State          string    `json:"state"`
	Spent          *string   `json:"spent,omitempty"`
	ReportRevision *int64    `json:"report_revision,omitempty"`
}

func MigrateBudgets(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0094_billing_budgets")
}

const budgetKeyCondition = `(tenant_id,project_id,account_id,source,period,currency,basis)=($1,$2,$3,$4,$5::date,$6,$7)`

func (key BudgetKey) args() []any {
	return []any{key.Tenant, key.Project, key.Account, key.Source, key.Period + "-01", key.Currency, key.Basis}
}

func (s Store) GetBudget(ctx context.Context, token string, key BudgetKey) (Budget, error) {
	if !key.Valid() {
		return Budget{}, ErrInvalid
	}
	var budget Budget
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := authorizeBilling(ctx, tx, token, key.Key); err != nil {
			return err
		}
		if _, err := accountBinding(ctx, tx, key.Key, false); err != nil {
			return err
		}
		var err error
		budget, err = readBudget(ctx, tx, key)
		return err
	})
	return budget, err
}

// SaveBudget 在同一事务保存预算、更新告警并记录变更。首次创建使用 expected=0。
func (s Store) SaveBudget(ctx context.Context, token string, key BudgetKey, expected int64, input BudgetInput) (Budget, error) {
	if !key.Valid() || expected < 0 || !input.valid() {
		return Budget{}, ErrInvalid
	}
	var budget Budget
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		if _, err := authorizeBilling(ctx, tx, token, key.Key); err != nil {
			return err
		}
		bound, err := accountBinding(ctx, tx, key.Key, true)
		if err != nil {
			return err
		}
		actor, err := authorizeBilling(ctx, tx, token, key.Key)
		if err != nil {
			return err
		}
		if focusProvider(bound.provider) == "" {
			return ErrInvalid
		}
		if err := saveBudget(ctx, tx, key, expected, input); err != nil {
			return err
		}
		budget, err = readBudget(ctx, tx, key)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SELECT cmp_recheck_billing_budget($1::uuid)", budget.ID); err != nil {
			return err
		}
		action := "updated"
		if expected == 0 {
			action = "created"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
			(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,actor_id,revision,occurred_at)
			VALUES ($1,$2,'billing_budget',$3,$4,'billing-budget',$5,$6,$7,$8)`,
			actor.Tenant, actor.Project, action, budget.ID, budget.Account, actor.Principal, budget.Revision, budget.UpdatedAt)
		return err
	})
	return budget, err
}

func saveBudget(ctx context.Context, tx *sql.Tx, key BudgetKey, expected int64, input BudgetInput) error {
	args := append(key.args(), input.Limit, input.WarningPercent, *input.Enabled)
	var result sql.Result
	var err error
	if expected == 0 {
		result, err = tx.ExecContext(ctx, `INSERT INTO cmp_billing_budgets
			(tenant_id,project_id,account_id,source,period,currency,basis,amount,warning_percent,enabled)
			VALUES ($1,$2,$3,$4,$5::date,$6,$7,$8::numeric,$9,$10) ON CONFLICT DO NOTHING`, args...)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE cmp_billing_budgets
			SET amount=$8::numeric,warning_percent=$9,enabled=$10,revision=revision+1,updated_at=clock_timestamp()
			WHERE `+budgetKeyCondition+` AND revision=$11 AND revision<9223372036854775807`, append(args, expected)...)
	}
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 1 {
		return err
	}
	if expected == 0 {
		return ErrBudgetExists
	}
	return ErrBudgetStale
}

func readBudget(ctx context.Context, tx *sql.Tx, key BudgetKey) (Budget, error) {
	budget := Budget{Account: key.Account, Period: key.Period, Source: key.Source, Currency: key.Currency, Basis: key.Basis}
	err := tx.QueryRowContext(ctx, `SELECT budget_id,amount::text,warning_percent,enabled,revision,updated_at,state,spend::text,report_revision
		FROM cmp_billing_budget_status WHERE `+budgetKeyCondition, key.args()...).
		Scan(&budget.ID, &budget.Limit, &budget.WarningPercent, &budget.Enabled, &budget.Revision,
			&budget.UpdatedAt, &budget.State, &budget.Spent, &budget.ReportRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return Budget{}, ErrNotFound
	}
	return budget, err
}
