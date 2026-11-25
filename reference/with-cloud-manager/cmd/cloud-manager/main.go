package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/operations"
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
		return errors.New("usage: cloud-manager migrate|api|controller|alert-controller|worker|sessions-purge|operations COMMAND TENANT PROJECT|audit {list,export} TENANT PROJECT|catalog COMMAND TENANT PROJECT|alerts COMMAND TENANT PROJECT|console-grants COMMAND TENANT PROJECT|event-publisher|event-{register,disable,failures} SINK|event-retry SINK EVENT_ID|verify-{openstack,aws,tencent,alibaba,huawei} TENANT ACCOUNT|scan-{openstack,aws,tencent,alibaba,huawei} TENANT ACCOUNT REGION KIND")
	}
	if args[0] == "operations" {
		return runOperationsClient(parent, args[1:], os.Getenv("CLOUD_MANAGER_API_URL"), os.Getenv("CLOUD_MANAGER_API_TOKEN"), os.Stdin, os.Stdout, nil)
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
		return runEventCommand(parent, db, args, os.Getenv("CLOUD_MANAGER_EVENT_PROFILES"), os.Stdout)
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
		return runWorker(parent, db, os.Getenv("CLOUD_MANAGER_WORKER_PROFILES"))
	}
	if args[0] == "controller" {
		return runController(parent, db, os.Getenv("CLOUD_MANAGER_CONTROLLER_PROFILES"))
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
	return nil
}

func validCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "migrate", "api", "controller", "alert-controller", "worker", "event-publisher", "sessions-purge":
		return len(args) == 1
	case "event-register", "event-disable", "event-failures":
		return len(args) == 2
	case "verify-openstack", "verify-aws", "verify-tencent", "verify-alibaba", "verify-huawei", "event-retry":
		return len(args) == 3
	case "scan-openstack", "scan-aws", "scan-tencent", "scan-alibaba", "scan-huawei":
		return len(args) == 5
	case "operations", "audit", "catalog", "alerts", "console-grants":
		return len(args) >= 2
	default:
		return false
	}
}
