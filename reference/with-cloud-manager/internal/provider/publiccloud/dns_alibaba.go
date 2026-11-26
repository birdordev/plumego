package publiccloud

import (
	"context"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

const alibabaDNSHost = "alidns.aliyuncs.com"
const alibabaDNSVersion = "2015-01-09"

type alibabaDNSZone struct {
	DomainId, DomainName, PunyCode string
}

func (row alibabaDNSZone) object() (provider.Object, error) {
	return dnsZoneObject(row.DomainId, row.DomainName, row.PunyCode, "configured")
}

func (c *Client) alibabaDNSZones(ctx context.Context, offset int) ([]provider.Object, *int, error) {
	var result struct {
		alibabaNumberedPage
		Domains struct{ Domain []alibabaDNSZone }
	}
	query := dnsNumberedQuery(offset)
	if err := c.alibaba(ctx, alibabaDNSHost, "DescribeDomains", alibabaDNSVersion, query, &result); err != nil {
		return nil, nil, err
	}
	if result.Domains.Domain == nil || result.PageNumber != offset/dnsPageSize+1 || result.PageSize != dnsPageSize {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	zones := make([]provider.Object, 0, len(result.Domains.Domain))
	for _, row := range result.Domains.Domain {
		object, err := row.object()
		if err != nil {
			return nil, nil, err
		}
		zones = append(zones, object)
	}
	return zones, result.TotalCount, nil
}

func (c *Client) checkAlibabaDNSZone(ctx context.Context, zone provider.DNSZoneRef) error {
	var result alibabaDNSZone
	query := url.Values{"DomainName": {zone.Name}}
	if err := c.alibaba(ctx, alibabaDNSHost, "DescribeDomainInfo", alibabaDNSVersion, query, &result); err != nil {
		return err
	}
	object, err := result.object()
	if err != nil {
		return err
	}
	if object.ID != zone.ID || object.Name != zone.Name {
		return &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return nil
}

func (c *Client) alibabaDNSRecords(ctx context.Context, zone provider.DNSZoneRef, offset int) ([]provider.DNSRecord, *int, error) {
	var result struct {
		alibabaNumberedPage
		DomainRecords struct {
			Record []struct {
				RecordId, RR, Type, Status, Line string
				TTL, Priority                    *int
				Value                            *string
			}
		}
	}
	query := dnsNumberedQuery(offset)
	query.Set("DomainName", zone.Name)
	if err := c.alibaba(ctx, alibabaDNSHost, "DescribeDomainRecords", alibabaDNSVersion, query, &result); err != nil {
		return nil, nil, err
	}
	rows := result.DomainRecords.Record
	if rows == nil || result.PageNumber != offset/dnsPageSize+1 || result.PageSize != dnsPageSize {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	records := make([]provider.DNSRecord, 0, len(rows))
	for _, row := range rows {
		if row.Value == nil || (strings.EqualFold(row.Type, "MX") && row.Priority == nil) {
			return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		record, err := newDNSRecord(row.RecordId, provider.DNSRecordOwner(zone.Name, row.RR), row.Type, row.Status, row.Line,
			row.TTL, row.Priority, []string{*row.Value})
		if err != nil {
			return nil, nil, err
		}
		records = append(records, record)
	}
	return records, result.TotalCount, nil
}
