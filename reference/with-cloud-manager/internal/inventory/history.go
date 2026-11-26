package inventory

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"time"

	"with-cloud-manager/internal/provider"
)

type Change struct {
	Account                 string                        `json:"account"`
	Region                  string                        `json:"region"`
	Kind                    string                        `json:"kind"`
	AssetID                 string                        `json:"asset_id"`
	Action                  string                        `json:"action"`
	PreviousName            *string                       `json:"previous_name,omitempty"`
	PreviousStatus          *string                       `json:"previous_status,omitempty"`
	Name                    string                        `json:"name"`
	Status                  string                        `json:"status"`
	OccurredAt              time.Time                     `json:"occurred_at"`
	PreviousPublicAddresses []string                      `json:"previous_public_addresses,omitempty"`
	PublicAddresses         []string                      `json:"public_addresses,omitempty"`
	PreviousLoadBalancer    *provider.LoadBalancerDetails `json:"previous_load_balancer,omitempty"`
	LoadBalancer            *provider.LoadBalancerDetails `json:"load_balancer,omitempty"`
	PreviousDatabase        *provider.DatabaseDetails     `json:"previous_database_instance,omitempty"`
	Database                *provider.DatabaseDetails     `json:"database_instance,omitempty"`
	PreviousBucket          *provider.BucketDetails       `json:"previous_object_bucket,omitempty"`
	Bucket                  *provider.BucketDetails       `json:"object_bucket,omitempty"`
	PreviousCache           *provider.CacheDetails        `json:"previous_cache_instance,omitempty"`
	Cache                   *provider.CacheDetails        `json:"cache_instance,omitempty"`
	PreviousCluster         *provider.ClusterDetails      `json:"previous_kubernetes_cluster,omitempty"`
	Cluster                 *provider.ClusterDetails      `json:"kubernetes_cluster,omitempty"`
	PreviousSnapshot        *provider.SnapshotDetails     `json:"previous_snapshot,omitempty"`
	Snapshot                *provider.SnapshotDetails     `json:"snapshot,omitempty"`
}

type Changes struct {
	Items []Change `json:"items"`
	Next  string   `json:"next,omitempty"`
}

type historyPosition struct {
	Filter string
	Time   time.Time
	ID     string
}

var eventID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (s Store) History(ctx context.Context, token string, q Query) (Changes, error) {
	cursor := q.Cursor
	q.Cursor = ""
	key, err := queryPosition(q)
	if err != nil {
		return Changes{}, err
	}
	position := historyPosition{Filter: key.Filter}
	if cursor != "" {
		if len(cursor) > 1024 {
			return Changes{}, ErrInvalidQuery
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &position) != nil || position.Filter != key.Filter ||
			position.Time.IsZero() || !eventID.MatchString(position.ID) {
			return Changes{}, ErrInvalidQuery
		}
	}
	result := Changes{Items: make([]Change, 0, q.Limit)}
	err = s.readProject(ctx, token, q, func(tx *sql.Tx) error {
		return listHistory(ctx, tx, q, position, &result)
	})
	if err != nil {
		return Changes{}, err
	}
	return result, nil
}

