package consolehttp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
)

const ConsoleCookie = "__Host-cmp_session"
const ConsoleCSRFHeader = "X-Console-CSRF"
const ConsoleSubjectHeader = "X-CMP-Subject"

type ConsoleConfig struct {
	Origin, issuer, proxyDNS string
	CertFile, KeyFile        string
	clientCAs                *x509.CertPool
	NativeConnect            nativeConnector
	NativeSerial             nativeSerialConnector
	NativeTransport          http.RoundTripper
	nativeStopped            <-chan struct{}
}

func LoadConsoleConfig(getenv func(string) string) (*ConsoleConfig, error) {
	rawOrigin := getenv("CLOUD_MANAGER_CONSOLE_ORIGIN")
	issuer := getenv("CLOUD_MANAGER_CONSOLE_ISSUER")
	proxyDNS := getenv("CLOUD_MANAGER_CONSOLE_PROXY_DNS")
	caFile := getenv("CLOUD_MANAGER_CONSOLE_PROXY_CA")
	cert := getenv("CLOUD_MANAGER_CONSOLE_CERT")
	key := getenv("CLOUD_MANAGER_CONSOLE_KEY")
	if rawOrigin == "" && issuer == "" && proxyDNS == "" && caFile == "" && cert == "" && key == "" {
		return nil, nil
	}
	origin, err := projecthttp.BaseURL(rawOrigin)
	if err != nil || origin.Scheme != "https" || (origin.Path != "" && origin.Path != "/") {
		return nil, errors.New("CLOUD_MANAGER_CONSOLE_ORIGIN must be an HTTPS origin")
	}
	issuerURL, err := projecthttp.BaseURL(issuer)
	if err != nil || issuerURL.Scheme != "https" {
		return nil, errors.New("CLOUD_MANAGER_CONSOLE_ISSUER must be an HTTPS issuer")
	}
	if !consoleIdentityValue(proxyDNS) || strings.ContainsAny(proxyDNS, "*/:") || cert == "" || key == "" || caFile == "" {
		return nil, errors.New("a console proxy DNS identity, CA and server TLS certificate are required")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return nil, errors.New("cannot read console proxy CA")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid console proxy CA")
	}
	return &ConsoleConfig{Origin: strings.TrimRight(origin.String(), "/"), issuer: issuer,
		proxyDNS: proxyDNS, CertFile: cert, KeyFile: key, clientCAs: roots}, nil
}

func (c ConsoleConfig) ConfigureTLS(app *core.App) error {
	if err := app.Prepare(); err != nil {
		return err
	}
	server, err := app.Server()
	if err != nil {
		return err
	}
	server.TLSConfig.ClientAuth = tls.VerifyClientCertIfGiven
	server.TLSConfig.ClientCAs = c.clientCAs
	return nil
}

func consoleCredential(r *http.Request) string {
	cookies := r.CookiesNamed(ConsoleCookie)
	if len(cookies) == 1 && identity.ValidBrowserCredential(cookies[0].Value) {
		return cookies[0].Value
	}
	return ""
}

func ConsoleCSRF(credential string) string {
	digest := sha256.Sum256([]byte("cmp-browser-csrf\x00" + credential))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (c ConsoleConfig) sameOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	return len(origins) == 1 && origins[0] == c.Origin
}

func (c ConsoleConfig) validWrite(r *http.Request, credential string) bool {
	tokens := r.Header.Values(ConsoleCSRFHeader)
	return c.sameOrigin(r) && len(tokens) == 1 &&
		subtle.ConstantTimeCompare([]byte(tokens[0]), []byte(ConsoleCSRF(credential))) == 1
}

func (c ConsoleConfig) apiSession(logger plumelog.StructuredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/v1/") || len(r.Header.Values("Authorization")) != 0 {
				next.ServeHTTP(w, r)
				return
			}
			credential := consoleCredential(r)
			if credential != "" {
				if r.Method != http.MethodGet && r.Method != http.MethodHead && !c.validWrite(r, credential) {
					w.Header().Set("Cache-Control", "no-store")
					transport.WriteAccountError(w, r, identity.ErrForbidden, logger)
					return
				}
				r = r.WithContext(identity.WithBrowserSession(r.Context(), credential))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func consoleIdentityValue(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && !strings.Contains(value, ",") &&
		strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}

func (c ConsoleConfig) gatewaySubject(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 ||
		!slices.Contains(r.TLS.PeerCertificates[0].DNSNames, c.proxyDNS) {
		return ""
	}
	subject := r.Header.Values(ConsoleSubjectHeader)
	if len(subject) != 1 || !consoleIdentityValue(subject[0]) {
		return ""
	}
	return subject[0]
}

func consoleSetCookie(w http.ResponseWriter, credential string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: ConsoleCookie, Value: credential, Path: "/", MaxAge: maxAge,
		Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func (c ConsoleConfig) login(sessions identity.BrowserSessions, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		subject := c.gatewaySubject(r)
		if subject == "" || !c.sameOrigin(r) {
			transport.WriteAccountError(w, r, identity.ErrForbidden, logger)
			return
		}
		var input struct {
			Tenant string `json:"tenant"`
		}
		if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 2048}); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		if !consoleIdentityValue(input.Tenant) {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		credential, err := sessions.Login(ctx, input.Tenant, c.issuer, subject, consoleCredential(r))
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		consoleSetCookie(w, credential, int(identity.BrowserSessionTTL/time.Second))
		if err := contract.WriteResponse(w, r, http.StatusOK, map[string]bool{"authenticated": true}, nil); err != nil {
			logger.Warn("console login response write failed", plumelog.Fields{})
		}
	})
}

func consoleProfile(sessions identity.BrowserSessions, cfg ConsoleConfig, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		credential := consoleCredential(r)
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		profile, err := sessions.Profile(ctx, credential)
		if err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		result := struct {
			identity.BrowserProfile
			CSRF          string `json:"csrf"`
			NativeConsole bool   `json:"native_console,omitempty"`
			NativeSerial  bool   `json:"native_serial_console,omitempty"`
		}{profile, ConsoleCSRF(credential), cfg.NativeConnect != nil, cfg.NativeSerial != nil}
		if err := contract.WriteResponse(w, r, http.StatusOK, result, nil); err != nil {
			logger.Warn("console profile response write failed", plumelog.Fields{})
		}
	})
}

func (c ConsoleConfig) logout(sessions identity.BrowserSessions, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		credential := consoleCredential(r)
		if credential == "" || !c.validWrite(r, credential) {
			transport.WriteAccountError(w, r, identity.ErrForbidden, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := sessions.Logout(ctx, credential); err != nil {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		consoleSetCookie(w, "", -1)
		if err := contract.WriteResponse(w, r, http.StatusOK, map[string]bool{"authenticated": false}, nil); err != nil {
			logger.Warn("console logout response write failed", plumelog.Fields{})
		}
	})
}
