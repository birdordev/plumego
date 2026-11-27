package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmSharedStoragePoolsRoute = kvmSiteRoute + "/shared-storage-pools"
const kvmSharedStoragePoolRoute = kvmSharedStoragePoolsRoute + "/:pool"

func (h kvmHTTP) registerSharedStorage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var spec kvm.SharedStorageSpec
	if err := decodeKVMBody(r, &spec, 1024); err != nil {
		return err
	}
	pool, err := h.store.RegisterSharedStorage(ctx, token, scope, site, spec)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/shared-storage-pools/"+url.PathEscape(pool.ID))
	h.write(w, r, http.StatusCreated, pool)
	return nil
}

func (h kvmHTTP) getSharedStorage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	withUsage := true
	if values, specified := r.URL.Query()["usage"]; specified {
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			return kvm.ErrInvalid
		}
		withUsage = values[0] == "true"
	}
	pool, err := h.store.GetSharedStorage(ctx, token, scope, site, router.Param(r, "pool"), withUsage)
	if err != nil {
		return err
	}
	if withUsage {
		h.write(w, r, http.StatusOK, pool)
	} else {
		h.write(w, r, http.StatusOK, pool.SharedStorageSpec)
	}
	return nil
}
