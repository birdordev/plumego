package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
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
	Account           string                        `json:"account"`
	Provider          string                        `json:"provider"`
	Region            string                        `json:"region"`
	Kind              provider.Kind                 `json:"kind"`
	ID                string                        `json:"id"`
	ParentID          string                        `json:"parent_id,omitempty"`
	Name              string                        `json:"name"`
	Status            string                        `json:"status"`
	State             string                        `json:"inventory_state"`
	ObservedAt        time.Time                     `json:"observed_at"`
	Project           string                        `json:"project"`
	OwnershipSource   string                        `json:"ownership_source"`
	OwnershipRevision int64                         `json:"ownership_revision"`
	LastFullSync      *time.Time                    `json:"last_full_sync,omitempty"`
	PublicAddresses   []string                      `json:"public_addresses,omitempty"`
	LoadBalancer      *provider.LoadBalancerDetails `json:"load_balancer,omitempty"`
	Database          *provider.DatabaseDetails     `json:"database_instance,omitempty"`
	Bucket            *provider.BucketDetails       `json:"object_bucket,omitempty"`
	Cache             *provider.CacheDetails        `json:"cache_instance,omitempty"`
	Cluster           *provider.ClusterDetails      `json:"kubernetes_cluster,omitempty"`
	Snapshot          *provider.SnapshotDetails     `json:"snapshot,omitempty"`
	Volume            *provider.VolumeDetails       `json:"volume,omitempty"`
}

type Assets struct {
	Items []Asset `json:"items"`
	Next  string  `json:"next,omitempty"`
}

func validInventoryText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

type position struct {
	Filter  string
	Account string
	Region  string
	Kind    provider.Kind
	ID      string
}

const projectedInventoryState = `CASE WHEN o.missing_generation IS NOT NULL THEN 'missing_candidate'
	WHEN a.mode NOT IN ('read_only','write_enabled') OR a.credential_revision<>s.credential_revision THEN 'stale'
	WHEN s.kind IN ('security-group-rule','snapshot') AND a.credential_revision<>parent_scope.credential_revision THEN 'stale'
	ELSE 'observed' END`

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

const assetSearchTerm = `WITH search_term AS MATERIALIZED (SELECT lower($6) AS needle)`

// 每个作用域在完成可见性、搜索和游标筛选后，最多为全局页提供 limit+1 个候选。
// 外层按游标顺序读取作用域，使 PostgreSQL 可增量排序，在拼满一页后停止。
const listAssetScopeStart = `
	SELECT s.account_id,a.provider,s.region,s.kind,page.*
	FROM (
		SELECT scope_id,tenant_id,account_id,region,kind,credential_revision,completed_at
		FROM cmp_inventory_scopes
		WHERE tenant_id=$1 AND ($3='' OR account_id=$3) AND ($4='' OR region=$4) AND ($5='' OR kind=$5)
		  AND ($7='' OR (account_id,region,kind)>=($7,$8,$9))
		ORDER BY account_id,region,kind`

const listAssetScopeEnd = `
	) AS s
	CROSS JOIN LATERAL (
		SELECT provider,project_id,mode,credential_revision FROM cmp_cloud_accounts
		WHERE (tenant_id,account_id)=(s.tenant_id,s.account_id)
		LIMIT 1
	) AS a
	CROSS JOIN LATERAL (
		SELECT ` + assetListProjection + `
		FROM cmp_inventory_objects AS o` + inventoryOwnershipJoins + `
		WHERE o.scope_id=s.scope_id AND o.removed_at IS NULL AND ` + assetVisibility + `
		  AND ($7='' OR (s.account_id,s.region,s.kind)<>($7,$8,$9) OR o.external_id>$10)
		  AND ($6='' OR ` + assetSearchText

const assetListProjection = `
	o.external_id,COALESCE(parent.external_id,''),o.name,o.status,
	` + projectedInventoryState + `,o.observed_at,s.completed_at,` + projectedOwner + `,` + projectedRevision + `,
	CASE WHEN s.kind='security-group-rule' THEN 'security_group' WHEN s.kind='snapshot' THEN 'source_volume'
		ELSE ` + ownershipSource + ` END,
	o.public_addresses,o.snapshot,o.load_balancer,o.database_instance,o.object_bucket,o.cache_instance,o.kubernetes_cluster,o.volume_details`

// 无关联子查询复用已折叠的搜索词，同时保留数据库排序规则。
const foldedAssetSearch = `(SELECT needle FROM search_term)`

const assetSearchText = `strpos(lower(o.name),` + foldedAssetSearch + `)>0
	OR strpos(lower(o.external_id),` + foldedAssetSearch + `)>0
	OR (o.public_addresses<>'[]'::jsonb AND strpos(o.public_addresses::text,` + foldedAssetSearch + `)>0)`

