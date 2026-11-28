package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

var bareMetalID = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)

const BareMetalPowerMigration = "0118_bare_metal_power"

const (
	powerOnBareMetal  = "power-on-bare-metal"
	shutdownBareMetal = "shutdown-bare-metal"
)

func MigrateBareMetalPower(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, BareMetalPowerMigration)
}

type BareMetalChangeInput struct {
	Account string `json:"account"`
	Site    string `json:"region"`
	AssetID string `json:"asset_id"`
	Action  string `json:"action"`
}

func (s Store) SubmitBareMetalPower(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input BareMetalChangeInput) (Operation, error) {
	if !bareMetalID.MatchString(input.AssetID) || (input.Action != powerOnBareMetal && input.Action != shutdownBareMetal) {
		return Operation{}, ErrInvalid
	}
	return s.submitBareMetalChange(ctx, token, scope, key, revision, input)
}

func (s Store) submitBareMetalChange(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input BareMetalChangeInput) (Operation, error) {
	payload, err := json.Marshal(struct {
		BareMetalChangeInput
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte("bare-metal-power\x00"), payload...))
	return s.submitApprovedAssetChange(ctx, token, scope, key, revision, provider.BareMetal,
		input.Account, input.Site, input.AssetID, input.Action, "", digest, nil)
}

func checkBareMetalPower(ctx context.Context, tx *sql.Tx, w *work) error {
	if w.Action != powerOnBareMetal && w.Action != shutdownBareMetal && !bareMetalPXEAction(w.Action) {
		return ErrConflict
	}
	if w.Status == "queued" {
		if err := checkDispatchApproval(ctx, tx, w, true); err != nil {
			return err
		}
	}
	actor, membership, credential := w.actor, w.membership, w.credential
	resolve := inventory.ResolveBareMetalMutation
	if w.Status != "queued" && w.Status != "awaiting_approval" {
		resolve = inventory.ResolveBareMetalObservation
		var reviewer creationObserver
		err := tx.QueryRowContext(ctx, `SELECT reviewer_id,membership_revision,credential_revision
			FROM cmp_instance_observation_retries WHERE operation_id=$1 ORDER BY created_at DESC, reviewer_id DESC, evidence_ref DESC LIMIT 1`, w.ID).
			Scan(&reviewer.principal, &reviewer.membership, &reviewer.credential)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			actor.Principal, membership, credential = reviewer.principal, reviewer.membership, reviewer.credential
			if _, err := identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Approve); err != nil {
				return err
			}
		}
	}
	target, err := resolve(ctx, tx, actor, w.Account, w.Region, w.AssetID)
	if err != nil {
		return err
	}
	if target.CredentialRevision != credential || target.MembershipRevision != membership || target.OwnershipRevision != w.ownership {
		return identity.ErrAccountStale
	}
	if w.Status == "queued" || w.Status == "awaiting_approval" {
		if err := checkBareMetalDeploymentFence(ctx, tx, w.AssetID, w.ID); err != nil {
			return err
		}
		if w.Action == powerOnBareMetal {
			return checkDeploymentInstaller(ctx, tx, w)
		}
	}
	if w.Action == powerOnBareMetal && w.Status != "queued" && w.Status != "awaiting_approval" {
		return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_bare_metal_deployments WHERE boot_operation=$1)`, w.ID).
			Scan(&w.bareMetalDeployment)
	}
	return nil
}

func (s Store) processBareMetalPower(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	if w.Status == "queued" {
		if bareMetalPXEAction(w.Action) {
			operator, ok := client.(provider.BareMetalBootOperator)
			if !ok {
				return false, s.endUnavailable(ctx, task, w)
			}
			apply, err := operator.PrepareBareMetalPXE(ctx, scope, w.AssetID, w.Action)
			return s.dispatchSingleMutation(ctx, task, w, apply, err, "bare_metal_pxe")
		}
		if w.bareMetalDeployment {
			operator, ok := client.(provider.BareMetalDeploymentOperator)
			if !ok {
				return false, s.endUnavailable(ctx, task, w)
			}
			apply, err := operator.PrepareBareMetalDeploymentBoot(ctx, scope, w.AssetID)
			return s.dispatchSingleMutation(ctx, task, w, apply, err, "bare_metal_deployment")
		}
		if _, ok := client.(provider.InstancePowerPreparer); !ok {
			return false, s.endUnavailable(ctx, task, w)
		}
		return s.dispatchInstance(ctx, task, w, client, scope)
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, w.Status, "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	if bareMetalPXEAction(w.Action) {
		return s.observeBareMetalPXE(ctx, task, w, client, scope)
	}
	desired := "off"
	if w.Action == powerOnBareMetal {
		desired = "on"
	}
	var matched bool
	if w.bareMetalDeployment {
		if operator, ok := client.(provider.BareMetalDeploymentOperator); ok {
			observed, err := operator.ObserveBareMetalDeploymentBoot(ctx, scope, w.AssetID)
			matched = err == nil && observed
		}
	} else {
		object, err := client.Observe(ctx, scope, w.AssetID)
		matched = err == nil && object.ID == w.AssetID && object.Status == desired
	}
	if matched {
		return false, s.confirmBareMetalPower(ctx, task, w, desired)
	}
	if task.Attempts >= task.MaxAttempts {
		return false, s.transition(ctx, task, w.Status, "needs_review", "observation_budget_exhausted", "")
	}
	return true, nil
}

func (s Store) confirmBareMetalPower(ctx context.Context, task mq.Task, w work, status string) error {
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		scopeID, err := inventory.LockBareMetalScope(ctx, tx, w.actor.Tenant, w.Account, w.Region)
		if err != nil {
			return err
		}
		if err := checkBareMetalPower(ctx, tx, &w); err != nil {
			return err
		}
		if err := inventory.RecordObservedAssetStatus(ctx, tx, scopeID, w.AssetID, status); err != nil {
			return err
		}
		return writeTransition(ctx, tx, task.ID, w.Status, "succeeded", "desired_state_observed", "")
	})
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
