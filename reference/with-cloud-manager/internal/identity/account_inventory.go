package identity

import (
	"context"
	"database/sql"
	"errors"
)

const DefaultAccountPageLimit = 50

type AccountPageQuery struct {
	After string
	Limit int
}

func (q AccountPageQuery) valid() bool {
	return q.Limit >= 1 && q.Limit <= 100 && (q.After == "" || accountIdentifier.MatchString(q.After))
}

type AccountPage[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

type AccountGrant struct {
	Project    string `json:"project"`
	AllowWrite bool   `json:"allow_write"`
}

func (a AccountAdmin) List(ctx context.Context, token string, scope Scope, q AccountPageQuery) (AccountPage[Account], error) {
	if !q.valid() {
		return AccountPage[Account]{}, ErrAccountInvalid
	}
	page := AccountPage[Account]{Items: make([]Account, 0, q.Limit)}
	err := a.project(ctx, token, scope, false, func(tx *sql.Tx, actor Scope) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+accountFields+`
			FROM cmp_cloud_accounts WHERE (tenant_id,project_id)=($1,$2) AND account_id>$3
			ORDER BY account_id LIMIT $4`, actor.Tenant, actor.Project, q.After, q.Limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if len(page.Items) == q.Limit {
				page.Next = page.Items[q.Limit-1].ID
				break
			}
			item, err := scanAccount(rows)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	return page, err
}

func (a AccountAdmin) ListGrants(ctx context.Context, token string, scope Scope, id string, q AccountPageQuery) (AccountPage[AccountGrant], error) {
	if !q.valid() || !accountIdentifier.MatchString(id) {
		return AccountPage[AccountGrant]{}, ErrAccountInvalid
	}
	page := AccountPage[AccountGrant]{Items: make([]AccountGrant, 0, q.Limit)}
	err := a.project(ctx, token, scope, false, func(tx *sql.Tx, actor Scope) error {
		var exists bool
		err := tx.QueryRowContext(ctx, `SELECT true FROM cmp_cloud_accounts
			WHERE (tenant_id,project_id,account_id)=($1,$2,$3)`, actor.Tenant, actor.Project, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT g.project_id,g.allow_write
			FROM cmp_cloud_account_grants AS g JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			WHERE (a.tenant_id,a.project_id,a.account_id)=($1,$2,$3) AND g.project_id>$4
			ORDER BY g.project_id LIMIT $5`, actor.Tenant, actor.Project, id, q.After, q.Limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			if len(page.Items) == q.Limit {
				page.Next = page.Items[q.Limit-1].Project
				break
			}
			var item AccountGrant
			if err := rows.Scan(&item.Project, &item.AllowWrite); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	return page, err
}
