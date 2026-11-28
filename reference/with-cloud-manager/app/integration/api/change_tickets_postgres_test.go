package apitest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/webhook"

	"with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/delivery"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceChangeTicketIntegration(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	if err := app.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
		SELECT 'tenant',name,'service','fixture',name FROM unnest(ARRAY['admin','reviewer','verifier']) AS names(name);
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		('tenant','project','admin','admin'),('tenant','project','reviewer','approver'),('tenant','project','verifier','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant','account','us-east-1','instance',1,now());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,'i-fixture','fixture','stopped',1 FROM cmp_inventory_scopes;`)
	token := func(name string) string { return "integration-change-ticket-bearer-" + name }
	for _, name := range []string{"admin", "reviewer", "verifier"} {
		digest := sha256.Sum256([]byte(token(name)))
		exec(`INSERT INTO cmp_service_tokens (tenant_id,principal_id,token_hash,expires_at)
			VALUES ('tenant',$1,$2,clock_timestamp()+interval '5 minutes')`, name, digest[:])
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewTLSServer(app)
	defer api.Close()
	const project = "/v1/tenants/tenant/projects/project"
	request := func(method, path, actor, key, revision, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token(actor))
		req.Header.Set("Content-Type", "application/json")
		if revision != "" {
			req.Header.Set("If-Match", revision)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	assertStatus := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("HTTP status %d, want %d: %s", w.Code, want, w.Body.String())
		}
	}
	var cli bytes.Buffer
	if err := commands.RunGovernanceClient(ctx, []string{"ticket-policy-create", "tenant", "project"}, api.URL, token("admin"),
		strings.NewReader(`{"actions":["start"],"verifier":"verifier"}`), &cli, api.Client().Transport); err != nil {
		t.Fatal(err)
	}
	const policyPath = project + "/governance/ticket-policy"
	res := request(http.MethodGet, policyPath, "admin", "", "", "")
	assertStatus(res, http.StatusOK)
	if res.Header().Get("ETag") != `"1"` || !strings.Contains(res.Body.String(), `"revision":"1"`) {
		t.Fatalf("policy version not exposed: %s", res.Body)
	}
	assertStatus(request(http.MethodPut, policyPath, "admin", "", `"2"`, `{"actions":[]}`), http.StatusPreconditionFailed)

	const secret = "fixture-change-ticket-destination-secret"
	var consumed []byte
	var mutex sync.Mutex
	callbacks := make(chan int, 2)
	expiration := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	listener := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		var event struct {
			ID          string `json:"id"`
			OperationID string `json:"operation_id"`
			Type        string `json:"type"`
			Revision    string `json:"policy_revision"`
		}
		signature := "v1=" + webhook.SignV1(secret, r.Header.Get("X-Webhook-Timestamp"), body)
		if err != nil || json.Unmarshal(body, &event) != nil || event.Type != "change.ticket.requested" ||
			event.Revision != "1" || r.Header.Get("X-Webhook-Signature") != signature ||
			r.Header.Get("X-Webhook-Delivery") != event.ID || event.OperationID == "" {
			w.WriteHeader(http.StatusBadRequest)
			t.Error("invalid signed change request")
			return
		}
		mutex.Lock()
		first := consumed == nil
		if first {
			consumed = body
		} else if !bytes.Equal(consumed, body) {
			t.Error("delivery retry changed its signed body")
		}
		mutex.Unlock()
		result := request(http.MethodPost, project+"/governance/change-tickets/"+event.OperationID, "verifier", "", "",
			`{"ticket_id":"CHG-23","source_revision":"17","decision":"approved","valid_until":`+strconv.Quote(expiration)+`}`)
		callbacks <- result.Code
		if first || result.Code != http.StatusOK {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer listener.Close()
	destination, err := delivery.NewDestination(delivery.Config{ID: "itsm", Tenant: "tenant", Project: "project",
		Endpoint: listener.URL, Secret: secret, IncludeChangeTickets: true})
	if err != nil {
		t.Fatal(err)
	}
	outbox := delivery.NewStore(db)
	if err := outbox.Register(ctx, destination); err != nil {
		t.Fatal(err)
	}
	plain, err := delivery.NewDestination(delivery.Config{ID: "plain", Tenant: "tenant", Project: "project", Endpoint: listener.URL, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Register(ctx, plain); err != nil {
		t.Fatal(err)
	}
	submit := func() *httptest.ResponseRecorder {
		return request(http.MethodPost, project+"/instance-operations", "admin", "ticket-change-23", `"1"`,
			`{"account":"account","region":"us-east-1","asset_id":"i-fixture","action":"start"}`)
	}
	exec(`ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT fail_ticket_delivery CHECK (event_type<>'change.ticket.requested') NOT VALID`)
	assertStatus(submit(), http.StatusServiceUnavailable)
	exec("ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT fail_ticket_delivery")
	res = submit()
	assertStatus(res, http.StatusAccepted)
	var receipt struct{ Data operations.Operation }
	if err := json.Unmarshal(res.Body.Bytes(), &receipt); err != nil || receipt.Data.Status != "awaiting_approval" || receipt.Data.TicketPolicyRevision != 1 {
		t.Fatalf("invalid operation receipt: %s, %v", res.Body, err)
	}
	path := project + "/operations/" + receipt.Data.ID + "/approval"
	if res = request(http.MethodPost, path, "reviewer", "", "", `{"decision":"approve"}`); res.Code != http.StatusConflict ||
		!strings.Contains(res.Body.String(), "change_ticket_required") {
		t.Fatalf("local approval bypassed external evidence: %d %s", res.Code, res.Body)
	}
	var subscriptions int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='change.ticket.requested'").
		Scan(&subscriptions); err != nil || subscriptions != 1 {
		t.Fatalf("unexpected ticket destinations: %d %v", subscriptions, err)
	}
	publisher, err := delivery.NewPublisher(ctx, outbox, []delivery.Destination{destination}, listener.Client())
	if err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithTimeout(ctx, 12*time.Second)
	done := make(chan error, 1)
	go func() { done <- publisher.Run(run) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for range 2 {
		select {
		case code := <-callbacks:
			if code != http.StatusOK {
				t.Fatalf("verified connector callback: %d", code)
			}
		case <-run.Done():
			t.Fatal("signed ticket request or retry was not delivered")
		}
	}
	cli.Reset()
	if err := commands.RunGovernanceClient(ctx, []string{"ticket-get", "tenant", "project", receipt.Data.ID}, api.URL, token("admin"),
		nil, &cli, api.Client().Transport); err != nil || !strings.Contains(cli.String(), `"ready":true`) {
		t.Fatalf("CLI cannot read verified evidence: %s %v", &cli, err)
	}
	assertStatus(request(http.MethodPost, path, "reviewer", "", "", `{"decision":"approve"}`), http.StatusOK)
	res = request(http.MethodGet, project+"/audit?category=change_ticket", "admin", "", "", "")
	assertStatus(res, http.StatusOK)
	if strings.Count(res.Body.String(), `"action":"record_receipt"`) != 1 ||
		!strings.Contains(res.Body.String(), `"action":"publish_policy"`) {
		t.Fatalf("unexpected ticket audit: %s", res.Body)
	}
}
