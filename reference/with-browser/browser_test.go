package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/security/authn"
)

type fixtureProvider struct{}

func (fixtureProvider) authorizationURL(state, _, _ string) string {
	return "https://issuer.example/login?state=" + url.QueryEscape(state)
}
func (fixtureProvider) identity(ctx context.Context, code, verifier, nonce string) (authn.Principal, error) {
	if code != "fixture-code" || verifier == "" || nonce == "" {
		return authn.Principal{}, errors.New("exchange rejected")
	}
	if _, ok := ctx.Deadline(); !ok {
		return authn.Principal{}, errors.New("identity verification is unbounded")
	}
	return mapPrincipal("https://issuer.example", "test-user", "web-admins", []string{"web-admins"}), nil
}

func TestBrowserLoginProfileAndLogout(t *testing.T) {
	now := time.Unix(10, 0)
	sessions := newSessions(func() time.Time { return now })
	origin, err := secureURL("https://app.example")
	if err != nil {
		t.Fatal(err)
	}
	app, err := newBrowser("127.0.0.1:0", origin, fixtureProvider{}, sessions)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, data string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(data))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if cookie != nil {
			req.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	begin := func() *http.Cookie {
		w := request("POST", "/login", "", nil, "https://app.example")
		if w.Code != 303 || len(w.Result().Cookies()) != 1 {
			t.Fatalf("begin login: %d %s", w.Code, w.Body)
		}
		cookie := w.Result().Cookies()[0]
		if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
			t.Fatalf("insecure login cookie: %+v", cookie)
		}
		return cookie
	}
	login := begin()
	if w := request("GET", "/auth/callback?state=wrong&code=fixture-code", "", login, ""); w.Code != 401 {
		t.Fatalf("accepted wrong OAuth state: %d", w.Code)
	}
	path := "/auth/callback?code=fixture-code&state=" + url.QueryEscape(login.Value)
	w := request("GET", path, "", login, "")
	if w.Code != 303 || len(w.Result().Cookies()) != 2 {
		t.Fatalf("callback: %d %s", w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[1]
	if w := request("GET", path, "", login, ""); w.Code != 401 {
		t.Fatalf("reused state: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "https://app.example/", nil)
	r.AddCookie(cookie)
	_, session, ok := sessions.get(r)
	if !ok {
		t.Fatal("session was not established")
	}
	invalidForm := url.Values{"csrf": {session.csrf}, "name": {strings.Repeat("<", 81)}}.Encode()
	if w := request("POST", "/profile", invalidForm, cookie, "https://app.example"); w.Code != http.StatusBadRequest ||
		!strings.Contains(w.Body.String(), "Enter a display name") || !strings.Contains(w.Body.String(), "&lt;") {
		t.Fatalf("invalid form was not shown safely: %d %s", w.Code, w.Body)
	}
	form := url.Values{"csrf": {session.csrf}, "name": {"<example>"}}.Encode()
	queryToken := "/profile?csrf=" + url.QueryEscape(session.csrf)
	if w := request("POST", queryToken, "name=query-token", cookie, "https://app.example"); w.Code != http.StatusForbidden {
		t.Fatalf("query supplied the form token: %d", w.Code)
	}
	if w := request("POST", "/profile", "name="+strings.Repeat("a", 4096), cookie, "https://app.example"); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized form: %d", w.Code)
	}
	if w := request("POST", "/profile", "name=%", cookie, "https://app.example"); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed form: %d", w.Code)
	}
	wrongMedia := httptest.NewRequest(http.MethodPost, "https://app.example/profile", strings.NewReader(form))
	wrongMedia.Header.Set("Content-Type", "text/plain")
	wrongMedia.AddCookie(cookie)
	rejected := httptest.NewRecorder()
	app.ServeHTTP(rejected, wrongMedia)
	if rejected.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("unsupported form media type: %d", rejected.Code)
	}
	for _, denied := range []struct{ body, origin string }{
		{"name=no-token", "https://app.example"},
		{form, "https://other.example"},
	} {
		if w := request("POST", "/profile", denied.body, cookie, denied.origin); w.Code != 403 {
			t.Fatalf("allowed rejected form: %d", w.Code)
		}
	}
	if w := request("POST", "/profile?csrf=%&name=ignored", form, cookie, "https://app.example"); w.Code != 303 {
		t.Fatalf("form did not redirect: %d", w.Code)
	}
	if w := request("GET", "/", "", cookie, ""); !strings.Contains(w.Body.String(), "&lt;example&gt;") {
		t.Fatalf("profile output was not escaped: %s", w.Body)
	}
	if w := request("GET", "/admin", "", cookie, ""); w.Code != 200 {
		t.Fatalf("admin role was not mapped: %d", w.Code)
	}
	now = now.Add(50 * time.Minute)
	for _, denied := range []struct{ body, origin string }{
		{"csrf=invalid", "https://app.example"},
		{form, "https://other.example"},
	} {
		if w := request("POST", "/session/renew", denied.body, cookie, denied.origin); w.Code != 403 ||
			len(w.Result().Cookies()) != 0 {
			t.Fatalf("unauthorized session renewal: %d", w.Code)
		}
	}
	w = request("POST", "/session/renew", form, cookie, "https://app.example")
	cookies := w.Result().Cookies()
	if w.Code != http.StatusSeeOther || len(cookies) != 1 || cookies[0].Value == cookie.Value ||
		!cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode ||
		cookies[0].MaxAge != int(sessionTTL/time.Second) {
		t.Fatalf("session renewal returned invalid status or cookie attributes: %d", w.Code)
	}
	if w := request("GET", "/admin", "", cookie, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("old session remained usable after renewal: %d", w.Code)
	}
	cookie = cookies[0]
	now = now.Add(10 * time.Minute)
	if w := request("GET", "/", "", cookie, ""); !strings.Contains(w.Body.String(), "&lt;example&gt;") {
		t.Fatal("renewed session lost the profile at the original expiry")
	}
	if w := request("POST", "/logout", form, cookie, "https://app.example"); w.Code != http.StatusForbidden {
		t.Fatalf("previous CSRF token remained valid: %d", w.Code)
	}
	r = httptest.NewRequest("GET", "https://app.example/", nil)
	r.AddCookie(cookie)
	_, session, ok = sessions.get(r)
	if !ok {
		t.Fatal("renewal did not establish a session")
	}
	form = url.Values{"csrf": {session.csrf}}.Encode()
	if w := request("POST", "/logout", form, cookie, "https://app.example"); w.Code != 303 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := request("GET", "/admin", "", cookie, ""); w.Code != 401 {
		t.Fatalf("session remained usable: %d", w.Code)
	}
	login = begin()
	path = "/auth/callback?state=" + url.QueryEscape(login.Value)
	if w := request("GET", path+"&code=rejected", "", login, ""); w.Code != http.StatusUnauthorized ||
		strings.Contains(w.Body.String(), "exchange rejected") || len(w.Result().Cookies()) != 1 {
		t.Fatalf("identity provider failure was not handled safely: %d %s", w.Code, w.Body)
	}
	if w := request("GET", path+"&code=fixture-code", "", login, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("rejected login state could be reused: %d", w.Code)
	}
	login = begin()
	path = "/auth/callback?code=fixture-code&state=" + url.QueryEscape(login.Value)
	w = request("GET", path, "", login, "")
	if w.Code != http.StatusSeeOther || len(w.Result().Cookies()) != 2 {
		t.Fatalf("new login failed: %d %s", w.Code, w.Body)
	}
	expiring := w.Result().Cookies()[1]
	now = now.Add(sessionTTL)
	if w := request("GET", "/admin", "", expiring, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired session remained usable: %d", w.Code)
	}
	login = begin()
	now = now.Add(loginTTL)
	path = "/auth/callback?code=fixture-code&state=" + url.QueryEscape(login.Value)
	if w := request("GET", path, "", login, ""); w.Code != 401 {
		t.Fatalf("expired login state: %d", w.Code)
	}
}

func TestSessionRenewalHasAbsoluteExpiry(t *testing.T) {
	now := time.Unix(10, 0)
	sessions := newSessions(func() time.Time { return now })
	req := httptest.NewRequest(http.MethodGet, "https://app.example/", nil)
	key, ok := sessions.establish(mapPrincipal("https://issuer.example", "user", "", nil), req)
	if !ok {
		t.Fatal("failed to establish initial session")
	}
	var ttl time.Duration
	for range 8 {
		now = now.Add(59 * time.Minute)
		session := sessions.sessions[key]
		previous := key
		key, ttl, ok = sessions.apply(previous, session.csrf, "", renewSession)
		if !ok {
			t.Fatal("session expired before the absolute limit")
		}
		if _, _, ok := sessions.apply(previous, session.csrf, "", renewSession); ok {
			t.Fatal("session could be renewed twice with the same credentials")
		}
	}
	if ttl != 8*time.Minute {
		t.Fatalf("last renewal TTL = %s", ttl)
	}
	now = now.Add(ttl)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: key})
	if _, _, ok := sessions.get(req); ok {
		t.Fatal("session survived the absolute expiry")
	}
}
