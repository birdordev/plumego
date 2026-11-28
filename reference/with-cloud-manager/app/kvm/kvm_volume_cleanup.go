package kvmapp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmVolumeCleanupsRoute = kvmPlacementRoute + "/volume-cleanups"
const kvmVolumeCleanupRoute = kvmVolumeCleanupsRoute + "/:cleanup"
const kvmHostVolumeCleanupsRoute = kvmHostRoute + "/volume-cleanups"
const kvmVolumeCleanupAgentRoute = kvmHostVolumeCleanupsRoute + "/:placement/:cleanup"

func (h kvmHTTP) requestVolumeCleanup(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := transport.AccountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.VolumeCleanupInput
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	placement := router.Param(r, "placement")
	result, err := h.store.RequestVolumeCleanup(ctx, token, scope, site, placement, revision, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/placements/"+url.PathEscape(placement)+"/volume-cleanups/"+url.PathEscape(result.ID))
	h.write(w, r, http.StatusAccepted, result)
	return nil
}

func (h kvmHTTP) getVolumeCleanup(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetVolumeCleanup(ctx, token, scope, site, router.Param(r, "placement"), router.Param(r, "cleanup"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) listVolumeCleanups(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query := r.URL.Query()
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		var err error
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return kvm.ErrInvalid
		}
	}
	result, err := h.store.ListVolumeCleanups(ctx, token, scope, site, router.Param(r, "host"), query.Get("after"), limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) reviewVolumeCleanup(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := transport.AccountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.VMReview
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	result, err := h.store.ReviewVolumeCleanup(ctx, token, scope, site, router.Param(r, "placement"), router.Param(r, "cleanup"), revision, input.Decision)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) resolveVolumeCleanup(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := transport.AccountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.VolumeCleanupResolution
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	result, err := h.store.ResolveFencedVolumeCleanup(ctx, token, scope, site, router.Param(r, "placement"), router.Param(r, "cleanup"), revision, input)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) applyVolumeCleanup(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.VolumeCleanupCommand
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	result, err := h.store.ApplyVolumeCleanupCommand(ctx, token, scope, site,
		router.Param(r, "host"), router.Param(r, "placement"), router.Param(r, "cleanup"), input)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}
