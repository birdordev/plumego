package provider

import (
	"context"
	"slices"
)

type DNSRecordStatusChange struct {
	Zone    DNSZoneRef
	Record  DNSRecord
	Enabled bool
}

type DNSRecordStatusOperator interface {
	PrepareDNSRecordStatus(context.Context, Scope, DNSRecordStatusChange) (func(context.Context) (string, error), error)
	ObserveDNSRecordStatus(context.Context, Scope, DNSRecordStatusChange) (ActionResult, error)
}

func ValidDNSRecordID(id string) bool { return dnsID.MatchString(id) }

func DNSRecordEnabled(vendor, status string) (bool, bool) {
	switch vendor {
	case TencentCVM, AlibabaECS:
		return status == "enable", status == "enable" || status == "disable"
	case HuaweiECS:
		return status == "active", status == "active" || status == "disable"
	default:
		return false, false
	}
}

func (c DNSRecordStatusChange) ValidFor(vendor string) bool {
	enabled, stable := DNSRecordEnabled(vendor, c.Record.Status)
	return stable && enabled != c.Enabled && validDNSAddressRecord(c.Zone, c.Record)
}

func validDNSAddressRecord(zone DNSZoneRef, record DNSRecord) bool {
	return len(record.Values) > 0 && record.ValueCount == len(record.Values) &&
		(record.Type == "A" || record.Type == "AAAA") && NormalizeDNSRecords(zone, []DNSRecord{record}) == nil
}

// MatchesAddressRecord compares record data independently of its lifecycle status.
func (r DNSRecord) MatchesAddressRecord(record DNSRecord) bool {
	return record.ID == r.ID && record.Name == r.Name && record.Type == r.Type &&
		record.Line == r.Line && record.TTL == r.TTL && record.ValueCount == r.ValueCount &&
		slices.Equal(record.Values, r.Values)
}

func (c DNSRecordStatusChange) DesiredStatus(vendor string) string {
	switch {
	case !c.Enabled:
		return "disable"
	case vendor == HuaweiECS:
		return "active"
	default:
		return "enable"
	}
}
