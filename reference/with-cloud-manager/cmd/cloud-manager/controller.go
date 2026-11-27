package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"time"

	plumelog "github.com/birdordev/plumego/log"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/awsec2"
	"with-cloud-manager/internal/provider/openstack"
	"with-cloud-manager/internal/provider/publiccloud"
)

func runController(ctx context.Context, db *sql.DB, path string, watchOnly bool, dispatchLog io.Writer) error {
	var profiles map[string]string
	if err := loadPrivateProfile(path, &profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return errors.New("controller needs a credential reference map")
	}
	store := controller.NewStore(db)
	if !watchOnly && dispatchLog != nil {
		store = store.WithDispatchObserver(inventoryDispatchLogger(dispatchLog))
	}
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

func inventoryDispatchLogger(output io.Writer) func(controller.DispatchObservation) {
	logger := plumelog.NewLogger(plumelog.LoggerConfig{Format: plumelog.LoggerFormatJSON, Output: output})
	return func(event controller.DispatchObservation) {
		logger.Info("inventory_dispatch_claimed", plumelog.Fields{
			"schema_version": 1, "tenant": event.Tenant, "account": event.Account,
			"provider": event.Provider, "kind": event.Kind, "schedule_id": event.ScheduleID, "fence": event.Fence,
			"due_at": event.DueAt.Format(time.RFC3339Nano), "claimed_at": event.ClaimedAt.Format(time.RFC3339Nano),
			"due_wait_ms": max(0, event.ClaimedAt.Sub(event.DueAt).Milliseconds()),
		})
	}
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
		case controller.BucketEncryptionScheduleKind, controller.BucketVersioningScheduleKind:
			return prepareScheduledBucketSetting(ctx, db, task, path, transport)
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
		case provider.Redfish:
			return nil, runRedfish(ctx, db, args, path, transport)
		case provider.VSphere:
			return nil, runVSphere(ctx, db, args, path, transport)
		case openstack.Name:
			return nil, runOpenStack(ctx, db, args, path, nil)
		case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
			return nil, scanScheduledPublicCloud(ctx, db, task, path, transport)
		default:
			return nil, errors.New("controller provider unavailable")
		}
	}
}

func scanScheduledPublicCloud(ctx context.Context, db *sql.DB, task controller.Task, path string, transport http.RoundTripper) error {
	var profile publicCloudProfile
	if err := loadPrivateProfile(path, &profile); err != nil {
		return err
	}
	binding, err := identity.NewStore(db).CloudAccountBinding(ctx, task.Key.Tenant, task.Key.Account)
	if err != nil || binding.Provider != task.Provider || binding.CredentialRef != profile.Ref ||
		binding.Revision != task.CredentialRevision || binding.RemoteIdentity == "" {
		return inventory.ErrStale
	}
	client, err := publiccloud.Connect(ctx, task.Provider, profile.Config, transport)
	if err != nil {
		return err
	}
	if client.Identity() != binding.RemoteIdentity {
		return inventory.ErrStale
	}
	store := inventory.NewStore(db)
	claim, err := store.ClaimScheduled(ctx, task.Key, 10*time.Minute, binding.Revision, task.Lease)
	if err != nil {
		return err
	}
	return scanInventoryPages(ctx, store, claim, client)
}

func migrateController(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := controller.Migrate(ctx, db); err != nil {
		return errors.New("inventory schedule migration failed")
	}
	return nil
}
