package provider

import (
	"cmp"
	"context"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	DNSGlobalScope        = "global"
	MaxDNSRecords         = 5000
	MaxDNSProjectionBytes = 4 << 20
)

var (
	dnsID    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	dnsLabel = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)
	dnsLine  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.=-]{0,79}$`)
	dnsCode  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
)

type DNSZoneRef struct {
	ID, Name string
}

func (z DNSZoneRef) Valid() bool {
	return dnsID.MatchString(z.ID) && DNSName(z.Name, false) == z.Name && z.Name != ""
}

type DNSRecordReader interface {
	ReadDNSRecords(context.Context, Scope, DNSZoneRef) ([]DNSRecord, error)
}

type DNSRecord struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	TTL        int      `json:"ttl"`
	Status     string   `json:"status"`
	Line       string   `json:"line,omitempty"`
	ValueCount int      `json:"value_count"`
	Values     []string `json:"values,omitempty"`
}

// DNSName 将 ASCII 域名转换为不带尾点的小写形式。
func DNSName(raw string, owner bool) string {
	name := strings.ToLower(strings.TrimSuffix(raw, "."))
	if len(name) == 0 || len(name) > 253 {
		return ""
	}
	for i, label := range strings.Split(name, ".") {
		if owner && i == 0 && label == "*" {
			continue
		}
		if !dnsLabel.MatchString(label) || (!owner && strings.Contains(label, "_")) {
			return ""
		}
	}
	return name
}

func DNSRecordOwner(zone, relative string) string {
	if relative == "@" {
		return zone
	}
	if relative == "" || strings.HasSuffix(relative, ".") {
		return ""
	}
	return DNSName(relative+"."+zone, true)
}

// SetValues 只投影具有明确值语法的记录类型。
func (r *DNSRecord) SetValues(raw []string, mxPriority *int) error {
	if raw == nil || len(raw) > 128 {
		return &Failure{Kind: InvalidResponse}
	}
	r.ValueCount = len(raw)
	r.Values = nil
	switch r.Type {
	case "A", "AAAA", "CNAME", "NS", "PTR", "MX":
		r.Values = make([]string, 0, len(raw))
		for _, value := range raw {
			normalized := dnsValue(r.Type, value, mxPriority)
			if normalized == "" {
				return &Failure{Kind: InvalidResponse}
			}
			r.Values = append(r.Values, normalized)
		}
		slices.Sort(r.Values)
	}
	return nil
}

func dnsValue(typ, value string, mxPriority *int) string {
	switch typ {
	case "A", "AAAA":
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Zone() != "" || (typ == "A") != ip.Is4() {
			return ""
		}
		return ip.String()
	case "MX":
		var priority int
		if mxPriority == nil {
			parts := strings.Fields(value)
			if len(parts) != 2 {
				return ""
			}
			var err error
			priority, err = strconv.Atoi(parts[0])
			if err != nil {
				return ""
			}
			value = parts[1]
		} else {
			priority = *mxPriority
		}
		if priority < 0 || priority > 65535 {
			return ""
		}
		if value == "." && priority == 0 {
			return "0 ."
		}
		if server := DNSName(value, false); server != "" {
			return strconv.Itoa(priority) + " " + server
		}
		return ""
	default:
		return DNSName(value, false)
	}
}

func NormalizeDNSRecords(zone DNSZoneRef, records []DNSRecord) error {
	if !zone.Valid() || records == nil || len(records) > MaxDNSRecords {
		return &Failure{Kind: InvalidResponse}
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		_, duplicate := seen[record.ID]
		if duplicate || !dnsID.MatchString(record.ID) || record.Name == "" || DNSName(record.Name, true) != record.Name ||
			(record.Name != zone.Name && !strings.HasSuffix(record.Name, "."+zone.Name)) ||
			!dnsCode.MatchString(record.Type) || record.TTL < 0 || record.TTL > 1<<31-1 ||
			!dnsCode.MatchString(record.Status) || (record.Line != "" && !dnsLine.MatchString(record.Line)) ||
			record.ValueCount < 0 || record.ValueCount > 128 {
			return &Failure{Kind: InvalidResponse}
		}
		seen[record.ID] = struct{}{}
		switch record.Type {
		case "A", "AAAA", "CNAME", "NS", "PTR", "MX":
			if len(record.Values) != record.ValueCount {
				return &Failure{Kind: InvalidResponse}
			}
			for _, value := range record.Values {
				if dnsValue(record.Type, value, nil) != value {
					return &Failure{Kind: InvalidResponse}
				}
			}
		default:
			if len(record.Values) != 0 {
				return &Failure{Kind: InvalidResponse}
			}
		}
	}
	slices.SortFunc(records, func(a, b DNSRecord) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Type, b.Type), cmp.Compare(a.ID, b.ID))
	})
	return nil
}
