package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/awsec2"
	"with-cloud-manager/internal/provider/openstack"
)

func runController(ctx context.Context, db *sql.DB, path string, watchOnly bool) error {
	var profiles map[string]string
	if err := loadPrivateProfile(path, &profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return errors.New("controller needs a credential reference map")
	}
	store := controller.NewStore(db)
	execute := inventoryScheduleExecutor(db, profiles, nil)
	run := store.Run
	if watchOnly {
		run = store.RunWorkloadWatch
	}
	if err := run(ctx, execute); err != nil {
		return errors.New("inventory controller storage unavailable")
	}
	return nil
}

func inventoryScheduleExecutor(db *sql.DB, profiles map[string]string, transport http.RoundTripper) controller.Execute {
	return func(ctx context.Context, task controller.Task) (controller.Publish, error) {
		path := profiles[task.CredentialRef]
		if path == "" {
			return nil, errors.New("controller profile unavailable")
		}
		switch task.ScheduleKind {
		case controller.NodePoolScheduleKind:
			return prepareScheduledNodePools(ctx, db, task, path, transport)
		case controller.WorkloadScheduleKind, controller.WorkloadWatchScheduleKind:
			return prepareScheduledWorkloads(ctx, db, task, path)
		}
		key := task.Key
		region := key.Region
		if region == "" {
			region = "-"
		}
		args := []string{"scan", key.Tenant, key.Account, region, string(key.Kind)}
		switch task.Provider {
		case awsec2.Name:
			return nil, runAWS(ctx, db, args, path)
		case provider.VSphere:
			return nil, runVSphere(ctx, db, args, path, transport)
		case openstack.Name:
			return nil, runOpenStack(ctx, db, args, path, nil)
		case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
			return nil, runPublicCloud(ctx, db, args, task.Provider, path, transport)
		default:
			return nil, errors.New("controller provider unavailable")
		}
	}
}

func migrateController(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := controller.Migrate(ctx, db); err != nil {
		return errors.New("inventory schedule migration failed")
	}
	return nil
}
