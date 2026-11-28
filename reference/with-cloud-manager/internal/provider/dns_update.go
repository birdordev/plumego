package provider

import (
	"context"
	"net/netip"
	"slices"
)

type DNSRecordUpdate struct {
	Zone   DNSZoneRef
	Record DNSRecord
	Values []string
	TTL    int
}

type DNSRecordUpdater interface {
	PrepareDNSRecordUpdate(context.Context, Scope, DNSRecordUpdate) (func(context.Context) (string, error), error)
	ObserveDNSRecordUpdate(context.Context, Scope, DNSRecordUpdate) (ActionResult, error)
}

func NormalizeDNSUpdateValues(raw []string) ([]string, bool) {
	if len(raw) == 0 || len(raw) > 128 {
		return nil, false
	}
	values := make([]string, len(raw))
	for i, address := range raw {
		value, err := netip.ParseAddr(address)
		if err != nil || value.Zone() != "" {
			return nil, false
		}
		values[i] = value.String()
	}
	slices.Sort(values)
	values = slices.Compact(values)
	return values, len(values) == len(raw)
}

func (c DNSRecordUpdate) ValidFor(vendor string) bool {
	enabled, _ := DNSRecordEnabled(vendor, c.Record.Status)
	if !enabled || !validDNSAddressRecord(c.Zone, c.Record) || c.TTL < 1 || c.TTL > 604800 {
		return false
	}
	switch vendor {
	case TencentCVM, AlibabaECS:
		if len(c.Record.Values) != 1 || len(c.Values) != 1 || c.Record.Line == "" {
			return false
		}
	case HuaweiECS:
		if c.Record.Line != "" {
			return false
		}
	}
	values, ok := NormalizeDNSUpdateValues(c.Values)
	target := c.Target()
	return ok && slices.Equal(values, c.Values) && validDNSAddressRecord(c.Zone, target) && !c.Record.MatchesAddressRecord(target)
}

func (c DNSRecordUpdate) Target() DNSRecord {
	target := c.Record
	target.Values = c.Values
	target.ValueCount = len(c.Values)
	target.TTL = c.TTL
	return target
}
