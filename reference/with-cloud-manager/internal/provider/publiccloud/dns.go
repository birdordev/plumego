package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

const dnsPageSize = 100

func (c *Client) dnsScope(region string) error {
	if region != provider.DNSGlobalScope ||
		(c.name == provider.HuaweiECS && !slices.Contains(c.profile.Regions, "cn-north-4")) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	return nil
}

func (c *Client) discoverDNSZones(ctx context.Context, region, cursor string) (provider.Page, error) {
	if err := c.dnsScope(region); err != nil {
		return provider.Page{}, err
	}
	var offset, previousTotal int
	if cursor != "" {
		offsetCode, totalCode, ok := strings.Cut(cursor, ":")
		var err error
		offset, err = pageNumber(offsetCode, 0)
		if !ok || err != nil || offset == 0 || offset%dnsPageSize != 0 {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		previousTotal, err = pageNumber(totalCode, offset)
		if err != nil || previousTotal == offset {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
	}
	var rows []provider.Object
	var total *int
	var err error
	switch c.name {
	case provider.TencentCVM:
		rows, total, err = c.tencentDNSZones(ctx, offset, "")
	case provider.AlibabaECS:
		rows, total, err = c.alibabaDNSZones(ctx, offset)
	case provider.HuaweiECS:
		rows, total, err = c.huaweiDNSZones(ctx, offset)
	default:
		return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if err != nil {
		return provider.Page{}, err
	}
	if err := checkDNSPage(total, offset, len(rows), 1_000_000); err != nil {
		return provider.Page{}, err
	}
	if cursor != "" && previousTotal != *total {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	page := provider.Page{Objects: rows}
	if end := offset + len(rows); end < *total {
		page.Next = strconv.Itoa(end) + ":" + strconv.Itoa(*total)
	}
	if err := page.Validate(cursor); err != nil {
		return provider.Page{}, err
	}
	return page, nil
}

func (c *Client) ReadDNSRecords(ctx context.Context, scope provider.Scope, zone provider.DNSZoneRef) ([]provider.DNSRecord, error) {
	return c.readDNSRecords(ctx, scope, zone, nil)
}

func (c *Client) validateDNSRecordScope(scope provider.Scope, zone provider.DNSZoneRef) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if err := c.dnsScope(scope.Region); err != nil {
		return err
	}
	if scope.Kind != provider.DNSZone || !zone.Valid() {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	return nil
}

func (c *Client) readDNSRecords(ctx context.Context, scope provider.Scope, zone provider.DNSZoneRef, evidence *dnsCreationEvidence) ([]provider.DNSRecord, error) {
	if err := c.validateDNSRecordScope(scope, zone); err != nil {
		return nil, err
	}

	var fetch func(context.Context, provider.DNSZoneRef, int) ([]provider.DNSRecord, *int, error)
	switch c.name {
	case provider.TencentCVM:
		fetch = func(ctx context.Context, zone provider.DNSZoneRef, offset int) ([]provider.DNSRecord, *int, error) {
			return c.tencentDNSRecords(ctx, zone, offset, evidence)
		}
	case provider.AlibabaECS:
		if err := c.checkAlibabaDNSZone(ctx, zone); err != nil {
			return nil, err
		}
		fetch = c.alibabaDNSRecords
	case provider.HuaweiECS:
		fetch = func(ctx context.Context, zone provider.DNSZoneRef, offset int) ([]provider.DNSRecord, *int, error) {
			return c.huaweiDNSRecords(ctx, zone, offset, evidence)
		}
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}

	var size, projectedBytes int
	records := make([]provider.DNSRecord, 0)
	for offset := 0; ; offset += dnsPageSize {
		rows, total, err := fetch(ctx, zone, offset)
		if err != nil {
			return nil, err
		}
		if err := checkDNSPage(total, offset, len(rows), provider.MaxDNSRecords); err != nil {
			return nil, err
		}
		if offset > 0 && size != *total {
			return nil, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		size = *total
		projected, err := json.Marshal(rows)
		if err != nil {
			return nil, err
		}
		projectedBytes += len(projected)
		if projectedBytes > provider.MaxDNSProjectionBytes {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		records = append(records, rows...)
		if len(records) == size {
			break
		}
	}
	if c.name == provider.AlibabaECS {
		if err := c.checkAlibabaDNSZone(ctx, zone); err != nil {
			return nil, err
		}
	}
	if err := provider.NormalizeDNSRecords(zone, records); err != nil {
		return nil, err
	}
	return records, nil
}

func checkDNSPage(total *int, offset, count, limit int) error {
	if total == nil || *total < 0 || *total > limit || count > dnsPageSize {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if offset > *total || count != min(dnsPageSize, *total-offset) {
		return &provider.Failure{Kind: provider.ExpiredCursor}
	}
	return nil
}

func dnsZoneObject(id, name, punycode, status string) (provider.Object, error) {
	if punycode != "" {
		name = punycode
	}
	zone := provider.DNSZoneRef{ID: id, Name: provider.DNSName(name, false)}
	if !zone.Valid() {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.Object{ID: zone.ID, Name: zone.Name, Status: status}, nil
}

func dnsOffsetQuery(offset int) url.Values {
	return url.Values{"limit": {strconv.Itoa(dnsPageSize)}, "offset": {strconv.Itoa(offset)}}
}

func dnsNumberedQuery(offset int) url.Values {
	return url.Values{"PageSize": {strconv.Itoa(dnsPageSize)}, "PageNumber": {strconv.Itoa(offset/dnsPageSize + 1)}}
}

func newDNSRecord(id, name, typ, status, line string, ttl, priority *int, values []string) (provider.DNSRecord, error) {
	if ttl == nil {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	record := provider.DNSRecord{
		ID: id, Name: name, Type: strings.ToUpper(typ), Status: strings.ToLower(status), Line: line, TTL: *ttl,
	}
	if err := record.SetValues(values, priority); err != nil {
		return provider.DNSRecord{}, err
	}
	return record, nil
}
