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
const projectBudgetRoute = "/v1/tenants/:tenant/projects/:project/billing/project-budget"

func budgetHandler(store billing.Store, logger plumelog.StructuredLogger) http.Handler {
	return billingBudgetEndpoint(budgetQuery, store.GetBudget, store.SaveBudget,
		func(budget billing.Budget) string { return strconv.FormatInt(budget.Revision, 10) }, 1024, logger)
}

func projectBudgetHandler(store billing.Store, logger plumelog.StructuredLogger) http.Handler {
	return billingBudgetEndpoint(projectBudgetQuery, store.GetProjectBudget, store.SaveProjectBudget,
		func(budget billing.ProjectBudget) string { return budget.Revision }, 16<<10, logger)
}

func billingBudgetEndpoint[Key, Input, Budget any](
	parse func(string, string, string) (Key, error),
	read func(context.Context, string, Key) (Budget, error),
	save func(context.Context, string, Key, int64, Input) (Budget, error),
	version func(Budget) string, maxBody int64, logger plumelog.StructuredLogger,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := requestCredential(r)
		if !ok {
			writeBillingError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		key, err := parse(router.Param(r, "tenant"), router.Param(r, "project"), r.URL.RawQuery)
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var budget Budget
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			revision, input, inputErr := billingBudgetInput[Input](r, maxBody)
			if inputErr != nil {
				writeBillingError(w, r, inputErr, logger)
				return
			}
			budget, err = save(ctx, token, key, revision, input)
		} else {
			budget, err = read(ctx, token, key)
		}
		if err != nil {
			writeBillingError(w, r, err, logger)
			return
		}
		w.Header().Set("ETag", strconv.Quote(version(budget)))
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

func billingBudgetInput[Input any](r *http.Request, maxBody int64) (int64, Input, error) {
	var revision int64
	var input Input
	if r.Method == http.MethodPut {
		var ok bool
		revision, ok = accountRevision(r)
		if !ok {
			return 0, input, billing.ErrBudgetStale
		}
	} else if len(r.Header.Values("If-Match")) != 0 {
		return 0, input, billing.ErrInvalid
	}
	if err := contract.DecodeUniqueJSONRequest(r, &input, contract.JSONRequestOptions{MaxBytes: maxBody}); err != nil {
		return 0, input, err
	}
	return revision, input, nil
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

func projectBudgetQuery(tenant, project, raw string) (billing.ProjectBudgetKey, error) {
	if len(raw) > 1024 {
		return billing.ProjectBudgetKey{}, billing.ErrInvalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 4 {
		return billing.ProjectBudgetKey{}, billing.ErrInvalid
	}
	for _, name := range []string{"period", "source", "currency", "basis"} {
		if len(values[name]) != 1 {
			return billing.ProjectBudgetKey{}, billing.ErrInvalid
		}
	}
	key := billing.ProjectBudgetKey{Tenant: tenant, Project: project, Period: values.Get("period"),
		Source: values.Get("source"), Currency: values.Get("currency"), Basis: values.Get("basis")}
	if !key.Valid() {
		return billing.ProjectBudgetKey{}, billing.ErrInvalid
	}
	return key, nil
}

func budgetOpenAPI(hints map[string]openapi.Op, readAuth, writeAuth []openapi.SecurityRequirement) {
	scope := []openapi.Param{
		{Name: "period", In: "query", Required: true, Schema: openapi.String, Description: "YYYY-MM"},
		{Name: "source", In: "query", Required: true, Schema: openapi.String, Description: "native 或 focus-1.0"},
		{Name: "currency", In: "query", Required: true, Schema: openapi.String},
		{Name: "basis", In: "query", Required: true, Schema: openapi.String, Description: "billed 或 effective"},
	}
	addBudgetOpenAPI(hints, budgetRoute, "BillingBudget", "当期预算、状态、费用口径与所用账单版本。金额为十进制字符串",
		append([]openapi.Param{{Name: "account", In: "query", Required: true, Schema: openapi.String}}, scope...),
		readAuth, writeAuth, false)
	addBudgetOpenAPI(hints, projectBudgetRoute, "ProjectBillingBudget", "项目预算、选定账号的覆盖与来源版本。金额和版本为十进制字符串。",
		scope, readAuth, writeAuth, true)
}

func addBudgetOpenAPI(hints map[string]openapi.Op, route, suffix, description string, params []openapi.Param,
	readAuth, writeAuth []openapi.SecurityRequirement, project bool,
) {
	responses := func(status string) map[string]openapi.Response {
		return map[string]openapi.Response{
			status: {Description: description},
			"400":  {Description: "查询或预算配置无效"},
			"401":  {Description: "凭据缺失、过期或无效"},
			"403":  {Description: "需要归属项目的管理员权限"},
			"503":  {Description: "预算存储不可用"},
		}
	}
	read := openapi.Op{OperationID: "get" + suffix, Params: params, Security: readAuth, Responses: responses("200")}
	read.Responses["404"] = openapi.Response{Description: "预算尚未设置"}
	create := openapi.Op{OperationID: "create" + suffix, Params: params, Security: writeAuth, Responses: responses("201")}
	create.Responses["409"] = openapi.Response{Description: "预算已存在"}
	update := openapi.Op{OperationID: "update" + suffix, Params: append(append([]openapi.Param(nil), params...),
		openapi.Param{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}),
		Security: writeAuth, Responses: responses("200")}
	update.Responses["412"] = openapi.Response{Description: "需要预算当前 ETag"}
	fields := map[string]openapi.Schema{"limit": openapi.String, "warning_percent": openapi.Integer, "enabled": {Type: "boolean"}}
	bodyDescription := "额度为正十进制字符串，预警百分比为 1 至 99，启用状态必填。"
	if project {
		fields["accounts"] = openapi.Schema{Type: "array", Items: &openapi.String}
		bodyDescription += "accounts 必须列出 1 至 100 个不同云账号，更新时提交完整范围。"
		content := map[string]openapi.MediaType{"application/json": {Schema: projectBudgetResponseSchema()}}
		read.Responses["200"] = openapi.Response{Description: description, Content: content}
		create.Responses["201"] = openapi.Response{Description: description, Content: content}
		update.Responses["200"] = openapi.Response{Description: description, Content: content}
	}
	body := &openapi.RequestBody{Required: true, Description: bodyDescription,
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: fields,
		}}}}
	create.Body, update.Body = body, body
	for _, problems := range []map[string]openapi.Response{create.Responses, update.Responses} {
		problems["413"] = openapi.Response{Description: "正文超出大小限制"}
		problems["415"] = openapi.Response{Description: "媒体类型无效"}
	}
	hints["GET "+route], hints["POST "+route], hints["PUT "+route] = read, create, update
}

func projectBudgetResponseSchema() openapi.Schema {
	account := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"account": openapi.String, "state": openapi.String, "report_revision": openapi.String, "amount": openapi.String,
	}}
	return openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"data": {Type: "object", Properties: map[string]openapi.Schema{
			"id": {Type: "string", Format: "uuid"}, "period": openapi.String, "source": openapi.String,
			"currency": openapi.String, "basis": openapi.String, "limit": openapi.String, "warning_percent": openapi.Integer,
			"enabled": openapi.Boolean, "revision": openapi.String, "updated_at": {Type: "string", Format: "date-time"},
			"state": openapi.String, "spent": openapi.String, "observed_accounts": openapi.Integer,
			"accounts": {Type: "array", Items: &account},
		}},
	}}
}
