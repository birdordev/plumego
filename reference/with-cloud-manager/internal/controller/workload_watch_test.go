package controller

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

func TestAcceptanceWorkloadWatchIsolatesControllerPools(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	for _, migrate := range []func(context.Context, *sql.DB) error{identity.Migrate, inventory.Migrate, Migrate} {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant','account','project','tencent-cvm','profiles/reader','fixture-remote','read_only');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,target_id,interval_seconds) VALUES
			('tenant','account','ap-guangzhou','kubernetes-workload-watch','unreachable',60),
			('tenant','account','ap-guangzhou','kubernetes-workload-watch','online',60),
			('tenant','account','ap-guangzhou','instance','',60)`)
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	normal, err := store.claim(ctx, time.Minute, inventoryPool)
	if err != nil || normal.task.ScheduleKind != "instance" {
		t.Fatalf("ordinary inventory pool claimed a watch: %+v err=%v", normal.task, err)
	}
	if err := store.release(ctx, normal); err != nil {
		t.Fatal(err)
	}

	watchCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	done := make(chan error, 1)
	unreachable := make(chan struct{})
	go func() {
		done <- store.RunWorkloadWatch(watchCtx, func(ctx context.Context, task Task) (Publish, error) {
			switch task.TargetID {
			case "unreachable":
				close(unreachable)
				<-ctx.Done()
				return nil, ctx.Err()
			case "online":
				select {
				case <-unreachable:
					return nil, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			default:
				t.Error("workload pool claimed ordinary inventory")
				return nil, nil
			}
		})
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
		var leased bool
		err := db.QueryRowContext(ctx, "SELECT lease_until IS NOT NULL FROM cmp_inventory_schedules WHERE target_id='unreachable'").Scan(&leased)
		if err != nil || leased {
			t.Fatalf("watch shutdown failed to yield lease: %t %v", leased, err)
		}
	}()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var healthy bool
		err := db.QueryRowContext(watchCtx, `SELECT last_success_at IS NOT NULL FROM cmp_inventory_schedules WHERE target_id='online'`).Scan(&healthy)
		if err != nil {
			t.Fatal(err)
		}
		if healthy {
			break
		}
		select {
		case <-watchCtx.Done():
			t.Fatal("unreachable site blocked the live site's successful round")
		case <-ticker.C:
		}
	}
}
