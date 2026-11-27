package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmEvacuationBatchesRoute = kvmHostFenceRoute + "/evacuation-batches"
const kvmEvacuationBatchRoute = kvmEvacuationBatchesRoute + "/:batch"
const kvmEvacuationBatchBodyLimit = 8192

func (h kvmHTTP) requestEvacuationBatch(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.EvacuationBatchInput
	if err := decodeKVMBody(r, &input, kvmEvacuationBatchBodyLimit); err != nil {
		return err
	}
	host := router.Param(r, "host")
	result, err := h.store.RequestEvacuationBatch(ctx, token, scope, site, host, revision, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/hosts/"+url.PathEscape(host)+"/fence/evacuation-batches/"+url.PathEscape(result.ID))
	h.write(w, r, http.StatusAccepted, result)
	return nil
}

func (h kvmHTTP) getEvacuationBatch(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetEvacuationBatch(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "batch"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}
