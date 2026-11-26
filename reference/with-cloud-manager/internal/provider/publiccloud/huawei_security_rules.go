package publiccloud

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type huaweiRule struct {
	ID, Direction, Protocol, EtherType, Multiport, Action, Description string
	GroupID                                                            string `json:"security_group_id"`
	ProjectID                                                          string `json:"project_id"`
	RemoteCIDR                                                         string `json:"remote_ip_prefix"`
	RemoteGroup                                                        string `json:"remote_group_id"`
	RemoteAddressGroup                                                 string `json:"remote_address_group_id"`
	Priority                                                           *int
	Enabled                                                            *bool
}

func (c *Client) huaweiSecurityRules(ctx context.Context, region, cursor string) (provider.Page, error) {
	query := url.Values{"limit": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		if !huaweiResourceID.MatchString(cursor) {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		query.Set("marker", cursor)
	}
	endpoint := url.URL{
		Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/security-group-rules", RawQuery: query.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		Rules    []huaweiRule `json:"security_group_rules"`
		PageInfo *struct {
			Next  string `json:"next_marker"`
			Count *int   `json:"current_count"`
		} `json:"page_info"`
	}
	if err := c.request(req, &result); err != nil {
		return provider.Page{}, err
	}
	rows := result.Rules
	if rows == nil || len(rows) > pageSize || (len(rows) == pageSize && result.PageInfo == nil) {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(rows))}
	if info := result.PageInfo; info != nil {
		if (info.Count != nil && *info.Count != len(rows)) ||
			(info.Next != "" && (len(rows) == 0 || info.Next != rows[len(rows)-1].ID)) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Next = info.Next
	}
	for _, row := range rows {
		if row.ProjectID != c.profile.ProjectID {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !huaweiResourceID.MatchString(row.ID) || !huaweiResourceID.MatchString(row.GroupID) || !validRuleDirection(row.Direction) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Objects = append(page.Objects, row.asset())
	}
	return page, nil
}

func (r huaweiRule) asset() provider.Object {
	family := r.EtherType
	if strings.EqualFold(family, "ipv4") {
		family = "IPv4"
	} else if strings.EqualFold(family, "ipv6") {
		family = "IPv6"
	}
	remote := strings.TrimSpace(ruleDetail("cidr", r.RemoteCIDR) + ruleDetail("remote_group", r.RemoteGroup) +
		ruleDetail("address_group", r.RemoteAddressGroup))
	rule := provider.ObservedSecurityRule(r.ID, r.GroupID, r.Direction, strings.ToLower(r.Protocol),
		family, nil, nil, remote)
	rule.Name += ruleDetail("ports", r.Multiport) + ruleDetail("action", r.Action) + rulePriority(r.Priority)
	if r.Enabled != nil && !*r.Enabled {
		rule.Status = "disabled"
	}
	return rule
}
