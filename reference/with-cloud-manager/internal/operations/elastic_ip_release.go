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

//go:embed 0038_elastic_ip_release.sql
var elasticIPReleaseSchema string

const releaseElasticIP = "release-elastic-ip"

type ElasticIPRelease struct {
	Account       string `json:"account"`
	Region        string `json:"region"`
	AssetID       string `json:"asset_id"`
	PublicAddress string `json:"public_address"`
}

func (s Store) SubmitElasticIPRelease(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input ElasticIPRelease) (Operation, error) {
	return s.submitElasticIPChange(ctx, token, scope, key, revision, releaseElasticIP, ElasticIPBinding{ElasticIPRelease: input})
}

func (s Store) submitElasticIPChange(ctx context.Context, token string, scope identity.Scope, key string, revision int64, action string, input ElasticIPBinding) (Operation, error) {
	address := provider.ElasticIPBinding{ElasticIPTarget: provider.ElasticIPTarget{ID: input.AssetID, Address: input.PublicAddress},
		InstanceID: input.InstanceID, PortID: input.PortID, Bind: action == bindElasticIP}
	if revision < 1 || !operationKey.MatchString(key) || !operationKey.MatchString(input.Account) || !operationKey.MatchString(input.Region) ||
		!operationKey.MatchString(input.AssetID) || !address.ValidIPv4() {
		return Operation{}, ErrInvalid
	}
	if isElasticIPBinding(action) && (!operationKey.MatchString(input.InstanceID) || input.InstanceRevision < 1 ||
		(input.PortID != "" && !elasticIPPortID.MatchString(input.PortID))) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		ElasticIPBinding
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
		target, err := inventory.ResolveNetworkMutation(ctx, tx, actor, input.Account, input.Region, input.AssetID, provider.ElasticIP)
		if err != nil {
			return err
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		name, err := elasticIPInventoryName(ctx, tx, target, address, true)
		if err != nil {
			return err
		}
		var instanceScope int64
		if isElasticIPBinding(action) {
			instanceScope, err = resolveBindingInstance(ctx, tx, actor, target, input.Account, input.Region, address, input.InstanceRevision, true, "")
			if err != nil {
				return err
			}
		}
		id := rand.Text()
		err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
			(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,status,resource_kind,
			 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'awaiting_approval','elastic-ip',$10,$11,$12,$13,$14)
			ON CONFLICT DO NOTHING RETURNING `+operationFields, id, actor.Tenant, actor.Project, actor.Principal, target.ScopeID,
			input.AssetID, input.Account, input.Region, action, key, digest[:], target.CredentialRevision,
			target.MembershipRevision, revision), &op)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_network_intents
			(operation_id,launch_token,name,release_address,binding_address,parent_scope_id,parent_id,parent_revision,elastic_ip_port)
			VALUES ($1,$2,$3,CASE WHEN $5::bigint=0 THEN $4::inet END,CASE WHEN $5<>0 THEN $4::inet END,
				NULLIF($5,0),NULLIF($6,''),$7,$8)`,
			id, rand.Text(), name, input.PublicAddress, instanceScope, input.InstanceID, input.InstanceRevision, input.PortID); err != nil {
			return err
		}
		return s.recordSubmission(ctx, tx, actor.Tenant, op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func elasticIPInventoryName(ctx context.Context, tx *sql.Tx, target inventory.MutationTarget, address provider.ElasticIPBinding, beforeWrite bool) (string, error) {
	unbound, bound := elasticIPStatuses(target.Provider)
	if unbound == "" {
		return "", ErrInvalid
	}
	expected := unbound
	unbinding := address.InstanceID != "" && !address.Bind
	if unbinding {
		expected = bound
	}
	var name string
	err := tx.QueryRowContext(ctx, `SELECT o.name FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE (o.scope_id,o.external_id)=($1,$2) AND o.public_addresses=jsonb_build_array($3::text)
		AND (NOT $4::bool OR (o.status=$5 AND s.credential_revision=$6 AND (
			(NOT $7::bool AND o.links='[]'::jsonb) OR
			($7 AND o.links @> jsonb_build_array(jsonb_build_object('kind','instance','id',$8::text,'relation','bound_to'))
				AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(o.links) AS link
				WHERE link->>'relation'='bound_to' AND (link->>'kind',link->>'id') IS DISTINCT FROM ('instance',$8::text)))
		)))`,
		target.ScopeID, address.ID, address.Address, beforeWrite, expected, target.CredentialRevision, unbinding, address.InstanceID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrConflict
	}
	return name, err
}

func checkElasticIPChange(ctx context.Context, tx *sql.Tx, w *work) error {
	var scopeID, instanceScope, instanceRevision int64
	var observer creationObserver
	var name string
	w.elasticIP.ID = w.AssetID
	w.elasticIP.Bind = w.Action == bindElasticIP
	if err := tx.QueryRowContext(ctx, `SELECT o.scope_id,i.name,COALESCE(host(i.binding_address),host(i.release_address)),i.observation_reviewer,
		i.observation_membership_revision,i.observation_credential_revision,COALESCE(i.parent_scope_id,0),COALESCE(i.parent_id,''),
		i.parent_revision,i.elastic_ip_port,i.launch_token
		FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, w.ID).
		Scan(&scopeID, &name, &w.elasticIP.Address, &observer.principal, &observer.membership, &observer.credential,
			&instanceScope, &w.elasticIP.InstanceID, &instanceRevision, &w.elasticIP.PortID, &w.elasticIP.ClientToken); err != nil {
		return err
	}
	beforeWrite := w.Status == "queued" || w.Status == "awaiting_approval"
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return err
		}
	}
	actor, memberRevision, credentialRevision := w.actor, w.membership, w.credential
	if !beforeWrite && observer.principal != "" {
		actor.Principal, memberRevision, credentialRevision = observer.principal, observer.membership, observer.credential
		if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Approve); err != nil {
			return err
		}
	}
	var target inventory.MutationTarget
	var err error
	if beforeWrite {
		target, err = inventory.ResolveNetworkMutation(ctx, tx, actor, w.Account, w.Region, w.AssetID, provider.ElasticIP)
	} else {
		target, err = inventory.ResolveElasticIPObservation(ctx, tx, actor, w.Account, w.Region, w.AssetID)
	}
	if err != nil {
		return err
	}
	if target.ScopeID != scopeID || target.OwnershipRevision != w.ownership ||
		target.CredentialRevision != credentialRevision || target.MembershipRevision != memberRevision {
		return identity.ErrAccountStale
	}
	currentName, err := elasticIPInventoryName(ctx, tx, target, w.elasticIP, beforeWrite)
	if err != nil {
		return err
	}
	if beforeWrite && currentName != name {
		return identity.ErrAccountStale
	}
	if isElasticIPBinding(w.Action) {
		resolved, err := resolveBindingInstance(ctx, tx, actor, target, w.Account, w.Region, w.elasticIP, instanceRevision, beforeWrite, w.ID)
		if err != nil {
			return err
		}
		if resolved != instanceScope {
			return identity.ErrAccountStale
		}
	}
	w.elasticIPScope = scopeID
	return nil
}

func (s Store) processElasticIPChange(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	scope.Kind = provider.ElasticIP
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	if isElasticIPBinding(w.Action) {
		return s.processElasticIPBinding(ctx, task, w, client, scope)
	}
	releaser, ok := client.(provider.ElasticIPReleaser)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		apply, err := releaser.PrepareElasticIPRelease(ctx, scope, w.elasticIP.ElasticIPTarget)
		return s.dispatchSingleMutation(ctx, task, w, apply, err, "elastic_ip_release")
	}
	result, err := releaser.ObserveElasticIPRelease(ctx, scope, w.elasticIP.ElasticIPTarget)
	settled, settleErr := s.recordReleaseEvidence(ctx, task, w, err == nil && result == provider.ActionSucceeded)
	if settled || settleErr != nil {
		return false, settleErr
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "elastic_ip_release_requires_evidence", "")
	}
	return true, nil
}

func (s Store) recordReleaseEvidence(ctx context.Context, task mq.Task, w work, absent bool) (bool, error) {
	var observations int16
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		if err := checkElasticIPChange(ctx, tx, &w); err != nil {
			return err
		}
		var err error
		observations, err = recordAbsenceObservation(ctx, tx, w.ID, absent)
		if err != nil || observations < 2 {
			return err
		}
		if err := inventory.MarkResourceDeleted(ctx, tx, w.elasticIPScope, w.AssetID); err != nil {
			return err
		}
		return writeTransition(ctx, tx, w.ID, w.Status, "succeeded", "elastic_ip_absence_confirmed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return true, s.endUnavailable(ctx, task, w)
	}
	return observations == 2, err
}
