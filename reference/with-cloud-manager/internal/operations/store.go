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

//go:embed 0139_operation_observation_worker.sql
var observationWorkerSchema string

const ObservationWorkerMigration = "0139_operation_observation_worker"

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
	BareMetalDeployment             *BareMetalDeploymentView       `json:"bare_metal_deployment,omitempty"`
	InstanceRetirementIntent        *InstanceRetirementView        `json:"instance_retirement_intent,omitempty"`
	CacheRenameIntent               *CacheRenameView               `json:"cache_rename_intent,omitempty"`
	DatabaseRenameIntent            *DatabaseRenameView            `json:"database_rename_intent,omitempty"`
	WorkloadScaleIntent             *provider.WorkloadScaleChange  `json:"workload_scale_intent,omitempty"`
	ID                              string                         `json:"id"`
	Account                         string                         `json:"account"`
	Region                          string                         `json:"region"`
	AssetID                         string                         `json:"asset_id"`
	ResourceKind                    provider.Kind                  `json:"resource_kind"`
	VolumeID                        string                         `json:"volume_id,omitempty"`
	Device                          string                         `json:"device,omitempty"`
	Action                          string                         `json:"action"`
	Status                          string                         `json:"status"`
	CreatedAt                       time.Time                      `json:"created_at"`
	UpdatedAt                       *time.Time                     `json:"updated_at,omitempty"`
	OutcomeReason                   string                         `json:"outcome_reason,omitempty"`
	ProviderRequestID               string                         `json:"provider_request_id,omitempty"`
	CatalogTemplate                 string                         `json:"catalog_template,omitempty"`
	CatalogVersion                  int64                          `json:"catalog_version,omitempty"`
	TicketPolicyRevision            int64                          `json:"ticket_policy_revision,string,omitempty"`
	NetworkIntent                   *NetworkSpecView               `json:"network_intent,omitempty"`
	SnapshotIntent                  *SnapshotSpecView              `json:"snapshot_intent,omitempty"`
	ImageCopyIntent                 *ImageCopyView                 `json:"image_copy_intent,omitempty"`
	ImageCreationIntent             *ImageCreationView             `json:"image_creation_intent,omitempty"`
	NetworkDeletionIntent           *NetworkDeletionView           `json:"network_deletion_intent,omitempty"`
	DNSRecordCreationIntent         *DNSRecordCreationView         `json:"dns_record_creation_intent,omitempty"`
	DNSRecordDeletionIntent         *DNSRecordDeletionView         `json:"dns_record_deletion_intent,omitempty"`
	DNSRecordStatusIntent           *DNSRecordStatusView           `json:"dns_record_status_intent,omitempty"`
	LoadBalancerBackendAttachIntent *LoadBalancerBackendAttachView `json:"load_balancer_backend_attach_intent,omitempty"`
	LoadBalancerBackendDetachIntent *LoadBalancerBackendDetachView `json:"load_balancer_backend_detach_intent,omitempty"`
	LoadBalancerBackendWeightIntent *LoadBalancerBackendWeightView `json:"load_balancer_backend_weight_intent,omitempty"`
	LoadBalancerRenameIntent        *LoadBalancerRenameView        `json:"load_balancer_rename_intent,omitempty"`
	ImageDeletionIntent             *ImageDeletionView             `json:"image_deletion_intent,omitempty"`
	SnapshotRestoreIntent           *SnapshotRestoreSpecView       `json:"snapshot_restore_intent,omitempty"`
}

type NetworkSpecView struct {
	provider.NetworkSpec
	ParentRevision int64  `json:"parent_revision"`
	AssetRevision  int64  `json:"asset_revision,omitempty"`
	PublicAddress  string `json:"public_address,omitempty"`
	PortID         string `json:"port_id,omitempty"`
}

type Store struct {
	connectWorkloadScaler ConnectWorkloadScaler
	db                    *sql.DB
	queue                 *mqstore.SQLStore
	observationOnly       bool
	workerTenant          string
}

func NewStore(db *sql.DB) Store {
	return Store{db: db, queue: newOperationQueue(db, false)}
}

// WithObservationOnly 只领取已有派发记录的操作，禁止首次派发和自动重发。
func (s Store) WithObservationOnly() Store {
	s.observationOnly = true
	s.queue = newOperationQueue(s.db, true)
	return s
}

