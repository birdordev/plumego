package consoletest

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/websocket"

	"with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	consolehttp "with-cloud-manager/app/handlers/console"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/openstack"
)

const consoleFixtureAsset = "21111111-1111-4111-8111-111111111111"
const consoleFixtureToken = "fixture-secret-nova-console-token"

func TestAcceptanceOpenStackNativeConsole(t *testing.T) {
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
		  ('tenant','operator','human','https://issuer.example','one'),
		  ('tenant','second','human','https://issuer.example','two'),
		  ('tenant','administrator','human','https://issuer.example','admin'),
		  ('tenant','reviewer','service','fixture','reviewer'),
		  ('foreign','foreign','human','https://issuer.example','foreign');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		  ('tenant','project','operator','operator'),('tenant','project','second','operator'),
		  ('tenant','project','administrator','admin'),('tenant','other','administrator','admin'),
		  ('tenant','project','reviewer','approver'),('foreign','project','foreign','operator');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','nova','project','openstack','fixture/private','site:nova-project','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant','nova','RegionOne','instance',1,now());`)
	exec(`INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$1,'console fixture','ACTIVE',1 FROM cmp_inventory_scopes`, consoleFixtureAsset)
	const reviewerToken = "fixture-console-approval-service-bearer"
	hash := sha256.Sum256([]byte(reviewerToken))
	exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','reviewer',now()+interval '5 minutes')`, hash[:])
	sessions := identity.NewBrowserSessions(db)
	login := func(tenant, subject string) string {
		t.Helper()
		cookie, err := sessions.Login(ctx, tenant, "https://issuer.example", subject, "")
		if err != nil {
			t.Fatal(err)
		}
		return cookie
	}
	operator, second := login("tenant", "one"), login("tenant", "two")
	admin, foreign := login("tenant", "admin"), login("foreign", "foreign")
	nova, mintCalls, ended := startNovaConsoleFixture(t)
	defer nova.Close()
	cfg := openstack.Config{
		Site: "site", Project: "nova-project", IdentityURL: nova.URL + "/v3",
		ApplicationID: "fixture-app", Secret: "fixture-keystone-secret", EndpointOrigins: []string{nova.URL},
		ConsoleProxyURL: nova.URL + "/websockify",
	}
	front := httptest.NewUnstartedServer(nil)
	origin := "https://" + front.Listener.Addr().String()
	pki := testsupport.NewConsolePKI(t, "fixture-gateway.example")
	console := consolehttp.ConsoleConfig{Origin: origin, CertFile: pki.CertFile, KeyFile: pki.KeyFile, NativeTransport: nova.Client().Transport}
	console.NativeConnect = func(ctx context.Context, session nativeconsole.Session) (*url.URL, error) {
		if session.Binding.RemoteIdentity != "site:nova-project" || session.Binding.CredentialRef != "fixture/private" {
			return nil, identity.ErrForbidden
		}
		client, err := openstack.Connect(ctx, cfg, nova.Client())
		if err != nil {
			return nil, err
		}
		scope := provider.Scope{Provider: "openstack", RemoteIdentity: client.Identity(), Region: session.Region, Kind: provider.Instance}
		return client.OpenNoVNC(ctx, scope, session.AssetID)
	}
	app, err := routers.NewAPI(db, "", &console)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	front.Config.Handler = app
	front.StartTLS()
	defer front.Close()
	headers := func(cookie string) http.Header {
		h := http.Header{"Cookie": {consolehttp.ConsoleCookie + "=" + cookie}, "Origin": {origin}}
		h.Set(consolehttp.ConsoleCSRFHeader, consolehttp.ConsoleCSRF(cookie))
		return h
	}
	send := func(method, path, cookie, match, key, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != "" {
			r.Header = headers(cookie)
		}
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		res := httptest.NewRecorder()
		app.ServeHTTP(res, r)
		if res.Code != status || strings.Contains(res.Body.String(), consoleFixtureToken) || strings.Contains(res.Body.String(), "fixture/private") {
			t.Fatalf("%s %s want %d got %d: %s", method, path, status, res.Code, res.Body.String())
		}
		return res
	}
	const base = "/v1/tenants/tenant/projects/project"
	const collection = base + "/console-grants"
	const payload = `{"account":"nova","region":"RegionOne","asset_id":"` + consoleFixtureAsset + `","purpose":"troubleshoot"}`
	send("POST", collection, "", `"1"`, "first", payload, 401)
	send("POST", collection, operator, `"99"`, "first", payload, 412)
	assetETag := `"1"`
	create := func(key string) nativeconsole.Grant {
		t.Helper()
		var g nativeconsole.Grant
		w := send("POST", collection, operator, assetETag, key, payload, 201)
		testsupport.DecodeProjectData(t, w, &g)
		if w.Header().Get("ETag") != `"1"` || w.Header().Get("Location") != collection+"/"+g.ID || g.Status != "awaiting_approval" {
			t.Fatalf("unexpected grant: %+v", g)
		}
		return g
	}
	grant := create("first")
	if replay := create("first"); grant.ID != replay.ID {
		t.Fatal("console request replay created another grant")
	}
	send("POST", collection, operator, `"1"`, "first", strings.Replace(payload, "troubleshoot", "maintenance", 1), 409)
	detail := collection + "/" + grant.ID
	send("GET", detail, foreign, "", "", "", 403)
	send("GET", strings.Replace(detail, "/project/", "/other/", 1), admin, "", "", "", 403)
	send("POST", detail+"/approval", operator, `"1"`, "", `{"decision":"approve"}`, 403)
	stream := detail + "/stream"
	dial := func(h http.Header, want int) *websocket.Conn {
		t.Helper()
		handshake, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		conn, res, err := websocket.Dial(handshake, front.URL+stream, &websocket.DialOptions{
			HTTPClient: front.Client(), HTTPHeader: h, Subprotocols: []string{"binary"},
		})
		if res == nil || res.StatusCode != want || (want == http.StatusSwitchingProtocols) != (err == nil) {
			t.Fatalf("unexpected console handshake: response=%v err=%v", res != nil, err)
		}
		if res.Header.Get("X-Fixture-Private") != "" || res.Header.Get("Set-Cookie") != "" {
			t.Fatal("upstream console header leaked to the browser")
		}
		return conn
	}
	dial(headers(operator), 412)
	var output bytes.Buffer
	approve := func(id string, expected bool) {
		t.Helper()
		output.Reset()
		err := commands.RunNativeClient(ctx, []string{"approve", "tenant", "project", id, "--if-match", "1"},
			front.URL, reviewerToken, &output, front.Client().Transport)
		if (err == nil) != expected {
			t.Fatalf("approve expected success=%t err=%v", expected, err)
		}
	}
	approve(grant.ID, true)
	dial(headers(second), 403)
	wrongOrigin := headers(operator)
	wrongOrigin.Set("Origin", "https://offsite.example")
	dial(wrongOrigin, 403)
	send("GET", stream, operator, "", "", "", 400)
	conn := dial(headers(operator), 101)
	defer func() { _ = conn.Close() }()
	op, banner, err := conn.ReadMessage()
	if err != nil || op != 2 || string(banner) != "RFB 003.008\n" {
		t.Fatalf("native frame not relayed: opcode=%d data=%q err=%v", op, banner, err)
	}
	if err := conn.WriteMessage(2, banner); err != nil {
		t.Fatal(err)
	}
	if op, security, err := conn.ReadMessage(); err != nil || op != 2 || !bytes.Equal(security, []byte{1, 1}) {
		t.Fatalf("browser frame was not relayed to Nova: opcode=%d data=%v err=%v", op, security, err)
	}
	dial(headers(operator), 412)
	if mintCalls.Load() != 1 {
		t.Fatalf("expected one Nova session allocation, got %d", mintCalls.Load())
	}
	send("POST", detail+"/revoke", admin, `"2"`, "", "", 412)
	send("POST", detail+"/revoke", admin, `"3"`, "", "", 200)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked console socket remained open")
	}
	var events audit.Page
	testsupport.DecodeProjectData(t, send("GET", base+"/audit?category=console_access&subject_id="+grant.ID, admin, "", "", "", 200), &events)
	if len(events.Items) != 4 || events.Items[0].Action != "revoked" || events.Items[0].Actor != "administrator" ||
		events.Items[1].Action != "in_use" || events.Items[1].Actor != "operator" || events.Items[2].Actor != "reviewer" {
		t.Fatalf("unexpected native console audit: %+v", events.Items)
	}
	stale := create("rotation")
	exec("UPDATE cmp_cloud_accounts SET credential_ref='fixture/rotated' WHERE account_id='nova'")
	assetETag = `"2"`
	approve(stale.ID, false)
	var page nativeconsole.Page
	testsupport.DecodeProjectData(t, send("GET", collection+"?limit=1", admin, "", "", "", 200), &page)
	if len(page.Items) != 1 || page.Next != page.Items[0].ID {
		t.Fatalf("missing native console next page: %+v", page)
	}
	testsupport.DecodeProjectData(t, send("GET", collection+"?limit=1&after="+page.Next, admin, "", "", "", 200), &page)
	if len(page.Items) != 1 || page.Items[0].ID != grant.ID {
		t.Fatalf("invalid native console next page: %+v", page)
	}
	withdrawn := create("reviewer-removed")
	approve(withdrawn.ID, true)
	exec("UPDATE cmp_memberships SET active=false WHERE (tenant_id,project_id,principal_id)=('tenant','project','reviewer')")
	stream = collection + "/" + withdrawn.ID + "/stream"
	dial(headers(operator), 403)
	exec("UPDATE cmp_memberships SET active=true WHERE (tenant_id,project_id,principal_id)=('tenant','project','reviewer')")
	expired := create("expired")
	approve(expired.ID, true)
	exec("UPDATE cmp_instance_console_grants SET expires_at=now()-interval '1 second' WHERE grant_id=$1::uuid", expired.ID)
	stream = collection + "/" + expired.ID + "/stream"
	dial(headers(operator), 412)
	if mintCalls.Load() != 1 {
		t.Fatalf("expired or withdrawn grants reached Nova: %d allocations", mintCalls.Load())
	}
}

