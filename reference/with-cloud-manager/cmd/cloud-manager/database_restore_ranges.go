package main

import (
	"maps"
	"net/http"
	"net/url"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/inventory"
)

func databaseRecoveryOpenAPI(submit openapi.Op) openapi.Op {
	op := submit
	op.OperationID = "recoverDatabaseInstance"
	op.Summary = "申请从腾讯云、阿里云或华为云 MySQL 的指定时间点恢复到新实例"
	op.Responses = maps.Clone(submit.Responses)
	op.Responses["202"] = openapi.Response{Description: "恢复到新实例的申请，需独立审批"}
	op.Responses["412"] = openapi.Response{Description: "源资产归属、指定快照或隔离目标拓扑失效"}
	properties := make(map[string]openapi.Schema)
	for _, key := range []string{"account", "region", "source_id", "name", "vpc_id", "subnet_id", "security_group_id", "zone", "flavor", "storage_type"} {
		properties[key] = openapi.String
	}
	properties["observed_at"] = openapi.Schema{Type: "string", Format: "date-time"}
	properties["restore_time"] = properties["observed_at"]
	for _, key := range []string{"storage_gib", "cpu", "memory_mib"} {
		properties[key] = openapi.Integer
	}
	op.Body = &openapi.RequestBody{Required: true,
		Description: "If-Match 使用源实例归属版本。恢复时间使用带时区的 RFC 3339 时间。华为云最多毫秒精度，腾讯云和阿里云精确到秒。security_group_id 在腾讯云和华为云必填，cpu 和 memory_mib 在腾讯云必填。目标必须为同项目的独立 VPC，符合 Worker 配置。",
		Content:     map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: properties}}}}
	return op
}

func databaseRestoreWindowsQuery(r *http.Request) (inventory.DatabaseAssetQuery, error) {
	if len(r.URL.RawQuery) > 2048 {
		return inventory.DatabaseAssetQuery{}, inventory.ErrInvalidQuery
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 2 || len(values["account"]) != 1 || len(values["region"]) != 1 {
		return inventory.DatabaseAssetQuery{}, inventory.ErrInvalidQuery
	}
	return inventory.DatabaseAssetQuery{
		Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project"), ID: router.Param(r, "asset"),
		Account: values.Get("account"), Region: values.Get("region"),
	}, nil
}

func databaseRestoreWindowsOpenAPI(auth []openapi.SecurityRequirement) openapi.Op {
	timeSchema := openapi.Schema{Type: "string", Format: "date-time"}
	window := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"start_at": timeSchema, "end_at": timeSchema,
	}}
	snapshot := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"observed_at": timeSchema,
		"scope":       openapi.String,
		"windows":     {Type: "array", Items: &window},
	}}
	return openapi.Op{OperationID: "getDatabaseRestoreWindows", Security: auth, Summary: "读取厂商报告的托管数据库时间点恢复范围",
		Params: []openapi.Param{
			{Name: "account", In: "query", Required: true, Schema: openapi.String},
			{Name: "region", In: "query", Required: true, Schema: openapi.String},
		}, Responses: map[string]openapi.Response{
			"200": {Description: "厂商报告的 UTC 时间窗和快照时间。instance 为实例级范围，source_instance 表示目标为源实例。空数组表示厂商报告无可恢复时间窗。",
				Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
					Type: "object", Properties: map[string]openapi.Schema{"data": snapshot},
				}}}},
			"400": {Description: "作用域无效"},
			"401": {Description: "凭据无效"},
			"403": {Description: "项目授权无效"},
			"404": {Description: "资产不可见或没有当前凭据的快照"},
			"503": {Description: "盘点存储不可用"},
		}}
}
