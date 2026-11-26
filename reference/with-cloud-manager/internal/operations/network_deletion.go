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

//go:embed 0052_network_deletion.sql
var networkDeletionSchema string

//go:embed 0053_security_group_deletion.sql
var securityGroupDeletionSchema string

//go:embed 0055_network_deletion_backfill.sql
var networkDeletionBackfillSchema string

const deleteNetwork = "delete-network"
const deleteSubnet = "delete-subnet"
const deleteSecurityGroup = "delete-security-group"

func isNetworkDeletion(action string) bool {
	return action == deleteNetwork || action == deleteSubnet || action == deleteSecurityGroup
}

type NetworkDeletion struct {
	Account            string        `json:"account"`
	Region             string        `json:"region"`
	AssetID            string        `json:"asset_id"`
	Kind               provider.Kind `json:"kind"`
	ConfirmRuleRemoval *bool         `json:"confirm_rule_removal,omitempty"`
}

type NetworkDeletionView struct {
	Name                 string `json:"name"`
	OwnershipRevision    int64  `json:"ownership_revision"`
	ParentID             string `json:"parent_id,omitempty"`
	ParentRevision       int64  `json:"parent_revision,omitempty"`
	RuleRemovalConfirmed bool   `json:"rule_removal_confirmed,omitempty"`
}

