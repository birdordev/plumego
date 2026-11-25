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
	if err := page.Validate(cursor); err != nil || kind != provider.SecurityGroupRule {
		return err
	}
	seen := make(map[string]struct{}, len(page.Objects))
	for _, object := range page.Objects {
		_, hasParent := provider.SecurityRuleParent(object)
		if _, duplicate := seen[object.ID]; duplicate || !hasParent || object.Tags != nil {
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
			err = upsert(ctx, tx, scopeID, generation, []provider.Object{object})
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
