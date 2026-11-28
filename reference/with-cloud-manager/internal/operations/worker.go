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
	bareMetalDeployment      bool
	retirement               provider.InstanceRetirement
	cacheRename              provider.CacheRename
	databaseRename           provider.DatabaseRename
	databaseRecovery         provider.DatabaseRecovery
	volumeRelease            provider.VolumeRelease
	volumeSpec               provider.VolumeSpec
	bucketVersioning         provider.BucketVersioningChange
	bucketEncryptionLocation string
	instanceResize           provider.InstanceResize
	volumeResize             provider.VolumeResize
	workloadScale            provider.WorkloadScaleChange
	Operation
	actor                             identity.Scope
	binding                           identity.CloudAccountBinding
	ownership, credential, membership int64
	volumeRevision                    int64
	ruleScope                         int64
	elasticIPScope                    int64
	snapshotScope                     int64
	restore                           provider.SnapshotRestoreSpec
	snapshot                          provider.SnapshotTarget
	elasticIP                         provider.ElasticIPBinding
	launch                            launch
	network                           networkIntent
	networkDeletion                   provider.NetworkDeletionTarget
	networkDeletionScope              int64
	imageCopy                         imageCopyIntent
	rename                            provider.LoadBalancerRename
	backendWeight                     provider.LoadBalancerBackendWeight
	backendDetach                     provider.LoadBalancerBackendTarget
	backendDetachBinding              string
	backendAttach                     provider.LoadBalancerBackendAttach
	dnsDeletion                       provider.DNSRecordDeletion
	dnsUpdate                         provider.DNSRecordUpdate
	dnsStatus                         provider.DNSRecordStatusChange
	dnsCreation                       dnsRecordCreation
	backendAttachBinding              string
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
	tasks, err := s.reserveWorkerTask(ctx, mq.ReserveOptions{Topics: []string{topic}, Limit: 1, Lease: 90 * time.Second, ConsumerID: rand.Text(), Now: now})
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

