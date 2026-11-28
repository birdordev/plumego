package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/provider"
)

const DatabaseBackupsMigration = "0167_database_backups"

var ErrDatabaseBackupsNotFound = errors.New("database backup snapshot unavailable")
var ErrDatabaseBackupsChanged = errors.New("database backup snapshot changed")

func MigrateDatabaseBackups(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, DatabaseBackupsMigration)
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

func (s Store) RefreshDatabaseBackups(ctx context.Context, key Key, id string, revision int64, reader provider.DatabaseBackupReader) error {
	publish, err := s.PrepareDatabaseBackups(ctx, key, id, revision, reader)
	if err != nil {
		return err
	}
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error { return publish(ctx, tx) })
}

func (s Store) PrepareDatabaseBackups(ctx context.Context, key Key, id string, revision int64, reader provider.DatabaseBackupReader) (func(context.Context, *sql.Tx) error, error) {
	target, err := s.databaseObservation(ctx, key, id, revision)
	if err != nil {
		return nil, err
	}
	if !provider.ValidDatabaseBackupTarget(target.scope.Provider, id, target.details) {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	end := target.started.UTC().Truncate(time.Minute)
	start := end.Add(-provider.DatabaseBackupWindow)
	backups, err := reader.ReadDatabaseBackups(ctx, target.scope, id, target.details, end)
	if err != nil {
		return nil, err
	}
	if err := provider.NormalizeDatabaseBackups(backups); err != nil {
		return nil, err
	}
	data, err := json.Marshal(backups)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := target.lock(ctx, tx); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO cmp_database_backups AS saved
			(scope_id,external_id,product,engine,credential_revision,refresh_started_at,observed_at,window_start,window_end,backups)
			VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp(),$7,$8,$9::jsonb)
			ON CONFLICT (scope_id,external_id) DO UPDATE SET product=EXCLUDED.product,engine=EXCLUDED.engine,
				credential_revision=EXCLUDED.credential_revision,refresh_started_at=EXCLUDED.refresh_started_at,observed_at=EXCLUDED.observed_at,
				window_start=EXCLUDED.window_start,window_end=EXCLUDED.window_end,backups=EXCLUDED.backups
			WHERE (saved.credential_revision,saved.refresh_started_at) < (EXCLUDED.credential_revision,EXCLUDED.refresh_started_at)`,
			target.scopeID, id, target.details.Product, target.details.Engine, revision, target.started, start, end, string(data))
		return checkDatabaseObservationWrite(result, err)
	}, nil
}

func (s Store) ReadDatabaseBackups(ctx context.Context, token string, q DatabaseBackupQuery) (DatabaseBackupPage, error) {
	if !(DatabaseAssetQuery{q.Tenant, q.Project, q.Account, q.Region, q.ID}).valid() ||
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
				AND snapshot.engine=o.database_instance->>'engine'`+ownedObservedDatabase,
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
