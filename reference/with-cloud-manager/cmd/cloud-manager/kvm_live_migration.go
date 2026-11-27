package main

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmLiveMigrationsRoute = kvmVMRoute + "/live-migrations"
const kvmLiveMigrationRoute = kvmLiveMigrationsRoute + "/:migration"
const kvmLiveMigrationTargetsRoute = kvmVMRoute + "/live-migration-targets"
const kvmLiveMigrationAgentRoute = kvmHostRoute + "/live-migrations/:vm/:migration"

func (h kvmHTTP) listLiveMigrations(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	result, err := h.store.ListLiveMigrations(ctx, token, scope, site, router.Param(r, "vm"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) listLiveMigrationTargets(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	result, err := h.store.ListLiveMigrationTargets(ctx, token, scope, site, router.Param(r, "vm"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) requestLiveMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.LiveMigrationInput
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	vm := router.Param(r, "vm")
	result, err := h.store.RequestLiveMigration(ctx, token, scope, site, vm, revision, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/vms/"+url.PathEscape(vm)+"/live-migrations/"+url.PathEscape(result.ID))
	h.write(w, r, http.StatusAccepted, result)
	return nil
}

func (h kvmHTTP) getLiveMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetLiveMigration(ctx, token, scope, site, router.Param(r, "vm"), router.Param(r, "migration"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) reviewLiveMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.VMReview
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	result, err := h.store.ReviewLiveMigration(ctx, token, scope, site, router.Param(r, "vm"), router.Param(r, "migration"), revision, input.Decision)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) abortFencedLiveMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input struct {
		Evidence string `json:"evidence_sha256"`
	}
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	result, err := h.store.AbortFencedLiveMigration(ctx, token, scope, site, router.Param(r, "vm"), router.Param(r, "migration"), revision, input.Evidence)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) applyLiveMigrationCommand(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.LiveMigrationCommand
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	result, err := h.store.ApplyLiveMigrationCommand(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "vm"), router.Param(r, "migration"), input)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}
