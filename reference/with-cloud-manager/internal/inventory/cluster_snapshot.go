package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"with-cloud-manager/internal/provider"
)

type ClusterSnapshotQuery struct {
	Tenant, Project, Account, Region, ID string
	Offset, Limit                        int
	Snapshot                             string
}

type ClusterSnapshotInfo struct {
	Snapshot   string    `json:"snapshot"`
	ObservedAt time.Time `json:"observed_at"`
	Total      int       `json:"total"`
	NextOffset int       `json:"next_offset,omitempty"`
}

type clusterSnapshotSource struct {
	table, column     string
	maxRows, maxBytes int
	notFound, changed error
}

var clusterSnapshotID = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

const currentCluster = `s.kind='kubernetes-cluster' AND o.kubernetes_cluster IS NOT NULL
	AND o.removed_at IS NULL AND o.missing_generation IS NULL
	AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision
	AND a.remote_identity IS NOT NULL AND a.remote_identity<>''`

const selectCluster = ` FROM cmp_inventory_objects AS o
	JOIN cmp_inventory_scopes AS s USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE (s.tenant_id,s.account_id,s.region,o.external_id)=($1,$2,$3,$4)
	AND a.credential_revision=$5 AND ` + currentCluster

func validClusterScope(tenant, account, region, id string) bool {
	return validConfigurationQueryValue(tenant, 256) && validConfigurationQueryValue(account, 256) &&
		validConfigurationQueryValue(region, 128) && validConfigurationQueryValue(id, 128)
}

type clusterParent struct {
	scopeID            int64
	workloadGeneration int64
	scope              provider.Scope
	seen, started      time.Time
	args               []any
}

func (s Store) currentClusterParent(ctx context.Context, key Key, id string, revision int64) (clusterParent, error) {
	if key.Kind != provider.KubernetesCluster || revision <= 0 || !validClusterScope(key.Tenant, key.Account, key.Region, id) {
		return clusterParent{}, ErrInvalidQuery
	}
	scope := provider.Scope{Kind: key.Kind, Region: key.Region}
	var scopeID, workloadGeneration int64
	var parentSeen, started time.Time
	args := []any{key.Tenant, key.Account, key.Region, id, revision}
	err := s.db.QueryRowContext(ctx, "SELECT s.scope_id,a.provider,a.remote_identity,o.observed_at,clock_timestamp(),o.workload_change_generation"+selectCluster, args...).
		Scan(&scopeID, &scope.Provider, &scope.RemoteIdentity, &parentSeen, &started, &workloadGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return clusterParent{}, ErrStale
	}
	if err != nil {
		return clusterParent{}, err
	}
	return clusterParent{scopeID: scopeID, scope: scope, seen: parentSeen, started: started, workloadGeneration: workloadGeneration, args: args}, nil
}

func (p clusterParent) lock(ctx context.Context, tx *sql.Tx) error {
	var current int64
	err := tx.QueryRowContext(ctx, "SELECT s.scope_id"+selectCluster+
		" AND s.scope_id=$6 AND o.observed_at=$7 AND a.remote_identity=$8 AND o.workload_change_generation=$9 FOR SHARE OF a,s,o",
		append(p.args, p.scopeID, p.seen, p.scope.RemoteIdentity, p.workloadGeneration)...).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	return err
}

// 只在事务外读取集群，在发布时重新校验父集群和凭据代次。
func prepareClusterSnapshot[T any](ctx context.Context, s Store, key Key, id string, revision int64, source clusterSnapshotSource,
	read func(context.Context, provider.Scope, string) ([]T, error), normalize func([]T) error,
) (func(context.Context, *sql.Tx) error, error) {
	parent, err := s.currentClusterParent(ctx, key, id, revision)
	if err != nil {
		return nil, err
	}
	items, err := read(ctx, parent.scope, id)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeClusterItems(items, source.maxBytes, normalize)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := parent.lock(ctx, tx); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO `+source.table+` AS saved
			(scope_id,external_id,credential_revision,refresh_started_at,observed_at,`+source.column+`)
			VALUES ($1,$2,$3,$4,clock_timestamp(),$5::jsonb)
			ON CONFLICT (scope_id,external_id) DO UPDATE SET credential_revision=EXCLUDED.credential_revision,
				refresh_started_at=EXCLUDED.refresh_started_at,observed_at=EXCLUDED.observed_at,snapshot_id=EXCLUDED.snapshot_id,
				`+source.column+`=EXCLUDED.`+source.column+`
			WHERE (saved.credential_revision,saved.refresh_started_at)<(EXCLUDED.credential_revision,EXCLUDED.refresh_started_at)`,
			parent.scopeID, id, revision, parent.started, encoded)
		return requireClusterWrite(result, err)
	}, nil
}

func encodeClusterItems[T any](items []T, maxBytes int, normalize func([]T) error) (string, error) {
	if err := normalize(items); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxBytes {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return string(encoded), nil
}

func requireClusterWrite(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return ErrStale
	}
	return err
}

func readClusterSnapshot[T any](ctx context.Context, s Store, token string, q ClusterSnapshotQuery, source clusterSnapshotSource) (ClusterSnapshotInfo, []T, error) {
	if !validClusterScope(q.Tenant, q.Account, q.Region, q.ID) || !validConfigurationQueryValue(q.Project, 256) ||
		q.Offset < 0 || q.Offset > source.maxRows || q.Limit < 1 || q.Limit > 100 ||
		((q.Offset > 0 || q.Snapshot != "") && !clusterSnapshotID.MatchString(q.Snapshot)) {
		return ClusterSnapshotInfo{}, nil, ErrInvalidQuery
	}
	var result ClusterSnapshotInfo
	var items []T
	err := s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		var page []byte
		err := tx.QueryRowContext(ctx, `SELECT snapshot.snapshot_id::text,snapshot.observed_at,jsonb_array_length(snapshot.`+source.column+`),
			COALESCE((SELECT jsonb_agg(item.value ORDER BY item.position)
				FROM jsonb_array_elements(snapshot.`+source.column+`) WITH ORDINALITY AS item(value,position)
				WHERE item.position > $6::integer AND item.position <= $6::integer+$7::integer),'[]'::jsonb)
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			JOIN cmp_inventory_objects AS o USING (scope_id)
			JOIN `+source.table+` AS snapshot ON (snapshot.scope_id,snapshot.external_id)=(s.scope_id,o.external_id)
				AND snapshot.credential_revision=a.credential_revision`+inventoryOwnershipJoins+
			` WHERE `+assetVisibility+` AND `+currentCluster+` AND (s.account_id,s.region,o.external_id)=($3,$4,$5)`,
			q.Tenant, q.Project, q.Account, q.Region, q.ID, q.Offset, q.Limit).Scan(&result.Snapshot, &result.ObservedAt, &result.Total, &page)
		if errors.Is(err, sql.ErrNoRows) {
			return source.notFound
		}
		if err != nil {
			return err
		}
		if q.Snapshot != "" && q.Snapshot != result.Snapshot {
			return source.changed
		}
		if q.Offset > result.Total {
			return ErrInvalidQuery
		}
		return json.Unmarshal(page, &items)
	})
	if err != nil {
		return ClusterSnapshotInfo{}, nil, err
	}
	if end := q.Offset + len(items); end < result.Total {
		result.NextOffset = end
	}
	return result, items, nil
}
