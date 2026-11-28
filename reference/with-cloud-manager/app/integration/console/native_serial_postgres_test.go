package consoletest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	consolehttp "with-cloud-manager/app/handlers/console"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/provider"
)

const serialFixtureID = "i-0123456789abcdef0"
const serialFixtureHost = "serial-console.ec2-instance-connect.us-east-1.aws"

func fixtureRSAPublicKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var wire []byte
	for _, part := range [][]byte{[]byte("ssh-rsa"), {1, 0, 1}, append([]byte{0}, key.N.Bytes()...)} {
		wire = binary.BigEndian.AppendUint32(wire, uint32(len(part)))
		wire = append(wire, part...)
	}
	return "ssh-rsa " + base64.StdEncoding.EncodeToString(wire)
}

func TestAcceptanceAWSSerialConsoleApproval(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	if err := app.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('foreign');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other'),('foreign','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
		 ('tenant','operator','service','test','operator'),('tenant','reviewer','service','test','reviewer'),
		 ('tenant','admin','service','test','admin'),('tenant','peer','service','test','peer'),('foreign','foreign','service','test','foreign');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		 ('tenant','project','operator','operator'),('tenant','project','reviewer','approver'),('tenant','project','admin','admin'),
		 ('tenant','other','admin','admin'),('tenant','project','peer','operator'),('foreign','project','foreign','operator');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','aws','project','aws-ec2','fixture/private','aws:123456789012','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant','aws','us-east-1','instance',1,now());`)
	exec(`INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$1,'serial fixture','running',1 FROM cmp_inventory_scopes`, serialFixtureID)
	token := func(principal string) string { return "fixture-serial-test-service-token-" + principal }
	for _, principal := range []string{"operator", "reviewer", "admin", "peer", "foreign"} {
		hash := sha256.Sum256([]byte(token(principal)))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			SELECT $1,tenant_id,principal_id,now()+interval '5 minutes' FROM cmp_principals WHERE principal_id=$2`, hash[:], principal)
	}
	publicKey := fixtureRSAPublicKey(t)
	wrongKey := fixtureRSAPublicKey(t)
	entered, allow := make(chan struct{}, 1), make(chan struct{})
	var writes, mode atomic.Int32
	cfg := consolehttp.ConsoleConfig{}
	pki := testsupport.NewConsolePKI(t, "serial-fixture-gateway.example")
	cfg.CertFile, cfg.KeyFile = pki.CertFile, pki.KeyFile
	cfg.NativeSerial = func(_ context.Context, s nativeconsole.Session, key string) (provider.SerialDispatch, error) {
		if s.AssetID != serialFixtureID || s.Region != "us-east-1" || s.Provider != "aws-ec2" ||
			s.Binding.RemoteIdentity != "aws:123456789012" || s.Binding.CredentialRef != "fixture/private" || key != publicKey {
			t.Error("unapproved identity, region, asset or public key reached AWS preparation")
			return nil, &provider.Failure{Kind: provider.Unauthorized}
		}
		if mode.Load() == 3 {
			exec("UPDATE cmp_memberships SET active=false WHERE principal_id='reviewer'")
		}
		return func(ctx context.Context) (provider.SerialReceipt, error) {
			writes.Add(1)
			switch mode.Load() {
			case 1:
				entered <- struct{}{}
				select {
				case <-allow:
				case <-ctx.Done():
					return provider.SerialReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
				}
			case 2:
				return provider.SerialReceipt{}, &provider.Failure{Kind: provider.OutcomeUnknown}
			}
			return provider.SerialReceipt{Host: serialFixtureHost, Port: 22, Username: serialFixtureID + ".port0",
				ValidUntil: time.Now().Add(time.Minute).UTC(), RequestID: "fixture-serial-receipt"}, nil
		}, nil
	}
	app, err := routers.NewAPI(db, "", &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewTLSServer(app)
	defer api.Close()
	const base = "/v1/tenants/tenant/projects/project"
	const collection = base + "/console-grants"
	send := func(method, path, principal, revision, key string, body []byte) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		if principal != "" {
			r.Header.Set("Authorization", "Bearer "+token(principal))
		}
		if revision != "" {
			r.Header.Set("If-Match", revision)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if len(body) != 0 {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	want := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status || strings.Contains(w.Body.String(), publicKey) || strings.Contains(w.Body.String(), "fixture/private") {
			t.Fatalf("unexpected API outcome: got HTTP %d, wanted %d; response contained disallowed fields or error", w.Code, status)
		}
	}
	payload := func(data any) []byte {
		t.Helper()
		body, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	application := func(key string) []byte {
		return payload(nativeconsole.Request{Account: "aws", Region: "us-east-1", AssetID: serialFixtureID, Purpose: "maintenance", SSHPublicKey: key})
	}
	publicKeyFile := filepath.Join(t.TempDir(), "fixture.pub")
	if err := os.WriteFile(publicKeyFile, []byte(publicKey+" local-comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(principal string, args ...string) []byte {
		t.Helper()
		var output bytes.Buffer
		command := append([]string{args[0], "tenant", "project"}, args[1:]...)
		if err := commands.RunNativeClient(ctx, command, api.URL, token(principal), &output, api.Client().Transport); err != nil {
			t.Fatal(err)
		}
		data := output.Bytes()
		if bytes.Contains(data, []byte(publicKey)) {
			t.Fatal("CLI emitted raw public key")
		}
		return data
	}
	requestGrant := func(idempotency string) (g nativeconsole.Grant) {
		t.Helper()
		data := call("operator", "request-serial", "--account", "aws", "--region", "us-east-1", "--asset-id", serialFixtureID,
			"--purpose", "maintenance", "--if-match", "1", "--idempotency-key", idempotency, "--public-key", publicKeyFile)
		if err := json.Unmarshal(data, &g); err != nil || g.Status != "awaiting_approval" || g.Revision != 1 || g.Provider != "aws-ec2" ||
			!strings.HasPrefix(g.KeyFingerprint, "SHA256:") {
			t.Fatalf("invalid grant: %+v %v", g, err)
		}
		return g
	}
	approve := func(g nativeconsole.Grant) {
		t.Helper()
		var approved nativeconsole.Grant
		if err := json.Unmarshal(call("reviewer", "approve", g.ID, "--if-match", "1"), &approved); err != nil || approved.Status != "approved" || approved.Revision != 2 {
			t.Fatalf("invalid independent approval: %+v %v", approved, err)
		}
	}
	g := requestGrant("initial")
	detail := collection + "/" + g.ID
	activation := detail + "/serial-key"
	approvedKey := payload(consolehttp.SerialKeyInput{SSHPublicKey: publicKey})
	want(send("POST", collection, "operator", `"1"`, "initial", application(wrongKey)), 409)
	replay := send("POST", collection, "operator", `"1"`, "initial", application(publicKey+" edited comment"))
	want(replay, 201)
	var repeated nativeconsole.Grant
	testsupport.DecodeProjectData(t, replay, &repeated)
	if repeated.ID != g.ID {
		t.Fatal("identical fingerprint did not return prior grant")
	}
	want(send("GET", detail, "foreign", "", "", nil), 403)
	want(send("GET", strings.Replace(detail, "/project/", "/other/", 1), "admin", "", "", nil), 403)
	want(send("POST", activation, "operator", `"1"`, "", approvedKey), 412)
	want(send("POST", detail+"/approval", "operator", `"1"`, "", []byte(`{"decision":"approve"}`)), 403)
	approve(g)
	want(send("POST", activation, "peer", `"2"`, "", approvedKey), 403)
	want(send("POST", activation, "operator", `"2"`, "", payload(consolehttp.SerialKeyInput{SSHPublicKey: wrongKey})), 403)
	want(send("POST", activation, "operator", `"2"`, "", payload(consolehttp.SerialKeyInput{SSHPublicKey: "-----BEGIN OPENSSH PRIVATE KEY-----"})), 400)
	want(send("POST", activation, "operator", `"1"`, "", approvedKey), 412)
	if writes.Load() != 0 {
		t.Fatal("denied activation attempted to send a key")
	}
	mode.Store(1)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- send("POST", activation, "operator", `"2"`, "", approvedKey) }()
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("first activation was not dispatched")
	}
	want(send("POST", activation, "operator", `"2"`, "", approvedKey), 412)
	want(send("POST", detail+"/revoke", "admin", `"3"`, "", nil), 412)
	close(allow)
	var result consolehttp.SerialActivation
	select {
	case w := <-finished:
		want(w, 200)
		testsupport.DecodeProjectData(t, w, &result)
	case <-time.After(4 * time.Second):
		t.Fatal("activation did not settle")
	}
	if result.Grant.Status != "key_sent" || result.Grant.Revision != 4 || result.Grant.ProviderRequestID != "fixture-serial-receipt" ||
		result.SSH.Host != serialFixtureHost || result.SSH.Port != 22 || result.SSH.Username != serialFixtureID+".port0" || writes.Load() != 1 {
		t.Fatalf("incorrect one-time receipt: %+v", result)
	}
	var events audit.Page
	w := send("GET", base+"/audit?category=console_access&subject_id="+g.ID, "admin", "", "", nil)
	want(w, 200)
	testsupport.DecodeProjectData(t, w, &events)
	if len(events.Items) != 4 || events.Items[0].Action != "key_sent" || events.Items[1].Action != "in_use" ||
		events.Items[2].Actor != "reviewer" || events.Items[3].Actor != "operator" {
		t.Fatalf("unexpected serial audit events: %+v", events.Items)
	}

	unknown := requestGrant("unknown")
	approve(unknown)
	mode.Store(2)
	path := collection + "/" + unknown.ID
	want(send("POST", path+"/serial-key", "operator", `"2"`, "", approvedKey), 503)
	want(send("POST", path+"/serial-key", "operator", `"2"`, "", approvedKey), 412)
	w = send("GET", path, "operator", "", "", nil)
	want(w, 200)
	testsupport.DecodeProjectData(t, w, &unknown)
	if unknown.Status != "dispatch_unknown" || writes.Load() != 2 {
		t.Fatalf("uncertain AWS write was not retained as consumed: %+v", unknown)
	}

	denied := requestGrant("reviewer-withdrawal")
	approve(denied)
	mode.Store(3)
	want(send("POST", collection+"/"+denied.ID+"/serial-key", "operator", `"2"`, "", approvedKey), 403)
	if writes.Load() != 2 {
		t.Fatal("withdrawn approval reached AWS after preparation")
	}
	exec("UPDATE cmp_memberships SET active=true WHERE principal_id='reviewer'")
	mode.Store(0)
	cli := requestGrant("cli-activation")
	approve(cli)
	if err := json.Unmarshal(call("operator", "activate-serial", cli.ID, "--if-match", "2", "--public-key", publicKeyFile), &result); err != nil ||
		result.Grant.Status != "key_sent" || result.Grant.ID != cli.ID || writes.Load() != 3 {
		t.Fatalf("CLI activation produced invalid receipt: %v", err)
	}
	var persisted int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_instance_console_grants WHERE provider='aws-ec2' AND octet_length(ssh_key_hash)=32").Scan(&persisted); err != nil || persisted != 4 {
		t.Fatalf("unexpected persisted fingerprint count: %d %v", persisted, err)
	}
}
