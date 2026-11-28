package publiccloud

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareDNSRecordStatus(ctx context.Context, scope provider.Scope, change provider.DNSRecordStatusChange) (func(context.Context) (string, error), error) {
	if !c.profile.AllowDNSRecordStatus {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	found, err := c.readDNSStatusTarget(ctx, scope, change)
	if err != nil {
		return nil, err
	}
	if found.Status != change.Record.Status {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.changeTencentDNSStatus(ctx, change)
		case provider.AlibabaECS:
			return c.changeAlibabaDNSStatus(ctx, change)
		case provider.HuaweiECS:
			return c.changeHuaweiDNSStatus(ctx, change)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}, nil
}

func (c *Client) ObserveDNSRecordStatus(ctx context.Context, scope provider.Scope, change provider.DNSRecordStatusChange) (provider.ActionResult, error) {
	record, err := c.readDNSStatusTarget(ctx, scope, change)
	if err != nil {
		return "", err
	}
	if record.Status == change.DesiredStatus(c.name) {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}

func (c *Client) readDNSStatusTarget(ctx context.Context, scope provider.Scope, change provider.DNSRecordStatusChange) (provider.DNSRecord, error) {
	if !change.ValidFor(c.name) {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.Unsupported}
	}
	return c.readDNSAddressRecord(ctx, scope, change.Zone, change.Record, false)
}

func (c *Client) readDNSAddressRecord(ctx context.Context, scope provider.Scope, zone provider.DNSZoneRef, expected provider.DNSRecord, deleting bool) (provider.DNSRecord, error) {
	record, err := c.readDNSRecord(ctx, scope, zone, expected.ID, deleting)
	if err != nil {
		return provider.DNSRecord{}, err
	}
	if !expected.MatchesAddressRecord(record) {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return record, nil
}

func (c *Client) readDNSRecord(ctx context.Context, scope provider.Scope, zone provider.DNSZoneRef, id string, restrictedHuawei bool) (provider.DNSRecord, error) {
	if err := c.validateDNSRecordScope(scope, zone); err != nil {
		return provider.DNSRecord{}, err
	}
	var record provider.DNSRecord
	var err error
	switch c.name {
	case provider.TencentCVM:
		record, err = c.tencentDNSRecord(ctx, zone, id)
	case provider.AlibabaECS:
		record, err = c.alibabaDNSRecord(ctx, zone, id)
	case provider.HuaweiECS:
		record, err = c.huaweiDNSRecord(ctx, zone, id, restrictedHuawei)
	default:
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if err != nil {
		return provider.DNSRecord{}, err
	}
	if err := provider.NormalizeDNSRecords(zone, []provider.DNSRecord{record}); err != nil {
		return provider.DNSRecord{}, err
	}
	return record, nil
}

type tencentDNSRecordInput struct {
	DomainID uint64 `json:"DomainId"`
	RecordID uint64 `json:"RecordId"`
	Domain   string
	Status   string `json:",omitempty"`
}

func tencentDNSRecordParams(ref provider.DNSZoneRef, id string) (tencentDNSRecordInput, error) {
	zone, zoneErr := strconv.ParseUint(ref.ID, 10, 64)
	record, recordErr := strconv.ParseUint(id, 10, 64)
	if zoneErr != nil || recordErr != nil || zone == 0 || record == 0 {
		return tencentDNSRecordInput{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return tencentDNSRecordInput{DomainID: zone, RecordID: record, Domain: ref.Name}, nil
}

func (c *Client) tencentDNSRecord(ctx context.Context, zone provider.DNSZoneRef, id string) (provider.DNSRecord, error) {
	params, err := tencentDNSRecordParams(zone, id)
	if err != nil {
		return provider.DNSRecord{}, err
	}
	var result struct {
		RecordInfo struct {
			Id, DomainId                        uint64
			SubDomain, RecordType, RecordLineId string
			Enabled                             *int
			Value                               *string
			TTL                                 *int
		}
	}
	if err := c.tencentDNS(ctx, "DescribeRecord", params, &result); err != nil {
		return provider.DNSRecord{}, err
	}
	row := result.RecordInfo
	if row.Id != params.RecordID || row.DomainId != params.DomainID || row.Value == nil || row.Enabled == nil || (*row.Enabled != 0 && *row.Enabled != 1) {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	state := "disable"
	if *row.Enabled == 1 {
		state = "enable"
	}
	return newDNSRecord(id, provider.DNSRecordOwner(zone.Name, row.SubDomain),
		row.RecordType, state, row.RecordLineId, row.TTL, nil, []string{*row.Value})
}

func (c *Client) changeTencentDNSStatus(ctx context.Context, change provider.DNSRecordStatusChange) (string, error) {
	params, err := tencentDNSRecordParams(change.Zone, change.Record.ID)
	if err != nil {
		return "", err
	}
	params.Status = "DISABLE"
	if change.Enabled {
		params.Status = "ENABLE"
	}
	var result struct {
		RecordId  uint64
		RequestId string
	}
	err = c.tencentDNS(ctx, "ModifyRecordStatus", params, &result)
	if err == nil && (result.RecordId != params.RecordID || result.RequestId == "") {
		err = &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result.RequestId, err
}

func (c *Client) alibabaDNSRecord(ctx context.Context, zone provider.DNSZoneRef, id string) (provider.DNSRecord, error) {
	var result struct {
		DomainId, RecordId, RR, Type, Status, Line string
		TTL                                        *int
		Value                                      *string
		Locked                                     *bool
	}
	query := url.Values{"RecordId": {id}}
	if err := c.alibaba(ctx, alibabaDNSHost, "DescribeDomainRecordInfo", alibabaDNSVersion, query, &result); err != nil {
		return provider.DNSRecord{}, err
	}
	if result.DomainId != zone.ID || result.RecordId != id || result.Value == nil || result.Locked == nil {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if *result.Locked {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return newDNSRecord(result.RecordId, provider.DNSRecordOwner(zone.Name, result.RR), result.Type, result.Status, result.Line,
		result.TTL, nil, []string{*result.Value})
}

func (c *Client) changeAlibabaDNSStatus(ctx context.Context, change provider.DNSRecordStatusChange) (string, error) {
	state := "Disable"
	if change.Enabled {
		state = "Enable"
	}
	var result struct{ RecordId, RequestId, Status string }
	query := url.Values{"RecordId": {change.Record.ID}, "Status": {state}}
	err := c.alibaba(ctx, alibabaDNSHost, "SetDomainRecordStatus", alibabaDNSVersion, query, &result)
	if err == nil && (result.RequestId == "" || result.RecordId != change.Record.ID || result.Status != state) {
		err = &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result.RequestId, err
}

func (c *Client) huaweiDNSRecord(ctx context.Context, zone provider.DNSZoneRef, id string, restricted bool) (provider.DNSRecord, error) {
	if !projectID.MatchString(zone.ID) || !projectID.MatchString(id) {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	version := "/v2"
	if restricted {
		version = "/v2.1"
	}
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: version + "/zones/" + zone.ID + "/recordsets/" + id}
	var result struct {
		ID, Name, Type, Status string
		Zone                   string `json:"zone_id"`
		ZoneName               string `json:"zone_name"`
		Project                string `json:"project_id"`
		TTL                    *int
		Records                []string
		Default                *bool
		Line                   string
		HealthCheckID          string    `json:"health_check_id"`
		AliasTarget            *struct{} `json:"alias_target"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return provider.DNSRecord{}, err
	}
	if result.ID != id || result.Zone != zone.ID || result.Project != c.profile.ProjectID ||
		provider.DNSName(result.ZoneName, false) != zone.Name {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	// v2.1 返回修改或删除前需要核对的线路和资源关联。
	if restricted && (result.Default == nil || *result.Default || result.Line != "default_view" ||
		result.HealthCheckID != "" || result.AliasTarget != nil) {
		return provider.DNSRecord{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return newDNSRecord(result.ID, provider.DNSName(result.Name, true), result.Type, result.Status, "", result.TTL, nil, result.Records)
}

func (c *Client) changeHuaweiDNSStatus(ctx context.Context, change provider.DNSRecordStatusChange) (string, error) {
	state := "DISABLE"
	if change.Enabled {
		state = "ENABLE"
	}
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: "/v2.1/recordsets/" + change.Record.ID + "/statuses/set"}
	body := fmt.Appendf(nil, `{"status":%q}`, state)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
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
