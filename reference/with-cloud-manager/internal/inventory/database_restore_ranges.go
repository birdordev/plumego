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

const DatabaseRestoreWindowsMigration = "0168_database_restore_windows"

var ErrDatabaseRestoreWindowsNotFound = errors.New("database restore window snapshot unavailable")

func MigrateDatabaseRestoreWindows(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, DatabaseRestoreWindowsMigration)
}

type DatabaseRestoreWindows struct {
	ObservedAt time.Time                        `json:"observed_at"`
	Scope      string                           `json:"scope"`
	Windows    []provider.DatabaseRestoreWindow `json:"windows"`
}

func (s Store) RefreshDatabaseRestoreWindows(ctx context.Context, key Key, id string, revision int64, reader provider.DatabaseRestoreWindowReader) error {
	publish, err := s.PrepareDatabaseRestoreWindows(ctx, key, id, revision, reader)
	if err != nil {
		return err
	}
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error { return publish(ctx, tx) })
}

func (s Store) PrepareDatabaseRestoreWindows(ctx context.Context, key Key, id string, revision int64, reader provider.DatabaseRestoreWindowReader) (func(context.Context, *sql.Tx) error, error) {
	target, err := s.databaseObservation(ctx, key, id, revision)
	if err != nil {
		return nil, err
	}
	recoveryScope := provider.DatabaseRestoreWindowScope(target.scope.Provider, id, target.details)
	if recoveryScope == "" {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	windows, err := reader.ReadDatabaseRestoreWindows(ctx, target.scope, id, target.details)
	if err != nil {
		return nil, err
	}
	if err := provider.NormalizeDatabaseRestoreWindows(windows); err != nil {
		return nil, err
	}
	data, err := json.Marshal(windows)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, tx *sql.Tx) error {
		if err := target.lock(ctx, tx); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO cmp_database_restore_windows AS saved
			(scope_id,external_id,product,engine,credential_revision,refresh_started_at,observed_at,recovery_scope,windows)
			VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp(),$7,$8::jsonb)
			ON CONFLICT (scope_id,external_id) DO UPDATE SET product=EXCLUDED.product,engine=EXCLUDED.engine,
				credential_revision=EXCLUDED.credential_revision,refresh_started_at=EXCLUDED.refresh_started_at,observed_at=EXCLUDED.observed_at,
				recovery_scope=EXCLUDED.recovery_scope,windows=EXCLUDED.windows
			WHERE (saved.credential_revision,saved.refresh_started_at) < (EXCLUDED.credential_revision,EXCLUDED.refresh_started_at)`,
			target.scopeID, id, target.details.Product, target.details.Engine, revision, target.started, recoveryScope, string(data))
		return checkDatabaseObservationWrite(result, err)
	}, nil
}

func (s Store) ReadDatabaseRestoreWindows(ctx context.Context, token string, q DatabaseAssetQuery) (DatabaseRestoreWindows, error) {
	if !q.valid() {
		return DatabaseRestoreWindows{}, ErrInvalidQuery
	}
	var result DatabaseRestoreWindows
	err := s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		var data []byte
		err := tx.QueryRowContext(ctx, `SELECT snapshot.observed_at,snapshot.recovery_scope,snapshot.windows
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			JOIN cmp_inventory_objects AS o USING (scope_id)
			JOIN cmp_database_restore_windows AS snapshot ON (snapshot.scope_id,snapshot.external_id)=(o.scope_id,o.external_id)
				AND snapshot.credential_revision=a.credential_revision AND snapshot.product=o.database_instance->>'product'
				AND snapshot.engine=o.database_instance->>'engine'`+ownedObservedDatabase,
			q.Tenant, q.Project, q.Account, q.Region, q.ID).Scan(&result.ObservedAt, &result.Scope, &data)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDatabaseRestoreWindowsNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &result.Windows)
	})
	if err != nil {
		return DatabaseRestoreWindows{}, err
	}
	return result, nil
}
