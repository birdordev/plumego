package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0016_instance_creation.sql
var creationSchema string

//go:embed 0017_creation_receipt.sql
var creationReceiptSchema string

var instanceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 _.:-]{0,79}$`)

type CreateInput struct {
	Account string `json:"account"`
	Region  string `json:"region"`
	provider.InstanceSpec
}

type launch struct {
	provider.InstanceSpec
	token      string
	instanceID string
	revisions  [3]int64
	replayable bool
	observer   creationObserver
}

type creationObserver struct {
	principal              string
	membership, credential int64
}

func migrateCreation(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0016_instance_creation", creationSchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0017_creation_receipt", creationReceiptSchema)
}

func validLaunchInput(input CreateInput) bool {
	return operationKey.MatchString(input.Account) && operationKey.MatchString(input.Region) &&
		instanceName.MatchString(input.Name) && operationKey.MatchString(input.ImageID) && operationKey.MatchString(input.Size) &&
		operationKey.MatchString(input.NetworkID) && operationKey.MatchString(input.SecurityGroup)
}

func (s Store) SubmitCreation(ctx context.Context, token string, scope identity.Scope, key string, input CreateInput) (Operation, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !operationKey.MatchString(key) || !validLaunchInput(input) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(input)
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte("create\x00"), data...))
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		return s.submitCreation(ctx, tx, actor, key, digest, input, catalogReference{}, &op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func (s Store) submitCreation(ctx context.Context, tx *sql.Tx, actor identity.Scope, key string, digest [32]byte, input CreateInput, ref catalogReference, op *Operation) error {
	access, err := authorizeCreation(ctx, tx, actor, input.Account)
	if err != nil {
		return err
	}
	if err := lockBudget(ctx, tx, actor); err != nil {
		return err
	}
	found, err := findReplay(ctx, tx, actor, key, digest, op)
	if err != nil || found {
		return err
	}
	if ref.id != "" && !ref.eligible {
		return ErrCatalogStale
	}
	revisions, err := validateLaunchSpec(ctx, tx, actor, input.Account, input.Region, access.Provider, input.InstanceSpec)
	if err != nil {
		return err
	}
	if err := checkBudget(ctx, tx, actor); err != nil {
		return err
	}
	if err := checkLaunchCapacity(ctx, tx, actor); err != nil {
		return err
	}
	status, err := initialStatus(ctx, tx, actor, "create")
	if err != nil {
		return err
	}
	id := rand.Text()
	err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
		(operation_id,tenant_id,project_id,principal_id,account_id,region,action,status,
		 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision,catalog_id,catalog_version)
		VALUES ($1,$2,$3,$4,$5,$6,'create',$7,$8,$9,$10,$11,0,NULLIF($12,''),NULLIF($13::bigint,0)) RETURNING `+operationFields,
		id, actor.Tenant, actor.Project, actor.Principal, input.Account, input.Region, status, key, digest[:],
		access.CredentialRevision, access.MembershipRevision, ref.id, ref.version), op)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_launches
		(operation_id,launch_token,name,image_id,size,network_id,security_group_id,image_revision,security_group_revision,network_revision)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, rand.Text(), input.Name, input.ImageID, input.Size, input.NetworkID,
		input.SecurityGroup, revisions[0], revisions[1], revisions[2]); err != nil {
		return err
	}
	return s.recordSubmission(ctx, tx, actor.Tenant, *op)
}

func authorizeCreation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account string) (identity.AccountAccess, error) {
	if err := lockOwnedCreationAccount(ctx, tx, actor, account); err != nil {
		return identity.AccountAccess{}, err
	}
	access, err := identity.NewStore(tx).AuthorizeAccount(ctx, actor, account, identity.Execute)
	if err == nil {
		switch access.Provider {
		case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS, "aws-ec2", "openstack":
		default:
			return identity.AccountAccess{}, ErrInvalid
		}
	}
	return access, err
}

