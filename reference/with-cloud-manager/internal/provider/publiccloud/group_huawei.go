package publiccloud

import (
	"context"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createHuaweiGroup(ctx context.Context, region string, spec provider.NetworkSpec, description string) (provider.LaunchReceipt, error) {
	type group struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	payload := struct {
		Group group `json:"security_group"`
	}{group{spec.Name, description}}
	var response struct {
		RequestID string `json:"request_id"`
		Group     struct {
			ID      string
			Project string `json:"project_id"`
		} `json:"security_group"`
	}
	if err := c.postHuaweiNetwork(ctx, region, "/v3/"+c.profile.ProjectID+"/vpc/security-groups", payload, &response); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return c.huaweiNetworkReceipt(response.RequestID, huaweiNetworkReceipt{ID: response.Group.ID, Tenant: response.Group.Project})
}

type huaweiGroupObservation struct {
	ID, Name, Description string
	Project               string `json:"project_id"`
	Tags                  []*tag
}

type huaweiGroupDetails struct {
	huaweiGroupObservation
	Rules []huaweiRule `json:"security_group_rules"`
}

func (c *Client) readHuaweiGroup(ctx context.Context, region, id string) (huaweiGroupDetails, error) {
	var result struct {
		Group huaweiGroupDetails `json:"security_group"`
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if !huaweiResourceID.MatchString(id) {
		return huaweiGroupDetails{}, invalid
	}
	endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/security-groups/" + id}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return huaweiGroupDetails{}, err
	}
	row := result.Group
	if row.Project != c.profile.ProjectID {
		return huaweiGroupDetails{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if row.ID != id || row.Name == "" || row.Rules == nil {
		return huaweiGroupDetails{}, invalid
	}
	return row, nil
}

func (c *Client) findHuaweiGroup(ctx context.Context, region string, spec provider.NetworkSpec, id, description string) (provider.Object, error) {
	endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/security-groups"}
	if id == "" {
		var result struct {
			Groups   []huaweiGroupObservation `json:"security_groups"`
			PageInfo *huaweiPageInfo          `json:"page_info"`
		}
		search := endpoint
		search.RawQuery = url.Values{"limit": {"2"}, "description": {description}}.Encode()
		if err := c.readHuawei(ctx, search, &result); err != nil {
			return provider.Object{}, err
		}
		if result.Groups == nil || !result.PageInfo.isCompleteSingleton(len(result.Groups)) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if len(result.Groups) == 0 {
			return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
		}
		match := result.Groups[0]
		if match.Project != c.profile.ProjectID {
			return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if match.Name != spec.Name || match.Description != description {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		id = match.ID
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	row, err := c.readHuaweiGroup(ctx, region, id)
	if err != nil {
		return provider.Object{}, err
	}
	if row.Name != spec.Name || row.Description != description {
		return provider.Object{}, invalid
	}
	for _, rule := range row.Rules {
		if rule.ProjectID != c.profile.ProjectID || rule.GroupID != id || !huaweiResourceID.MatchString(rule.ID) {
			return provider.Object{}, invalid
		}
		switch rule.Direction {
		case "ingress":
			if rule.Action != "allow" || rule.RemoteGroup != id || rule.RemoteCIDR != "" || rule.RemoteAddressGroup != "" {
				return provider.Object{}, invalid
			}
		case "egress":
		default:
			return provider.Object{}, invalid
		}
	}
	return tagObject(row.ID, row.Name, "available", row.Tags), nil
}
