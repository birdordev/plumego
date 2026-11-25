package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	dbutil "github.com/birdordev/plumego/store/db"
	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

//go:embed 0010_instance_operations.sql
var schema string

//go:embed queue.sql
var queueSchema string

//go:embed 0011_operation_worker.sql
var workerSchema string

//go:embed 0014_instance_deletion.sql
var deletionSchema string

const topic = "cmp.instance.v1"

var (
	ErrInvalid  = errors.New("invalid instance action")
	ErrConflict = errors.New("instance action conflicts with an operation")
	ErrQuota    = errors.New("active operation quota exceeded")
)

var operationKey = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:/-]{0,127}$`)

type Input struct {
	Account        string `json:"account"`
	Region         string `json:"region"`
	AssetID        string `json:"asset_id"`
	Action         string `json:"action"`
	VolumeID       string `json:"volume_id,omitempty"`
	VolumeRevision int64  `json:"volume_revision,omitempty"`
	Device         string `json:"device,omitempty"`
	GuestUnmounted bool   `json:"guest_unmounted,omitempty"`
}

type Operation struct {
	ID                string           `json:"id"`
	Account           string           `json:"account"`
	Region            string           `json:"region"`
	AssetID           string           `json:"asset_id"`
	ResourceKind      provider.Kind    `json:"resource_kind"`
	VolumeID          string           `json:"volume_id,omitempty"`
	Device            string           `json:"device,omitempty"`
	Action            string           `json:"action"`
	Status            string           `json:"status"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         *time.Time       `json:"updated_at,omitempty"`
	OutcomeReason     string           `json:"outcome_reason,omitempty"`
	ProviderRequestID string           `json:"provider_request_id,omitempty"`
	CatalogTemplate   string           `json:"catalog_template,omitempty"`
	CatalogVersion    int64            `json:"catalog_version,omitempty"`
	NetworkIntent     *NetworkSpecView `json:"network_intent,omitempty"`
}

type NetworkSpecView struct {
	provider.NetworkSpec
	ParentRevision int64 `json:"parent_revision"`
}

type Store struct {
	db    *sql.DB
	queue *mqstore.SQLStore
}

func NewStore(db *sql.DB) Store {
	cfg := mqstore.DefaultSQLConfig()
	cfg.Table = "cmp_operation_tasks"
	cfg.DLQTable = "cmp_operation_dlq"
	cfg.AttemptsTable = "cmp_operation_attempts"
	return Store{db: db, queue: mqstore.NewSQL(db, cfg)}
}

func Migrate(ctx context.Context, db *sql.DB) error {
	if err := dbschema.Apply(ctx, db, "0010_instance_operations", schema+queueSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0011_operation_worker", workerSchema); err != nil {
		return err
	}
	if err := migrateResolution(ctx, db); err != nil {
		return err
	}
	if err := migrateApproval(ctx, db); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0014_instance_deletion", deletionSchema); err != nil {
		return err
	}
	if err := migrateCreation(ctx, db); err != nil {
		return err
	}
	if err := migrateVolume(ctx, db); err != nil {
		return err
	}
	if err := migrateOutbox(ctx, db); err != nil {
		return err
	}
	if err := migrateNetwork(ctx, db); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0021_security_group_creation", securityGroupSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0022_operation_queries", operationQuerySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0023_operation_batches", operationBatchSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0026_instance_catalog", catalogSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0031_security_group_rules", securityRuleSchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, "0032_security_rule_revocation", securityRuleRevocationSchema)
}

const operationFields = "operation_id,account_id,region,COALESCE(external_id,''),action,status,created_at,updated_at,COALESCE(outcome_reason,''),COALESCE(provider_request_id,''),COALESCE(volume_id,''),device,resource_kind,COALESCE(catalog_id,''),COALESCE(catalog_version,0)"

