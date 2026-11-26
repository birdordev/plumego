package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) tencentDNS(ctx context.Context, action string, params, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.tencent(ctx, "dnspod", "", action, "2021-03-23", body, result)
}

func (c *Client) tencentDNSZones(ctx context.Context, offset int, keyword string) ([]provider.Object, *int, error) {
	var result struct {
		DomainCountInfo struct{ DomainTotal *int }
		DomainList      []struct {
			DomainID               uint64 `json:"DomainId"`
			Name, Punycode, Status string
		}
	}
	params := struct {
		Type          string
		Offset, Limit int
		Keyword       string `json:",omitempty"`
	}{Type: "MINE", Offset: offset, Limit: dnsPageSize, Keyword: keyword}
	if err := c.tencentDNS(ctx, "DescribeDomainList", params, &result); err != nil {
		return nil, nil, err
	}
	if result.DomainList == nil {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	zones := make([]provider.Object, 0, len(result.DomainList))
	for _, row := range result.DomainList {
		if row.DomainID == 0 || row.Status == "" {
			return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object, err := dnsZoneObject(strconv.FormatUint(row.DomainID, 10), row.Name, row.Punycode, strings.ToLower(row.Status))
		if err != nil {
			return nil, nil, err
		}
		zones = append(zones, object)
	}
	return zones, result.DomainCountInfo.DomainTotal, nil
}

func (c *Client) tencentDNSRecords(ctx context.Context, zone provider.DNSZoneRef, offset int, evidence *dnsCreationEvidence) ([]provider.DNSRecord, *int, error) {
	domainID, err := strconv.ParseUint(zone.ID, 10, 64)
	if err != nil || domainID == 0 {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	var result struct {
		RecordCountInfo struct{ TotalCount *int }
		RecordList      []struct {
			RecordID                           uint64 `json:"RecordId"`
			Name, Type, Status, LineId, Remark string
			Value                              *string
			TTL, MX                            *int
		}
	}
	params := struct {
		DomainID      uint64 `json:"DomainId"`
		Domain        string
		Offset, Limit int
		ErrorOnEmpty  string
	}{domainID, zone.Name, offset, dnsPageSize, "no"}
	if err := c.tencentDNS(ctx, "DescribeRecordList", params, &result); err != nil {
		return nil, nil, err
	}
	if result.RecordList == nil {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	records := make([]provider.DNSRecord, 0, len(result.RecordList))
	for _, row := range result.RecordList {
		if row.RecordID == 0 || row.Value == nil || (strings.EqualFold(row.Type, "MX") && row.MX == nil) {
			return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		record, err := newDNSRecord(strconv.FormatUint(row.RecordID, 10), provider.DNSRecordOwner(zone.Name, row.Name),
			row.Type, row.Status, row.LineId, row.TTL, row.MX, []string{*row.Value})
		if err != nil {
			return nil, nil, err
		}
		evidence.capture(record.ID, row.Remark)
		records = append(records, record)
	}
	return records, result.RecordCountInfo.TotalCount, nil
}
