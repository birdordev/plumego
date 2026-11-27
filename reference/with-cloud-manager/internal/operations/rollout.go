package operations

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
)

//go:embed 0142_operation_rollout.sql
var operationRolloutSchema string

const OperationRolloutMigration = "0142_operation_rollout"

const MaxRolloutRules = 128

var (
	rolloutRegion = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	rolloutAction = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

type RolloutRule struct {
	Region string `json:"region"`
	Action string `json:"action"`
}

type RolloutPolicy struct {
	Revision int64         `json:"revision,string"`
	Rules    []RolloutRule `json:"rules"`
}

func (s Store) AccountRollout(ctx context.Context, token string, scope identity.Scope, account string) (RolloutPolicy, error) {
	var policy RolloutPolicy
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := rolloutAdmin(ctx, tx, token, scope, account, false)
		if err != nil {
			return err
		}
		policy, err = readRollout(ctx, tx, actor.Tenant, account)
		return err
	})
	return policy, err
}

func (s Store) ReplaceAccountRollout(ctx context.Context, token string, scope identity.Scope, account string, expected int64, rules []RolloutRule) (RolloutPolicy, error) {
	rules, err := normalizeRollout(rules)
	if err != nil || expected < 0 || expected == math.MaxInt64 {
		return RolloutPolicy{}, identity.ErrAccountInvalid
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return RolloutPolicy{}, err
	}
	policy := RolloutPolicy{Revision: expected + 1, Rules: rules}
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := rolloutAdmin(ctx, tx, token, scope, account, true)
		if err != nil {
			return err
		}
		current, err := readRollout(ctx, tx, actor.Tenant, account)
		if err != nil {
			return err
		}
		if current.Revision != expected {
			return identity.ErrAccountStale
		}
		if current.Revision > 0 && slices.Equal(current.Rules, rules) {
			policy = current
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_account_operation_rollouts
			(tenant_id,account_id,revision,rules) VALUES ($1,$2,$3,$4::jsonb)`,
			actor.Tenant, account, policy.Revision, string(encoded)); err != nil {
			return err
		}
		outcome := "restricted"
		if len(rules) == 0 {
			outcome = "frozen"
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
			(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,actor_id,outcome,revision,occurred_at)
			VALUES ($1,$2,'account','replace-operation-rollout',$3,'cloud-account',$3,$4,$5,$6,clock_timestamp())`,
			actor.Tenant, actor.Project, account, actor.Principal, outcome, policy.Revision)
		return err
	})
	if err != nil {
		return RolloutPolicy{}, err
	}
	return policy, nil
}

func rolloutAdmin(ctx context.Context, tx *sql.Tx, token string, scope identity.Scope, account string, write bool) (identity.Scope, error) {
	if !operationKey.MatchString(account) {
		return identity.Scope{}, identity.ErrAccountInvalid
	}
	actor, err := operationActor(ctx, tx, token, scope, identity.Manage)
	if err != nil {
		return identity.Scope{}, err
	}
	query := "SELECT provider FROM cmp_cloud_accounts WHERE tenant_id=$1 AND project_id=$2 AND account_id=$3"
	if write {
		query += " FOR NO KEY UPDATE"
	}
	var name string
	err = tx.QueryRowContext(ctx, query, actor.Tenant, actor.Project, account).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Scope{}, identity.ErrForbidden
	}
	if err != nil {
		return identity.Scope{}, err
	}
	if !publicCloudProvider(name) {
		return identity.Scope{}, identity.ErrAccountConflict
	}
	return actor, nil
}

func readRollout(ctx context.Context, tx *sql.Tx, tenant, account string) (RolloutPolicy, error) {
	policy := RolloutPolicy{Rules: []RolloutRule{}}
	var data []byte
	err := tx.QueryRowContext(ctx, `SELECT revision,rules FROM cmp_account_operation_rollouts
		WHERE (tenant_id,account_id)=($1,$2) ORDER BY revision DESC LIMIT 1`, tenant, account).Scan(&policy.Revision, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return RolloutPolicy{}, err
	}
	return policy, json.Unmarshal(data, &policy.Rules)
}

func normalizeRollout(rules []RolloutRule) ([]RolloutRule, error) {
	if rules == nil || len(rules) > MaxRolloutRules {
		return nil, identity.ErrAccountInvalid
	}
	result := slices.Clone(rules)
	slices.SortFunc(result, func(a, b RolloutRule) int {
		if a.Region < b.Region || (a.Region == b.Region && a.Action < b.Action) {
			return -1
		}
		if a == b {
			return 0
		}
		return 1
	})
	for i, rule := range result {
		if !rolloutRegion.MatchString(rule.Region) || !rolloutAction.MatchString(rule.Action) ||
			(i > 0 && result[i-1] == rule) {
			return nil, identity.ErrAccountInvalid
		}
	}
	return result, nil
}

func captureRollout(ctx context.Context, tx *sql.Tx, id string) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, `WITH current AS MATERIALIZED (
		SELECT cmp_operation_rollout_revisions(o) AS revisions FROM cmp_instance_operations o WHERE operation_id=$1
	), captured AS (
		UPDATE cmp_instance_operations SET rollout_revisions=current.revisions FROM current
		WHERE operation_id=$1 AND -1<>ALL(current.revisions) AND current.revisions<>ARRAY[0,0]::bigint[]
	)
	SELECT -1<>ALL(revisions) FROM current`, id).Scan(&allowed)
	if err == nil && !allowed {
		return identity.ErrForbidden
	}
	return err
}

func authorizeRolloutDispatch(ctx context.Context, tx *sql.Tx, id string) error {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT current.revisions=o.rollout_revisions AND -1<>ALL(current.revisions)
		FROM cmp_instance_operations o CROSS JOIN LATERAL (SELECT cmp_operation_rollout_revisions(o) AS revisions) current
		WHERE o.operation_id=$1`, id).Scan(&allowed)
	if err == nil && !allowed {
		return identity.ErrForbidden
	}
	return err
}
