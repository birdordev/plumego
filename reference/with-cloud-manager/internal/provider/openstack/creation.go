package openstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

const novaLaunchVersion = "compute 2.52"
const novaLaunchProofVersion = "compute 2.46"

var novaUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var novaLaunchToken = regexp.MustCompile(`^[a-zA-Z0-9]{16,52}$`)

func (c *Client) creationEndpoint(scope provider.Scope, spec provider.InstanceSpec, token string) (*url.URL, error) {
	if err := c.checkScope(scope); err != nil {
		return nil, err
	}
	if scope.Kind != provider.Instance || !novaUUID.MatchString(spec.ImageID) || !novaUUID.MatchString(spec.NetworkID) ||
		!novaUUID.MatchString(spec.SecurityGroup) || !identifier.MatchString(spec.Size) || !novaLaunchToken.MatchString(token) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	base, _, err := c.endpoint(scope)
	return base, err
}

type novaCreateRef struct {
	ID string `json:"id"`
}

type novaNetworkRef struct {
	UUID string `json:"uuid"`
}

type novaGroupRef struct {
	Name string `json:"name"`
}

type novaCreateRequest struct {
	Server struct {
		Name     string           `json:"name"`
		Image    string           `json:"imageRef"`
		Flavor   string           `json:"flavorRef"`
		Tags     []string         `json:"tags"`
		Networks []novaNetworkRef `json:"networks"`
		Groups   []novaGroupRef   `json:"security_groups"`
	} `json:"server"`
}

func (c *Client) CreateInstance(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token string) (provider.LaunchReceipt, error) {
	base, err := c.creationEndpoint(scope, spec, token)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	if !slices.Contains(c.instanceCreateSizes, spec.Size) {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	var input novaCreateRequest
	input.Server.Name, input.Server.Image, input.Server.Flavor = spec.Name, spec.ImageID, spec.Size
	input.Server.Tags = []string{"cmp-" + token}
	input.Server.Networks = []novaNetworkRef{{UUID: spec.NetworkID}}
	input.Server.Groups = []novaGroupRef{{Name: spec.SecurityGroup}}
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		Server novaCreateRef `json:"server"`
	}
	headers, err := c.requestStatus(ctx, http.MethodPost, base.JoinPath("servers"), body, &result, novaLaunchVersion, http.StatusAccepted)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	receipt := provider.LaunchReceipt{RequestID: headers.Get("X-Openstack-Request-Id")}
	if !novaUUID.MatchString(result.Server.ID) {
		return receipt, &provider.Failure{Kind: provider.InvalidResponse}
	}
	receipt.ResourceID = result.Server.ID
	return receipt, nil
}

func (c *Client) FindCreatedInstance(ctx context.Context, scope provider.Scope, spec provider.InstanceSpec, token, instanceID string) (provider.Object, error) {
	base, err := c.creationEndpoint(scope, spec, token)
	if err != nil {
		return provider.Object{}, err
	}
	mark := "cmp-" + token
	if instanceID == "" {
		instanceID, err = c.findLaunchTarget(ctx, base, mark)
		if err != nil {
			return provider.Object{}, err
		}
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if !novaUUID.MatchString(instanceID) {
		return provider.Object{}, invalid
	}
	var detail struct {
		Server struct {
			object
			Project string        `json:"tenant_id"`
			Flavor  novaCreateRef `json:"flavor"`
		} `json:"server"`
	}
	_, err = c.request(ctx, http.MethodGet, base.JoinPath("servers", instanceID), nil, &detail, novaLaunchProofVersion)
	if err != nil {
		return provider.Object{}, err
	}
	_, project, _ := strings.Cut(c.identity, ":")
	server := detail.Server
	if server.ID != instanceID || server.Project != project || !slices.Contains(server.Tags, mark) || server.Status == "" {
		return provider.Object{}, invalid
	}
	asset := server.asset(provider.Instance)
	if server.Status != "ACTIVE" {
		return asset, nil
	}
	var image novaCreateRef
	if json.Unmarshal(server.Image, &image) != nil || image.ID != spec.ImageID || server.Flavor.ID != spec.Size {
		return provider.Object{}, invalid
	}
	scope.Kind = provider.Network
	network, _, err := c.endpoint(scope)
	if err != nil {
		return provider.Object{}, err
	}
	if err := c.verifyLaunchPort(ctx, network, project, instanceID, spec); err != nil {
		return provider.Object{}, err
	}
	asset.Link(provider.Network, spec.NetworkID, "in_network")
	asset.Link(provider.SecurityGroup, spec.SecurityGroup, "secured_by")
	return asset, nil
}

type novaPageLink struct {
	Rel string `json:"rel"`
}

func hasMore(links []novaPageLink) bool {
	return slices.ContainsFunc(links, func(link novaPageLink) bool { return link.Rel == "next" })
}

func (c *Client) findLaunchTarget(ctx context.Context, base *url.URL, mark string) (string, error) {
	u := base.JoinPath("servers")
	u.RawQuery = url.Values{"tags": {mark}, "limit": {"2"}}.Encode()
	var result struct {
		Servers []novaCreateRef `json:"servers"`
		Links   []novaPageLink  `json:"servers_links"`
	}
	if _, err := c.request(ctx, http.MethodGet, u, nil, &result, novaLaunchProofVersion); err != nil {
		return "", err
	}
	if result.Servers == nil || len(result.Servers) > 1 || hasMore(result.Links) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.Servers) == 0 {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return result.Servers[0].ID, nil
}

func (c *Client) verifyLaunchPort(ctx context.Context, base *url.URL, project, instanceID string, spec provider.InstanceSpec) error {
	u := base.JoinPath("ports")
	u.RawQuery = url.Values{"device_id": {instanceID}, "project_id": {project}, "limit": {"2"}}.Encode()
	var result struct {
		Ports []struct {
			Project string   `json:"project_id"`
			Tenant  string   `json:"tenant_id"`
			Device  string   `json:"device_id"`
			Owner   string   `json:"device_owner"`
			Network string   `json:"network_id"`
			Groups  []string `json:"security_groups"`
		} `json:"ports"`
		Links []novaPageLink `json:"ports_links"`
	}
	if _, err := c.request(ctx, http.MethodGet, u, nil, &result, ""); err != nil {
		return err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if result.Ports == nil || len(result.Ports) > 1 || hasMore(result.Links) {
		return invalid
	}
	if len(result.Ports) == 0 {
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	port := result.Ports[0]
	if port.Project == "" {
		port.Project = port.Tenant
	}
	if port.Project != project || (port.Tenant != "" && port.Tenant != project) || port.Device != instanceID ||
		!strings.HasPrefix(port.Owner, "compute:") || port.Network != spec.NetworkID || len(port.Groups) != 1 || port.Groups[0] != spec.SecurityGroup {
		return invalid
	}
	return nil
}