func (s Store) reserveWorkerTask(ctx context.Context, opts mq.ReserveOptions) ([]mq.Task, error) {
	if s.workerTenant != "" {
		return s.queue.ReserveTenant(ctx, opts, s.workerTenant)
	}
	return s.queue.Reserve(ctx, opts)
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

	if w.Action == scaleWorkload {
		return s.processWorkloadScale(ctx, task, w)
	}
	client, err := connect(ctx, w.binding)
	if err != nil {
		if errors.Is(err, identity.ErrForbidden) || task.Attempts >= task.MaxAttempts {
			return false, s.endUnavailable(ctx, task, w)
		}
		return true, nil
	}
	scope := provider.Scope{Provider: w.binding.Provider, RemoteIdentity: w.binding.RemoteIdentity, Region: w.Region, Kind: provider.Instance}
	if w.ResourceKind == provider.BareMetal {
		if closer, ok := client.(interface{ Close() }); ok {
			defer closer.Close()
		}
		scope.Kind = provider.BareMetal
		return s.processBareMetalPower(ctx, task, w, client, scope)
	}
	if w.Action == retireInstance {
		return s.processInstanceRetirement(ctx, task, w, client, scope)
	}
	if w.Action == "create" {
		return s.processCreation(ctx, task, w, client, scope)
	}
	if w.Action == copyImage {
		return s.processImageCopy(ctx, task, w, client, connect)
	}
	if w.Action == createImage {
		return s.processImageCreation(ctx, task, w, client, scope)
	}
	if isNetworkDeletion(w.Action) {
		return s.processNetworkDeletion(ctx, task, w, client, scope)
	}
	if w.Action == deleteImage {
		return s.processImageDeletion(ctx, task, w, client, scope)
	}
	if w.Action == deleteSnapshot {
		return s.processSnapshotDeletion(ctx, task, w, client, scope)
	}
	if w.Action == rollbackSnapshot {
		return s.processSnapshotRollback(ctx, task, w, client, scope)
	}
	if w.Action == createVolume {
		return s.processVolumeCreation(ctx, task, w, client, scope)
	}
	if w.Action == restoreSnapshot {
		return s.processSnapshotRestore(ctx, task, w, client, scope)
	}
	if w.Action == createSnapshot {
		return s.processSnapshot(ctx, task, w, client, scope)
	}
	if isNetworkCreation(w.Action) {
		return s.processNetwork(ctx, task, w, client, scope)
	}
	if w.Action == revokeSecurityRule {
		return s.processSecurityRuleRevocation(ctx, task, w, client, scope)
	}
	if w.Action == releaseElasticIP || isElasticIPBinding(w.Action) {
		return s.processElasticIPChange(ctx, task, w, client, scope)
	}
	if w.Action == setLoadBalancerBackendWeight {
		return s.processLoadBalancerBackendWeight(ctx, task, w, client, scope)
	}
	if w.Action == detachLoadBalancerBackend {
		return s.processLoadBalancerBackendDetach(ctx, task, w, client, scope)
	}
	if w.Action == attachLoadBalancerBackend {
		return s.processLoadBalancerBackendAttach(ctx, task, w, client, scope)
	}
	if w.Action == createDNSRecord {
		return s.processDNSRecordCreation(ctx, task, w, client, scope)
	}
	if w.Action == deleteDNSRecord {
		return s.processDNSRecordDeletion(ctx, task, w, client, scope)
	}
	if w.Action == updateDNSRecord {
		return s.processDNSRecordUpdate(ctx, task, w, client, scope)
	}
	if w.Action == setDNSRecordStatus {
		return s.processDNSRecordStatus(ctx, task, w, client, scope)
	}
	if isLoadBalancerRename(w.Action) {
		return s.processLoadBalancerRename(ctx, task, w, client, scope)
	}
	if w.Action == renameCacheInstance {
		return s.processCacheRename(ctx, task, w, client, scope)
	}
	if w.Action == releaseVolume {
		return s.processVolumeRelease(ctx, task, w, client, scope)
	}
	if w.Action == enableBucketEncryption {
		return s.processBucketEncryption(ctx, task, w, client, scope)
	}
	if w.Action == setBucketVersioning {
		return s.processBucketVersioning(ctx, task, w, client, scope)
	}
	if w.Action == resizeInstance {
		return s.processInstanceResize(ctx, task, w, client, scope)
	}
	if w.Action == resizeVolume {
		return s.processVolumeResize(ctx, task, w, client, scope)
	}
	if w.Action == renameDatabaseInstance {
		return s.processDatabaseRename(ctx, task, w, client, scope)
	}
	if w.Action == recoverDatabaseInstance {
		return s.processDatabaseRecovery(ctx, task, w, client, scope)
	}
	if volumeAction(w.Action) {
		return s.processVolume(ctx, task, w, client, scope)
	}
	if w.Status == "queued" {
		return s.dispatchInstance(ctx, task, w, client, scope)
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
	if w.Action == "delete" && publicCloudProvider(scope.Provider) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "deletion_completion_requires_evidence", "")
	}
	object, err := client.Observe(ctx, scope, w.AssetID)
	if err == nil && object.ID == w.AssetID && reached(w.Action, object.Status) {
		if w.Action == "delete" {
			return false, s.confirmDeletion(ctx, task, w, "desired_state_observed")
		}
		if publicCloudProvider(scope.Provider) || reviewedPowerObservation(w.Operation) {
			return false, s.confirmInstancePower(ctx, task, w, object.Status)
		}
		return false, s.transition(ctx, task, w.Status, "succeeded", "desired_state_observed", "")
	}
	if task.Attempts >= task.MaxAttempts {
		return false, s.transition(ctx, task, w.Status, "needs_review", "observation_budget_exhausted", "")
	}
	return true, nil
}

