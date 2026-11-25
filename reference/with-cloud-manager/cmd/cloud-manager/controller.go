package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/awsec2"
	"with-cloud-manager/internal/provider/openstack"
)

func runController(ctx context.Context, db *sql.DB, path string) error {
	var profiles map[string]string
	if err := loadPrivateProfile(path, &profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return errors.New("controller needs a credential reference map")
	}
	store := controller.NewStore(db)
	err := store.Run(ctx, func(ctx context.Context, key inventory.Key, driver, reference string) error {
		path := profiles[reference]
		if path == "" {
			return errors.New("controller profile unavailable")
		}
		region := key.Region
		if region == "" {
			region = "-"
		}
		args := []string{"scan", key.Tenant, key.Account, region, string(key.Kind)}
		switch driver {
		case awsec2.Name:
			return runAWS(ctx, db, args, path)
		case openstack.Name:
			return runOpenStack(ctx, db, args, path, nil)
		case provider.TencentCVM, provider.AlibabaECS, provider.HuaweiECS:
			return runPublicCloud(ctx, db, args, driver, path, nil)
		default:
			return errors.New("controller provider unavailable")
		}
	})
	if err != nil {
		return errors.New("inventory controller storage unavailable")
	}
	return nil
}

func migrateController(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := controller.Migrate(ctx, db); err != nil {
		return errors.New("inventory schedule migration failed")
	}
	return nil
}
