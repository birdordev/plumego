package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

type Config struct {
	Endpoint          string   `json:"endpoint"`
	CA                string   `json:"ca_pem"`
	Token             string   `json:"bearer_token"`
	IdentityNamespace string   `json:"identity_namespace"`
	IdentityUID       string   `json:"identity_uid"`
	Namespaces        []string `json:"namespaces"`
}

type Client struct {
	client   *http.Client
	endpoint string
	config   Config
}

const maxNamespaces = 32

var bearerToken = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

func NewClient(cfg Config) (*Client, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || strings.Contains(cfg.Endpoint, "#") || !validEndpoint(endpoint) || len(cfg.Token) > 8192 || !bearerToken.MatchString(cfg.Token) ||
		!provider.ValidWorkloadNamespace(cfg.IdentityNamespace) || !provider.ValidWorkloadUID(cfg.IdentityUID) ||
		len(cfg.Namespaces) == 0 || len(cfg.Namespaces) > maxNamespaces {
		return nil, errors.New("invalid Kubernetes workload profile")
	}
	namespaces := make(map[string]bool, len(cfg.Namespaces))
	for _, namespace := range cfg.Namespaces {
		if !provider.ValidWorkloadNamespace(namespace) || namespaces[namespace] {
			return nil, errors.New("invalid Kubernetes workload namespaces")
		}
		namespaces[namespace] = true
	}
	roots := x509.NewCertPool()
	if len(cfg.CA) > 24<<10 || !roots.AppendCertsFromPEM([]byte(cfg.CA)) {
		return nil, errors.New("invalid Kubernetes workload CA")
	}
	cfg.Namespaces = append([]string(nil), cfg.Namespaces...)
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext, ForceAttemptHTTP2: true,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
			TLSHandshakeTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
		},
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Client{client: client, endpoint: strings.TrimSuffix(endpoint.String(), "/"), config: cfg}, nil
}

func validEndpoint(u *url.URL) bool {
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		u.RawPath != "" || (u.Path != "" && u.Path != "/") || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if p := u.Port(); p != "" {
		port, err := strconv.ParseUint(p, 10, 16)
		if err != nil || port == 0 {
			return false
		}
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(strings.ToLower(host), ".") {
		if !provider.ValidWorkloadNamespace(label) {
			return false
		}
	}
	return true
}

func (c *Client) CloseIdleConnections() { c.client.CloseIdleConnections() }

func (c *Client) openJSON(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	return c.sendJSON(ctx, http.MethodGet, path, query, nil)
}

func (c *Client) sendJSON(ctx context.Context, method, path string, query url.Values, body []byte) (*http.Response, error) {
	target := c.endpoint + path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	var input io.Reader
	if body != nil {
		input = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, input)
	if err != nil {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.GetBody = nil
	}
	request.Header.Set("Authorization", "Bearer "+c.config.Token)
	resp, err := c.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &provider.Failure{Kind: provider.Transient}
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		if method == http.MethodPut && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusConflict ||
			resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusBadRequest) {
			return nil, &provider.Failure{Kind: provider.PreconditionFailed}
		}
		return nil, responseFailure(resp)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		_ = resp.Body.Close()
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return resp, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, limit int64, budget *observationBudget, result any) error {
	resp, err := c.openJSON(ctx, path, query)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	reader := &io.LimitedReader{R: budget.reader(resp.Body), N: limit + 1}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(result); err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF || reader.N == 0 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func responseFailure(response *http.Response) error {
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &provider.Failure{Kind: provider.Unauthorized}
	case http.StatusNotFound:
		return &provider.Failure{Kind: provider.Absent}
	case http.StatusGone:
		return &provider.Failure{Kind: provider.ExpiredCursor}
	case http.StatusTooManyRequests:
		raw := response.Header.Get("Retry-After")
		var delay time.Duration
		if seconds, err := strconv.ParseUint(raw, 10, 32); err == nil {
			delay = time.Duration(min(seconds, 86400)) * time.Second
		} else if when, err := http.ParseTime(raw); err == nil {
			delay = max(0, min(time.Until(when), 24*time.Hour))
		}
		return &provider.Failure{Kind: provider.Throttled, RetryAfter: delay}
	}
	if response.StatusCode >= 500 {
		return &provider.Failure{Kind: provider.Transient}
	}
	return &provider.Failure{Kind: provider.InvalidResponse}
}
