package main

import "github.com/birdordev/plumego/x/openapi"

func kvmVMHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	submit := operation("submitKVMVM", "202", "固定预留和启动卷的待审批虚拟机任务", true)
	submit.Body = serviceCatalogBody([]string{"placement_id", "boot_volume", "image_id"}, false)
	submit.Body.Description = "绑定已预置的独占 boot_volume 或已登记的 image_id，两者择一。"
	review := operation("reviewKVMVM", "200", "独立审批后的虚拟机任务", true)
	review.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	review.Responses["412"] = openapi.Response{Description: "需要当前虚拟机任务版本"}
	review.Body = serviceCatalogBody([]string{"decision"}, false)
	shutdown := operation("requestKVMVMShutdown", "202", "已持久受理的待审批 ACPI 关机申请", true)
	shutdown.Params = review.Params
	shutdown.Responses["412"] = review.Responses["412"]
	shutdown.Responses["409"] = openapi.Response{Description: "虚拟机状态不允许提交关机"}
	hints["POST "+kvmVMShutdownRoute] = shutdown
	retirement := operation("requestKVMVMRetirement", "202", "已持久受理的待审批退役申请", true)
	retirement.Params = review.Params
	retirement.Responses["412"] = review.Responses["412"]
	retirement.Responses["409"] = openapi.Response{Description: "虚拟机尚未处于已确认关机状态"}
	hints["POST "+kvmVMRetirementRoute] = retirement
	hints["POST "+kvmVMsRoute] = submit
	hints["GET "+kvmVMRoute] = operation("getKVMVM", "200", "项目虚拟机的任务状态和观测", false)
	hints["POST "+kvmVMRoute+"/review"] = review

	for _, item := range []struct{ method, path, id, description string }{
		{"POST", kvmVMPollRoute, "pollKVMVM", "宿主机代理领取一个任务描述。返回空对象表示没有任务。"},
		{"POST", kvmVMJobRoute + "/arm", "armKVMVM", "登记下一项宿主机写入，仅允许一次。"},
		{"POST", kvmVMImageStageRoute, "authorizeKVMImageStage", "复核准备镜像时的代理代次、当前版本和独立审批。"},
		{"PUT", kvmVMJobRoute + "/observation", "observeKVMVM", "报告同一持久域的只读观测，缺失或不符继续保留预留。"},
	} {
		op := operation(item.id, "200", item.description, true)
		op.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
		op.Responses["412"] = openapi.Response{Description: "宿主机代次或虚拟机任务版本已变化"}
		fields := map[string]openapi.Schema{"uuid": openapi.String, "generation": openapi.Integer}
		if item.path != kvmVMPollRoute {
			fields["revision"] = openapi.Integer
		}
		if item.method == "PUT" {
			fields["observed"] = openapi.String
		}
		op.Body = &openapi.RequestBody{Required: true, Description: item.description,
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: fields}}}}
		hints[item.method+" "+item.path] = op
	}
}
