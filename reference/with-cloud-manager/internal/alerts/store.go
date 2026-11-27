package alerts

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

var ErrInvalid = errors.New("invalid alert query or identifier")
var ErrStale = errors.New("alert acknowledgement requires the current open revision")
var alertID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

const DefaultLimit = 50

type Alert struct {
	ID             string       `json:"id"`
	Rule           string       `json:"rule"`
	SubjectID      string       `json:"subject_id"`
	ResourceKind   string       `json:"resource_kind"`
	Account        string       `json:"account"`
	Region         string       `json:"region,omitempty"`
	SourceStatus   string       `json:"source_status"`
	Status         string       `json:"status"`
	OpenedAt       time.Time    `json:"opened_at"`
	AcknowledgedAt *time.Time   `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string       `json:"acknowledged_by,omitempty"`
	ResolvedAt     *time.Time   `json:"resolved_at,omitempty"`
	Revision       int64        `json:"revision"`
	BudgetScope    *BudgetScope `json:"budget_scope,omitempty"`
}

type BudgetScope struct {
	Period   string `json:"period"`
	Source   string `json:"source"`
	Currency string `json:"currency"`
	Basis    string `json:"basis"`
}

type Query struct {
	Status string
	Rule   string
	After  string
	Limit  int
}

type Page struct {
	Items []Alert `json:"items"`
	Next  string  `json:"next,omitempty"`
}

type Store struct {
	db       *sql.DB
	evaluate ProjectEvaluation
}

func NewStore(db *sql.DB) Store { return Store{db: db} }

func authorize(ctx context.Context, tx *sql.Tx, token string, scope identity.Scope, permission identity.Permission) (identity.Scope, bool, error) {
	store := identity.NewStore(tx)
	actor, err := store.AuthenticateRequest(ctx, token)
	if err != nil {
		return identity.Scope{}, false, err
	}
	if actor.Tenant != scope.Tenant {
		return identity.Scope{}, false, identity.ErrForbidden
	}
	actor.Project = scope.Project
	member, err := store.AuthorizeProject(ctx, actor, permission)
	return actor, member.Role == "admin", err
}

const alertFields = `alert_id::text,rule,subject_id,resource_kind,account_id,region,source_status,
	CASE WHEN resolved_at IS NOT NULL THEN 'resolved' WHEN acknowledged_at IS NOT NULL THEN 'acknowledged' ELSE 'open' END,
	opened_at,acknowledged_at,COALESCE(acknowledged_by,''),resolved_at,revision`

func scanAlert(row interface{ Scan(...any) error }, a *Alert) error {
	return row.Scan(&a.ID, &a.Rule, &a.SubjectID, &a.ResourceKind, &a.Account, &a.Region, &a.SourceStatus,
		&a.Status, &a.OpenedAt, &a.AcknowledgedAt, &a.AcknowledgedBy, &a.ResolvedAt, &a.Revision)
}

func alertStatusCondition(status string) (string, error) {
	switch status {
	case "", "active":
		return " AND resolved_at IS NULL", nil
	case "open":
		return " AND resolved_at IS NULL AND acknowledged_at IS NULL", nil
	case "acknowledged":
		return " AND resolved_at IS NULL AND acknowledged_at IS NOT NULL", nil
	case "resolved":
		return " AND resolved_at IS NOT NULL", nil
	case "all":
		return "", nil
	default:
		return "", ErrInvalid
	}
}

func validAlertRule(rule string) bool {
	switch rule {
	case "", "operation_review", "operation_delayed", "scan_failures", "scan_stale", "billing_budget_warning", "billing_budget_exceeded":
		return true
	default:
		return false
	}
}

func visibleAlert(adminParameter string) string {
	return "(" + adminParameter + " OR rule NOT IN ('billing_budget_warning','billing_budget_exceeded'))"
}

func (s Store) List(ctx context.Context, token string, scope identity.Scope, q Query) (Page, error) {
	condition, err := alertStatusCondition(q.Status)
	if err != nil || !validAlertRule(q.Rule) || (q.After != "" && !alertID.MatchString(q.After)) || q.Limit < 1 || q.Limit > 100 {
		return Page{}, ErrInvalid
	}
	page := Page{Items: make([]Alert, 0)}
	err = dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) (resultErr error) {
		actor, admin, err := authorize(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		statement := "SELECT " + alertFields + " FROM cmp_project_alerts WHERE (tenant_id,project_id)=($1,$2)" + condition
		params := []any{actor.Tenant, actor.Project, q.Limit + 1, q.Rule, admin}
		statement += " AND ($4='' OR rule=$4) AND " + visibleAlert("$5")
		if q.After != "" {
			var anchor time.Time
			err := tx.QueryRowContext(ctx, `SELECT opened_at FROM cmp_project_alerts
				WHERE (tenant_id,project_id,alert_id)=($1,$2,$3::uuid) AND `+visibleAlert("$4"), actor.Tenant, actor.Project, q.After, admin).Scan(&anchor)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInvalid
			}
			if err != nil {
				return err
			}
			statement += " AND (opened_at,alert_id)<($6,$7::uuid)"
			params = append(params, anchor, q.After)
		}
		rows, err := tx.QueryContext(ctx, statement+" ORDER BY opened_at DESC,alert_id DESC LIMIT $3", params...)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
		for rows.Next() {
			if len(page.Items) == q.Limit {
				page.Next = page.Items[q.Limit-1].ID
				break
			}
			var item Alert
			if err := scanAlert(rows, &item); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func (s Store) Get(ctx context.Context, token string, scope identity.Scope, id string) (Alert, error) {
	if !alertID.MatchString(id) {
		return Alert{}, ErrInvalid
	}
	var alert Alert
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, admin, err := authorize(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		err = scanAlert(tx.QueryRowContext(ctx, "SELECT "+alertFields+` FROM cmp_project_alerts
			WHERE (tenant_id,project_id,alert_id)=($1,$2,$3::uuid) AND `+visibleAlert("$4"), actor.Tenant, actor.Project, id, admin), &alert)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		return readBudgetScope(ctx, tx, actor, &alert)
	})
	if err != nil {
		return Alert{}, err
	}
	return alert, nil
}

func (s Store) Acknowledge(ctx context.Context, token string, scope identity.Scope, id string, revision int64) (Alert, error) {
	if !alertID.MatchString(id) || revision < 1 {
		return Alert{}, ErrInvalid
	}
	var alert Alert
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, _, err := authorize(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		err = scanAlert(tx.QueryRowContext(ctx, `UPDATE cmp_project_alerts
			SET acknowledged_at=GREATEST(clock_timestamp(),opened_at),acknowledged_by=$5,revision=revision+1
			WHERE (tenant_id,project_id,alert_id,revision)=($1,$2,$3::uuid,$4)
			  AND acknowledged_at IS NULL AND resolved_at IS NULL RETURNING `+alertFields,
			actor.Tenant, actor.Project, id, revision, actor.Principal), &alert)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		return readBudgetScope(ctx, tx, actor, &alert)
	})
	if err != nil {
		return Alert{}, err
	}
	return alert, nil
}

func readBudgetScope(ctx context.Context, tx *sql.Tx, actor identity.Scope, alert *Alert) error {
	if alert.Rule != "billing_budget_warning" && alert.Rule != "billing_budget_exceeded" {
		return nil
	}
	var scope BudgetScope
	err := tx.QueryRowContext(ctx, `SELECT to_char(period,'YYYY-MM'),source,currency,basis FROM cmp_billing_budgets
		WHERE (tenant_id,project_id,budget_id)=($1,$2,$3::uuid)`, actor.Tenant, actor.Project, alert.SubjectID).
		Scan(&scope.Period, &scope.Source, &scope.Currency, &scope.Basis)
	if err == nil {
		alert.BudgetScope = &scope
	}
	return err
}
