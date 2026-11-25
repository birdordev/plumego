package operations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type ConnectOperator func(context.Context, identity.CloudAccountBinding) (provider.InstanceOperator, error)

type work struct {
	Operation
	actor                             identity.Scope
	binding                           identity.CloudAccountBinding
	ownership, credential, membership int64
	volumeRevision                    int64
	ruleScope                         int64
	launch                            launch
	network                           networkIntent
}

const accountingTimeout = 5 * time.Second

func (s Store) RunWorker(ctx context.Context, connect ConnectOperator) error {
	idle := time.NewTicker(time.Second)
	defer idle.Stop()
	for ctx.Err() == nil {
		worked, err := s.step(ctx, connect)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if !worked {
			select {
			case <-ctx.Done():
				return nil
			case <-idle.C:
			}
		}
	}
	return nil
}

func (s Store) dbTime(ctx context.Context) (time.Time, error) {
	var stamp time.Time
	err := s.db.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&stamp)
	return stamp, err
}

func (s Store) step(ctx context.Context, connect ConnectOperator) (bool, error) {
	now, err := s.dbTime(ctx)
	if err != nil {
		return false, err
	}
	tasks, err := s.queue.Reserve(ctx, mq.ReserveOptions{Topics: []string{topic}, Limit: 1, Lease: 90 * time.Second, ConsumerID: rand.Text(), Now: now})
	if err != nil || len(tasks) == 0 {
		return false, err
	}
	task := tasks[0]
	attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
	retry, err := s.process(attempt, task, connect)
	cancel()
	if errors.Is(err, mq.ErrLeaseLost) {
		return true, nil
	}
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return true, err
		}
		retry = true
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer stop()
	now, err = s.dbTime(cleanup)
	if err != nil {
		return true, err
	}
	if retry {
		err = s.queue.Release(cleanup, task.ID, task.LeaseOwner, mq.ReleaseOptions{Now: now, RetryAt: now.Add(15 * time.Second)})
	} else {
		err = s.queue.Ack(cleanup, task.ID, task.LeaseOwner, now)
	}
	if errors.Is(err, mq.ErrLeaseLost) {
		return true, nil
	}
	return true, err
}

func terminal(status string) bool {
	return status == "succeeded" || status == "failed" || status == "needs_review"
}

func (s Store) process(ctx context.Context, task mq.Task, connect ConnectOperator) (bool, error) {
	w, err := s.prepare(ctx, task, false)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
		return false, s.endUnavailable(ctx, task, w)
	}
	if err != nil || terminal(w.Status) || w.Status == "awaiting_approval" {
		return false, err
	}

	client, err := connect(ctx, w.binding)
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) || task.Attempts >= task.MaxAttempts {
			return false, s.endUnavailable(ctx, task, w)
		}
		return true, nil
	}
	scope := provider.Scope{Provider: w.binding.Provider, RemoteIdentity: w.binding.RemoteIdentity, Region: w.Region, Kind: provider.Instance}
	if w.Action == "create" {
		return s.processCreation(ctx, task, w, client, scope)
	}
	if isNetworkCreation(w.Action) {
		return s.processNetwork(ctx, task, w, client, scope)
	}
	if w.Action == revokeSecurityRule {
		return s.processSecurityRuleRevocation(ctx, task, w, client, scope)
	}
	if volumeAction(w.Action) {
		return s.processVolume(ctx, task, w, client, scope)
	}
	if w.Status == "queued" {
		armed, err := s.prepare(ctx, task, true)
		if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) || errors.Is(err, ErrConflict) {
			return false, s.endUnavailable(ctx, task, armed)
		}
		if err != nil {
			return false, err
		}
		if armed.Status != "applying" {
			return false, mq.ErrLeaseLost
		}
		requestID, callErr := client.ApplyInstance(ctx, scope, w.AssetID, w.Action)
		state, reason := "observing", "provider_accepted"
		switch {
		case provider.IsFailure(callErr, provider.DeletionBlocked):
			state, reason = "failed", "deletion_preconditions_unconfirmed"
		case provider.IsFailure(callErr, provider.DeletionDisabled):
			state, reason = "failed", "provider_deletion_disabled"
		case callErr != nil:
			reason = "submission_outcome_unknown"
		}
		if w.Action == "reboot" {
			_, observesReboot := client.(provider.RebootObserver)
			if callErr != nil || !observesReboot || !requestIDPattern.MatchString(requestID) {
				state, reason = "needs_review", "reboot_completion_requires_evidence"
			}
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
		defer cancel()
		err = s.transition(cleanup, task, "applying", state, reason, requestID)
		return !terminal(state), err
	}

	if w.Action == "reboot" {
		return s.observeReboot(ctx, task, w, client, scope)
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
	}
	if w.Action == "delete" && w.ProviderRequestID != "" {
		if observer, ok := client.(provider.DeletionObserver); ok {
			return s.observeDeletion(ctx, task, w, scope, observer)
		}
	}
	object, err := client.Observe(ctx, scope, w.AssetID)
	if err == nil && object.ID == w.AssetID && reached(w.Action, object.Status) {
		if w.Action == "delete" {
			return false, s.confirmDeletion(ctx, task, w, "desired_state_observed")
		}
		return false, s.transition(ctx, task, w.Status, "succeeded", "desired_state_observed", "")
	}
	if task.Attempts >= task.MaxAttempts {
		return false, s.transition(ctx, task, w.Status, "needs_review", "observation_budget_exhausted", "")
	}
	return true, nil
}

