package provider

import "context"

// DNSRecordDeletion binds a deletion to the observed disabled address record.
type DNSRecordDeletion struct {
	Zone   DNSZoneRef
	Record DNSRecord
}

func (d DNSRecordDeletion) Valid() bool {
	return d.Record.Status == "disable" && validDNSAddressRecord(d.Zone, d.Record)
}

type DNSRecordDeleter interface {
	PrepareDNSRecordDeletion(context.Context, Scope, DNSRecordDeletion) (func(context.Context) (string, error), error)
	ObserveDNSRecordDeletion(context.Context, Scope, DNSRecordDeletion) (ActionResult, error)
}
