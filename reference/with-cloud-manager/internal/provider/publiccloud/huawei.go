package publiccloud

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"with-cloud-manager/internal/provider"
)

var huaweiResourceID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

func (c *Client) readHuawei(ctx context.Context, endpoint url.URL, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	return c.request(req, dst)
}

func (c *Client) verifyHuawei(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://iam.myhuaweicloud.com/v3/auth/tokens?nocatalog=true", nil)
	if err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("X-Subject-Token", c.profile.IAMToken)
	var result struct {
		Token struct {
			Expires time.Time `json:"expires_at"`
			Project struct {
				ID, Name string
				Domain   struct{ ID string }
			}
		}
	}
	if err := c.request(req, &result); err != nil {
		return err
	}
	if result.Token.Project.ID != c.profile.ProjectID || result.Token.Project.Domain.ID != c.profile.AccountID || !result.Token.Expires.After(time.Now()) {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	c.projectName = result.Token.Project.Name
	return nil
}

func (c *Client) huaweiInstances(ctx context.Context, region, cursor string) (provider.Page, error) {
	query := url.Values{"limit": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		if !huaweiResourceID.MatchString(cursor) {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		query.Set("marker", cursor)
	}
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com", Path: "/v1.1/" + c.profile.ProjectID + "/cloudservers/detail", RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		Links   []struct{ Rel, Href string } `json:"servers_links"`
		Servers []struct {
			ID, Name, Status string
			Tenant           string `json:"tenant_id"`
			Zone             string `json:"availability_zone"`
			Image, Flavor    struct{ ID string }
			SecurityGroups   []struct{ ID string } `json:"security_groups"`
			Addresses        map[string]json.RawMessage
		}
	}
	if err := c.request(req, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Servers == nil || len(result.Servers) > pageSize {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{}
	for _, row := range result.Servers {
		if row.Tenant != c.profile.ProjectID {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !huaweiResourceID.MatchString(row.ID) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := provider.Object{ID: row.ID, Name: row.Name, Status: row.Status, InstanceType: row.Flavor.ID}
		item.Link(provider.Zone, row.Zone, "in_zone")
		item.Link(provider.Image, row.Image.ID, "uses_image")
		for _, network := range slices.Sorted(maps.Keys(row.Addresses)) {
			if !huaweiResourceID.MatchString(network) {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			item.Link(provider.Network, network, "in_network")
		}
		for _, group := range row.SecurityGroups {
			item.Link(provider.SecurityGroup, group.ID, "uses_security_group")
		}
		page.Objects = append(page.Objects, item)
	}
	for _, link := range result.Links {
		if link.Rel != "next" {
			continue
		}
		if page.Next != "" || len(page.Objects) == 0 {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		target, err := endpoint.Parse(link.Href)
		if err != nil || target.Scheme != endpoint.Scheme || target.Host != endpoint.Host || target.Path != endpoint.Path || target.RawPath != "" || target.User != nil || target.Fragment != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		parameters, err := url.ParseQuery(target.RawQuery)
		if err != nil || len(parameters["marker"]) != 1 || parameters.Get("marker") != page.Objects[len(page.Objects)-1].ID {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		page.Next = parameters.Get("marker")
	}
	if page.Next == "" && len(page.Objects) == pageSize {
		page.Next = page.Objects[len(page.Objects)-1].ID
	}
	return page, nil
}

func (c *Client) huaweiNetworking(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	query := url.Values{"limit": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		if !huaweiResourceID.MatchString(cursor) {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		query.Set("marker", cursor)
	}
	var collection string
	switch scope.Kind {
	case provider.Network:
		collection = "vpcs"
	case provider.Subnet:
		collection = "virsubnets"
	case provider.SecurityGroup:
		collection = "security-groups"
	}
	endpoint := url.URL{
		Scheme: "https", Host: "vpc." + scope.Region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/" + collection, RawQuery: query.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	type networkRow struct {
		ID, Name, Status string
		ProjectID        string `json:"project_id"`
		VPC              string `json:"vpc_id"`
		Zone             string `json:"zone_id"`
		Tags             []*tag
	}
	var result struct {
		VPCs           []networkRow
		Virsubnets     []networkRow
		SecurityGroups []networkRow `json:"security_groups"`
		PageInfo       *struct {
			Next  string `json:"next_marker"`
			Count *int   `json:"current_count"`
		} `json:"page_info"`
	}
	if err := c.request(req, &result); err != nil {
		return provider.Page{}, err
	}
	rows := result.VPCs
	switch scope.Kind {
	case provider.Subnet:
		rows = result.Virsubnets
	case provider.SecurityGroup:
		rows = result.SecurityGroups
	}
	if rows == nil || len(rows) > pageSize || (result.PageInfo == nil && len(rows) == pageSize) {
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
		if !huaweiResourceID.MatchString(row.ID) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		status := row.Status
		if scope.Kind == provider.SecurityGroup {
			status = "observed"
		}
		item := tagObject(row.ID, row.Name, status, row.Tags)
		if scope.Kind == provider.Subnet {
			if row.VPC != "" && !huaweiResourceID.MatchString(row.VPC) {
				return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			item.Link(provider.Network, row.VPC, "in_network")
			item.Link(provider.Zone, row.Zone, "in_zone")
		}
		page.Objects = append(page.Objects, item)
	}
	return page, nil
}