func lockOwnedCreationAccount(ctx context.Context, tx *sql.Tx, actor identity.Scope, account string) error {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT account_id FROM cmp_cloud_accounts
		WHERE tenant_id=$1 AND account_id=$2 AND project_id=$3 FOR SHARE`, actor.Tenant, account, actor.Project).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrForbidden
	}
	return err
}

func authorizeLaunchObservation(ctx context.Context, tx *sql.Tx, actor identity.Scope, account string) (identity.AccountAccess, error) {
	if err := lockOwnedCreationAccount(ctx, tx, actor, account); err != nil {
		return identity.AccountAccess{}, err
	}
	store := identity.NewStore(tx)
	reviewer, err := store.AuthorizeProject(ctx, actor, identity.Approve)
	if err != nil {
		return identity.AccountAccess{}, err
	}
	access, err := store.AuthorizeAccount(ctx, actor, account, identity.View)
	if err == nil && access.MembershipRevision != reviewer.Revision {
		return identity.AccountAccess{}, identity.ErrAccountStale
	}
	return access, err
}

func checkLaunchCapacity(ctx context.Context, tx *sql.Tx, actor identity.Scope) error {
	var available bool
	err := tx.QueryRowContext(ctx, `SELECT b.max_managed_instances>(SELECT count(*) FROM cmp_instance_launches AS l
		JOIN cmp_instance_operations AS o USING (operation_id)
		WHERE (o.tenant_id,o.project_id)=(b.tenant_id,b.project_id) AND l.released_at IS NULL)
		FROM cmp_instance_operation_budgets AS b WHERE tenant_id=$1 AND project_id=$2`, actor.Tenant, actor.Project).Scan(&available)
	if err == nil && !available {
		return ErrQuota
	}
	return err
}

func validateLaunchSpec(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, providerName string, spec provider.InstanceSpec) ([3]int64, error) {
	var size string
	err := tx.QueryRowContext(ctx, `SELECT size FROM cmp_instance_offerings
		WHERE (tenant_id,account_id,region,size)=($1,$2,$3,$4) FOR SHARE`, actor.Tenant, account, region, spec.Size).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return [3]int64{}, ErrInvalid
	}
	if err != nil {
		return [3]int64{}, err
	}
	return inventory.CreationDependencies(ctx, tx, actor, account, region, spec, providerName)
}

func checkLaunch(ctx context.Context, tx *sql.Tx, w *work, validateDependencies bool) error {
	err := tx.QueryRowContext(ctx, `SELECT launch_token,provider_instance_id,name,image_id,size,network_id,security_group_id,
		image_revision,security_group_revision,network_revision,observation_reviewer,observation_membership_revision,observation_credential_revision,
		(replayed_at IS NULL AND dispatch_started_at IS NOT NULL AND dispatch_started_at>clock_timestamp()-interval '3 minutes')
		FROM cmp_instance_launches WHERE operation_id=$1`, w.ID).
		Scan(&w.launch.token, &w.launch.instanceID, &w.launch.Name, &w.launch.ImageID, &w.launch.Size, &w.launch.NetworkID, &w.launch.SecurityGroup,
			&w.launch.revisions[0], &w.launch.revisions[1], &w.launch.revisions[2],
			&w.launch.observer.principal, &w.launch.observer.membership, &w.launch.observer.credential, &w.launch.replayable)
	if err != nil {
		return err
	}
	access, err := checkCreationAuthority(ctx, tx, w, w.launch.observer, validateDependencies)
	if err != nil || !validateDependencies {
		return err
	}
	if w.Status == "queued" && publicCloudProvider(access.Provider) {
		if err := checkDispatchApproval(ctx, tx, w, false); err != nil {
			return err
		}
	}
	revisions, err := validateLaunchSpec(ctx, tx, w.actor, w.Account, w.Region, access.Provider, w.launch.InstanceSpec)
	if errors.Is(err, ErrInvalid) || (err == nil && revisions != w.launch.revisions) {
		return identity.ErrAccountStale
	}
	return err
}

func checkCreationAuthority(ctx context.Context, tx *sql.Tx, w *work, observer creationObserver, beforeWrite bool) (identity.AccountAccess, error) {
	if !beforeWrite && observer.principal != "" {
		reviewer := w.actor
		reviewer.Principal = observer.principal
		access, err := authorizeLaunchObservation(ctx, tx, reviewer, w.Account)
		if err == nil && (access.CredentialRevision != observer.credential || access.MembershipRevision != observer.membership) {
			err = identity.ErrAccountStale
		}
		return access, err
	}
	access, err := authorizeCreation(ctx, tx, w.actor, w.Account)
	if err == nil && (access.CredentialRevision != w.credential || access.MembershipRevision != w.membership) {
		err = identity.ErrAccountStale
	}
	return access, err
}

func (s Store) processCreation(ctx context.Context, task mq.Task, w work, client provider.InstanceOperator, scope provider.Scope) (bool, error) {
	creator, ok := client.(provider.InstanceCreator)
	if !ok {
		return false, s.endUnavailable(ctx, task, w)
	}
	if w.Status == "queued" {
		return s.dispatchCreation(ctx, task, w, scope, creator)
	}
	if w.Status == "applying" {
		if err := s.transition(ctx, task, "applying", "observing", "submission_outcome_unknown", ""); err != nil {
			return false, err
		}
		w.Status = "observing"
		w.OutcomeReason = "submission_outcome_unknown"
	}
	var object provider.Object
	var err error
	if observer, ok := creator.(provider.InstanceCreationReceiptObserver); ok {
		receipt := provider.LaunchReceipt{RequestID: w.ProviderRequestID, ResourceID: w.launch.instanceID}
		object, err = observer.FindCreatedInstanceWithReceipt(ctx, scope, w.launch.InstanceSpec, w.launch.token, receipt)
	} else {
		object, err = creator.FindCreatedInstance(ctx, scope, w.launch.InstanceSpec, w.launch.token, w.launch.instanceID)
	}
	if err == nil && object.ID != "" {
		if w.launch.instanceID != "" && object.ID != w.launch.instanceID {
			return false, s.transition(ctx, task, w.Status, "needs_review", "creation_receipt_mismatch", "")
		}
		switch object.Status {
		case "running", "Running", "RUNNING", "ACTIVE":
			return false, s.confirmCreation(ctx, task, w, object)
		case "terminated":
			return false, s.transition(ctx, task, w.Status, "failed", "created_instance_terminated", "")
		case "ERROR", "LAUNCH_FAILED":
			return false, s.transition(ctx, task, w.Status, "needs_review", "provider_creation_error", "")
		}
	}
	if task.Attempts >= task.MaxAttempts || provider.IsFailure(err, provider.Unauthorized) || provider.IsFailure(err, provider.InvalidResponse) {
		return false, s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
	}
	if scope.Provider == "aws-ec2" && w.launch.observer.principal == "" && w.launch.replayable && w.OutcomeReason == "submission_outcome_unknown" &&
		provider.IsFailure(err, provider.OutcomeUnknown) {
		return s.replayCreation(ctx, task, w, scope, creator)
	}
	return true, nil
}

func (s Store) dispatchCreation(ctx context.Context, task mq.Task, w work, scope provider.Scope, creator provider.InstanceCreator) (bool, error) {
	var send func(context.Context) (provider.LaunchReceipt, error)
	if preparer, ok := creator.(provider.InstanceCreationPreparer); ok {
		var err error
		send, err = preparer.PrepareInstanceCreation(ctx, scope, w.launch.InstanceSpec, w.launch.token)
		if err != nil || send == nil || ctx.Err() != nil {
			return s.rejectCreationPreflight(ctx, task, w, err)
		}
	}
	armed, err := s.prepare(ctx, task, true)
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return false, s.endUnavailable(ctx, task, armed)
	}
	if err != nil {
		return false, err
	}
	if armed.Status != "applying" {
		return false, mq.ErrLeaseLost
	}
	if send == nil {
		send = func(ctx context.Context) (provider.LaunchReceipt, error) {
			return creator.CreateInstance(ctx, scope, armed.launch.InstanceSpec, armed.launch.token)
		}
	}
	receipt, callErr := send(ctx)
	return s.settleCreationDispatch(ctx, task, provider.Instance, receipt, callErr)
}

func (s Store) rejectCreationPreflight(ctx context.Context, task mq.Task, w work, err error) (bool, error) {
	if task.Attempts < task.MaxAttempts && (ctx.Err() != nil || remotePreflightRetryable(err)) {
		return true, nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	return false, s.transition(cleanup, task, w.Status, "failed", "provider_creation_blocked", "")
}

func (s Store) settleCreationDispatch(ctx context.Context, task mq.Task, kind provider.Kind, receipt provider.LaunchReceipt, callErr error) (bool, error) {
	state, reason := "observing", "provider_accepted"
	switch {
	case provider.IsFailure(callErr, provider.CreationBlocked):
		state, reason = "failed", "provider_creation_blocked"
	case callErr != nil:
		reason = "submission_outcome_unknown"
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	return !terminal(state), s.recordLaunchReceipt(cleanup, task, kind, "applying", state, reason, receipt)
}

func (s Store) replayCreation(ctx context.Context, task mq.Task, w work, scope provider.Scope, creator provider.InstanceCreator) (bool, error) {
	var claimed string
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		var active string
		err := tx.QueryRowContext(ctx, `SELECT operation_id FROM cmp_instance_operations WHERE operation_id=$1
			AND status='observing' AND outcome_reason='submission_outcome_unknown' FOR UPDATE`, w.ID).Scan(&active)
		if err != nil {
			return err
		}
		if err := requireChangeTicket(ctx, tx, w.actor, w.Operation); err != nil {
			return err
		}
		if err := checkLaunch(ctx, tx, &w, true); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `UPDATE cmp_instance_launches SET replayed_at=clock_timestamp() WHERE operation_id=$1
			AND replayed_at IS NULL AND dispatch_started_at>clock_timestamp()-interval '3 minutes'
			RETURNING operation_id`, w.ID).Scan(&claimed)
	})
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) ||
		errors.Is(err, ErrTicketRequired) || isTicketGuardError(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	receipt, err := creator.CreateInstance(ctx, scope, w.launch.InstanceSpec, w.launch.token)
	if err != nil {
		return true, nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountingTimeout)
	defer cancel()
	return true, s.recordLaunchReceipt(cleanup, task, provider.Instance, "observing", "observing", "provider_accepted_after_replay", receipt)
}

func (s Store) recordLaunchReceipt(ctx context.Context, task mq.Task, kind provider.Kind, from, to, reason string, receipt provider.LaunchReceipt) error {
	if !requestIDPattern.MatchString(receipt.RequestID) {
		receipt.RequestID = ""
	}
	if kind == provider.DNSZone && !provider.ValidDNSRecordID(receipt.ResourceID) {
		receipt.ResourceID = ""
	}
	return s.withLease(ctx, task, func(tx *sql.Tx) error {
		if requestIDPattern.MatchString(receipt.ResourceID) {
			var id string
			query := `UPDATE cmp_instance_launches SET provider_instance_id=$2
				WHERE operation_id=$1 AND (provider_instance_id='' OR provider_instance_id=$2) RETURNING operation_id`
			switch kind {
			case provider.Instance:
			case provider.DNSZone:
				query = `UPDATE cmp_dns_record_creation_intents SET record_id=$2
					WHERE operation_id=$1 AND (record_id='' OR record_id=$2) RETURNING operation_id`
			default:
				query = `UPDATE cmp_network_intents SET provider_resource_id=$2
					WHERE operation_id=$1 AND (provider_resource_id='' OR provider_resource_id=$2) RETURNING operation_id`
			}
			err := tx.QueryRowContext(ctx, query, task.ID, receipt.ResourceID).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return writeTransition(ctx, tx, task.ID, from, "needs_review", "creation_receipt_mismatch", "")
			}
			if err != nil {
				return err
			}
		}
		return writeTransition(ctx, tx, task.ID, from, to, reason, receipt.RequestID)
	})
}

func (s Store) confirmCreation(ctx context.Context, task mq.Task, w work, object provider.Object) error {
	if err := (provider.Page{Objects: []provider.Object{object}}).Validate(""); err != nil {
		return s.transition(ctx, task, w.Status, "needs_review", "creation_requires_evidence", "")
	}
	err := s.withLease(ctx, task, func(tx *sql.Tx) error {
		var current string
		if err := tx.QueryRowContext(ctx, "SELECT status FROM cmp_instance_operations WHERE operation_id=$1 FOR UPDATE", w.ID).Scan(&current); err != nil {
			return err
		}
		if current != w.Status {
			return mq.ErrLeaseLost
		}
		if err := checkOperationTarget(ctx, tx, &w); err != nil {
			return err
		}
		scopeID, err := inventory.RegisterCreatedAsset(ctx, tx, w.actor, w.Account, w.Region, w.ResourceKind, w.binding.Revision, object)
		if err != nil {
			return err
		}
		var linked string
		reason := "created_resource_observed"
		if w.ResourceKind == provider.Instance {
			reason = "created_instance_observed"
		}
		err = tx.QueryRowContext(ctx, `UPDATE cmp_instance_operations SET scope_id=$2,external_id=$3,status='succeeded',
			outcome_reason=$4,updated_at=statement_timestamp()
			WHERE operation_id=$1 AND NOT EXISTS (SELECT 1 FROM cmp_instance_operations AS prior
			WHERE prior.action IN ('create','create-network','create-subnet','create-security-group','create-security-group-rule','create-elastic-ip','create-snapshot','restore-snapshot','create-image','copy-image')
			AND (prior.scope_id,prior.external_id)=($2,$3)) RETURNING operation_id`, w.ID, scopeID, object.ID, reason).Scan(&linked)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if w.Action == createImage || w.Action == copyImage {
			if err := releaseImageSource(ctx, tx, w.Action, w.ID); err != nil {
				return err
			}
		}
		return recordOperationEvent(ctx, tx, w.ID, "succeeded")
	})
	if errors.Is(err, ErrConflict) {
		return s.transition(ctx, task, w.Status, "needs_review", "creation_asset_conflict", "")
	}
	if errors.Is(err, identity.ErrForbidden) || errors.Is(err, identity.ErrAccountStale) {
		return s.endUnavailable(ctx, task, w)
	}
	return err
}
