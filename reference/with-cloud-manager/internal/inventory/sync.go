package inventory

import (
	"context"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/provider"
)

func (s Store) ScanPage(ctx context.Context, claim Claim, adapter provider.Discovery) (Claim, bool, error) {
	scope, err := s.scope(ctx, claim)
	if err != nil {
		return Claim{}, false, err
	}
	page, err := adapter.Discover(ctx, scope, claim.cursor)
	if provider.IsFailure(err, provider.ExpiredCursor) {
		_, _, restartErr := s.advance(ctx, claim, provider.Page{}, true)
		return Claim{}, false, errors.Join(err, restartErr)
	}
	if err != nil {
		return Claim{}, false, err
	}
	if err := validateObservationPage(scope.Kind, page, claim.cursor); err != nil {
		return Claim{}, false, err
	}
	return s.advance(ctx, claim, page, false)
}

func validateObservationPage(kind provider.Kind, page provider.Page, cursor string) error {
	if err := page.Validate(cursor); err != nil {
		return err
	}
	for _, object := range page.Objects {
		if (kind == provider.LoadBalancer) != (object.LoadBalancer != nil) ||
			(kind == provider.DatabaseInstance) != (object.Database != nil) ||
			(kind == provider.ObjectBucket) != (object.Bucket != nil) ||
			(kind == provider.CacheInstance) != (object.Cache != nil) ||
			(kind == provider.KubernetesCluster) != (object.Cluster != nil) {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	switch kind {
	case provider.SecurityGroupRule, provider.Snapshot, provider.LoadBalancer, provider.DNSZone, provider.DatabaseInstance, provider.ObjectBucket, provider.CacheInstance, provider.KubernetesCluster:
	default:
		return nil
	}
	seen := make(map[string]struct{}, len(page.Objects))
	for _, object := range page.Objects {
		var valid bool
		switch kind {
		case provider.Snapshot:
			valid = object.ValidSnapshot()
		case provider.SecurityGroupRule:
			_, hasParent := provider.SecurityRuleParent(object)
			valid = hasParent && object.Tags == nil
		case provider.LoadBalancer, provider.DatabaseInstance, provider.ObjectBucket, provider.CacheInstance, provider.KubernetesCluster:
			valid = object.Status != ""
		case provider.DNSZone:
			zone := provider.DNSZoneRef{ID: object.ID, Name: object.Name}
			valid = zone.Valid() && object.Status != "" && object.Tags == nil && len(object.PublicAddresses) == 0 && len(object.Links) == 0
		}
		if _, duplicate := seen[object.ID]; duplicate || !valid {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		seen[object.ID] = struct{}{}
	}
	return nil
}

const missingCandidate = ` FROM cmp_inventory_objects AS o
	JOIN cmp_inventory_scopes AS s USING (scope_id)
	JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
	WHERE s.tenant_id=$1 AND s.account_id=$2 AND s.region=$3 AND s.kind=$4 AND o.external_id=$5
	  AND NOT s.running AND s.completed_at IS NOT NULL
	  AND o.missing_generation=s.generation AND o.removed_at IS NULL
	  AND a.mode IN ('read_only','write_enabled') AND a.credential_revision=s.credential_revision`

// ConfirmMissing 在授权点查确认缺失后才标记移除。
func (s Store) ConfirmMissing(ctx context.Context, key Key, id string, adapter provider.Observer) (bool, error) {
	return s.ConfirmMissingWithCredentialRevision(ctx, key, id, 0, adapter)
}

func (s Store) ConfirmMissingWithCredentialRevision(ctx context.Context, key Key, id string, expected int64, adapter provider.Observer) (bool, error) {
	if id == "" || len(id) > provider.MaxObjectIDSize || expected < 0 {
		return false, ErrStale
	}
	var scope provider.Scope
	var generation, revision int64
	args := []any{key.Tenant, key.Account, key.Region, key.Kind, id}
	err := s.db.QueryRowContext(ctx, "SELECT a.provider,a.remote_identity,s.region,s.kind,s.generation,s.credential_revision"+missingCandidate, args...).
		Scan(&scope.Provider, &scope.RemoteIdentity, &scope.Region, &scope.Kind, &generation, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrStale
	}
	if err != nil {
		return false, err
	}
	if expected != 0 && expected != revision {
		return false, ErrStale
	}
	object, err := adapter.Observe(ctx, scope, id)
	absent := provider.IsFailure(err, provider.Absent)
	if err != nil && !absent {
		return false, err
	}
	if !absent {
		if object.ID != id {
			return false, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if err := validateObservationPage(scope.Kind, provider.Page{Objects: []provider.Object{object}}, ""); err != nil {
			return false, err
		}
	}

	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var scopeID int64
		err := tx.QueryRowContext(ctx, "SELECT s.scope_id"+missingCandidate+
			" AND s.generation=$6 AND s.credential_revision=$7 FOR UPDATE OF s FOR SHARE OF a",
			append(args, generation, revision)...).Scan(&scopeID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		if absent {
			_, err = tx.ExecContext(ctx, "UPDATE cmp_inventory_objects SET removed_at=statement_timestamp() WHERE scope_id=$1 AND external_id=$2",
				scopeID, id)
		} else {
			err = upsert(ctx, tx, scopeID, generation, scope.Kind, []provider.Object{object})
		}
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return absent, nil
}
