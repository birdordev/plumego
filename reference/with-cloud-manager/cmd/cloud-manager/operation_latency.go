package main

import (
	"flag"
	"io"
	"net/url"
	"time"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/operations"
)

const operationLatencyRoute = "/v1/tenants/:tenant/projects/:project/operation-latency"

func parseLatencyQuery(raw string) (operations.LatencyFilter, error) {
	var filter operations.LatencyFilter
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
		case "from":
			filter.From, err = time.Parse(time.RFC3339Nano, list[0])
		case "until":
			filter.Until, err = time.Parse(time.RFC3339Nano, list[0])
		case "account":
			filter.Account = list[0]
		case "region":
			filter.Region = list[0]
		case "action":
			filter.Action = list[0]
		default:
			return filter, operations.ErrInvalidQuery
		}
		if err != nil {
			return filter, operations.ErrInvalidQuery
		}
	}
	if !filter.Valid() {
		return filter, operations.ErrInvalidQuery
	}
	return filter, nil
}

func latencyClientQuery(args []string) (url.Values, bool) {
	flags := flag.NewFlagSet("latency", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	for _, name := range []string{"from", "until", "account", "region", "action"} {
		flags.String(name, "", "")
	}
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return nil, false
	}
	query := make(url.Values, 5)
	flags.Visit(func(option *flag.Flag) { query.Set(option.Name, option.Value.String()) })
	_, err := parseLatencyQuery(query.Encode())
	return query, err == nil
}

func operationLatencyOpenAPI(auth []openapi.SecurityRequirement) openapi.Op {
	date := openapi.Schema{Type: "string", Format: "date-time"}
	stats := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"samples": openapi.Integer, "p50_ms": openapi.Integer, "p95_ms": openapi.Integer, "max_ms": openapi.Integer,
	}}
	properties := func() map[string]openapi.Schema {
		return map[string]openapi.Schema{
			"accepted": openapi.Integer, "enqueued": openapi.Integer, "succeeded": openapi.Integer,
			"failed": openapi.Integer, "needs_review": openapi.Integer, "pending": openapi.Integer,
			"enqueue_to_first_lease": stats, "dispatch_to_observed_success": stats,
		}
	}
	provider := openapi.Schema{Type: "object", Properties: properties()}
	provider.Properties["provider"] = openapi.String
	report := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"as_of": date, "from": date, "until": date, "summary": {Type: "object", Properties: properties()},
		"providers": {Type: "array", Items: &provider},
		"scope": {Type: "object", Properties: map[string]openapi.Schema{
			"tenant": openapi.String, "project": openapi.String, "account": openapi.String, "region": openapi.String, "action": openapi.String,
		}},
	}}
	return openapi.Op{OperationID: "getProjectOperationLatency", Security: auth, Params: []openapi.Param{
		{Name: "from", In: "query", Schema: date, Required: true}, {Name: "until", In: "query", Schema: date, Required: true},
		{Name: "account", In: "query", Schema: openapi.String}, {Name: "region", In: "query", Schema: openapi.String},
		{Name: "action", In: "query", Schema: openapi.String},
	}, Responses: map[string]openapi.Response{
		"200": {Description: "按受理时间选定操作并统计首次领取和自动成功收敛耗时，毫秒分位数只覆盖有证据的样本。",
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
				Type: "object", Properties: map[string]openapi.Schema{"data": report},
			}}}},
		"400": {Description: "筛选项或最长 31 天的时间窗口无效"}, "401": {Description: "凭据缺失、过期或无效"},
		"403": {Description: "项目权限不足"}, "422": {Description: "窗口包含超过 10000 条操作"},
		"503": {Description: "操作账本不可用"},
	}}
}
