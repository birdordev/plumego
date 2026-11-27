package redfish

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

type resetAction struct {
	Target string   `json:"target"`
	Values []string `json:"ResetType@Redfish.AllowableValues"`
	Info   string   `json:"@Redfish.ActionInfo"`
}

type PowerOperator struct{ *Client }

func (c *Client) readSystem(ctx context.Context, target *url.URL) (system, error) {
	var machine system
	etag, err := c.getDocument(ctx, target, &machine)
	if err != nil {
		return system{}, err
	}
	link, err := relativeLink(machine.ID)
	if err != nil || link.RawQuery != "" || strings.TrimSuffix(link.Path, "/") != strings.TrimSuffix(target.Path, "/") {
		return system{}, invalidResponse()
	}
	machine.etag = etag
	return machine, nil
}

func (c PowerOperator) locate(ctx context.Context, scope provider.Scope, id string) (*url.URL, system, error) {
	if scope.Provider != provider.Redfish || scope.RemoteIdentity != c.Identity() || scope.Region != c.config.Site ||
		scope.Kind != provider.BareMetal || !validUUID(id) {
		return nil, system{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	var path *url.URL
	var target system
	if err := c.walkSystems(ctx, func(uri *url.URL, machine system) {
		if strings.EqualFold(machine.UUID, id) {
			path, target = uri, machine
		}
	}); err != nil {
		return nil, system{}, err
	}
	if path == nil {
		return nil, system{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return path, target, nil
}

func (c PowerOperator) Observe(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	_, machine, err := c.locate(ctx, scope, id)
	if err != nil {
		return provider.Object{}, err
	}
	return provider.Object{ID: id, Name: machine.Name, Status: machine.status()}, nil
}

func (PowerOperator) ApplyInstance(context.Context, provider.Scope, string, string) (string, error) {
	return "", &provider.Failure{Kind: provider.Unsupported}
}

func (c PowerOperator) PrepareInstancePower(ctx context.Context, scope provider.Scope, id, action string) (func(context.Context) (string, error), error) {
	return c.preparePower(ctx, scope, id, action, false)
}

func (c PowerOperator) PrepareBareMetalDeploymentBoot(ctx context.Context, scope provider.Scope, id string) (func(context.Context) (string, error), error) {
	if !c.config.AllowPXE {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	return c.preparePower(ctx, scope, id, "power-on-bare-metal", true)
}

func (c PowerOperator) ObserveBareMetalDeploymentBoot(ctx context.Context, scope provider.Scope, id string) (bool, error) {
	_, machine, err := c.locate(ctx, scope, id)
	return err == nil && machine.status() == "on" && machine.Boot.Enabled == "Disabled", err
}

func (c PowerOperator) preparePower(ctx context.Context, scope provider.Scope, id, action string, deployment bool) (func(context.Context) (string, error), error) {
	if !c.config.AllowPower {
		return nil, &provider.Failure{Kind: provider.PowerDisabled}
	}
	reset, before, after := "On", "off", "on"
	switch action {
	case "power-on-bare-metal":
	case "shutdown-bare-metal":
		reset, before, after = "GracefulShutdown", "on", "off"
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	resource, machine, err := c.locate(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if (deployment && (machine.status() != before || !machine.Boot.oncePXE())) ||
		(machine.status() != before && machine.status() != after) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if machine.status() == before {
		if _, err := c.resetTarget(ctx, resource, machine.Actions.Reset, reset); err != nil {
			return nil, err
		}
	}
	return func(ctx context.Context) (string, error) {
		latest, err := c.readSystem(ctx, resource)
		if err != nil {
			return "", err
		}
		if latest.Type != "Physical" || !strings.EqualFold(latest.UUID, id) {
			return "", &provider.Failure{Kind: provider.PreconditionFailed}
		}
		if deployment && (latest.status() != before || !latest.Boot.oncePXE()) {
			return "", &provider.Failure{Kind: provider.PreconditionFailed}
		}
		if latest.status() == after {
			return "", nil
		}
		if latest.status() != before {
			return "", &provider.Failure{Kind: provider.PreconditionFailed}
		}
		target, err := c.resetTarget(ctx, resource, latest.Actions.Reset, reset)
		if err != nil {
			return "", err
		}
		return "", c.submitReset(ctx, target, reset)
	}, nil
}

func (c PowerOperator) resetTarget(ctx context.Context, resource *url.URL, action resetAction, reset string) (*url.URL, error) {
	target, err := relativeLink(action.Target)
	base := strings.TrimSuffix(resource.Path, "/")
	if err != nil || target.RawQuery != "" || target.Path != base+"/Actions/ComputerSystem.Reset" {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if len(action.Values) > 0 && !slices.Contains(action.Values, reset) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if action.Info == "" {
		if slices.Contains(action.Values, reset) {
			return target, nil
		}
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	info, err := relativeLink(action.Info)
	if err != nil || info.RawQuery != "" || !strings.HasPrefix(info.Path, base+"/") {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	var details struct {
		Parameters []struct {
			Name          string   `json:"Name"`
			AllowedValues []string `json:"AllowableValues"`
		} `json:"Parameters"`
	}
	if err := c.get(ctx, info, &details); err != nil {
		return nil, err
	}
	matches, allowed := 0, false
	for _, p := range details.Parameters {
		if p.Name == "ResetType" {
			matches++
			allowed = slices.Contains(p.AllowedValues, reset)
		}
	}
	if matches != 1 || !allowed {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return target, nil
}

func (c PowerOperator) submitReset(ctx context.Context, target *url.URL, reset string) error {
	payload, err := json.Marshal(map[string]string{"ResetType": reset})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.ResolveReference(target).String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.GetBody = nil
	request.SetBasicAuth(c.config.User, c.config.Password)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("OData-Version", "4.0")
	response, err := c.http.Do(request)
	if err != nil {
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		return nil
	default:
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	}
}
