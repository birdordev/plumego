package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmIPv4PoolsRoute = kvmSiteRoute + "/networks"
const kvmIPv4PoolRoute = kvmIPv4PoolsRoute + "/:network"
const kvmHostIPv4PoolRoute = kvmHostRoute + "/dhcp-plan/:network"

func (h kvmHTTP) registerIPv4Pool(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.IPv4PoolSpec
	if err := decodeKVMBody(r, &input, 2048); err != nil {
		return err
	}
	pool, err := h.store.RegisterIPv4Pool(ctx, token, scope, site, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/networks/"+url.PathEscape(pool.Network))
	h.write(w, r, http.StatusCreated, pool)
	return nil
}

func (h kvmHTTP) getIPv4Pool(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	pool, err := h.store.GetIPv4Pool(ctx, token, scope, site, router.Param(r, "network"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, pool)
	return nil
}

func (h kvmHTTP) hostIPv4Pool(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var agent kvm.AgentIdentity
	if err := decodeKVMBody(r, &agent, 1024); err != nil {
		return err
	}
	pool, err := h.store.HostIPv4Pool(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "network"), agent)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, pool)
	return nil
}
