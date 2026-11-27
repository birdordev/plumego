package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/delivery"
)

func TestAcceptanceEventReconciliation(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	cloud := seedPITRPower(t, db)
	app := pitrAPI(t, db)
	pitrExec(t, db, "INSERT INTO cmp_projects VALUES ('tenant','elsewhere')")
	cfg := func(id, project string) delivery.Config {
		return delivery.Config{ID: id, Tenant: "tenant", Project: project, Endpoint: "https://receiver.invalid/events",
			Secret: strings.Repeat("x", 32)}
	}
	profile := filepath.Join(t.TempDir(), "events.json")
	config, err := json.Marshal([]delivery.Config{cfg("primary", "project"), cfg("later", "project"), cfg("elsewhere", "elsewhere")})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, config, 0600); err != nil {
		t.Fatal(err)
	}
	register := func(sink string) {
		t.Helper()
		if err := runEventCommand(ctx, db, []string{"event-register", sink}, profile, nil, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	register("primary")
	register("elsewhere")
	terminal := submitPITRPower(t, app, cloud, "start", "terminal")
	pitrExec(t, db, "UPDATE cmp_instance_operations SET status='succeeded' WHERE operation_id=$1", terminal.ID)
	pitrExec(t, db, "INSERT INTO cmp_instance_operation_events(operation_id,status,recorded_at) VALUES ($1,'succeeded',now()-interval '1 hour')", terminal.ID)
	register("later")

	queued := submitFixturePower(t, app, cloud.provider, cloud.region, pitrParkedInstance,
		pitrServiceToken, "start", "queued", http.StatusAccepted)
	var receipt delivery.TerminalReceipt
	var id string
	var body []byte
	if err := db.QueryRowContext(ctx, "SELECT public_id::text,body FROM cmp_operation_event_outbox WHERE sink_id='primary'").
		Scan(&id, &body); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &receipt); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	receipt.DeliveryID, receipt.BodySHA256 = id, hex.EncodeToString(hash[:])
	receiptFor := func(deliveryID, operation, asset string) delivery.TerminalReceipt {
		value := receipt
		value.DeliveryID, value.OperationID, value.Asset = deliveryID, operation, asset
		value.EffectID = "cmp-op-final-v1:" + operation
		return value
	}
	runManifest := func(sink string, manifest delivery.ReceiptManifest, wantErr error, wantCounts map[string]int) delivery.Reconciliation {
		t.Helper()
		payload, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		err = runEventCommand(ctx, db, []string{"event-reconcile", sink}, profile, bytes.NewReader(payload), &output)
		if !errors.Is(err, wantErr) {
			t.Fatalf("reconcile error=%v, want %v", err, wantErr)
		}
		var report delivery.Reconciliation
		if wantCounts == nil {
			if output.Len() != 0 {
				t.Fatal("invalid input returned local data")
			}
			return report
		}
		if err := json.Unmarshal(output.Bytes(), &report); err != nil || !reflect.DeepEqual(report.Counts, wantCounts) ||
			report.Sink != sink || report.Project != manifest.Project || report.Tenant != "tenant" ||
			report.Clean != (wantErr == nil) || report.CheckedAt.IsZero() ||
			len(report.Items) != len(manifest.Receipts)+wantCounts["receiver_missing"] {
			t.Fatalf("unexpected reconciliation report %+v: %v", report, err)
		}
		for i, receipt := range manifest.Receipts {
			if report.Items[i].DeliveryID != receipt.DeliveryID || report.Items[i].EffectID != receipt.EffectID {
				t.Fatalf("receipt order changed at entry %d", i)
			}
		}
		return report
	}
	run := func(sink, project string, input []delivery.TerminalReceipt, wantErr error, wantCounts map[string]int) delivery.Reconciliation {
		t.Helper()
		return runManifest(sink, delivery.ReceiptManifest{Sink: sink, Tenant: "tenant", Project: project, Receipts: input}, wantErr, wantCounts)
	}
	regenerated := receiptFor("12345678-1234-4321-8765-111111111111", terminal.ID, cloud.instance)
	unsettled := receiptFor("12345678-1234-4321-8765-222222222222", queued.ID, pitrParkedInstance)
	lost := receiptFor("12345678-1234-4321-8765-333333333333", "lost-after-restore", cloud.instance)
	report := run("primary", "project", []delivery.TerminalReceipt{regenerated, receipt, unsettled, lost}, errEventReconciliationIncomplete,
		map[string]int{"regenerated": 1, "matched": 1, "source_unsettled": 1, "source_missing": 1})
	if report.Items[0].SourceDelivery != id || report.Items[2].SourceStatus != "queued" || report.Items[3].SourceStatus != "" {
		t.Fatalf("unexpected recovery evidence: %+v", report.Items)
	}
	run("primary", "project", []delivery.TerminalReceipt{receipt, regenerated}, nil, map[string]int{"matched": 1, "regenerated": 1})
	run("later", "project", []delivery.TerminalReceipt{receipt}, errEventReconciliationIncomplete, map[string]int{"outbox_missing": 1})
	run("elsewhere", "elsewhere", []delivery.TerminalReceipt{receipt}, errEventReconciliationIncomplete, map[string]int{"source_missing": 1})
	run("elsewhere", "project", []delivery.TerminalReceipt{receipt}, delivery.ErrBinding, nil)
	run("primary", "project", []delivery.TerminalReceipt{receipt, receipt}, delivery.ErrReceiptManifest, nil)
	wrongDigest := receipt
	wrongDigest.BodySHA256 = strings.Repeat("f", 64)
	run("primary", "project", []delivery.TerminalReceipt{wrongDigest}, errEventReconciliationIncomplete, map[string]int{"conflict": 1})
	wrongEffect := receipt
	wrongEffect.EffectID = lost.EffectID
	run("primary", "project", []delivery.TerminalReceipt{wrongEffect}, delivery.ErrReceiptManifest, nil)
	wrongAsset := regenerated
	wrongAsset.Asset = pitrParkedInstance
	run("primary", "project", []delivery.TerminalReceipt{wrongAsset}, errEventReconciliationIncomplete, map[string]int{"conflict": 1})

	var intact bool
	if err := db.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM cmp_operation_event_outbox
		WHERE attempts<>0 OR lease_until IS NOT NULL OR delivered_at IS NOT NULL OR failed_at IS NOT NULL)
		AND EXISTS (SELECT 1 FROM cmp_instance_operations WHERE operation_id=$1 AND status='queued')`, queued.ID).
		Scan(&intact); err != nil || !intact {
		t.Fatalf("reconciliation modified queue or delivery state: %v", err)
	}
	verifyReceiptCoverage(t, db, receipt, runManifest)
}

func verifyReceiptCoverage(t *testing.T, db *sql.DB, receipt delivery.TerminalReceipt,
	run func(string, delivery.ReceiptManifest, error, map[string]int) delivery.Reconciliation,
) {
	t.Helper()
	if receipt.OccurredAt == nil {
		t.Fatal("missing signed receipt occurrence time")
	}
	from := receipt.OccurredAt.UTC()
	until := from.Add(time.Minute)
	pitrExec(t, db, `INSERT INTO cmp_instance_operations(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,
			account_id,region,action,status,idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
		SELECT label,o.tenant_id,o.project_id,o.principal_id,o.scope_id,o.external_id,o.account_id,o.region,o.action,
			'succeeded',label,o.request_hash,o.credential_revision,o.membership_revision,o.ownership_revision
		FROM cmp_instance_operations o CROSS JOIN (VALUES ('before'),('same-time'),('only-source-pending'),
			('only-source-failed'),('only-source-leased'),('at-until')) AS fixtures(label) WHERE o.operation_id=$1`, receipt.OperationID)
	pitrExec(t, db, `INSERT INTO cmp_instance_operation_events(operation_id,status,recorded_at)
	SELECT operation_id,'succeeded',$1::timestamptz+CASE operation_id
		WHEN 'before' THEN interval '-1 microsecond'
		WHEN 'same-time' THEN interval '0 seconds'
		WHEN 'only-source-pending' THEN interval '1 second'
		WHEN 'only-source-failed' THEN interval '2 seconds'
		WHEN 'only-source-leased' THEN interval '3 seconds'
		ELSE interval '1 minute' END FROM cmp_instance_operations
	WHERE operation_id IN ('before','same-time','only-source-pending','only-source-failed','only-source-leased','at-until')`, from)
	pitrExec(t, db, `UPDATE cmp_operation_event_outbox box SET
		delivered_at=CASE WHEN e.operation_id='same-time' THEN now() END,
		failed_at=CASE WHEN e.operation_id='only-source-failed' THEN now() END,
		lease_until=CASE WHEN e.operation_id='only-source-leased' THEN now()+interval '2 minutes' END,
		lease_token=CASE WHEN e.operation_id='only-source-leased' THEN 'fixture-lease' END
		FROM cmp_instance_operation_events e WHERE e.event_id=box.sequence`)

	manifest := delivery.ReceiptManifest{Sink: "primary", Tenant: "tenant", Project: "project",
		Coverage: &delivery.ReceiptCoverage{From: from, Until: until}, Receipts: []delivery.TerminalReceipt{receipt}}
	report := run("primary", manifest, errEventReconciliationIncomplete, map[string]int{"matched": 1, "receiver_missing": 4})
	if report.Coverage == nil || !report.Coverage.From.Equal(from) || !report.Coverage.Until.Equal(until) {
		t.Fatalf("coverage boundaries changed: %+v", report.Coverage)
	}
	expected := []string{"delivered", "pending", "failed", "leased"}
	for i, item := range report.Items[1:] {
		if item.State != "receiver_missing" || item.SourceDelivery == "" || item.DeliveryID != "" ||
			item.SourceDeliveryState != expected[i] || item.SourceOccurredAt == nil {
			t.Fatalf("missing receipt has wrong evidence: %+v", item)
		}
	}
	movedReceipt := receipt
	moved := from.Add(time.Microsecond)
	movedReceipt.OccurredAt = &moved
	altered := manifest
	altered.Receipts = []delivery.TerminalReceipt{movedReceipt}
	run("primary", altered, errEventReconciliationIncomplete, map[string]int{"conflict": 1, "receiver_missing": 4})
	replacedReceipt := receipt
	replacedReceipt.DeliveryID = "12345678-1234-4321-8765-555555555555"
	altered.Receipts = []delivery.TerminalReceipt{replacedReceipt}
	run("primary", altered, errEventReconciliationIncomplete, map[string]int{"regenerated": 1, "receiver_missing": 4})
	empty := manifest
	empty.Sink, empty.Project, empty.Receipts = "elsewhere", "elsewhere", []delivery.TerminalReceipt{}
	run("elsewhere", empty, nil, map[string]int{})
	empty.Sink, empty.Project = manifest.Sink, manifest.Project
	run("primary", empty, errEventReconciliationIncomplete, map[string]int{"receiver_missing": 5})
	empty.Receipts = nil
	run("primary", empty, delivery.ErrReceiptManifest, nil)
	bad := manifest
	bad.Coverage = &delivery.ReceiptCoverage{From: from.Add(time.Microsecond), Until: until}
	run("primary", bad, delivery.ErrReceiptManifest, nil)
	bad.Coverage = &delivery.ReceiptCoverage{From: from, Until: time.Now().Add(time.Hour)}
	run("primary", bad, delivery.ErrReceiptManifest, nil)

	pitrExec(t, db, `INSERT INTO cmp_instance_operations(operation_id,tenant_id,project_id,principal_id,scope_id,external_id,
		account_id,region,action,status,idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
		SELECT 'bulk-'||n,tenant_id,project_id,principal_id,scope_id,external_id,account_id,region,action,
			'succeeded','bulk-'||n,request_hash,credential_revision,membership_revision,ownership_revision
		FROM cmp_instance_operations CROSS JOIN generate_series(1,$2::int) AS n WHERE operation_id=$1`,
		receipt.OperationID, delivery.MaxReconcileReceipts+1)
	bulkTime := until.Add(time.Minute)
	pitrExec(t, db, `INSERT INTO cmp_instance_operation_events(operation_id,status,recorded_at)
		SELECT operation_id,'succeeded',$1 FROM cmp_instance_operations WHERE operation_id LIKE 'bulk-%'`, bulkTime)
	empty.Receipts = []delivery.TerminalReceipt{}
	empty.Coverage = &delivery.ReceiptCoverage{From: bulkTime, Until: bulkTime.Add(time.Second)}
	run("primary", empty, delivery.ErrReconcileWindowTooLarge, nil)
}
