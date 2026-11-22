package main

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	"github.com/birdordev/plumego/x/validate"
)

type browser struct {
	origin   *url.URL
	provider loginProvider
	sessions *sessionStore
	page     *template.Template
}

type pageData struct {
	Authenticated bool
	Admin         bool
	CSRF          string
	Name          string
	Error         string
}

func newBrowser(address string, origin *url.URL, provider loginProvider, sessions *sessionStore) (*core.App, error) {
	page, err := template.New("home").Parse(pageHTML)
	if err != nil {
		return nil, err
	}
	h := browser{origin: origin, provider: provider, sessions: sessions, page: page}
	cfg := core.DefaultConfig()
	cfg.Addr = address
	cfg.Router.MethodNotAllowed = true
	app := core.New(cfg, core.AppDependencies{})
	if err := app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; form-action 'self'; base-uri 'none'")
			next.ServeHTTP(w, r)
		})
	}, http.NewCrossOriginProtection().Handler); err != nil {
		return nil, err
	}
	for _, route := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{http.MethodGet, "/", h.home},
		{http.MethodPost, "/login", h.login},
		{http.MethodGet, "/auth/callback", h.callback},
		{http.MethodPost, "/profile", h.profile},
		{http.MethodPost, "/session/renew", h.renew},
		{http.MethodPost, "/logout", h.logout},
		{http.MethodGet, "/admin", h.admin},
	} {
		if err := app.AddRoute(route.method, route.path, route.handler); err != nil {
			return nil, err
		}
	}
	return app, nil
}

func (h browser) cookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: h.origin.Scheme == "https", SameSite: http.SameSiteLaxMode})
}

func (h browser) login(w http.ResponseWriter, r *http.Request) {
	state, login, ok := h.sessions.beginLogin()
	if !ok {
		http.Error(w, "Login is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	h.cookie(w, loginCookie, state, int(loginTTL/time.Second))
	http.Redirect(w, r, h.provider.authorizationURL(state, login.nonce, login.verifier), http.StatusSeeOther)
}

func (h browser) callback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	login, ok := h.sessions.consume(query.Get("state"), r)
	h.cookie(w, loginCookie, "", -1)
	if !ok || query.Get("code") == "" {
		http.Error(w, "Login could not be verified", http.StatusUnauthorized)
		return
	}
	verifyCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	principal, err := h.provider.identity(verifyCtx, query.Get("code"), login.verifier, login.nonce)
	if r.Context().Err() != nil {
		return
	}
	if errors.Is(verifyCtx.Err(), context.DeadlineExceeded) {
		http.Error(w, "Login verification timed out", http.StatusServiceUnavailable)
		return
	}
	if err != nil {
		http.Error(w, "Login could not be verified", http.StatusUnauthorized)
		return
	}
	key, ok := h.sessions.establish(principal, r)
	if !ok {
		http.Error(w, "Login is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	h.cookie(w, sessionCookie, key, int(sessionTTL/time.Second))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h browser) home(w http.ResponseWriter, r *http.Request) {
	_, session, ok := h.sessions.get(r)
	h.render(w, http.StatusOK, pageData{
		Authenticated: ok, Admin: slices.Contains(session.principal.Roles, "admin"), CSRF: session.csrf, Name: session.name,
	})
}

func (h browser) profile(w http.ResponseWriter, r *http.Request) {
	h.form(w, r, updateProfile)
}

func (h browser) renew(w http.ResponseWriter, r *http.Request) {
	h.form(w, r, renewSession)
}

func (h browser) logout(w http.ResponseWriter, r *http.Request) {
	h.form(w, r, endSession)
}

func (h browser) form(w http.ResponseWriter, r *http.Request, action sessionAction) {
	key, session, ok := h.sessions.get(r)
	if !ok {
		http.Error(w, "Sign in required", http.StatusUnauthorized)
		return
	}
	var csrf, name string
	if err := validate.BindForm(w, r, 4096, func(values url.Values) error {
		csrf = values.Get("csrf")
		name = strings.TrimSpace(values.Get("name"))
		return nil
	}); err != nil {
		status := http.StatusBadRequest
		if apiErr, ok := contract.AsAPIError(err); ok {
			status = apiErr.Status()
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	if !equalToken(session.csrf, csrf) {
		http.Error(w, "Invalid form token", http.StatusForbidden)
		return
	}
	if action == updateProfile && (name == "" || len(name) > 80) {
		h.render(w, http.StatusBadRequest, pageData{
			Authenticated: true, Admin: slices.Contains(session.principal.Roles, "admin"),
			CSRF: session.csrf, Name: name, Error: "Enter a display name up to 80 bytes.",
		})
		return
	}
	replacement, ttl, ok := h.sessions.apply(key, csrf, name, action)
	if !ok {
		http.Error(w, "Sign in required", http.StatusUnauthorized)
		return
	}
	switch action {
	case endSession:
		h.cookie(w, sessionCookie, "", -1)
	case renewSession:
		h.cookie(w, sessionCookie, replacement, int((ttl+time.Second-1)/time.Second))
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h browser) admin(w http.ResponseWriter, r *http.Request) {
	principal, err := h.sessions.Authenticate(r)
	if err != nil {
		http.Error(w, "Sign in required", http.StatusUnauthorized)
		return
	}
	if !slices.Contains(principal.Roles, "admin") {
		http.Error(w, "Administrator role required", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Administrator access confirmed."))
}

func (h browser) render(w http.ResponseWriter, status int, data pageData) {
	var output bytes.Buffer
	if err := h.page.Execute(&output, data); err != nil {
		http.Error(w, "Page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(output.Bytes())
}

const pageHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Browser session</title></head>
<body><main>
<h1>Browser session</h1>
{{if .Authenticated}}
{{if .Admin}}<p><a href="/admin">Administrator access</a></p>{{end}}
{{if .Error}}<p role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/profile">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <label for="name">Display name</label>
  <input type="text" id="name" name="name" value="{{.Name}}" maxlength="80" required>
  <button type="submit">Save</button>
</form>
<form method="post" action="/session/renew">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <button type="submit">Extend session</button>
</form>
<form method="post" action="/logout">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <button type="submit">Sign out</button>
</form>
{{else}}
<form method="post" action="/login"><button type="submit">Sign in with OpenID Connect</button></form>
{{end}}
</main></body></html>`
