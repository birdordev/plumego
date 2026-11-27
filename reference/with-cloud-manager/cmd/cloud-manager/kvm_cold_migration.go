package main

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmColdMigrationRoute = kvmVMRoute + "/cold-migration"

func (h kvmHTTP) getColdMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetColdMigration(ctx, token, scope, site, router.Param(r, "vm"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) requestColdMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.ColdMigrationInput
	if err := decodeKVMBody(r, &input, 2048); err != nil {
		return err
	}
	result, err := h.store.RequestColdMigration(ctx, token, scope, site, router.Param(r, "vm"), revision, input)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusAccepted, result)
	return nil
}

func (h kvmHTTP) reviewColdMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.ColdMigrationReview
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	result, err := h.store.ReviewColdMigration(ctx, token, scope, site, router.Param(r, "vm"), revision, input)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}
