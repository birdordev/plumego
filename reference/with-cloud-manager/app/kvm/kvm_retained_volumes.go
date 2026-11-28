package kvmapp

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmRetainedVolumesRoute = kvmVMRoute + "/retained-volumes"

func (h kvmHTTP) listRetainedVolumes(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	page, err := h.store.ListRetainedVolumes(ctx, token, scope, site, router.Param(r, "vm"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func (h kvmHTTP) listVolumeCleanupHistory(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	page, err := h.store.ListVolumeCleanupHistory(ctx, token, scope, site, router.Param(r, "placement"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}
