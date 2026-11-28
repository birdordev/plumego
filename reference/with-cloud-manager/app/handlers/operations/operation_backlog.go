package operationhttp

import (
	"flag"
	"io"
	"net/url"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/operations"
)

const OperationBacklogRoute = "/v1/tenants/:tenant/projects/:project/operation-backlog"

func OperationBudgetOpenAPI(hints map[string]openapi.Op, readAuth, writeAuth []openapi.SecurityRequirement) {
	limits := map[string]openapi.Schema{"max_active": openapi.Integer, "max_managed_instances": openapi.Integer}
	budget := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"revision": openapi.String, "max_active": openapi.Integer, "active_operations": openapi.Integer,
		"max_managed_instances": openapi.Integer, "reserved_instances": openapi.Integer,
	}}
	read := openapi.Op{OperationID: "getProjectOperationBudget", Security: readAuth, Responses: map[string]openapi.Response{
		"200": {Description: "项目额度、当前占用及策略版本 ETag。",
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
				Type: "object", Properties: map[string]openapi.Schema{"data": budget},
			}}}},
		"400": {Description: "查询参数无效"}, "401": {Description: "凭据缺失、过期或无效"},
		"403": {Description: "项目权限不足"}, "503": {Description: "配额账本不可用"},
	}}
	hints["GET "+OperationBudgetRoute] = read
	write := read
	write.OperationID, write.Security = "replaceProjectOperationBudget", writeAuth
	write.Summary = "管理员按当前版本更新项目受理额度"
	write.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	write.Body = &openapi.RequestBody{Required: true, Description: "两项上限均必填，活跃操作 1–10000，受管实例预留 0–100000。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: limits}}}}
	write.Responses = map[string]openapi.Response{
		"200": read.Responses["200"], "400": {Description: "请求体、额度或查询参数无效"}, "401": read.Responses["401"],
		"403": {Description: "项目管理员权限不足"}, "409": {Description: "策略版本不可再增加"},
		"412": {Description: "缺少当前策略 ETag 或版本已变化"}, "413": {Description: "正文过大"},
		"415": {Description: "媒体类型错误"}, "503": read.Responses["503"],
	}
	hints["PUT "+OperationBudgetRoute] = write
}

func parseBacklogQuery(raw string) (operations.BacklogFilter, error) {
	var filter operations.BacklogFilter
	if len(raw) > 4096 {
		return filter, operations.ErrInvalidQuery
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return filter, operations.ErrInvalidQuery
	}
	for name, list := range values {
		if len(list) != 1 || list[0] == "" {
			return filter, operations.ErrInvalidQuery
		}
		switch name {
		case "account":
			filter.Account = list[0]
		case "region":
			filter.Region = list[0]
		default:
			return filter, operations.ErrInvalidQuery
		}
	}
	if !filter.Valid() {
		return filter, operations.ErrInvalidQuery
	}
	return filter, nil
}

func BacklogClientQuery(args []string) (url.Values, bool) {
	var filter operations.BacklogFilter
	flags := flag.NewFlagSet("backlog", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&filter.Account, "account", "", "")
	flags.StringVar(&filter.Region, "region", "", "")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !filter.Valid() {
		return nil, false
	}
	query := make(url.Values, 2)
	valid := true
	flags.Visit(func(option *flag.Flag) {
		value := option.Value.String()
		if value == "" {
			valid = false
		}
		query.Set(option.Name, value)
	})
	return query, valid
}

func OperationBacklogOpenAPI(auth []openapi.SecurityRequirement) openapi.Op {
	date := openapi.Schema{Type: "string", Format: "date-time"}
	countFields := []string{"awaiting_approval", "ready", "deferred", "leased", "recoverable", "needs_review", "queue_anomalies"}
	counts := func() map[string]openapi.Schema {
		properties := make(map[string]openapi.Schema, len(countFields)+3)
		for _, field := range countFields {
			properties[field] = openapi.Integer
		}
		properties["earliest_ready_at"], properties["earliest_recovery_at"], properties["next_deferred_at"] = date, date, date
		return properties
	}
	provider := openapi.Schema{Type: "object", Properties: counts()}
	provider.Properties["provider"] = openapi.String
	report := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"as_of": date, "summary": {Type: "object", Properties: counts()},
		"providers": {Type: "array", Items: &provider},
	}}
	return openapi.Op{OperationID: "getProjectOperationBacklog", Security: auth, Params: []openapi.Param{
		{Name: "account", In: "query", Schema: openapi.String}, {Name: "region", In: "query", Schema: openapi.String},
	}, Responses: map[string]openapi.Response{
		"200": {Description: "同一项目快照的非终态操作队列汇总及厂商分组。",
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
				Type: "object", Properties: map[string]openapi.Schema{"data": report},
			}}}},
		"400": {Description: "筛选项无效"}, "401": {Description: "凭据缺失、过期或无效"},
		"403": {Description: "项目权限不足"}, "503": {Description: "操作队列快照不可用"},
	}}
}
