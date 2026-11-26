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

const budgetRoute = "/v1/tenants/:tenant/projects/:project/billing/budget"

func budgetHandler(store billing.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeBillingError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		key, err := budgetQuery(router.Param(r, "tenant"), router.Param(r, "project"), r.URL.RawQuery)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var budget billing.Budget
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			budget, err = writeBudget(ctx, r, store, token, key)
		} else {
			budget, err = store.GetBudget(ctx, token, key)
		}
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(strconv.FormatInt(budget.Revision, 10)))
		if r.Method == http.MethodPost {
			err = contract.WriteResponse(w, r, http.StatusCreated, budget, nil)
		} else {
			err = contract.WriteResponse(w, r, http.StatusOK, budget, nil)
		}
		if err != nil {
			logger.Warn("billing budget response write failed", plumelog.Fields{})
		}
	})
}

func writeBudget(ctx context.Context, r *http.Request, store billing.Store, token string, key billing.BudgetKey) (billing.Budget, error) {
	var revision int64
	if r.Method == http.MethodPut {
		var ok bool
		revision, ok = accountRevision(r)
		if !ok {
			return billing.Budget{}, billing.ErrBudgetStale
		}
	} else if len(r.Header.Values("If-Match")) != 0 {
		return billing.Budget{}, billing.ErrInvalid
	}
	var input billing.BudgetInput
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: 1024}); err != nil {
		return billing.Budget{}, err
	}
	return store.SaveBudget(ctx, token, key, revision, input)
}

func budgetQuery(tenant, project, raw string) (billing.BudgetKey, error) {
	if len(raw) > 1024 {
		return billing.BudgetKey{}, billing.ErrInvalid
	}
	query, err := url.ParseQuery(raw)
	if err != nil || len(query["currency"]) != 1 || len(query["basis"]) != 1 || query.Has("revision") {
		return billing.BudgetKey{}, billing.ErrInvalid
	}
	key := billing.BudgetKey{Currency: query.Get("currency"), Basis: query.Get("basis")}
	query.Del("currency")
	query.Del("basis")
	key.Key, _, err = billingQuery(tenant, project, query.Encode())
	if err != nil || !key.Valid() {
		return billing.BudgetKey{}, billing.ErrInvalid
	}
	return key, nil
}

func budgetOpenAPI(hints map[string]openapi.Op, readAuth, writeAuth []openapi.SecurityRequirement) {
	description := "当期预算、状态、费用口径与所用账单版本。金额为十进制字符串"
	params := []openapi.Param{
		{Name: "account", In: "query", Required: true, Schema: openapi.String},
		{Name: "period", In: "query", Required: true, Schema: openapi.String, Description: "YYYY-MM"},
		{Name: "source", In: "query", Required: true, Schema: openapi.String, Description: "native 或 focus-1.0"},
		{Name: "currency", In: "query", Required: true, Schema: openapi.String},
		{Name: "basis", In: "query", Required: true, Schema: openapi.String, Description: "billed 或 effective"},
	}
	responses := func(status string) map[string]openapi.Response {
		return map[string]openapi.Response{
			status: {Description: description},
			"400":  {Description: "查询或预算配置无效"},
			"401":  {Description: "凭据缺失、过期或无效"},
			"403":  {Description: "需要归属项目的管理员权限"},
			"503":  {Description: "预算存储不可用"},
		}
	}
	read := openapi.Op{OperationID: "getBillingBudget", Params: params, Security: readAuth, Responses: responses("200")}
	read.Responses["404"] = openapi.Response{Description: "预算尚未设置"}
	create := openapi.Op{OperationID: "createBillingBudget", Params: params, Security: writeAuth, Responses: responses("201")}
	create.Responses["409"] = openapi.Response{Description: "预算已存在"}
	update := openapi.Op{OperationID: "updateBillingBudget", Params: append(append([]openapi.Param(nil), params...),
		openapi.Param{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}),
		Security: writeAuth, Responses: responses("200")}
	update.Responses["412"] = openapi.Response{Description: "需要预算当前 ETag"}
	body := &openapi.RequestBody{Required: true, Description: "额度为正十进制字符串，预警百分比为 1 至 99，启用状态必填。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"limit": openapi.String, "warning_percent": openapi.Integer, "enabled": {Type: "boolean"}},
		}}}}
	create.Body, update.Body = body, body
	for _, problems := range []map[string]openapi.Response{create.Responses, update.Responses} {
		problems["413"] = openapi.Response{Description: "正文超出大小限制"}
		problems["415"] = openapi.Response{Description: "媒体类型无效"}
	}
	hints["GET "+budgetRoute], hints["POST "+budgetRoute], hints["PUT "+budgetRoute] = read, create, update
}
