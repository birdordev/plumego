package cloudtest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceInventoryDispatchReport(t *testing.T) {
	db, handler := prepareFreshnessReport(t)
	seedInventoryDispatchReport(t, db)
	startFreshnessScan(t, inventory.NewStore(db), inventory.Key{
		Tenant: "lab", Account: "hw", Region: "cn-test", Kind: provider.Instance,
	})

	report := getFreshnessReport(t, handler, freshnessPath)
	want := inventory.FreshnessDispatchSummary{Scheduled: 1, Ready: 3, AccountBusy: 1, Cooldown: 1, Leased: 1}
	if report.Summary.Total != 7 || len(report.Items) != 7 {
		t.Fatalf("unexpected dispatch scope: %+v", report)
	}
	want.LongestReadyOverdue = int64(report.AsOf.Sub(report.Items[5].NextAt).Seconds())
	if want.LongestReadyOverdue < 1200 || report.DispatchSummary != want {
		t.Fatalf("unexpected dispatch snapshot: got %+v, want %+v", report.DispatchSummary, want)
	}
	for i, state := range []string{"leased", "cooldown", "ready", "account_busy", "scheduled", "ready", "ready"} {
		if item := report.Items[i]; item.DispatchState != state {
			t.Fatalf("scope %s/%s: got %s, want %s", item.Account, item.Kind, item.DispatchState, state)
		}
	}
	leased, cooling, scanning, busy := report.Items[0], report.Items[1], report.Items[2], report.Items[3]
	if leased.ScheduleLeaseUntil == nil || leased.AccountLeaseUntil == nil ||
		!leased.ScheduleLeaseUntil.Equal(*leased.AccountLeaseUntil) || leased.AccountCooldownUntil != nil ||
		cooling.AccountCooldownUntil == nil || !cooling.AccountCooldownUntil.After(cooling.NextAt) ||
		cooling.ScheduleLeaseUntil != nil || busy.AccountLeaseUntil == nil || busy.ScheduleLeaseUntil != nil ||
		!scanning.Running || scanning.ScheduleLeaseUntil != nil {
		t.Fatalf("incorrect dispatch deadlines or scan state: %+v", report.Items)
	}
	sharedPath := strings.Replace(freshnessPath, "/compute/", "/shared/", 1)
	shared := getFreshnessReport(t, handler, sharedPath)
	if shared.Summary.Total != 3 || shared.DispatchSummary != (inventory.FreshnessDispatchSummary{Leased: 1, AccountBusy: 1, Scheduled: 1}) {
		t.Fatalf("shared account dispatch scope differs: %+v", shared)
	}
	checkDispatchReportPaging(t, handler, report)
	checkDispatchReportCLI(t, handler)

	execFreshness(t, db, `UPDATE cmp_inventory_account_dispatch SET cooldown_until=now()-interval '1 second'
		WHERE (tenant_id,account_id)=('lab','ali')`)
	afterCooldown := getFreshnessReport(t, handler, freshnessPath+"?account=ali")
	if len(afterCooldown.Items) != 1 || afterCooldown.Items[0].AccountCooldownUntil != nil ||
		afterCooldown.DispatchSummary != (inventory.FreshnessDispatchSummary{Scheduled: 1}) {
		t.Fatalf("expired cooldown is still reported: %+v", afterCooldown)
	}
}

func seedInventoryDispatchReport(t *testing.T, db *sql.DB) {
	t.Helper()
	execFreshness(t, db, `UPDATE cmp_inventory_schedules SET
			lease_until=now()+interval '10 minutes',fence=1,next_at=now()-interval '3 hours'
		WHERE (tenant_id,account_id,region,kind)=('lab','tc','ap-test','instance');
		UPDATE cmp_inventory_schedules SET next_at=now()+interval '2 minutes' WHERE (tenant_id,account_id)=('lab','ali');
		UPDATE cmp_inventory_schedules SET next_at=now()-interval '5 minutes'
		WHERE (tenant_id,account_id,kind)=('lab','hw','instance');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds,next_at) VALUES
			('lab','tc','ap-test','volume',60,now()-interval '2 hours'),
			('lab','tc','ap-test','network',60,now()+interval '1 hour'),
			('lab','hw','cn-test','network',60,now()-interval '20 minutes'),
			('lab','hw','cn-test','volume',60,now()-interval '20 minutes');
		INSERT INTO cmp_inventory_account_dispatch (tenant_id,account_id,last_claimed_at,schedule_id,schedule_fence,lease_until)
			SELECT tenant_id,account_id,now(),schedule_id,fence,lease_until FROM cmp_inventory_schedules
			WHERE (tenant_id,account_id,region,kind)=('lab','tc','ap-test','instance');
		INSERT INTO cmp_inventory_account_dispatch (tenant_id,account_id,last_claimed_at,cooldown_until) VALUES
			('lab','ali',now(),now()+interval '5 minutes'),('foreign','tc',now(),now()+interval '15 minutes');`)
}

func checkDispatchReportPaging(t *testing.T, handler http.Handler, report inventory.FreshnessReport) {
	t.Helper()
	path := freshnessPath + "?dispatch_state=ready&state=pending&order=next_at&limit=1"
	cursor := ""
	for i, kind := range []string{"network", "volume", "instance"} {
		target := path
		if cursor != "" {
			target += "&after=" + url.QueryEscape(cursor)
		}
		page := getFreshnessReport(t, handler, target)
		if page.Summary != report.Summary || page.DispatchSummary.Ready != 3 || len(page.Items) != 1 ||
			page.Items[0].Kind != kind || page.Items[0].Account != "hw" ||
			(page.Next == "") != (i == 2) || (page.Next != "" && page.Next == cursor) {
			t.Fatalf("unexpected ordered dispatch page %d: %+v", i+1, page)
		}
		cursor = page.Next
	}
	for _, query := range []string{
		"?dispatch_state=unknown", "?dispatch_state=ready&dispatch_state=leased", "?order=unknown", "?order=next_at&after=1",
		"?after=2026-11-27T07:20:30Z%2F1", "?order=next_at&after=2026-11-27T07:20:30.0000001Z%2F1",
	} {
		requireFreshnessStatus(t, freshnessRequest(t, handler, freshnessPath+query, freshnessToken), http.StatusBadRequest)
	}
}

func checkDispatchReportCLI(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	var out bytes.Buffer
	err := commands.RunGovernanceClient(t.Context(), []string{"inventory-freshness", "lab", "compute",
		"--dispatch-state", "ready", "--order", "next_at", "--limit", "1"}, server.URL, freshnessToken, nil, &out, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	var report inventory.FreshnessReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Items) != 1 ||
		report.Items[0].Kind != "network" || report.Items[0].DispatchState != "ready" || report.Next == "" {
		t.Fatalf("unexpected sorted CLI report: %+v, %v", report, err)
	}
}
