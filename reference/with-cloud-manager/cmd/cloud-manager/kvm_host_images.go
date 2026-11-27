package main

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmHostImageRoute = kvmHostRoute + "/images/:image"
const kvmHostImageCheckRoute = kvmHostImageRoute + "/check"

func (h kvmHTTP) getHostImage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	receipt, err := h.store.GetHostImage(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "image"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, receipt)
	return nil
}

func (h kvmHTTP) checkHostImage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var agent kvm.AgentIdentity
	if err := decodeKVMBody(r, &agent, 1024); err != nil {
		return err
	}
	receipt, err := h.store.CheckHostImage(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "image"), agent)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, receipt)
	return nil
}

func (h kvmHTTP) reportHostImage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.HostImageReport
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	receipt, err := h.store.ReportHostImage(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "image"), input)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, receipt)
	return nil
}
