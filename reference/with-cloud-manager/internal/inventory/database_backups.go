package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/provider"
)

//go:embed 0167_database_backups.sql
var databaseBackupsSchema string

const DatabaseBackupsMigration = "0167_database_backups"

var ErrDatabaseBackupsNotFound = errors.New("database backup snapshot unavailable")
var ErrDatabaseBackupsChanged = errors.New("database backup snapshot changed")

func MigrateDatabaseBackups(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, DatabaseBackupsMigration, databaseBackupsSchema)
}

type DatabaseBackupQuery struct {
	Tenant, Project, Account, Region, ID string
	ObservedAt                           time.Time
	Offset, Limit                        int
}

type DatabaseBackupPage struct {
	ObservedAt time.Time                 `json:"observed_at"`
	Start      time.Time                 `json:"window_start"`
	End        time.Time                 `json:"window_end"`
	Backups    []provider.DatabaseBackup `json:"backups"`
	Total      int                       `json:"total"`
	NextOffset int                       `json:"next_offset,omitempty"`
}

const currentBackupDatabase = `s.kind='database-instance' AND o.database_instance IS NOT NULL
	AND o.removed_at IS NULL AND o.missing_generation IS NULL
	AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision
	AND a.remote_identity<>''`

const selectBackupDatabase = ` FROM cmp_inventory_objects AS o
	JOIN cmp_inventory_scopes AS s USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE (s.tenant_id,s.account_id,s.region,o.external_id,a.credential_revision)=($1,$2,$3,$4,$5) AND ` + currentBackupDatabase

func (s Store) RefreshDatabaseBackups(ctx context.Context, key Key, id string, revision int64, reader provider.DatabaseBackupReader) error {
	if key.Kind != provider.DatabaseInstance || revision < 1 || !validConfigurationQueryValue(key.Tenant, 256) ||
		!validConfigurationQueryValue(key.Account, 256) || !validConfigurationQueryValue(key.Region, 128) || !validConfigurationQueryValue(id, 128) {
		return ErrInvalidQuery
	}
	var scopeID int64
	var parentObserved, started time.Time
	var details provider.DatabaseDetails
	scope := provider.Scope{Kind: key.Kind, Region: key.Region}
	args := []any{key.Tenant, key.Account, key.Region, id, revision}
	err := s.db.QueryRowContext(ctx, `SELECT s.scope_id,a.provider,a.remote_identity,o.database_instance->>'product',
		o.database_instance->>'engine',o.observed_at,clock_timestamp()`+selectBackupDatabase, args...).
		Scan(&scopeID, &scope.Provider, &scope.RemoteIdentity, &details.Product, &details.Engine, &parentObserved, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	if err != nil {
		return err
	}
	if !provider.ValidDatabaseBackupTarget(scope.Provider, id, details) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	end := started.UTC().Truncate(time.Minute)
	start := end.Add(-provider.DatabaseBackupWindow)
	backups, err := reader.ReadDatabaseBackups(ctx, scope, id, details, end)
	if err != nil {
		return err
	}
	if err := provider.NormalizeDatabaseBackups(backups); err != nil {
		return err
	}
	data, err := json.Marshal(backups)
	if err != nil {
		return err
	}
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var found int64
		err := tx.QueryRowContext(ctx, "SELECT s.scope_id"+selectBackupDatabase+
			` AND s.scope_id=$6 AND o.observed_at=$7 AND a.remote_identity=$8 AND o.database_instance->>'product'=$9
			AND o.database_instance->>'engine'=$10 FOR SHARE OF a,s,o`,
			append(args, scopeID, parentObserved, scope.RemoteIdentity, details.Product, details.Engine)...).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO cmp_database_backups AS saved
			(scope_id,external_id,product,engine,credential_revision,refresh_started_at,observed_at,window_start,window_end,backups)
			VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp(),$7,$8,$9::jsonb)
			ON CONFLICT (scope_id,external_id) DO UPDATE SET product=EXCLUDED.product,engine=EXCLUDED.engine,
				credential_revision=EXCLUDED.credential_revision,refresh_started_at=EXCLUDED.refresh_started_at,observed_at=EXCLUDED.observed_at,
				window_start=EXCLUDED.window_start,window_end=EXCLUDED.window_end,backups=EXCLUDED.backups
			WHERE (saved.credential_revision,saved.refresh_started_at) < (EXCLUDED.credential_revision,EXCLUDED.refresh_started_at)`,
			scopeID, id, details.Product, details.Engine, revision, started, start, end, string(data))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err == nil && count != 1 {
			return ErrStale
		}
		return err
	})
}

func (s Store) ReadDatabaseBackups(ctx context.Context, token string, q DatabaseBackupQuery) (DatabaseBackupPage, error) {
	if !validConfigurationQueryValue(q.Tenant, 256) || !validConfigurationQueryValue(q.Project, 256) ||
		!validConfigurationQueryValue(q.Account, 256) || !validConfigurationQueryValue(q.Region, 128) || !validConfigurationQueryValue(q.ID, 128) ||
		q.Offset < 0 || q.Offset > provider.MaxDatabaseBackups || q.Limit < 1 || q.Limit > 100 || (q.Offset > 0 && q.ObservedAt.IsZero()) {
		return DatabaseBackupPage{}, ErrInvalidQuery
	}
	var result DatabaseBackupPage
	err := s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		var records []byte
		err := tx.QueryRowContext(ctx, `SELECT snapshot.observed_at,snapshot.window_start,snapshot.window_end,jsonb_array_length(snapshot.backups),
			COALESCE((SELECT jsonb_agg(backup.value ORDER BY backup.position)
				FROM jsonb_array_elements(snapshot.backups) WITH ORDINALITY AS backup(value,position)
				WHERE backup.position>$6::int AND backup.position<=$6::int+$7::int),'[]'::jsonb)
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			JOIN cmp_inventory_objects AS o USING (scope_id)
			JOIN cmp_database_backups AS snapshot ON (snapshot.scope_id,snapshot.external_id)=(o.scope_id,o.external_id)
				AND snapshot.credential_revision=a.credential_revision AND snapshot.product=o.database_instance->>'product'
				AND snapshot.engine=o.database_instance->>'engine'`+inventoryOwnershipJoins+
			" WHERE "+assetVisibility+" AND "+currentBackupDatabase+" AND (s.account_id,s.region,o.external_id)=($3,$4,$5)",
			q.Tenant, q.Project, q.Account, q.Region, q.ID, q.Offset, q.Limit).
			Scan(&result.ObservedAt, &result.Start, &result.End, &result.Total, &records)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDatabaseBackupsNotFound
		}
		if err != nil {
			return err
		}
		if !q.ObservedAt.IsZero() && !q.ObservedAt.Equal(result.ObservedAt) {
			return ErrDatabaseBackupsChanged
		}
		return json.Unmarshal(records, &result.Backups)
	})
	if err != nil {
		return DatabaseBackupPage{}, err
	}
	if end := q.Offset + len(result.Backups); end < result.Total {
		result.NextOffset = end
	}
	return result, nil
}
