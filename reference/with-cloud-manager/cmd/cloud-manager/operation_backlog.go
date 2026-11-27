package main

import (
	"flag"
	"io"
	"net/url"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/operations"
)

const operationBacklogRoute = "/v1/tenants/:tenant/projects/:project/operation-backlog"

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

func backlogClientQuery(args []string) (url.Values, bool) {
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

func operationBacklogOpenAPI(auth []openapi.SecurityRequirement) openapi.Op {
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
