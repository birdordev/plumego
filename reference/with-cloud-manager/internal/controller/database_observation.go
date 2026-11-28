package controller

import (
	"context"
	"database/sql"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func isDatabaseObservationSchedule(kind provider.Kind) bool {
	return kind == DatabaseBackupScheduleKind || kind == DatabaseRestoreWindowScheduleKind
}

func checkDatabaseScheduleTarget(ctx context.Context, tx *sql.Tx, actor identity.Scope, account, region, target string, kind provider.Kind) error {
	if !isDatabaseObservationSchedule(kind) {
		return nil
	}
	vendor, details, err := inventory.ResolveDatabaseObservationTarget(ctx, tx, inventory.DatabaseAssetQuery{
		Tenant: actor.Tenant, Project: actor.Project, Account: account, Region: region, ID: target,
	})
	if err != nil {
		return err
	}
	switch kind {
	case DatabaseBackupScheduleKind:
		if !provider.ValidDatabaseBackupTarget(vendor, target, details) {
			return identity.ErrAccountInvalid
		}
	case DatabaseRestoreWindowScheduleKind:
		if provider.DatabaseRestoreWindowScope(vendor, target, details) == "" {
			return identity.ErrAccountInvalid
		}
	}
	return nil
}
