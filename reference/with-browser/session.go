package main

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/birdordev/plumego/security/authn"
	"golang.org/x/oauth2"
)

const (
	loginCookie        = "plumego_login"
	sessionCookie      = "plumego_session"
	loginTTL           = 5 * time.Minute
	sessionTTL         = time.Hour
	maxSessionLifetime = 8 * time.Hour
	maxBrowserEntries  = 4096
)

type sessionAction int

const (
	updateProfile sessionAction = iota
	renewSession
	endSession
)

type pendingLogin struct {
	verifier string
	nonce    string
	expiry   time.Time
}

type browserSession struct {
	principal      authn.Principal
	csrf           string
	name           string
	expiry         time.Time
	absoluteExpiry time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	now      func() time.Time
	logins   map[string]pendingLogin
	sessions map[string]browserSession
}

func newSessions(now func() time.Time) *sessionStore {
	return &sessionStore{now: now, logins: make(map[string]pendingLogin), sessions: make(map[string]browserSession)}
}

func (s *sessionStore) beginLogin() (string, pendingLogin, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range s.logins {
		if !now.Before(value.expiry) {
			delete(s.logins, key)
		}
	}
	if len(s.logins) >= maxBrowserEntries {
		return "", pendingLogin{}, false
	}
	state := rand.Text()
	login := pendingLogin{verifier: oauth2.GenerateVerifier(), nonce: rand.Text(), expiry: now.Add(loginTTL)}
	s.logins[state] = login
	return state, login, true
}

func (s *sessionStore) consume(state string, r *http.Request) (pendingLogin, bool) {
	cookie, err := r.Cookie(loginCookie)
	if err != nil || state == "" || !equalToken(cookie.Value, state) {
		return pendingLogin{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.logins[state]
	delete(s.logins, state)
	return login, ok && s.now().Before(login.expiry)
}

func (s *sessionStore) establish(principal authn.Principal, r *http.Request) (string, bool) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range s.sessions {
		if !now.Before(value.expiry) {
			delete(s.sessions, key)
		}
	}
	if old, err := r.Cookie(sessionCookie); err == nil {
		delete(s.sessions, old.Value)
	}
	if len(s.sessions) >= maxBrowserEntries {
		return "", false
	}
	key := rand.Text()
	s.sessions[key] = browserSession{
		principal: principal, csrf: rand.Text(), expiry: now.Add(sessionTTL),
		absoluteExpiry: now.Add(maxSessionLifetime),
	}
	return key, true
}

func (s *sessionStore) get(r *http.Request) (string, browserSession, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", browserSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[cookie.Value]
	if !ok || !s.now().Before(session.expiry) {
		delete(s.sessions, cookie.Value)
		return "", browserSession{}, false
	}
	return cookie.Value, session, true
}

func (s *sessionStore) apply(key, csrf, name string, action sessionAction) (string, time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	now := s.now()
	if !ok || !now.Before(session.expiry) || !equalToken(session.csrf, csrf) {
		return "", 0, false
	}
	switch action {
	case endSession:
		delete(s.sessions, key)
	case updateProfile:
		session.name = name
		s.sessions[key] = session
	case renewSession:
		expiry := now.Add(sessionTTL)
		if session.absoluteExpiry.Before(expiry) {
			expiry = session.absoluteExpiry
		}
		session.expiry, session.csrf = expiry, rand.Text()
		replacement := rand.Text()
		delete(s.sessions, key)
		s.sessions[replacement] = session
		return replacement, expiry.Sub(now), true
	default:
		return "", 0, false
	}
	return "", 0, true
}

func (s *sessionStore) Authenticate(r *http.Request) (*authn.Principal, error) {
	_, session, ok := s.get(r)
	if !ok {
		return nil, errors.New("browser session required")
	}
	principal := session.principal
	principal.Roles = slices.Clone(principal.Roles)
	return &principal, nil
}

func equalToken(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
