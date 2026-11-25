package openstack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

const pageLimit = "200"

type collection struct {
	service, path, list, singular string
	version                       string
}

func collectionFor(kind provider.Kind) (collection, bool) {
	switch kind {
	case provider.Instance:
		return collection{"compute", "servers/detail", "servers", "server", ""}, true
	case provider.Volume:
		return collection{"volumev3", "volumes/detail", "volumes", "volume", ""}, true
	case provider.Network:
		return collection{"network", "networks", "networks", "network", "/v2.0"}, true
	case provider.Subnet:
		return collection{"network", "subnets", "subnets", "subnet", "/v2.0"}, true
	case provider.SecurityGroup:
		return collection{"network", "security-groups", "security_groups", "security_group", "/v2.0"}, true
	case provider.SecurityGroupRule:
		return collection{"network", "security-group-rules", "security_group_rules", "security_group_rule", "/v2.0"}, true
	case provider.Image:
		return collection{"image", "images", "images", "", "/v2"}, true
	case provider.Zone:
		return collection{"compute", "os-availability-zone", "availabilityZoneInfo", "", ""}, true
	default:
		return collection{}, false
	}
}

func (c *Client) checkScope(scope provider.Scope) error {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	return nil
}

func (c *Client) endpoint(scope provider.Scope) (*url.URL, collection, error) {
	item, ok := collectionFor(scope.Kind)
	if !ok || c.endpoints[scope.Region][item.service] == nil {
		return nil, collection{}, &provider.Failure{Kind: provider.Unsupported}
	}
	base := c.endpoints[scope.Region][item.service]
	if item.version != "" && !strings.HasSuffix(base.Path, item.version) {
		base = base.JoinPath(item.version)
	}
	return base, item, nil
}

