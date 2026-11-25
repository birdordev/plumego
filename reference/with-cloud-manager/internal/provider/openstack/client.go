package openstack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const Name = "openstack"

const maxBody = 8 << 20

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)

type Config struct {
	Site                        string   `json:"site"`
	Project                     string   `json:"project"`
	IdentityURL                 string   `json:"identity_url"`
	ApplicationID               string   `json:"application_id"`
	Secret                      string   `json:"secret"`
	EndpointOrigins             []string `json:"endpoint_origins"`
	NovaTags                    bool     `json:"nova_tags,omitempty"`
	AllowInstanceDelete         bool     `json:"allow_instance_delete,omitempty"`
	AllowVolumeChanges          bool     `json:"allow_volume_changes,omitempty"`
	AllowNetworkCreation        bool     `json:"allow_network_creation,omitempty"`
	AllowSecurityGroupCreation  bool     `json:"allow_security_group_creation,omitempty"`
	AllowSecurityGroupRules     bool     `json:"allow_security_group_rules,omitempty"`
	AllowSecurityRuleRevocation bool     `json:"allow_security_rule_revocation,omitempty"`
	InstanceCreateSizes         []string `json:"instance_create_sizes,omitempty"`
	ConsoleProxyURL             string   `json:"console_proxy_url,omitempty"`
}

type Endpoint struct {
	Region    string `json:"region_id"`
	Interface string `json:"interface"`
	URL       string `json:"url"`
}

type catalogEntry struct {
	Type      string     `json:"type"`
	Endpoints []Endpoint `json:"endpoints"`
}

type Client struct {
	http                        http.Client
	identity                    string
	token                       string
	novaTags                    bool
	allowInstanceDelete         bool
	allowVolumeChanges          bool
	allowNetworkCreation        bool
	allowSecurityGroupCreation  bool
	allowSecurityGroupRules     bool
	allowSecurityRuleRevocation bool
	instanceCreateSizes         []string
	endpoints                   map[string]map[string]*url.URL
	consoleProxy                *url.URL
}

func (c *Client) Identity() string { return c.identity }

func Connect(ctx context.Context, cfg Config, base *http.Client) (*Client, error) {
	if !identifier.MatchString(cfg.Site) || !identifier.MatchString(cfg.Project) ||
		strings.Contains(cfg.Site, ":") || strings.Contains(cfg.Project, ":") ||
		cfg.ApplicationID == "" || cfg.Secret == "" || len(cfg.EndpointOrigins) == 0 {
		return nil, errors.New("invalid OpenStack profile")
	}
	identityURL, err := endpointURL(cfg.IdentityURL)
	if err != nil || !strings.HasSuffix(identityURL.Path, "/v3") {
		return nil, errors.New("invalid Keystone v3 endpoint")
	}
	origins := make(map[string]bool, len(cfg.EndpointOrigins))
	for _, origin := range cfg.EndpointOrigins {
		parsed, err := endpointURL(origin)
		if err != nil || parsed.Path != "" {
			return nil, errors.New("invalid OpenStack service origin")
		}
		origins[parsed.Host] = true
	}
	if base == nil {
		base = http.DefaultClient
	}
	client := &Client{http: *base, identity: cfg.Site + ":" + cfg.Project,
		novaTags: cfg.NovaTags, allowInstanceDelete: cfg.AllowInstanceDelete,
		allowVolumeChanges:          cfg.AllowVolumeChanges,
		allowNetworkCreation:        cfg.AllowNetworkCreation,
		allowSecurityGroupCreation:  cfg.AllowSecurityGroupCreation,
		allowSecurityGroupRules:     cfg.AllowSecurityGroupRules,
		allowSecurityRuleRevocation: cfg.AllowSecurityRuleRevocation,
		instanceCreateSizes:         slices.Clone(cfg.InstanceCreateSizes)}
	if cfg.ConsoleProxyURL != "" {
		proxy, err := endpointURL(cfg.ConsoleProxyURL)
		if err != nil || strings.Contains(cfg.ConsoleProxyURL, "#") || (proxy.Path != "" && strings.Contains(proxy.Path, "..")) {
			return nil, errors.New("invalid OpenStack console proxy")
		}
		if proxy.Path == "" {
			proxy.Path = "/"
		}
		client.consoleProxy = proxy
	}
	if client.http.Timeout <= 0 || client.http.Timeout > 20*time.Second {
		client.http.Timeout = 20 * time.Second
	}
	client.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	requestBody := struct {
		Auth struct {
			Identity struct {
				Methods    []string          `json:"methods"`
				Credential map[string]string `json:"application_credential"`
			} `json:"identity"`
		} `json:"auth"`
	}{}
	requestBody.Auth.Identity.Methods = []string{"application_credential"}
	requestBody.Auth.Identity.Credential = map[string]string{"id": cfg.ApplicationID, "secret": cfg.Secret}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}
	var response struct {
		Token struct {
			Project struct {
				ID string `json:"id"`
			} `json:"project"`
			Catalog []catalogEntry `json:"catalog"`
		} `json:"token"`
	}
	headers, err := client.request(ctx, http.MethodPost, identityURL.JoinPath("auth", "tokens"), body, &response, "")
	if err != nil {
		return nil, err
	}
	client.token = headers.Get("X-Subject-Token")
	if client.token == "" || response.Token.Project.ID != cfg.Project {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	client.endpoints, err = catalogEndpoints(response.Token.Catalog, origins)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func endpointURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u, nil
}

