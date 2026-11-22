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
func (fixtureProvider) identity(_ context.Context, code, verifier, nonce string) (authn.Principal, error) {
	if code != "fixture-code" || verifier == "" || nonce == "" {
		return authn.Principal{}, errors.New("exchange rejected")
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
	form := url.Values{"csrf": {session.csrf}, "name": {"<example>"}}.Encode()
	for _, denied := range []struct{ body, origin string }{
		{"name=no-token", "https://app.example"},
		{form, "https://other.example"},
	} {
		if w := request("POST", "/profile", denied.body, cookie, denied.origin); w.Code != 403 {
			t.Fatalf("allowed rejected form: %d", w.Code)
		}
	}
	if w := request("POST", "/profile", form, cookie, "https://app.example"); w.Code != 303 {
		t.Fatalf("form did not redirect: %d", w.Code)
	}
	if w := request("GET", "/", "", cookie, ""); !strings.Contains(w.Body.String(), "&lt;example&gt;") {
		t.Fatalf("profile output was not escaped: %s", w.Body)
	}
	if w := request("GET", "/admin", "", cookie, ""); w.Code != 200 {
		t.Fatalf("admin role was not mapped: %d", w.Code)
	}
	if w := request("POST", "/logout", form, cookie, "https://app.example"); w.Code != 303 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := request("GET", "/admin", "", cookie, ""); w.Code != 401 {
		t.Fatalf("session remained usable: %d", w.Code)
	}
	login = begin()
	now = now.Add(loginTTL)
	path = "/auth/callback?code=fixture-code&state=" + url.QueryEscape(login.Value)
	if w := request("GET", path, "", login, ""); w.Code != 401 {
		t.Fatalf("expired login state: %d", w.Code)
	}
}
