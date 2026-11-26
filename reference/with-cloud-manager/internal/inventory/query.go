package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

var ErrInvalidQuery = errors.New("invalid inventory query")

type Query struct {
	Tenant, Project string
	Account, Region string
	Kind            provider.Kind
	Search, Cursor  string
	Limit           int
}

type Asset struct {
	Account           string                    `json:"account"`
	Provider          string                    `json:"provider"`
	Region            string                    `json:"region"`
	Kind              provider.Kind             `json:"kind"`
	ID                string                    `json:"id"`
	ParentID          string                    `json:"parent_id,omitempty"`
	Name              string                    `json:"name"`
	Status            string                    `json:"status"`
	State             string                    `json:"inventory_state"`
	ObservedAt        time.Time                 `json:"observed_at"`
	Project           string                    `json:"project"`
	OwnershipSource   string                    `json:"ownership_source"`
	OwnershipRevision int64                     `json:"ownership_revision"`
	LastFullSync      *time.Time                `json:"last_full_sync,omitempty"`
	PublicAddresses   []string                  `json:"public_addresses,omitempty"`
	Snapshot          *provider.SnapshotDetails `json:"snapshot,omitempty"`
}

type Assets struct {
	Items []Asset `json:"items"`
	Next  string  `json:"next,omitempty"`
}

type position struct {
	Filter  string
	Account string
	Region  string
	Kind    provider.Kind
	ID      string
}

func queryPosition(q Query) (position, error) {
	if q.Limit < 1 || q.Limit > 200 ||
		len(q.Tenant) > 256 || len(q.Project) > 256 || len(q.Account) > 256 ||
		len(q.Region) > 256 || len(q.Search) > 256 || !utf8.ValidString(q.Search) ||
		(q.Kind != "" && !q.Kind.Valid()) || len(q.Cursor) > 4096 {
		return position{}, ErrInvalidQuery
	}
	filter, err := json.Marshal([6]string{q.Tenant, q.Project, q.Account, q.Region, string(q.Kind), q.Search})
	if err != nil {
		return position{}, err
	}
	hash := sha256.Sum256(filter)
	expected := base64.RawURLEncoding.EncodeToString(hash[:])
	if q.Cursor == "" {
		return position{Filter: expected}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	var p position
	if err != nil || json.Unmarshal(data, &p) != nil || p.Filter != expected ||
		p.Account == "" || len(p.Account) > 256 || len(p.Region) > 256 ||
		!p.Kind.Valid() || p.ID == "" || len(p.ID) > provider.MaxObjectIDSize {
		return position{}, ErrInvalidQuery
	}
	return p, nil
}

func (s Store) List(ctx context.Context, token string, q Query) (Assets, error) {
	p, err := queryPosition(q)
	if err != nil {
		return Assets{}, err
	}
	result := Assets{Items: make([]Asset, 0, q.Limit)}
	err = s.readProject(ctx, token, q, func(tx *sql.Tx) error {
		return listAssets(ctx, tx, q, p, &result)
	})
	if err != nil {
		return Assets{}, err
	}
	return result, nil
}

func (s Store) readProject(ctx context.Context, token string, q Query, fn func(*sql.Tx) error) error {
	return dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		auth := identity.NewStore(tx)
		scope, err := auth.AuthenticateRequest(ctx, token)
		if err != nil {
			return err
		}
		if scope.Tenant != q.Tenant {
			return identity.ErrForbidden
		}
		scope.Project = q.Project
		if _, err := auth.AuthorizeProject(ctx, scope, identity.View); err != nil {
			return err
		}
		return fn(tx)
	})
}

func listAssets(ctx context.Context, tx *sql.Tx, q Query, p position, result *Assets) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.account_id,a.provider,s.region,s.kind,o.external_id,COALESCE(parent.external_id,''),o.name,o.status,
		CASE WHEN o.missing_generation IS NOT NULL THEN 'missing_candidate'
		     WHEN a.mode NOT IN ('read_only','write_enabled') OR a.credential_revision<>s.credential_revision THEN 'stale'
		     WHEN s.kind IN ('security-group-rule','snapshot') AND a.credential_revision<>parent_scope.credential_revision THEN 'stale'
		     ELSE 'observed' END,o.observed_at,s.completed_at,`+projectedOwner+`,`+projectedRevision+`,
		CASE WHEN s.kind='security-group-rule' THEN 'security_group' WHEN s.kind='snapshot' THEN 'source_volume'
		ELSE `+ownershipSource+` END,o.public_addresses,o.snapshot
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id)`+inventoryOwnershipJoins+`
		WHERE `+assetVisibility+`
		AND o.removed_at IS NULL
		AND ($3='' OR s.account_id=$3) AND ($4='' OR s.region=$4) AND ($5='' OR s.kind=$5)
		AND ($6='' OR strpos(lower(o.name),lower($6))>0 OR strpos(lower(o.external_id),lower($6))>0
		     OR (o.public_addresses<>'[]'::jsonb AND strpos(o.public_addresses::text,lower($6))>0)
		     OR (s.kind='snapshot' AND strpos(lower(parent.external_id),lower($6))>0))
		AND ($7='' OR (s.account_id,s.region,s.kind,o.external_id)>($7,$8,$9,$10))
		ORDER BY s.account_id,s.region,s.kind,o.external_id LIMIT $11`,
		q.Tenant, q.Project, q.Account, q.Region, q.Kind, q.Search, p.Account, p.Region, p.Kind, p.ID, q.Limit+1)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a Asset
		var addresses, snapshot []byte
		if err := rows.Scan(&a.Account, &a.Provider, &a.Region, &a.Kind, &a.ID, &a.ParentID, &a.Name, &a.Status, &a.State, &a.ObservedAt,
			&a.LastFullSync, &a.Project, &a.OwnershipRevision, &a.OwnershipSource, &addresses, &snapshot); err != nil {
			return err
		}
		if err := json.Unmarshal(addresses, &a.PublicAddresses); err != nil {
			return err
		}
		if len(snapshot) != 0 {
			if err := json.Unmarshal(snapshot, &a.Snapshot); err != nil {
				return err
			}
		}
		if len(result.Items) == q.Limit {
			last := result.Items[len(result.Items)-1]
			next, err := json.Marshal(position{p.Filter, last.Account, last.Region, last.Kind, last.ID})
			if err != nil {
				return err
			}
			result.Next = base64.RawURLEncoding.EncodeToString(next)
			break
		}
		result.Items = append(result.Items, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}
