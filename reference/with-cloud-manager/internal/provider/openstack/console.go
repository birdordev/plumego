package openstack

import (
	"context"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"with-cloud-manager/internal/provider"
)

var consoleInstanceID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
var consoleToken = regexp.MustCompile(`^[A-Za-z0-9._~-]{16,256}$`)

func (c *Client) OpenNoVNC(ctx context.Context, scope provider.Scope, id string) (*url.URL, error) {
	if err := c.checkScope(scope); err != nil {
		return nil, err
	}
	if scope.Kind != provider.Instance || !consoleInstanceID.MatchString(id) || c.consoleProxy == nil {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	base, _, err := c.endpoint(scope)
	if err != nil {
		return nil, err
	}
	target := base.JoinPath("servers", id)
	var server struct {
		Server struct {
			ID      string `json:"id"`
			Project string `json:"tenant_id"`
		} `json:"server"`
	}
	if _, err := c.request(ctx, http.MethodGet, target, nil, &server, "compute 2.6"); err != nil {
		return nil, err
	}
	_, project, _ := strings.Cut(c.identity, ":")
	if server.Server.ID != id || server.Server.Project != project {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	var result struct {
		Remote struct {
			Protocol string `json:"protocol"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"remote_console"`
	}
	const body = `{"remote_console":{"protocol":"vnc","type":"novnc"}}`
	if _, err := c.requestStatus(ctx, http.MethodPost, target.JoinPath("remote-consoles"), []byte(body), &result, "compute 2.6", http.StatusOK); err != nil {
		return nil, err
	}
	if result.Remote.Protocol != "vnc" || result.Remote.Type != "novnc" {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return novaConsoleTarget(c.consoleProxy, result.Remote.URL)
}

func novaConsoleTarget(proxy *url.URL, raw string) (*url.URL, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if len(raw) > 4096 || strings.Contains(raw, "#") {
		return nil, invalid
	}
	viewer, err := url.Parse(raw)
	if err != nil || viewer.Scheme != "https" || viewer.Host != proxy.Host || viewer.User != nil || viewer.Opaque != "" ||
		viewer.RawPath != "" || path.Dir(viewer.Path) != path.Dir(proxy.Path) ||
		(path.Base(viewer.Path) != "vnc_lite.html" && path.Base(viewer.Path) != "vnc_auto.html") {
		return nil, invalid
	}
	query, err := url.ParseQuery(viewer.RawQuery)
	if err != nil || len(query) != 1 {
		return nil, invalid
	}
	if value := query["path"]; len(value) == 1 {
		ws, err := url.Parse(value[0])
		if err != nil || ws.Scheme != "" || ws.Host != "" || ws.User != nil || ws.Opaque != "" || ws.RawPath != "" || ws.Fragment != "" ||
			(ws.Path != "" && ws.Path != path.Base(proxy.Path) && ws.Path != proxy.Path) {
			return nil, invalid
		}
		query, err = url.ParseQuery(ws.RawQuery)
		if err != nil || len(query) != 1 {
			return nil, invalid
		}
	}
	tokens := query["token"]
	if len(tokens) != 1 || !consoleToken.MatchString(tokens[0]) {
		return nil, invalid
	}
	result := *proxy
	result.RawQuery = url.Values{"token": {tokens[0]}}.Encode()
	return &result, nil
}
