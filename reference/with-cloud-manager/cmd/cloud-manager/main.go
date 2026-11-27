package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/kvm"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) (retErr error) {
	if !validCommand(args) {
		return errors.New("usage: cloud-manager billing COMMAND TENANT PROJECT ACCOUNT PERIOD SOURCE ...|billing-{import,replay,source,allocate,allocation-replay} TENANT PROJECT ACCOUNT PERIOD SOURCE ...|cmdb-migrate|cmdb-sync TENANT PROJECT|cmdb-status TENANT PROJECT|kvm COMMAND TENANT PROJECT SITE|migrate|api|controller|workload-controller|kvm-evacuation-controller|alert-controller|idle-controller|idle-schedule TENANT ACCOUNT REGION INSTANCE DURATION_OR_OFF|billing-pull-controller|billing-pull[-status|-source|-replay] TENANT PROJECT ACCOUNT PERIOD SOURCE ...|worker|sessions-purge|operations COMMAND TENANT PROJECT|governance COMMAND TENANT PROJECT|cloud-accounts COMMAND TENANT PROJECT|audit {list,export} TENANT PROJECT|catalog COMMAND TENANT PROJECT|alerts COMMAND TENANT PROJECT|console-grants COMMAND TENANT PROJECT|event-publisher|event-{register,disable,failures,reconcile} SINK|event-retry SINK EVENT_ID|verify-{openstack,aws,tencent,alibaba,huawei,vsphere,redfish} TENANT ACCOUNT|scan-{openstack,aws,tencent,alibaba,huawei,vsphere,redfish} TENANT ACCOUNT REGION KIND (or append ASSET_ID for a public-cloud load-balancer, database-instance, cache-instance, kubernetes-cluster or object-bucket absence check)|refresh-{tencent,alibaba,huawei}-load-balancer TENANT ACCOUNT REGION ASSET_ID [--include-backends]|refresh-{tencent,alibaba,huawei}-dns-zone TENANT ACCOUNT ZONE_ID|refresh-{tencent,alibaba,huawei}-bucket-{versioning,encryption} TENANT ACCOUNT ASSET_ID|refresh-{tencent,alibaba,huawei}-cluster-{node-pools,workloads} TENANT ACCOUNT REGION CLUSTER_ID")
	}
	if strings.HasPrefix(args[0], "cmdb-") {
		return runCMDB(parent, args, os.Getenv, os.Stdout)
	}
	if args[0] == "kvm" {
		return runKVMClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdin, os.Stdout, nil)
	}
	if args[0] == "operations" {
		return runOperationsClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdin, os.Stdout, nil)
	}
	if args[0] == "billing" {
		return runBillingClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdin, os.Stdout, nil)
	}
	if args[0] == "governance" {
		return runGovernanceClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdin, os.Stdout, nil)
	}
	if args[0] == "cloud-accounts" {
		return runCloudAccountsClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdin, os.Stdout, nil)
	}
	if args[0] == "audit" {
		return runAuditClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdout, nil)
	}
	if args[0] == "catalog" {
		return runCatalogClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdin, os.Stdout, nil)
	}
	if args[0] == "alerts" {
		return runAlertsClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdout, nil)
	}
	if args[0] == "console-grants" {
		return runNativeClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdout, nil)
	}
	dsn := os.Getenv("CLOUD_MANAGER_DATABASE_URL")
	if dsn == "" {
		return errors.New("CLOUD_MANAGER_DATABASE_URL is required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return errors.New("invalid PostgreSQL configuration")
	}
	db := stdlib.OpenDB(*cfg)
	defer func() {
		if err := db.Close(); err != nil {
			retErr = errors.Join(retErr, errors.New("closing storage connection failed"))
		}
	}()
	if strings.HasPrefix(args[0], "event-") {
		return runEventCommand(parent, db, args, os.Getenv("CLOUD_MANAGER_EVENT_PROFILES"), os.Stdin, os.Stdout)
	}
	if args[0] == "verify-redfish" || args[0] == "scan-redfish" {
		return runRedfish(parent, db, args, os.Getenv("CLOUD_MANAGER_REDFISH_PROFILE"), nil)
	}
	if args[0] == "verify-vsphere" || args[0] == "scan-vsphere" {
		return runVSphere(parent, db, args, os.Getenv("CLOUD_MANAGER_VSPHERE_PROFILE"), nil)
	}
	if args[0] == "verify-openstack" || args[0] == "scan-openstack" {
		return runOpenStack(parent, db, args, os.Getenv("CLOUD_MANAGER_OPENSTACK_PROFILE"), nil)
	}
	if args[0] == "verify-aws" || args[0] == "scan-aws" {
		return runAWS(parent, db, args, os.Getenv("CLOUD_MANAGER_AWS_PROFILE"))
	}
	if name, variable := publicCloudCommand(args[0]); name != "" {
		return runPublicCloud(parent, db, args, name, os.Getenv(variable), nil)
	}
	if args[0] == "worker" {
		return runWorker(parent, db, os.Getenv("CLOUD_MANAGER_WORKER_PROFILES"), os.Getenv("CLOUD_MANAGER_WORKER_MODE"))
	}
	if args[0] == "controller" || args[0] == "workload-controller" {
		watchOnly := args[0] == "workload-controller"
		if watchOnly {
			db.SetMaxOpenConns(8)
			db.SetMaxIdleConns(4)
		}
		var dispatchLog io.Writer
		if !watchOnly && os.Getenv("CLOUD_MANAGER_CONTROLLER_DISPATCH_LOG") == "1" {
			dispatchLog = os.Stdout
		}
		return runController(parent, db, os.Getenv("CLOUD_MANAGER_CONTROLLER_PROFILES"), watchOnly, dispatchLog)
	}
	if strings.HasPrefix(args[0], "billing-pull") {
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(2)
		return runBillingPull(parent, db, args, os.Getenv(billingPullProfiles), os.Stdout, nil)
	}
	if args[0] == "idle-controller" || args[0] == "idle-schedule" {
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(2)
		return runIdleCommand(parent, db, args, os.Getenv("CLOUD_MANAGER_IDLE_PROFILES"), nil)
	}
	if args[0] == "kvm-evacuation-controller" {
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
		if err := kvm.NewStore(db).RunEvacuationController(parent); err != nil {
			return errors.New("KVM evacuation controller storage unavailable")
		}
		return nil
	}
	if args[0] == "alert-controller" {
		if err := alerts.NewStore(db).Run(parent); err != nil {
			return errors.New("project alert evaluation unavailable")
		}
		return nil
	}
	if args[0] == "api" {
		addr := os.Getenv("CLOUD_MANAGER_LISTEN_ADDR")
		console, err := loadConsoleConfig(os.Getenv)
		if err != nil {
			return err
		}
		if err := configureNativeConsole(console, os.Getenv("CLOUD_MANAGER_NATIVE_CONSOLE_PROFILES"), parent.Done()); err != nil {
			return err
		}
		app, err := newAPI(db, addr, console)
		if err != nil {
			return err
		}
		if console != nil {
			if err := console.configureTLS(app); err != nil {
				return err
			}
		}
		return app.RunContext(parent, 10*time.Second)
	}
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	if strings.HasPrefix(args[0], "billing-") {
		return runBillingStorageCommand(ctx, db, args, os.Stdout)
	}
	if args[0] == "sessions-purge" {
		removed, err := identity.NewBrowserSessions(db).PurgeExpired(ctx)
		if err != nil {
			return errors.New("browser session cleanup failed")
		}
		_, err = fmt.Fprintln(os.Stdout, removed)
		return err
	}
	return migrateCloudManager(ctx, db)
}

