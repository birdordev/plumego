package main

import "github.com/birdordev/plumego/x/openapi"

func kvmLiveMigrationHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	request := operation("requestKVMLiveMigration", "202", "登记同共享池的运行中 VM 迁移请求", true)
	request.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	request.Responses["412"] = openapi.Response{Description: "当前 VM 版本已变化"}
	request.Body = serviceCatalogBody([]string{"migration_id", "target_host"}, false)
	hints["POST "+kvmLiveMigrationsRoute] = request
	hints["GET "+kvmLiveMigrationRoute] = operation("getKVMLiveMigration", "200", "读取迁移状态与控制面交接时间", false)
	review := operation("reviewKVMLiveMigration", "200", "独立审批并预留目标计算资源，派发前可拒绝", true)
	review.Params = request.Params
	review.Responses["412"] = request.Responses["412"]
	review.Body = serviceCatalogBody([]string{"decision"}, false)
	hints["POST "+kvmLiveMigrationRoute+"/review"] = review
	abort := operation("abortFencedKVMLiveMigration", "200", "独立确认两端隔离及目标清理后终止已派发迁移", true)
	abort.Params = request.Params
	abort.Responses["412"] = openapi.Response{Description: "需要当前迁移版本"}
	abort.Body = serviceCatalogBody([]string{"evidence_sha256"}, false)
	hints["POST "+kvmLiveMigrationRoute+"/abort"] = abort
	agent := operation("applyKVMLiveMigrationCommand", "200", "绑定源或目标代理推进同一迁移代次", true)
	agent.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
	agent.Responses["412"] = openapi.Response{Description: "代理、宿主机、VM 或迁移版本已变化"}
	agent.Body = serviceCatalogBody([]string{"uuid", "action", "network_sha256", "source_mac"}, false)
	properties := agent.Body.Content["application/json"].Schema.Properties
	properties["generation"], properties["revision"] = openapi.Integer, openapi.Integer
	hints["POST "+kvmLiveMigrationAgentRoute] = agent
}
