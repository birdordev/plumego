package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmSiteRoute = "/v1/tenants/:tenant/projects/:project/kvm/sites/:site"
const kvmHostsRoute = kvmSiteRoute + "/hosts"
const kvmHostRoute = kvmHostsRoute + "/:host"
const kvmHostFenceRoute = kvmHostRoute + "/fence"
const kvmCapacityRoute = kvmHostRoute + "/capacity"
const kvmPlacementsRoute = kvmSiteRoute + "/placements"
const kvmPlacementRoute = kvmPlacementsRoute + "/:placement"

type kvmHTTP struct {
	store  kvm.Store
	logger plumelog.StructuredLogger
}

func registerKVM(app *core.App, store kvm.Store) error {
	h := kvmHTTP{store: store, logger: app.Logger()}
	return errors.Join(
		app.Post(kvmHostsRoute, h.endpoint(h.registerHost)),
		app.Get(kvmHostRoute, h.endpoint(h.getHost)),
		app.Patch(kvmHostRoute, h.endpoint(h.changeHost)),
		app.Get(kvmHostFenceRoute, h.endpoint(h.getHostFence)),
		app.Post(kvmHostFenceRoute, h.endpoint(h.changeHostFence)),
		app.Put(kvmCapacityRoute, h.endpoint(h.report)),
		app.Post(kvmIPv4PoolsRoute, h.endpoint(h.registerIPv4Pool)),
		app.Get(kvmIPv4PoolRoute, h.endpoint(h.getIPv4Pool)),
		app.Post(kvmBootImagesRoute, h.endpoint(h.registerBootImage)),
		app.Get(kvmBootImageRoute, h.endpoint(h.getBootImage)),
		app.Post(kvmHostIPv4PoolRoute, h.endpoint(h.hostIPv4Pool)),
		app.Post(kvmPlacementsRoute, h.endpoint(h.reserve)),
		app.Get(kvmPlacementRoute, h.endpoint(h.getPlacement)),
		app.Delete(kvmPlacementRoute, h.endpoint(h.release)),
		app.Post(kvmVMsRoute, h.endpoint(h.submitVM)),
		app.Get(kvmVMRoute, h.endpoint(h.getVM)),
		app.Get(kvmFencedVMRecoveryRoute, h.endpoint(h.getFencedVMRecovery)),
		app.Post(kvmFencedVMRecoveryRoute, h.endpoint(h.requestFencedVMRecovery)),
		app.Post(kvmFencedVMRecoveryRoute+"/review", h.endpoint(h.reviewFencedVMRecovery)),
		app.Post(kvmVMShutdownRoute, h.endpoint(h.requestVMShutdown)),
		app.Post(kvmVMRetirementRoute, h.endpoint(h.requestVMRetirement)),
		app.Post(kvmVMRoute+"/review", h.endpoint(h.reviewVM)),
		app.Post(kvmVMPollRoute, h.endpoint(h.pollVM)),
		app.Post(kvmVMInventoryRoute, h.endpoint(h.lookupVMInventory)),
		app.Post(kvmVMJobRoute+"/arm", h.endpoint(h.armVM)),
		app.Post(kvmVMImageStageRoute, h.endpoint(h.authorizeImageStage)),
		app.Put(kvmVMJobRoute+"/observation", h.endpoint(h.observeVM)),
	)
}

type kvmEndpoint func(context.Context, http.ResponseWriter, *http.Request, string, identity.Scope, string) error

func (h kvmHTTP) endpoint(next kvmEndpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeAccountError(w, r, identity.ErrUnauthenticated, h.logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		err := next(ctx, w, r, token, scope, router.Param(r, "site"))
		if err != nil {
			writeKVMError(w, r, err, h.logger)
		}
	})
}

func (h kvmHTTP) registerHost(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	var input kvm.HostSpec
	if err := decodeKVMBody(r, &input, 8192); err != nil {
		return err
	}
	host, err := h.store.RegisterHost(ctx, token, scope, site, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/hosts/"+url.PathEscape(host.ID))
	h.writeHost(w, r, http.StatusCreated, host)
	return nil
}

func (h kvmHTTP) getHost(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	host, err := h.store.GetHost(ctx, token, scope, site, router.Param(r, "host"))
	if err != nil {
		return err
	}
	h.writeHost(w, r, http.StatusOK, host)
	return nil
}

func (h kvmHTTP) changeHost(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := accountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.HostChange
	if err := decodeKVMBody(r, &input, 1024); err != nil {
		return err
	}
	host, err := h.store.ChangeHost(ctx, token, scope, site, router.Param(r, "host"), revision, input)
	if err != nil {
		return err
	}
	h.writeHost(w, r, http.StatusOK, host)
	return nil
}

func (h kvmHTTP) report(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	if token == "" {
		return identity.ErrUnauthenticated
	}
	var input kvm.Report
	if err := decodeKVMBody(r, &input, 16<<10); err != nil {
		return err
	}
	host, err := h.store.Report(ctx, token, scope, site, router.Param(r, "host"), input)
	if err != nil {
		return err
	}
	h.writeHost(w, r, http.StatusOK, host)
	return nil
}
