package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceInventoryDispatchStatsReplicas(t *testing.T) {
	db, _ := prepareFreshnessReport(t)
	execFreshness(t, db, `UPDATE cmp_inventory_schedules SET next_at=now()+interval '1 hour';
		UPDATE cmp_inventory_schedules SET next_at=now()-interval '1 minute'
		WHERE enabled AND kind='instance' AND account_id<>'paused';`)
	start := postgresDispatchTime(t, db)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	var logs [2]bytes.Buffer
	results := make(chan error, len(logs))
	executed := make(chan struct{}, 4)
	releaseFirst := make(chan struct{})
	for i := range logs {
		workers.Go(func() {
			first := true
			store := controller.NewStore(db).WithDispatchObserver(inventoryDispatchLogger(&logs[i]))
			results <- store.Run(ctx, func(ctx context.Context, _ controller.Task) (controller.Publish, error) {
				select {
				case executed <- struct{}{}:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if first {
					first = false
					select {
					case <-releaseFirst:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return nil, nil
			})
		})
	}
	for i := range 4 {
		select {
		case <-ctx.Done():
			t.Fatalf("only %d schedules reached execution: %v", i, ctx.Err())
		case err := <-results:
			t.Fatalf("controller exited before evidence collection: %v", err)
		case <-executed:
			if i == 1 {
				close(releaseFirst)
			}
		}
	}
	end := postgresDispatchTime(t, db)
	cancel()
	workers.Wait()
	for range logs {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if logs[0].Len() == 0 || logs[1].Len() == 0 {
		t.Fatal("expected observations from each replica")
	}
	// 合并相反顺序的副本日志，并模拟采集端重复递送一份。
	input := bytes.Join([][]byte{logs[1].Bytes(), logs[0].Bytes(), logs[1].Bytes()}, nil)
	var output bytes.Buffer
	args := []string{"inventory-dispatch-stats", "--from", start.Format(time.RFC3339Nano), "--until", end.Format(time.RFC3339Nano),
		"--expect", "lab/tencent-cvm", "--expect", "lab/alibaba-ecs", "--expect", "lab/huawei-ecs", "--expect", "foreign/tencent-cvm"}
	if err := runGovernanceClient(t.Context(), args, "", "", bytes.NewReader(input), &output, nil); err != nil {
		t.Fatal(err)
	}
	var report dispatchStatsReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil || !report.ThresholdMet || report.Claims != 4 ||
		report.Duplicates != bytes.Count(logs[1].Bytes(), []byte{'\n'}) || report.Tenants["foreign"].Claims != 1 ||
		report.Tenants["lab"].Claims != 3 || report.Providers[provider.TencentCVM].Claims != 2 ||
		report.Providers[provider.AlibabaECS].Claims != 1 || report.Providers[provider.HuaweiECS].Claims != 1 ||
		len(report.Cohorts) != 4 || report.Tenants["foreign"].P95 < time.Minute.Milliseconds() {
		t.Fatalf("unexpected PostgreSQL dispatch report: %s, %v", &output, err)
	}
}

func postgresDispatchTime(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	var at time.Time
	if err := db.QueryRowContext(t.Context(), `SELECT clock_timestamp()`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at.UTC()
}
