package main

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

func newAPI(db *sql.DB, addr string, console *consoleConfig) (*core.App, error) {
	cfg := core.DefaultConfig()
	cfg.Addr = "127.0.0.1:8096"
	if addr != "" {
		cfg.Addr = addr
	}
	if console != nil {
		cfg.TLS = core.TLSConfig{Enabled: true, CertFile: console.certFile, KeyFile: console.keyFile}
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
		inventory.InstanceTypeInventoryMigration, operations.InstanceResizeMigration, inventory.InstanceResizeOptionsMigration, operations.BucketVersioningMigration, operations.BucketEncryptionEnableMigration, operations.DNSRecordUpdateMigration, billing.AllocationPublishMigration, inventory.DatabaseBackupsMigration, inventory.DatabaseRestoreWindowsMigration, operations.DatabaseRecoveryMigration, inventory.DatabaseProtectionMigration)
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
	deploymentHandler := bareMetalDeploymentHandler(operationStore, app.Logger())
	instanceOps := operationHandler(operationStore, app.Logger())
	approve := operationDecisionHandler(operationStore.Approve, app.Logger())
	resolve := operationDecisionHandler(operationStore.Resolve, app.Logger())
	retryObservation := operationDecisionHandler(operationStore.RetryCreationObservation, app.Logger())
	projectAlerts := alertHandler(alerts.NewStore(db), app.Logger())
	billingStore := billing.NewStore(db)
	budgets := budgetHandler(billingStore, app.Logger())
	projectBudgets := projectBudgetHandler(billingStore, app.Logger())
	tagPolicy := tagPolicyHandler(store, app.Logger())
	ticketPolicy := ticketPolicyHandler(operationStore, app.Logger())
	changeTicket := changeTicketHandler(operationStore, app.Logger())
	accountAdmin := identity.NewAccountAdmin(db)
	accounts := accountHandler(accountAdmin, app.Logger())
	accountSchedules := inventorySchedulesHandler(controller.NewStore(db), app.Logger())
	if err := errors.Join(app.Get("/healthz", health), app.Get("/readyz", ready),
		app.Get(assetsRoute, assetHandler(store.List, app.Logger())),
		app.Get(assetsRoute+"/changes", assetHandler(store.History, app.Logger())),
		app.Get(assetsRoute+"/export", inventoryHandler(store.Export, writeAssetCSV, true, app.Logger())),
		app.Get(assetsRoute+"/topology", assetHandler(store.Topology, app.Logger())),
		app.Get(loadBalancerConfigurationRoute, loadBalancerConfigurationHandler(store, app.Logger())),
		app.Get(databaseBackupsRoute, databaseBackupsHandler(store, app.Logger())),
		app.Get(databaseRestoreWindowsRoute, databaseRestoreWindowsHandler(store, app.Logger())),
		app.Get(dnsRecordsRoute, dnsRecordsHandler(store, app.Logger())),
		app.Get(bucketVersioningRoute, bucketSettingHandler(store.ReadBucketVersioning, app.Logger())),
		app.Post(bucketEncryptionEnableRoute, versionedOperationHandler(operationStore.SubmitBucketEncryption, bucketEncryptionRevisionError, app.Logger())),
		app.Post(bucketVersioningChangeRoute, versionedOperationHandler(operationStore.SubmitBucketVersioning, bucketVersioningRevisionError, app.Logger())),
		app.Get(bucketEncryptionRoute, bucketSettingHandler(store.ReadBucketEncryption, app.Logger())),
		app.Get(clusterNodePoolsRoute, clusterNodePoolsHandler(store, app.Logger())),
		app.Get(clusterWorkloadsRoute, clusterWorkloadsHandler(store, app.Logger())),
		app.Get(billingRoute, billingHandler(billingStore, app.Logger())),
		app.Get(billingSummaryRoute, billingSummaryHandler(billingStore, app.Logger())),
		app.Get(allocationRoute, allocationHandler(billingStore, app.Logger())),
		app.Post(allocationRoute, allocationWriteHandler(billingStore, app.Logger(), false)),
		app.Post(allocationPreviewRoute, allocationWriteHandler(billingStore, app.Logger(), true)),
		app.Get(budgetRoute, budgets), app.Post(budgetRoute, budgets), app.Put(budgetRoute, budgets),
		app.Get(projectBudgetRoute, projectBudgets), app.Post(projectBudgetRoute, projectBudgets), app.Put(projectBudgetRoute, projectBudgets),
		app.Get(tagPolicyRoute, tagPolicy), app.Post(tagPolicyRoute, tagPolicy), app.Put(tagPolicyRoute, tagPolicy),
		app.Get(ticketPolicyRoute, ticketPolicy), app.Post(ticketPolicyRoute, ticketPolicy), app.Put(ticketPolicyRoute, ticketPolicy),
		app.Get(changeTicketRoute, changeTicket), app.Post(changeTicketRoute, changeTicket),
		app.Get(cmdbSnapshotRoute, cmdbSnapshotHandler(store, app.Logger())),
		app.Get(tagComplianceRoute, tagComplianceHandler(store, app.Logger())),
		app.Get(idleInstancesRoute, idleInstancesHandler(store, app.Logger())),
		app.Get(inventoryFreshnessRoute, inventoryFreshnessHandler(store, app.Logger())),
		app.Get(bucketProtectionRoute, bucketProtectionHandler(store, app.Logger())),
		app.Get(databaseProtectionRoute, databaseProtectionHandler(store, app.Logger())),
		app.Get(auditRoute, projectAuditHandler(auditStore, app.Logger(), false)),
		app.Get(auditRoute+"/export", projectAuditHandler(auditStore, app.Logger(), true)),
		app.Get(alertsRoute, projectAlerts), app.Get(alertsRoute+"/:alert", projectAlerts),
		app.Post(alertsRoute+"/:alert/acknowledgement", projectAlerts),
		app.Patch(assetsRoute+"/ownership", ownershipHandler(store, app.Logger())),
		app.Post(instancesRoute, creationHandler(operationStore.SubmitCreation, app.Logger())),
		app.Get(instanceResizeOptionsRoute, instanceResizeOptionsHandler(store, false, app.Logger())),
		app.Post(instanceResizeOptionsRoute, instanceResizeOptionsHandler(store, true, app.Logger())),
		app.Post(instanceResizeRoute, versionedOperationHandler(operationStore.SubmitInstanceResize, instanceResizeRevisionError, app.Logger())),
		app.Get(operationsRoute+"/retirement-preview", assetOperationPreviewHandler(operationStore.PreviewInstanceRetirement, app.Logger())),
		app.Post(operationsRoute+"/retire", versionedOperationHandler(operationStore.SubmitInstanceRetirement, retirementRevisionError, app.Logger())),
		app.Post(operationsRoute, instanceOps), app.Get(operationsRoute+"/deletion-preview", deletionPreviewHandler(operationStore, app.Logger())), app.Get(operationsRoute+"/:operation", instanceOps),
		app.Post(operationsRoute+"/:operation/approval", approve),
		app.Post(operationsRoute+"/:operation/resolution", resolve),
		app.Get(operationsRoute+"/:operation/events", operationEventsHandler(operationStore, app.Logger())),
		app.Post(operationsRoute+"/:operation/retry-observation", retryObservation),
		app.Post(imagesRoute, versionedOperationHandler(operationStore.SubmitImageCreation, sourceInstanceRevisionError, app.Logger())),
		app.Post(imagesRoute+"/copy", versionedOperationHandler(operationStore.SubmitImageCopy, imageRevisionError, app.Logger())),
		app.Post(imagesRoute+"/delete", versionedOperationHandler(operationStore.SubmitImageDeletion, imageRevisionError, app.Logger())),
		app.Post(dnsRecordCreationRoute, versionedOperationHandler(operationStore.SubmitDNSRecordCreation, dnsRecordRevisionError, app.Logger())),
		app.Post(dnsRecordDeletionRoute, versionedOperationHandler(operationStore.SubmitDNSRecordDeletion, dnsRecordRevisionError, app.Logger())),
		app.Post(workloadScaleRoute, versionedOperationHandler(operationStore.SubmitWorkloadScale, workloadScaleRevisionError, app.Logger())),
		app.Post(dnsRecordUpdateRoute, versionedOperationHandler(operationStore.SubmitDNSRecordUpdate, dnsRecordRevisionError, app.Logger())),
		app.Post(dnsRecordStatusRoute, versionedOperationHandler(operationStore.SubmitDNSRecordStatus, dnsRecordRevisionError, app.Logger())),
		app.Post(volumesRoute, creationHandler(operationStore.SubmitVolumeCreation, app.Logger())),
		app.Get(volumeReleasePreviewRoute, assetOperationPreviewHandler(operationStore.PreviewVolumeRelease, app.Logger())),
		app.Post(volumeReleaseRoute, versionedOperationHandler(operationStore.SubmitVolumeRelease, volumeRevisionAndCapacityError, app.Logger())),
		app.Post(volumeResizeRoute, versionedOperationHandler(operationStore.SubmitVolumeResize, volumeRevisionAndCapacityError, app.Logger())),
		app.Post(databaseRenameRoute, versionedOperationHandler(operationStore.SubmitDatabaseRename, databaseRenameRevisionError, app.Logger())),
		app.Post(databaseRecoveryRoute, versionedOperationHandler(operationStore.SubmitDatabaseRecovery, databaseRecoveryRevisionError, app.Logger())),
		app.Post(deploymentsRoute, deploymentHandler),
		app.Get(deploymentRoute, deploymentHandler),
		app.Post(deploymentReceiptRoute, deploymentHandler),
		app.Post(reclaimsRoute, bareMetalReclaimHandler(operationStore, app.Logger(), true)),
		app.Get(reclaimsRoute+"/current", bareMetalCurrentReclaimHandler(operationStore, app.Logger())),
		app.Get(reclaimRoute, bareMetalReclaimHandler(operationStore, app.Logger(), false)),
		app.Post(reclaimRoute+"/approval", bareMetalReclaimTransitionHandler(operationStore.ReviewBareMetalReclaim, app.Logger())),
		app.Post(reclaimRoute+"/claim", bareMetalReclaimTransitionHandler(operationStore.ClaimBareMetalReclaim, app.Logger())),
		app.Post(reclaimRoute+"/receipt", bareMetalReclaimTransitionHandler(operationStore.RecordBareMetalReclaim, app.Logger())),
		app.Post(reclaimRoute+"/recovery", bareMetalReclaimTransitionHandler(operationStore.RequestBareMetalReclaimRecovery, app.Logger())),
		app.Post(reclaimRoute+"/recovery/approval", bareMetalReclaimTransitionHandler(operationStore.ReviewBareMetalReclaimRecovery, app.Logger())),
		app.Post(bareMetalPXERoute, versionedOperationHandler(operationStore.SubmitBareMetalPXE, bareMetalPowerRevisionError, app.Logger())),
		app.Post(bareMetalPowerRoute, versionedOperationHandler(operationStore.SubmitBareMetalPower, bareMetalPowerRevisionError, app.Logger())),
		app.Post(cacheRenameRoute, versionedOperationHandler(operationStore.SubmitCacheRename, cacheRenameRevisionError, app.Logger())),
		app.Post(loadBalancerRenameRoute, versionedOperationHandler(operationStore.SubmitLoadBalancerRename, loadBalancerRenameRevisionError, app.Logger())),
		app.Post(loadBalancerListenerRenameRoute, versionedOperationHandler(operationStore.SubmitLoadBalancerListenerRename, loadBalancerListenerRenameRevisionError, app.Logger())),
		app.Post(loadBalancerTargetGroupRenameRoute, versionedOperationHandler(operationStore.SubmitLoadBalancerTargetGroupRename, loadBalancerTargetGroupRenameRevisionError, app.Logger())),
		app.Post(loadBalancerBackendWeightRoute, versionedOperationHandler(operationStore.SubmitLoadBalancerBackendWeight, loadBalancerBackendWeightRevisionError, app.Logger())),
		app.Post(loadBalancerBackendDetachRoute, versionedOperationHandler(operationStore.SubmitLoadBalancerBackendDetach, loadBalancerBackendDetachRevisionError, app.Logger())),
		app.Post(loadBalancerBackendAttachRoute, versionedOperationHandler(operationStore.SubmitLoadBalancerBackendAttach, loadBalancerBackendAttachRevisionError, app.Logger())),
		app.Post(snapshotsRoute, versionedOperationHandler(operationStore.SubmitSnapshotCreation, volumeRevisionError, app.Logger())),
		app.Post(snapshotsRoute+"/restore", versionedOperationHandler(operationStore.SubmitSnapshotRestore, volumeRevisionError, app.Logger())),
		app.Post(snapshotsRoute+"/rollback", versionedOperationHandler(operationStore.SubmitSnapshotRollback, volumeRevisionError, app.Logger())),
		app.Post(snapshotsRoute+"/delete", versionedOperationHandler(operationStore.SubmitSnapshotDeletion, volumeRevisionError, app.Logger())),
		app.Post(networkOperationsRoute, networkCreationHandler(operationStore, app.Logger())),
		app.Post(networkOperationsRoute+"/delete", versionedOperationHandler(operationStore.SubmitNetworkDeletion, networkDeletionRevisionError, app.Logger())),
		app.Post(networkOperationsRoute+"/revoke-rule", versionedOperationHandler(operationStore.SubmitSecurityRuleRevocation, networkRevisionError, app.Logger())),
		app.Post(networkOperationsRoute+"/release-elastic-ip", versionedOperationHandler(operationStore.SubmitElasticIPRelease, elasticIPRevisionError, app.Logger())),
		app.Post(networkOperationsRoute+"/bind-elastic-ip", versionedOperationHandler(operationStore.SubmitElasticIPBinding, elasticIPBindingRevisionError, app.Logger())),
		app.Post(networkOperationsRoute+"/unbind-elastic-ip", versionedOperationHandler(operationStore.SubmitElasticIPUnbinding, elasticIPBindingRevisionError, app.Logger())),
		app.Get(networkOperationsRoute+"/:operation", instanceOps),
		app.Post(networkOperationsRoute+"/:operation/approval", approve),
		app.Post(networkOperationsRoute+"/:operation/resolution", resolve),
		app.Get(networkOperationsRoute+"/:operation/events", operationEventsHandler(operationStore, app.Logger())),
		app.Post(networkOperationsRoute+"/:operation/retry-observation", retryObservation),
		app.Get(projectOperationsRoute, operationListHandler(operationStore, app.Logger())),
		app.Get(operationBacklogRoute, operationBacklogHandler(operationStore, app.Logger())),
		app.Get(operationLatencyRoute, operationLatencyHandler(operationStore, app.Logger())),
		app.Get(projectOperationsRoute+"/:operation", instanceOps),
		app.Get(projectOperationsRoute+"/:operation/events", operationEventsHandler(operationStore, app.Logger())),
		app.Post(projectOperationsRoute+"/:operation/approval", approve),
		app.Post(projectOperationsRoute+"/:operation/resolution", resolve),
		app.Post(projectOperationsRoute+"/:operation/retry-observation", retryObservation),
		app.Post(operationBatchesRoute+"/preview", operationBatchHandler(operationStore, app.Logger(), true)),
		app.Post(operationBatchesRoute, operationBatchHandler(operationStore, app.Logger(), false)),
		app.Get(operationBatchesRoute+"/:batch", operationBatchHandler(operationStore, app.Logger(), false)),
		app.Get(accountSchedulesRoute, accountSchedules),
		app.Post(accountSchedulesRoute, accountSchedules),
		app.Patch(accountScheduleRoute, accountSchedules),
		app.Get(accountsRoute, accountInventoryHandler(accountAdmin, app.Logger(), false)),
		app.Get(accountGrantsRoute, accountInventoryHandler(accountAdmin, app.Logger(), true)),
		app.Get(accountRolloutRoute, accountRolloutHandler(operationStore, app.Logger())),
		app.Put(accountRolloutRoute, accountRolloutHandler(operationStore, app.Logger())),
		app.Post(accountsRoute, accounts), app.Get(accountsRoute+"/:account", accounts), app.Patch(accountsRoute+"/:account", accounts)); err != nil {
		return nil, err
	}
	if console != nil {
		if err := registerConsole(app, *console, identity.NewBrowserSessions(db)); err != nil {
			return nil, err
		}
	}
	nativeEnabled := console != nil && (console.nativeConnect != nil || console.nativeSerial != nil)
	if nativeEnabled {
		if err := registerNativeConsole(app, *console, nativeconsole.NewStore(db)); err != nil {
			return nil, err
		}
	}
	if err := registerCatalog(app, operationStore); err != nil {
		return nil, err
	}
	if err := registerKVM(app, kvm.NewStore(db)); err != nil {
		return nil, err
	}
	if err := registerOpenAPI(app, console); err != nil {
		return nil, err
	}
	return app, nil
}
