package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/kvm"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/operations"
)

func TestAcceptancePostgresReadiness(t *testing.T) {
	db := dbtest.Open(t)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	get := func(path string, wantStatus int, wantBody string) {
		t.Helper()
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != wantStatus || !strings.Contains(w.Body.String(), wantBody) || strings.Contains(w.Body.String(), "cmp_migrations") {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
		}
	}
	get("/healthz", http.StatusOK, `"ok"`)
	get("/openapi.json", http.StatusOK, `"renameCacheInstance"`)
	get("/openapi.json", http.StatusOK, `"resizeVolume"`)
	get("/openapi.json", http.StatusOK, `"setBucketVersioning"`)
	get("/openapi.json", http.StatusOK, `"enableBucketEncryption"`)
	get("/openapi.json", http.StatusOK, `"updateDNSRecord"`)
	get("/openapi.json", http.StatusOK, `"publishBillingAllocation"`)
	get("/openapi.json", http.StatusOK, `"previewBillingAllocation"`)
	get("/openapi.json", http.StatusOK, `"getDatabaseBackups"`)
	get("/openapi.json", http.StatusOK, `"getDatabaseRestoreWindows"`)
	get("/openapi.json", http.StatusOK, `"recoverDatabaseInstance"`)
	get("/openapi.json", http.StatusOK, `"readInstanceResizeOptions"`)
	get("/openapi.json", http.StatusOK, `"refreshInstanceResizeOptions"`)
	get("/openapi.json", http.StatusOK, `"createVolume"`)
	get("/openapi.json", http.StatusOK, `"releaseVolume"`)
	get("/openapi.json", http.StatusOK, `"listKVMHostFenceWork"`)
	get("/openapi.json", http.StatusOK, `"reviewKVMColdMigration"`)
	get("/openapi.json", http.StatusOK, `"cancelKVMEvacuationPlan"`)
	get("/openapi.json", http.StatusOK, `"reportKVMHostImage"`)
	get("/openapi.json", http.StatusOK, `"registerKVMSharedStorage"`)
	get("/openapi.json", http.StatusOK, `"applyKVMLiveMigrationCommand"`)
	get("/openapi.json", http.StatusOK, `"applyKVMVolumeCleanupCommand"`)
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := identity.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := inventory.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := identity.MigrateServiceTokens(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := controller.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := identity.MigrateAccounts(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := operations.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := identity.MigrateBrowserSessions(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := audit.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := alerts.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := nativeconsole.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := billing.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := billing.MigrateBudgets(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := billing.MigrateProjectBudgets(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := alerts.MigrateBucketProtection(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := billing.MigrateAllocations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := billing.MigratePulls(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := inventory.MigrateTagPolicies(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := inventory.MigrateIdleAnalysis(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateChangeTicketEvents(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateDatabaseRenameEvents(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateCacheRenameEvents(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := kvm.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateBareMetalPower(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateBareMetalPXE(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateVolumeResize(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateVolumeCreation(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateVolumeRelease(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateInstanceResize(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := inventory.MigrateInstanceResizeOptions(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateBucketVersioning(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := operations.MigrateBucketEncryptionEnable(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := operations.MigrateDNSRecordUpdate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := billing.MigrateAllocationPublish(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := inventory.MigrateDatabaseBackups(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := inventory.MigrateDatabaseRestoreWindows(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := inventory.MigrateDatabaseProtection(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := operations.MigrateDatabaseRecovery(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := operations.MigrateBareMetalDeployments(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateBareMetalReclaims(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := operations.MigrateBareMetalReclaimRecoveries(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := identity.MigrateAccountWriteMode(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := inventory.MigrateTagPolicyAlerts(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := alerts.MigrateOperationQueue(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusOK, `"ready"`)
	for _, version := range []string{kvm.HostFenceMigration, kvm.FencedVMRecoveryMigration, kvm.ColdMigrationVersion,
		kvm.EvacuationMigration, kvm.AutomaticEvacuationMigration, kvm.EvacuationBatchMigration, kvm.EvacuationPlanMigration,
		kvm.HostImageMigration, kvm.SharedStorageMigration, kvm.LiveMigrationVersion, kvm.VolumeCleanupMigration,
		inventory.InventoryCompletionMigration, identity.AccountWriteModeMigration, controller.ScheduleConfigMigration,
		controller.AccountDispatchMigration, controller.TenantDispatchMigration, controller.DatabaseObservationScheduleMigration, operations.ObservationWorkerMigration,
		operations.EffectIDMigration, operations.TerminalEventTimeMigration, alerts.BucketProtectionMigration, inventory.TagPolicyAlertsMigration,
		operations.FirstLeaseMigration, operations.QueueSnapshotMigration, alerts.OperationQueueMigration, operations.QueueFindingsMigration,
		operations.ObservationQueueRepairMigration, operations.PowerObservationMigration, inventory.VolumeInventoryMigration, operations.VolumeResizeMigration, operations.VolumeCreationMigration, operations.VolumeReleaseMigration, inventory.InstanceTypeInventoryMigration, operations.InstanceResizeMigration, inventory.InstanceResizeOptionsMigration, operations.BucketVersioningMigration, operations.BucketEncryptionEnableMigration, operations.DNSRecordUpdateMigration, billing.AllocationPublishMigration, inventory.DatabaseBackupsMigration, inventory.DatabaseRestoreWindowsMigration, operations.DatabaseRecoveryMigration, inventory.DatabaseProtectionMigration} {
		if _, err := db.ExecContext(t.Context(), "DELETE FROM cmp_migrations WHERE version=$1", version); err != nil {
			t.Fatal(err)
		}
		get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
		if _, err := db.ExecContext(t.Context(), "INSERT INTO cmp_migrations(version) VALUES ($1)", version); err != nil {
			t.Fatal(err)
		}
		get("/readyz", http.StatusOK, `"ready"`)
	}
	if _, err := db.ExecContext(t.Context(), "DELETE FROM cmp_migrations WHERE version IN ('0034_security_rule_outbox','0047_image_deletion_outbox')"); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if err := alerts.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusOK, `"ready"`)
	if _, err := db.ExecContext(t.Context(), "DELETE FROM cmp_migrations WHERE version=$1", kvm.BootImageMigration); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	if _, err := db.ExecContext(t.Context(), "INSERT INTO cmp_migrations(version) VALUES ($1)", kvm.BootImageMigration); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusOK, `"ready"`)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.ExecContext(t.Context(), "SET default_transaction_read_only=on"); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
	get("/healthz", http.StatusOK, `"ok"`)
	if _, err := db.ExecContext(t.Context(), "RESET default_transaction_read_only"); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusOK, `"ready"`)
	if _, err := db.ExecContext(t.Context(), "DELETE FROM cmp_migrations WHERE version=$1", controller.RedfishScheduleMigration); err != nil {
		t.Fatal(err)
	}
	get("/readyz", http.StatusServiceUnavailable, "storage is unavailable")
}
