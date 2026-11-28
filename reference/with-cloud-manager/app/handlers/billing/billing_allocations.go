package billinghttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/app/handlers/transport"
	billingservice "with-cloud-manager/app/services/billing"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/identity"
)

const AllocationRoute = "/v1/tenants/:tenant/projects/:project/billing/allocations"

func AllocationHandler(store billing.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			writeBillingError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		key, report, revision, err := AllocationQuery(router.Param(r, "tenant"), router.Param(r, "project"), r.URL.RawQuery)
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
		w.Header().Set("ETag", strconv.Quote(value.Version))
		writeAllocationResponse(w, r, value, logger)
	})
}

func AllocationWriteHandler(store billing.Store, logger plumelog.StructuredLogger, preview bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			writeBillingError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		target, err := AllocationWriteQuery(router.Param(r, "tenant"), router.Param(r, "project"), r.URL.RawQuery, preview)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		expect, err := allocationMatch(r, preview)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		var raw json.RawMessage
		if err := contract.DecodeJSONRequest(r, &raw, contract.JSONRequestOptions{MaxBytes: billing.MaxAllocationPolicyBytes}); err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		policy, err := billingservice.DecodeAllocationPolicy(raw)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var result any
		if preview {
			result, err = store.PreviewAllocation(ctx, token, target.key, target.revision, policy)
		} else {
			var published billing.Allocation
			published, err = store.PublishAllocation(ctx, token, target.key, target.revision, expect, target.reportDigest, target.policyDigest, policy)
			if err == nil {
				w.Header().Set("ETag", strconv.Quote(published.Version))
			}
			result = published
		}
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		writeAllocationResponse(w, r, result, logger)
	})
}

func writeAllocationResponse(w http.ResponseWriter, r *http.Request, value any, logger plumelog.StructuredLogger) {
	if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
		logger.Warn("billing allocation response write failed", plumelog.Fields{})
	}
}

func AllocationQuery(tenant, project, raw string) (billing.Key, int64, int64, error) {
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
		parsed, ok := billingservice.ParseRevision(parts[0], false)
		if !ok {
			return billing.Key{}, 0, 0, billing.ErrInvalid
		}
		revision = parsed
		params.Del("allocation_revision")
	}
	key, report, err := BillingQuery(tenant, project, params.Encode())
	if err != nil || (report == 0 && revision > 0) {
		return billing.Key{}, 0, 0, billing.ErrInvalid
	}
	return key, report, revision, nil
}

func AllocationOpenAPI(auth []openapi.SecurityRequirement) openapi.Op {
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
