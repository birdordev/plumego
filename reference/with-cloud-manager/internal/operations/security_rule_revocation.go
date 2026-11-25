package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0032_security_rule_revocation.sql
var securityRuleRevocationSchema string

const revokeSecurityRule = "revoke-security-group-rule"

type RuleRevocation struct {
	Account  string `json:"account"`
	Region   string `json:"region"`
	ParentID string `json:"parent_id"`
	RuleID   string `json:"rule_id"`
}

func (s Store) SubmitSecurityRuleRevocation(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input RuleRevocation) (Operation, error) {
	if revision < 1 || !operationKey.MatchString(key) || !operationKey.MatchString(input.Account) || !operationKey.MatchString(input.Region) ||
		!operationKey.MatchString(input.ParentID) || !operationKey.MatchString(input.RuleID) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		RuleRevocation
		ParentRevision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(revokeSecurityRule+"\x00"), data...))
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		if _, err := identity.NewStore(tx).AuthorizeAccount(ctx, actor, input.Account, identity.Execute); err != nil {
			return err
		}
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		found, err := findReplay(ctx, tx, actor, key, digest, &op)
		if found || err != nil {
			return err
		}
		if err := checkBudget(ctx, tx, actor); err != nil {
			return err
		}
		parent, err := inventory.ResolveNetworkMutation(ctx, tx, actor, input.Account, input.Region, input.ParentID, provider.SecurityGroup)
		if err != nil {
			return err
		}
		if parent.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		if parent.Provider != "aws-ec2" && parent.Provider != "openstack" {
			return ErrInvalid
		}
		assetScope, name, err := lookupSecurityRule(ctx, tx, actor.Tenant, input, true)
		if err != nil {
			return err
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'awaiting_approval','security-group-rule',$10,$11,$12,$13,$14)
			ON CONFLICT DO NOTHING RETURNING `+operationFields, id, actor.Tenant, actor.Project, actor.Principal, assetScope,
			input.RuleID, input.Account, input.Region, revokeSecurityRule, key, digest[:], parent.CredentialRevision, parent.MembershipRevision, revision), &op)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents(operation_id,launch_token,name,parent_scope_id,parent_id,parent_revision)
			VALUES ($1,$2,$3,$4,$5,$6)`, id, rand.Text(), name, parent.ScopeID, input.ParentID, revision); err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func lookupSecurityRule(ctx context.Context, tx *sql.Tx, tenant string, input RuleRevocation, beforeWrite bool) (int64, string, error) {
	var id int64
	var name string
	err := tx.QueryRowContext(ctx, `SELECT s.scope_id,o.name FROM cmp_inventory_scopes AS s
		JOIN cmp_inventory_objects AS o USING (scope_id)
		JOIN cmp_cloud_accounts AS a USING (tenant_id,account_id)
		WHERE (s.tenant_id,s.account_id,s.region,s.kind,o.external_id)=($1,$2,$3,'security-group-rule',$4)
		AND o.links=jsonb_build_array(jsonb_build_object('kind','security-group','id',$5::text,'relation','in_security_group'))
		AND (NOT $6::bool OR (o.removed_at IS NULL AND o.missing_generation IS NULL AND a.credential_revision=s.credential_revision))
		FOR UPDATE OF o`, tenant, input.Account, input.Region, input.RuleID, input.ParentID, beforeWrite).Scan(&id, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", identity.ErrForbidden
	}
	return id, name, err
}