const (
	searchAssetSnapshot = `strpos(lower(parent.external_id),` + foldedAssetSearch + `)>0`
	searchAssetVolume   = `strpos(lower(concat_ws(' ',o.volume_details->>'storage_class',o.volume_details->>'usage')),` + foldedAssetSearch + `)>0`
	searchAssetBalancer = `strpos(lower(COALESCE(o.load_balancer->>'dns_name','') || ' ' ||
		COALESCE(o.load_balancer->>'private_addresses','')),` + foldedAssetSearch + `)>0`
	searchAssetDatabase = `strpos(lower(concat_ws(' ',o.database_instance->>'engine',
		o.database_instance->>'version',o.database_instance->>'product')),` + foldedAssetSearch + `)>0`
	searchAssetBucket = `strpos(lower(concat_ws(' ',o.object_bucket->>'location',
		o.object_bucket->>'storage_class',o.object_bucket->>'product')),` + foldedAssetSearch + `)>0`
	searchAssetCache = `strpos(lower(concat_ws(' ',o.cache_instance->>'engine',
		o.cache_instance->>'version',o.cache_instance->>'product')),` + foldedAssetSearch + `)>0`
	searchAssetCluster = `strpos(lower(concat_ws(' ',o.kubernetes_cluster->>'cluster_type',
		o.kubernetes_cluster->>'version',o.kubernetes_cluster->>'product')),` + foldedAssetSearch + `)>0`
	searchAssetAll = `CASE s.kind
		WHEN 'snapshot' THEN ` + searchAssetSnapshot + `
		WHEN 'volume' THEN ` + searchAssetVolume + `
		WHEN 'load-balancer' THEN ` + searchAssetBalancer + `
		WHEN 'database-instance' THEN ` + searchAssetDatabase + `
		WHEN 'object-bucket' THEN ` + searchAssetBucket + `
		WHEN 'cache-instance' THEN ` + searchAssetCache + `
		WHEN 'kubernetes-cluster' THEN ` + searchAssetCluster + `
		ELSE false END`
)

const listAssetsPageEnd = `)
		ORDER BY o.external_id LIMIT $11
	) AS page
	ORDER BY s.account_id,s.region,s.kind,page.external_id LIMIT $11`

// 首个作用域无法填满一页时，批量筛选资产键，避免反复访问同一批堆页。
const listAssetsSearchStart = `), matches AS MATERIALIZED (
		SELECT o.scope_id,o.external_id
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
		WHERE s.tenant_id=$1 AND o.removed_at IS NULL
		  AND ($3='' OR s.account_id=$3) AND ($4='' OR s.region=$4) AND s.kind=$5
		  AND ($7='' OR (s.account_id,s.region,s.kind,o.external_id)>($7,$8,$9,$10))
		  AND (SELECT count(*) FROM first_scope)<$11
		  AND (` + assetSearchText

const listAssetsSearchEnd = `)
	), search_page AS (
		SELECT s.account_id,a.provider,s.region,s.kind,` + assetListProjection + `
		FROM matches
		JOIN cmp_inventory_scopes AS s USING (scope_id)
		JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		JOIN cmp_inventory_objects AS o USING (scope_id,external_id)` + inventoryOwnershipJoins + `
		WHERE ` + assetVisibility + `
		ORDER BY s.account_id,s.region,s.kind,o.external_id LIMIT $11
	)
	SELECT * FROM first_scope WHERE (SELECT count(*) FROM first_scope)>=$11
	UNION ALL SELECT * FROM search_page
	ORDER BY account_id,region,kind,external_id LIMIT $11`

func assetMetadataSearch(kind provider.Kind) string {
	switch kind {
	case "":
		return searchAssetAll
	case provider.Snapshot:
		return searchAssetSnapshot
	case provider.Volume:
		return searchAssetVolume
	case provider.LoadBalancer:
		return searchAssetBalancer
	case provider.DatabaseInstance:
		return searchAssetDatabase
	case provider.ObjectBucket:
		return searchAssetBucket
	case provider.CacheInstance:
		return searchAssetCache
	case provider.KubernetesCluster:
		return searchAssetCluster
	default:
		return ""
	}
}

func listAssets(ctx context.Context, tx *sql.Tx, q Query, p position, result *Assets) error {
	metadata := ""
	if fields := assetMetadataSearch(q.Kind); fields != "" {
		// 指定资源类型时只搜索其元数据字段，普通字段筛选可先于归属连接执行。
		metadata = ` OR ` + fields
	}
	query := assetSearchTerm + listAssetScopeStart + listAssetScopeEnd + metadata + listAssetsPageEnd
	if q.Search != "" && q.Account == "" && q.Kind != "" && q.Kind != provider.Snapshot {
		query = assetSearchTerm + `, first_scope AS MATERIALIZED (` + listAssetScopeStart + ` LIMIT 1` +
			listAssetScopeEnd + metadata + listAssetsPageEnd + listAssetsSearchStart + metadata + listAssetsSearchEnd
	}
	rows, err := tx.QueryContext(ctx, query,
		q.Tenant, q.Project, q.Account, q.Region, q.Kind, q.Search, p.Account, p.Region, p.Kind, p.ID, q.Limit+1)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a Asset
		var addresses, snapshot, loadBalancer, database, bucket, cache, cluster, volume []byte
		if err := rows.Scan(&a.Account, &a.Provider, &a.Region, &a.Kind, &a.ID, &a.ParentID, &a.Name, &a.Status, &a.State, &a.ObservedAt,
			&a.LastFullSync, &a.Project, &a.OwnershipRevision, &a.OwnershipSource, &addresses, &snapshot, &loadBalancer, &database, &bucket, &cache, &cluster, &volume); err != nil {
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
		if len(loadBalancer) != 0 {
			if err := json.Unmarshal(loadBalancer, &a.LoadBalancer); err != nil {
				return err
			}
		}
		if len(database) != 0 {
			if err := json.Unmarshal(database, &a.Database); err != nil {
				return err
			}
		}
		if len(bucket) != 0 {
			if err := json.Unmarshal(bucket, &a.Bucket); err != nil {
				return err
			}
		}
		if len(cache) != 0 {
			if err := json.Unmarshal(cache, &a.Cache); err != nil {
				return err
			}
		}
		if len(cluster) != 0 {
			if err := json.Unmarshal(cluster, &a.Cluster); err != nil {
				return err
			}
		}
		if len(volume) != 0 {
			if err := json.Unmarshal(volume, &a.Volume); err != nil {
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
