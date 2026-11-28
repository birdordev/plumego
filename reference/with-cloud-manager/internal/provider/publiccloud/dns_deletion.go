package publiccloud

import (
	"context"
	"net/http"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareDNSRecordDeletion(ctx context.Context, scope provider.Scope, target provider.DNSRecordDeletion) (func(context.Context) (string, error), error) {
	if !c.profile.AllowDNSRecordDeletion {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	if !target.Valid() {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err := c.validateDNSRecordScope(scope, target.Zone); err != nil {
		return nil, err
	}
	if err := c.verifyPublicDNSZone(ctx, target.Zone); err != nil {
		return nil, err
	}
	record, err := c.readDNSAddressRecord(ctx, scope, target.Zone, target.Record, true)
	if err != nil {
		return nil, err
	}
	if record.Status != target.Record.Status {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		return c.deleteDNSRecord(ctx, target)
	}, nil
}

func (c *Client) ObserveDNSRecordDeletion(ctx context.Context, scope provider.Scope, target provider.DNSRecordDeletion) (provider.ActionResult, error) {
	if !target.Valid() {
		return "", &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err := c.validateDNSRecordScope(scope, target.Zone); err != nil {
		return "", err
	}

	// 阿里云的完整读取已在分页前后校验域名和 ID。
	if c.name != provider.AlibabaECS {
		if err := c.verifyPublicDNSZone(ctx, target.Zone); err != nil {
			return "", err
		}
	}
	records, err := c.ReadDNSRecords(ctx, scope, target.Zone)
	if err != nil {
		return "", err
	}
	if c.name != provider.AlibabaECS {
		if err := c.verifyPublicDNSZone(ctx, target.Zone); err != nil {
			return "", err
		}
	}
	for _, record := range records {
		if record.ID != target.Record.ID {
			continue
		}
		stillRemoving := c.name == provider.HuaweiECS && record.Status == "pending_delete"
		if !target.Record.MatchesAddressRecord(record) || (record.Status != target.Record.Status && !stillRemoving) {
			return "", &provider.Failure{Kind: provider.PreconditionFailed}
		}
		return provider.ActionPending, nil
	}
	return provider.ActionSucceeded, nil
}

func (c *Client) verifyPublicDNSZone(ctx context.Context, zone provider.DNSZoneRef) error {
	switch c.name {
	case provider.TencentCVM:
		return c.verifyTencentDNSZone(ctx, zone)
	case provider.AlibabaECS:
		return c.checkAlibabaDNSZone(ctx, zone)
	case provider.HuaweiECS:
		return c.verifyHuaweiDNSZone(ctx, zone)
	default:
		return &provider.Failure{Kind: provider.Unsupported}
	}
}

func (c *Client) verifyTencentDNSZone(ctx context.Context, zone provider.DNSZoneRef) error {
	var expected int
	for offset := 0; ; offset += dnsPageSize {
		rows, total, err := c.tencentDNSZones(ctx, offset, zone.Name)
		if err != nil {
			return err
		}
		if err := checkDNSPage(total, offset, len(rows), 1_000_000); err != nil {
			return err
		}
		if offset > 0 && expected != *total {
			return &provider.Failure{Kind: provider.ExpiredCursor}
		}
		expected = *total
		for _, row := range rows {
			if row.ID == zone.ID && row.Name == zone.Name {
				return nil
			}
		}
		if offset+len(rows) == expected {
			return &provider.Failure{Kind: provider.PreconditionFailed}
		}
	}
}

func (c *Client) verifyHuaweiDNSZone(ctx context.Context, zone provider.DNSZoneRef) error {
	if !projectID.MatchString(zone.ID) {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: "/v2/zones/" + zone.ID}
	var result struct {
		ID, Name string
		Kind     string `json:"zone_type"`
		Project  string `json:"project_id"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return err
	}
	if result.Project != c.profile.ProjectID || result.Kind != "public" {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if result.ID != zone.ID || provider.DNSName(result.Name, false) != zone.Name {
		return &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return nil
}

func (c *Client) deleteDNSRecord(ctx context.Context, target provider.DNSRecordDeletion) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		params, err := tencentDNSRecordParams(target.Zone, target.Record.ID)
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		err = c.tencentDNS(ctx, "DeleteRecord", params, &result)
		if err == nil && result.RequestId == "" {
			err = &provider.Failure{Kind: provider.InvalidResponse}
		}
		return result.RequestId, err
	case provider.AlibabaECS:
		query := url.Values{"RecordId": {target.Record.ID}}
		var result struct{ RequestId, RecordId string }
		err := c.alibaba(ctx, alibabaDNSHost, "DeleteDomainRecord", alibabaDNSVersion, query, &result)
		if err == nil && (result.RequestId == "" || result.RecordId != target.Record.ID) {
			err = &provider.Failure{Kind: provider.InvalidResponse}
		}
		return result.RequestId, err
	case provider.HuaweiECS:
		return "", c.deleteHuaweiDNSRecord(ctx, target)
	default:
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
}

func (c *Client) deleteHuaweiDNSRecord(ctx context.Context, target provider.DNSRecordDeletion) error {
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: "/v2/zones/" + target.Zone.ID + "/recordsets/" + target.Record.ID}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		ID      string
		Zone    string `json:"zone_id"`
		Project string `json:"project_id"`
	}
	if err := c.request(req, &result, http.StatusAccepted); err != nil {
		return err
	}
	if result.ID != target.Record.ID || result.Zone != target.Zone.ID || result.Project != c.profile.ProjectID {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}
