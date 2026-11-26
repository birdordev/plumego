package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmVMsRoute = kvmSiteRoute + "/vms"
const kvmVMRoute = kvmVMsRoute + "/:vm"
const kvmVMShutdownRoute = kvmVMRoute + "/shutdown"
const kvmVMRetirementRoute = kvmVMRoute + "/retirement"
const kvmVMPollRoute = kvmHostRoute + "/vm-work"
const kvmVMJobRoute = kvmVMPollRoute + "/:vm"

func (h kvmHTTP) submitVM(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.VMInput
	if err := decodeKVMBody(r, &input, 2048); err != nil {
		return err
	}
	vm, err := h.store.SubmitVM(ctx, token, scope, site, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/vms/"+url.PathEscape(vm.ID))
	h.writeVM(w, r, http.StatusAccepted, vm)
	return nil
}

func (h kvmHTTP) getVM(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	vm, err := h.store.GetVM(ctx, token, scope, site, router.Param(r, "vm"))
	if err != nil {
		return err
	}
	h.writeVM(w, r, http.StatusOK, vm)
	return nil
}

func (h kvmHTTP) requestVMShutdown(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	vm, err := h.store.RequestVMShutdown(ctx, token, scope, site, router.Param(r, "vm"), revision)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/vms/"+url.PathEscape(vm.ID))
	h.writeVM(w, r, http.StatusAccepted, vm)
	return nil
}

func (h kvmHTTP) requestVMRetirement(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	vm, err := h.store.RequestVMRetirement(ctx, token, scope, site, router.Param(r, "vm"), revision)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/vms/"+url.PathEscape(vm.ID))
	h.writeVM(w, r, http.StatusAccepted, vm)
	return nil
}

func (h kvmHTTP) reviewVM(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.VMReview
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	vm, err := h.store.ReviewVM(ctx, token, scope, site, router.Param(r, "vm"), revision, input)
	if err != nil {
		return err
	}
	h.writeVM(w, r, http.StatusOK, vm)
	return nil
}

func (h kvmHTTP) pollVM(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var agent kvm.AgentIdentity
	if err := decodeKVMBody(r, &agent, 1024); err != nil {
		return err
	}
	plan, err := h.store.PollVM(ctx, token, scope, site, router.Param(r, "host"), agent)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, plan)
	return nil
}

func (h kvmHTTP) armVM(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.VMCommand
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	vm, err := h.store.ArmVM(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "vm"), input)
	if err != nil {
		return err
	}
	h.writeVM(w, r, http.StatusOK, vm)
	return nil
}

func (h kvmHTTP) observeVM(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.VMObservation
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	vm, err := h.store.ObserveVM(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "vm"), input)
	if err != nil {
		return err
	}
	h.writeVM(w, r, http.StatusOK, vm)
	return nil
}