// WithWorkerTenant 将操作 Worker 的领取范围限定在指定租户。
func (s Store) WithWorkerTenant(tenant string) Store {
	s.workerTenant = tenant
	return s
}

func newOperationQueue(db *sql.DB, observationOnly bool) *mqstore.SQLStore {
	cfg := mqstore.DefaultSQLConfig()
	cfg.Table = "cmp_operation_tasks"
	cfg.DLQTable = "cmp_operation_dlq"
	cfg.AttemptsTable = "cmp_operation_attempts"
	if observationOnly {
		cfg.BlockedClaimsTable = "cmp_operation_observation_blocked_claims"
		cfg.DeferredExpiration = true
	}
	return mqstore.NewSQL(db, cfg)
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
	if err := dbschema.Apply(ctx, db, "0032_security_rule_revocation", securityRuleRevocationSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0033_subnet_zone", subnetZoneSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0037_elastic_ip_creation", elasticIPCreationSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0038_elastic_ip_release", elasticIPReleaseSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0039_elastic_ip_binding", elasticIPBindingSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0042_snapshot_creation", snapshotCreationSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0043_snapshot_deletion", snapshotDeletionSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0044_snapshot_restore", snapshotRestoreSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0045_snapshot_rollback", snapshotRollbackSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0046_image_deletion", imageDeletionSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0049_image_creation", imageCreationSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0051_image_copy", imageCopySchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0052_network_deletion", networkDeletionSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0053_security_group_deletion", securityGroupDeletionSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0055_network_deletion_backfill", networkDeletionBackfillSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0059_load_balancer_rename", loadBalancerRenameSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0061_load_balancer_listener_rename", loadBalancerListenerRenameSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0062_load_balancer_target_group_rename", loadBalancerTargetGroupRenameSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0064_load_balancer_backend_weight", loadBalancerBackendWeightSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0065_load_balancer_backend_detach", loadBalancerBackendDetachSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0066_load_balancer_backend_attach", loadBalancerBackendAttachSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0070_load_balancer_change_backfill", loadBalancerChangeBackfillSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0072_dns_record_status", dnsRecordStatusSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0074_dns_record_creation", dnsRecordCreationSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0075_dns_record_deletion", dnsRecordDeletionSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0091_workload_scale", workloadScaleSchema); err != nil {
		return err
	}
	if err := migrateChangeTickets(ctx, db); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0104_database_rename", databaseRenameSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0107_instance_retirement", instanceRetirementSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, "0109_cache_rename", cacheRenameSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, ObservationWorkerMigration, observationWorkerSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, EffectIDMigration, effectIDSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, TerminalEventTimeMigration, terminalEventTimeSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, OperationRolloutMigration, operationRolloutSchema); err != nil {
		return err
	}
	if err := dbschema.Apply(ctx, db, FirstLeaseMigration, firstLeaseSchema); err != nil {
		return err
	}
	return dbschema.Apply(ctx, db, QueueSnapshotMigration, queueSnapshotSchema)
}

const operationFields = "operation_id,account_id,region,COALESCE(external_id,''),action,status,created_at,updated_at,COALESCE(outcome_reason,''),COALESCE(provider_request_id,''),COALESCE(volume_id,''),device,resource_kind,COALESCE(catalog_id,''),COALESCE(catalog_version,0),COALESCE(ticket_policy_revision,0)"

