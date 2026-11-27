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
const kvmHostFenceWorkRoute = kvmHostFenceRoute + "/work"
const kvmEvacuationCleanupRoute = kvmHostFenceRoute + "/cleanup"
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
		app.Get(kvmHostFenceWorkRoute, h.endpoint(h.listHostFenceWork)),
		app.Post(kvmEvacuationCleanupRoute, h.endpoint(h.confirmEvacuationCleanup)),
		app.Post(kvmEvacuationBatchesRoute, h.endpoint(h.requestEvacuationBatch)),
		app.Get(kvmEvacuationBatchRoute, h.endpoint(h.getEvacuationBatch)),
		app.Post(kvmEvacuationPlansRoute, h.endpoint(h.armEvacuationPlan)),
		app.Get(kvmEvacuationPlanRoute, h.endpoint(h.getEvacuationPlan)),
		app.Delete(kvmEvacuationPlanRoute, h.endpoint(h.cancelEvacuationPlan)),
		app.Post(kvmHostFenceRoute, h.endpoint(h.changeHostFence)),
		app.Put(kvmCapacityRoute, h.endpoint(h.report)),
		app.Post(kvmIPv4PoolsRoute, h.endpoint(h.registerIPv4Pool)),
		app.Get(kvmIPv4PoolRoute, h.endpoint(h.getIPv4Pool)),
		app.Post(kvmBootImagesRoute, h.endpoint(h.registerBootImage)),
		app.Get(kvmBootImageRoute, h.endpoint(h.getBootImage)),
		app.Post(kvmSharedStoragePoolsRoute, h.endpoint(h.registerSharedStorage)),
		app.Get(kvmSharedStoragePoolRoute, h.endpoint(h.getSharedStorage)),
		app.Get(kvmHostImageRoute, h.endpoint(h.getHostImage)),
		app.Post(kvmHostImageCheckRoute, h.endpoint(h.checkHostImage)),
		app.Put(kvmHostImageRoute, h.endpoint(h.reportHostImage)),
		app.Post(kvmHostIPv4PoolRoute, h.endpoint(h.hostIPv4Pool)),
		app.Post(kvmPlacementsRoute, h.endpoint(h.reserve)),
		app.Get(kvmPlacementRoute, h.endpoint(h.getPlacement)),
		app.Delete(kvmPlacementRoute, h.endpoint(h.release)),
		app.Post(kvmVolumeCleanupsRoute, h.endpoint(h.requestVolumeCleanup)),
		app.Get(kvmVolumeCleanupRoute, h.endpoint(h.getVolumeCleanup)),
		app.Get(kvmHostVolumeCleanupsRoute, h.endpoint(h.listVolumeCleanups)),
		app.Post(kvmVolumeCleanupRoute+"/review", h.endpoint(h.reviewVolumeCleanup)),
		app.Post(kvmVolumeCleanupRoute+"/resolve", h.endpoint(h.resolveVolumeCleanup)),
		app.Post(kvmVolumeCleanupAgentRoute, h.endpoint(h.applyVolumeCleanup)),
		app.Post(kvmVMsRoute, h.endpoint(h.submitVM)),
		app.Get(kvmVMRoute, h.endpoint(h.getVM)),
		app.Get(kvmFencedVMRecoveryRoute, h.endpoint(h.getFencedVMRecovery)),
		app.Post(kvmFencedVMRecoveryRoute, h.endpoint(h.requestFencedVMRecovery)),
		app.Post(kvmFencedVMRecoveryRoute+"/review", h.endpoint(h.reviewFencedVMRecovery)),
		app.Get(kvmColdMigrationRoute, h.endpoint(h.getColdMigration)),
		app.Post(kvmColdMigrationRoute, h.endpoint(h.requestColdMigration)),
		app.Post(kvmColdMigrationRoute+"/review", h.endpoint(h.reviewColdMigration)),
		app.Post(kvmLiveMigrationsRoute, h.endpoint(h.requestLiveMigration)),
		app.Get(kvmLiveMigrationRoute, h.endpoint(h.getLiveMigration)),
		app.Post(kvmLiveMigrationRoute+"/review", h.endpoint(h.reviewLiveMigration)),
		app.Post(kvmLiveMigrationRoute+"/abort", h.endpoint(h.abortFencedLiveMigration)),
		app.Post(kvmLiveMigrationAgentRoute, h.endpoint(h.applyLiveMigrationCommand)),
		app.Post(kvmVMShutdownRoute, h.endpoint(h.requestVMShutdown)),
		app.Post(kvmVMRetirementRoute, h.endpoint(h.requestVMRetirement)),
		app.Post(kvmVMRoute+"/review", h.endpoint(h.reviewVM)),
		app.Post(kvmVMPollRoute, h.endpoint(h.pollVM)),
		app.Post(kvmVMInventoryRoute, h.endpoint(h.lookupVMInventory)),
		app.Post(kvmVMJobRoute+"/arm", h.endpoint(h.armVM)),
		app.Post(kvmVMBackupRoute, h.endpoint(h.authorizeVMBackup)),
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
