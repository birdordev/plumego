package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0022_operation_queries.sql
var operationQuerySchema string

var ErrInvalidQuery = errors.New("invalid operation query")

const DefaultOperationPageSize = 50

type Query struct {
	Account string
	Region  string
	Kind    provider.Kind
	Action  string
	Status  string
	Cursor  string
	Limit   int
}

type OperationPage struct {
	Items []Operation `json:"items"`
	Next  string      `json:"next,omitempty"`
}

type operationPosition struct {
	Filter string    `json:"filter"`
	Time   time.Time `json:"time"`
	ID     string    `json:"id"`
}

func queryPosition(scope identity.Scope, q Query) (operationPosition, error) {
	if q.Limit < 1 || q.Limit > 100 || len(q.Cursor) > 1024 ||
		(q.Account != "" && !operationKey.MatchString(q.Account)) ||
		(q.Region != "" && !operationKey.MatchString(q.Region)) ||
		(q.Action != "" && !operationKey.MatchString(q.Action)) {
		return operationPosition{}, ErrInvalidQuery
	}
	switch q.Status {
	case "", "awaiting_approval", "queued", "applying", "observing", "succeeded", "failed", "needs_review":
	default:
		return operationPosition{}, ErrInvalidQuery
	}
	switch q.Kind {
	case "", provider.Instance, provider.Network, provider.Subnet, provider.SecurityGroup, provider.SecurityGroupRule, provider.ElasticIP, provider.Snapshot, provider.Volume, provider.Image, provider.LoadBalancer, provider.DNSZone, provider.KubernetesCluster, provider.DatabaseInstance, provider.CacheInstance:
	default:
		return operationPosition{}, ErrInvalidQuery
	}
	keys, err := json.Marshal([7]string{scope.Tenant, scope.Project, q.Account, q.Region, string(q.Kind), q.Action, q.Status})
	if err != nil {
		return operationPosition{}, err
	}
	hash := sha256.Sum256(keys)
	filter := base64.RawURLEncoding.EncodeToString(hash[:])
	if q.Cursor == "" {
		return operationPosition{Filter: filter}, nil
	}
	var p operationPosition
	raw, err := base64.RawURLEncoding.Strict().DecodeString(q.Cursor)
	if err != nil || json.Unmarshal(raw, &p) != nil || p.Filter != filter || p.Time.IsZero() || !operationKey.MatchString(p.ID) {
		return operationPosition{}, ErrInvalidQuery
	}
	return p, nil
}

func (s Store) List(ctx context.Context, token string, scope identity.Scope, q Query) (OperationPage, error) {
	p, err := queryPosition(scope, q)
	if err != nil {
		return OperationPage{}, err
	}
	page := OperationPage{Items: make([]Operation, 0, q.Limit)}
	err = dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		return listOperations(ctx, tx, actor, q, p, &page)
	})
	if err != nil {
		return OperationPage{}, err
	}
	return page, nil
}

func listOperations(ctx context.Context, tx *sql.Tx, actor identity.Scope, q Query, p operationPosition, page *OperationPage) (retErr error) {
	var statement strings.Builder
	statement.WriteString("SELECT " + operationFields + " FROM cmp_instance_operations WHERE (tenant_id,project_id)=($1,$2)")
	args := []any{actor.Tenant, actor.Project}
	param := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	for _, filter := range []struct{ column, value string }{
		{"account_id", q.Account}, {"region", q.Region}, {"resource_kind", string(q.Kind)}, {"action", q.Action}, {"status", q.Status},
	} {
		if filter.value != "" {
			statement.WriteString(" AND " + filter.column + "=" + param(filter.value))
		}
	}
	if p.ID != "" {
		statement.WriteString(" AND (created_at,operation_id)<(" + param(p.Time) + "," + param(p.ID) + ")")
	}
	statement.WriteString(" ORDER BY created_at DESC,operation_id DESC LIMIT " + param(q.Limit+1))
	rows, err := tx.QueryContext(ctx, statement.String(), args...)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	for rows.Next() {
		if len(page.Items) == q.Limit {
			last := page.Items[q.Limit-1]
			encoded, err := json.Marshal(operationPosition{Filter: p.Filter, Time: last.CreatedAt.UTC(), ID: last.ID})
			if err != nil {
				return err
			}
			page.Next = base64.RawURLEncoding.EncodeToString(encoded)
			break
		}
		var op Operation
		if err := scanOperation(rows, &op); err != nil {
			return err
		}
		page.Items = append(page.Items, op)
	}
	return rows.Err()
}
