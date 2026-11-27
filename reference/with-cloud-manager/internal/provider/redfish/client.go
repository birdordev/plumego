package redfish

import (
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
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const rootPath = "/redfish/v1/"
const maxDocument = 2 << 20

var uuid = regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`)
var siteID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Config struct {
	Endpoint   string `json:"endpoint"`
	UUID       string `json:"service_uuid"`
	Site       string `json:"site"`
	User       string `json:"username"`
	Password   string `json:"password"`
	CAPEM      string `json:"ca_pem,omitempty"`
	AllowPXE   bool   `json:"allow_pxe,omitempty"`
	AllowPower bool   `json:"allow_power,omitempty"`
}

type link struct {
	ID string `json:"@odata.id"`
}

type Client struct {
	config         Config
	ownedTransport bool
	base           *url.URL
	http           http.Client
	systems        string
}

func Connect(ctx context.Context, cfg Config, transport http.RoundTripper) (*Client, error) {
	base, err := url.Parse(cfg.Endpoint)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" || base.User != nil ||
		base.Opaque != "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" ||
		base.RawPath != "" || (base.Path != "" && base.Path != "/") ||
		!validUUID(cfg.UUID) || !siteID.MatchString(cfg.Site) ||
		!credential(cfg.User, 256) || strings.Contains(cfg.User, ":") || !credential(cfg.Password, 4096) {
		return nil, errors.New("invalid Redfish profile")
	}
	owned := transport == nil
	if owned {
		transport, err = newTransport(cfg.CAPEM)
		if err != nil {
			return nil, err
		}
	}
	c := &Client{config: cfg, base: base, ownedTransport: owned, http: http.Client{
		Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	connected := false
	defer func() {
		if !connected {
			c.Close()
		}
	}()
	var root struct {
		UUID    string `json:"UUID"`
		Systems link   `json:"Systems"`
	}
	if err := c.get(ctx, &url.URL{Path: rootPath}, &root); err != nil {
		return nil, err
	}
	if strings.ToLower(root.UUID) != cfg.UUID {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	systems, err := relativeLink(root.Systems.ID)
	if err != nil || systems.RawQuery != "" {
		return nil, invalidResponse()
	}
	c.systems = strings.TrimSuffix(systems.Path, "/")
	connected = true
	return c, nil
}

func (c *Client) Identity() string { return c.config.Identity() }

func (cfg Config) Identity() string { return provider.Redfish + ":" + cfg.UUID + ":" + cfg.Site }

func validUUID(value string) bool {
	return value != "00000000-0000-0000-0000-000000000000" && uuid.MatchString(value)
}

func credential(value string, limit int) bool {
	return value != "" && len(value) <= limit && !strings.ContainsAny(value, "\x00\r\n")
}

func newTransport(ca string) (*http.Transport, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if ca != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			return nil, errors.New("invalid Redfish CA bundle")
		}
		config.RootCAs = roots
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: config,
		TLSHandshakeTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second,
		MaxIdleConnsPerHost: 2, ForceAttemptHTTP2: true}, nil
}

func relativeLink(value string) (*url.URL, error) {
	target, err := url.Parse(value)
	if err != nil || len(value) > 2048 || target.IsAbs() || target.Host != "" || target.User != nil ||
		target.Opaque != "" || target.Fragment != "" || strings.Contains(target.EscapedPath(), "%") || target.ForceQuery ||
		!strings.HasPrefix(target.Path, rootPath) || target.Path == rootPath || strings.Contains(target.Path, "\\") {
		return nil, invalidResponse()
	}
	for _, segment := range strings.Split(strings.TrimSuffix(target.Path, "/")[len(rootPath):], "/") {
		if segment == "" || segment == "." || segment == ".." {
			return nil, invalidResponse()
		}
	}
	return target, nil
}

func invalidResponse() error { return &provider.Failure{Kind: provider.InvalidResponse} }

func (c *Client) get(ctx context.Context, target *url.URL, value any) error {
	_, err := c.getDocument(ctx, target, value)
	return err
}

func (c *Client) getDocument(ctx context.Context, target *url.URL, value any) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.ResolveReference(target).String(), nil)
	if err != nil {
		return "", invalidResponse()
	}
	if target.Path != rootPath {
		request.SetBasicAuth(c.config.User, c.config.Password)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &provider.Failure{Kind: provider.Transient}
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", &provider.Failure{Kind: provider.Unauthorized}
	case http.StatusTooManyRequests:
		return "", &provider.Failure{Kind: provider.Throttled}
	default:
		if response.StatusCode >= 500 {
			return "", &provider.Failure{Kind: provider.Transient}
		}
		return "", invalidResponse()
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return "", invalidResponse()
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDocument+1))
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &provider.Failure{Kind: provider.Transient}
	}
	if len(data) > maxDocument || json.Unmarshal(data, value) != nil {
		return "", invalidResponse()
	}
	etags := response.Header.Values("ETag")
	if len(etags) == 1 {
		return etags[0], nil
	}
	return "", nil
}

func (c *Client) Close() {
	if c.ownedTransport {
		c.http.CloseIdleConnections()
	}
}
