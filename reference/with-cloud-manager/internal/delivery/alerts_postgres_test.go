package delivery

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/birdordev/plumego/x/messaging/webhook"

	"with-cloud-manager/internal/alerts"
)

func TestAcceptanceAlertMigrationWithOperationBacklog(t *testing.T) {
	db, ctx := openOperationDeliveryTestDB(t), t.Context()
	exec := func(statement string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','actor','service','fixture','actor');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','acct','project','aws-ec2','fixture/ref','aws:backlog','write_enabled');
		INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash)
		VALUES ('legacy','tenant','project',decode(repeat('00',32),'hex'));
		INSERT INTO cmp_instance_operations
		    (operation_id,tenant_id,project_id,principal_id,account_id,region,resource_kind,action,status,idempotency_key,request_hash,
		     credential_revision,membership_revision,ownership_revision)
		SELECT kind,'tenant','project','actor','acct','us-east-1',kind,action,'failed',kind,decode(repeat('00',32),'hex'),1,1,0
		FROM (VALUES ('instance','create'),('network','create-network'),('subnet','create-subnet'),('security-group','create-security-group')) v(kind,action);`)
	const record = `INSERT INTO cmp_instance_operation_events (operation_id,status)
	                SELECT operation_id,'failed' FROM cmp_instance_operations`
	exec(record)
	exec("CREATE TABLE fixture_backlog AS SELECT sink_id,sequence,public_id,event_type,body FROM cmp_operation_event_outbox")
	if err := alerts.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec(record)
	var unchanged, sources, notifications int
	err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM fixture_backlog JOIN cmp_operation_event_outbox USING (sink_id,sequence,public_id,event_type,body)),
		count(DISTINCT event_type),count(*) FROM cmp_operation_event_outbox WHERE sequence IS NOT NULL AND alert_id IS NULL`).
		Scan(&unchanged, &sources, &notifications)
	if err != nil || unchanged != 4 || sources != 4 || notifications != 8 {
		t.Fatalf("delivery changed by alert migration: unchanged=%d sources=%d notifications=%d err=%v", unchanged, sources, notifications, err)
	}
}

func TestAcceptanceSignedAlertDelivery(t *testing.T) {
	db, ctx := openDeliveryTestDB(t), t.Context()
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','acct','project','aws-ec2','fixture/do-not-transmit','aws:alerts','read_only');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds,failures,last_outcome)
		VALUES ('tenant','acct','us-east-1','instance',300,3,'failure');`)
	const key = "fixture-alert-destination-signing-secret"
	var requests atomic.Int32
	type delivered struct {
		ID, Type, Rule, Tenant, Project string
		AlertID                         string `json:"alert_id"`
		SchemaVersion                   int    `json:"schema_version"`
		Revision                        int64  `json:"revision"`
	}
	type receipt struct {
		event delivered
		body  []byte
	}
	receipts := make(chan receipt, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signature, err := webhook.VerifyHMAC(r, webhook.HMACConfig{Secret: []byte(key), Header: "X-Webhook-Signature", Prefix: "v1=",
			SignedFormat: webhook.PayloadTimestampBody, Replay: webhook.HMACReplayConfig{TimestampHeader: "X-Webhook-Timestamp"}})
		if err != nil || r.Header.Get("X-Webhook-Id") != "alerts" {
			t.Error("unexpected alert signature or destination")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var event delivered
		if err := json.Unmarshal(signature.Body, &event); err != nil || event.ID != r.Header.Get("X-Webhook-Delivery") ||
			event.Type != r.Header.Get("X-Webhook-Event") || event.SchemaVersion != 1 || event.Rule != "scan_failures" ||
			event.Tenant != "tenant" || event.Project != "project" || event.AlertID == "" ||
			bytes.Contains(signature.Body, []byte("fixture/do-not-transmit")) {
			t.Errorf("unexpected alert payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case receipts <- receipt{event, signature.Body}:
		default:
			t.Error("unexpected additional delivery")
		}
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	subscribe := func(id string, enabled bool) Destination {
		t.Helper()
		dest, err := NewDestination(Config{ID: id, Tenant: "tenant", Project: "project", Endpoint: server.URL + "/events",
			Secret: key, IncludeAlerts: enabled})
		if err != nil {
			t.Fatal(err)
		}
		if err := NewStore(db).Register(ctx, dest); err != nil {
			t.Fatal(err)
		}
		return dest
	}
	alertSink := subscribe("alerts", true)
	legacy := subscribe("legacy", false)
	publisher, err := NewPublisher(ctx, NewStore(db), []Destination{alertSink, legacy}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	evaluator := alerts.NewStore(db)
	evaluate := func() {
		t.Helper()
		worked, err := evaluator.EvaluateNext(ctx)
		if !worked || err != nil {
			t.Fatalf("evaluate worked=%t err=%v", worked, err)
		}
	}
	step := func() receipt {
		t.Helper()
		worked, err := publisher.step(ctx)
		if !worked || err != nil {
			t.Fatalf("publisher worked=%t err=%v", worked, err)
		}
		select {
		case received := <-receipts:
			return received
		default:
			t.Fatal("webhook was not received")
			return receipt{}
		}
	}
	evaluate()
	initial := step()
	if initial.event.Type != "project.alert.opened" || initial.event.Revision != 1 {
		t.Fatalf("unexpected initial notification: %+v", initial.event)
	}
	var delayed bool
	if err := db.QueryRowContext(ctx, `SELECT available_at>statement_timestamp() AND delivered_at IS NULL
		FROM cmp_operation_event_outbox WHERE public_id=$1::uuid`, initial.event.ID).Scan(&delayed); err != nil || !delayed {
		t.Fatalf("retry delay not recorded: %v", err)
	}
	exec("UPDATE cmp_operation_event_outbox SET available_at=statement_timestamp() WHERE sink_id='alerts'")
	if replay := step(); replay.event.ID != initial.event.ID || !bytes.Equal(replay.body, initial.body) {
		t.Fatal("alert retry changed identity or payload")
	}
	exec(`UPDATE cmp_inventory_schedules SET failures=0,last_outcome='success';
	      UPDATE cmp_project_alert_checks SET next_at=statement_timestamp();`)
	evaluate()
	recovery := step()
	if recovery.event.Type != "project.alert.resolved" || recovery.event.AlertID != initial.event.AlertID ||
		recovery.event.ID == initial.event.ID || recovery.event.Revision != 2 {
		t.Fatalf("inconsistent recovery: %+v", recovery.event)
	}
	if worked, err := publisher.step(ctx); err != nil || worked {
		t.Fatalf("legacy sink or duplicate delivery remained: worked=%t err=%v", worked, err)
	}
	subscribe("alerts", false)
	exec(`UPDATE cmp_inventory_schedules SET failures=3,last_outcome='failure';
	      UPDATE cmp_project_alert_checks SET next_at=statement_timestamp();`)
	evaluate()
	if worked, err := publisher.step(ctx); err != nil || worked {
		t.Fatalf("delivery after opt-out: worked=%t err=%v", worked, err)
	}
}
