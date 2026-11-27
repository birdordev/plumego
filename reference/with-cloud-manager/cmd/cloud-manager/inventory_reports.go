package main

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/inventory"
)

const inventoryFreshnessRoute = governanceRoute + "/inventory-freshness"

func inventoryFreshnessHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveInventory(inventoryFreshnessQuery, store.Freshness,
		func(w http.ResponseWriter, r *http.Request, result inventory.FreshnessReport) error {
			return contract.WriteResponse(w, r, http.StatusOK, result, nil)
		}, logger)
}

func inventoryFreshnessQuery(r *http.Request) (inventory.FreshnessQuery, error) {
	q := inventory.FreshnessQuery{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"),
		Window: inventory.DefaultFreshnessWindow, Limit: inventory.DefaultFreshnessLimit}
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return q, inventory.ErrInvalidQuery
	}
	for name, values := range params {
		if len(values) != 1 || values[0] == "" {
			return q, inventory.ErrInvalidQuery
		}
		switch value := values[0]; name {
		case "provider":
			q.Provider = value
		case "account":
			q.Account = value
		case "state":
			q.State = value
		case "dispatch_state":
			q.DispatchState = value
		case "order":
			q.Order = value
		case "after":
			q.After = value
		case "window_seconds":
			q.Window, err = strconv.Atoi(value)
		case "limit":
			q.Limit, err = strconv.Atoi(value)
		case "scope_fingerprint":
			if value != "true" && value != "false" {
				return q, inventory.ErrInvalidQuery
			}
			q.ScopeFingerprint = value == "true"
		default:
			return q, inventory.ErrInvalidQuery
		}
		if err != nil {
			return q, inventory.ErrInvalidQuery
		}
	}
	return q, nil
}

func inventoryFreshnessOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	dateTime := openapi.Schema{Type: "string", Format: "date-time"}
	item := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"account": openapi.String, "provider": openapi.String, "region": openapi.String, "kind": openapi.String,
		"state": openapi.String, "running": {Type: "boolean"}, "completed_at": dateTime, "next_at": dateTime,
		"dispatch_state": openapi.String, "schedule_lease_until": dateTime,
		"account_lease_until": dateTime, "account_cooldown_until": dateTime,
	}}
	summary := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"total": openapi.Integer, "fresh": openapi.Integer, "stale": openapi.Integer, "pending": openapi.Integer,
	}}
	report := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"as_of": dateTime, "window_seconds": openapi.Integer, "summary": summary,
		"dispatch_summary": {Type: "object", Properties: map[string]openapi.Schema{
			"scheduled": openapi.Integer, "ready": openapi.Integer, "account_busy": openapi.Integer,
			"cooldown": openapi.Integer, "leased": openapi.Integer, "longest_ready_overdue_seconds": openapi.Integer,
		}},
		"scope_fingerprint": openapi.String, "items": {Type: "array", Items: &item}, "next": openapi.String,
	}}
	return openapi.Op{OperationID: "listInventoryFreshness", Security: readAuth, Params: []openapi.Param{
		{Name: "provider", In: "query", Schema: openapi.String},
		{Name: "account", In: "query", Schema: openapi.String},
		{Name: "state", In: "query", Schema: openapi.String, Description: "仅筛选明细，支持 fresh、stale 和 pending。"},
		{Name: "dispatch_state", In: "query", Schema: openapi.String, Description: "仅筛选明细，支持 scheduled、ready、account_busy、cooldown 和 leased。"},
		{Name: "order", In: "query", Schema: openapi.String, Description: "next_at 按计划时间升序排列明细，同时间按计划 ID 排列。省略时按计划 ID 排列。"},
		{Name: "window_seconds", In: "query", Schema: openapi.Integer, Description: "60 至 604800 秒，默认 900 秒。"},
		{Name: "after", In: "query", Schema: openapi.String, Description: "保留筛选项并传入上一页的 next。"},
		{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
		{Name: "scope_fingerprint", In: "query", Schema: openapi.Boolean, Description: "返回同一快照下完整可见范围的指纹，独立于明细筛选和分页。"},
	}, Responses: map[string]openapi.Response{
		"200": {Description: "项目可见的启用盘点计划、调度状态及完整扫描新鲜度。汇总覆盖全部符合账号和厂商筛选的计划。",
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
				Type: "object", Properties: map[string]openapi.Schema{"data": report},
			}}}},
		"400": {Description: "筛选项或游标无效"},
		"401": {Description: "凭据缺失、过期或无效"},
		"403": {Description: "项目权限不足"},
		"503": {Description: "盘点存储不可用"},
	}}
}

const idleInstancesRoute = governanceRoute + "/idle-instances"

func idleInstancesHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveInventory(func(r *http.Request) (inventory.IdleQuery, error) {
		query, state, err := parseGovernanceReportQuery(r, false)
		return inventory.IdleQuery{Query: query, State: state}, err
	}, store.IdleInstances, func(w http.ResponseWriter, r *http.Request, page inventory.IdleInstances) error {
		return contract.WriteResponse(w, r, http.StatusOK, page, nil)
	}, logger)
}

func idleReportOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	return openapi.Op{OperationID: "listIdleInstances", Security: readAuth,
		Params: []openapi.Param{
			{Name: "state", In: "query", Schema: openapi.String, Description: "candidate、active、insufficient 或 not_running"},
			{Name: "account", In: "query", Schema: openapi.String},
			{Name: "region", In: "query", Schema: openapi.String},
			{Name: "cursor", In: "query", Schema: openapi.String},
			{Name: "limit", In: "query", Schema: openapi.Integer},
		}, Responses: map[string]openapi.Response{
			"200": {Description: "项目内的七天实例利用率分析、样本覆盖、指标峰值、判断阈值和续页游标"},
			"400": {Description: "筛选项或游标无效"},
			"401": {Description: "凭据缺失、过期或无效"},
			"403": {Description: "项目权限不足"},
			"503": {Description: "盘点或分析存储不可用"},
		}}
}
