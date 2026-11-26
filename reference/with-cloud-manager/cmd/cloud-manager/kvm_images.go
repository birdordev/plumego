package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmBootImagesRoute = kvmSiteRoute + "/images"
const kvmBootImageRoute = kvmBootImagesRoute + "/:image"
const kvmVMImageStageRoute = kvmVMJobRoute + "/image-plan"

func (h kvmHTTP) registerBootImage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.BootImageSpec
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	image, err := h.store.RegisterBootImage(ctx, token, scope, site, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/images/"+url.PathEscape(image.ID))
	h.write(w, r, http.StatusCreated, image)
	return nil
}

func (h kvmHTTP) getBootImage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	image, err := h.store.GetBootImage(ctx, token, scope, site, router.Param(r, "image"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, image)
	return nil
}

func (h kvmHTTP) authorizeImageStage(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var command kvm.VMCommand
	if err := decodeKVMBody(r, &command, 1024); err != nil {
		return err
	}
	vm, err := h.store.AuthorizeImageStage(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "vm"), command)
	if err != nil {
		return err
	}
	h.writeVM(w, r, http.StatusOK, vm)
	return nil
}
