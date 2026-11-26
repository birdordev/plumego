package publiccloud

import (
	"context"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

const huaweiDNSHost = "dns.cn-north-4.myhuaweicloud.com"

type huaweiDNSMetadata struct {
	Total *int `json:"total_count"`
}

func (c *Client) huaweiDNSZones(ctx context.Context, offset int) ([]provider.Object, *int, error) {
	query := dnsOffsetQuery(offset)
	query.Set("type", "public")
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: "/v2/zones", RawQuery: query.Encode()}
	var result struct {
		Metadata huaweiDNSMetadata
		Zones    []struct {
			ID, Name, Status string
			Project          string `json:"project_id"`
			Type             string `json:"zone_type"`
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return nil, nil, err
	}
	if result.Zones == nil {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	zones := make([]provider.Object, 0, len(result.Zones))
	for _, row := range result.Zones {
		if row.Project != c.profile.ProjectID || row.Type != "public" {
			return nil, nil, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !projectID.MatchString(row.ID) || row.Status == "" {
			return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object, err := dnsZoneObject(row.ID, row.Name, "", strings.ToLower(row.Status))
		if err != nil {
			return nil, nil, err
		}
		zones = append(zones, object)
	}
	return zones, result.Metadata.Total, nil
}

func (c *Client) huaweiDNSRecords(ctx context.Context, zone provider.DNSZoneRef, offset int, evidence *dnsCreationEvidence) ([]provider.DNSRecord, *int, error) {
	if !projectID.MatchString(zone.ID) {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: "/v2/zones/" + zone.ID + "/recordsets",
		RawQuery: dnsOffsetQuery(offset).Encode()}
	var result struct {
		Metadata   huaweiDNSMetadata
		Recordsets []struct {
			ID, Name, Type, Status, Description string
			Zone                                string `json:"zone_id"`
			ZoneName                            string `json:"zone_name"`
			Project                             string `json:"project_id"`
			TTL                                 *int
			Records                             []string
		}
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return nil, nil, err
	}
	if result.Recordsets == nil {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	records := make([]provider.DNSRecord, 0, len(result.Recordsets))
	for _, row := range result.Recordsets {
		if row.Project != c.profile.ProjectID || row.Zone != zone.ID || provider.DNSName(row.ZoneName, false) != zone.Name {
			return nil, nil, &provider.Failure{Kind: provider.Unauthorized}
		}
		record, err := newDNSRecord(row.ID, provider.DNSName(row.Name, true), row.Type, row.Status, "", row.TTL, nil, row.Records)
		if err != nil {
			return nil, nil, err
		}
		evidence.capture(record.ID, row.Description)
		records = append(records, record)
	}
	return records, result.Metadata.Total, nil
}
