package inventory

import (
	"context"
	"database/sql"
	"encoding/json"

	"with-cloud-manager/internal/provider"
)

type Topology struct {
	Nodes []TopologyNode `json:"nodes"`
	Next  string         `json:"next,omitempty"`
}

type TopologyNode struct {
	Asset Asset          `json:"asset"`
	Links []TopologyLink `json:"links"`
}

type TopologyLink struct {
	provider.Link
	Name string `json:"name"`
}

type assetKey struct {
	Account string
	Region  string
	Kind    provider.Kind
	ID      string
}

func (s Store) Topology(ctx context.Context, token string, q Query) (Topology, error) {
	if q.Limit > 50 {
		return Topology{}, ErrInvalidQuery
	}
	start, err := queryPosition(q)
	if err != nil {
		return Topology{}, err
	}
	result := Topology{Nodes: make([]TopologyNode, 0, q.Limit)}
	err = s.readProject(ctx, token, q, func(tx *sql.Tx) error {
		var page Assets
		if err := listAssets(ctx, tx, q, start, &page); err != nil {
			return err
		}
		result.Next = page.Next
		index := make(map[assetKey]int, len(page.Items))
		keys := make([]assetKey, 0, len(page.Items))
		for _, a := range page.Items {
			key := assetKey{a.Account, a.Region, a.Kind, a.ID}
			index[key] = len(result.Nodes)
			keys = append(keys, key)
			result.Nodes = append(result.Nodes, TopologyNode{Asset: a, Links: []TopologyLink{}})
		}
		if len(keys) == 0 {
			return nil
		}
		return topologyLinks(ctx, tx, q, keys, index, result.Nodes)
	})
	if err != nil {
		return Topology{}, err
	}
	return result, nil
}

func topologyLinks(ctx context.Context, tx *sql.Tx, q Query, keys []assetKey, index map[assetKey]int, nodes []TopologyNode) error {
	input, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT src.account_id,src.region,src.kind,source.external_id,
		    s.kind,o.external_id,ref.relation,o.name
		FROM jsonb_to_recordset($3::jsonb) AS k("Account" text,"Region" text,"Kind" text,"ID" text)
		JOIN cmp_inventory_scopes AS src ON src.tenant_id=$1 AND (src.account_id,src.region,src.kind)=(k."Account",k."Region",k."Kind")
		JOIN cmp_inventory_objects AS source ON (source.scope_id,source.external_id)=(src.scope_id,k."ID")
		CROSS JOIN LATERAL jsonb_to_recordset(source.links) AS ref(kind text,id text,relation text)
		JOIN cmp_inventory_scopes AS s ON (s.tenant_id,s.account_id,s.region,s.kind)=(src.tenant_id,src.account_id,src.region,ref.kind)
		JOIN cmp_inventory_objects AS o ON (o.scope_id,o.external_id)=(s.scope_id,ref.id)
		JOIN cmp_cloud_accounts AS a ON (a.tenant_id,a.account_id)=(s.tenant_id,s.account_id)`+inventoryOwnershipJoins+`
		WHERE o.removed_at IS NULL AND `+assetVisibility+`
		ORDER BY src.account_id,src.region,src.kind,source.external_id,s.kind,o.external_id,ref.relation,o.name`, q.Tenant, q.Project, string(input))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key assetKey
		var link TopologyLink
		if err := rows.Scan(&key.Account, &key.Region, &key.Kind, &key.ID,
			&link.Kind, &link.ID, &link.Relation, &link.Name); err != nil {
			return err
		}
		i := index[key]
		nodes[i].Links = append(nodes[i].Links, link)
	}
	return rows.Err()
}
