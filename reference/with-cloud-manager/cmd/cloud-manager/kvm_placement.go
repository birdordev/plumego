package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

func (h kvmHTTP) reserve(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		return kvm.ErrInvalid
	}
	var input kvm.PlacementInput
	if err := decodeKVMBody(r, &input, 4096); err != nil {
		return err
	}
	placement, err := h.store.Reserve(ctx, token, scope, site, keys[0], input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/placements/"+url.PathEscape(placement.ID))
	h.write(w, r, http.StatusCreated, placement)
	return nil
}

func (h kvmHTTP) getPlacement(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	placement, err := h.store.GetPlacement(ctx, token, scope, site, router.Param(r, "placement"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, placement)
	return nil
}

func (h kvmHTTP) release(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	placement, err := h.store.Release(ctx, token, scope, site, router.Param(r, "placement"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, placement)
	return nil
}

func kvmSitePath(scope identity.Scope, site string) string {
	return "/v1/tenants/" + url.PathEscape(scope.Tenant) + "/projects/" + url.PathEscape(scope.Project) + "/kvm/sites/" + url.PathEscape(site)
}
