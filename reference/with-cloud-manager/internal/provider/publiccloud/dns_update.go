package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareDNSRecordUpdate(ctx context.Context, scope provider.Scope, change provider.DNSRecordUpdate) (func(context.Context) (string, error), error) {
	if !c.profile.AllowDNSRecordUpdate {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	record, err := c.readDNSUpdateTarget(ctx, scope, change)
	if err != nil {
		return nil, err
	}
	if record.Status != change.Record.Status || !change.Record.MatchesAddressRecord(record) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.updateTencentDNSRecord(ctx, change)
		case provider.AlibabaECS:
			return c.updateAlibabaDNSRecord(ctx, change)
		case provider.HuaweiECS:
			return c.updateHuaweiDNSRecord(ctx, change)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}, nil
}

func (c *Client) ObserveDNSRecordUpdate(ctx context.Context, scope provider.Scope, change provider.DNSRecordUpdate) (provider.ActionResult, error) {
	record, err := c.readDNSUpdateTarget(ctx, scope, change)
	if err != nil {
		return "", err
	}
	desired := change.Target().MatchesAddressRecord(record)
	if record.Status == change.Record.Status && desired {
		return provider.ActionSucceeded, nil
	}
	if (desired || change.Record.MatchesAddressRecord(record)) &&
		(record.Status == change.Record.Status || (c.name == provider.HuaweiECS && record.Status == "pending_update")) {
		return provider.ActionPending, nil
	}
	return "", &provider.Failure{Kind: provider.PreconditionFailed}
}

func (c *Client) readDNSUpdateTarget(ctx context.Context, scope provider.Scope, change provider.DNSRecordUpdate) (provider.DNSRecord, error) {
	if !change.ValidFor(c.name) {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.Unsupported}
	}
	record, err := c.readDNSRecord(ctx, scope, change.Zone, change.Record.ID, true)
	if err != nil {
		return provider.DNSRecord{}, err
	}
	if err := c.verifyPublicDNSZone(ctx, change.Zone); err != nil {
		return provider.DNSRecord{}, err
	}
	return record, nil
}

func (c *Client) updateTencentDNSRecord(ctx context.Context, change provider.DNSRecordUpdate) (string, error) {
	target, err := tencentDNSRecordParams(change.Zone, change.Record.ID)
	if err != nil {
		return "", err
	}
	type field struct{ Key, Value string }
	params := struct {
		tencentDNSRecordInput
		FieldList []field
	}{target, []field{{"value", change.Values[0]}, {"ttl", strconv.Itoa(change.TTL)}}}
	var result struct {
		RecordId  uint64
		RequestId string
	}
	if err := c.tencentDNS(ctx, "ModifyRecordFields", params, &result); err != nil {
		return "", err
	}
	if result.RequestId == "" || result.RecordId != target.RecordID {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result.RequestId, nil
}

func (c *Client) updateAlibabaDNSRecord(ctx context.Context, change provider.DNSRecordUpdate) (string, error) {
	query := url.Values{
		"RecordId": {change.Record.ID}, "RR": {dnsRelativeName(change.Zone.Name, change.Record.Name)},
		"Type": {change.Record.Type}, "Line": {change.Record.Line}, "Value": {change.Values[0]}, "TTL": {strconv.Itoa(change.TTL)},
	}
	var result struct{ RecordId, RequestId string }
	if err := c.alibaba(ctx, alibabaDNSHost, "UpdateDomainRecord", alibabaDNSVersion, query, &result); err != nil {
		return "", err
	}
	if result.RecordId != change.Record.ID || result.RequestId == "" {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result.RequestId, nil
}

func (c *Client) updateHuaweiDNSRecord(ctx context.Context, change provider.DNSRecordUpdate) (string, error) {
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: "/v2.1/zones/" + change.Zone.ID + "/recordsets/" + change.Record.ID}
	body, err := json.Marshal(struct {
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		TTL     int      `json:"ttl"`
		Records []string `json:"records"`
	}{change.Record.Name + ".", change.Record.Type, change.TTL, change.Values})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.GetBody = nil
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("Content-Type", "application/json")
	var result struct {
		ID      string
		Zone    string `json:"zone_id"`
		Project string `json:"project_id"`
	}
	if err := c.request(req, &result, http.StatusAccepted); err != nil {
		return "", err
	}
	if result.ID != change.Record.ID || result.Zone != change.Zone.ID || result.Project != c.profile.ProjectID {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return "", nil
}
