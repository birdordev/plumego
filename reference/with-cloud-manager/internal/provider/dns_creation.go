package provider

import (
	"context"
	"strings"
)

type DNSRecordSpec struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   int    `json:"ttl"`
}

func (s DNSRecordSpec) Normalize() (DNSRecordSpec, bool) {
	s.Name = DNSName(s.Name, true)
	s.Type = strings.ToUpper(s.Type)
	if s.Name == "" || (s.Type != "A" && s.Type != "AAAA") || s.TTL < 1 || s.TTL > 604800 {
		return DNSRecordSpec{}, false
	}
	s.Value = dnsValue(s.Type, s.Value, nil)
	return s, s.Value != ""
}

func (s DNSRecordSpec) InZone(zone DNSZoneRef) bool {
	return zone.Valid() && (s.Name == zone.Name || strings.HasSuffix(s.Name, "."+zone.Name))
}

func (s DNSRecordSpec) ConflictsWith(zone DNSZoneRef, record DNSRecord) bool {
	if record.Name == s.Name && (record.Type == s.Type || record.Type == "CNAME") {
		return true
	}
	return (record.Type == "NS" || record.Type == "DNAME") && record.Name != zone.Name &&
		(s.Name == record.Name || strings.HasSuffix(s.Name, "."+record.Name))
}

func (s DNSRecordSpec) Matches(record DNSRecord) bool {
	return record.Name == s.Name && record.Type == s.Type && record.TTL == s.TTL &&
		record.ValueCount == 1 && len(record.Values) == 1 && record.Values[0] == s.Value
}

type DNSRecordCreator interface {
	PrepareDNSRecordCreation(context.Context, Scope, DNSZoneRef, DNSRecordSpec, string) (func(context.Context) (LaunchReceipt, error), error)
	ObserveDNSRecordCreation(context.Context, Scope, DNSZoneRef, DNSRecordSpec, string, string) (string, error)
}