func catalogEndpoints(catalog []catalogEntry, origins map[string]bool) (map[string]map[string]*url.URL, error) {
	endpoints := make(map[string]map[string]*url.URL)
	for _, service := range catalog {
		serviceType := service.Type
		if serviceType == "block-storage" {
			serviceType = "volumev3"
		}
		if !slices.Contains([]string{"compute", "volumev3", "network", "image"}, serviceType) {
			continue
		}
		for _, endpoint := range service.Endpoints {
			if endpoint.Interface != "public" || !identifier.MatchString(endpoint.Region) {
				continue
			}
			u, err := endpointURL(endpoint.URL)
			if err != nil || !origins[u.Host] {
				continue
			}
			byType := endpoints[endpoint.Region]
			if byType == nil {
				byType = make(map[string]*url.URL)
				endpoints[endpoint.Region] = byType
			}
			if prior := byType[serviceType]; prior != nil && prior.String() != u.String() {
				return nil, &provider.Failure{Kind: provider.InvalidResponse}
			}
			byType[serviceType] = u
		}
	}
	if len(endpoints) == 0 {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	return endpoints, nil
}

func (c *Client) request(ctx context.Context, method string, u *url.URL, body []byte, dst any, microversion string) (http.Header, error) {
	expectedStatus := http.StatusOK
	if method == http.MethodPost {
		expectedStatus = http.StatusCreated
	}
	if dst == nil {
		expectedStatus = http.StatusAccepted
		if method == http.MethodDelete {
			expectedStatus = http.StatusNoContent
		}
	}
	return c.requestStatus(ctx, method, u, body, dst, microversion, expectedStatus)
}

func (c *Client) requestStatus(ctx context.Context, method string, u *url.URL, body []byte, dst any, microversion string, expectedStatus int) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Accept", "application/json")
	if microversion != "" {
		req.Header.Set("OpenStack-API-Version", microversion)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("X-Auth-Token", c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &provider.Failure{Kind: provider.Transient}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != expectedStatus {
		return nil, responseFailure(res)
	}
	if dst == nil {
		return res.Header, nil
	}
	reader := &io.LimitedReader{R: res.Body, N: maxBody + 1}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(dst); err != nil {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF || reader.N == 0 {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return res.Header, nil
}

func responseFailure(res *http.Response) error {
	f := &provider.Failure{Kind: provider.InvalidResponse}
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		f.Kind = provider.Unauthorized
	case http.StatusNotFound:
		f.Kind = provider.OutcomeUnknown
	case http.StatusTooManyRequests:
		f.Kind = provider.Throttled
		if seconds, err := strconv.ParseInt(res.Header.Get("Retry-After"), 10, 32); err == nil && seconds > 0 {
			f.RetryAfter = time.Duration(seconds) * time.Second
		} else if deadline, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
			f.RetryAfter = max(time.Until(deadline), 0)
		}
	case http.StatusRequestTimeout, http.StatusConflict:
		f.Kind = provider.Transient
	default:
		if res.StatusCode >= http.StatusInternalServerError {
			f.Kind = provider.Transient
		}
	}
	return f
}
