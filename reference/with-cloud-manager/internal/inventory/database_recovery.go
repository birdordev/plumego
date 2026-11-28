package inventory

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func CheckDatabaseRecoveryPoint(ctx context.Context, tx *sql.Tx, target MutationTarget, source string, observed, at time.Time) error {
	var db provider.DatabaseDetails
	err := tx.QueryRowContext(ctx, `SELECT snapshot.product,snapshot.engine
		FROM cmp_database_restore_windows AS snapshot
		JOIN cmp_inventory_objects AS o USING (scope_id,external_id)
		JOIN cmp_inventory_scopes AS s USING (scope_id)
		WHERE (snapshot.scope_id,snapshot.external_id,snapshot.credential_revision,snapshot.observed_at)=($1,$2,$3,$4)
		  AND s.credential_revision=snapshot.credential_revision AND snapshot.recovery_scope='instance'
		  AND snapshot.product=o.database_instance->>'product' AND snapshot.engine=o.database_instance->>'engine'
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(snapshot.windows) AS recovery_range
		    WHERE $5::timestamptz BETWEEN (recovery_range->>'start_at')::timestamptz AND (recovery_range->>'end_at')::timestamptz)
		FOR SHARE OF snapshot`, target.ScopeID, source, target.CredentialRevision, observed, at).Scan(&db.Product, &db.Engine)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !provider.SupportsDatabaseRecovery(target.Provider, db)) {
		return identity.ErrAccountStale
	}
	return err
}

func CheckDatabaseRecoveryPlacement(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region string,
	source MutationTarget, spec provider.DatabaseRecovery) ([3]int64, error) {
	var revisions [3]int64
	var sourceVPC string
	var db provider.DatabaseDetails
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(o.links->0->>'id',''),o.database_instance->>'product',o.database_instance->>'engine'
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
		WHERE (o.scope_id,o.external_id,s.credential_revision)=($1,$2,$3)
		  AND o.links->0->>'kind'='network' AND o.database_instance IS NOT NULL`,
		source.ScopeID, spec.SourceID, source.CredentialRevision).Scan(&sourceVPC, &db.Product, &db.Engine)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (sourceVPC == "" || sourceVPC == spec.VPC || !provider.SupportsDatabaseRecovery(source.Provider, db))) {
		return revisions, identity.ErrAccountStale
	}
	if err != nil {
		return revisions, err
	}
	for i, dep := range []struct {
		kind provider.Kind
		id   string
	}{{provider.Network, spec.VPC}, {provider.Subnet, spec.Subnet}, {provider.SecurityGroup, spec.SecurityGroup}} {
		if dep.kind == provider.SecurityGroup && source.Provider == provider.AlibabaECS {
			continue
		}
		target, err := ResolveNetworkMutation(ctx, tx, actor, account, region, dep.id, dep.kind)
		if err != nil {
			return revisions, err
		}
		var valid bool
		err = tx.QueryRowContext(ctx, `SELECT s.credential_revision=$3
			AND ($4<>'subnet' OR EXISTS (SELECT 1 FROM jsonb_array_elements(o.links) AS link
				WHERE (link->>'kind',link->>'relation',link->>'id')=('network','in_network',$5)))
			AND (NOT $6 OR $4<>'subnet' OR EXISTS (SELECT 1 FROM jsonb_array_elements(o.links) AS link
				WHERE (link->>'kind',link->>'relation',link->>'id')=('zone','in_zone',$7)))
			FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING (scope_id)
			WHERE (o.scope_id,o.external_id)=($1,$2)`, target.ScopeID, dep.id, source.CredentialRevision,
			dep.kind, spec.VPC, source.Provider == provider.AlibabaECS, spec.Zone).Scan(&valid)
		if err != nil {
			return revisions, err
		}
		deleting, err := NetworkDeletionActive(ctx, tx, target.ScopeID, dep.id)
		if err != nil {
			return revisions, err
		}
		if !valid || deleting || target.CredentialRevision != source.CredentialRevision {
			return revisions, identity.ErrAccountStale
		}
		revisions[i] = target.OwnershipRevision
	}
	return revisions, nil
}
