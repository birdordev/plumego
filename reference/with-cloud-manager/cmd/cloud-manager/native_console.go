package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"io"
	stdlog "log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/nativeconsole"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/awsec2"
	"with-cloud-manager/internal/provider/openstack"
)

const nativeRoute = "/v1/tenants/:tenant/projects/:project/console-grants"

type nativeConnector func(context.Context, nativeconsole.Session) (*url.URL, error)
type nativeSerialConnector func(context.Context, nativeconsole.Session, string) (provider.SerialDispatch, error)

func configureNativeConsole(cfg *consoleConfig, manifest string, stopped <-chan struct{}) error {
	if manifest == "" {
		return nil
	}
	if cfg == nil {
		return errors.New("native console requires the browser console")
	}
	var profiles map[string]string
	if err := loadPrivateProfile(manifest, &profiles); err != nil {
		return err
	}
	if len(profiles) == 0 {
		return errors.New("native console needs a credential reference map")
	}
	cfg.nativeStopped = stopped
	cfg.nativeSerial = func(ctx context.Context, session nativeconsole.Session, key string) (provider.SerialDispatch, error) {
		binding := session.Binding
		path := profiles[binding.CredentialRef]
		if path == "" || binding.Provider != awsec2.Name {
			return nil, nativeconsole.ErrUnsupported
		}
		var profile awsProfile
		if err := loadPrivateProfile(path, &profile); err != nil {
			return nil, err
		}
		if profile.Ref != binding.CredentialRef || profile.SerialConsoleHost == "" {
			return nil, identity.ErrForbidden
		}
		client, err := awsec2.Connect(ctx, profile.Config)
		if err != nil {
			return nil, err
		}
		if client.Identity() != binding.RemoteIdentity {
			return nil, identity.ErrForbidden
		}
		scope := provider.Scope{Provider: binding.Provider, RemoteIdentity: binding.RemoteIdentity, Region: session.Region, Kind: provider.Instance}
		return client.PrepareSerialConsole(ctx, scope, session.AssetID, key)
	}
	cfg.nativeConnect = func(ctx context.Context, session nativeconsole.Session) (*url.URL, error) {
		binding := session.Binding
		path := profiles[binding.CredentialRef]
		if path == "" || binding.Provider != openstack.Name {
			return nil, nativeconsole.ErrUnsupported
		}
		var profile openStackProfile
		if err := loadPrivateProfile(path, &profile); err != nil {
			return nil, err
		}
		if profile.Ref != binding.CredentialRef || profile.ConsoleProxyURL == "" {
			return nil, identity.ErrForbidden
		}
		client, err := openstack.Connect(ctx, profile.Config, nil)
		if err != nil {
			return nil, err
		}
		if client.Identity() != binding.RemoteIdentity {
			return nil, identity.ErrForbidden
		}
		scope := provider.Scope{Provider: binding.Provider, RemoteIdentity: binding.RemoteIdentity, Region: session.Region, Kind: provider.Instance}
		return client.OpenNoVNC(ctx, scope, session.AssetID)
	}
	return nil
}

func registerNativeConsole(app *core.App, cfg consoleConfig, store nativeconsole.Store) error {
	h := nativeHTTP{store: store, logger: app.Logger(), cfg: cfg}
	err := errors.Join(
		app.Get(nativeRoute, h.endpoint(h.list)),
		app.Post(nativeRoute, h.endpoint(h.request)),
		app.Get(nativeRoute+"/:grant", h.endpoint(h.detail)),
		app.Post(nativeRoute+"/:grant/approval", h.endpoint(h.decide)),
		app.Post(nativeRoute+"/:grant/revoke", h.endpoint(h.revoke)),
	)
	if cfg.nativeConnect != nil {
		err = errors.Join(err, app.Get(nativeRoute+"/:grant/stream", http.HandlerFunc(h.stream)))
	}
	if cfg.nativeSerial != nil {
		err = errors.Join(err, app.Post(nativeRoute+"/:grant/serial-key", h.timedEndpoint(20*time.Second, h.activateSerial)))
	}
	return err
}

type nativeHTTP struct {
	store  nativeconsole.Store
	logger plumelog.StructuredLogger
	cfg    consoleConfig
}

func nativeScope(r *http.Request) identity.Scope {
	return identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
}

func (h nativeHTTP) endpoint(f func(context.Context, http.ResponseWriter, *http.Request, string, identity.Scope)) http.Handler {
	return h.timedEndpoint(3*time.Second, f)
}

func (h nativeHTTP) timedEndpoint(timeout time.Duration, f func(context.Context, http.ResponseWriter, *http.Request, string, identity.Scope)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeNativeError(w, r, identity.ErrUnauthenticated, h.logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		f(ctx, w, r, token, nativeScope(r))
	})
}

