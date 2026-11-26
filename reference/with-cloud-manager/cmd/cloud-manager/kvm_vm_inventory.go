package main

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmVMInventoryRoute = kvmHostRoute + "/vm-inventory"

func (h kvmHTTP) lookupVMInventory(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var query kvm.VMInventoryQuery
	if err := decodeKVMBody(r, &query, 16<<10); err != nil {
		return err
	}
	records, err := h.store.LookupVMInventory(ctx, token, scope, site, router.Param(r, "host"), query)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, records)
	return nil
}