func scanOperation(row interface{ Scan(...any) error }, op *Operation, tail ...any) error {
	fields := []any{&op.ID, &op.Account, &op.Region, &op.AssetID, &op.Action, &op.Status, &op.CreatedAt, &op.UpdatedAt, &op.OutcomeReason, &op.ProviderRequestID, &op.VolumeID, &op.Device, &op.ResourceKind, &op.CatalogTemplate, &op.CatalogVersion}
	return row.Scan(append(fields, tail...)...)
}

func operationActor(ctx context.Context, tx *sql.Tx, token string, scope identity.Scope, permission identity.Permission) (identity.Scope, error) {
	auth := identity.NewStore(tx)
	actor, err := auth.AuthenticateRequest(ctx, token)
	if err != nil {
		return identity.Scope{}, err
	}
	if actor.Tenant != scope.Tenant {
		return identity.Scope{}, identity.ErrForbidden
	}
	actor.Project = scope.Project
	_, err = auth.AuthorizeProject(ctx, actor, permission)
	return actor, err
}

func (s Store) Get(ctx context.Context, token string, scope identity.Scope, id string) (Operation, error) {
	var op Operation
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		err = scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+" FROM cmp_instance_operations WHERE tenant_id=$1 AND project_id=$2 AND operation_id=$3", actor.Tenant, actor.Project, id), &op)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err == nil && op.ResourceKind == provider.SecurityGroupRule {
			var intent NetworkSpecView
			var rule []byte
			if err := tx.QueryRowContext(ctx, `SELECT parent_id,parent_revision,rule_spec,name FROM cmp_network_intents WHERE operation_id=$1`, id).
				Scan(&intent.ParentID, &intent.ParentRevision, &rule, &intent.Name); err != nil {
				return err
			}
			if len(rule) != 0 {
				if err := json.Unmarshal(rule, &intent.Rule); err != nil {
					return err
				}
			}
			op.NetworkIntent = &intent
		}
		return err
	})
	return op, err
}

func (s Store) Submit(ctx context.Context, token string, scope identity.Scope, key string, revision int64, input Input) (Operation, error) {
	if !operationKey.MatchString(key) || !validOperationInput(input, revision) {
		return Operation{}, ErrInvalid
	}
	data, err := json.Marshal(struct {
		Input
		Revision int64
	}{input, revision})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(data)
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		target, err := inventory.ResolveInstanceMutation(ctx, tx, actor, input.Account, input.Region, input.AssetID)
		if err != nil {
			return err
		}
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		found, err := findReplay(ctx, tx, actor, key, digest, &op)
		if err != nil || found {
			return err
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		volume, err := resolveOperationVolume(ctx, tx, actor, input, target.Provider)
		if err != nil {
			return err
		}
		if err := checkDeletionProtection(ctx, tx, target.ScopeID, input.AssetID, input.Action); err != nil {
			return err
		}
		if err := checkBudget(ctx, tx, actor); err != nil {
			return err
		}
		return s.insert(ctx, tx, actor, input, target, volume, key, digest, &op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}

func validOperationInput(input Input, revision int64) bool {
	return operationKey.MatchString(input.Account) && operationKey.MatchString(input.Region) &&
		operationKey.MatchString(input.AssetID) && revision > 0 && validAction(input)
}

func findReplay(ctx context.Context, tx *sql.Tx, actor identity.Scope, key string, digest [32]byte, op *Operation) (bool, error) {
	var recorded []byte
	err := scanOperation(tx.QueryRowContext(ctx, "SELECT "+operationFields+",request_hash FROM cmp_instance_operations WHERE (tenant_id,project_id,principal_id,idempotency_key)=($1,$2,$3,$4)", actor.Tenant, actor.Project, actor.Principal, key), op, &recorded)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if subtle.ConstantTimeCompare(digest[:], recorded) != 1 {
		return true, ErrConflict
	}
	return true, nil
}

func lockBudget(ctx context.Context, tx *sql.Tx, actor identity.Scope) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO cmp_instance_operation_budgets (tenant_id,project_id) VALUES ($1,$2) ON CONFLICT DO NOTHING", actor.Tenant, actor.Project); err != nil {
		return err
	}
	var limit int
	return tx.QueryRowContext(ctx, "SELECT max_active FROM cmp_instance_operation_budgets WHERE tenant_id=$1 AND project_id=$2 FOR UPDATE", actor.Tenant, actor.Project).Scan(&limit)
}