func migrateCloudManager(ctx context.Context, db *sql.DB) error {
	if err := identity.Migrate(ctx, db); err != nil {
		return errors.New("identity migration failed")
	}
	if err := inventory.Migrate(ctx, db); err != nil {
		return errors.New("inventory migration failed")
	}
	if err := identity.MigrateServiceTokens(ctx, db); err != nil {
		return errors.New("service token migration failed")
	}
	if err := migrateController(ctx, db); err != nil {
		return err
	}
	if err := identity.MigrateAccounts(ctx, db); err != nil {
		return errors.New("account events migration failed")
	}
	if err := operations.Migrate(ctx, db); err != nil {
		return errors.New("instance operation migration failed")
	}
	if err := identity.MigrateBrowserSessions(ctx, db); err != nil {
		return errors.New("browser session migration failed")
	}
	if err := audit.Migrate(ctx, db); err != nil {
		return errors.New("project audit migration failed")
	}
	if err := alerts.Migrate(ctx, db); err != nil {
		return errors.New("project alert migration failed")
	}
	if err := nativeconsole.Migrate(ctx, db); err != nil {
		return errors.New("native console migration failed")
	}
	if err := billing.Migrate(ctx, db); err != nil {
		return errors.New("billing ledger migration failed")
	}
	if err := billing.MigrateBudgets(ctx, db); err != nil {
		return errors.New("billing budget migration failed")
	}
	if err := billing.MigrateAllocations(ctx, db); err != nil {
		return errors.New("billing allocation migration failed")
	}
	if err := billing.MigratePulls(ctx, db); err != nil {
		return errors.New("billing pull migration failed")
	}
	if err := inventory.MigrateTagPolicies(ctx, db); err != nil {
		return errors.New("tag policy migration failed")
	}
	if err := inventory.MigrateIdleAnalysis(ctx, db); err != nil {
		return errors.New("instance idle analysis migration failed")
	}
	if err := operations.MigrateChangeTicketEvents(ctx, db); err != nil {
		return errors.New("change ticket events migration failed")
	}
	if err := operations.MigrateDatabaseRenameEvents(ctx, db); err != nil {
		return errors.New("database rename events migration failed")
	}
	if err := operations.MigrateCacheRenameEvents(ctx, db); err != nil {
		return errors.New("cache rename events migration failed")
	}
	if err := operations.MigrateBareMetalPower(ctx, db); err != nil {
		return errors.New("bare-metal power migration failed")
	}
	if err := identity.MigrateAccountWriteMode(ctx, db); err != nil {
		return errors.New("account write mode migration failed")
	}
	if err := operations.MigrateBareMetalPXE(ctx, db); err != nil {
		return errors.New("bare-metal PXE migration failed")
	}
	if err := operations.MigrateBareMetalDeployments(ctx, db); err != nil {
		return errors.New("bare-metal deployment migration failed")
	}
	if err := operations.MigrateBareMetalReclaims(ctx, db); err != nil {
		return errors.New("bare-metal reclaim migration failed")
	}
	if err := operations.MigrateBareMetalReclaimRecoveries(ctx, db); err != nil {
		return errors.New("bare-metal reclaim recovery migration failed")
	}
	if err := kvm.Migrate(ctx, db); err != nil {
		return errors.New("KVM placement migration failed")
	}
	return nil
}

func validCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "cmdb-migrate":
		return len(args) == 1
	case "cmdb-sync", "cmdb-status":
		return len(args) == 3
	case "migrate", "api", "controller", "workload-controller", "kvm-evacuation-controller", "alert-controller", "idle-controller", "billing-pull-controller", "worker", "event-publisher", "sessions-purge":
		return len(args) == 1
	case "event-register", "event-disable", "event-failures", "event-reconcile":
		return len(args) == 2
	case "verify-redfish", "verify-vsphere", "verify-openstack", "verify-aws", "verify-tencent", "verify-alibaba", "verify-huawei", "event-retry":
		return len(args) == 3
	case "scan-openstack", "scan-aws", "scan-vsphere", "scan-redfish":
		return len(args) == 5
	case "scan-tencent", "scan-alibaba", "scan-huawei":
		return len(args) == 5 || (len(args) == 6 && publicCloudMissingKind(provider.Kind(args[4])) && args[5] != "")
	case "refresh-tencent-load-balancer", "refresh-alibaba-load-balancer", "refresh-huawei-load-balancer":
		return (len(args) == 5 || (len(args) == 6 && args[5] == "--include-backends")) && args[4] != ""
	case "refresh-tencent-cluster-node-pools", "refresh-alibaba-cluster-node-pools", "refresh-huawei-cluster-node-pools",
		"refresh-tencent-cluster-workloads", "refresh-alibaba-cluster-workloads", "refresh-huawei-cluster-workloads":
		return len(args) == 5 && args[4] != ""
	case "refresh-tencent-dns-zone", "refresh-alibaba-dns-zone", "refresh-huawei-dns-zone",
		"refresh-tencent-bucket-versioning", "refresh-alibaba-bucket-versioning", "refresh-huawei-bucket-versioning",
		"refresh-tencent-bucket-encryption", "refresh-alibaba-bucket-encryption", "refresh-huawei-bucket-encryption":
		return len(args) == 4 && args[3] != ""
	case "idle-schedule":
		return len(args) == 6
	case "billing-import":
		return len(args) >= 9 && len(args) <= 8+billing.MaxParts
	case "billing-replay":
		return len(args) == 7
	case "billing-source", "billing-allocation-replay":
		return len(args) == 8
	case "billing-pull", "billing-pull-status":
		return len(args) == 6
	case "billing-pull-replay":
		return len(args) == 7
	case "billing-pull-source":
		return len(args) == 8
	case "billing-allocate":
		return len(args) == 9
	case "kvm", "operations", "audit", "catalog", "alerts", "console-grants", "billing", "governance", "cloud-accounts":
		return len(args) >= 2
	default:
		return false
	}
}