func (s Store) observeReboot(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	observer, ok := client.(provider.RebootObserver)
	if !ok || w.Status != "observing" || w.ProviderRequestID == "" {
		return false, s.transition(ctx, task, w.Status, "needs_review", "reboot_completion_requires_evidence", "")
	}
	result, err := observer.ObserveReboot(ctx, scope, w.AssetID, w.ProviderRequestID)
	if err == nil {
		switch result {
		case provider.ActionSucceeded:
			return false, s.transition(ctx, task, w.Status, "succeeded", "provider_reboot_confirmed", "")
		case provider.ActionFailed:
			return false, s.transition(ctx, task, w.Status, "failed", "provider_reboot_failed", "")
		}
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.Unauthorized) ||
		provider.IsFailure(err, provider.Unsupported) || provider.IsFailure(err, provider.InvalidResponse) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "reboot_completion_requires_evidence", "")
	}
	return true, nil
}

func (s Store) observeDeletion(ctx context.Context, task mq.Task, w work, scope provider.Scope, observer provider.DeletionObserver) (bool, error) {
	result, err := observer.ObserveDeletion(ctx, scope, w.AssetID, w.ProviderRequestID)
	if err == nil {
		switch result {
		case provider.ActionSucceeded:
			return false, s.confirmDeletion(ctx, task, w, "provider_deletion_confirmed")
		case provider.ActionFailed:
			return false, s.transition(ctx, task, w.Status, "failed", "provider_deletion_failed", "")
		}
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.InvalidResponse) ||
		provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.Unsupported) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "deletion_completion_requires_evidence", "")
	}
	return true, nil
}

func reached(action, status string) bool {
	switch strings.ToLower(status) {
	case "terminated":
		return action == "delete"
	case "active", "running":
		return action == "start"
	case "shutoff", "stopped":
		return action == "stop"
	}
	return false
}

func (s Store) endUnavailable(ctx context.Context, task mq.Task, w work) error {
	if w.Status == "queued" {
		return s.transition(ctx, task, w.Status, "failed", "dispatch_unavailable", "")
	}
	return s.transition(ctx, task, w.Status, "needs_review", "observation_unavailable", "")
}