func startNovaConsoleFixture(t *testing.T) (*httptest.Server, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	var mint atomic.Int32
	closed := make(chan struct{}, 1)
	fixture := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		origin := "https://" + r.Host
		switch r.URL.Path {
		case "/v3/auth/tokens":
			w.Header().Set("X-Subject-Token", "fixture-private-keystone-token")
			w.WriteHeader(http.StatusCreated)
			writeNativeFixture(t, w, `{"token":{"project":{"id":"nova-project"},"catalog":[{"type":"compute","endpoints":[{"region_id":"RegionOne","interface":"public","url":%q}]}]}}`, origin+"/v2.1")
		case "/v2.1/servers/" + consoleFixtureAsset:
			if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != "fixture-private-keystone-token" {
				t.Error("unexpected Nova target lookup")
			}
			writeNativeFixture(t, w, `{"server":{"id":%q,"tenant_id":"nova-project"}}`, consoleFixtureAsset)
		case "/v2.1/servers/" + consoleFixtureAsset + "/remote-consoles":
			mint.Add(1)
			body, err := io.ReadAll(io.LimitReader(r.Body, 512))
			if err != nil || r.Method != http.MethodPost || r.Header.Get("OpenStack-API-Version") != "compute 2.6" ||
				string(body) != `{"remote_console":{"protocol":"vnc","type":"novnc"}}` {
				t.Error("unexpected Nova console API request")
			}
			address := origin + "/vnc_lite.html?path=" + url.QueryEscape("websockify?token="+consoleFixtureToken)
			writeNativeFixture(t, w, `{"remote_console":{"protocol":"vnc","type":"novnc","url":%q}}`, address)
		case "/websockify":
			protocol := r.Header.Get("Sec-WebSocket-Protocol")
			if r.URL.Query().Get("token") != consoleFixtureToken || r.Header.Get("Cookie") != "" ||
				r.Header.Get("Authorization") != "" || r.Header.Get("X-Auth-Token") != "" || r.Header.Get("Origin") != origin ||
				(protocol != "" && protocol != "binary") {
				t.Error("console relay forwarded incorrect credentials or headers")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			serveNativeFixtureSocket(t, w, r, closed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	fixture.StartTLS()
	return fixture, &mint, closed
}

func writeNativeFixture(t *testing.T, w http.ResponseWriter, format string, args ...any) {
	t.Helper()
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		t.Error(err)
	}
}

func serveNativeFixtureSocket(t *testing.T, w http.ResponseWriter, r *http.Request, closed chan<- struct{}) {
	t.Helper()
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() { _ = conn.Close() }()
	defer func() { closed <- struct{}{} }()
	defer func() { _ = rw.Flush() }()
	sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	protocol := ""
	if r.Header.Get("Sec-WebSocket-Protocol") == "binary" {
		protocol = "Sec-WebSocket-Protocol: binary\r\n"
	}
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n%sX-Fixture-Private: %s\r\nSet-Cookie: bad=token\r\n\r\n",
		base64.StdEncoding.EncodeToString(sum[:]), protocol, consoleFixtureToken); err != nil {
		t.Error(err)
		return
	}
	initial := []byte{0, 16, 0, 16, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0, 0, 0, 0, 10}
	initial = append(initial, "Fixture VM"...)
	for _, step := range []struct{ send, expect []byte }{
		{[]byte("RFB 003.008\n"), []byte("RFB 003.008\n")},
		{[]byte{1, 1}, []byte{1}},
		{[]byte{0, 0, 0, 0}, []byte{1}},
		{initial, nil},
	} {
		if _, err := rw.Write(append([]byte{0x82, byte(len(step.send))}, step.send...)); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}
		if len(step.expect) > 0 {
			message, err := readSmallNativeFrame(rw)
			if err != nil {
				return
			}
			if !bytes.Equal(message, step.expect) {
				t.Errorf("unexpected RFB handshake bytes: %v", message)
				return
			}
		}
	}
	_, _ = io.Copy(io.Discard, rw)
}

func readSmallNativeFrame(r io.Reader) ([]byte, error) {
	var header [6]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	length := header[1] & 0x7f
	if header[0] != 0x82 || header[1]&0x80 == 0 || length >= 126 {
		return nil, fmt.Errorf("invalid test WebSocket frame header %d", binary.BigEndian.Uint16(header[:2]))
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	for i := range payload {
		payload[i] ^= header[2+i%4]
	}
	return payload, nil
}
