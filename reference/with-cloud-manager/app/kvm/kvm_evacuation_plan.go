package kvmapp

import (
	"context"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmEvacuationPlansRoute = kvmHostFenceRoute + "/evacuation-plans"
const kvmEvacuationPlanRoute = kvmEvacuationPlansRoute + "/:plan"

func (h kvmHTTP) armEvacuationPlan(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	revision, ok := transport.AccountRevision(r)
	if !ok {
		return kvm.ErrStale
	}
	var input kvm.EvacuationPlanInput
	if err := decodeKVMBody(r, &input, kvmEvacuationBatchBodyLimit); err != nil {
		return err
	}
	host := router.Param(r, "host")
	plan, err := h.store.ArmEvacuationPlan(ctx, token, scope, site, host, revision, input)
	if err != nil {
		return err
	}
	w.Header().Set("Location", kvmSitePath(scope, site)+"/hosts/"+url.PathEscape(host)+"/fence/evacuation-plans/"+url.PathEscape(plan.ID))
	h.write(w, r, http.StatusCreated, plan)
	return nil
}

func (h kvmHTTP) getEvacuationPlan(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	plan, err := h.store.GetEvacuationPlan(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "plan"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, plan)
	return nil
}

func (h kvmHTTP) cancelEvacuationPlan(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	plan, err := h.store.CancelEvacuationPlan(ctx, token, scope, site, router.Param(r, "host"), router.Param(r, "plan"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, plan)
	return nil
}