func (s Store) dispatchInstance(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	send := func(ctx context.Context) (string, error) {
		return client.ApplyInstance(ctx, scope, w.AssetID, w.Action)
	}
	var prepareSend func() (func(context.Context) (string, error), error)
	if preparer, ok := client.(provider.InstanceDeletionPreparer); ok && w.Action == "delete" {
		prepareSend = func() (func(context.Context) (string, error), error) {
			return preparer.PrepareInstanceDeletion(ctx, scope, w.AssetID)
		}
	} else if preparer, ok := client.(provider.InstancePowerPreparer); ok && (instancePowerAction(w.Action) || w.ResourceKind == provider.BareMetal) {
		prepareSend = func() (func(context.Context) (string, error), error) {
			return preparer.PrepareInstancePower(ctx, scope, w.AssetID, w.Action)
		}
	}
	if prepareSend != nil {
		prepared, err := prepareSend()
		if err != nil || prepared == nil || ctx.Err() != nil {
			if task.Attempts < task.MaxAttempts && (ctx.Err() != nil || remotePreflightRetryable(err)) {
				return true, nil
			}
			reason := "power_target_unconfirmed"
			switch {
			case provider.IsFailure(err, provider.DeletionDisabled):
				reason = "provider_deletion_disabled"
			case w.Action == "delete":
				reason = "deletion_preconditions_unconfirmed"
			case provider.IsFailure(err, provider.PowerDisabled):
				reason = "provider_power_disabled"
			}
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
			defer cancel()
			return false, s.transition(cleanup, task, w.Status, "failed", reason, "")
		}
		send = prepared
	}

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
	if armed.bareMetalDeployment {
		return false, s.transition(ctx, task, "applying", "failed", "deployment_binding_changed", "")
	}
	requestID, callErr := send(ctx)
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
	if w.Action == "delete" && state == "observing" && publicCloudProvider(scope.Provider) {
		_, observesDeletion := client.(provider.DeletionObserver)
		if !observesDeletion || !requestIDPattern.MatchString(requestID) {
			state, reason = "needs_review", "deletion_completion_requires_evidence"
		}
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	err = s.transition(cleanup, task, "applying", state, reason, requestID)
	return !terminal(state), err
}

func remotePreflightRetryable(err error) bool {
	return provider.IsFailure(err, provider.Transient) || provider.IsFailure(err, provider.Throttled) ||
		provider.IsFailure(err, provider.OutcomeUnknown)
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
			return false, s.settleRebootObservation(ctx, task, w, "succeeded", "provider_reboot_confirmed")
		case provider.ActionFailed:
			return false, s.settleRebootObservation(ctx, task, w, "failed", "provider_reboot_failed")
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
		if s.observationOnly && (arm || (w.Status != "applying" && w.Status != "observing")) {
			return mq.ErrLeaseLost
		}
		if terminal(w.Status) {
			return nil
		}
		if arm && w.Status != "queued" {
			return mq.ErrLeaseLost
		}
		if w.Status == "queued" {
			if err := authorizeCapturedRollout(ctx, tx, w.ID); err != nil {
				return err
			}
			if err := requireChangeTicket(ctx, tx, w.actor, w.Operation); err != nil {
				if errors.Is(err, ErrTicketRequired) {
					return identity.ErrForbidden
				}
				return err
			}
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
	if reviewedPowerObservation(w.Operation) {
		return checkPowerObservation(ctx, tx, w)
	}
	if w.ResourceKind == provider.BareMetal {
		return checkBareMetalPower(ctx, tx, w)
	}
	if w.Action == retireInstance {
		return checkInstanceRetirement(ctx, tx, w)
	}
	if w.Action == scaleWorkload {
		_, err := checkWorkloadScale(ctx, tx, w)
		return err
	}
	if w.Action == "create" {
		return checkLaunch(ctx, tx, w, w.Status == "queued" || w.Status == "awaiting_approval")
	}
	if w.Action == copyImage {
		return checkImageCopy(ctx, tx, w)
	}
	if isNetworkDeletion(w.Action) {
		return checkNetworkDeletion(ctx, tx, w)
	}
	if w.Action == deleteImage {
		_, err := checkImageDeletion(ctx, tx, *w)
		return err
	}
	if w.Action == deleteSnapshot || w.Action == rollbackSnapshot {
		return checkSnapshotMutation(ctx, tx, w)
	}
	if w.Action == createVolume {
		return checkVolumeCreation(ctx, tx, w)
	}
	if w.Action == restoreSnapshot {
		return checkSnapshotRestore(ctx, tx, w)
	}
	if isResourceCreation(w.Action) {
		return checkNetworkIntent(ctx, tx, w, w.Status == "queued" || w.Status == "awaiting_approval")
	}
	if w.Action == revokeSecurityRule {
		return checkSecurityRuleRevocation(ctx, tx, w)
	}
	if w.Action == releaseElasticIP || isElasticIPBinding(w.Action) {
		return checkElasticIPChange(ctx, tx, w)
	}
	if w.Action == setLoadBalancerBackendWeight {
		_, err := checkLoadBalancerBackendWeight(ctx, tx, w)
		return err
	}
	if w.Action == detachLoadBalancerBackend {
		_, err := checkLoadBalancerBackendDetach(ctx, tx, w)
		return err
	}
	if w.Action == attachLoadBalancerBackend {
		_, err := checkLoadBalancerBackendAttach(ctx, tx, w)
		return err
	}
	if w.Action == createDNSRecord {
		_, err := checkDNSRecordCreation(ctx, tx, w)
		return err
	}
	if w.Action == deleteDNSRecord {
		_, err := checkDNSRecordDeletion(ctx, tx, w)
		return err
	}
	if w.Action == updateDNSRecord {
		_, err := checkDNSRecordUpdate(ctx, tx, w)
		return err
	}
	if w.Action == setDNSRecordStatus {
		_, err := checkDNSRecordStatus(ctx, tx, w)
		return err
	}
	if isLoadBalancerRename(w.Action) {
		return checkLoadBalancerRename(ctx, tx, w)
	}
	if w.Action == renameCacheInstance {
		return checkCacheRename(ctx, tx, w)
	}
	if w.Action == releaseVolume {
		return checkVolumeRelease(ctx, tx, w)
	}
	if w.Action == enableBucketEncryption {
		_, err := checkBucketEncryption(ctx, tx, w)
		return err
	}
	if w.Action == setBucketVersioning {
		_, err := checkBucketVersioning(ctx, tx, w)
		return err
	}
	if w.Action == resizeInstance {
		return checkInstanceResize(ctx, tx, w)
	}
	if w.Action == resizeVolume {
		return checkVolumeResize(ctx, tx, w)
	}
	if w.Action == renameDatabaseInstance {
		return checkDatabaseRename(ctx, tx, w)
	}
	if w.Action == recoverDatabaseInstance {
		return checkDatabaseRecovery(ctx, tx, w)
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
	if !supportsInstanceAction(target.Provider, w.Action) {
		return ErrConflict
	}
	if w.Status == "queued" && publicCloudProvider(target.Provider) {
		if err := checkDispatchApproval(ctx, tx, w, false); err != nil {
			return err
		}
	}
	if volumeAction(w.Action) {
		_, err := resolveOperationVolume(ctx, tx, w.actor, Input{Account: w.Account, Region: w.Region, Action: w.Action,
			VolumeID: w.VolumeID, Device: w.Device, VolumeRevision: w.volumeRevision}, target.Provider)
		return err
	}
	if w.Status == "queued" || w.Status == "awaiting_approval" {
		return checkInstanceMutationProtection(ctx, tx, target.ScopeID, w.AssetID, w.Action)
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

func (s Store) dispatchSingleMutation(ctx context.Context, task mq.Task, w work, apply func(context.Context) (string, error), err error, reason string) (bool, error) {
	switch {
	case provider.IsFailure(err, provider.DeletionBlocked) || provider.IsFailure(err, provider.PreconditionFailed):
		return false, s.transition(ctx, task, w.Status, "failed", reason+"_preconditions_unconfirmed", "")
	case provider.IsFailure(err, provider.DeletionDisabled) || provider.IsFailure(err, provider.Unsupported):
		return false, s.transition(ctx, task, w.Status, "failed", reason+"_disabled", "")
	case provider.IsFailure(err, provider.InvalidResponse) || provider.IsFailure(err, provider.Unauthorized):
		return false, s.transition(ctx, task, w.Status, "failed", reason+"_preflight_unavailable", "")
	case err != nil || apply == nil:
		if task.Attempts >= task.MaxAttempts {
			return false, s.transition(ctx, task, w.Status, "failed", reason+"_preflight_unavailable", "")
		}
		return true, nil
	}
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
	requestID, callErr := apply(ctx)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	if w.Action == scaleWorkload && (provider.IsFailure(callErr, provider.PreconditionFailed) || provider.IsFailure(callErr, provider.Unauthorized)) {
		return false, s.transition(cleanup, task, "applying", "failed", "workload_scale_rejected", "")
	}
	if w.bareMetalDeployment && provider.IsFailure(callErr, provider.PreconditionFailed) {
		return false, s.transition(cleanup, task, "applying", "failed", "bare_metal_deployment_preconditions_unconfirmed", "")
	}
	reason = "provider_accepted"
	if callErr != nil {
		reason = "submission_outcome_unknown"
	}
	return true, s.transition(cleanup, task, "applying", "observing", reason, requestID)
}

func recordAbsenceObservation(ctx context.Context, tx *sql.Tx, operationID string, absent bool) (int16, error) {
	var observations int16
	err := tx.QueryRowContext(ctx, `UPDATE cmp_network_intents SET absence_observations=CASE WHEN $2::bool
		THEN LEAST(absence_observations+1,2) ELSE 0 END WHERE operation_id=$1 RETURNING absence_observations`,
		operationID, absent).Scan(&observations)
	return observations, err
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
	var action string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_instance_operations SET status=$3,outcome_reason=$4,
		provider_request_id=COALESCE(NULLIF($5,''),provider_request_id),updated_at=statement_timestamp()
		WHERE operation_id=$1 AND status=$2 RETURNING action`, id, from, to, reason, requestID).Scan(&action)
	if errors.Is(err, sql.ErrNoRows) {
		return mq.ErrLeaseLost
	}
	if isTicketGuardError(err) {
		if from == "awaiting_approval" {
			return ErrTicketRequired
		}
		return identity.ErrForbidden
	}
	if err != nil {
		return err
	}
	if err := recordOperationEvent(ctx, tx, id, to); err != nil {
		return err
	}
	if to == "failed" {
		if action == createImage || action == copyImage {
			return releaseImageSource(ctx, tx, action, id)
		}
		_, err = tx.ExecContext(ctx, `UPDATE cmp_instance_launches SET released_at=statement_timestamp()
			WHERE operation_id=$1 AND released_at IS NULL`, id)
	}
	return err
}