func (h nativeHTTP) list(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeNativeError(w, r, nativeconsole.ErrInvalid, h.logger)
		return
	}
	limit, after := 50, ""
	for field, values := range q {
		if len(values) != 1 {
			err = nativeconsole.ErrInvalid
			break
		}
		switch field {
		case "after":
			after = values[0]
		case "limit":
			limit, err = strconv.Atoi(values[0])
		default:
			err = nativeconsole.ErrInvalid
		}
		if err != nil {
			break
		}
	}
	if err != nil {
		writeNativeError(w, r, nativeconsole.ErrInvalid, h.logger)
		return
	}
	page, err := h.store.List(ctx, token, scope, after, limit)
	h.ok(w, r, page, err)
}

func (h nativeHTTP) request(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	match, ok := accountRevision(r)
	keys := r.Header.Values("Idempotency-Key")
	if !ok {
		writeNativeError(w, r, nativeconsole.ErrStale, h.logger)
		return
	}
	if len(keys) != 1 {
		writeNativeError(w, r, nativeconsole.ErrInvalid, h.logger)
		return
	}
	var input nativeconsole.Request
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 8192}); err != nil {
		writeNativeError(w, r, err, h.logger)
		return
	}
	if (input.SSHPublicKey != "" && h.cfg.nativeSerial == nil) || (input.SSHPublicKey == "" && h.cfg.nativeConnect == nil) {
		writeNativeError(w, r, nativeconsole.ErrUnsupported, h.logger)
		return
	}
	item, err := h.store.Request(ctx, token, scope, keys[0], match, input)
	if err != nil {
		writeNativeError(w, r, err, h.logger)
		return
	}
	w.Header().Set("Location", r.URL.EscapedPath()+"/"+item.ID)
	setGrantETag(w, item)
	if err := contract.WriteResponse(w, r, http.StatusCreated, item, nil); err != nil {
		h.logger.Warn("native console response write failed", plumelog.Fields{})
	}
}

func (h nativeHTTP) detail(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	item, err := h.store.Get(ctx, token, scope, router.Param(r, "grant"))
	h.grant(w, r, item, err)
}

func (h nativeHTTP) decide(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	match, ok := accountRevision(r)
	if !ok {
		writeNativeError(w, r, nativeconsole.ErrStale, h.logger)
		return
	}
	var input struct {
		Decision string `json:"decision"`
	}
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 4096}); err != nil {
		writeNativeError(w, r, err, h.logger)
		return
	}
	item, err := h.store.Decide(ctx, token, scope, router.Param(r, "grant"), match, input.Decision)
	h.grant(w, r, item, err)
}

func (h nativeHTTP) revoke(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope) {
	match, ok := accountRevision(r)
	if !ok {
		writeNativeError(w, r, nativeconsole.ErrStale, h.logger)
		return
	}
	if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeNativeError(w, r, nativeconsole.ErrInvalid, h.logger)
		return
	}
	item, err := h.store.Revoke(ctx, token, scope, router.Param(r, "grant"), match)
	h.grant(w, r, item, err)
}

func setGrantETag(w http.ResponseWriter, item nativeconsole.Grant) {
	w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(item.Revision, 10)))
}

func (h nativeHTTP) grant(w http.ResponseWriter, r *http.Request, item nativeconsole.Grant, err error) {
	if err == nil {
		setGrantETag(w, item)
	}
	h.ok(w, r, item, err)
}

func (h nativeHTTP) ok(w http.ResponseWriter, r *http.Request, item any, err error) {
	if err != nil {
		writeNativeError(w, r, err, h.logger)
		return
	}
	if err := contract.WriteResponse(w, r, http.StatusOK, item, nil); err != nil {
		h.logger.Warn("native console response write failed", plumelog.Fields{})
	}
}

func writeNativeError(w http.ResponseWriter, r *http.Request, err error, logger plumelog.StructuredLogger) {
	if _, ok := contract.AsAPIError(err); ok {
		writeAccountError(w, r, err, logger)
		return
	}
	var kind contract.ErrorType
	var message string
	switch {
	case errors.Is(err, nativeconsole.ErrInvalid):
		kind, message = contract.TypeBadRequest, "invalid console grant request"
	case errors.Is(err, nativeconsole.ErrConflict):
		kind, message = contract.TypeConflict, "console grant conflicts with the existing request"
	case errors.Is(err, nativeconsole.ErrStale):
		kind, message = contract.TypePreconditionFailed, "current and unexpired console grant required"
	case errors.Is(err, nativeconsole.ErrUnsupported):
		kind, message = contract.TypeForbidden, "console access is unavailable for this target"
	case errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrUnauthenticated):
		writeAccountError(w, r, err, logger)
		return
	default:
		kind, message = contract.TypeUnavailable, "instance console is unavailable"
	}
	writeAccountError(w, r, contract.NewErrorBuilder().Type(kind).Message(message).Build(), logger)
}

