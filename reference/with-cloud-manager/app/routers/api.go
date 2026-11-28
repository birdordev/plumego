package routers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/middleware/concurrencylimit"

	"with-cloud-manager/app/handlers/accounts"
	"with-cloud-manager/app/handlers/assets"
	billinghttp "with-cloud-manager/app/handlers/billing"
	consolehttp "with-cloud-manager/app/handlers/console"
	"with-cloud-manager/app/handlers/governance"
	operationhttp "with-cloud-manager/app/handlers/operations"
	kvmapp "with-cloud-manager/app/kvm"
	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/kvm"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/operations"
)

func NewAPI(db *sql.DB, addr string, console *consolehttp.ConsoleConfig) (*core.App, error) {
	cfg := core.DefaultConfig()
	cfg.Addr = "127.0.0.1:8096"
	if addr != "" {
		cfg.Addr = addr
	}
	if console != nil {
		cfg.TLS = core.TLSConfig{Enabled: true, CertFile: console.CertFile, KeyFile: console.KeyFile}
	}
	app := core.New(cfg, core.AppDependencies{Logger: plumelog.NewLogger()})
	status := func(w http.ResponseWriter, r *http.Request, state string) {
		if err := contract.WriteResponse(w, r, http.StatusOK, map[string]string{"status": state}, nil); err != nil {
			app.Logger().Warn("probe response write failed", plumelog.Fields{})
		}
	}
	health := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status(w, r, "ok")
	})
	additionalVersions := append(kvm.RequiredMigrations(), controller.RedfishScheduleMigration, controller.ScheduleConfigMigration,
		controller.AccountDispatchMigration, controller.TenantDispatchMigration, controller.BucketSettingScheduleMigration, controller.DatabaseObservationScheduleMigration,
		inventory.InventoryCompletionMigration, identity.AccountWriteModeMigration, operations.BareMetalPowerMigration,
		operations.BareMetalPXEMigration, operations.BareMetalDeploymentMigration, operations.BareMetalReclaimsMigration,
		operations.BareMetalReclaimRecoveriesMigration, operations.ObservationWorkerMigration, operations.EffectIDMigration,
		operations.TerminalEventTimeMigration, operations.OperationRolloutMigration, inventory.BucketVersioningMigration, inventory.BucketEncryptionMigration)
	additionalVersions = append(additionalVersions, billing.ProjectBudgetMigration, alerts.BucketProtectionMigration,
		inventory.TagPolicyAlertsMigration, operations.FirstLeaseMigration, operations.QueueSnapshotMigration, alerts.OperationQueueMigration,
		operations.QueueFindingsMigration, operations.ObservationQueueRepairMigration, operations.PowerObservationMigration,
		inventory.VolumeInventoryMigration, operations.VolumeResizeMigration, operations.VolumeCreationMigration, operations.VolumeReleaseMigration,
		inventory.InstanceTypeInventoryMigration, operations.InstanceResizeMigration, inventory.InstanceResizeOptionsMigration, operations.BucketVersioningMigration, operations.BucketEncryptionEnableMigration, operations.DNSRecordUpdateMigration, billing.AllocationPublishMigration, inventory.DatabaseBackupsMigration, inventory.DatabaseRestoreWindowsMigration, operations.DatabaseRecoveryMigration, inventory.DatabaseProtectionMigration, alerts.DatabaseProtectionMigration, operations.BudgetMigration)
	additionalVersionList := strings.Join(additionalVersions, ",")
	ready := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		var applied int
		var writable bool
		err := db.QueryRowContext(ctx, `SELECT count(*), NOT pg_is_in_recovery() AND current_setting('transaction_read_only')='off'
			FROM cmp_migrations
			WHERE version IN ('0001_identity','0002_inventory','0003_service_tokens','0004_inventory_schedules','0005_inventory_history','0006_inventory_topology','0007_account_events','0008_asset_ownership','0009_tag_assignment','0010_instance_operations','0011_operation_worker','0012_operation_resolution','0013_operation_approval','0014_instance_deletion','0015_confirmed_instance_deletions','0016_instance_creation','0017_creation_receipt','0018_volume_operations','0019_operation_outbox','0020_network_operations','0021_security_group_creation','0022_operation_queries','0023_operation_batches','0024_browser_sessions','0025_project_audit','0026_instance_catalog','0027_catalog_audit','0028_project_alerts','0029_native_console','0030_aws_serial_console','0031_security_group_rules','0032_security_rule_revocation','0033_subnet_zone','0034_security_rule_outbox','0035_inventory_public_addresses','0036_network_asset_schedules','0037_elastic_ip_creation','0038_elastic_ip_release','0039_elastic_ip_binding','0040_snapshot_inventory','0041_snapshot_schedules','0042_snapshot_creation','0043_snapshot_deletion','0044_snapshot_restore','0045_snapshot_rollback','0046_image_deletion','0047_image_deletion_outbox','0048_instance_image_reservations','0049_image_creation','0050_image_copy_reservations','0051_image_copy','0052_network_deletion','0053_security_group_deletion','0054_network_deletion_reservations','0055_network_deletion_backfill','0056_load_balancer_inventory','0057_load_balancer_schedules','0058_load_balancer_configuration','0059_load_balancer_rename','0060_load_balancer_rename_outbox','0061_load_balancer_listener_rename','0062_load_balancer_target_group_rename','0063_load_balancer_backend_keys','0064_load_balancer_backend_weight','0065_load_balancer_backend_detach','0066_load_balancer_backend_attach','0067_dns_zone_records','0068_dns_zone_schedules','0069_load_balancer_change_reservations','0070_load_balancer_change_backfill','0071_dns_zone_mutation','0072_dns_record_status','0073_dns_zone_outbox','0074_dns_record_creation','0075_dns_record_deletion','0076_database_inventory','0077_database_schedules','0078_object_bucket_inventory','0079_object_bucket_schedules','0080_cache_inventory','0081_cache_schedules','0082_kubernetes_inventory','0083_kubernetes_schedules','0084_cluster_node_pools','0085_cluster_node_pool_schedules','0086_cluster_workloads','0087_cluster_workload_schedules','0088_cluster_workload_checkpoints','0089_cluster_workload_watch_schedules','0090_workload_change_reservations','0091_workload_scale','0092_workload_scale_outbox','0093_billing_ledger','0094_billing_budgets','0095_billing_allocations','0096_billing_pull','0097_tag_observations','0098_tag_policies','0099_idle_schedules','0100_instance_idle','0101_change_tickets','0102_change_ticket_events','0103_database_rename_reservations','0104_database_rename','0105_database_rename_events','0106_instance_retirement_reservations','0107_instance_retirement','0108_cache_rename_reservations','0109_cache_rename','0110_cache_rename_events') OR version = ANY(string_to_array($1, ','))`, additionalVersionList).Scan(&applied, &writable)
		if err == nil && writable && applied == 110+len(additionalVersions) {
			status(w, r, "ready")
			return
		}
		problem := contract.NewErrorBuilder().Type(contract.TypeUnavailable).
			Message("cloud manager storage is unavailable").Build()
		if err := contract.WriteError(w, r, problem); err != nil {
			app.Logger().Warn("readiness error response write failed", plumelog.Fields{})
		}
	})
	store := inventory.NewStore(db)
	auditStore := audit.NewStore(db)
	operationStore := operations.NewStore(db)
	stream := concurrencylimit.Middleware(concurrencylimit.DefaultConfig(32))(operationhttp.OperationEventStreamHandler(operationStore, app.Logger()))
	operationBudget := operationhttp.OperationBudgetHandler(operationStore, app.Logger())
	deploymentHandler := operationhttp.BareMetalDeploymentHandler(operationStore, app.Logger())
	instanceOps := operationhttp.OperationHandler(operationStore, app.Logger())
	approve := operationhttp.OperationDecisionHandler(operationStore.Approve, app.Logger())
	resolve := operationhttp.OperationDecisionHandler(operationStore.Resolve, app.Logger())
	retryObservation := operationhttp.OperationDecisionHandler(operationStore.RetryCreationObservation, app.Logger())
	projectAlerts := governance.AlertHandler(alerts.NewStore(db), app.Logger())
	billingStore := billing.NewStore(db)
	budgets := billinghttp.BudgetHandler(billingStore, app.Logger())
	projectBudgets := billinghttp.ProjectBudgetHandler(billingStore, app.Logger())
	tagPolicy := governance.TagPolicyHandler(store, app.Logger())
	ticketPolicy := operationhttp.TicketPolicyHandler(operationStore, app.Logger())
	changeTicket := operationhttp.ChangeTicketHandler(operationStore, app.Logger())
	accountAdmin := identity.NewAccountAdmin(db)
	accountMutations := accounts.AccountHandler(accountAdmin, app.Logger())
	accountSchedules := accounts.InventorySchedulesHandler(controller.NewStore(db), app.Logger())
	if err := errors.Join(app.Get("/healthz", health), app.Get("/readyz", ready),
		app.Get(assets.AssetsRoute, assets.AssetHandler(store.List, app.Logger())),
		app.Get(assets.AssetsRoute+"/changes", assets.AssetHandler(store.History, app.Logger())),
		app.Get(assets.AssetsRoute+"/export", assets.InventoryHandler(store.Export, assets.WriteAssetCSV, true, app.Logger())),
		app.Get(assets.AssetsRoute+"/topology", assets.AssetHandler(store.Topology, app.Logger())),
		app.Get(assets.LoadBalancerConfigurationRoute, assets.LoadBalancerConfigurationHandler(store, app.Logger())),
		app.Get(assets.DatabaseBackupsRoute, assets.DatabaseBackupsHandler(store, app.Logger())),
		app.Get(assets.DatabaseRestoreWindowsRoute, assets.DatabaseRestoreWindowsHandler(store, app.Logger())),
		app.Get(assets.DNSRecordsRoute, assets.DNSRecordsHandler(store, app.Logger())),
		app.Get(assets.BucketVersioningRoute, assets.BucketSettingHandler(store.ReadBucketVersioning, app.Logger())),
		app.Post(operationhttp.BucketEncryptionEnableRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitBucketEncryption, operationhttp.BucketEncryptionRevisionError, app.Logger())),
		app.Post(operationhttp.BucketVersioningChangeRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitBucketVersioning, operationhttp.BucketVersioningRevisionError, app.Logger())),
		app.Get(assets.BucketEncryptionRoute, assets.BucketSettingHandler(store.ReadBucketEncryption, app.Logger())),
		app.Get(assets.ClusterNodePoolsRoute, assets.ClusterNodePoolsHandler(store, app.Logger())),
		app.Get(assets.ClusterWorkloadsRoute, assets.ClusterWorkloadsHandler(store, app.Logger())),
		app.Get(billinghttp.BillingRoute, billinghttp.BillingHandler(billingStore, app.Logger())),
		app.Get(billinghttp.BillingSummaryRoute, billinghttp.BillingSummaryHandler(billingStore, app.Logger())),
		app.Get(billinghttp.AllocationRoute, billinghttp.AllocationHandler(billingStore, app.Logger())),
		app.Post(billinghttp.AllocationRoute, billinghttp.AllocationWriteHandler(billingStore, app.Logger(), false)),
		app.Post(billinghttp.AllocationPreviewRoute, billinghttp.AllocationWriteHandler(billingStore, app.Logger(), true)),
		app.Get(billinghttp.BudgetRoute, budgets), app.Post(billinghttp.BudgetRoute, budgets), app.Put(billinghttp.BudgetRoute, budgets),
		app.Get(billinghttp.ProjectBudgetRoute, projectBudgets), app.Post(billinghttp.ProjectBudgetRoute, projectBudgets), app.Put(billinghttp.ProjectBudgetRoute, projectBudgets),
		app.Get(governance.TagPolicyRoute, tagPolicy), app.Post(governance.TagPolicyRoute, tagPolicy), app.Put(governance.TagPolicyRoute, tagPolicy),
		app.Get(operationhttp.TicketPolicyRoute, ticketPolicy), app.Post(operationhttp.TicketPolicyRoute, ticketPolicy), app.Put(operationhttp.TicketPolicyRoute, ticketPolicy),
		app.Get(operationhttp.ChangeTicketRoute, changeTicket), app.Post(operationhttp.ChangeTicketRoute, changeTicket),
		app.Get(assets.CMDBSnapshotRoute, assets.CMDBSnapshotHandler(store, app.Logger())),
		app.Get(governance.TagComplianceRoute, governance.TagComplianceHandler(store, app.Logger())),
		app.Get(governance.IdleInstancesRoute, governance.IdleInstancesHandler(store, app.Logger())),
		app.Get(governance.InventoryFreshnessRoute, governance.InventoryFreshnessHandler(store, app.Logger())),
		app.Get(governance.BucketProtectionRoute, governance.BucketProtectionHandler(store, app.Logger())),
		app.Get(governance.DatabaseProtectionRoute, governance.DatabaseProtectionHandler(store, app.Logger())),
		app.Get(governance.AuditRoute, governance.ProjectAuditHandler(auditStore, app.Logger(), false)),
		app.Get(governance.AuditRoute+"/export", governance.ProjectAuditHandler(auditStore, app.Logger(), true)),
		app.Get(governance.AlertsRoute, projectAlerts), app.Get(governance.AlertsRoute+"/:alert", projectAlerts),
		app.Post(governance.AlertsRoute+"/:alert/acknowledgement", projectAlerts),
		app.Patch(assets.AssetsRoute+"/ownership", assets.OwnershipHandler(store, app.Logger())),
		app.Post(operationhttp.InstancesRoute, operationhttp.CreationHandler(operationStore.SubmitCreation, app.Logger())),
		app.Get(assets.InstanceResizeOptionsRoute, assets.InstanceResizeOptionsHandler(store, false, app.Logger())),
		app.Post(assets.InstanceResizeOptionsRoute, assets.InstanceResizeOptionsHandler(store, true, app.Logger())),
		app.Post(operationhttp.InstanceResizeRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitInstanceResize, operationhttp.InstanceResizeRevisionError, app.Logger())),
		app.Get(operationhttp.OperationsRoute+"/retirement-preview", operationhttp.AssetOperationPreviewHandler(operationStore.PreviewInstanceRetirement, app.Logger())),
		app.Post(operationhttp.OperationsRoute+"/retire", operationhttp.VersionedOperationHandler(operationStore.SubmitInstanceRetirement, operationhttp.RetirementRevisionError, app.Logger())),
		app.Post(operationhttp.OperationsRoute, instanceOps), app.Get(operationhttp.OperationsRoute+"/deletion-preview", operationhttp.DeletionPreviewHandler(operationStore, app.Logger())), app.Get(operationhttp.OperationsRoute+"/:operation", instanceOps),
		app.Post(operationhttp.OperationsRoute+"/:operation/approval", approve),
		app.Post(operationhttp.OperationsRoute+"/:operation/resolution", resolve),
		app.Get(operationhttp.OperationsRoute+"/:operation/events", operationhttp.OperationEventsHandler(operationStore, app.Logger())),
		app.Post(operationhttp.OperationsRoute+"/:operation/retry-observation", retryObservation),
		app.Post(operationhttp.ImagesRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitImageCreation, operationhttp.SourceInstanceRevisionError, app.Logger())),
		app.Post(operationhttp.ImagesRoute+"/copy", operationhttp.VersionedOperationHandler(operationStore.SubmitImageCopy, operationhttp.ImageRevisionError, app.Logger())),
		app.Post(operationhttp.ImagesRoute+"/delete", operationhttp.VersionedOperationHandler(operationStore.SubmitImageDeletion, operationhttp.ImageRevisionError, app.Logger())),
		app.Post(operationhttp.DNSRecordCreationRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitDNSRecordCreation, operationhttp.DNSRecordRevisionError, app.Logger())),
		app.Post(operationhttp.DNSRecordDeletionRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitDNSRecordDeletion, operationhttp.DNSRecordRevisionError, app.Logger())),
		app.Post(operationhttp.WorkloadScaleRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitWorkloadScale, operationhttp.WorkloadScaleRevisionError, app.Logger())),
		app.Post(operationhttp.DNSRecordUpdateRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitDNSRecordUpdate, operationhttp.DNSRecordRevisionError, app.Logger())),
		app.Post(operationhttp.DNSRecordStatusRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitDNSRecordStatus, operationhttp.DNSRecordRevisionError, app.Logger())),
		app.Post(operationhttp.VolumesRoute, operationhttp.CreationHandler(operationStore.SubmitVolumeCreation, app.Logger())),
		app.Get(operationhttp.VolumeReleasePreviewRoute, operationhttp.AssetOperationPreviewHandler(operationStore.PreviewVolumeRelease, app.Logger())),
		app.Post(operationhttp.VolumeReleaseRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitVolumeRelease, operationhttp.VolumeRevisionAndCapacityError, app.Logger())),
		app.Post(operationhttp.VolumeResizeRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitVolumeResize, operationhttp.VolumeRevisionAndCapacityError, app.Logger())),
		app.Post(operationhttp.DatabaseRenameRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitDatabaseRename, operationhttp.DatabaseRenameRevisionError, app.Logger())),
		app.Post(operationhttp.DatabaseRecoveryRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitDatabaseRecovery, operationhttp.DatabaseRecoveryRevisionError, app.Logger())),
		app.Post(operationhttp.DeploymentsRoute, deploymentHandler),
		app.Get(operationhttp.DeploymentRoute, deploymentHandler),
		app.Post(operationhttp.DeploymentReceiptRoute, deploymentHandler),
		app.Post(operationhttp.ReclaimsRoute, operationhttp.BareMetalReclaimHandler(operationStore, app.Logger(), true)),
		app.Get(operationhttp.ReclaimsRoute+"/current", operationhttp.BareMetalCurrentReclaimHandler(operationStore, app.Logger())),
		app.Get(operationhttp.ReclaimRoute, operationhttp.BareMetalReclaimHandler(operationStore, app.Logger(), false)),
		app.Post(operationhttp.ReclaimRoute+"/approval", operationhttp.BareMetalReclaimTransitionHandler(operationStore.ReviewBareMetalReclaim, app.Logger())),
		app.Post(operationhttp.ReclaimRoute+"/claim", operationhttp.BareMetalReclaimTransitionHandler(operationStore.ClaimBareMetalReclaim, app.Logger())),
		app.Post(operationhttp.ReclaimRoute+"/receipt", operationhttp.BareMetalReclaimTransitionHandler(operationStore.RecordBareMetalReclaim, app.Logger())),
		app.Post(operationhttp.ReclaimRoute+"/recovery", operationhttp.BareMetalReclaimTransitionHandler(operationStore.RequestBareMetalReclaimRecovery, app.Logger())),
		app.Post(operationhttp.ReclaimRoute+"/recovery/approval", operationhttp.BareMetalReclaimTransitionHandler(operationStore.ReviewBareMetalReclaimRecovery, app.Logger())),
		app.Post(operationhttp.BareMetalPXERoute, operationhttp.VersionedOperationHandler(operationStore.SubmitBareMetalPXE, operationhttp.BareMetalPowerRevisionError, app.Logger())),
		app.Post(operationhttp.BareMetalPowerRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitBareMetalPower, operationhttp.BareMetalPowerRevisionError, app.Logger())),
		app.Post(operationhttp.CacheRenameRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitCacheRename, operationhttp.CacheRenameRevisionError, app.Logger())),
		app.Post(operationhttp.LoadBalancerRenameRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitLoadBalancerRename, operationhttp.LoadBalancerRenameRevisionError, app.Logger())),
		app.Post(operationhttp.LoadBalancerListenerRenameRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitLoadBalancerListenerRename, operationhttp.LoadBalancerListenerRenameRevisionError, app.Logger())),
		app.Post(operationhttp.LoadBalancerTargetGroupRenameRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitLoadBalancerTargetGroupRename, operationhttp.LoadBalancerTargetGroupRenameRevisionError, app.Logger())),
		app.Post(operationhttp.LoadBalancerBackendWeightRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitLoadBalancerBackendWeight, operationhttp.LoadBalancerBackendWeightRevisionError, app.Logger())),
		app.Post(operationhttp.LoadBalancerBackendDetachRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitLoadBalancerBackendDetach, operationhttp.LoadBalancerBackendDetachRevisionError, app.Logger())),
		app.Post(operationhttp.LoadBalancerBackendAttachRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitLoadBalancerBackendAttach, operationhttp.LoadBalancerBackendAttachRevisionError, app.Logger())),
		app.Post(operationhttp.SnapshotsRoute, operationhttp.VersionedOperationHandler(operationStore.SubmitSnapshotCreation, operationhttp.VolumeRevisionError, app.Logger())),
		app.Post(operationhttp.SnapshotsRoute+"/restore", operationhttp.VersionedOperationHandler(operationStore.SubmitSnapshotRestore, operationhttp.VolumeRevisionError, app.Logger())),
		app.Post(operationhttp.SnapshotsRoute+"/rollback", operationhttp.VersionedOperationHandler(operationStore.SubmitSnapshotRollback, operationhttp.VolumeRevisionError, app.Logger())),
		app.Post(operationhttp.SnapshotsRoute+"/delete", operationhttp.VersionedOperationHandler(operationStore.SubmitSnapshotDeletion, operationhttp.VolumeRevisionError, app.Logger())),
		app.Post(operationhttp.NetworkOperationsRoute, operationhttp.NetworkCreationHandler(operationStore, app.Logger())),
		app.Post(operationhttp.NetworkOperationsRoute+"/delete", operationhttp.VersionedOperationHandler(operationStore.SubmitNetworkDeletion, operationhttp.NetworkDeletionRevisionError, app.Logger())),
		app.Post(operationhttp.NetworkOperationsRoute+"/revoke-rule", operationhttp.VersionedOperationHandler(operationStore.SubmitSecurityRuleRevocation, operationhttp.NetworkRevisionError, app.Logger())),
		app.Post(operationhttp.NetworkOperationsRoute+"/release-elastic-ip", operationhttp.VersionedOperationHandler(operationStore.SubmitElasticIPRelease, operationhttp.ElasticIPRevisionError, app.Logger())),
		app.Post(operationhttp.NetworkOperationsRoute+"/bind-elastic-ip", operationhttp.VersionedOperationHandler(operationStore.SubmitElasticIPBinding, operationhttp.ElasticIPBindingRevisionError, app.Logger())),
		app.Post(operationhttp.NetworkOperationsRoute+"/unbind-elastic-ip", operationhttp.VersionedOperationHandler(operationStore.SubmitElasticIPUnbinding, operationhttp.ElasticIPBindingRevisionError, app.Logger())),
		app.Get(operationhttp.NetworkOperationsRoute+"/:operation", instanceOps),
		app.Post(operationhttp.NetworkOperationsRoute+"/:operation/approval", approve),
		app.Post(operationhttp.NetworkOperationsRoute+"/:operation/resolution", resolve),
		app.Get(operationhttp.NetworkOperationsRoute+"/:operation/events", operationhttp.OperationEventsHandler(operationStore, app.Logger())),
		app.Post(operationhttp.NetworkOperationsRoute+"/:operation/retry-observation", retryObservation),
		app.Get(operationhttp.ProjectOperationsRoute, operationhttp.OperationListHandler(operationStore, app.Logger())),
		app.Get(operationhttp.OperationBudgetRoute, operationBudget),
		app.Put(operationhttp.OperationBudgetRoute, operationBudget),
		app.Get(operationhttp.OperationBacklogRoute, operationhttp.OperationBacklogHandler(operationStore, app.Logger())),
		app.Get(operationhttp.OperationLatencyRoute, operationhttp.OperationLatencyHandler(operationStore, app.Logger())),
		app.Get(operationhttp.ProjectOperationsRoute+"/:operation", instanceOps),
		app.Get(operationhttp.OperationEventStreamRoute, stream),
		app.Get(operationhttp.ProjectOperationsRoute+"/:operation/events", operationhttp.OperationEventsHandler(operationStore, app.Logger())),
		app.Post(operationhttp.ProjectOperationsRoute+"/:operation/approval", approve),
		app.Post(operationhttp.ProjectOperationsRoute+"/:operation/resolution", resolve),
		app.Post(operationhttp.ProjectOperationsRoute+"/:operation/retry-observation", retryObservation),
		app.Post(operationhttp.OperationBatchesRoute+"/preview", operationhttp.OperationBatchHandler(operationStore, app.Logger(), true)),
		app.Post(operationhttp.OperationBatchesRoute, operationhttp.OperationBatchHandler(operationStore, app.Logger(), false)),
		app.Get(operationhttp.OperationBatchesRoute+"/:batch", operationhttp.OperationBatchHandler(operationStore, app.Logger(), false)),
		app.Get(accounts.AccountSchedulesRoute, accountSchedules),
		app.Post(accounts.AccountSchedulesRoute, accountSchedules),
		app.Patch(accounts.AccountScheduleRoute, accountSchedules),
		app.Get(accounts.AccountsRoute, accounts.AccountInventoryHandler(accountAdmin, app.Logger(), false)),
		app.Get(accounts.AccountGrantsRoute, accounts.AccountInventoryHandler(accountAdmin, app.Logger(), true)),
		app.Get(accounts.AccountRolloutRoute, accounts.AccountRolloutHandler(operationStore, app.Logger())),
		app.Put(accounts.AccountRolloutRoute, accounts.AccountRolloutHandler(operationStore, app.Logger())),
		app.Post(accounts.AccountsRoute, accountMutations), app.Get(accounts.AccountsRoute+"/:account", accountMutations), app.Patch(accounts.AccountsRoute+"/:account", accountMutations)); err != nil {
		return nil, err
	}
	if console != nil {
		if err := consolehttp.RegisterConsole(app, *console, identity.NewBrowserSessions(db)); err != nil {
			return nil, err
		}
	}
	nativeEnabled := console != nil && (console.NativeConnect != nil || console.NativeSerial != nil)
	if nativeEnabled {
		if err := consolehttp.RegisterNativeConsole(app, *console, nativeconsole.NewStore(db)); err != nil {
			return nil, err
		}
	}
	if err := accounts.RegisterCatalog(app, operationStore); err != nil {
		return nil, err
	}
	if err := kvmapp.Register(app, kvm.NewStore(db)); err != nil {
		return nil, err
	}
	if err := registerOpenAPI(app, console); err != nil {
		return nil, err
	}
	return app, nil
}
