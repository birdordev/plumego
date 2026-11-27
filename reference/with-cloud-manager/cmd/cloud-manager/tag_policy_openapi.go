package main

import "github.com/birdordev/plumego/x/openapi"

func tagPolicyOpenAPI(hints map[string]openapi.Op, readAuth, writeAuth []openapi.SecurityRequirement) {
	responses := func(status, description string) map[string]openapi.Response {
		return map[string]openapi.Response{
			status: {Description: description},
			"400":  {Description: "规则或查询参数无效"},
			"401":  {Description: "凭据缺失、过期或无效"},
			"403":  {Description: "项目权限不足"},
			"503":  {Description: "标签治理存储不可用"},
		}
	}
	latest := openapi.Op{OperationID: "getProjectTagPolicy", Security: readAuth,
		Params:    []openapi.Param{{Name: "revision", In: "query", Schema: openapi.Integer, Description: "省略时读取当前规则"}},
		Responses: responses("200", "规则按版本保存，ETag 为读取到的版本")}
	latest.Responses["404"] = openapi.Response{Description: "项目或指定历史版本没有规则"}
	hints["GET "+tagPolicyRoute] = latest

	rule := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"kind": openapi.String, "required_keys": {Type: "array", Items: &openapi.String},
	}}
	body := &openapi.RequestBody{Required: true, Description: "必填 rules 和 max_age_seconds。每种资产类型至多一条规则，每条包含 kind 和 required_keys。空规则数组停用评估。证据有效期为 60 至 604800 秒。可选 alert_on_noncompliance，首版省略时关闭，更新时省略则沿用上一版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{
				"rules": {Type: "array", Items: &rule}, "max_age_seconds": openapi.Integer,
				"alert_on_noncompliance": {Type: "boolean"},
			}}}}}
	create := openapi.Op{OperationID: "createProjectTagPolicy", Security: writeAuth, Body: body,
		Responses: responses("201", "由项目管理员发布首版规则和 ETag")}
	create.Responses["409"] = openapi.Response{Description: "规则已经存在"}
	update := openapi.Op{OperationID: "updateProjectTagPolicy", Security: writeAuth, Body: body,
		Params:    []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}},
		Responses: responses("200", "由项目管理员发布新版本和 ETag")}
	update.Responses["412"] = openapi.Response{Description: "需要规则当前的强 ETag"}
	for _, entry := range []map[string]openapi.Response{create.Responses, update.Responses} {
		entry["413"] = openapi.Response{Description: "正文超出大小限制"}
		entry["415"] = openapi.Response{Description: "媒体类型无效"}
	}
	hints["POST "+tagPolicyRoute], hints["PUT "+tagPolicyRoute] = create, update

	report := openapi.Op{OperationID: "listProjectTagCompliance", Security: readAuth,
		Params: []openapi.Param{
			{Name: "kind", In: "query", Required: true, Schema: openapi.String},
			{Name: "state", In: "query", Schema: openapi.String, Description: "compliant、noncompliant 或 unknown"},
			{Name: "account", In: "query", Schema: openapi.String},
			{Name: "region", In: "query", Schema: openapi.String},
			{Name: "cursor", In: "query", Schema: openapi.String},
			{Name: "limit", In: "query", Schema: openapi.Integer},
		}, Responses: responses("200", "返回当前规则版本、资产合规状态、证据时间、缺少的键和分页游标")}
	report.Responses["404"] = openapi.Response{Description: "当前规则未覆盖该资产类型"}
	hints["GET "+tagComplianceRoute] = report
}
