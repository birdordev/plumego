package main

import "github.com/birdordev/plumego/x/openapi"

func kvmHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
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
	})

	change := operation("changeKVMHost", "200", "变更后的宿主机", true)
	change.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	change.Responses["412"] = openapi.Response{Description: "需要当前宿主机管理版本"}
	change.Body = body("管理员执行 drain、resume 或 rebind。rebind 需要新的代理服务主体。", map[string]openapi.Schema{
		"action": openapi.String, "agent": openapi.String,
	})

	report := operation("reportKVMCapacity", "200", "成功接收的宿主机观测", true)
	report.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
	report.Responses["412"] = openapi.Response{Description: "代理代次、上报序号、重放内容或域证据不匹配"}
	report.Body = body("已绑定的代理发送宿主机 UUID、代次、单调上报序号及容量。", map[string]openapi.Schema{
		"uuid": openapi.String, "generation": openapi.Integer, "sequence": openapi.Integer,
		"capacity": {Type: "object", Properties: budgets},
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
		"required_host": openapi.String, "failure_domain": openapi.String, "excluded_failure_domain": openapi.String, "ipv4": openapi.String,
	})

	image := operation("registerKVMBootImage", "201", "项目站点不可变 raw 镜像", true)
	image.Body = body("登记本地 raw 镜像的标识、SHA-256 及精确字节数。", map[string]openapi.Schema{
		"image_id": openapi.String, "image_sha256": openapi.String, "image_size_bytes": openapi.Integer,
	})
	image.Responses["409"] = openapi.Response{Description: "相同镜像标识已被登记"}
	hints["POST "+kvmBootImagesRoute] = image
	hints["GET "+kvmBootImageRoute] = operation("getKVMBootImage", "200", "项目站点镜像校验信息", false)

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
