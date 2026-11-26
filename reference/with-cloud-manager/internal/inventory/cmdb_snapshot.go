package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

const cmdbPageSize = 256

type CMDBAsset struct {
	Account           string          `json:"account"`
	Region            string          `json:"region"`
	Kind              provider.Kind   `json:"kind"`
	ID                string          `json:"id"`
	Provider          string          `json:"provider"`
	Name              string          `json:"name"`
	Status            string          `json:"status"`
	State             string          `json:"inventory_state"`
	OwnerProject      string          `json:"owner_project"`
	OwnershipRevision int64           `json:"ownership_revision,string"`
	Links             []provider.Link `json:"links"`
}

type cmdbPosition struct {
	scopeID int64
	id      string
}

type cmdbViewer struct {
	principal string
	revision  int64
}

func (s Store) StreamCMDB(ctx context.Context, token string, scope identity.Scope, start func(time.Time) error,
	write func(CMDBAsset) error) error {
	if token == "" {
		return identity.ErrUnauthenticated
	}
	q := Query{Tenant: scope.Tenant, Project: scope.Project}
	var viewer cmdbViewer
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		var err error
		viewer, err = readCMDBViewer(ctx, tx, token, scope)
		if err != nil {
			return err
		}
		var captured time.Time
		if err := tx.QueryRowContext(ctx, "SELECT transaction_timestamp()").Scan(&captured); err != nil {
			return err
		}
		if err := start(captured); err != nil || write == nil {
			return err
		}
		position := cmdbPosition{}
		for {
			items, next, err := cmdbSnapshotPage(ctx, tx, q, position)
			if err != nil {
				return err
			}
			if err := cmdbSnapshotLinks(ctx, tx, q, items); err != nil {
				return err
			}
			for _, item := range items {
				if err := write(item); err != nil {
					return err
				}
			}
			if len(items) < cmdbPageSize {
				return nil
			}
			position = next
		}
	})
	if err != nil || write == nil {
		return err
	}
	// 读取事务结束后以当前权限复核，拒绝为已撤权的读操作写入完整性尾帧。
	current, err := readCMDBViewer(ctx, s.db, token, scope)
	if err == nil && current != viewer {
		return identity.ErrForbidden
	}
	return err
}

func readCMDBViewer(ctx context.Context, db identity.Querier, token string, scope identity.Scope) (cmdbViewer, error) {
	auth := identity.NewStore(db)
	actor, err := auth.AuthenticateService(ctx, token)
	if err != nil {
		return cmdbViewer{}, err
	}
	if actor.Tenant != scope.Tenant {
		return cmdbViewer{}, identity.ErrForbidden
	}
	actor.Project = scope.Project
	member, err := auth.AuthorizeProject(ctx, actor, identity.View)
	return cmdbViewer{principal: actor.Principal, revision: member.Revision}, err
}

func cmdbSnapshotPage(ctx context.Context, tx *sql.Tx, q Query, start cmdbPosition) (items []CMDBAsset, next cmdbPosition, retErr error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.scope_id,s.account_id,s.region,s.kind,o.external_id,a.provider,o.name,o.status,
		`+projectedInventoryState+`,`+projectedOwner+`,`+projectedRevision+`
		FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id)`+inventoryOwnershipJoins+`
		WHERE `+assetVisibility+` AND o.removed_at IS NULL
		AND (o.scope_id,o.external_id)>($3,$4)
		ORDER BY o.scope_id,o.external_id LIMIT $5`, q.Tenant, q.Project, start.scopeID, start.id, cmdbPageSize)
	if err != nil {
		return nil, next, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	items = make([]CMDBAsset, 0, cmdbPageSize)
	for rows.Next() {
		var item CMDBAsset
		if err := rows.Scan(&next.scopeID, &item.Account, &item.Region, &item.Kind, &item.ID, &item.Provider,
			&item.Name, &item.Status, &item.State, &item.OwnerProject, &item.OwnershipRevision); err != nil {
			return nil, next, err
		}
		next.id = item.ID
		items = append(items, item)
	}
	return items, next, rows.Err()
}

func cmdbSnapshotLinks(ctx context.Context, tx *sql.Tx, q Query, items []CMDBAsset) error {
	if len(items) == 0 {
		return nil
	}
	keys := make([]assetKey, len(items))
	index := make(map[assetKey]int, len(items))
	nodes := make([]TopologyNode, len(items))
	for i, item := range items {
		key := assetKey{item.Account, item.Region, item.Kind, item.ID}
		keys[i], index[key] = key, i
	}
	if err := topologyLinks(ctx, tx, q, keys, index, nodes); err != nil {
		return err
	}
	for i, node := range nodes {
		items[i].Links = make([]provider.Link, len(node.Links))
		for j, link := range node.Links {
			items[i].Links[j] = link.Link
		}
	}
	return nil
}
