package billing

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
)

//go:embed 0146_project_billing_budgets.sql
var projectBudgetSchema string

const ProjectBudgetMigration = "0146_project_billing_budgets"
const MaxProjectBudgetAccounts = 100

func MigrateProjectBudgets(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, ProjectBudgetMigration, projectBudgetSchema)
}

type ProjectBudgetKey struct {
	Tenant, Project, Period, Source, Currency, Basis string
}

func (k ProjectBudgetKey) Valid() bool {
	_, validMonth := billingMonth(k.Period)
	return validID(k.Tenant) && validID(k.Project) && validMonth && (k.Source == Native || k.Source == Focus) &&
		currencyCode.MatchString(k.Currency) && (k.Basis == BudgetBilled || k.Basis == BudgetEffective)
}

func (k ProjectBudgetKey) authKey() Key { return Key{Tenant: k.Tenant, Project: k.Project} }

func (k ProjectBudgetKey) args() []any {
	return []any{k.Tenant, k.Project, k.Period + "-01", k.Source, k.Currency, k.Basis}
}

const projectBudgetCondition = `(tenant_id,project_id,period,source,currency,basis)=($1,$2,$3::date,$4,$5,$6) AND account_id IS NULL`

type ProjectBudgetInput struct {
	BudgetInput
	Accounts []string `json:"accounts"`
}

func (input ProjectBudgetInput) normalizedAccounts() ([]string, bool) {
	if !input.BudgetInput.valid() || len(input.Accounts) == 0 || len(input.Accounts) > MaxProjectBudgetAccounts {
		return nil, false
	}
	accounts := slices.Clone(input.Accounts)
	slices.Sort(accounts)
	for i, account := range accounts {
		if !validID(account) || (i > 0 && accounts[i-1] == account) {
			return nil, false
		}
	}
	return accounts, true
}

type ProjectBudget struct {
	ID               string                 `json:"id"`
	Period           string                 `json:"period"`
	Source           string                 `json:"source"`
	Currency         string                 `json:"currency"`
	Basis            string                 `json:"basis"`
	Limit            string                 `json:"limit"`
	WarningPercent   int                    `json:"warning_percent"`
	Enabled          bool                   `json:"enabled"`
	Revision         string                 `json:"revision"`
	UpdatedAt        time.Time              `json:"updated_at"`
	State            string                 `json:"state"`
	Spent            *string                `json:"spent,omitempty"`
	ObservedAccounts int                    `json:"observed_accounts"`
	Accounts         []ProjectBudgetAccount `json:"accounts"`
}

type ProjectBudgetAccount struct {
	Account        string  `json:"account"`
	State          string  `json:"state"`
	ReportRevision string  `json:"report_revision,omitempty"`
	Amount         *string `json:"amount,omitempty"`
}

func (s Store) GetProjectBudget(ctx context.Context, token string, key ProjectBudgetKey) (ProjectBudget, error) {
	if !key.Valid() {
		return ProjectBudget{}, ErrInvalid
	}
	var budget ProjectBudget
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := authorizeBilling(ctx, tx, token, key.authKey()); err != nil {
			return err
		}
		var err error
		budget, err = readProjectBudget(ctx, tx, key)
		return err
	})
	return budget, err
}

func readProjectBudget(ctx context.Context, tx *sql.Tx, key ProjectBudgetKey) (ProjectBudget, error) {
	out := ProjectBudget{Period: key.Period, Source: key.Source, Currency: key.Currency, Basis: key.Basis}
	var coverage []byte
	err := tx.QueryRowContext(ctx, `SELECT budget_id,amount::text,warning_percent,enabled,revision::text,updated_at,
		state,spend::text,observed_accounts,accounts FROM cmp_billing_project_budget_status WHERE `+
		projectBudgetCondition, key.args()...).Scan(&out.ID, &out.Limit, &out.WarningPercent, &out.Enabled,
		&out.Revision, &out.UpdatedAt, &out.State, &out.Spent, &out.ObservedAccounts, &coverage)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectBudget{}, ErrNotFound
	}
	if err != nil {
		return ProjectBudget{}, err
	}
	if err := json.Unmarshal(coverage, &out.Accounts); err != nil {
		return ProjectBudget{}, err
	}
	return out, nil
}

