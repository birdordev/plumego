package publiccloud

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"with-cloud-manager/internal/provider"
)

var huaweiServerID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)

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
				ID     string
				Domain struct{ ID string }
			}
		}
	}
	if err := c.request(req, &result); err != nil {
		return err
	}
	if result.Token.Project.ID != c.profile.ProjectID || result.Token.Project.Domain.ID != c.profile.AccountID || !result.Token.Expires.After(time.Now()) {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	return nil
}

func (c *Client) huaweiInstances(ctx context.Context, region, cursor string) (provider.Page, error) {
	query := url.Values{"limit": {strconv.Itoa(pageSize)}}
	if cursor != "" {
		if !huaweiServerID.MatchString(cursor) {
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
			Image            struct{ ID string }
			SecurityGroups   []struct{ ID string } `json:"security_groups"`
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
		if !huaweiServerID.MatchString(row.ID) {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := provider.Object{ID: row.ID, Name: row.Name, Status: row.Status}
		item.Link(provider.Zone, row.Zone, "in_zone")
		item.Link(provider.Image, row.Image.ID, "uses_image")
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
