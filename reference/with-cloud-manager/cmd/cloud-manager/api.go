package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
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
	ready := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		var applied int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_migrations
			WHERE version IN ('0001_identity','0002_inventory','0003_service_tokens','0004_inventory_schedules','0005_inventory_history','0006_inventory_topology','0007_account_events','0008_asset_ownership','0009_tag_assignment','0010_instance_operations','0011_operation_worker','0012_operation_resolution','0013_operation_approval','0014_instance_deletion','0015_confirmed_instance_deletions','0016_instance_creation','0017_creation_receipt','0018_volume_operations','0019_operation_outbox','0020_network_operations','0021_security_group_creation','0022_operation_queries','0023_operation_batches','0024_browser_sessions','0025_project_audit','0026_instance_catalog','0027_catalog_audit','0028_project_alerts','0029_native_console','0030_aws_serial_console','0031_security_group_rules','0032_security_rule_revocation','0033_subnet_zone','0034_security_rule_outbox','0035_inventory_public_addresses','0036_network_asset_schedules','0037_elastic_ip_creation','0038_elastic_ip_release','0039_elastic_ip_binding','0040_snapshot_inventory','0041_snapshot_schedules','0042_snapshot_creation','0043_snapshot_deletion','0044_snapshot_restore','0045_snapshot_rollback','0046_image_deletion','0047_image_deletion_outbox','0048_instance_image_reservations','0049_image_creation','0050_image_copy_reservations','0051_image_copy','0052_network_deletion','0053_security_group_deletion','0054_network_deletion_reservations','0055_network_deletion_backfill')`).Scan(&applied)
		if err == nil && applied == 55 {
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
	instanceOps := operationHandler(operationStore, app.Logger())
	approve := operationDecisionHandler(operationStore.Approve, app.Logger())
	resolve := operationDecisionHandler(operationStore.Resolve, app.Logger())
	retryObservation := operationDecisionHandler(operationStore.RetryCreationObservation, app.Logger())
	projectAlerts := alertHandler(alerts.NewStore(db), app.Logger())
	accounts := accountHandler(identity.NewAccountAdmin(db), app.Logger())
	if err := errors.Join(app.Get("/healthz", health), app.Get("/readyz", ready),
		app.Get(assetsRoute, assetHandler(store.List, app.Logger())),
		app.Get(assetsRoute+"/changes", assetHandler(store.History, app.Logger())),
		app.Get(assetsRoute+"/export", inventoryHandler(store.Export, writeAssetCSV, true, app.Logger())),
		app.Get(assetsRoute+"/topology", assetHandler(store.Topology, app.Logger())),
		app.Get(auditRoute, projectAuditHandler(auditStore, app.Logger(), false)),
		app.Get(auditRoute+"/export", projectAuditHandler(auditStore, app.Logger(), true)),
		app.Get(alertsRoute, projectAlerts), app.Get(alertsRoute+"/:alert", projectAlerts),
		app.Post(alertsRoute+"/:alert/acknowledgement", projectAlerts),
		app.Patch(assetsRoute+"/ownership", ownershipHandler(store, app.Logger())),
		app.Post(instancesRoute, creationHandler(operationStore, app.Logger())),
		app.Post(operationsRoute, instanceOps), app.Get(operationsRoute+"/deletion-preview", deletionPreviewHandler(operationStore, app.Logger())), app.Get(operationsRoute+"/:operation", instanceOps),
		app.Post(operationsRoute+"/:operation/approval", approve),
		app.Post(operationsRoute+"/:operation/resolution", resolve),
		app.Get(operationsRoute+"/:operation/events", operationEventsHandler(operationStore, app.Logger())),
		app.Post(operationsRoute+"/:operation/retry-observation", retryObservation),
		app.Post(imagesRoute, versionedOperationHandler(operationStore.SubmitImageCreation, sourceInstanceRevisionError, app.Logger())),
		app.Post(imagesRoute+"/copy", versionedOperationHandler(operationStore.SubmitImageCopy, imageRevisionError, app.Logger())),
		app.Post(imagesRoute+"/delete", versionedOperationHandler(operationStore.SubmitImageDeletion, imageRevisionError, app.Logger())),
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
		app.Get(projectOperationsRoute+"/:operation", instanceOps),
		app.Get(projectOperationsRoute+"/:operation/events", operationEventsHandler(operationStore, app.Logger())),
		app.Post(projectOperationsRoute+"/:operation/approval", approve),
		app.Post(projectOperationsRoute+"/:operation/resolution", resolve),
		app.Post(projectOperationsRoute+"/:operation/retry-observation", retryObservation),
		app.Post(operationBatchesRoute+"/preview", operationBatchHandler(operationStore, app.Logger(), true)),
		app.Post(operationBatchesRoute, operationBatchHandler(operationStore, app.Logger(), false)),
		app.Get(operationBatchesRoute+"/:batch", operationBatchHandler(operationStore, app.Logger(), false)),
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
	if err := registerOpenAPI(app, console); err != nil {
		return nil, err
	}
	return app, nil
}
