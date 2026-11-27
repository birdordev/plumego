package main

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

func (h kvmHTTP) getHostFence(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetHostFence(ctx, token, scope, site, router.Param(r, "host"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) changeHostFence(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.HostFenceChange
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	host, err := h.store.ChangeHostFence(ctx, token, scope, site, router.Param(r, "host"), revision, input)
	if err != nil {
		return err
	}
	h.writeHost(w, r, http.StatusOK, host)
	return nil
}
