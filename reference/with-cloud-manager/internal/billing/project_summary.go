package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/migrations"
)

type ProjectSummaryQuery struct {
	Tenant, Project, From, Through, Source, Currency, Provider string
	MissingOnly                                                bool
	After                                                      string
	Limit                                                      int
}

func (q ProjectSummaryQuery) Valid() bool {
	from, ok := billingMonth(q.From)
	if !ok {
		return false
	}
	through, ok := billingMonth(q.Through)
	if !ok || through.Before(from) || !through.Before(from.AddDate(0, 12, 0)) {
		return false
	}
	if q.After != "" {
		month, account, found := strings.Cut(q.After, "/")
		after, ok := billingMonth(month)
		if !found || !ok || after.Before(from) || after.After(through) || !validID(account) {
			return false
		}
	}
	return validID(q.Tenant) && validID(q.Project) && (q.Source == Native || q.Source == Focus) &&
		currencyCode.MatchString(q.Currency) && (q.Provider == "" || focusProvider(q.Provider) != "") &&
		q.Limit >= 1 && q.Limit <= 100
}

func billingMonth(raw string) (time.Time, bool) {
	value, err := time.Parse("2006-01", raw)
	return value, err == nil && value.Year() >= 2000 && value.Format("2006-01") == raw
}

type ProjectSummary struct {
	AsOf     time.Time              `json:"as_of"`
	From     string                 `json:"from"`
	Through  string                 `json:"through"`
	Source   string                 `json:"source"`
	Currency string                 `json:"currency"`
	Provider string                 `json:"provider,omitempty"`
	Months   []ProjectMonthSummary  `json:"months"`
	Accounts []ProjectReportAccount `json:"accounts"`
	Next     string                 `json:"next,omitempty"`
}

type ProjectMonthSummary struct {
	Period           string `json:"period"`
	EligibleAccounts int64  `json:"eligible_accounts"`
	Published        int64  `json:"published_accounts"`
	WithCurrency     int64  `json:"currency_accounts"`
	Total            *Total `json:"total,omitempty"`
}

type ProjectReportAccount struct {
	Period   string                  `json:"period"`
	Account  string                  `json:"account"`
	Provider string                  `json:"provider"`
	Report   *ProjectReportReference `json:"report,omitempty"`
	Billed   *string                 `json:"billed_cost,omitempty"`
}

type ProjectReportReference struct {
	Revision string `json:"revision"`
	Digest   string `json:"sha256"`
	Rows     int    `json:"rows"`
}

func (s Store) ReadProjectSummary(ctx context.Context, token string, q ProjectSummaryQuery) (ProjectSummary, error) {
	if !q.Valid() {
		return ProjectSummary{}, ErrInvalid
	}
	result := ProjectSummary{From: q.From, Through: q.Through, Source: q.Source, Currency: q.Currency, Provider: q.Provider}
	afterPeriod, afterAccount, _ := strings.Cut(q.After, "/")
	if afterPeriod != "" {
		afterPeriod += "-01"
	}
	err := dbutil.WithTransaction(ctx, s.db, readOnly(), func(tx *sql.Tx) error {
		if _, err := authorizeBilling(ctx, tx, token, Key{Tenant: q.Tenant, Project: q.Project}); err != nil {
			return err
		}
		var months, accounts []byte
		err := tx.QueryRowContext(ctx, migrations.ProjectSummary(), q.Tenant, q.Project, q.From+"-01", q.Through+"-01",
			q.Source, q.Currency, q.Provider, afterPeriod, afterAccount, q.MissingOnly, q.Limit+1).
			Scan(&result.AsOf, &months, &accounts)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(months, &result.Months); err != nil {
			return err
		}
		return json.Unmarshal(accounts, &result.Accounts)
	})
	if err != nil {
		return ProjectSummary{}, err
	}
	if len(result.Accounts) > q.Limit {
		result.Accounts = result.Accounts[:q.Limit]
		last := result.Accounts[q.Limit-1]
		result.Next = last.Period + "/" + last.Account
	}
	return result, nil
}
