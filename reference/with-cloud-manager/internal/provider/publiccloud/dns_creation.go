package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type dnsCreationEvidence struct {
	marker string
	ids    []string
}

func (e *dnsCreationEvidence) capture(id, annotation string) {
	if e != nil && e.marker == annotation {
		e.ids = append(e.ids, id)
	}
}

func (c *Client) PrepareDNSRecordCreation(ctx context.Context, scope provider.Scope, zone provider.DNSZoneRef, spec provider.DNSRecordSpec, operation string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if !c.profile.AllowDNSRecordCreation {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if err := validateDNSCreation(zone, spec, operation); err != nil {
		return nil, err
	}
	records, err := c.ReadDNSRecords(ctx, scope, zone)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if spec.ConflictsWith(zone, record) {
			return nil, &provider.Failure{Kind: provider.PreconditionFailed}
		}
	}
	switch c.name {
	case provider.TencentCVM:
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createTencentDNSRecord(ctx, zone, spec, dnsCreationMarker(operation))
		}, nil
	case provider.AlibabaECS:
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createAlibabaDNSRecord(ctx, zone, spec)
		}, nil
	case provider.HuaweiECS:
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createHuaweiDNSRecord(ctx, zone, spec, dnsCreationMarker(operation))
		}, nil
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
}

func (c *Client) ObserveDNSRecordCreation(ctx context.Context, scope provider.Scope, zone provider.DNSZoneRef, spec provider.DNSRecordSpec, operation, receiptID string) (string, error) {
	if err := validateDNSCreation(zone, spec, operation); err != nil {
		return "", err
	}
	if (receiptID != "" && !provider.ValidDNSRecordID(receiptID)) || (c.name == provider.AlibabaECS && receiptID == "") {
		return "", &provider.Failure{Kind: provider.PreconditionFailed}
	}
	var evidence *dnsCreationEvidence
	if c.name != provider.AlibabaECS {
		evidence = &dnsCreationEvidence{marker: dnsCreationMarker(operation)}
	}
	records, err := c.readDNSRecords(ctx, scope, zone, evidence)
	if err != nil {
		return "", err
	}
	if evidence != nil {
		if len(evidence.ids) == 0 {
			return "", nil
		}
		if len(evidence.ids) != 1 || (receiptID != "" && evidence.ids[0] != receiptID) {
			return "", &provider.Failure{Kind: provider.PreconditionFailed}
		}
		receiptID = evidence.ids[0]
	}
	var found *provider.DNSRecord
	for i := range records {
		record := &records[i]
		if record.ID == receiptID {
			found = record
			continue
		}
		if spec.ConflictsWith(zone, *record) {
			return "", &provider.Failure{Kind: provider.PreconditionFailed}
		}
	}
	if found == nil {
		return "", nil
	}
	if !spec.Matches(*found) || found.Line != defaultDNSLine(c.name) {
		return "", &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if enabled, _ := provider.DNSRecordEnabled(c.name, found.Status); enabled {
		return receiptID, nil
	}
	return "", nil
}

func validateDNSCreation(zone provider.DNSZoneRef, spec provider.DNSRecordSpec, operation string) error {
	if normalized, ok := spec.Normalize(); !ok || normalized != spec ||
		!spec.InZone(zone) || !provider.ValidDNSRecordID(operation) {
		return &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return nil
}

func defaultDNSLine(vendor string) string {
	switch vendor {
	case provider.TencentCVM:
		return "0"
	case provider.AlibabaECS:
		return "default"
	default:
		return ""
	}
}

func dnsCreationMarker(operation string) string { return "plumego-dns:" + operation }

func dnsRelativeName(zone, name string) string {
	if name == zone {
		return "@"
	}
	return strings.TrimSuffix(name, "."+zone)
}

func (c *Client) createTencentDNSRecord(ctx context.Context, zone provider.DNSZoneRef, spec provider.DNSRecordSpec, marker string) (provider.LaunchReceipt, error) {
	id, err := strconv.ParseUint(zone.ID, 10, 64)
	if err != nil || id == 0 {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	params := struct {
		DomainID                                                uint64 `json:"DomainId"`
		Domain, SubDomain, RecordType, RecordLine, RecordLineId string
		Value, Remark                                           string
		TTL                                                     int
	}{
		DomainID: id, Domain: zone.Name, SubDomain: dnsRelativeName(zone.Name, spec.Name), RecordType: spec.Type,
		RecordLine: "默认", RecordLineId: "0", Value: spec.Value, Remark: marker, TTL: spec.TTL,
	}
	var result struct {
		RecordId  uint64
		RequestId string
	}
	if err := c.tencentDNS(ctx, "CreateRecord", params, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.RecordId == 0 {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.LaunchReceipt{RequestID: result.RequestId, ResourceID: strconv.FormatUint(result.RecordId, 10)}, nil
}

func (c *Client) createAlibabaDNSRecord(ctx context.Context, zone provider.DNSZoneRef, spec provider.DNSRecordSpec) (provider.LaunchReceipt, error) {
	query := url.Values{
		"DomainName": {zone.Name}, "RR": {dnsRelativeName(zone.Name, spec.Name)}, "Type": {spec.Type},
		"Value": {spec.Value}, "Line": {"default"}, "TTL": {strconv.Itoa(spec.TTL)},
	}
	var result struct{ RecordId, RequestId string }
	if err := c.alibaba(ctx, alibabaDNSHost, "AddDomainRecord", alibabaDNSVersion, query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if !provider.ValidDNSRecordID(result.RecordId) {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.LaunchReceipt{RequestID: result.RequestId, ResourceID: result.RecordId}, nil
}

func (c *Client) createHuaweiDNSRecord(ctx context.Context, zone provider.DNSZoneRef, spec provider.DNSRecordSpec, marker string) (provider.LaunchReceipt, error) {
	endpoint := url.URL{Scheme: "https", Host: huaweiDNSHost, Path: "/v2/zones/" + zone.ID + "/recordsets"}
	body, err := json.Marshal(struct {
		Name        string   `json:"name"`
		Type        string   `json:"type"`
		Description string   `json:"description"`
		TTL         int      `json:"ttl"`
		Records     []string `json:"records"`
	}{spec.Name + ".", spec.Type, marker, spec.TTL, []string{spec.Value}})
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.Header.Set("Content-Type", "application/json")
	var result struct {
		ID      string
		Zone    string `json:"zone_id"`
		Project string `json:"project_id"`
	}
	if err := c.request(req, &result, http.StatusCreated, http.StatusAccepted); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if !projectID.MatchString(result.ID) || result.Zone != zone.ID || result.Project != c.profile.ProjectID {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return provider.LaunchReceipt{ResourceID: result.ID}, nil
}
