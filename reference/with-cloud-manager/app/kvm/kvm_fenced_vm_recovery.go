package kvmapp

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmFencedVMRecoveryRoute = kvmVMRoute + "/fence-recovery"

func (h kvmHTTP) getFencedVMRecovery(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetFencedVMRecovery(ctx, token, scope, site, router.Param(r, "vm"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) requestFencedVMRecovery(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := transport.AccountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.FencedVMRecoveryInput
	if err := decodeKVMBody(r, &input, 2048); err != nil {
		return err
	}
	result, err := h.store.RequestFencedVMRecovery(ctx, token, scope, site, router.Param(r, "vm"), revision, input)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusAccepted, result)
	return nil
}

func (h kvmHTTP) reviewFencedVMRecovery(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := transport.AccountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var review kvm.FencedVMRecoveryReview
	if err := decodeKVMBody(r, &review, 1024); err != nil {
		return err
	}
	result, err := h.store.ReviewFencedVMRecovery(ctx, token, scope, site, router.Param(r, "vm"), revision, review)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}