func (s Store) SubmitNetworkDeletion(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input NetworkDeletion) (Operation, error) {
	if revision < 1 || !operationKey.MatchString(key) || !operationKey.MatchString(input.Account) ||
		!operationKey.MatchString(input.Region) || !operationKey.MatchString(input.AssetID) {
		return Operation{}, ErrInvalid
	}
	var action string
	switch input.Kind {
	case provider.Network:
		action = deleteNetwork
	case provider.Subnet:
		action = deleteSubnet
	case provider.SecurityGroup:
		action = deleteSecurityGroup
	default:
		return Operation{}, ErrInvalid
	}
	ruleRemoval := action == deleteSecurityGroup
	if (ruleRemoval && (input.ConfirmRuleRemoval == nil || !*input.ConfirmRuleRemoval)) ||
		(!ruleRemoval && input.ConfirmRuleRemoval != nil) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		NetworkDeletion
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte(action+"\x00"), data...))
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
		if found, err := findReplay(ctx, tx, actor, key, digest, &op); found || err != nil {
			return err
		}
		if err := checkBudget(ctx, tx, actor); err != nil {
			return err
		}
		target, parent, parentID, err := resolveNetworkDeletion(ctx, tx, actor, input, true)
		if err != nil {
			return err
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		if err := checkNetworkDeletionDependencies(ctx, tx, actor.Tenant, input, target, ""); err != nil {
			return err
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'awaiting_approval',$10,$11,$12,$13,$14,$15)
			ON CONFLICT DO NOTHING RETURNING `+operationFields, id, actor.Tenant, actor.Project, actor.Principal, target.ScopeID,
			input.AssetID, input.Account, input.Region, action, input.Kind, key, digest[:], target.CredentialRevision, target.MembershipRevision, revision), &op)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cmp_network_intents (operation_id,launch_token,name)
			SELECT $1,$2,name FROM cmp_inventory_objects WHERE (scope_id,external_id)=($3,$4)`,
			id, rand.Text(), target.ScopeID, input.AssetID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_deletion_intents (operation_id,parent_scope_id,parent_id,parent_revision,rule_removal_confirmed)
			VALUES ($1,NULLIF($2::bigint,0),NULLIF($3,''),$4,$5)`, id, parent.ScopeID, parentID, parent.OwnershipRevision, ruleRemoval); err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func (s Store) processNetworkDeletion(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	deleter, ok := client.(provider.NetworkDeleter)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	scope.Kind = w.ResourceKind
	if w.Status == "queued" {
		send, err := deleter.PrepareNetworkDeletion(ctx, scope, w.networkDeletion)
		return s.dispatchSingleMutation(ctx, task, w, send, err, "network_deletion")
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	result, err := deleter.ObserveNetworkDeletion(ctx, scope, w.networkDeletion)
	var observations int16
	settleErr := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if targetErr := checkNetworkDeletion(ctx, tx, &w); targetErr != nil {
			return targetErr
		}
		var targetErr error
		observations, targetErr = recordAbsenceObservation(ctx, tx, w.ID, err == nil && result == provider.ActionSucceeded)
		if targetErr != nil || observations < 2 {
			return targetErr
		}
		if targetErr := inventory.MarkResourceDeleted(ctx, tx, w.networkDeletionScope, w.AssetID); targetErr != nil {
			return targetErr
		}
		if w.ResourceKind == provider.SecurityGroup {
			if err := inventory.MarkSecurityGroupRulesDeleted(ctx, tx, w.networkDeletionScope, w.AssetID); err != nil {
				return err
			}
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "network_absence_confirmed", "")
	})
	if errors.Is(settleErr, identity.ErrForbidden) || errors.Is(settleErr, identity.ErrAccountStale) {
		return false, s.endUnavailable(ctx, task, w)
	}
	if observations == 2 || settleErr != nil {
		return false, settleErr
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "network_deletion_requires_evidence", "")
	}
	return true, nil
}

func resolveNetworkDeletion(ctx context.Context, tx *sql.Tx, actor identity.Scope, input NetworkDeletion, beforeWrite bool) (inventory.MutationTarget, inventory.MutationTarget, string, error) {
	var target, parent inventory.MutationTarget
	var parentID string
	var err error
	if input.Kind != provider.Network {
		parentID, err = inventory.NetworkParentID(ctx, tx, actor.Tenant, input.Account, input.Region, input.AssetID, input.Kind)
		if err != nil {
			return target, parent, "", err
		}
	}
	if parentID != "" {
		// 父 VPC 在子资源之前取得行锁。
		resolveParent := inventory.ResolveNetworkObservation
		if beforeWrite {
			resolveParent = inventory.ResolveNetworkMutation
		}
		parent, err = resolveParent(ctx, tx, actor, input.Account, input.Region, parentID, provider.Network)
		if err != nil {
			return target, parent, "", err
		}
	}
	target, err = inventory.ResolveNetworkDeletion(ctx, tx, actor, input.Account, input.Region, input.AssetID, input.Kind, beforeWrite)
	if err != nil {
		return target, parent, "", err
	}
	if input.Kind != provider.Network {
		lockedParent, err := inventory.NetworkParentID(ctx, tx, actor.Tenant, input.Account, input.Region, input.AssetID, input.Kind)
		if err != nil {
			return target, parent, "", err
		}
		if lockedParent != parentID || (parentID != "" && parent.OwnershipRevision < 1) {
			return target, parent, "", identity.ErrAccountStale
		}
		if beforeWrite && parentID != "" {
			blocked, err := inventory.NetworkDeletionActive(ctx, tx, parent.ScopeID, parentID)
			if err != nil {
				return target, parent, "", err
			}
			if blocked {
				return target, parent, "", ErrConflict
			}
		}
	}
	if !(provider.NetworkDeletionTarget{ID: input.AssetID, ParentID: parentID}).Valid(target.Provider, input.Kind) {
		return target, parent, "", ErrInvalid
	}
	return target, parent, parentID, nil
}

func checkNetworkDeletionDependencies(ctx context.Context, tx *sql.Tx, tenant string, input NetworkDeletion, target inventory.MutationTarget, exclude string) error {
	var current, blocked bool
	err := tx.QueryRowContext(ctx, `SELECT s.credential_revision=$7, EXISTS (
		SELECT 1 FROM cmp_inventory_scopes AS scope JOIN cmp_inventory_objects AS o USING(scope_id)
		WHERE (scope.tenant_id,scope.account_id,scope.region)=($2,$3,$4) AND o.removed_at IS NULL
		  AND o.links @> jsonb_build_array(jsonb_build_object('kind',$5::text,'id',$6::text))
		  AND NOT ($5='security-group' AND scope.kind='security-group-rule'
		    AND o.links @> jsonb_build_array(jsonb_build_object('kind','security-group','id',$6::text,'relation','in_security_group')))
		UNION ALL
		SELECT 1 FROM cmp_instance_operations WHERE (scope_id,external_id)=($1,$6)
		  AND operation_id<>$8 AND status IN `+activeOperationStatuses+`
		UNION ALL
		SELECT 1 FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE (i.parent_scope_id,i.parent_id)=($1,$6) AND o.status IN `+activeOperationStatuses+`
		UNION ALL
		SELECT 1 FROM cmp_network_deletion_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
		WHERE (i.parent_scope_id,i.parent_id)=($1,$6) AND o.status IN `+activeOperationStatuses+`
		UNION ALL
		SELECT 1 FROM cmp_instance_operations AS o JOIN cmp_instance_launches AS launch USING(operation_id)
		WHERE (o.tenant_id,o.account_id,o.region)=($2,$3,$4)
		  AND (($5='subnet' AND launch.network_id=$6) OR ($5='security-group' AND launch.security_group_id=$6))
		  AND o.action='create' AND o.status IN `+activeOperationStatuses+`
		UNION ALL
		SELECT 1 FROM cmp_instance_catalog_items AS item JOIN cmp_instance_catalog_versions AS version
		  USING(tenant_id,project_id,template_id,version)
		WHERE (version.tenant_id,version.account_id,version.region)=($2,$3,$4)
		  AND (($5='subnet' AND version.network_id=$6) OR ($5='security-group' AND version.security_group_id=$6))
		  AND item.enabled
	) FROM cmp_inventory_scopes AS s WHERE s.scope_id=$1`,
		target.ScopeID, tenant, input.Account, input.Region, input.Kind, input.AssetID, target.CredentialRevision, exclude).Scan(&current, &blocked)
	if err != nil {
		return err
	}
	if !current {
		return identity.ErrAccountStale
	}
	if blocked {
		return ErrConflict
	}
	return nil
}

func checkNetworkDeletion(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID, parentScopeID, parentRevision int64
	var parentID string
	var ruleRemoval bool
	var observer creationObserver
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,COALESCE(d.parent_scope_id,0),COALESCE(d.parent_id,''),d.parent_revision,d.rule_removal_confirmed,
		i.observation_reviewer,i.observation_membership_revision,i.observation_credential_revision
		FROM cmp_network_deletion_intents AS d JOIN cmp_network_intents AS i USING(operation_id)
		JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, w.ID).
		Scan(&scopeID, &parentScopeID, &parentID, &parentRevision, &ruleRemoval, &observer.principal, &observer.membership, &observer.credential); err != nil {
		return err
	}
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return err
		}
	}
	actor, membership, credential := w.actor, w.membership, w.credential
	if !beforeWrite && observer.principal != "" {
		actor.Principal, membership, credential = observer.principal, observer.membership, observer.credential
		if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Approve); err != nil {
			return err
		}
	}
	input := NetworkDeletion{Account: w.Account, Region: w.Region, AssetID: w.AssetID, Kind: w.ResourceKind}
	target, parent, observedParent, err := resolveNetworkDeletion(ctx, tx, actor, input, beforeWrite)
	if errors.Is(err, ErrInvalid) {
		return identity.ErrAccountStale
	}
	if err != nil {
		return err
	}
	if ruleRemoval != (w.ResourceKind == provider.SecurityGroup) || target.ScopeID != scopeID || target.OwnershipRevision != w.ownership || target.CredentialRevision != credential ||
		target.MembershipRevision != membership || parent.ScopeID != parentScopeID ||
		parent.OwnershipRevision != parentRevision || observedParent != parentID {
		return identity.ErrAccountStale
	}
	w.networkDeletion = provider.NetworkDeletionTarget{ID: w.AssetID, ParentID: parentID}
	w.networkDeletionScope = scopeID
	if beforeWrite {
		return checkNetworkDeletionDependencies(ctx, tx, actor.Tenant, input, target, w.ID)
	}
	return nil
}
