package inventory

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0097_tag_observations.sql
var tagObservationsSchema string

//go:embed 0098_tag_policies.sql
var tagPoliciesSchema string

const TagPolicyAlertsMigration = "0148_tag_policy_alerts"

//go:embed 0148_tag_policy_alerts.sql
var tagPolicyAlertsSchema string

var (
	ErrTagPolicyNotFound = errors.New("tag policy is unavailable")
	ErrTagPolicyStale    = errors.New("tag policy revision changed")
	ErrTagPolicyExists   = errors.New("tag policy already exists")
)

type TagPolicyScope struct {
	Tenant  string
	Project string
}

type TagRule struct {
	Kind         provider.Kind `json:"kind"`
	RequiredKeys []string      `json:"required_keys"`
}

type TagPolicyInput struct {
	MaxAgeSeconds        int       `json:"max_age_seconds"`
	Rules                []TagRule `json:"rules"`
	AlertOnNoncompliance *bool     `json:"alert_on_noncompliance,omitempty"`
}

type TagPolicy struct {
	TagPolicyInput
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

func MigrateTagPolicies(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0098_tag_policies", tagPoliciesSchema)
}

func MigrateTagPolicyAlerts(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, TagPolicyAlertsMigration, tagPolicyAlertsSchema)
}

func (key TagPolicyScope) valid() bool {
	return key.Tenant != "" && key.Project != "" && len(key.Tenant) <= 256 && len(key.Project) <= 256 &&
		validInventoryText(key.Tenant) && validInventoryText(key.Project)
}

func (input TagPolicyInput) valid() bool {
	if input.MaxAgeSeconds < 60 || input.MaxAgeSeconds > 604800 || input.Rules == nil || len(input.Rules) > 24 {
		return false
	}
	kinds := make(map[provider.Kind]bool, len(input.Rules))
	for _, rule := range input.Rules {
		if !rule.Kind.Valid() || kinds[rule.Kind] || len(rule.RequiredKeys) == 0 || len(rule.RequiredKeys) > 24 {
			return false
		}
		kinds[rule.Kind] = true
		keys := make(map[string]bool, len(rule.RequiredKeys))
		for _, key := range rule.RequiredKeys {
			if key == "" || len(key) > 128 || strings.TrimSpace(key) != key || !utf8.ValidString(key) ||
				strings.IndexFunc(key, unicode.IsControl) >= 0 || keys[key] {
				return false
			}
			keys[key] = true
		}
	}
	return true
}

func (s Store) GetTagPolicy(ctx context.Context, token string, scope TagPolicyScope, revision int64) (TagPolicy, error) {
	if !scope.valid() || revision < 0 {
		return TagPolicy{}, ErrInvalidQuery
	}
	var found TagPolicy
	err := s.readProject(ctx, token, Query{Tenant: scope.Tenant, Project: scope.Project}, func(tx *sql.Tx) error {
		var err error
		found, err = readTagPolicy(ctx, tx, scope, revision)
		return err
	})
	return found, err
}

func readTagPolicy(ctx context.Context, tx *sql.Tx, scope TagPolicyScope, revision int64) (TagPolicy, error) {
	var policy TagPolicy
	var rules []byte
	var notify bool
	err := tx.QueryRowContext(ctx, `SELECT revision,max_age_seconds,rules,updated_at,alert_on_noncompliance
		FROM cmp_tag_policies WHERE (tenant_id,project_id)=($1,$2) AND ($3::bigint=0 OR revision=$3)
		ORDER BY revision DESC LIMIT 1`, scope.Tenant, scope.Project, revision).
		Scan(&policy.Revision, &policy.MaxAgeSeconds, &rules, &policy.UpdatedAt, &notify)
	if errors.Is(err, sql.ErrNoRows) {
		return TagPolicy{}, ErrTagPolicyNotFound
	}
	if err != nil {
		return TagPolicy{}, err
	}
	policy.AlertOnNoncompliance = &notify
	if err := json.Unmarshal(rules, &policy.Rules); err != nil || !policy.TagPolicyInput.valid() {
		return TagPolicy{}, errors.New("stored tag policy violated its contract")
	}
	return policy, nil
}

func (s Store) SaveTagPolicy(ctx context.Context, token string, scope TagPolicyScope, expected int64, input TagPolicyInput) (TagPolicy, error) {
	if !scope.valid() || expected < 0 || expected == math.MaxInt64 || !input.valid() {
		return TagPolicy{}, ErrInvalidQuery
	}
	rules, err := json.Marshal(input.Rules)
	if err != nil {
		return TagPolicy{}, err
	}
	var policy TagPolicy
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		auth := identity.NewStore(tx)
		if _, err := authorizeTagPolicyWrite(ctx, auth, token, scope); err != nil {
			return err
		}
		// 项目行锁串行化首版创建和后续版本发布。
		var project string
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM cmp_projects
			WHERE (tenant_id,project_id)=($1,$2) FOR NO KEY UPDATE`, scope.Tenant, scope.Project).Scan(&project); err != nil {
			return err
		}
		actor, err := authorizeTagPolicyWrite(ctx, auth, token, scope)
		if err != nil {
			return err
		}
		var current int64
		var notify bool
		if err := tx.QueryRowContext(ctx, `SELECT revision,alert_on_noncompliance FROM cmp_tag_policies
			WHERE (tenant_id,project_id)=($1,$2) ORDER BY revision DESC LIMIT 1`, scope.Tenant, scope.Project).
			Scan(&current, &notify); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if expected != current {
			if expected == 0 {
				return ErrTagPolicyExists
			}
			return ErrTagPolicyStale
		}
		if input.AlertOnNoncompliance != nil {
			notify = *input.AlertOnNoncompliance
		}
		policy = TagPolicy{TagPolicyInput: input, Revision: expected + 1}
		policy.AlertOnNoncompliance = &notify
		if err := tx.QueryRowContext(ctx, `INSERT INTO cmp_tag_policies
			(tenant_id,project_id,revision,max_age_seconds,rules,updated_by,alert_on_noncompliance)
			VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7) RETURNING updated_at`,
			scope.Tenant, scope.Project, policy.Revision, input.MaxAgeSeconds, string(rules), actor.Principal, notify).Scan(&policy.UpdatedAt); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_project_audit
			(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,actor_id,revision,occurred_at)
			VALUES ($1,$2,'tag_policy','publish',$2,'tag-policy','',$3,$4,$5)`,
			scope.Tenant, scope.Project, actor.Principal, policy.Revision, policy.UpdatedAt)
		return err
	})
	if err != nil {
		return TagPolicy{}, err
	}
	return policy, nil
}

func authorizeTagPolicyWrite(ctx context.Context, auth identity.Store, token string, scope TagPolicyScope) (identity.Scope, error) {
	actor, err := auth.AuthenticateRequest(ctx, token)
	if err != nil {
		return identity.Scope{}, err
	}
	if actor.Tenant != scope.Tenant {
		return identity.Scope{}, identity.ErrForbidden
	}
	actor.Project = scope.Project
	_, err = auth.AuthorizeProject(ctx, actor, identity.Manage)
	return actor, err
}
