package operationhttp

import (
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/app/handlers/transport"
)

const DeploymentsRoute = "/v1/tenants/:tenant/projects/:project/bare-metal/deployments"
const DeploymentRoute = DeploymentsRoute + "/:deployment"
const DeploymentReceiptRoute = DeploymentRoute + "/receipt"
const ReclaimsRoute = "/v1/tenants/:tenant/projects/:project/bare-metal/reclaims"
const ReclaimRoute = ReclaimsRoute + "/:reclaim"

func BareMetalDeploymentHints(hints map[string]openapi.Op, op func(string, string, string, bool) openapi.Op) {
	register := op("registerBareMetalDeployment", "201", "部署已关联已确认的 PXE 和待审批开机操作", true)
	register.Body = transport.ServiceCatalogBody([]string{"stage_operation", "boot_operation", "image_sha256", "installer_id"}, false)
	register.Responses["412"] = openapi.Response{Description: "账号或资产代次已变化"}
	hints["POST "+DeploymentsRoute] = register
	hints["GET "+DeploymentRoute] = op("getBareMetalDeployment", "200", "查看部署及安装收据", false)
	receipt := op("recordBareMetalDeploymentReceipt", "200", "已登记安装结果与证据摘要", true)
	receipt.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
	receipt.Body = transport.ServiceCatalogBody([]string{"system_id", "image_sha256", "result", "evidence_sha256"}, false)
	receipt.Responses["412"] = register.Responses["412"]
	hints["POST "+DeploymentReceiptRoute] = receipt
}

func BareMetalReclaimHints(hints map[string]openapi.Op, op func(string, string, string, bool) openapi.Op) {
	start := op("requestBareMetalReclaim", "202", "已登记并阻止同一设备的电源与启动配置操作", true)
	start.Params = []openapi.Param{
		{Name: "If-Match", In: "header", Required: true, Schema: openapi.String},
		{Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String},
	}
	start.Body = transport.ServiceCatalogBody([]string{"account", "region", "system_id", "sanitizer_id"}, false)
	stale := openapi.Response{Description: "资产或授权代次已变化"}
	start.Responses["412"] = stale
	hints["POST "+ReclaimsRoute] = start
	hints["GET "+ReclaimRoute] = op("getBareMetalReclaim", "200", "回收状态与清理证据摘要", false)
	current := op("getCurrentBareMetalReclaim", "200", "本项目设备的当前回收记录，空闲时 reclaim 为 null", false)
	for _, name := range []string{"account", "region", "system_id"} {
		current.Params = append(current.Params, openapi.Param{Name: name, In: "query", Required: true, Schema: openapi.String})
	}
	current.Responses["400"] = openapi.Response{Description: "设备坐标缺失、重复或格式错误"}
	hints["GET "+ReclaimsRoute+"/current"] = current
	review := op("reviewBareMetalReclaim", "200", "独立审批人批准或拒绝", true)
	review.Body = transport.ServiceCatalogBody([]string{"decision"}, false)
	review.Responses["412"] = stale
	hints["POST "+ReclaimRoute+"/approval"] = review
	claim := op("claimBareMetalReclaim", "200", "指定站点服务单次领取清理授权", true)
	claim.Body = transport.ServiceCatalogBody([]string{"system_id"}, false)
	claim.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
	claim.Responses["412"] = stale
	hints["POST "+ReclaimRoute+"/claim"] = claim
	receipt := op("recordBareMetalReclaim", "200", "清理证据及本轮关机观测已核对", true)
	receipt.Body = transport.ServiceCatalogBody([]string{"system_id", "evidence_sha256"}, false)
	receipt.Security = claim.Security
	receipt.Responses["412"] = stale
	hints["POST "+ReclaimRoute+"/receipt"] = receipt
	recovery := op("requestBareMetalReclaimRecovery", "200", "记录待独立审批的隔离及清理证据", true)
	recovery.Body = transport.ServiceCatalogBody([]string{"recovery_id", "system_id", "isolation_sha256", "evidence_sha256"}, false)
	recovery.Responses["412"] = stale
	hints["POST "+ReclaimRoute+"/recovery"] = recovery
	decision := op("reviewBareMetalReclaimRecovery", "200", "确认恢复证据或拒绝申请", true)
	decision.Body = transport.ServiceCatalogBody([]string{"recovery_id", "decision"}, false)
	decision.Responses["412"] = stale
	hints["POST "+ReclaimRoute+"/recovery/approval"] = decision
}
