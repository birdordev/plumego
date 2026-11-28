package kvmapp

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmColdMigrationRoute = kvmVMRoute + "/cold-migration"
const kvmColdMigrationsRoute = kvmVMRoute + "/cold-migrations"
const kvmColdMigrationReceiptRoute = kvmColdMigrationsRoute + "/:migration"
const kvmColdMigrationEligibilityRoute = kvmVMRoute + "/cold-migration-eligibility"

func (h kvmHTTP) listColdMigrations(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	result, err := h.store.ListColdMigrations(ctx, token, scope, site, router.Param(r, "vm"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) getColdMigrationReceipt(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetColdMigrationReceipt(ctx, token, scope, site, router.Param(r, "vm"), router.Param(r, "migration"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) getColdMigrationEligibility(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetColdMigrationEligibility(ctx, token, scope, site, router.Param(r, "vm"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) getColdMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetColdMigration(ctx, token, scope, site, router.Param(r, "vm"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func (h kvmHTTP) requestColdMigration(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := transport.AccountRevision(r)
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
	revision, ok := transport.AccountRevision(r)
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
