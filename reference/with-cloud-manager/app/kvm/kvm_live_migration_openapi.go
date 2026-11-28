package kvmapp

import (
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/app/handlers/transport"
)

func kvmLiveMigrationHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	list := operation("listKVMLiveMigrations", "200", "虚拟机实时迁移记录，最近申请优先。有更多记录时返回 next。", false)
	list.Params = []openapi.Param{
		{Name: "after", In: "query", Schema: openapi.String, Description: "同一虚拟机上一页返回的 next。"},
		{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
	}
	list.Responses["400"] = openapi.Response{Description: "分页参数无效"}
	hints["GET "+kvmLiveMigrationsRoute] = list
	targets := operation("listKVMLiveMigrationTargets", "200", "管理员查看同站点符合登记配置与容量采样条件的候选宿主机。审批时复核实际可用容量。", false)
	targets.Params = list.Params
	targets.Responses["400"] = list.Responses["400"]
	targets.Responses["409"] = openapi.Response{Description: "源虚拟机当前不满足实时迁移条件"}
	hints["GET "+kvmLiveMigrationTargetsRoute] = targets
	request := operation("requestKVMLiveMigration", "202", "登记同共享池的运行中 VM 迁移请求", true)
	request.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	request.Responses["412"] = openapi.Response{Description: "当前 VM 版本已变化"}
	request.Body = transport.ServiceCatalogBody([]string{"migration_id", "target_host"}, false)
	hints["POST "+kvmLiveMigrationsRoute] = request
	hints["GET "+kvmLiveMigrationRoute] = operation("getKVMLiveMigration", "200", "读取迁移状态与控制面交接时间", false)
	review := operation("reviewKVMLiveMigration", "200", "独立审批并预留目标计算资源，派发前可拒绝", true)
	review.Params = request.Params
	review.Responses["412"] = request.Responses["412"]
	review.Body = transport.ServiceCatalogBody([]string{"decision"}, false)
	hints["POST "+kvmLiveMigrationRoute+"/review"] = review
	abort := operation("abortFencedKVMLiveMigration", "200", "独立确认两端隔离及目标清理后终止已派发迁移", true)
	abort.Params = request.Params
	abort.Responses["412"] = openapi.Response{Description: "需要当前迁移版本"}
	abort.Body = transport.ServiceCatalogBody([]string{"evidence_sha256"}, false)
	hints["POST "+kvmLiveMigrationRoute+"/abort"] = abort
	agent := operation("applyKVMLiveMigrationCommand", "200", "绑定源或目标代理推进同一迁移代次", true)
	agent.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
	agent.Responses["412"] = openapi.Response{Description: "代理、宿主机、VM 或迁移版本已变化"}
	agent.Body = transport.ServiceCatalogBody([]string{"uuid", "action", "network_sha256", "source_mac"}, false)
	properties := agent.Body.Content["application/json"].Schema.Properties
	properties["generation"], properties["revision"] = openapi.Integer, openapi.Integer
	hints["POST "+kvmLiveMigrationAgentRoute] = agent
}
