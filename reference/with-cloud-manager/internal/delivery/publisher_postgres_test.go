package delivery

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/webhook"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func openOperationDeliveryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func(context.Context, *sql.DB) error{
		identity.Migrate, inventory.Migrate, identity.MigrateServiceTokens, controller.Migrate, identity.MigrateAccounts,
		operations.Migrate, audit.Migrate,
	} {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func openDeliveryTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openOperationDeliveryTestDB(t)
	if err := alerts.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAcceptanceOperationOutboxRecovery(t *testing.T) {
	db, ctx := openDeliveryTestDB(t), t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('foreign');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','elsewhere'),('foreign','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
		('tenant','operator','service','fixture','operator'),('tenant','reviewer','service','fixture','reviewer');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		('tenant','project','operator','operator'),('tenant','project','reviewer','approver');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','account','project','aws-ec2','fixture/private-ref','aws:fixture','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant','account','us-east-1','instance',1,statement_timestamp());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,id,id,'stopped',1 FROM cmp_inventory_scopes CROSS JOIN (VALUES ('vm-a'),('vm-b'),('vm-c')) AS vm(id);
		INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','start');
		CREATE TABLE fixture_receiver_receipts (id uuid PRIMARY KEY, body_hash bytea NOT NULL);`)
	const operatorToken = "fixture-outbox-operator-service-token"
	const reviewerToken = "fixture-outbox-reviewer-service-token"
	for user, token := range map[string]string{"operator": operatorToken, "reviewer": reviewerToken} {
		hash := sha256.Sum256([]byte(token))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')", hash[:], user)
	}

	const key = "fixture-test-key-with-more-than-thirty-two-bytes"
	var calls atomic.Int32
	var mode atomic.Value
	mode.Store("lost-response")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /events", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		verified, err := webhook.VerifyHMAC(r, webhook.HMACConfig{Secret: []byte(key), Header: "X-Webhook-Signature", Prefix: "v1=",
			SignedFormat: webhook.PayloadTimestampBody, Replay: webhook.HMACReplayConfig{TimestampHeader: "X-Webhook-Timestamp"}})
		if err != nil || r.Header.Get("X-Webhook-Id") != "primary" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected signature or scope: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var event struct {
			ID, Tenant, Project, Type, Status, Sequence string
			Operation                                   string `json:"operation_id"`
			EffectID                                    string `json:"effect_id"`
			Reason                                      string `json:"reason"`
		}
		if err := json.Unmarshal(verified.Body, &event); err != nil || event.ID != r.Header.Get("X-Webhook-Delivery") ||
			event.Tenant != "tenant" || event.Project != "project" || event.Status != "failed" || event.Reason != "approval_rejected" ||
			event.Type != "instance.operation.failed" || event.Type != r.Header.Get("X-Webhook-Event") || event.Operation == "" || event.Sequence == "" ||
			event.EffectID != "cmp-op-final-v1:"+event.Operation ||
			strings.Contains(string(verified.Body), "fixture/private-ref") || strings.Contains(string(verified.Body), "principal_id") {
			t.Errorf("unexpected event %s, %v", verified.Body, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch mode.Load() {
		case "rate":
			w.Header().Set("Retry-After", "90")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case "redirect":
			w.Header().Set("Location", "/unexpected")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		hash := sha256.Sum256(verified.Body)
		if _, err := db.ExecContext(r.Context(), `INSERT INTO fixture_receiver_receipts VALUES ($1::uuid,$2)
			ON CONFLICT (id) DO NOTHING`, event.ID, hash[:]); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var matches bool
		if err := db.QueryRowContext(r.Context(), "SELECT body_hash=$2 FROM fixture_receiver_receipts WHERE id=$1::uuid", event.ID, hash[:]).Scan(&matches); err != nil || !matches {
			t.Errorf("replayed event changed bytes: %v", err)
			w.WriteHeader(http.StatusConflict)
			return
		}
		if mode.Load() == "lost-response" {
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/unexpected", func(w http.ResponseWriter, r *http.Request) {
		t.Error("publisher followed a redirect")
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()

	store := NewStore(db)
	destination := func(id, tenant, project, path string) Destination {
		t.Helper()
		d, err := NewDestination(Config{ID: id, Tenant: tenant, Project: project, Endpoint: server.URL + path, Secret: key})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	primary := destination("primary", "tenant", "project", "/events")
	secondary := destination("secondary", "tenant", "project", "/events")
	otherProject := destination("other-project", "tenant", "elsewhere", "/events")
	foreign := destination("foreign", "foreign", "project", "/events")
	for _, d := range []Destination{primary, secondary, otherProject, foreign} {
		if err := store.Register(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	changed := destination("primary", "tenant", "project", "/changed")
	if err := store.Register(ctx, changed); !errors.Is(err, ErrBinding) {
		t.Fatalf("sink URL could be repointed: %v", err)
	}
	if _, err := NewPublisher(ctx, store, []Destination{changed}, server.Client()); !errors.Is(err, ErrBinding) {
		t.Fatalf("publisher accepted changed URL: %v", err)
	}
	publisher, err := NewPublisher(ctx, store, []Destination{primary, otherProject, foreign}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	step := func(want bool) {
		t.Helper()
		worked, err := publisher.step(ctx)
		if err != nil || worked != want {
			t.Fatalf("delivery worked=%t expected=%t err=%v", worked, want, err)
		}
	}

	opStore := operations.NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: "project"}
	submit := func(id string) operations.Operation {
		t.Helper()
		op, err := opStore.Submit(ctx, operatorToken, scope, id, 1, operations.Input{Account: "account", Region: "us-east-1", AssetID: id, Action: "start"})
		if err != nil {
			t.Fatal(err)
		}
		return op
	}
	reject := func(id string) error {
		_, err := opStore.Approve(ctx, reviewerToken, scope, id, operations.Approval{Decision: "reject"})
		return err
	}
	first := submit("vm-a")
	step(false)
	exec("ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT reject_outbox CHECK (sink_id<>'primary') NOT VALID")
	if err := reject(first.ID); err == nil {
		t.Fatal("injected outbox fault did not abort approval")
	}
	if current, err := opStore.Get(ctx, operatorToken, scope, first.ID); err != nil || current.Status != "awaiting_approval" {
		t.Fatalf("operation changed without its event: status=%s err=%v", current.Status, err)
	}
	exec("ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT reject_outbox")
	if err := reject(first.ID); err != nil {
		t.Fatal(err)
	}
	var count, identities int
	if err := db.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT public_id) FROM cmp_operation_event_outbox`).Scan(&count, &identities); err != nil || count != 2 || identities != 1 {
		t.Fatalf("event fanout was not project scoped: rows=%d ids=%d err=%v", count, identities, err)
	}
	step(true)
	step(false)
	mode.Store("accepted")
	exec("UPDATE cmp_operation_event_outbox SET available_at=statement_timestamp() WHERE sink_id='primary'")
	step(true)
	step(false)
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM fixture_receiver_receipts").Scan(&count); err != nil || count != 1 || calls.Load() != 2 {
		t.Fatalf("deduplicated state count=%d calls=%d error=%v", count, calls.Load(), err)
	}

	type leaseResult struct {
		lease claim
		err   error
	}
	leases := make(chan leaseResult, 2)
	for range 2 {
		go func() {
			lease, err := store.claim(ctx, []string{"secondary"})
			leases <- leaseResult{lease, err}
		}()
	}
	firstLease, secondLease := <-leases, <-leases
	if firstLease.err != nil {
		firstLease, secondLease = secondLease, firstLease
	}
	if firstLease.err != nil || !errors.Is(secondLease.err, sql.ErrNoRows) {
		t.Fatalf("concurrent publishers claimed the same event: %v and %v", firstLease.err, secondLease.err)
	}
	old := firstLease.lease
	exec("UPDATE cmp_operation_event_outbox SET lease_until=statement_timestamp()-interval '1 second' WHERE sink_id='secondary'")
	replacement, err := store.claim(ctx, []string{"secondary"})
	if err != nil || replacement.id != old.id || replacement.token == old.token {
		t.Fatalf("wrong lease takeover: %v", err)
	}
	if settled, err := store.settle(ctx, old, settlement{state: "delivered"}); err != nil || settled {
		t.Fatalf("old claim could settle: %t %v", settled, err)
	}
	if settled, err := store.settle(ctx, replacement, settlement{state: "delivered"}); err != nil || !settled {
		t.Fatalf("replacement settlement: %t %v", settled, err)
	}
	if err := store.Disable(ctx, secondary); err != nil {
		t.Fatal(err)
	}

	if err := reject(submit("vm-b").ID); err != nil {
		t.Fatal(err)
	}
	mode.Store("rate")
	step(true)
	var delayed bool
	if err := db.QueryRowContext(ctx, `SELECT available_at>=clock_timestamp()+interval '80 seconds'
		FROM cmp_operation_event_outbox WHERE sink_id='primary' AND delivered_at IS NULL`).Scan(&delayed); err != nil || !delayed {
		t.Fatalf("retry-after did not delay claim: %v", err)
	}
	step(false)
	mode.Store("redirect")
	exec("UPDATE cmp_operation_event_outbox SET available_at=statement_timestamp() WHERE sink_id='primary' AND delivered_at IS NULL")
	step(true)
	failed, err := store.Failures(ctx, primary)
	if err != nil || len(failed) != 1 || failed[0].Reason != "receiver_rejected" || failed[0].Attempts != 2 ||
		failed[0].HTTPStatus == nil || *failed[0].HTTPStatus != http.StatusTemporaryRedirect {
		t.Fatalf("failed delivery state: %+v %v", failed, err)
	}
	if err := store.Retry(ctx, foreign, failed[0].ID); !errors.Is(err, ErrNotFailed) {
		t.Fatalf("cross-tenant retry accepted: %v", err)
	}
	if err := store.Retry(ctx, primary, failed[0].ID); err != nil {
		t.Fatal(err)
	}
	mode.Store("accepted")
	step(true)
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM fixture_receiver_receipts WHERE id=$1::uuid", failed[0].ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("retry replaced event identity: %d %v", count, err)
	}

	if err := reject(submit("vm-c").ID); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE cmp_operation_event_outbox SET attempts=$1 WHERE sink_id='primary' AND delivered_at IS NULL", maxAttempts)
	prior := calls.Load()
	step(true)
	exhausted, err := store.Failures(ctx, primary)
	if err != nil || len(exhausted) != 1 || exhausted[0].Reason != "lease_exhausted" || calls.Load() != prior {
		t.Fatalf("claim budget not enforced: %+v %v", exhausted, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_operation_event_outbox WHERE sink_id='secondary'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("disabled sink received later events: %d %v", count, err)
	}
}

func TestDestinationRejectsUnsafeURLs(t *testing.T) {
	for _, endpoint := range []string{"http://example.test/", "https://user@example.test/", "https://example.test/a?token=fixture", "https://example.test/a#fragment"} {
		if _, err := NewDestination(Config{ID: "events", Tenant: "tenant", Project: "project", Endpoint: endpoint, Secret: strings.Repeat("x", 32)}); !errors.Is(err, ErrBinding) {
			t.Errorf("unsafe endpoint passed: %v", err)
		}
	}
	if got := failedDelivery(http.StatusServiceUnavailable, nil, "3600", 2); got.state != "retry" || got.retryDelay != 10*time.Minute {
		t.Fatalf("retry delay was not capped: %+v", got)
	}
}
