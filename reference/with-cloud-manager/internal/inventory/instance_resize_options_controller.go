package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type ReadResizeOptions func(context.Context, identity.CloudAccountBinding, provider.Scope, string, string) (provider.InstanceResizeOptions, error)

type resizeOptionsTask struct {
	actor      identity.Scope
	account    string
	region     string
	id         string
	current    string
	scopeID    int64
	fence      int64
	credential int64
	ownership  int64
}

func (s Store) RunInstanceResizeOptions(ctx context.Context, read ReadResizeOptions) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := s.CollectInstanceResizeOptions(ctx, read)
		if err != nil && ctx.Err() == nil {
			return err
		}
		if !worked {
			select {
			case <-ctx.Done():
			case <-ticker.C:
			}
		}
	}
	return nil
}

func (s Store) CollectInstanceResizeOptions(ctx context.Context, read ReadResizeOptions) (bool, error) {
	var task resizeOptionsTask
	err := s.db.QueryRowContext(ctx, `WITH next AS (
		SELECT c.scope_id,c.external_id,s.tenant_id,s.account_id,s.region
		FROM cmp_instance_resize_options AS c JOIN cmp_inventory_scopes AS s USING (scope_id)
		WHERE c.state='pending' AND c.due_at<=statement_timestamp()
		ORDER BY c.due_at,c.scope_id,c.external_id LIMIT 1 FOR UPDATE OF c SKIP LOCKED
	)
	UPDATE cmp_instance_resize_options AS c SET fence=c.fence+1,due_at=statement_timestamp()+interval '45 seconds'
	FROM next WHERE (c.scope_id,c.external_id)=(next.scope_id,next.external_id)
	RETURNING c.scope_id,c.external_id,c.fence,c.project_id,c.principal_id,c.current_type,
		c.credential_revision,c.ownership_revision,next.tenant_id,next.account_id,next.region`).
		Scan(&task.scopeID, &task.id, &task.fence, &task.actor.Project, &task.actor.Principal, &task.current,
			&task.credential, &task.ownership, &task.actor.Tenant, &task.account, &task.region)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var binding identity.CloudAccountBinding
	err = dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		if err := task.validate(ctx, tx, false); err != nil {
			return err
		}
		var err error
		binding, err = identity.NewStore(tx).CloudAccountBinding(ctx, task.actor.Tenant, task.account)
		return err
	})
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
			return true, s.finishResizeOptions(ctx, task, provider.InstanceResizeOptions{})
		}
		return true, err
	}
	scope := provider.Scope{Provider: binding.Provider, RemoteIdentity: binding.RemoteIdentity, Region: task.region, Kind: provider.Instance}
	call, cancel := context.WithTimeout(ctx, 20*time.Second)
	options, err := read(call, binding, scope, task.id, task.current)
	cancel()
	if err != nil {
		if provider.IsFailure(err, provider.Throttled) || provider.IsFailure(err, provider.Transient) || errors.Is(err, context.DeadlineExceeded) {
			return true, nil
		}
		options = provider.InstanceResizeOptions{}
	}
	return true, s.finishResizeOptions(ctx, task, options)
}

func (task resizeOptionsTask) validate(ctx context.Context, tx *sql.Tx, lock bool) error {
	resolve := ResolveMutationPreview
	if lock {
		resolve = ResolveNetworkMutation
	}
	target, err := resolve(ctx, tx, task.actor, task.account, task.region, task.id, provider.Instance)
	if err != nil {
		return err
	}
	current, stopped, err := resizeOptionSource(ctx, tx, target, task.id)
	if err != nil {
		return err
	}
	if !stopped || current != task.current || target.ScopeID != task.scopeID ||
		target.CredentialRevision != task.credential || target.OwnershipRevision != task.ownership {
		return identity.ErrAccountStale
	}
	var live bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_instance_resize_options
		WHERE (scope_id,external_id,fence)=($1,$2,$3) AND state='pending' AND expires_at>statement_timestamp()
		AND due_at>statement_timestamp())`, task.scopeID, task.id, task.fence).Scan(&live)
	if err == nil && !live {
		return identity.ErrAccountStale
	}
	return err
}

func (s Store) finishResizeOptions(ctx context.Context, task resizeOptionsTask, options provider.InstanceResizeOptions) error {
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		ready := resizeOptionKey.MatchString(options.Zone) && len(options.Types) <= 128 && slices.IsSorted(options.Types)
		if ready {
			for _, size := range options.Types {
				if size == task.current || !provider.ValidInstanceType(size) {
					ready = false
					break
				}
			}
		}
		if ready {
			if err := task.validate(ctx, tx, true); err != nil {
				if !errors.Is(err, identity.ErrForbidden) && !errors.Is(err, identity.ErrAccountStale) {
					return err
				}
				ready = false
			}
		}
		state := "ready"
		if !ready {
			state = "unavailable"
			options = provider.InstanceResizeOptions{Types: []string{}}
		} else if options.Types == nil {
			options.Types = []string{}
		}
		raw, err := json.Marshal(options.Types)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE cmp_instance_resize_options SET state=$4,zone=$5,target_types=$6::jsonb,
			observed_at=CASE WHEN $4='ready' THEN statement_timestamp() END,
			expires_at=statement_timestamp()+CASE WHEN $4='ready' THEN interval '2 minutes' ELSE interval '20 seconds' END
			WHERE (scope_id,external_id,fence)=($1,$2,$3) AND state='pending' AND due_at>statement_timestamp()`,
			task.scopeID, task.id, task.fence, state, options.Zone, string(raw))
		return err
	})
}