func listHistory(ctx context.Context, tx *sql.Tx, q Query, cursor historyPosition, result *Changes) error {
	var stamp, event any
	if cursor.ID != "" {
		stamp, event = cursor.Time, cursor.ID
	}
	rows, err := tx.QueryContext(ctx, `SELECT h.event_id::text,s.account_id,s.region,s.kind,h.external_id,
		h.action,h.previous_name,h.previous_status,h.name,h.status,h.occurred_at,h.previous_public_addresses,h.public_addresses,
		h.previous_snapshot,h.snapshot,h.previous_load_balancer,h.load_balancer,h.previous_database_instance,h.database_instance,h.previous_object_bucket,h.object_bucket,
		h.previous_cache_instance,h.cache_instance,h.previous_kubernetes_cluster,h.kubernetes_cluster
		FROM cmp_inventory_history AS h JOIN cmp_inventory_scopes AS s USING (scope_id)
		JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		LEFT JOIN cmp_inventory_objects AS o ON (o.scope_id,o.external_id)=(h.scope_id,h.external_id)`+inventoryOwnershipJoins+`
		WHERE `+assetVisibility+`
		AND ($3='' OR s.account_id=$3) AND ($4='' OR s.region=$4) AND ($5='' OR s.kind=$5)
		AND ($6='' OR strpos(lower(h.name),lower($6))>0 OR strpos(lower(h.external_id),lower($6))>0
		     OR (h.public_addresses<>'[]'::jsonb AND strpos(h.public_addresses::text,lower($6))>0)
		     OR (h.previous_public_addresses<>'[]'::jsonb AND strpos(h.previous_public_addresses::text,lower($6))>0)
		     OR (s.kind='load-balancer' AND strpos(lower(COALESCE(h.load_balancer->>'dns_name','') || ' ' ||
		         COALESCE(h.load_balancer->>'private_addresses','') || ' ' ||
		         COALESCE(h.previous_load_balancer->>'dns_name','') || ' ' ||
		         COALESCE(h.previous_load_balancer->>'private_addresses','')),lower($6))>0)
		     OR (s.kind='database-instance' AND strpos(lower(concat_ws(' ',h.database_instance->>'engine',
		         h.database_instance->>'version',h.database_instance->>'product',h.previous_database_instance->>'engine',
		         h.previous_database_instance->>'version',h.previous_database_instance->>'product')),lower($6))>0)
		     OR (s.kind='object-bucket' AND strpos(lower(concat_ws(' ',h.object_bucket->>'location',h.object_bucket->>'storage_class',
		         h.object_bucket->>'product',h.previous_object_bucket->>'location',
		         h.previous_object_bucket->>'storage_class',h.previous_object_bucket->>'product')),lower($6))>0)
		     OR (s.kind='cache-instance' AND strpos(lower(concat_ws(' ',h.cache_instance->>'engine',
		         h.cache_instance->>'version',h.cache_instance->>'product',h.previous_cache_instance->>'engine',
		         h.previous_cache_instance->>'version',h.previous_cache_instance->>'product')),lower($6))>0)
		     OR (s.kind='kubernetes-cluster' AND strpos(lower(concat_ws(' ',h.kubernetes_cluster->>'cluster_type',
		         h.kubernetes_cluster->>'version',h.kubernetes_cluster->>'product',h.previous_kubernetes_cluster->>'cluster_type',
		         h.previous_kubernetes_cluster->>'version',h.previous_kubernetes_cluster->>'product')),lower($6))>0))
		AND ($7::timestamptz IS NULL OR (h.occurred_at,h.event_id)<($7,$8::uuid))
		ORDER BY h.occurred_at DESC,h.event_id DESC LIMIT $9`,
		q.Tenant, q.Project, q.Account, q.Region, q.Kind, q.Search, stamp, event, q.Limit+1)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	last := cursor
	for rows.Next() {
		if len(result.Items) == q.Limit {
			raw, err := json.Marshal(last)
			if err != nil {
				return err
			}
			result.Next = base64.RawURLEncoding.EncodeToString(raw)
			break
		}
		var item Change
		var previous, addresses, previousSnapshot, snapshot, previousLB, loadBalancer, previousDatabase, database, previousBucket, bucket, previousCache, cache, previousCluster, cluster []byte
		if err := rows.Scan(&last.ID, &item.Account, &item.Region, &item.Kind, &item.AssetID,
			&item.Action, &item.PreviousName, &item.PreviousStatus, &item.Name, &item.Status, &item.OccurredAt, &previous, &addresses,
			&previousSnapshot, &snapshot, &previousLB, &loadBalancer, &previousDatabase, &database, &previousBucket, &bucket, &previousCache, &cache, &previousCluster, &cluster); err != nil {
			return err
		}
		if len(previous) != 0 {
			if err := json.Unmarshal(previous, &item.PreviousPublicAddresses); err != nil {
				return err
			}
		}
		if err := json.Unmarshal(addresses, &item.PublicAddresses); err != nil {
			return err
		}
		if len(previousSnapshot) != 0 {
			if err := json.Unmarshal(previousSnapshot, &item.PreviousSnapshot); err != nil {
				return err
			}
		}
		if len(snapshot) != 0 {
			if err := json.Unmarshal(snapshot, &item.Snapshot); err != nil {
				return err
			}
		}
		last.Time = item.OccurredAt
		if len(previousLB) != 0 {
			if err := json.Unmarshal(previousLB, &item.PreviousLoadBalancer); err != nil {
				return err
			}
		}
		if len(loadBalancer) != 0 {
			if err := json.Unmarshal(loadBalancer, &item.LoadBalancer); err != nil {
				return err
			}
		}
		if len(previousDatabase) != 0 {
			if err := json.Unmarshal(previousDatabase, &item.PreviousDatabase); err != nil {
				return err
			}
		}
		if len(database) != 0 {
			if err := json.Unmarshal(database, &item.Database); err != nil {
				return err
			}
		}
		if len(previousBucket) != 0 {
			if err := json.Unmarshal(previousBucket, &item.PreviousBucket); err != nil {
				return err
			}
		}
		if len(bucket) != 0 {
			if err := json.Unmarshal(bucket, &item.Bucket); err != nil {
				return err
			}
		}
		if len(previousCache) != 0 {
			if err := json.Unmarshal(previousCache, &item.PreviousCache); err != nil {
				return err
			}
		}
		if len(cache) != 0 {
			if err := json.Unmarshal(cache, &item.Cache); err != nil {
				return err
			}
		}
		if len(previousCluster) != 0 {
			if err := json.Unmarshal(previousCluster, &item.PreviousCluster); err != nil {
				return err
			}
		}
		if len(cluster) != 0 {
			if err := json.Unmarshal(cluster, &item.Cluster); err != nil {
				return err
			}
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}
