package openstack

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

func (c *Client) ApplyInstance(ctx context.Context, scope provider.Scope, id, action string) (string, error) {
	if err := c.checkScope(scope); err != nil {
		return "", err
	}
	if scope.Kind != provider.Instance || !identifier.MatchString(id) {
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	base, _, err := c.endpoint(scope)
	if err != nil {
		return "", err
	}
	var body string
	switch action {
	case "delete":
		if !c.allowInstanceDelete {
			return "", &provider.Failure{Kind: provider.DeletionDisabled}
		}
		return c.deleteServer(ctx, base, id)
	case "start":
		body = `{"os-start":null}`
	case "stop":
		body = `{"os-stop":null}`
	case "reboot":
		body = `{"reboot":{"type":"SOFT"}}`
	default:
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	headers, err := c.request(ctx, http.MethodPost, base.JoinPath("servers", id, "action"), []byte(body), nil, c.microversion(scope.Kind))
	if err != nil {
		return "", err
	}
	return headers.Get("X-Openstack-Request-Id"), nil
}

func (c *Client) deleteServer(ctx context.Context, base *url.URL, id string) (string, error) {
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	serverURL := base.JoinPath("servers", id)
	var target struct {
		Server struct {
			ID      string `json:"id"`
			Project string `json:"tenant_id"`
			Volumes []struct {
				ID      string `json:"id"`
				Destroy *bool  `json:"delete_on_termination"`
			} `json:"os-extended-volumes:volumes_attached"`
		} `json:"server"`
	}
	_, err := c.request(ctx, http.MethodGet, serverURL, nil, &target, "compute 2.51")
	_, project, _ := strings.Cut(c.identity, ":")
	if err != nil || target.Server.ID != id || target.Server.Project != project || target.Server.Volumes == nil {
		return "", blocked
	}
	for _, volume := range target.Server.Volumes {
		if !identifier.MatchString(volume.ID) || volume.Destroy == nil || *volume.Destroy {
			return "", blocked
		}
	}
	headers, err := c.request(ctx, http.MethodDelete, serverURL, nil, nil, "")
	if err != nil {
		return "", err
	}
	return headers.Get("X-Openstack-Request-Id"), nil
}

func (c *Client) ObserveReboot(ctx context.Context, scope provider.Scope, id, requestID string) (provider.ActionResult, error) {
	return c.observeInstanceAction(ctx, scope, id, requestID, "reboot", "compute_reboot_instance")
}

func (c *Client) ObserveDeletion(ctx context.Context, scope provider.Scope, id, requestID string) (provider.ActionResult, error) {
	return c.observeInstanceAction(ctx, scope, id, requestID, "delete", "compute_terminate_instance")
}

func (c *Client) observeInstanceAction(ctx context.Context, scope provider.Scope, id, requestID, actionName, eventName string) (provider.ActionResult, error) {
	if err := c.checkScope(scope); err != nil {
		return "", err
	}
	if scope.Kind != provider.Instance || !identifier.MatchString(id) || !identifier.MatchString(requestID) {
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	base, _, err := c.endpoint(scope)
	if err != nil {
		return "", err
	}
	var response struct {
		Action struct {
			Name     string `json:"action"`
			Instance string `json:"instance_uuid"`
			Request  string `json:"request_id"`
			Project  string `json:"project_id"`
			Events   []struct {
				Name     string `json:"event"`
				Finished string `json:"finish_time"`
				Result   string `json:"result"`
			} `json:"events"`
		} `json:"instanceAction"`
	}
	_, err = c.request(ctx, http.MethodGet, base.JoinPath("servers", id, "os-instance-actions", requestID), nil, &response, "compute 2.51")
	if err != nil {
		return "", err
	}
	action := response.Action
	_, project, _ := strings.Cut(c.identity, ":")
	if action.Name != actionName || action.Instance != id || action.Request != requestID || action.Project != project {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	result := provider.ActionPending
	for _, event := range action.Events {
		if event.Name != eventName {
			continue
		}
		if event.Finished == "" {
			return provider.ActionPending, nil
		}
		if !validNovaTimestamp(event.Finished) || result != provider.ActionPending {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		switch event.Result {
		case "Success":
			result = provider.ActionSucceeded
		case "Error":
			result = provider.ActionFailed
		default:
			return provider.ActionPending, nil
		}
	}
	return result, nil
}

func validNovaTimestamp(value string) bool {
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return true
	}
	_, err := time.Parse("2006-01-02T15:04:05.999999999", value)
	return err == nil
}
