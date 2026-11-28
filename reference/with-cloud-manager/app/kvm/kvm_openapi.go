package kvmapp

import (
	"github.com/birdordev/plumego/x/openapi"
)

func OpenAPIHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	kvmProvisioningHints(hints, operation)
	kvmInventoryHints(hints, operation)
	kvmEvacuationInventoryHints(hints, operation)
	kvmVolumeCleanupHints(hints, operation)
	kvmVMHints(hints, operation)
	body := func(description string, properties map[string]openapi.Schema) *openapi.RequestBody {
		return &openapi.RequestBody{Required: true, Description: description,
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: properties}}}}
	}
	budgets := map[string]openapi.Schema{"vcpu": openapi.Integer, "memory_mib": openapi.Integer, "disk_mib": openapi.Integer, "gpu": openapi.Integer}

	register := operation("registerKVMHost", "201", "已登记宿主机。首次有效容量上报后可供放置。", true)
	register.Body = body("管理员登记宿主机 UUID、代理服务主体、故障域、网络和宿主机本地存储池。", map[string]openapi.Schema{
		"id": openapi.String, "uuid": openapi.String, "agent": openapi.String, "failure_domain": openapi.String,
		"networks": {Type: "array", Items: &openapi.String}, "storage_pool": openapi.String, "gpu_model": openapi.String,
		"shared_storage_pool_id": openapi.String,
	})

	change := operation("changeKVMHost", "200", "变更后的宿主机", true)
	change.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	change.Responses["412"] = openapi.Response{Description: "需要当前宿主机管理版本"}
	change.Body = body("管理员执行 drain、resume 或 rebind。rebind 需要新的代理服务主体。", map[string]openapi.Schema{
		"action": openapi.String, "agent": openapi.String,
	})
	fence := operation("changeKVMHostFence", "200", "按宿主机版本转换隔离状态并记录证据", true)
	fence.Params = change.Params
	fence.Responses["412"] = change.Responses["412"]
	fence.Body = body("管理员 request 或 request-rejoin，独立审批人 confirm、approve-rejoin 或 reject-rejoin。confirm 及 request-rejoin 需要现场证据摘要，request-rejoin 还需替换代理。",
		map[string]openapi.Schema{"action": openapi.String, "uuid": openapi.String,
			"evidence_sha256": openapi.String, "replacement_agent": openapi.String})
	hints["POST "+kvmHostFenceRoute] = fence
	hints["GET "+kvmHostFenceRoute] = operation("getKVMHostFence", "200", "宿主机当前状态和最近 20 条隔离证据事件", false)
	work := operation("listKVMHostFenceWork", "200", "阻止宿主机重新接入的虚拟机任务及尚待现场清理的疏散记录", false)
	work.Params = []openapi.Param{
		{Name: "after", In: "query", Description: "当前隔离代次上一页返回的 next", Schema: openapi.String},
		{Name: "limit", In: "query", Description: "默认 50，最大 100", Schema: openapi.Integer},
	}
	work.Responses["400"] = openapi.Response{Description: "分页参数无效"}
	work.Responses["412"] = openapi.Response{Description: "分页游标与当前宿主机或隔离代次不符"}
	hints["GET "+kvmHostFenceWorkRoute] = work
	cleanup := operation("confirmKVMEvacuationCleanup", "200", "登记源域和网络清理证据，解除该次疏散的接入阻塞", true)
	cleanup.Params = change.Params
	cleanup.Responses["412"] = change.Responses["412"]
	cleanup.Body = body("独立审批人提交仍隔离的源宿主机版本、VM ID、疏散 ID 和现场清理证据摘要。", map[string]openapi.Schema{
		"vm_id": openapi.String, "migration_id": openapi.String, "evidence_sha256": openapi.String,
	})
	hints["POST "+kvmEvacuationCleanupRoute] = cleanup
	batch := operation("requestKVMEvacuationBatch", "202", "原子受理一个源宿主机的疏散批次，各虚拟机独立审批", true)
	batch.Params = change.Params
	batch.Responses["409"] = openapi.Response{Description: "至少一台虚拟机或候选目标不满足本批要求"}
	batch.Responses["412"] = openapi.Response{Description: "需要当前源宿主机隔离代次、管理版本和每台虚拟机的版本"}
	item := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"vm_id": openapi.String, "vm_revision": openapi.Integer, "image_id": openapi.String, "evidence_sha256": openapi.String,
		"require_verified_backup": {Type: "boolean"},
	}}
	batch.Body = body("提交 1 至 16 个同源宿主机虚拟机及各自备份与现场证据。", map[string]openapi.Schema{
		"batch_id": openapi.String, "source_fence_generation": openapi.Integer, "items": {Type: "array", Items: &item},
	})
	hints["POST "+kvmEvacuationBatchesRoute] = batch
	hints["GET "+kvmEvacuationBatchRoute] = operation("getKVMEvacuationBatch", "200", "批次回执及每台虚拟机的当前审批与清理状态", false)

	plan := operation("armKVMEvacuationPlan", "201", "登记由独立隔离确认激活的疏散预案", true)
	plan.Params = change.Params
	plan.Responses["409"] = openapi.Response{Description: "隔离已确认、预案成员重复或资源状态不满足要求"}
	plan.Responses["412"] = openapi.Response{Description: "需要当前宿主机和每台虚拟机的版本"}
	plan.Body = body("项目管理员登记同源宿主机的 1 至 16 台虚拟机、各 VM 版本、备份和证据摘要。",
		map[string]openapi.Schema{"plan_id": openapi.String, "items": {Type: "array", Items: &item}})
	hints["POST "+kvmEvacuationPlansRoute] = plan
	hints["GET "+kvmEvacuationPlanRoute] = operation("getKVMEvacuationPlan", "200", "读取预案及其提交、重试或失败状态", false)
	cancel := operation("cancelKVMEvacuationPlan", "200", "撤销仍未提交疏散批次的预案", true)
	delete(cancel.Responses, "413")
	delete(cancel.Responses, "415")
	cancel.Responses["409"] = openapi.Response{Description: "预案已经提交或终止"}
	hints["DELETE "+kvmEvacuationPlanRoute] = cancel

	report := operation("reportKVMCapacity", "200", "成功接收的宿主机观测", true)
	report.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
	report.Responses["412"] = openapi.Response{Description: "代理代次、上报序号、重放内容或域证据不匹配"}
	report.Body = body("已绑定的代理发送宿主机 UUID、代次、单调上报序号及容量。", map[string]openapi.Schema{
		"uuid": openapi.String, "generation": openapi.Integer, "sequence": openapi.Integer,
		"capacity": {Type: "object", Properties: budgets},
		"shared_storage": {Type: "object", Properties: map[string]openapi.Schema{
			"id": openapi.String, "server": openapi.String, "export": openapi.String,
		}},
		"inventory": {Type: "object", Properties: map[string]openapi.Schema{
			"host_revision": openapi.Integer, "domains": {Type: "array", Items: &openapi.String},
		}},
	})
	inventory := operation("lookupKVMVMInventory", "200", "当前宿主机上找到的已绑定域", true)
	inventory.Security = report.Security
	inventory.Responses["412"] = openapi.Response{Description: "宿主机代理代次已变化"}
	inventory.Body = body("按宿主机批量核对最多 256 个 libvirt UUID。", map[string]openapi.Schema{
		"uuid": openapi.String, "generation": openapi.Integer, "domains": {Type: "array", Items: &openapi.String},
	})

	reserve := operation("reserveKVMPlacement", "201", "原子容量预留。相同幂等键和参数返回原预留。", true)
	reserve.Params = []openapi.Param{{Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String}}
	reserve.Responses["409"] = openapi.Response{Description: "没有满足约束和容量的宿主机，或幂等键已用于其他参数"}
	reserve.Body = body("指定 vCPU、内存、磁盘、网络、本地存储池、有效秒数和可选的 GPU、宿主机及故障域约束。", map[string]openapi.Schema{
		"vcpu": openapi.Integer, "memory_mib": openapi.Integer, "disk_mib": openapi.Integer, "gpu": openapi.Integer,
		"network": openapi.String, "storage_pool": openapi.String, "gpu_model": openapi.String, "ttl_seconds": openapi.Integer,
		"required_host": openapi.String, "failure_domain": openapi.String, "excluded_failure_domain": openapi.String,
		"ipv4": openapi.String, "evacuation_vm_id": openapi.String,
		"shared_storage_pool_id": openapi.String,
	})

	storage := operation("registerKVMSharedStorage", "201", "登记项目站点专用的 NFSv4 导出和磁盘预算", true)
	storage.Body = body("管理员指定规范的服务端 IPv4、导出路径、池 ID 和容量上限。", map[string]openapi.Schema{
		"id": openapi.String, "server": openapi.String, "export": openapi.String, "capacity_mib": openapi.Integer,
	})
	hints["POST "+kvmSharedStoragePoolsRoute] = storage
	getStorage := operation("getKVMSharedStorage", "200", "共享池身份、预算、当前预留和保留卷用量", false)
	getStorage.Responses["400"] = openapi.Response{Description: "无效的存储使用量查询选项"}
	getStorage.Params = []openapi.Param{{Name: "usage", In: "query", Description: "默认包含用量，false 只读取身份和预算", Schema: openapi.Schema{Type: "boolean"}}}
	hints["GET "+kvmSharedStoragePoolRoute] = getStorage

	image := operation("registerKVMBootImage", "201", "项目站点不可变 raw 镜像", true)
	image.Body = body("登记本地 raw 镜像的标识、SHA-256 及精确字节数。", map[string]openapi.Schema{
		"image_id": openapi.String, "image_sha256": openapi.String, "image_size_bytes": openapi.Integer,
	})
	image.Responses["409"] = openapi.Response{Description: "相同镜像标识已被登记"}
	hints["POST "+kvmBootImagesRoute] = image
	hints["GET "+kvmBootImageRoute] = operation("getKVMBootImage", "200", "项目站点镜像校验信息", false)
	hints["GET "+kvmHostImageRoute] = operation("getKVMHostImage", "200", "当前宿主机代次的本地镜像回执及有效期", false)
	checkImage := operation("checkKVMHostImage", "200", "绑定代理读取镜像目录指纹和回执版本", true)
	checkImage.Security = report.Security
	checkImage.Responses["412"] = openapi.Response{Description: "代理代次已变化"}
	checkImage.Body = body("在传输或校验之前核对宿主机 UUID 与代理代次。", map[string]openapi.Schema{
		"uuid": openapi.String, "generation": openapi.Integer,
	})
	hints["POST "+kvmHostImageCheckRoute] = checkImage
	reportImage := operation("reportKVMHostImage", "200", "写入镜像校验结果。相同版本和状态的重放返回原回执。", true)
	reportImage.Security = report.Security
	reportImage.Responses["412"] = openapi.Response{Description: "代理代次或回执版本已变化"}
	reportImage.Body = body("绑定代理报告 verified 或 missing，以及精确的目录指纹和上次读取的版本。", map[string]openapi.Schema{
		"uuid": openapi.String, "generation": openapi.Integer, "revision": openapi.Integer, "state": openapi.String,
		"image_sha256": openapi.String, "image_size_bytes": openapi.Integer,
	})
	hints["PUT "+kvmHostImageRoute] = reportImage

	pool := operation("registerKVMIPv4Pool", "201", "已登记项目站点网络的不可变 IPv4 地址池", true)
	pool.Body = body("包含规范的 IPv4 子网、网关和静态地址区间。", map[string]openapi.Schema{
		"network": openapi.String, "subnet": openapi.String, "gateway": openapi.String,
		"first_ip": openapi.String, "last_ip": openapi.String,
	})
	pool.Responses["409"] = openapi.Response{Description: "地址池已登记或该网络仍有活动预留"}
	hints["POST "+kvmIPv4PoolsRoute] = pool
	hints["GET "+kvmIPv4PoolRoute] = operation("getKVMIPv4Pool", "200", "项目站点网络的 IPv4 地址池", false)

	hostPool := operation("getKVMHostDHCPPlan", "200", "绑定宿主机允许配置的项目站点地址池", true)
	hostPool.Security = report.Security
	hostPool.Responses["412"] = openapi.Response{Description: "宿主机代理代次已变化"}
	hostPool.Body = body("提交当前绑定宿主机的 UUID 和代理代次。", map[string]openapi.Schema{
		"uuid": openapi.String, "generation": openapi.Integer,
	})
	hints["POST "+kvmHostIPv4PoolRoute] = hostPool

	hints["POST "+kvmHostsRoute] = register
	hints["GET "+kvmHostRoute] = operation("getKVMHost", "200", "项目宿主机及最近容量观测", false)
	hints["PATCH "+kvmHostRoute] = change
	hints["PUT "+kvmCapacityRoute] = report
	hints["POST "+kvmVMInventoryRoute] = inventory
	hints["POST "+kvmPlacementsRoute] = reserve
	hints["GET "+kvmPlacementRoute] = operation("getKVMPlacement", "200", "项目放置记录和当前有效性", false)
	hints["DELETE "+kvmPlacementRoute] = operation("releaseKVMPlacement", "200", "释放后的记录。已过期或释放的记录按当前状态返回。", true)
}
