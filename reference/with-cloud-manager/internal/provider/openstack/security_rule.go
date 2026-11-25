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

type neutronSecurityRule struct {
	ID           string `json:"id"`
	Project      string `json:"project_id"`
	Tenant       string `json:"tenant_id"`
	Group        string `json:"security_group_id"`
	Description  string `json:"description"`
	Direction    string `json:"direction"`
	EtherType    string `json:"ethertype"`
	Protocol     string `json:"protocol"`
	FromPort     *int32 `json:"port_range_min"`
	ToPort       *int32 `json:"port_range_max"`
	CIDR         string `json:"remote_ip_prefix"`
	RemoteGroup  string `json:"remote_group_id"`
	AddressGroup string `json:"remote_address_group_id"`
}

func (c *Client) inventorySecurityRule(row neutronSecurityRule) (provider.Object, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if !novaUUID.MatchString(row.ID) || !novaUUID.MatchString(row.Group) || (row.Direction != "ingress" && row.Direction != "egress") ||
		(row.EtherType != "IPv4" && row.EtherType != "IPv6") || (row.Protocol != "" && !identifier.MatchString(row.Protocol)) {
		return provider.Object{}, invalid
	}
	if !c.ownsNeutronProject(row.Project, row.Tenant) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	remote, targets := "", 0
	if row.CIDR != "" {
		prefix, err := netip.ParsePrefix(row.CIDR)
		if err != nil || prefix.Addr().Is4() != (row.EtherType == "IPv4") {
			return provider.Object{}, invalid
		}
		remote, targets = prefix.Masked().String(), 1
	}
	for _, target := range []struct{ id, kind string }{{row.RemoteGroup, "security-group"}, {row.AddressGroup, "address-group"}} {
		if target.id != "" {
			if !novaUUID.MatchString(target.id) {
				return provider.Object{}, invalid
			}
			remote, targets = target.kind+":"+target.id, targets+1
		}
	}
	if targets > 1 {
		return provider.Object{}, invalid
	}
	return provider.ObservedSecurityRule(row.ID, row.Group, row.Direction, row.Protocol, row.EtherType, row.FromPort, row.ToPort, remote), nil
}

func (c *Client) inventorySecurityRules(u *url.URL, data map[string]json.RawMessage, item collection, cursor string) (provider.Page, error) {
	var rows []neutronSecurityRule
	next, err := nextCursor(u, data, item.list)
	if err != nil || json.Unmarshal(data[item.list], &rows) != nil || rows == nil || len(rows) > provider.MaxPageObjects ||
		(next != "" && (len(rows) == 0 || next != rows[len(rows)-1].ID)) {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		object, err := c.inventorySecurityRule(row)
		if err != nil {
			return provider.Page{}, err
		}
		page.Objects = append(page.Objects, object)
	}
	if err := page.Validate(cursor); err != nil {
		return provider.Page{}, err
	}
	return page, nil
}

func (c *Client) observeSecurityRule(ctx context.Context, u *url.URL, id string) (provider.Object, error) {
	if !novaUUID.MatchString(id) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	var data struct {
		Rule neutronSecurityRule `json:"security_group_rule"`
	}
	if _, err := c.request(ctx, http.MethodGet, u, nil, &data, ""); err != nil {
		return provider.Object{}, err
	}
	if data.Rule.ID != id {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return c.inventorySecurityRule(data.Rule)
}

func (c *Client) createSecurityRule(ctx context.Context, base *url.URL, spec provider.NetworkSpec, token string) (provider.LaunchReceipt, error) {
	parent, err := c.readNeutronObject(ctx, base.JoinPath("security-groups", spec.ParentID), "security_group")
	if err != nil || parent.ID != spec.ParentID || !c.ownsNeutronObject(parent) || parent.Name == "" ||
		strings.EqualFold(parent.Name, "default") || (parent.Shared != nil && *parent.Shared) {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	rule := *spec.Rule
	body, err := json.Marshal(map[string]any{"security_group_rule": map[string]any{
		"security_group_id": spec.ParentID, "description": neutronIntentPrefix + token, "direction": rule.Direction,
		"ethertype": "IPv4", "protocol": rule.Protocol, "port_range_min": rule.FromPort, "port_range_max": rule.ToPort,
		"remote_ip_prefix": rule.RemoteCIDR,
	}})
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		Rule neutronSecurityRule `json:"security_group_rule"`
	}
	headers, err := c.request(ctx, http.MethodPost, base.JoinPath("security-group-rules"), body, &result, "")
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	receipt := provider.LaunchReceipt{RequestID: headers.Get("X-Openstack-Request-Id")}
	if novaUUID.MatchString(result.Rule.ID) {
		receipt.ResourceID = result.Rule.ID
	}
	return receipt, nil
}

func (c *Client) findCreatedSecurityRule(ctx context.Context, base *url.URL, item collection, spec provider.NetworkSpec, token, id string) (provider.Object, error) {
	marker := neutronIntentPrefix + token
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	var row neutronSecurityRule
	if id != "" {
		if !novaUUID.MatchString(id) {
			return provider.Object{}, invalid
		}
		var result struct {
			Rule neutronSecurityRule `json:"security_group_rule"`
		}
		if _, err := c.request(ctx, http.MethodGet, base.JoinPath(item.path, id), nil, &result, ""); err != nil {
			return provider.Object{}, err
		}
		row = result.Rule
	} else {
		_, project, _ := strings.Cut(c.identity, ":")
		u := base.JoinPath(item.path)
		u.RawQuery = url.Values{"description": {marker}, "project_id": {project}, "security_group_id": {spec.ParentID}, "limit": {"2"}}.Encode()
		var result map[string]json.RawMessage
		if _, err := c.request(ctx, http.MethodGet, u, nil, &result, ""); err != nil {
			return provider.Object{}, err
		}
		var rows []neutronSecurityRule
		next, err := nextCursor(u, result, item.list)
		if err != nil || next != "" || json.Unmarshal(result[item.list], &rows) != nil || rows == nil || len(rows) > 1 {
			return provider.Object{}, invalid
		}
		if len(rows) == 0 {
			return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		row = rows[0]
	}
	rule := *spec.Rule
	if !novaUUID.MatchString(row.ID) || (id != "" && row.ID != id) || !c.ownsNeutronProject(row.Project, row.Tenant) ||
		row.Group != spec.ParentID || row.Description != marker || row.Direction != rule.Direction || row.EtherType != "IPv4" ||
		row.Protocol != rule.Protocol || row.FromPort == nil || *row.FromPort != rule.FromPort || row.ToPort == nil || *row.ToPort != rule.ToPort || row.CIDR != rule.RemoteCIDR ||
		row.RemoteGroup != "" || row.AddressGroup != "" {
		return provider.Object{}, invalid
	}
	return rule.Asset(row.ID, spec.ParentID), nil
}
