package accounts

import (
	"github.com/birdordev/plumego/x/openapi"
)

func InventoryScheduleOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	list := operation("listInventorySchedules", "200", "当前云账号的盘点、对象桶配置及数据库采集计划。数据库计划的 target_id 为实例 ID。对象桶计划的 bucket_batch_rejected 表示最近提交批次中未能更新观测的桶数。", false)
	list.Params = []openapi.Param{
		{Name: "after", In: "query", Schema: openapi.String, Description: "上一页返回的 next"},
		{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100"},
	}
	list.Responses["400"] = openapi.Response{Description: "分页参数无效"}
	create := operation("registerInventorySchedule", "201", "为指定资源作用域登记盘点计划", true)
	update := operation("changeInventorySchedule", "200", "更新盘点间隔、启用状态或立即触发执行", true)
	for _, op := range []*openapi.Op{&create, &update} {
		op.Responses["409"] = openapi.Response{Description: "计划已存在，或账号尚不可读"}
	}
	create.Body = scheduleBody("region 和 kind 指定作用域。对象桶配置须使用 global。database-backups 和 database-restore-windows 须提供实例地域和 target_id，目标必须已盘点、当前项目可见且支持对应采集能力。", map[string]openapi.Schema{
		"region": openapi.String, "kind": openapi.String, "target_id": openapi.String,
		"interval_seconds": openapi.Integer, "enabled": {Type: "boolean"},
	})
	update.Body = scheduleBody("启用的计划可用 run_now 重新排队。", map[string]openapi.Schema{
		"interval_seconds": openapi.Integer, "enabled": {Type: "boolean"}, "run_now": {Type: "boolean"},
	})
	update.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	update.Responses["412"] = openapi.Response{Description: "需要当前计划版本"}
	hints["GET "+AccountSchedulesRoute] = list
	hints["POST "+AccountSchedulesRoute] = create
	hints["PATCH "+AccountScheduleRoute] = update
}

func scheduleBody(description string, fields map[string]openapi.Schema) *openapi.RequestBody {
	return &openapi.RequestBody{Required: true, Description: description,
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: fields}}}}
}

func AccountInventoryOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	for _, list := range []struct {
		route, id, description string
		fields                 map[string]openapi.Schema
	}{
		{AccountsRoute, "listCloudAccounts", "当前项目自有的云账号元数据", map[string]openapi.Schema{
			"id": openapi.String, "project": openapi.String, "provider": openapi.String, "mode": openapi.String, "revision": openapi.Integer,
		}},
		{AccountGrantsRoute, "listCloudAccountGrants", "当前自有账号的项目共享授权", map[string]openapi.Schema{
			"project": openapi.String, "allow_write": {Type: "boolean"},
		}},
	} {
		op := operation(list.id, "200", list.description, false)
		op.Params = []openapi.Param{
			{Name: "after", In: "query", Schema: openapi.String, Description: "同一列表上一页的 next。"},
			{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
		}
		item := openapi.Schema{Type: "object", Properties: list.fields}
		response := op.Responses["200"]
		response.Content = map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object", Properties: map[string]openapi.Schema{"data": {Type: "object", Properties: map[string]openapi.Schema{
				"items": {Type: "array", Items: &item}, "next": openapi.String,
			}}},
		}}}
		op.Responses["200"] = response
		op.Responses["400"] = openapi.Response{Description: "分页参数无效"}
		hints["GET "+list.route] = op
	}
}

func AccountRolloutOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	read := operation("getAccountOperationRollout", "200", "读取项目自有云账号的操作放量策略和版本", false)
	replace := operation("replaceAccountOperationRollout", "200", "按地域和动作替换白名单。空数组冻结新的派发。", true)
	replace.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	replace.Responses["409"] = openapi.Response{Description: "账号厂商不支持操作放量策略"}
	replace.Responses["412"] = openapi.Response{Description: "需要当前策略 ETag"}
	rule := openapi.Schema{Type: "object",
		Properties: map[string]openapi.Schema{"region": openapi.String, "action": openapi.String}}
	replace.Body = &openapi.RequestBody{Required: true, Description: "rules 必填，每条规则包含 region 和 action。", Content: map[string]openapi.MediaType{
		"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"rules": {Type: "array", Items: &rule},
		}}},
	}}
	hints["GET "+AccountRolloutRoute] = read
	hints["PUT "+AccountRolloutRoute] = replace
}
