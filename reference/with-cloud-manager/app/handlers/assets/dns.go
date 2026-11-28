package assets

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/inventory"
)

func DNSRecordsHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return ServeInventory(dnsRecordsQuery, store.ReadDNSRecords,
		func(w http.ResponseWriter, r *http.Request, result inventory.DNSRecordsPage) error {
			return contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}, logger)
}

func dnsRecordsQuery(r *http.Request) (inventory.DNSRecordsQuery, error) {
	if len(r.URL.RawQuery) > 8192 {
		return inventory.DNSRecordsQuery{}, inventory.ErrInvalidQuery
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values["account"]) != 1 {
		return inventory.DNSRecordsQuery{}, inventory.ErrInvalidQuery
	}
	query := inventory.DNSRecordsQuery{
		Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"), ID: router.Param(r, "asset"),
		Account: values.Get("account"), Limit: 50,
	}
	for key, entries := range values {
		if len(entries) != 1 {
			return inventory.DNSRecordsQuery{}, inventory.ErrInvalidQuery
		}
		if key == "account" {
			continue
		}
		number, err := strconv.Atoi(entries[0])
		if err != nil {
			return inventory.DNSRecordsQuery{}, inventory.ErrInvalidQuery
		}
		switch key {
		case "offset":
			query.Offset = number
		case "limit":
			query.Limit = number
		default:
			return inventory.DNSRecordsQuery{}, inventory.ErrInvalidQuery
		}
	}
	return query, nil
}
