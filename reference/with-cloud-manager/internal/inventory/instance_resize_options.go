package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

const InstanceResizeOptionsMigration = "0162_instance_resize_options"

func MigrateInstanceResizeOptions(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, InstanceResizeOptionsMigration)
}

var resizeOptionKey = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)

type ResizeOptions struct {
	State             string     `json:"state"`
	CurrentType       string     `json:"current_type"`
	OwnershipRevision int64      `json:"ownership_revision"`
	Zone              string     `json:"zone,omitempty"`
	Types             []string   `json:"target_types"`
	ObservedAt        *time.Time `json:"observed_at,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

func (s Store) InstanceResizeOptions(ctx context.Context, token string, requested identity.Scope, account, region, id string, refresh bool) (ResizeOptions, error) {
	if !resizeOptionKey.MatchString(account) || !resizeOptionKey.MatchString(region) || !resizeOptionKey.MatchString(id) {
		return ResizeOptions{}, ErrInvalidQuery
	}
	result := ResizeOptions{State: "unavailable", Types: []string{}}
	opts := &sql.TxOptions{ReadOnly: !refresh}
	if !refresh {
		opts.Isolation = sql.LevelRepeatableRead
	}
	err := dbutil.WithTransaction(ctx, s.db, opts, func(tx *sql.Tx) error {
		auth := identity.NewStore(tx)
		actor, err := auth.AuthenticateRequest(ctx, token)
		if err != nil {
			return err
		}
		if actor.Tenant != requested.Tenant {
			return identity.ErrForbidden
		}
		actor.Project = requested.Project
		resolve := ResolveMutationPreview
		if refresh {
			resolve = ResolveNetworkMutation
		}
		target, err := resolve(ctx, tx, actor, account, region, id, provider.Instance)
		if err != nil {
			return err
		}
		current, stopped, err := resizeOptionSource(ctx, tx, target, id)
		if err != nil {
			return err
		}
		result.CurrentType, result.OwnershipRevision = current, target.OwnershipRevision
		if !stopped {
			return nil
		}
		if refresh {
			_, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_resize_options AS old
				(scope_id,external_id,project_id,principal_id,credential_revision,ownership_revision,current_type,expires_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,statement_timestamp()+interval '2 minutes')
				ON CONFLICT (scope_id,external_id) DO UPDATE SET project_id=EXCLUDED.project_id,principal_id=EXCLUDED.principal_id,
				credential_revision=EXCLUDED.credential_revision,ownership_revision=EXCLUDED.ownership_revision,
				current_type=EXCLUDED.current_type,expires_at=EXCLUDED.expires_at,fence=old.fence+1,state='pending',
				due_at=statement_timestamp(),observed_at=NULL,zone='',target_types='[]'
				WHERE old.expires_at<=statement_timestamp() OR
				(old.project_id,old.credential_revision,old.ownership_revision,old.current_type) IS DISTINCT FROM
				(EXCLUDED.project_id,EXCLUDED.credential_revision,EXCLUDED.ownership_revision,EXCLUDED.current_type)`,
				target.ScopeID, id, actor.Project, actor.Principal, target.CredentialRevision, target.OwnershipRevision, current)
			if err != nil {
				return err
			}
		}
		var raw []byte
		err = tx.QueryRowContext(ctx, `SELECT state,zone,target_types,observed_at,expires_at FROM cmp_instance_resize_options
			WHERE (scope_id,external_id,project_id,credential_revision,ownership_revision,current_type)=($1,$2,$3,$4,$5,$6)
			AND expires_at>statement_timestamp()`, target.ScopeID, id, actor.Project, target.CredentialRevision, target.OwnershipRevision, current).
			Scan(&result.State, &result.Zone, &raw, &result.ObservedAt, &result.ExpiresAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, &result.Types)
	})
	return result, err
}

func resizeOptionSource(ctx context.Context, tx *sql.Tx, target MutationTarget, id string) (string, bool, error) {
	expected := provider.InstanceStoppedState(target.Provider)
	if expected == "" {
		return "", false, ErrInvalidQuery
	}
	current, state, err := InstanceResizeTarget(ctx, tx, target, id)
	return current, state == expected, err
}
