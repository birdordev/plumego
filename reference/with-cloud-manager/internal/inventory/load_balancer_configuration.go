package inventory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

var ErrLoadBalancerConfigurationNotFound = errors.New("load balancer configuration unavailable")

type LoadBalancerConfigurationQuery struct {
	Tenant, Project, Account, Region, ID string
}

type LoadBalancerConfiguration struct {
	ObservedAt time.Time `json:"observed_at"`
	provider.LoadBalancerConfiguration
}

const currentLoadBalancer = `s.kind='load-balancer' AND o.load_balancer IS NOT NULL
	AND o.removed_at IS NULL AND o.missing_generation IS NULL
	AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision
	AND a.remote_identity IS NOT NULL AND a.remote_identity<>''`

const selectLoadBalancer = ` FROM cmp_inventory_objects AS o
	JOIN cmp_inventory_scopes AS s USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE (s.tenant_id,s.account_id,s.region,o.external_id)=($1,$2,$3,$4)
	AND a.credential_revision=$5 AND ` + currentLoadBalancer

func migrateLoadBalancerConfiguration(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0058_load_balancer_configuration"); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0063_load_balancer_backend_keys")
}

func (s Store) RefreshLoadBalancerConfiguration(ctx context.Context, key Key, request provider.LoadBalancerConfigurationRequest, revision int64, adapter provider.LoadBalancerConfigurationReader) error {
	id := request.ID
	if key.Kind != provider.LoadBalancer || !validLoadBalancerConfigurationScope(key.Tenant, key.Account, key.Region, id) || revision <= 0 {
		return ErrInvalidQuery
	}
	var scope provider.Scope
	scope.Kind, scope.Region = key.Kind, key.Region
	var scopeID int64
	var parentObserved, started time.Time
	args := []any{key.Tenant, key.Account, key.Region, id, revision}
	err := s.db.QueryRowContext(ctx, "SELECT s.scope_id,a.provider,a.remote_identity,o.observed_at,clock_timestamp()"+selectLoadBalancer, args...).
		Scan(&scopeID, &scope.Provider, &scope.RemoteIdentity, &parentObserved, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStale
	}
	if err != nil {
		return err
	}

	request.BackendSecret = nil
	if request.IncludeBackends {
		secret, err := s.loadBalancerBackendSecret(ctx, scopeID, id)
		if err != nil {
			return err
		}
		request.BackendSecret = secret
	}
	configuration, err := adapter.ReadLoadBalancerConfiguration(ctx, scope, request)
	if err != nil {
		return err
	}
	if (configuration.Backends != nil) != request.IncludeBackends {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if err := configuration.Normalize(); err != nil {
		return err
	}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return err
	}

	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var found int64
		err := tx.QueryRowContext(ctx, "SELECT s.scope_id"+selectLoadBalancer+
			" AND s.scope_id=$6 AND o.observed_at=$7 AND a.remote_identity=$8 FOR SHARE OF a,s,o",
			append(args, scopeID, parentObserved, scope.RemoteIdentity)...).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO cmp_load_balancer_configurations AS saved
			(scope_id,external_id,credential_revision,refresh_started_at,observed_at,configuration)
			SELECT $1::bigint,$2::text,$3::bigint,$4::timestamptz,clock_timestamp(),$5::jsonb WHERE NOT EXISTS(
    SELECT 1 FROM cmp_load_balancer_backend_keys WHERE (scope_id,external_id)=($1,$2) AND change_observed_at>=$4)
   ON CONFLICT (scope_id,external_id) DO UPDATE SET credential_revision=EXCLUDED.credential_revision,
				refresh_started_at=EXCLUDED.refresh_started_at,observed_at=EXCLUDED.observed_at,configuration=EXCLUDED.configuration
			WHERE saved.credential_revision<EXCLUDED.credential_revision
			   OR (saved.credential_revision=EXCLUDED.credential_revision AND saved.refresh_started_at<EXCLUDED.refresh_started_at)`,
			scopeID, id, revision, started, string(encoded))
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err == nil && updated != 1 {
			return ErrStale
		}
		return err
	})
}

func (s Store) loadBalancerBackendSecret(ctx context.Context, scopeID int64, id string) ([]byte, error) {
	var secret []byte
	const query = `SELECT secret FROM cmp_load_balancer_backend_keys WHERE (scope_id,external_id)=($1,$2)`
	err := s.db.QueryRowContext(ctx, query, scopeID, id).Scan(&secret)
	if !errors.Is(err, sql.ErrNoRows) {
		return secret, err
	}
	generated := make([]byte, 32)
	if _, err := rand.Read(generated); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO cmp_load_balancer_backend_keys(scope_id,external_id,secret)
  VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, scopeID, id, generated); err != nil {
		return nil, err
	}
	err = s.db.QueryRowContext(ctx, query, scopeID, id).Scan(&secret)
	return secret, err
}

func (s Store) ReadLoadBalancerConfiguration(ctx context.Context, token string, q LoadBalancerConfigurationQuery) (LoadBalancerConfiguration, error) {
	if !validLoadBalancerConfigurationScope(q.Tenant, q.Account, q.Region, q.ID) ||
		!validConfigurationQueryValue(q.Project, 256) {
		return LoadBalancerConfiguration{}, ErrInvalidQuery
	}
	var result LoadBalancerConfiguration
	err := s.readProject(ctx, token, Query{Tenant: q.Tenant, Project: q.Project}, func(tx *sql.Tx) error {
		var configuration []byte
		err := tx.QueryRowContext(ctx, `SELECT config.observed_at,config.configuration
			FROM cmp_inventory_scopes AS s JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
			JOIN cmp_inventory_objects AS o USING (scope_id)
			JOIN cmp_load_balancer_configurations AS config ON (config.scope_id,config.external_id)=(s.scope_id,o.external_id)
				AND config.credential_revision=a.credential_revision`+inventoryOwnershipJoins+`
			WHERE `+assetVisibility+` AND `+currentLoadBalancer+`
			AND (s.account_id,s.region,o.external_id)=($3,$4,$5)`,
			q.Tenant, q.Project, q.Account, q.Region, q.ID).Scan(&result.ObservedAt, &configuration)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLoadBalancerConfigurationNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(configuration, &result.LoadBalancerConfiguration)
	})
	if err != nil {
		return LoadBalancerConfiguration{}, err
	}
	return result, nil
}

func validLoadBalancerConfigurationScope(tenant, account, region, id string) bool {
	return validConfigurationQueryValue(tenant, 256) && validConfigurationQueryValue(account, 256) &&
		validConfigurationQueryValue(region, 256) && validConfigurationQueryValue(id, provider.MaxObjectIDSize)
}

func validConfigurationQueryValue(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func loadBalancerRenameComponent(rename provider.LoadBalancerRename) (section, id string, requireExclusive bool) {
	if rename.TargetGroupID != "" {
		return "target_groups", rename.TargetGroupID, true
	}
	return "listeners", rename.ListenerID, false
}

func LoadBalancerComponentName(ctx context.Context, tx *sql.Tx, target MutationTarget, rename provider.LoadBalancerRename) (string, string, error) {
	section, id, exclusive := loadBalancerRenameComponent(rename)
	var name, product string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(item->>'name',''),
        CASE WHEN o.load_balancer->>'product'='tencent-clb'
              AND c.configuration->'target_groups' @> '[{"kind":"default"}]'::jsonb
             THEN 'tencent-classic-clb' ELSE o.load_balancer->>'product' END
  FROM cmp_load_balancer_configurations AS c JOIN cmp_inventory_objects AS o USING(scope_id,external_id)
  CROSS JOIN LATERAL jsonb_array_elements(c.configuration->$5::text) AS component(item)
  WHERE (c.scope_id,c.external_id,c.credential_revision)=($1,$2,$3)
    AND item->>'id'=$4 AND o.load_balancer->>'product' IS NOT NULL
      AND (NOT $6::boolean OR item->'exclusive'='true'::jsonb)`,
		target.ScopeID, rename.ID, target.CredentialRevision, id, section, exclusive).Scan(&name, &product)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", identity.ErrAccountStale
	}
	return name, product, err
}

func RecordLoadBalancerComponentName(ctx context.Context, tx *sql.Tx, scopeID int64, rename provider.LoadBalancerRename) error {
	section, id, exclusive := loadBalancerRenameComponent(rename)
	result, err := tx.ExecContext(ctx, `UPDATE cmp_load_balancer_configurations AS c
  SET configuration=jsonb_set(c.configuration, ARRAY[$6::text,(selected.position-1)::text,'name'], to_jsonb($5::text)),
      refresh_started_at=clock_timestamp()
  FROM (
    SELECT component.position
    FROM cmp_load_balancer_configurations AS saved
    CROSS JOIN LATERAL jsonb_array_elements(saved.configuration->$6::text) WITH ORDINALITY AS component(item,position)
    WHERE (saved.scope_id,saved.external_id)=($1,$2)
      AND item->>'id'=$3 AND COALESCE(item->>'name','') IN ($4,$5::text)
      AND (NOT $7::boolean OR item->'exclusive'='true'::jsonb)
  ) AS selected
  WHERE (c.scope_id,c.external_id)=($1,$2)`, scopeID, rename.ID, id, rename.CurrentName, rename.Name, section, exclusive)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err == nil && rows != 1 {
		return identity.ErrAccountStale
	}
	return err
}
