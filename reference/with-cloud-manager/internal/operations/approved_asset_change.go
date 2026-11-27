package operations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func (s Store) submitApprovedAssetChange(ctx context.Context, token string, scope identity.Scope, key string, revision int64, kind provider.Kind,
	account, region, assetID, action, groupID string, digest [32]byte, prepareIntent func(context.Context, *sql.Tx, identity.Scope, inventory.MutationTarget) (func(string) error, error)) (Operation, error) {
	if revision < 1 || !operationKey.MatchString(key) || !operationKey.MatchString(account) ||
		!operationKey.MatchString(region) || !operationKey.MatchString(assetID) {
		return Operation{}, ErrInvalid
	}
	var op Operation
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		permission := identity.Execute
		if kind == provider.BareMetal {
			permission = identity.ExecuteBareMetal
		}
		actor, err := operationActor(ctx, tx, token, scope, permission)
		if err != nil {
			return err
		}
		if _, err := identity.NewStore(tx).AuthorizeAccount(ctx, actor, account, permission); err != nil {
			return err
		}
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		if found, err := findReplay(ctx, tx, actor, key, digest, &op); found || err != nil {
			return err
		}
		if err := checkBudget(ctx, tx, actor); err != nil {
			return err
		}
		if groupID != "" {
			if _, err := inventory.LockLoadBalancerScope(ctx, tx, actor.Tenant, account, region); err != nil {
				return err
			}
		}
		var target inventory.MutationTarget
		if kind == provider.BareMetal {
			target, err = inventory.ResolveBareMetalMutation(ctx, tx, actor, account, region, assetID)
		} else {
			target, err = inventory.ResolveNetworkMutation(ctx, tx, actor, account, region, assetID, kind)
		}
		if err != nil {
			return err
		}
		if kind == provider.BareMetal {
			if err := checkBareMetalDeploymentFence(ctx, tx, assetID, ""); err != nil {
				return err
			}
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		var persistIntent func(string) error
		if prepareIntent != nil {
			persistIntent, err = prepareIntent(ctx, tx, actor, target)
			if err != nil {
				return err
			}
		}
		if groupID != "" {
			if err := checkActiveTargetGroupMutation(ctx, tx, target.ScopeID, groupID); err != nil {
				return err
			}
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'awaiting_approval',$15,$10,$11,$12,$13,$14)
			ON CONFLICT DO NOTHING RETURNING `+operationFields, id, actor.Tenant, actor.Project, actor.Principal, target.ScopeID,
			assetID, account, region, action, key, digest[:], target.CredentialRevision,
			target.MembershipRevision, revision, kind), &op)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if persistIntent != nil {
			if err := persistIntent(id); err != nil {
				return err
			}
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func resolveApprovedAssetChange(ctx context.Context, tx *sql.Tx, w *work, scopeID int64, observer creationObserver, kind provider.Kind) (inventory.MutationTarget, bool, error) {
	var target inventory.MutationTarget
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return target, beforeWrite, err
		}
	}
	actor, membership, credential := w.actor, w.membership, w.credential
	var err error
	if beforeWrite {
		target, err = inventory.ResolveNetworkMutation(ctx, tx, actor, w.Account, w.Region, w.AssetID, kind)
	} else {
		if observer.principal != "" {
			actor.Principal, membership, credential = observer.principal, observer.membership, observer.credential
			if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Approve); err != nil {
				return target, beforeWrite, err
			}
		}
		if w.Action == retireInstance || w.Action == releaseVolume {
			target, err = inventory.ResolveNetworkDeletion(ctx, tx, actor, w.Account, w.Region, w.AssetID, kind, false)
		} else {
			target, err = inventory.ResolveNetworkObservation(ctx, tx, actor, w.Account, w.Region, w.AssetID, kind)
		}
	}
	if err != nil {
		return target, beforeWrite, err
	}
	if target.ScopeID != scopeID || target.OwnershipRevision != w.ownership ||
		target.MembershipRevision != membership || target.CredentialRevision != credential {
		return target, beforeWrite, identity.ErrAccountStale
	}
	return target, beforeWrite, nil
}