type object struct {
	Tags         []string        `json:"tags"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Status       string          `json:"status"`
	NetworkID    string          `json:"network_id"`
	InstanceZone string          `json:"OS-EXT-AZ:availability_zone"`
	Image        json.RawMessage `json:"image"`
	Volumes      []struct {
		ID string `json:"id"`
	} `json:"os-extended-volumes:volumes_attached"`
	Attachments []struct {
		ServerID string `json:"server_id"`
	} `json:"attachments"`
	ZoneName  string `json:"zoneName"`
	ZoneState struct {
		Available *bool `json:"available"`
	} `json:"zoneState"`
}

func (o object) asset(kind provider.Kind) provider.Object {
	if o.ZoneName != "" {
		status := "unknown"
		if o.ZoneState.Available != nil {
			status = "unavailable"
			if *o.ZoneState.Available {
				status = "available"
			}
		}
		return provider.Object{ID: o.ZoneName, Name: o.ZoneName, Status: status}
	}
	asset := provider.Object{ID: o.ID, Name: o.Name, Status: o.Status, Tags: keyValueTags(o.Tags)}
	switch kind {
	case provider.Instance:
		asset.Link(provider.Zone, o.InstanceZone, "in_zone")
		var image struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(o.Image, &image) == nil {
			asset.Link(provider.Image, image.ID, "uses_image")
		}
		for _, volume := range o.Volumes {
			asset.Link(provider.Volume, volume.ID, "uses_volume")
		}
	case provider.Volume:
		for _, attachment := range o.Attachments {
			asset.Link(provider.Instance, attachment.ServerID, "attached_to")
		}
	case provider.Subnet:
		if asset.Status == "" {
			asset.Status = "available"
		}
		asset.Link(provider.Network, o.NetworkID, "in_network")
	}
	return asset
}

func (c *Client) Discover(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	if err := c.checkScope(scope); err != nil {
		return provider.Page{}, err
	}
	if scope.Kind == provider.Region {
		if scope.Region != "" || cursor != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
		}
		regions := make([]string, 0, len(c.endpoints))
		for region := range c.endpoints {
			regions = append(regions, region)
		}
		slices.Sort(regions)
		page := provider.Page{Objects: make([]provider.Object, 0, len(regions))}
		for _, region := range regions {
			page.Objects = append(page.Objects, provider.Object{ID: region, Name: region, Status: "available"})
		}
		return page, page.Validate(cursor)
	}

	base, item, err := c.endpoint(scope)
	if err != nil {
		return provider.Page{}, err
	}
	u := base.JoinPath(item.path)
	if scope.Kind != provider.Zone {
		query := url.Values{"limit": {pageLimit}}
		if scope.Kind == provider.SecurityGroupRule {
			_, project, _ := strings.Cut(c.identity, ":")
			query.Set("project_id", project)
		}
		if cursor != "" {
			if !identifier.MatchString(cursor) {
				return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
			}
			query.Set("marker", cursor)
		}
		u.RawQuery = query.Encode()
	} else if cursor != "" {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	var data map[string]json.RawMessage
	if _, err := c.request(ctx, http.MethodGet, u, nil, &data, c.microversion(scope.Kind)); err != nil {
		return provider.Page{}, err
	}
	if scope.Kind == provider.SecurityGroupRule {
		return c.inventorySecurityRules(u, data, item, cursor)
	}
	var objects []object
	if err := json.Unmarshal(data[item.list], &objects); err != nil || objects == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := nextCursor(u, data, item.list)
	if err != nil {
		return provider.Page{}, err
	}
	if scope.Kind == provider.Zone && next != "" {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Next: next, Objects: make([]provider.Object, 0, len(objects))}
	for _, o := range objects {
		page.Objects = append(page.Objects, o.asset(scope.Kind))
	}
	return page, page.Validate(cursor)
}

func nextCursor(current *url.URL, data map[string]json.RawMessage, list string) (string, error) {
	var raw string
	if value, ok := data["next"]; ok {
		if err := json.Unmarshal(value, &raw); err != nil {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	if value, ok := data[list+"_links"]; ok {
		var links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		}
		if err := json.Unmarshal(value, &links); err != nil {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, link := range links {
			if link.Rel == "next" {
				if raw != "" || link.Href == "" {
					return "", &provider.Failure{Kind: provider.InvalidResponse}
				}
				raw = link.Href
			}
		}
	}
	if raw == "" {
		return "", nil
	}
	target, err := current.Parse(raw)
	requestPath := "/" + strings.TrimPrefix(current.Path, "/")
	if err != nil || target.Scheme != current.Scheme || target.Host != current.Host ||
		target.Path != requestPath || target.RawPath != "" || target.User != nil || target.Fragment != "" {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil || len(query["marker"]) != 1 || !identifier.MatchString(query.Get("marker")) {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return query.Get("marker"), nil
}

func (c *Client) Observe(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if err := c.checkScope(scope); err != nil {
		return provider.Object{}, err
	}
	base, item, err := c.endpoint(scope)
	if err != nil {
		return provider.Object{}, err
	}
	if scope.Kind == provider.Zone || !identifier.MatchString(id) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	u := base.JoinPath(strings.TrimSuffix(item.path, "/detail"), id)
	if scope.Kind == provider.SecurityGroupRule {
		return c.observeSecurityRule(ctx, u, id)
	}
	var result object
	if item.singular == "" {
		if _, err := c.request(ctx, http.MethodGet, u, nil, &result, c.microversion(scope.Kind)); err != nil {
			return provider.Object{}, err
		}
	} else {
		var data map[string]json.RawMessage
		if _, err := c.request(ctx, http.MethodGet, u, nil, &data, c.microversion(scope.Kind)); err != nil {
			return provider.Object{}, err
		}
		if err := json.Unmarshal(data[item.singular], &result); err != nil {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	asset := result.asset(scope.Kind)
	if asset.ID != id {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return asset, (provider.Page{Objects: []provider.Object{asset}}).Validate("")
}

func keyValueTags(values []string) map[string]string {
	if values == nil {
		return nil
	}
	tags := make(map[string]string)
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			continue
		}
		if previous, exists := tags[key]; exists && previous != value {
			return nil
		}
		tags[key] = value
	}
	return tags
}

func (c *Client) microversion(kind provider.Kind) string {
	if c.novaTags && kind == provider.Instance {
		return "compute 2.26"
	}
	return ""
}