func scanOperation(row interface{ Scan(...any) error }, op *Operation, tail ...any) error {
	fields := []any{&op.ID, &op.Account, &op.Region, &op.AssetID, &op.Action, &op.Status, &op.CreatedAt, &op.UpdatedAt, &op.OutcomeReason, &op.ProviderRequestID, &op.VolumeID, &op.Device, &op.ResourceKind, &op.CatalogTemplate, &op.CatalogVersion, &op.TicketPolicyRevision}
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
		if err == nil && (op.ResourceKind == provider.SecurityGroupRule || op.ResourceKind == provider.Subnet ||
			op.ResourceKind == provider.SecurityGroup || op.ResourceKind == provider.ElasticIP) {
			var intent NetworkSpecView
			var rule []byte
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(parent_id,''),parent_revision,rule_spec,name,COALESCE(cidr::text,''),zone,
				bandwidth_mbps,address_pool,COALESCE(host(binding_address),host(release_address),''),
				CASE WHEN binding_address IS NOT NULL OR release_address IS NOT NULL THEN ownership_revision ELSE 0 END,elastic_ip_port
				FROM cmp_network_intents JOIN cmp_instance_operations USING(operation_id) WHERE operation_id=$1`, id).
				Scan(&intent.ParentID, &intent.ParentRevision, &rule, &intent.Name, &intent.CIDR, &intent.Zone,
					&intent.BandwidthMbps, &intent.AddressPool, &intent.PublicAddress, &intent.AssetRevision, &intent.PortID); err != nil {
				return err
			}
			if len(rule) != 0 {
				if err := json.Unmarshal(rule, &intent.Rule); err != nil {
					return err
				}
			}
			op.NetworkIntent = &intent
		}
		if err == nil && op.ResourceKind == provider.Snapshot {
			var intent SnapshotSpecView
			if err := tx.QueryRowContext(ctx, `SELECT i.name,i.parent_id,i.parent_revision,i.snapshot_allow_attached,i.snapshot_class,
				(o.action='rollback-snapshot' AND a.provider='huawei-ecs' AND i.snapshot_class='standard' AND COALESCE(o.provider_request_id,'')<>'')
				FROM cmp_network_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
				JOIN cmp_cloud_accounts AS a USING(tenant_id,account_id) WHERE operation_id=$1`, id).
				Scan(&intent.Name, &intent.VolumeID, &intent.VolumeRevision, &intent.AllowAttached, &intent.Class, &intent.CanRetryObservation); err != nil {
				return err
			}
			op.SnapshotIntent = &intent
		}
		if err == nil && op.Action == copyImage {
			intent := ImageCopyView{ImageCopy: ImageCopy{DestinationAccount: op.Account, DestinationRegion: op.Region}}
			if err := tx.QueryRowContext(ctx, `SELECT c.source_account,c.source_region,i.parent_id,i.parent_revision,i.name,
				(a.provider<>'tencent-cvm' OR i.provider_resource_id<>'')
				FROM cmp_network_intents AS i JOIN cmp_image_copy_intents AS c USING(operation_id)
				JOIN cmp_cloud_accounts AS a ON (a.tenant_id,a.account_id)=(c.tenant_id,c.source_account)
				WHERE i.operation_id=$1`, id).
				Scan(&intent.Account, &intent.Region, &intent.ImageID, &intent.SourceRevision, &intent.Name, &intent.CanRetryObservation); err != nil {
				return err
			}
			op.ImageCopyIntent = &intent
		}
		if err == nil && op.Action == createImage {
			var intent ImageCreationView
			if err := tx.QueryRowContext(ctx, `SELECT name,parent_id,parent_revision FROM cmp_network_intents WHERE operation_id=$1`, id).
				Scan(&intent.Name, &intent.InstanceID, &intent.InstanceRevision); err != nil {
				return err
			}
			op.ImageCreationIntent = &intent
		}
		if err == nil && isNetworkDeletion(op.Action) {
			var intent NetworkDeletionView
			if err := tx.QueryRowContext(ctx, `SELECT i.name,o.ownership_revision,COALESCE(d.parent_id,''),d.parent_revision,d.rule_removal_confirmed
				FROM cmp_network_deletion_intents AS d JOIN cmp_network_intents AS i USING(operation_id)
				JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, id).
				Scan(&intent.Name, &intent.OwnershipRevision, &intent.ParentID, &intent.ParentRevision, &intent.RuleRemovalConfirmed); err != nil {
				return err
			}
			op.NetworkDeletionIntent = &intent
		}
		if err == nil && op.Action == deleteImage {
			var intent ImageDeletionView
			if err := tx.QueryRowContext(ctx, `SELECT i.name,o.ownership_revision FROM cmp_network_intents AS i
				JOIN cmp_instance_operations AS o USING(operation_id) WHERE operation_id=$1`, id).
				Scan(&intent.Name, &intent.OwnershipRevision); err != nil {
				return err
			}
			op.ImageDeletionIntent = &intent
		}
		if err == nil && op.Action == restoreSnapshot {
			var intent SnapshotRestoreSpecView
			if err := tx.QueryRowContext(ctx, `SELECT restore_snapshot_id,parent_id,parent_revision,snapshot_class,name,zone,
				restore_disk_type,restore_size_gib,restore_encrypted FROM cmp_network_intents WHERE operation_id=$1`, id).
				Scan(&intent.SnapshotID, &intent.VolumeID, &intent.VolumeRevision, &intent.Class, &intent.Name, &intent.Zone,
					&intent.DiskType, &intent.SizeGiB, &intent.Encrypted); err != nil {
				return err
			}
			op.SnapshotRestoreIntent = &intent
		}
		if err == nil && op.Action == retireInstance {
			var intent InstanceRetirementView
			if err := tx.QueryRowContext(ctx, `SELECT system_disk_id,disk_ownership_revision
				FROM cmp_instance_retirement_intents WHERE operation_id=$1`, id).
				Scan(&intent.SystemDiskID, &intent.DiskOwnershipRevision); err != nil {
				return err
			}
			op.InstanceRetirementIntent = &intent
		}
		if err == nil && op.Action == renameDatabaseInstance {
			var intent DatabaseRenameView
			if err := tx.QueryRowContext(ctx, `SELECT i.product,i.current_name,i.name,o.ownership_revision
				FROM cmp_database_rename_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
				WHERE operation_id=$1`, id).Scan(&intent.Product, &intent.CurrentName, &intent.Name, &intent.OwnershipRevision); err != nil {
				return err
			}
			op.DatabaseRenameIntent = &intent
		}
		if err == nil && op.Action == renameCacheInstance {
			var intent CacheRenameView
			if err := tx.QueryRowContext(ctx, `SELECT i.product,i.current_name,i.name,o.ownership_revision
				FROM cmp_cache_rename_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
				WHERE operation_id=$1`, id).Scan(&intent.Product, &intent.CurrentName, &intent.Name, &intent.OwnershipRevision); err != nil {
				return err
			}
			op.CacheRenameIntent = &intent
		}
		if err == nil && isLoadBalancerRename(op.Action) {
			var intent LoadBalancerRenameView
			if err := tx.QueryRowContext(ctx, `SELECT i.product,i.current_name,i.name,i.listener_id,i.target_group_id,o.ownership_revision
				FROM cmp_load_balancer_rename_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
				WHERE operation_id=$1`, id).Scan(&intent.Product, &intent.CurrentName, &intent.Name, &intent.ListenerID, &intent.TargetGroupID, &intent.OwnershipRevision); err != nil {
				return err
			}
			op.LoadBalancerRenameIntent = &intent
		}
		if err == nil && op.Action == setLoadBalancerBackendWeight {
			var intent LoadBalancerBackendWeightView
			if err := tx.QueryRowContext(ctx, `SELECT i.product,i.target_group_id,i.member_key,i.current_weight,i.weight,o.ownership_revision
     FROM cmp_load_balancer_backend_weight_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
     WHERE operation_id=$1`, id).Scan(&intent.Product, &intent.TargetGroupID, &intent.MemberKey, &intent.CurrentWeight, &intent.Weight, &intent.OwnershipRevision); err != nil {
				return err
			}
			op.LoadBalancerBackendWeightIntent = &intent
		}
		if err == nil && op.Action == detachLoadBalancerBackend {
			var intent LoadBalancerBackendDetachView
			if err := tx.QueryRowContext(ctx, `SELECT i.product,i.target_group_id,i.member_key,o.ownership_revision
     FROM cmp_load_balancer_backend_detach_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
     WHERE operation_id=$1`, id).Scan(&intent.Product, &intent.TargetGroupID, &intent.MemberKey, &intent.OwnershipRevision); err != nil {
				return err
			}
			op.LoadBalancerBackendDetachIntent = &intent
		}
		if err == nil && op.Action == createDNSRecord {
			var intent DNSRecordCreationView
			err = tx.QueryRowContext(ctx, `SELECT i.zone_name,i.record_id,i.spec->>'name',i.spec->>'type',
				i.spec->>'value',(i.spec->>'ttl')::integer,(a.provider='alibaba-ecs' AND i.record_id='')
				FROM cmp_dns_record_creation_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
				JOIN cmp_cloud_accounts AS a USING(tenant_id,account_id) WHERE i.operation_id=$1`, op.ID).
				Scan(&intent.ZoneName, &intent.RecordID, &intent.Name, &intent.Type, &intent.Value, &intent.TTL, &intent.NeedsRecordID)
			if err == nil {
				op.DNSRecordCreationIntent = &intent
			}
		}
		if err == nil && op.Action == deleteDNSRecord {
			var intent DNSRecordDeletionView
			var encoded []byte
			err = tx.QueryRowContext(ctx, `SELECT zone_name,record FROM cmp_dns_record_deletion_intents
				WHERE operation_id=$1`, op.ID).Scan(&intent.ZoneName, &encoded)
			if err == nil {
				err = json.Unmarshal(encoded, &intent.Record)
			}
			if err == nil {
				op.DNSRecordDeletionIntent = &intent
			}
		}
		if err == nil && op.Action == setDNSRecordStatus {
			var intent DNSRecordStatusView
			err = tx.QueryRowContext(ctx, `SELECT zone_name,record->>'id',record->>'name',record->>'status',enabled
				FROM cmp_dns_record_status_intents WHERE operation_id=$1`, op.ID).
				Scan(&intent.ZoneName, &intent.RecordID, &intent.Name, &intent.Current, &intent.Enabled)
			if err == nil {
				op.DNSRecordStatusIntent = &intent
			}
		}
		if err == nil && op.Action == scaleWorkload {
			var change provider.WorkloadScaleChange
			if err := tx.QueryRowContext(ctx, `SELECT uid,namespace,kind,name,current_replicas,replicas
				FROM cmp_workload_scale_intents WHERE operation_id=$1`, op.ID).
				Scan(&change.UID, &change.Namespace, &change.Kind, &change.Name, &change.Current, &change.Desired); err != nil {
				return err
			}
			op.WorkloadScaleIntent = &change
		}
		if err == nil && op.Action == attachLoadBalancerBackend {
			var intent LoadBalancerBackendAttachView
			if err := tx.QueryRowContext(ctx, `SELECT i.product,i.target_group_id,i.instance_id,i.instance_revision,i.port,i.member_key,o.ownership_revision
     FROM cmp_load_balancer_backend_attach_intents AS i JOIN cmp_instance_operations AS o USING(operation_id)
     WHERE operation_id=$1`, id).Scan(&intent.Product, &intent.TargetGroupID, &intent.InstanceID, &intent.InstanceRevision, &intent.Port, &intent.MemberKey, &intent.OwnershipRevision); err != nil {
				return err
			}
			op.LoadBalancerBackendAttachIntent = &intent
		}
		if err == nil && op.Action == powerOnBareMetal {
			op.BareMetalDeployment, err = bareMetalDeploymentForBoot(ctx, tx, actor, op)
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
		if err := lockBudget(ctx, tx, actor); err != nil {
			return err
		}
		target, err := inventory.ResolveInstanceMutation(ctx, tx, actor, input.Account, input.Region, input.AssetID)
		if err != nil {
			return err
		}
		found, err := findReplay(ctx, tx, actor, key, digest, &op)
		if err != nil || found {
			return err
		}
		if target.OwnershipRevision != revision {
			return identity.ErrAccountStale
		}
		if !supportsInstanceAction(target.Provider, input.Action) {
			return ErrInvalid
		}
		volume, err := resolveOperationVolume(ctx, tx, actor, input, target.Provider)
		if err != nil {
			return err
		}
		if err := checkInstanceMutationProtection(ctx, tx, target.ScopeID, input.AssetID, input.Action); err != nil {
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

func supportsInstanceAction(name, action string) bool {
	if action == "delete" {
		return name != provider.TencentCVM
	}
	return !publicCloudProvider(name) || instancePowerAction(action) || volumeAction(action)
}

func instancePowerAction(action string) bool {
	return action == "start" || action == "stop" || action == "reboot"
}

func publicCloudProvider(name string) bool {
	switch name {
	case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
		return true
	default:
		return false
	}
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
	if action == "delete" || action == "detach-volume" || action == "create-security-group-rule" || action == createElasticIP || action == createSnapshot || action == createImage {
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
	if err := captureRollout(ctx, tx, op.ID); err != nil {
		return err
	}
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
	attempts := 8
	if op.Action == createImage || op.Action == copyImage {
		attempts = imageCreationAttempts
	}
	return s.queue.InsertTx(ctx, tx, mq.Task{ID: op.ID, TenantID: tenant, Topic: topic, Payload: []byte(op.ID),
		MaxAttempts: attempts, AvailableAt: stamp, CreatedAt: stamp, UpdatedAt: stamp})
}
