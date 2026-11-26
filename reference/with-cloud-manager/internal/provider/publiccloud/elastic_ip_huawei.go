package publiccloud

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

type huaweiElasticIP struct {
	ID, Alias, Status, Type string
	Description             string
	Project                 string `json:"project_id"`
	IPv4                    string `json:"public_ip_address"`
	IPv6                    string `json:"public_ipv6_address"`
	AssociatedType          string `json:"associate_instance_type"`
	AssociatedID            string `json:"associate_instance_id"`
	Pool                    string `json:"publicip_pool_name"`
	BillingInfo             string `json:"billing_info"`
	LockStatus              string `json:"lock_status"`
	IPVersion               int    `json:"ip_version"`
	Bandwidth               *huaweiElasticIPBandwidth
	Tags                    []string
	Vnic                    *struct {
		PortID      string `json:"port_id"`
		DeviceID    string `json:"device_id"`
		DeviceOwner string `json:"device_owner"`
		VpcID       string `json:"vpc_id"`
	}
}

type huaweiElasticIPBandwidth struct {
	Size        int    `json:"size"`
	ShareType   string `json:"share_type"`
	ChargeMode  string `json:"charge_mode"`
	Name        string `json:"name"`
	BillingInfo string `json:"billing_info,omitempty"`
}

type huaweiElasticIPList struct {
	Rows []huaweiElasticIP `json:"publicips"`
	Page *huaweiPageInfo   `json:"page_info"`
}

func (c *Client) huaweiElasticIPEndpoint(region string) url.URL {
	return url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/eip/publicips"}
}

func (c *Client) readHuaweiElasticIPs(ctx context.Context, region string, query url.Values) (huaweiElasticIPList, error) {
	const wireLimit = 2000
	endpoint := c.huaweiElasticIPEndpoint(region)
	query.Set("limit", "2000")
	endpoint.RawQuery = query.Encode()
	var result huaweiElasticIPList
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return huaweiElasticIPList{}, err
	}
	if result.Rows == nil || len(result.Rows) > wireLimit || result.Page == nil || result.Page.Count == nil ||
		*result.Page.Count != len(result.Rows) {
		return huaweiElasticIPList{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next := result.Page.Next
	if next != "" && (len(result.Rows) == 0 || next == query.Get("marker") || !huaweiResourceID.MatchString(next)) {
		return huaweiElasticIPList{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result, nil
}

func (c *Client) huaweiElasticIPs(ctx context.Context, region, cursor string) (provider.Page, error) {
	marker, digest, resume := strings.Cut(cursor, "~")
	if (marker != "" && !huaweiResourceID.MatchString(marker)) || (resume && len(digest) != sha256.Size*2) {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	query := url.Values{"fields": {
		"id", "project_id", "alias", "status", "type", "public_ip_address", "public_ipv6_address", "tags",
		"associate_instance_type", "associate_instance_id", "vnic",
	}}
	if marker != "" {
		query.Set("marker", marker)
	}
	result, err := c.readHuaweiElasticIPs(ctx, region, query)
	if err != nil {
		return provider.Page{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	page := provider.Page{Objects: make([]provider.Object, 0, len(result.Rows)), Next: result.Page.Next}
	seen := make(map[string]struct{}, len(result.Rows))
	for _, row := range result.Rows {
		if row.Project != c.profile.ProjectID {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if _, duplicate := seen[row.ID]; duplicate || !huaweiResourceID.MatchString(row.ID) || row.ID == marker {
			return provider.Page{}, invalid
		}
		seen[row.ID] = struct{}{}
		object, err := row.asset()
		if err != nil {
			return provider.Page{}, err
		}
		page.Objects = append(page.Objects, object)
	}
	return splitHuaweiElasticIPs(page, marker, digest, resume)
}

func splitHuaweiElasticIPs(page provider.Page, marker, digest string, resume bool) (provider.Page, error) {
	if len(page.Objects) <= provider.MaxPageObjects {
		if resume {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		return page, nil
	}
	encoded, err := json.Marshal(page)
	if err != nil {
		return provider.Page{}, err
	}
	revision := sha(encoded)
	if resume {
		if digest != revision {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		page.Objects = page.Objects[provider.MaxPageObjects:]
		return page, nil
	}
	page.Objects = page.Objects[:provider.MaxPageObjects]
	page.Next = marker + "~" + revision
	return page, nil
}

func (r huaweiElasticIP) asset() (provider.Object, error) {
	ipv4 := r.IPv4
	if r.Type == "DUALSTACK" && ipv4 == "0.0.0.0" {
		ipv4 = ""
	}
	object, err := elasticIPObject(r.ID, r.Alias, r.Status, huaweiElasticIPTags(r.Tags), ipv4, r.IPv6)
	if err != nil {
		return provider.Object{}, err
	}
	if r.Status == "ACTIVE" && r.AssociatedType == "PORT" && r.Vnic != nil &&
		huaweiResourceID.MatchString(r.Vnic.PortID) && huaweiResourceID.MatchString(r.AssociatedID) &&
		(r.AssociatedID == r.Vnic.PortID || r.AssociatedID == r.Vnic.DeviceID) {
		if strings.HasPrefix(r.Vnic.DeviceOwner, "compute:") && huaweiResourceID.MatchString(r.Vnic.DeviceID) {
			object.Link(provider.Instance, r.Vnic.DeviceID, "bound_to")
		}
		if huaweiResourceID.MatchString(r.Vnic.VpcID) {
			object.Link(provider.Network, r.Vnic.VpcID, "in_network")
		}
	}
	return object, nil
}

func huaweiElasticIPTags(values []string) []*tag {
	if values == nil {
		return nil
	}
	tags := make([]*tag, 0, len(values))
	for _, raw := range values {
		key, value, found := strings.Cut(raw, "=")
		if !found || key == "" {
			return nil
		}
		tags = append(tags, &tag{Key: key, Value: value})
	}
	return tags
}
