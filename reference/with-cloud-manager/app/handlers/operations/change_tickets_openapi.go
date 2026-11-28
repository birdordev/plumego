package operationhttp

import (
	"github.com/birdordev/plumego/x/openapi"
)

func ChangeTicketOpenAPI(hints map[string]openapi.Op, readAuth, writeAuth []openapi.SecurityRequirement) {
	responses := func(status, description string, hasBody bool) map[string]openapi.Response {
		result := map[string]openapi.Response{
			status: {Description: description},
			"400":  {Description: "无效的工单请求或查询参数"},
			"401":  {Description: "凭据缺失、过期或无效"},
			"403":  {Description: "项目或工单权限不足"},
			"503":  {Description: "工单存储不可用"},
		}
		if hasBody {
			result["413"] = openapi.Response{Description: "正文超出大小限制"}
			result["415"] = openapi.Response{Description: "媒体类型无效"}
		} else {
			result["404"] = openapi.Response{Description: "项目或操作没有所请求的工单策略"}
		}
		return result
	}
	policyBody := &openapi.RequestBody{Required: true, Description: "actions 为精确动作列表。单独使用 * 覆盖所有操作，更新为空数组时停用。verifier 为项目中仅有查看权限的服务主体。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"actions": {Type: "array", Items: &openapi.String}, "verifier": openapi.String}}}}}
	get := openapi.Op{OperationID: "getProjectChangeTicketPolicy", Security: readAuth,
		Params:    []openapi.Param{{Name: "revision", In: "query", Schema: openapi.Integer}},
		Responses: responses("200", "项目管理员读取策略版本和强 ETag", false)}
	create := openapi.Op{OperationID: "createProjectChangeTicketPolicy", Security: writeAuth, Body: policyBody,
		Responses: responses("201", "项目管理员发布首个工单策略", true)}
	create.Responses["409"] = openapi.Response{Description: "工单策略已存在"}
	update := openapi.Op{OperationID: "updateProjectChangeTicketPolicy", Security: writeAuth, Body: policyBody,
		Params:    []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}},
		Responses: responses("200", "发布新的策略版本，已受理操作保留原版本", true)}
	update.Responses["412"] = openapi.Response{Description: "需要当前策略的强 ETag"}
	hints["GET "+TicketPolicyRoute], hints["POST "+TicketPolicyRoute], hints["PUT "+TicketPolicyRoute] = get, create, update

	hints["GET "+ChangeTicketRoute] = openapi.Op{OperationID: "getOperationChangeTicket", Security: readAuth,
		Responses: responses("200", "返回固定策略版本、最近工单证据及当前是否允许平台审批", false)}
	receipt := openapi.Op{OperationID: "recordOperationChangeTicket", Security: []openapi.SecurityRequirement{{"serviceBearer": {}}},
		Body: &openapi.RequestBody{Required: true, Description: "策略指定的服务主体回报工单审批或撤回。source_revision 是递增的十进制字符串，approved 需要 24 小时以内的 valid_until，revoked 省略 valid_until。",
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
				Properties: map[string]openapi.Schema{"ticket_id": openapi.String, "source_revision": openapi.String,
					"decision": openapi.String, "valid_until": {Type: "string", Format: "date-time"}}}}}},
		Responses: responses("200", "持久化工单证据，完全相同的当前回执安全重放", true)}
	receipt.Responses["409"] = openapi.Response{Description: "工单绑定、版本或操作状态冲突"}
	hints["POST "+ChangeTicketRoute] = receipt
}