const activeOperationStatuses = "('awaiting_approval','queued','applying','observing','needs_review')"

func remainingBudget(ctx context.Context, tx *sql.Tx, actor identity.Scope) (int, error) {
	var remaining int
	err := tx.QueryRowContext(ctx, `SELECT greatest(COALESCE((SELECT max_active FROM cmp_instance_operation_budgets
		WHERE (tenant_id,project_id)=($1,$2)),100)-(SELECT count(*) FROM cmp_instance_operations
		WHERE (tenant_id,project_id)=($1,$2) AND status IN `+activeOperationStatuses+`),0)`, actor.Tenant, actor.Project).Scan(&remaining)
	return remaining, err
}

func checkBudget(ctx context.Context, tx *sql.Tx, actor identity.Scope) error {
	remaining, err := remainingBudget(ctx, tx, actor)
	if err == nil && remaining < 1 {
		return ErrQuota
	}
	return err
}

func (s Store) insert(ctx context.Context, tx *sql.Tx, actor identity.Scope, input Input, target, volume inventory.MutationTarget, key string, digest [32]byte, op *Operation) error {
	id := rand.Text()
	status, err := initialStatus(ctx, tx, actor, input.Action)
	if err != nil {
		return err
	}
	err = scanOperation(tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_operations
		(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,status,
		 idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision,volume_scope_id,volume_id,volume_revision,device,guest_unmounted)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$15,$10,$11,$12,$13,$14,NULLIF($16::bigint,0),NULLIF($17,''),$18,$19,$20)
		ON CONFLICT DO NOTHING RETURNING `+operationFields,
		id, actor.Tenant, actor.Project, actor.Principal, target.ScopeID, input.AssetID, input.Account, input.Region, input.Action,
		key, digest[:], target.CredentialRevision, target.MembershipRevision, target.OwnershipRevision, status,
		volume.ScopeID, input.VolumeID, volume.OwnershipRevision, input.Device, input.GuestUnmounted), op)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	return s.recordSubmission(ctx, tx, actor.Tenant, *op)
}

func initialStatus(ctx context.Context, tx *sql.Tx, actor identity.Scope, action string) (string, error) {
	if action == "delete" || action == "detach-volume" || action == "create-security-group-rule" {
		return "awaiting_approval", nil
	}
	var required bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_instance_approval_rules
		WHERE tenant_id=$1 AND project_id=$2 AND action=$3)`, actor.Tenant, actor.Project, action).Scan(&required)
	if required {
		return "awaiting_approval", err
	}
	return "queued", err
}

func (s Store) recordSubmission(ctx context.Context, tx *sql.Tx, tenant string, op Operation) error {
	if err := recordOperationEvent(ctx, tx, op.ID, op.Status); err != nil {
		return err
	}
	if op.Status == "awaiting_approval" {
		return nil
	}
	return s.enqueue(ctx, tx, tenant, op)
}

func (s Store) enqueue(ctx context.Context, tx *sql.Tx, tenant string, op Operation) error {
	stamp := op.CreatedAt
	if op.UpdatedAt != nil {
		stamp = *op.UpdatedAt
	}
	return s.queue.InsertTx(ctx, tx, mq.Task{ID: op.ID, TenantID: tenant, Topic: topic, Payload: []byte(op.ID),
		MaxAttempts: 8, AvailableAt: stamp, CreatedAt: stamp, UpdatedAt: stamp})
}