func (s Store) SaveProjectBudget(ctx context.Context, token string, key ProjectBudgetKey, expected int64, input ProjectBudgetInput) (ProjectBudget, error) {
	accounts, valid := input.normalizedAccounts()
	if !key.Valid() || expected < 0 || !valid {
		return ProjectBudget{}, ErrInvalid
	}
	var budget ProjectBudget
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		if _, err := authorizeBilling(ctx, tx, token, key.authKey()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cmp_project_alert_checks SET next_at=LEAST(next_at,statement_timestamp())
			WHERE (tenant_id,project_id)=($1,$2)`, key.Tenant, key.Project); err != nil {
			return err
		}
		if err := checkProjectBudgetAccounts(ctx, tx, key, expected, accounts); err != nil {
			return err
		}
		actor, err := authorizeBilling(ctx, tx, token, key.authKey())
		if err != nil {
			return err
		}
		action, err := saveProjectBudget(ctx, tx, key, expected, input, accounts)
		if err != nil {
			return err
		}
		budget, err = readProjectBudget(ctx, tx, key)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
			(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,actor_id,revision,occurred_at)
			VALUES ($1,$2,'billing_budget',$3,$4,'billing-project-budget','',$5,$6::bigint,$7)`,
			actor.Tenant, actor.Project, action, budget.ID, actor.Principal, budget.Revision, budget.UpdatedAt)
		return err
	})
	return budget, err
}

func checkProjectBudgetAccounts(ctx context.Context, tx *sql.Tx, key ProjectBudgetKey, expected int64, accounts []string) error {
	newAccounts := accounts
	if expected > 0 {
		var raw []byte
		err := tx.QueryRowContext(ctx, "SELECT to_json(project_accounts) FROM cmp_billing_budgets WHERE "+
			projectBudgetCondition+" AND revision=$7 AND revision<9223372036854775807 FOR UPDATE",
			append(key.args(), expected)...).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBudgetStale
		}
		if err != nil {
			return err
		}
		var current []string
		if err := json.Unmarshal(raw, &current); err != nil {
			return err
		}
		newAccounts = slices.DeleteFunc(slices.Clone(accounts), func(account string) bool { return slices.Contains(current, account) })
	}
	if len(newAccounts) == 0 {
		return nil
	}
	var readable int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (
		SELECT account_id FROM cmp_cloud_accounts WHERE (tenant_id,project_id)=($1,$2) AND account_id=ANY($3::text[])
		AND mode IN ('read_only','write_enabled') AND provider IN ('tencent-cvm','alibaba-ecs','huawei-ecs')
		ORDER BY account_id FOR SHARE
	) AS owned`, key.Tenant, key.Project, newAccounts).Scan(&readable)
	if err == nil && readable != len(newAccounts) {
		return identity.ErrForbidden
	}
	return err
}

func saveProjectBudget(ctx context.Context, tx *sql.Tx, key ProjectBudgetKey, expected int64, input ProjectBudgetInput, accounts []string) (string, error) {
	args := append(key.args(), input.Limit, input.WarningPercent, *input.Enabled, accounts)
	if expected == 0 {
		result, err := tx.ExecContext(ctx, `INSERT INTO cmp_billing_budgets
			(tenant_id,project_id,period,source,currency,basis,amount,warning_percent,enabled,project_accounts)
			VALUES ($1,$2,$3::date,$4,$5,$6,$7::numeric,$8,$9,$10::text[]) ON CONFLICT DO NOTHING`, args...)
		if err != nil {
			return "", err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			if err == nil {
				err = ErrBudgetExists
			}
			return "", err
		}
		return "created", nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE cmp_billing_budgets
		SET amount=$7::numeric,warning_percent=$8,enabled=$9,project_accounts=$10::text[],
			revision=revision+1,updated_at=clock_timestamp() WHERE `+projectBudgetCondition, args...)
	return "updated", err
}