func (s Store) prepare(ctx context.Context, task mq.Task, arm bool) (work, error) {
	var w work
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+",tenant_id,project_id,principal_id,ownership_revision,credential_revision,membership_revision,volume_revision FROM cmp_instance_operations WHERE operation_id=$1 FOR UPDATE", task.ID), &w.Operation,
			&w.actor.Tenant, &w.actor.Project, &w.actor.Principal, &w.ownership, &w.credential, &w.membership, &w.volumeRevision)
		if err != nil {
			return err
		}
		if w.actor.Tenant != task.TenantID || string(task.Payload) != task.ID {
			return mq.ErrLeaseLost
		}
		if terminal(w.Status) {
			return nil
		}
		if arm && w.Status != "queued" {
			return mq.ErrLeaseLost
		}
		if err := checkOperationTarget(ctx, tx, &w); err != nil {
			return err
		}
		w.binding, err = identity.NewStore(tx).CloudAccountBinding(ctx, w.actor.Tenant, w.Account)
		if err != nil {
			return err
		}
		if arm {
			if err := writeTransition(ctx, tx, task.ID, "queued", "applying", "dispatch_recorded", ""); err != nil {
				return err
			}
			if w.Action == "create" {
				if _, err := tx.ExecContext(ctx, "UPDATE cmp_instance_launches SET dispatch_started_at=clock_timestamp() WHERE operation_id=$1", w.ID); err != nil {
					return err
				}
			}
			w.Status = "applying"
		}
		return nil
	})
	return w, err
}

func checkOperationTarget(ctx context.Context, tx *sql.Tx, w *work) error {
	if w.Action == "create" {
		return checkLaunch(ctx, tx, w, w.Status == "queued" || w.Status == "awaiting_approval")
	}
	if isNetworkCreation(w.Action) {
		return checkNetworkIntent(ctx, tx, w, w.Status == "queued" || w.Status == "awaiting_approval")
	}
	if w.Action == revokeSecurityRule {
		return checkSecurityRuleRevocation(ctx, tx, w)
	}
	resolve := inventory.ResolveInstanceMutation
	if w.Action == "delete" && w.Status != "queued" && w.Status != "awaiting_approval" {
		resolve = inventory.ResolveInstanceObservation
	}
	target, err := resolve(ctx, tx, w.actor, w.Account, w.Region, w.AssetID)
	if err != nil {
		return err
	}
	if target.CredentialRevision != w.credential || target.MembershipRevision != w.membership || target.OwnershipRevision != w.ownership {
		return identity.ErrAccountStale
	}
	if volumeAction(w.Action) {
		_, err := resolveOperationVolume(ctx, tx, w.actor, Input{Account: w.Account, Region: w.Region, Action: w.Action,
			VolumeID: w.VolumeID, Device: w.Device, VolumeRevision: w.volumeRevision}, target.Provider)
		return err
	}
	if w.Status == "queued" || w.Status == "awaiting_approval" {
		return checkDeletionProtection(ctx, tx, target.ScopeID, w.AssetID, w.Action)
	}
	return nil
}

func (s Store) withLease(ctx context.Context, task mq.Task, fn func(*sql.Tx) error) error {
	return dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM cmp_operation_tasks WHERE id=$1 AND tenant_id=$2 AND lease_owner=$3
			AND status='leased' AND lease_until>clock_timestamp() FOR UPDATE`, task.ID, task.TenantID, task.LeaseOwner).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return mq.ErrLeaseLost
		}
		if err != nil {
			return err
		}
		return fn(tx)
	})
}

var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)

func (s Store) transition(ctx context.Context, task mq.Task, from, to, reason, requestID string) error {
	if !requestIDPattern.MatchString(requestID) {
		requestID = ""
	}
	return s.withLease(ctx, task, func(tx *sql.Tx) error {
		return writeTransition(ctx, tx, task.ID, from, to, reason, requestID)
	})
}

func writeTransition(ctx context.Context, tx *sql.Tx, id, from, to, reason, requestID string) error {
	var changed string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_instance_operations SET status=$3,outcome_reason=$4,
		provider_request_id=COALESCE(NULLIF($5,''),provider_request_id),updated_at=statement_timestamp()
		WHERE operation_id=$1 AND status=$2 RETURNING operation_id`, id, from, to, reason, requestID).Scan(&changed)
	if errors.Is(err, sql.ErrNoRows) {
		return mq.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if err := recordOperationEvent(ctx, tx, id, to); err != nil {
		return err
	}
	if to == "failed" {
		_, err = tx.ExecContext(ctx, `UPDATE cmp_instance_launches SET released_at=statement_timestamp()
			WHERE operation_id=$1 AND released_at IS NULL`, id)
	}
	return err
}
