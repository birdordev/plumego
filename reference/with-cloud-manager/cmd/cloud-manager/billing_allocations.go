package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/identity"
)

const allocationRoute = "/v1/tenants/:tenant/projects/:project/billing/allocations"

func allocationHandler(store billing.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeBillingError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		key, report, revision, err := allocationQuery(router.Param(r, "tenant"), router.Param(r, "project"), r.URL.RawQuery)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		value, err := store.ReadAllocation(ctx, token, key, report, revision)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
			logger.Warn("billing allocation response write failed", plumelog.Fields{})
		}
	})
}

func allocationQuery(tenant, project, raw string) (billing.Key, int64, int64, error) {
	if len(raw) > 1024 {
		return billing.Key{}, 0, 0, billing.ErrInvalid
	}
	params, err := url.ParseQuery(raw)
	if err != nil {
		return billing.Key{}, 0, 0, billing.ErrInvalid
	}
	var revision int64
	if parts, exists := params["allocation_revision"]; exists {
		if len(parts) != 1 {
			return billing.Key{}, 0, 0, billing.ErrInvalid
		}
		parsed, ok := billingRevision(parts[0], false)
		if !ok {
			return billing.Key{}, 0, 0, billing.ErrInvalid
		}
		revision = parsed
		params.Del("allocation_revision")
	}
	key, report, err := billingQuery(tenant, project, params.Encode())
	if err != nil || (report == 0 && revision > 0) {
		return billing.Key{}, 0, 0, billing.ErrInvalid
	}
	return key, report, revision, nil
}

func billingRevision(value string, zero bool) (int64, bool) {
	revision, err := strconv.ParseInt(value, 10, 64)
	return revision, err == nil && (revision > 0 || zero && revision == 0) && strconv.FormatInt(revision, 10) == value
}

func allocationOpenAPI(auth []openapi.SecurityRequirement) openapi.Op {
	return openapi.Op{OperationID: "getBillingAllocation", Security: auth,
		Summary: "读取项目账号在指定账单版本上的原币种费用分摊",
		Params: []openapi.Param{
			{Name: "account", In: "query", Required: true, Schema: openapi.String},
			{Name: "period", In: "query", Required: true, Schema: openapi.String, Description: "YYYY-MM"},
			{Name: "source", In: "query", Required: true, Schema: openapi.String},
			{Name: "revision", In: "query", Schema: openapi.Integer, Description: "账单版本，默认当前账单"},
			{Name: "allocation_revision", In: "query", Schema: openapi.Integer, Description: "分摊版本。显式指定时需同时提供账单版本"},
		}, Responses: map[string]openapi.Response{
			"200": {Description: "规则摘要、账单版本与按成本中心和原币种核对后的金额"},
			"400": {Description: "查询参数或版本无效"},
			"401": {Description: "凭据缺失、过期或无效"},
			"403": {Description: "需要账号归属项目的管理员权限"},
			"404": {Description: "对应的账单或分摊版本不存在"},
			"503": {Description: "分摊存储不可用"},
		}}
}