func validNativeUpgrade(r *http.Request) bool {
	if r.Method != http.MethodGet || r.ProtoMajor != 1 || r.URL.RawQuery != "" || len(r.Header.Values("Upgrade")) != 1 ||
		!strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" ||
		len(r.Header.Values("Sec-WebSocket-Key")) != 1 {
		return false
	}
	key, err := base64.StdEncoding.Strict().DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	if err != nil || len(key) != 16 {
		return false
	}
	for _, header := range r.Header.Values("Connection") {
		for field := range strings.SplitSeq(header, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "upgrade") {
				return true
			}
		}
	}
	return false
}

func (h nativeHTTP) stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, browser := identity.BrowserSessionFromContext(r.Context())
	if !browser || !h.cfg.sameOrigin(r) {
		writeNativeError(w, r, identity.ErrForbidden, h.logger)
		return
	}
	if !validNativeUpgrade(r) {
		writeNativeError(w, r, nativeconsole.ErrInvalid, h.logger)
		return
	}
	scope, id := nativeScope(r), router.Param(r, "grant")
	claimCtx, stop := context.WithTimeout(r.Context(), 3*time.Second)
	session, err := h.store.Claim(claimCtx, scope, id)
	stop()
	if err != nil {
		writeNativeError(w, r, err, h.logger)
		return
	}
	connected := false
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		if err := h.store.Finish(cleanup, scope, id, connected); err != nil {
			h.logger.Warn("native console closure record failed", plumelog.Fields{})
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), nativeconsole.SessionLimit)
	defer cancel()
	go h.monitor(ctx, cancel, scope, id)
	dial, stop := context.WithTimeout(ctx, 10*time.Second)
	target, err := h.cfg.nativeConnect(dial, session)
	stop()
	if err == nil {
		err = h.check(ctx, scope, id)
	}
	if err != nil || target == nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Fragment != "" {
		writeNativeError(w, r, err, h.logger)
		return
	}
	h.proxy(target, r, &connected).ServeHTTP(w, r.WithContext(ctx))
}

func (h nativeHTTP) check(ctx context.Context, scope identity.Scope, id string) error {
	check, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return h.store.Check(check, scope, id)
}

func (h nativeHTTP) monitor(ctx context.Context, cancel context.CancelFunc, scope identity.Scope, id string) {
	timer := time.NewTicker(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.cfg.nativeStopped:
			cancel()
			return
		case <-timer.C:
			if err := h.check(ctx, scope, id); err != nil {
				cancel()
				return
			}
		}
	}
}

func (h nativeHTTP) proxy(target *url.URL, r *http.Request, connected *bool) *httputil.ReverseProxy {
	key := r.Header.Get("Sec-WebSocket-Key")
	protocol := r.Header.Get("Sec-WebSocket-Protocol") == "binary"
	// RFC 6455 规定使用 SHA-1 计算 WebSocket 握手应答。
	challenge := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(challenge[:])
	return &httputil.ReverseProxy{
		Transport: h.cfg.nativeTransport,
		ErrorLog:  stdlog.New(io.Discard, "", 0),
		Rewrite: func(req *httputil.ProxyRequest) {
			u := *target
			req.Out.URL = &u
			req.Out.Host = u.Host
			req.Out.Header = make(http.Header)
			req.Out.Header.Set("Connection", "Upgrade")
			req.Out.Header.Set("Upgrade", "websocket")
			req.Out.Header.Set("Origin", u.Scheme+"://"+u.Host)
			req.Out.Header.Set("Sec-WebSocket-Key", key)
			req.Out.Header.Set("Sec-WebSocket-Version", "13")
			if protocol {
				req.Out.Header.Set("Sec-WebSocket-Protocol", "binary")
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			selected := resp.Header.Get("Sec-WebSocket-Protocol")
			if resp.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") ||
				resp.Header.Get("Sec-WebSocket-Accept") != accept || (selected != "" && (!protocol || selected != "binary")) {
				return errors.New("upstream console did not upgrade")
			}
			resp.Status = "101 Switching Protocols"
			resp.Header = make(http.Header)
			resp.Header.Set("Connection", "Upgrade")
			resp.Header.Set("Upgrade", "websocket")
			resp.Header.Set("Sec-WebSocket-Accept", accept)
			if selected == "binary" {
				resp.Header.Set("Sec-WebSocket-Protocol", "binary")
			}
			*connected = true
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeNativeError(w, r, err, h.logger)
		},
	}
}
