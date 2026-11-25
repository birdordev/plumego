package openstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

const neutronIntentPrefix = "plumego-network-intent:"
const neutronDescriptionExtension = "standard-attr-description"

type neutronNetworkObject struct {
	object
	Project     string `json:"project_id"`
	Tenant      string `json:"tenant_id"`
	Description string `json:"description"`
	CIDR        string `json:"cidr"`
	IPVersion   int    `json:"ip_version"`
	Shared      *bool  `json:"shared"`
	External    bool   `json:"router:external"`
	Rules       []struct {
		ID        string `json:"id"`
		Direction string `json:"direction"`
	} `json:"security_group_rules"`
}

func (c *Client) networkEndpoint(scope provider.Scope, spec provider.NetworkSpec, token string) (*url.URL, collection, error) {
	if err := c.checkScope(scope); err != nil {
		return nil, collection{}, err
	}
	valid := novaLaunchToken.MatchString(token)
	if scope.Kind != provider.SecurityGroupRule && spec.Rule != nil {
		valid = false
	}
	switch scope.Kind {
	case provider.Network:
		valid = valid && spec.CIDR == "" && spec.ParentID == ""
	case provider.Subnet:
		prefix, err := netip.ParsePrefix(spec.CIDR)
		valid = valid && err == nil && prefix.Addr().Is4() && prefix.Bits() > 0 && prefix == prefix.Masked() &&
			novaUUID.MatchString(spec.ParentID)
	case provider.SecurityGroup:
		valid = valid && spec.CIDR == "" && spec.ParentID == "" && spec.Name != "" && !strings.EqualFold(spec.Name, "default")
	case provider.SecurityGroupRule:
		valid = valid && spec.CIDR == "" && spec.Name == "" && novaUUID.MatchString(spec.ParentID) && spec.Rule != nil && spec.Rule.Valid()
	default:
		valid = false
	}
	if !valid {
		return nil, collection{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	return c.endpoint(scope)
}

func (c *Client) ownsNeutronObject(row neutronNetworkObject) bool {
	return c.ownsNeutronProject(row.Project, row.Tenant)
}

func (c *Client) ownsNeutronProject(projectID, tenantID string) bool {
	_, project, _ := strings.Cut(c.identity, ":")
	return (projectID == project || tenantID == project) &&
		(projectID == "" || projectID == project) && (tenantID == "" || tenantID == project)
}

func (c *Client) CreateNetwork(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token string) (provider.LaunchReceipt, error) {
	base, item, err := c.networkEndpoint(scope, spec, token)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	blocked := &provider.Failure{Kind: provider.CreationBlocked}
	allowed := c.allowNetworkCreation
	switch scope.Kind {
	case provider.SecurityGroup:
		allowed = c.allowSecurityGroupCreation
	case provider.SecurityGroupRule:
		allowed = c.allowSecurityGroupRules
	}
	if !allowed {
		return provider.LaunchReceipt{}, blocked
	}
	var extension struct {
		Extension struct {
			Alias string `json:"alias"`
		} `json:"extension"`
	}
	if _, err := c.request(ctx, http.MethodGet, base.JoinPath("extensions", neutronDescriptionExtension), nil, &extension, ""); err != nil ||
		extension.Extension.Alias != neutronDescriptionExtension {
		return provider.LaunchReceipt{}, blocked
	}
	if scope.Kind == provider.SecurityGroupRule {
		return c.createSecurityRule(ctx, base, spec, token)
	}
	input := map[string]any{"name": spec.Name, "description": neutronIntentPrefix + token}
	switch scope.Kind {
	case provider.Network:
		input["admin_state_up"], input["shared"] = true, false
	case provider.Subnet:
		parent, err := c.readNeutronObject(ctx, base.JoinPath("networks", spec.ParentID), "network")
		if err != nil || parent.ID != spec.ParentID || parent.Status != "ACTIVE" ||
			!c.ownsNeutronObject(parent) || parent.Shared == nil || *parent.Shared || parent.External {
			return provider.LaunchReceipt{}, blocked
		}
		input["network_id"], input["cidr"], input["ip_version"] = spec.ParentID, spec.CIDR, 4
	}
	body, err := json.Marshal(map[string]any{item.singular: input})
	if err != nil {
		return provider.LaunchReceipt{}, blocked
	}
	var result map[string]neutronNetworkObject
	headers, err := c.request(ctx, http.MethodPost, base.JoinPath(item.path), body, &result, "")
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	receipt := provider.LaunchReceipt{RequestID: headers.Get("X-Openstack-Request-Id")}
	if id := result[item.singular].ID; novaUUID.MatchString(id) {
		receipt.ResourceID = id
	}
	return receipt, nil
}

func (c *Client) readNeutronObject(ctx context.Context, u *url.URL, singular string) (neutronNetworkObject, error) {
	var result map[string]neutronNetworkObject
	if _, err := c.request(ctx, http.MethodGet, u, nil, &result, ""); err != nil {
		return neutronNetworkObject{}, err
	}
	row, ok := result[singular]
	if !ok {
		return neutronNetworkObject{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return row, nil
}

func (c *Client) FindCreatedNetwork(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token, id string) (provider.Object, error) {
	base, item, err := c.networkEndpoint(scope, spec, token)
	if err != nil {
		return provider.Object{}, err
	}
	if scope.Kind == provider.SecurityGroupRule {
		return c.findCreatedSecurityRule(ctx, base, item, spec, token, id)
	}
	marker := neutronIntentPrefix + token
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	var row neutronNetworkObject
	if id != "" {
		if !novaUUID.MatchString(id) {
			return provider.Object{}, invalid
		}
		row, err = c.readNeutronObject(ctx, base.JoinPath(item.path, id), item.singular)
		if err != nil {
			return provider.Object{}, err
		}
	} else {
		_, project, _ := strings.Cut(c.identity, ":")
		u := base.JoinPath(item.path)
		u.RawQuery = url.Values{"description": {marker}, "project_id": {project}, "limit": {"2"}}.Encode()
		var result map[string]json.RawMessage
		if _, err := c.request(ctx, http.MethodGet, u, nil, &result, ""); err != nil {
			return provider.Object{}, err
		}
		var rows []neutronNetworkObject
		var links []novaPageLink
		if json.Unmarshal(result[item.list], &rows) != nil || rows == nil ||
			(result[item.list+"_links"] != nil && json.Unmarshal(result[item.list+"_links"], &links) != nil) ||
			hasMore(links) || len(rows) > 1 {
			return provider.Object{}, invalid
		}
		if len(rows) == 0 {
			return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		row = rows[0]
	}
	if !novaUUID.MatchString(row.ID) || (id != "" && id != row.ID) ||
		!c.ownsNeutronObject(row) || row.Description != marker || row.Name != spec.Name {
		return provider.Object{}, invalid
	}
	switch scope.Kind {
	case provider.Network:
		if row.Shared == nil || *row.Shared || row.External {
			return provider.Object{}, invalid
		}
	case provider.Subnet:
		if row.IPVersion != 4 || row.CIDR != spec.CIDR || row.NetworkID != spec.ParentID {
			return provider.Object{}, invalid
		}
	case provider.SecurityGroup:
		if row.Rules == nil || (row.Shared != nil && *row.Shared) {
			return provider.Object{}, invalid
		}
		for _, rule := range row.Rules {
			if rule.Direction != "egress" {
				return provider.Object{}, invalid
			}
		}
		row.Status = "available"
	}
	return row.asset(scope.Kind), nil
}