func checkSecurityRuleRevocation(ctx context.Context, tx *sql.Tx, w *work) error {
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	intent := &w.network
	var targetScope int64
	err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.name,i.parent_id,i.parent_scope_id,i.parent_revision,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, w.ID).
		Scan(&targetScope, &intent.Name, &intent.ParentID, &intent.parentScope, &intent.parentRevision,
			&intent.observer.principal, &intent.observer.membership, &intent.observer.credential)
	if err != nil {
		return err
	}
	if w.Status == "queued" {
		if err := checkSecurityRuleApproval(ctx, tx, w); err != nil {
			return err
		}
	}
	actor, resolve := w.actor, inventory.ResolveNetworkMutation
	if !beforeWrite {
		resolve = inventory.ResolveNetworkObservation
		if intent.observer.principal != "" {
			actor.Principal = intent.observer.principal
		}
	}
	parent, err := resolve(ctx, tx, actor, w.Account, w.Region, intent.ParentID, provider.SecurityGroup)
	if err != nil {
		return err
	}
	if parent.ScopeID != intent.parentScope || ((beforeWrite || intent.observer.principal == "") && parent.OwnershipRevision != intent.parentRevision) {
		return identity.ErrAccountStale
	}
	if !beforeWrite && intent.observer.principal != "" {
		member, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Approve)
		if err != nil {
			return err
		}
		if member.Revision != intent.observer.membership || parent.MembershipRevision != intent.observer.membership || parent.CredentialRevision != intent.observer.credential {
			return identity.ErrAccountStale
		}
	} else if parent.CredentialRevision != w.credential || parent.MembershipRevision != w.membership {
		return identity.ErrAccountStale
	}
	assetScope, name, err := lookupSecurityRule(ctx, tx, actor.Tenant, RuleRevocation{w.Account, w.Region, intent.ParentID, w.AssetID}, beforeWrite)
	if err == nil && (assetScope != targetScope || (beforeWrite && name != intent.Name)) {
		return identity.ErrAccountStale
	}
	w.ruleScope = assetScope
	return err
}

func (s Store) processSecurityRuleRevocation(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	revoker, ok := client.(provider.SecurityRuleRevoker)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = provider.SecurityGroupRule
	target := provider.SecurityRuleTarget{ID: w.AssetID, GroupID: w.network.ParentID, Name: w.network.Name}
	if w.Status == "queued" {
		return s.dispatchSecurityRuleRevocation(ctx, task, w, revoker, scope, target)
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	result, err := revoker.ObserveSecurityRuleRevocation(ctx, scope, target)
	confirmed := err == nil && result == provider.ActionSucceeded
	settled, settleErr := s.recordSecurityRuleEvidence(ctx, task, w, confirmed)
	if settled || settleErr != nil {
		return false, settleErr
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "rule_revocation_requires_evidence", "")
	}
	return true, nil
}

func (s Store) dispatchSecurityRuleRevocation(ctx context.Context, task mq.Task, w work, revoker provider.SecurityRuleRevoker, scope provider.Scope, target provider.SecurityRuleTarget) (bool, error) {
	apply, err := revoker.PrepareSecurityRuleRevocation(ctx, scope, target)
	switch {
	case provider.IsFailure(err, provider.DeletionBlocked):
		return false, s.transition(ctx, task, w.Status, "failed", "rule_revocation_preconditions_unconfirmed", "")
	case provider.IsFailure(err, provider.DeletionDisabled):
		return false, s.transition(ctx, task, w.Status, "failed", "rule_revocation_disabled", "")
	case provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized):
		return false, s.transition(ctx, task, w.Status, "failed", "rule_revocation_preflight_unavailable", "")
	case err != nil || apply == nil:
		if task.Attempts >= task.MaxAttempts {
			return false, s.transition(ctx, task, w.Status, "failed", "rule_revocation_preflight_unavailable", "")
		}
		return true, nil
	}
	armed, err := s.prepare(ctx, task, true)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return false, s.endUnavailable(ctx, task, armed)
	}
	if err != nil {
		return false, err
	}
	requestID, callErr := apply(ctx)
	reason := "provider_accepted"
	if callErr != nil {
		reason = "submission_outcome_unknown"
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	return true, s.transition(cleanup, task, "applying", "observing", reason, requestID)
}

func (s Store) recordSecurityRuleEvidence(ctx context.Context, task mq.Task, w work, confirmed bool) (bool, error) {
	var observations int16
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := checkSecurityRuleRevocation(ctx, tx, &w); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `UPDATE cmp_network_intents SET absence_observations=CASE WHEN $2::bool
			THEN LEAST(absence_observations+1,2) ELSE 0 END WHERE operation_id=$1 RETURNING absence_observations`,
			w.ID, confirmed).Scan(&observations); err != nil || observations < 2 {
			return err
		}
		if err := inventory.MarkResourceDeleted(ctx, tx, w.ruleScope, w.AssetID); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "security_rule_absence_confirmed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return true, s.endUnavailable(ctx, task, w)
	}
	return observations == 2, err
}
