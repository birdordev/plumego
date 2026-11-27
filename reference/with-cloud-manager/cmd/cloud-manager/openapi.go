package main

import (
	"net/http"
	"strings"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/openapi"
)

func registerOpenAPI(app *core.App, console *consoleConfig) error {
	consoleEnabled := console != nil
	var encoded []byte
	if err := app.Get("/openapi.json", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(encoded); err != nil {
			app.Logger().Warn("OpenAPI response write failed", plumelog.Fields{})
		}
	})); err != nil {
		return err
	}
	schemes := map[string]openapi.SecurityScheme{"serviceBearer": {Type: "http", Scheme: "bearer"}}
	readAuth := []openapi.SecurityRequirement{{"serviceBearer": {}}}
	writeAuth := readAuth
	if consoleEnabled {
		schemes["consoleSession"] = openapi.SecurityScheme{Type: "apiKey", In: "cookie", Name: consoleCookie}
		schemes["consoleCSRF"] = openapi.SecurityScheme{Type: "apiKey", In: "header", Name: consoleCSRFHeader}
		readAuth = []openapi.SecurityRequirement{{"serviceBearer": {}}, {"consoleSession": {}}}
		writeAuth = []openapi.SecurityRequirement{{"serviceBearer": {}}, {"consoleSession": {}, "consoleCSRF": {}}}
	}
	generator := openapi.New().WithComponents(&openapi.Components{SecuritySchemes: schemes})
	list := openapi.Op{
		OperationID: "listProjectAssets",
		Security:    readAuth,
		Responses: map[string]openapi.Response{
			"200": {Description: "项目可见的资产页和后续游标。已分配的弹性公网 IP 包含 public_addresses。云盘快照包含 parent_id 和已知的 snapshot.class、snapshot.size_gib。负载均衡包含 load_balancer 的产品、地址类型、已知域名及私网地址，也可返回多个公网地址。托管数据库包含 database_instance 的产品、引擎和已知版本。对象存储桶包含 object_bucket 的产品、实际地域、可用存储类型和创建时间。托管缓存包含 cache_instance 的产品、引擎、已知版本和容量。Kubernetes 集群包含 kubernetes_cluster 的产品、类型和已知版本及节点数。"},
			"400": {Description: "查询参数或游标无效"},
			"401": {Description: "服务凭据缺失、无效、过期或撤销"},
			"403": {Description: "项目不可访问"},
			"503": {Description: "资产存储不可用"},
		},
	}
	for _, name := range []string{"account", "region", "kind", "q", "cursor"} {
		list.Params = append(list.Params, openapi.Param{Name: name, In: "query", Schema: openapi.String})
	}
	list.Params = append(list.Params, openapi.Param{Name: "limit", In: "query", Schema: openapi.Integer})
	history := list
	history.OperationID = "listProjectAssetChanges"
	history.Responses = map[string]openapi.Response{}
	for status, response := range list.Responses {
		history.Responses[status] = response
	}
	history.Responses["200"] = openapi.Response{Description: "项目可见的资产差异和后续游标，包含适用的前后公网地址、快照属性、负载均衡详情、托管数据库、对象存储桶、托管缓存或 Kubernetes 集群详情。"}
	export := list
	export.OperationID = "exportProjectAssets"
	export.Params = export.Params[:4]
	export.Responses = map[string]openapi.Response{}
	for status, response := range list.Responses {
		export.Responses[status] = response
	}
	export.Responses["200"] = openapi.Response{Description: "筛选后的资产 CSV。kind=elastic-ip 追加 public_addresses。kind=snapshot 追加 source_volume_id、snapshot_class 和 size_gib。kind=load-balancer 追加公网地址、产品、地址类型、域名和私网地址。kind=database-instance 追加 product、engine、engine_version。kind=object-bucket 追加 product、location、storage_class 和 created_at。kind=cache-instance 追加 product、engine、engine_version 和 capacity_mib。kind=kubernetes-cluster 追加 product、cluster_type、kubernetes_version 和 node_count。",
		Content: map[string]openapi.MediaType{"text/csv": {Schema: openapi.String}}}
	export.Responses["422"] = openapi.Response{Description: "匹配资产超过单次导出上限"}
	topology := list
	topology.OperationID = "listProjectAssetTopology"
	topology.Summary = "查询已盘点的资源及其关联，每页最多 50 个源资源"
	lbListener := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"id": openapi.String, "name": openapi.String, "protocol": openapi.String,
		"port": openapi.Integer, "end_port": openapi.Integer,
		"target_group_ids": {Type: "array", Items: &openapi.String},
	}}
	lbTargetGroup := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"id": openapi.String, "name": openapi.String, "kind": openapi.String, "protocol": openapi.String, "algorithm": openapi.String, "exclusive": {Type: "boolean"},
	}}
	lbBackendMember := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"key": openapi.String, "group_id": openapi.String, "port": openapi.Integer, "weight": openapi.Integer, "role": openapi.String, "weight_editable": {Type: "boolean"},
	}}
	lbBackendCheck := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"key": openapi.String, "listener_id": openapi.String, "group_id": openapi.String, "rule_id": openapi.String,
		"port": openapi.Integer, "listener_port": openapi.Integer, "status": openapi.String,
	}}
	lbData := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"observed_at": {Type: "string", Format: "date-time"},
		"listeners":   {Type: "array", Items: &lbListener}, "target_groups": {Type: "array", Items: &lbTargetGroup},
		"backends": {Type: "object", Properties: map[string]openapi.Schema{
			"members": {Type: "array", Items: &lbBackendMember}, "checks": {Type: "array", Items: &lbBackendCheck},
		}},
	}}
	lbConfiguration := openapi.Op{
		OperationID: "readProjectLoadBalancerConfiguration", Security: readAuth,
		Summary: "读取负载均衡监听器和后端组的最近完整快照",
		Params: []openapi.Param{
			{Name: "account", In: "query", Required: true, Schema: openapi.String},
			{Name: "region", In: "query", Required: true, Schema: openapi.String},
		},
		Responses: map[string]openapi.Response{
			"200": {Description: "最近完整配置快照。采集时启用后端盘点才提供 backends，其中包含后端成员及其实际报告的健康结果。key 仅在当前快照内用于关联。", Content: map[string]openapi.MediaType{
				"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{"data": lbData}}},
			}},
			"400": {Description: "查询参数无效"}, "401": {Description: "服务凭据无效"},
			"403": {Description: "项目不可访问"}, "404": {Description: "未刷新、父实例不可见或快照已失效"},
			"503": {Description: "资产存储不可用"},
		},
	}
	dnsRecord := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"id": openapi.String, "name": openapi.String, "type": openapi.String, "ttl": openapi.Integer,
		"status": openapi.String, "line": openapi.String, "value_count": openapi.Integer,
		"values": {Type: "array", Items: &openapi.String},
	}}
	dnsRecords := openapi.Op{
		OperationID: "readProjectDNSZoneRecords", Security: readAuth,
		Summary: "分页读取公网 DNS 托管区的最近完整记录集快照",
		Params: []openapi.Param{
			{Name: "account", In: "query", Required: true, Schema: openapi.String},
			{Name: "offset", In: "query", Description: "从零开始的行偏移", Schema: openapi.Integer},
			{Name: "limit", In: "query", Description: "每页 1 至 100 条，默认 50", Schema: openapi.Integer},
		},
		Responses: map[string]openapi.Response{
			"200": {Description: "最近完整快照的一页，TXT 等类型只返回值数量。", Content: map[string]openapi.MediaType{
				"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
					"data": {Type: "object", Properties: map[string]openapi.Schema{
						"observed_at": {Type: "string", Format: "date-time"}, "records": {Type: "array", Items: &dnsRecord},
						"total": openapi.Integer, "next_offset": openapi.Integer,
					}},
				}}},
			}},
			"400": {Description: "查询参数无效"}, "401": {Description: "服务凭据无效"},
			"403": {Description: "项目不可访问"}, "404": {Description: "未刷新、父资产不可见或快照失效"},
			"503": {Description: "资产存储不可用"},
		},
	}
	accountOp := func(id, status, description string, mutation bool) openapi.Op {
		auth := readAuth
		responses := map[string]openapi.Response{
			status: {Description: description}, "401": {Description: "服务凭据无效"},
			"403": {Description: "账号或项目不可访问"}, "503": {Description: "存储不可用"},
		}
		if mutation {
			auth = writeAuth
			responses["400"] = openapi.Response{Description: "无效参数"}
			responses["409"] = openapi.Response{Description: "账号或状态冲突"}
			responses["413"] = openapi.Response{Description: "正文过大"}
			responses["415"] = openapi.Response{Description: "媒体类型错误"}
		}
		return openapi.Op{OperationID: id, Security: auth, Responses: responses}
	}
	bucketSetting := func(id, description string) openapi.Op {
		op := accountOp(id, "200", description, false)
		op.Params = []openapi.Param{{Name: "account", In: "query", Required: true, Schema: openapi.String}}
		op.Responses["200"] = openapi.Response{Description: description,
			Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
				"data": {Type: "object", Properties: map[string]openapi.Schema{"state": openapi.String, "observed_at": {Type: "string", Format: "date-time"}}},
			}}}}}
		op.Responses["400"] = openapi.Response{Description: "对象桶标识或账号参数无效"}
		op.Responses["404"] = openapi.Response{Description: "对象桶不可见或没有有效观测"}
		return op
	}
	patchAccount := accountOp("changeCloudAccount", "200", "变更后的账号", true)
	patchAccount.Body = &openapi.RequestBody{
		Required:    true,
		Description: "账号操作包括 rotate、suspend、resume、share、unshare、transfer。已完成身份核验的通用云账号支持 enable-write 和 disable-write。Redfish 使用独立的 enable-bare-metal-power 和 disable-bare-metal-power。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object", Properties: map[string]openapi.Schema{
				"action": openapi.String, "credential_ref": openapi.String, "target_project": openapi.String,
			},
		}}},
	}
	patchAccount.Responses["409"] = openapi.Response{Description: "当前账号状态或厂商不允许该动作"}
	patchAccount.Responses["412"] = openapi.Response{Description: "需要当前账号 ETag"}
	patchAccount.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	ownership := accountOp("transferAssetOwnership", "200", "资源的新项目归属", true)
	ownership.Responses["412"] = openapi.Response{Description: "需要当前资源归属版本"}
	ownership.Params = patchAccount.Params
	submit := accountOp("submitInstanceOperation", "202", "已持久受理的实例操作", true)
	submit.Responses["412"] = openapi.Response{Description: "需要当前资源版本"}
	submit.Responses["429"] = openapi.Response{Description: "项目待执行操作配额已满"}
	submit.Params = []openapi.Param{
		{Name: "If-Match", In: "header", Required: true, Schema: openapi.String},
		{Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String},
	}
	submit.Body = &openapi.RequestBody{Required: true, Description: "支持 start、stop、reboot、delete、attach-volume 和 detach-volume。卷动作需要 volume_id 和 volume_revision。AWS 挂载需要 device。卸载需要 guest_unmounted=true，删除和卸载均要求独立审批。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "action": openapi.String,
				"volume_id": openapi.String, "volume_revision": openapi.Integer, "device": openapi.String, "guest_unmounted": {Type: "boolean"}}}}}}
	retire := accountOp("retireTencentInstance", "202", "已登记腾讯云 CVM 退役，释放实例及其系统盘，须独立审批", true)
	retire.Params = submit.Params
	retire.Responses["412"] = openapi.Response{Description: "需要当前实例归属版本和盘点系统盘"}
	retire.Responses["429"] = submit.Responses["429"]
	retire.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、asset_id、system_disk_id 和 confirm_asset_id。确认值须与实例 ID 完全一致。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String,
			"system_disk_id": openapi.String, "confirm_asset_id": openapi.String,
		}}}}}
	create := accountOp("createInstance", "202", "创建意图和配额预留已持久受理", true)
	create.Responses["429"] = openapi.Response{Description: "活动操作或已管理实例配额已满"}
	create.Params = []openapi.Param{{Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String}}
	createFields := map[string]openapi.Schema{}
	for _, field := range []string{"account", "region", "name", "image_id", "size", "network_id", "security_group_id"} {
		createFields[field] = openapi.String
	}
	create.Body = &openapi.RequestBody{Required: true, Description: "在已启用写入的 AWS、OpenStack、腾讯云、阿里云或华为云账号中创建一个实例。network_id 使用 AWS、腾讯云或华为云的子网 ID、阿里云的交换机 ID，或 OpenStack 的 Neutron 网络 UUID。size 使用对应厂商的规格标识。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: createFields}}}}
	review := accountOp("resolveInstanceOperation", "200", "已记录独立审批人的处置结果", true)
	review.Body = &openapi.RequestBody{Required: true, Description: "提交 decision、evidence_ref 及适用的 provider_request_id",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"decision": openapi.String, "evidence_ref": openapi.String, "provider_request_id": openapi.String,
		}}}}}
	retryObservation := accountOp("retryInstanceCreationObservation", "200", "已恢复对创建操作的只读观测", true)
	retryObservation.Responses["412"] = openapi.Response{Description: "续查授权在处理期间发生变化"}
	retryObservation.Body = &openapi.RequestBody{Required: true, Description: "独立审批人提交 evidence_ref。阿里云公网 DNS 创建丢失回执时另需 provider_resource_id。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{"evidence_ref": openapi.String, "provider_resource_id": openapi.String}}}}}
	approval := accountOp("decideInstanceApproval", "200", "已记录发送前审批决定", true)
	approval.Responses["412"] = openapi.Response{Description: "申请所依据的资源或授权版本已失效"}
	approval.Responses["409"] = openapi.Response{Description: "审批决定冲突、部署未核对或已过期、实例受删除保护或外部工单批准无效"}
	approval.Body = &openapi.RequestBody{Required: true, Description: "提交 approve 或 reject。批准已关联部署的开机操作时须提交已核对的 deployment_id。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"decision": openapi.String, "deployment_id": openapi.String,
		}}}}}
	preview := accountOp("previewInstanceDeletion", "200", "实例本地删除影响预览，含归属版本、保护状态、已知卷关联数、审批要求与厂商支持状态", false)
	preview.Responses["400"] = openapi.Response{Description: "无效的实例坐标"}
	for _, name := range []string{"account", "region", "asset_id"} {
		preview.Params = append(preview.Params, openapi.Param{Name: name, In: "query", Required: true, Schema: openapi.String})
	}
	retirementPreview := accountOp("previewTencentInstanceRetirement", "200", "当前关机实例的归属版本和待云端核验的盘点磁盘 ID", true)
	retirementPreview.Params = preview.Params
	events := accountOp("listInstanceOperationEvents", "200", "按序记录的项目操作事件及续读游标", false)
	events.Responses["400"] = openapi.Response{Description: "事件游标或页大小无效"}
	events.Params = []openapi.Param{{Name: "after", In: "query", Schema: openapi.String}, {Name: "limit", In: "query", Schema: openapi.Integer}}
	network := accountOp("createNetworkResource", "202", "已持久受理的网络资源创建意图，包括弹性公网 IP 申请", true)
	network.Responses["412"] = openapi.Response{Description: "父网络或父安全组需要当前归属版本"}
	network.Responses["429"] = submit.Responses["429"]
	network.Params = []openapi.Param{create.Params[0], {Name: "If-Match", In: "header", Schema: openapi.String}}
	rule := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"direction": openapi.String, "protocol": openapi.String, "from_port": openapi.Integer, "to_port": openapi.Integer, "remote_cidr": openapi.String,
	}}
	network.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、kind。网络、子网和组创建需要 name。AWS、腾讯云、阿里云和华为云使用 cidr 创建 IPv4 VPC。子网需要 parent_id、cidr、If-Match。腾讯云和阿里云子网还需要 zone，华为云子网可省略 zone。AWS 和阿里云安全组需要父 VPC 的 parent_id 与 If-Match。security-group-rule 需要 rule、父安全组 parent_id 和 If-Match。elastic-ip 需要 name、bandwidth_mbps，华为云还需要 address_pool。规则创建和弹性公网 IP 申请始终独立审批。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"account": openapi.String, "region": openapi.String, "kind": openapi.String,
				"name": openapi.String, "parent_id": openapi.String, "cidr": openapi.String, "zone": openapi.String, "rule": rule,
				"bandwidth_mbps": openapi.Integer, "address_pool": openapi.String}}}}}
	snapshot := accountOp("createVolumeSnapshot", "202", "已持久受理的腾讯云、阿里云或华为云快照申请，需独立审批", true)
	snapshot.Params = submit.Params
	snapshot.Responses["412"] = openapi.Response{Description: "缺少当前源卷归属版本，或源卷归属版本已变更"}
	snapshot.Responses["429"] = submit.Responses["429"]
	snapshot.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、volume_id、name。If-Match 使用源卷归属版本。allow_attached 默认为 false。设为 true 表示允许在云盘挂载时创建崩溃一致性快照。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"account": openapi.String, "region": openapi.String, "volume_id": openapi.String,
				"name": openapi.String, "allow_attached": {Type: "boolean"}}}}}}
	deleteSnapshot := accountOp("deleteVolumeSnapshot", "202", "已持久受理的云盘快照删除申请，需独立审批", true)
	deleteSnapshot.Params = submit.Params
	deleteSnapshot.Responses["412"], deleteSnapshot.Responses["429"] = snapshot.Responses["412"], submit.Responses["429"]
	deleteSnapshot.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、asset_id、volume_id。If-Match 使用源卷归属版本。受理后需独立审批及厂商缺失证据。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "volume_id": openapi.String,
		}}}}}
	restore := accountOp("restoreVolumeFromSnapshot", "202", "已持久受理的新建数据盘恢复申请，需独立审批", true)
	restore.Params = submit.Params
	restore.Responses["412"], restore.Responses["429"] = snapshot.Responses["412"], submit.Responses["429"]
	restore.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、asset_id（快照）、volume_id（源卷）、name、zone、disk_type 和 size_gib。If-Match 使用源卷归属版本。encrypted 须与源快照加密状态匹配。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "volume_id": openapi.String,
			"name": openapi.String, "zone": openapi.String, "disk_type": openapi.String, "size_gib": openapi.Integer, "encrypted": {Type: "boolean"},
		}}}}}
	rollback := accountOp("rollbackVolumeToSnapshot", "202", "已持久受理的原卷回滚申请，需独立审批", true)
	rollback.Params = submit.Params
	rollback.Responses["412"], rollback.Responses["429"] = snapshot.Responses["412"], submit.Responses["429"]
	rollback.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、asset_id（源快照）、volume_id（原卷）、confirm_overwrite=true。If-Match 使用源卷归属版本。回滚会覆盖原卷上快照之后的写入。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "volume_id": openapi.String, "confirm_overwrite": {Type: "boolean"},
		}}}}}
	imageCreation := accountOp("createSystemImage", "202", "已持久受理的系统盘镜像申请，需独立审批", true)
	imageCreation.Params = submit.Params
	imageCreation.Responses["412"] = openapi.Response{Description: "需要当前源实例归属版本"}
	imageCreation.Responses["429"] = submit.Responses["429"]
	imageCreation.Body = &openapi.RequestBody{Required: true, Description: "提交 account、region、instance_id、name。If-Match 使用源实例归属版本。源实例必须已停止，阿里云源实例须只挂载系统云盘。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "instance_id": openapi.String, "name": openapi.String,
		}}}}}
	imageCopy := accountOp("copyPrivateImage", "202", "已持久受理的跨地域镜像复制申请，需独立审批", true)
	imageCopy.Params = submit.Params
	imageCopy.Responses["412"] = openapi.Response{Description: "需要当前源镜像归属版本"}
	imageCopy.Responses["429"] = submit.Responses["429"]
	imageCopy.Body = &openapi.RequestBody{Required: true, Description: "提交源 account、region、asset_id 和目标 destination_region、name、destination_account。腾讯云与阿里云可省略 destination_account。华为云须指定同企业账号下、属于当前平台项目的另一 IAM 项目账号。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "name": openapi.String,
			"destination_account": openapi.String, "destination_region": openapi.String,
		}}}}}
	imageDeletion := accountOp("deleteOwnedImage", "202", "已持久受理的自有镜像删除申请，需独立审批", true)
	imageDeletion.Params = submit.Params
	imageDeletion.Responses["412"] = openapi.Response{Description: "需要最新的镜像归属版本和有效的账号盘点"}
	imageDeletion.Responses["429"] = submit.Responses["429"]
	imageDeletion.Body = &openapi.RequestBody{Required: true, Description: "提交 account、region、asset_id。If-Match 使用镜像归属版本。使用镜像的实例、待执行申请或已启用目录模板会阻止删除。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String,
		}}}}}
	deleteNetwork := accountOp("deleteNetworkResource", "202", "已持久受理的 VPC、子网或安全组删除申请，需独立审批", true)
	deleteNetwork.Params = submit.Params
	deleteNetwork.Responses["412"] = openapi.Response{Description: "需要当前资产及其父资源的有效归属与盘点"}
	deleteNetwork.Responses["429"] = submit.Responses["429"]
	dnsRecordCreation := accountOp("createDNSRecord", "202", "已持久受理的公网 DNS 记录创建申请，需独立审批", true)
	dnsRecordCreation.Params = submit.Params
	dnsRecordCreation.Responses["412"] = openapi.Response{Description: "需要当前托管区归属版本和记录快照"}
	dnsRecordCreation.Responses["429"] = submit.Responses["429"]
	dnsRecordCreation.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、asset_id、observed_at、name、type、value、ttl。name 是托管区内的完整名称，type 为 A 或 AAAA，If-Match 使用托管区归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "asset_id": openapi.String, "observed_at": {Type: "string", Format: "date-time"},
			"name": openapi.String, "type": openapi.String, "value": openapi.String, "ttl": openapi.Integer,
		}}}}}
	dnsRecordDeletion := accountOp("deleteDNSRecord", "202", "已持久受理的停用公网 DNS 记录删除申请，需独立审批", true)
	dnsRecordDeletion.Params = submit.Params
	dnsRecordDeletion.Responses["412"] = openapi.Response{Description: "需要当前托管区归属版本和记录快照"}
	dnsRecordDeletion.Responses["429"] = submit.Responses["429"]
	dnsRecordDeletion.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、asset_id、record_id、observed_at。目标必须是已停用的 A 或 AAAA 记录，If-Match 使用托管区归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object",
			Properties: map[string]openapi.Schema{
				"account": openapi.String, "asset_id": openapi.String, "record_id": openapi.String, "observed_at": {Type: "string", Format: "date-time"},
			},
		}}}}
	workloadScale := accountOp("scaleKubernetesWorkload", "202", "已持久受理的工作负载副本调整申请，需独立审批", true)
	workloadScale.Params = submit.Params
	workloadScale.Responses["412"] = openapi.Response{Description: "需要当前父集群归属版本与工作负载快照"}
	workloadScale.Responses["429"] = submit.Responses["429"]
	workloadScale.Body = &openapi.RequestBody{Required: true,
		Description: "提供账号、地域、集群 ID、快照 ID、工作负载 UID、命名空间、类型、名称和目标副本数。类型为 Deployment 或 StatefulSet，副本数范围为 1 至 100。If-Match 使用父集群归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "snapshot": {Type: "string", Format: "uuid"},
			"uid": openapi.String, "namespace": openapi.String, "kind": openapi.String, "name": openapi.String, "replicas": openapi.Integer,
		}}}}}

	dnsRecordStatus := accountOp("setDNSRecordStatus", "202", "已持久受理的公网 DNS 启停解析申请，需独立审批", true)
	dnsRecordStatus.Params = submit.Params
	dnsRecordStatus.Responses["412"] = openapi.Response{Description: "需要当前托管区归属版本和记录快照"}
	dnsRecordStatus.Responses["429"] = submit.Responses["429"]
	dnsRecordStatus.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、asset_id、record_id、observed_at、enabled。If-Match 使用托管区归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object",
			Properties: map[string]openapi.Schema{
				"account": openapi.String, "asset_id": openapi.String, "record_id": openapi.String,
				"observed_at": {Type: "string", Format: "date-time"}, "enabled": {Type: "boolean"},
			},
		}}}}
	databaseRename := accountOp("renameDatabaseInstance", "202", "已持久受理的托管数据库实例改名申请，需独立审批", true)
	databaseRename.Params = submit.Params
	databaseRename.Responses["412"] = openapi.Response{Description: "需要当前数据库名称、归属版本和有效的盘点"}
	databaseRename.Responses["429"] = submit.Responses["429"]
	databaseRename.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、asset_id、current_name 和 name。更新实例显示名。If-Match 使用数据库实例归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object",
			Properties: map[string]openapi.Schema{
				"account": openapi.String, "region": openapi.String, "asset_id": openapi.String,
				"current_name": openapi.String, "name": openapi.String,
			},
		}}}}
	bareMetalPower := accountOp("requestBareMetalPower", "202", "已持久受理的裸金属电源操作，需独立审批", true)
	bareMetalPower.Params = submit.Params
	bareMetalPower.Responses["412"] = openapi.Response{Description: "需要当前资产归属版本和有效盘点"}
	bareMetalPower.Responses["429"] = submit.Responses["429"]
	bareMetalPower.Body = serviceCatalogBody([]string{"account", "region", "asset_id", "action"}, false)
	bareMetalPower.Body.Description = "裸金属系统 UUID、站点和账号，以及 power-on-bare-metal 或 shutdown-bare-metal。"
	bareMetalPXE := accountOp("requestBareMetalPXE", "202", "已持久受理的裸金属一次性 PXE 配置申请，需独立审批", true)
	bareMetalPXE.Params = submit.Params
	bareMetalPXE.Responses["412"] = bareMetalPower.Responses["412"]
	bareMetalPXE.Responses["429"] = submit.Responses["429"]
	bareMetalPXE.Body = serviceCatalogBody([]string{"account", "region", "asset_id", "action"}, false)
	bareMetalPXE.Body.Description = "裸金属系统 UUID、站点和账号，以及 stage-bare-metal-pxe 或 cancel-bare-metal-pxe。"
	cacheRename := accountOp("renameCacheInstance", "202", "已持久受理的托管缓存实例改名申请，需独立审批", true)
	cacheRename.Params = submit.Params
	cacheRename.Responses["412"] = openapi.Response{Description: "需要当前缓存名称、归属版本和有效的盘点"}
	cacheRename.Responses["429"] = submit.Responses["429"]
	cacheRename.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、asset_id、current_name 和 name。新名称为 4–60 位，以英文字母开头，可包含字母、数字、下划线和连字符。If-Match 使用归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{
			Type: "object",
			Properties: map[string]openapi.Schema{
				"account": openapi.String, "region": openapi.String, "asset_id": openapi.String,
				"current_name": openapi.String, "name": openapi.String,
			},
		}}}}
	balancerRename := accountOp("renameLoadBalancer", "202", "已持久受理的负载均衡改名申请，需独立审批", true)
	balancerRename.Params = submit.Params
	balancerRename.Responses["412"] = openapi.Response{Description: "需要当前资源名称、归属版本和有效的盘点"}
	balancerRename.Responses["429"] = submit.Responses["429"]
	balancerRename.Body = &openapi.RequestBody{Required: true,
		Description: "提交 account、region、asset_id、current_name 和 name。If-Match 使用负载均衡归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String,
			"name": openapi.String, "current_name": openapi.String,
		}}}}}
	listenerRename := accountOp("renameLoadBalancerListener", "202", "已持久受理的负载均衡监听器改名申请，需独立审批", true)
	listenerRename.Params = submit.Params
	listenerRename.Responses["412"] = openapi.Response{Description: "需要监听器当前名称、父实例归属版本和当前配置快照"}
	listenerRename.Responses["429"] = submit.Responses["429"]
	listenerRename.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、asset_id、listener_id、current_name 和 name。asset_id 和 If-Match 使用父负载均衡的 ID 和归属版本。仅更新监听器名称。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "listener_id": openapi.String,
			"name": openapi.String, "current_name": openapi.String,
		}}}}}
	targetGroupRename := accountOp("renameLoadBalancerTargetGroup", "202", "已持久受理的负载均衡后端组改名申请，需独立审批", true)
	targetGroupRename.Params = submit.Params
	targetGroupRename.Responses["412"] = openapi.Response{Description: "需要后端组当前名称、父实例归属版本和独占关联快照"}
	targetGroupRename.Responses["429"] = submit.Responses["429"]
	targetGroupRename.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、asset_id、target_group_id、current_name 和 name。If-Match 使用父负载均衡归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "target_group_id": openapi.String, "name": openapi.String, "current_name": openapi.String,
		}}}}}
	backendWeight := accountOp("setLoadBalancerBackendWeight", "202", "已持久受理的单个后端成员权重变更，需独立审批", true)
	backendWeight.Params = submit.Params
	backendWeight.Responses["412"] = openapi.Response{Description: "需要当前权重、独占后端组、成员标识及父实例归属版本"}
	backendWeight.Responses["429"] = submit.Responses["429"]
	backendWeight.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、asset_id、target_group_id、member_key、current_weight 和 weight。权重为 0 至 100 的整数，If-Match 使用父实例归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "target_group_id": openapi.String, "member_key": openapi.String,
			"current_weight": openapi.Integer, "weight": openapi.Integer,
		}}}}}
	backendDetach := accountOp("detachLoadBalancerBackend", "202", "已持久受理的单后端成员解绑申请，需独立审批", true)
	backendDetach.Params = submit.Params
	backendDetach.Responses["412"] = openapi.Response{Description: "需要零权重成员、独占后端组和父实例当前归属版本"}
	backendDetach.Responses["429"] = submit.Responses["429"]
	backendDetach.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、asset_id、target_group_id、member_key 和 confirm_detach=true。If-Match 使用父负载均衡归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "target_group_id": openapi.String, "member_key": openapi.String,
			"confirm_detach": {Type: "boolean"},
		}}}}}
	backendAttach := accountOp("attachLoadBalancerBackend", "202", "已持久受理的单后端实例绑定申请，需独立审批", true)
	backendAttach.Params = submit.Params
	backendAttach.Responses["412"] = openapi.Response{Description: "需要当前实例及负载均衡归属版本和独占后端组快照"}
	backendAttach.Responses["429"] = submit.Responses["429"]
	backendAttach.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、asset_id、target_group_id、instance_id、instance_revision 和 port。新成员权重固定为 0。If-Match 使用父负载均衡归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "target_group_id": openapi.String,
			"instance_id": openapi.String, "instance_revision": openapi.Integer, "port": openapi.Integer,
		}}}}}
	deleteNetwork.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、asset_id 和 kind。kind 为 network、subnet 或 security-group。安全组必须传 confirm_rule_removal=true。If-Match 使用目标资产归属版本。存在外部依赖时拒绝删除。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"account": openapi.String, "region": openapi.String,
				"asset_id": openapi.String, "kind": openapi.String, "confirm_rule_removal": {Type: "boolean"}}}}}}
	revokeRule := accountOp("revokeSecurityGroupRule", "202", "已持久受理的安全组规则撤销操作，需独立审批", true)
	revokeRule.Responses["412"], revokeRule.Responses["429"] = network.Responses["412"], submit.Responses["429"]
	revokeRule.Params = submit.Params
	revokeRule.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、parent_id、rule_id。If-Match 使用父安全组当前归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "parent_id": openapi.String, "rule_id": openapi.String,
		}}}}}
	releaseIP := accountOp("releaseElasticIP", "202", "已持久受理的弹性公网 IP 释放操作，需独立审批", true)
	releaseIP.Responses["412"] = openapi.Response{Description: "缺少有效的弹性公网 IP 归属版本或版本已变更"}
	releaseIP.Responses["429"] = submit.Responses["429"]
	releaseIP.Params = submit.Params
	releaseIP.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、asset_id、public_address。仅释放未绑定的按量 IPv4 地址。If-Match 使用该地址当前归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "public_address": {Type: "string", Format: "ipv4"},
		}}}}}
	bindIP := accountOp("bindElasticIP", "202", "已持久受理的弹性公网 IP 绑定操作，需独立审批", true)
	bindIP.Responses["412"] = openapi.Response{Description: "弹性公网 IP 或目标实例的归属版本已变更"}
	bindIP.Responses["429"] = submit.Responses["429"]
	bindIP.Params = submit.Params
	bindIP.Body = &openapi.RequestBody{Required: true,
		Description: "必填 account、region、asset_id、public_address、instance_id 和 instance_revision。If-Match 使用地址归属版本。华为云须指定 port_id。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "public_address": {Type: "string", Format: "ipv4"},
			"instance_id": openapi.String, "instance_revision": openapi.Integer, "port_id": {Type: "string", Format: "uuid"},
		}}}}}
	unbindIP := accountOp("unbindElasticIP", "202", "已持久受理的弹性公网 IP 解绑操作，需独立审批", true)
	unbindIP.Responses["412"], unbindIP.Responses["429"] = bindIP.Responses["412"], bindIP.Responses["429"]
	unbindIP.Params, unbindIP.Body = bindIP.Params, bindIP.Body
	networkApproval, networkReview, networkEvents, networkRetry := approval, review, events, retryObservation
	networkApproval.OperationID = "decideNetworkApproval"
	networkReview.OperationID = "resolveNetworkOperation"
	networkEvents.OperationID = "listNetworkOperationEvents"
	networkRetry.OperationID = "retryNetworkCreationObservation"
	projectOperations := accountOp("listProjectOperations", "200", "按创建时间倒序返回项目操作及下一页游标", false)
	projectOperations.Responses["400"] = openapi.Response{Description: "筛选项、页大小或游标无效"}
	for _, name := range []string{"account", "region", "kind", "action", "status", "cursor"} {
		projectOperations.Params = append(projectOperations.Params, openapi.Param{Name: name, In: "query", Schema: openapi.String})
	}
	projectOperations.Params = append(projectOperations.Params, openapi.Param{Name: "limit", In: "query", Schema: openapi.Integer})
	projectEvents := events
	projectEvents.OperationID = "listProjectOperationEvents"
	projectApproval, projectReview, projectRetry := approval, review, retryObservation
	projectApproval.OperationID = "decideProjectOperationApproval"
	projectReview.OperationID = "resolveProjectOperation"
	projectRetry.OperationID = "retryProjectCreationObservation"
	batchItem := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"account": openapi.String, "region": openapi.String, "asset_id": openapi.String, "action": openapi.String,
		"ownership_revision": openapi.Integer, "volume_id": openapi.String, "volume_revision": openapi.Integer,
		"device": openapi.String, "guest_unmounted": {Type: "boolean"},
	}}
	batchBody := &openapi.RequestBody{Required: true, Description: "items 含 1 至 25 个实例动作，每项提供账号、区域、实例 ID、动作和当前归属版本。挂载和卸载还需匹配卷版本。每个实例和每个卷在一批中至多出现一次。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"items": {Type: "array", Items: &batchItem}}}}}}
	batchPreview := accountOp("previewOperationBatch", "200", "项目快照下的逐项准入与审批状态", true)
	batchPreview.Body = batchBody
	delete(batchPreview.Responses, "409")
	batchSubmit := accountOp("submitOperationBatch", "202", "同一事务受理的批次和按提交顺序返回的操作", true)
	batchSubmit.Body = batchBody
	batchSubmit.Params = []openapi.Param{{Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String}}
	batchSubmit.Responses["412"] = openapi.Response{Description: "至少一项实例或卷归属版本已变更"}
	batchSubmit.Responses["429"] = openapi.Response{Description: "项目剩余活动操作配额不足以容纳整个批次"}
	auditList := accountOp("listProjectAudit", "200", "按时间倒序返回的项目管理员审计记录", false)
	auditList.Responses["400"] = openapi.Response{Description: "筛选条件或续读游标无效"}
	for _, name := range []string{"category", "account", "subject_id", "from", "until"} {
		auditList.Params = append(auditList.Params, openapi.Param{Name: name, In: "query", Schema: openapi.String})
	}
	auditExport := accountOp("exportProjectAudit", "200", "同一快照下导出的项目审计 CSV", false)
	auditExport.Params = append(auditExport.Params, auditList.Params...)
	auditExport.Responses["200"] = openapi.Response{Description: "审计 CSV", Content: map[string]openapi.MediaType{"text/csv": {Schema: openapi.String}}}
	auditExport.Responses["400"] = openapi.Response{Description: "筛选条件无效"}
	auditExport.Responses["422"] = openapi.Response{Description: "匹配范围超过 2000 条"}
	auditList.Params = append(auditList.Params, openapi.Param{Name: "cursor", In: "query", Schema: openapi.String},
		openapi.Param{Name: "limit", In: "query", Schema: openapi.Integer})
	hints := map[string]openapi.Op{
		"GET " + assetsRoute:                                               list,
		"GET " + assetsRoute + "/changes":                                  history,
		"GET " + assetsRoute + "/export":                                   export,
		"PATCH " + assetsRoute + "/ownership":                              ownership,
		"GET " + assetsRoute + "/topology":                                 topology,
		"GET " + dnsRecordsRoute:                                           dnsRecords,
		"GET " + bucketVersioningRoute:                                     bucketSetting("readProjectBucketVersioning", "版本控制状态为 disabled、enabled 或 suspended。"),
		"GET " + bucketEncryptionRoute:                                     bucketSetting("readProjectBucketEncryption", "默认服务端加密方式为 unconfigured、aes256、sm4 或 kms。"),
		"GET " + clusterNodePoolsRoute:                                     clusterNodePoolsOpenAPI(readAuth),
		"GET " + clusterWorkloadsRoute:                                     clusterWorkloadsOpenAPI(readAuth),
		"GET " + loadBalancerConfigurationRoute:                            lbConfiguration,
		"POST " + loadBalancerBackendAttachRoute:                           backendAttach,
		"POST " + loadBalancerBackendDetachRoute:                           backendDetach,
		"POST " + loadBalancerBackendWeightRoute:                           backendWeight,
		"POST " + loadBalancerTargetGroupRenameRoute:                       targetGroupRename,
		"POST " + loadBalancerListenerRenameRoute:                          listenerRename,
		"POST " + dnsRecordCreationRoute:                                   dnsRecordCreation,
		"POST " + dnsRecordDeletionRoute:                                   dnsRecordDeletion,
		"POST " + workloadScaleRoute:                                       workloadScale,
		"POST " + dnsRecordStatusRoute:                                     dnsRecordStatus,
		"POST " + bareMetalPXERoute:                                        bareMetalPXE,
		"POST " + bareMetalPowerRoute:                                      bareMetalPower,
		"POST " + cacheRenameRoute:                                         cacheRename,
		"POST " + databaseRenameRoute:                                      databaseRename,
		"POST " + loadBalancerRenameRoute:                                  balancerRename,
		"POST " + accountsRoute:                                            accountOp("registerCloudAccount", "201", "待校验账号", true),
		"GET " + accountsRoute + "/:account":                               accountOp("getCloudAccount", "200", "账号元数据", false),
		"PATCH " + accountsRoute + "/:account":                             patchAccount,
		"POST " + instancesRoute:                                           create,
		"POST " + operationsRoute + "/retire":                              retire,
		"GET " + operationsRoute + "/retirement-preview":                   retirementPreview,
		"GET " + operationsRoute + "/deletion-preview":                     preview,
		"POST " + operationsRoute:                                          submit,
		"GET " + operationsRoute + "/:operation":                           accountOp("getInstanceOperation", "200", "项目操作状态", false),
		"POST " + operationsRoute + "/:operation/approval":                 approval,
		"POST " + operationsRoute + "/:operation/resolution":               review,
		"GET " + operationsRoute + "/:operation/events":                    events,
		"POST " + operationsRoute + "/:operation/retry-observation":        retryObservation,
		"POST " + imagesRoute:                                              imageCreation,
		"POST " + imagesRoute + "/copy":                                    imageCopy,
		"POST " + imagesRoute + "/delete":                                  imageDeletion,
		"POST " + snapshotsRoute + "/rollback":                             rollback,
		"POST " + snapshotsRoute + "/restore":                              restore,
		"POST " + snapshotsRoute + "/delete":                               deleteSnapshot,
		"POST " + snapshotsRoute:                                           snapshot,
		"POST " + networkOperationsRoute:                                   network,
		"POST " + networkOperationsRoute + "/delete":                       deleteNetwork,
		"POST " + networkOperationsRoute + "/revoke-rule":                  revokeRule,
		"POST " + networkOperationsRoute + "/release-elastic-ip":           releaseIP,
		"POST " + networkOperationsRoute + "/bind-elastic-ip":              bindIP,
		"POST " + networkOperationsRoute + "/unbind-elastic-ip":            unbindIP,
		"GET " + networkOperationsRoute + "/:operation":                    accountOp("getNetworkOperation", "200", "项目网络操作状态", false),
		"POST " + networkOperationsRoute + "/:operation/approval":          networkApproval,
		"POST " + networkOperationsRoute + "/:operation/resolution":        networkReview,
		"GET " + networkOperationsRoute + "/:operation/events":             networkEvents,
		"POST " + networkOperationsRoute + "/:operation/retry-observation": networkRetry,
		"GET " + projectOperationsRoute:                                    projectOperations,
		"GET " + projectOperationsRoute + "/:operation":                    accountOp("getProjectOperation", "200", "项目操作状态，安全组规则附带 network_intent，裸金属开机可附带 bare_metal_deployment", false),
		"GET " + projectOperationsRoute + "/:operation/events":             projectEvents,
		"POST " + projectOperationsRoute + "/:operation/approval":          projectApproval,
		"POST " + projectOperationsRoute + "/:operation/resolution":        projectReview,
		"POST " + projectOperationsRoute + "/:operation/retry-observation": projectRetry,
		"POST " + operationBatchesRoute + "/preview":                       batchPreview,
		"POST " + operationBatchesRoute:                                    batchSubmit,
		"GET " + operationBatchesRoute + "/:batch":                         accountOp("getOperationBatch", "200", "项目批次及当前子操作状态", false),
		"GET " + allocationRoute:                                           allocationOpenAPI(readAuth),
		"GET " + billingRoute:                                              billingOpenAPI(readAuth),
		"GET " + auditRoute:                                                auditList,
		"GET " + auditRoute + "/export":                                    auditExport,
		"GET /healthz":                                                     {Responses: map[string]openapi.Response{"200": {Description: "进程存活"}}},
		"GET /readyz": {Responses: map[string]openapi.Response{
			"200": {Description: "服务就绪"}, "503": {Description: "数据库或迁移不可用"},
		}},
		"GET /openapi.json": {Responses: map[string]openapi.Response{"200": {Description: "OpenAPI 文档"}}},
	}
	if consoleEnabled {
		consoleOpenAPI(hints)
	}
	if consoleEnabled && (console.nativeConnect != nil || console.nativeSerial != nil) {
		nativeOpenAPI(hints, accountOp, *console)
	}
	budgetOpenAPI(hints, readAuth, writeAuth)
	tagPolicyOpenAPI(hints, readAuth, writeAuth)
	changeTicketOpenAPI(hints, readAuth, writeAuth)
	hints["GET "+cmdbSnapshotRoute] = cmdbSnapshotOpenAPI()
	hints["GET "+idleInstancesRoute] = idleReportOpenAPI(readAuth)
	hints["GET "+inventoryFreshnessRoute] = inventoryFreshnessOpenAPI(readAuth)
	hints["GET "+bucketProtectionRoute] = bucketProtectionOpenAPI(readAuth)
	catalogOpenAPI(hints, accountOp)
	alertOpenAPI(hints, accountOp)
	accountInventoryOpenAPI(hints, accountOp)
	inventoryScheduleOpenAPI(hints, accountOp)
	accountRolloutOpenAPI(hints, accountOp)
	bareMetalDeploymentHints(hints, accountOp)
	bareMetalReclaimHints(hints, accountOp)
	kvmHints(hints, accountOp)
	doc, err := generator.GenerateStrict(app.Routes(), hints)
	if err != nil {
		return err
	}
	doc.Info = openapi.Info{Title: "Cloud Manager API", Version: "1.0.0"}
	encoded, err = openapi.MarshalJSON(doc)
	return err
}

func nativeOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op, console consoleConfig) {
	list := operation("listConsoleGrants", "200", "项目原生控制台访问申请", false)
	list.Params = []openapi.Param{{Name: "after", In: "query", Schema: openapi.String}, {Name: "limit", In: "query", Schema: openapi.Integer}}
	list.Responses["400"] = openapi.Response{Description: "分页参数无效"}
	get := operation("getConsoleGrant", "200", "访问申请和当前修订号 ETag", false)
	get.Responses["400"] = openapi.Response{Description: "申请 ID 无效"}
	request := operation("requestConsoleGrant", "201", "已提交的原生控制台访问申请", true)
	match := openapi.Param{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}
	request.Params = []openapi.Param{match, {Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String}}
	request.Body = serviceCatalogBody([]string{"account", "region", "asset_id", "purpose"}, false)
	if console.nativeSerial != nil {
		request.Body.Description += "。AWS 实例需要 ssh_public_key，并允许使用具有项目执行权限的服务凭据"
		request.Body.Content["application/json"].Schema.Properties["ssh_public_key"] = openapi.String
	} else {
		request.Security = []openapi.SecurityRequirement{{"consoleSession": {}, "consoleCSRF": {}}}
	}
	decide := operation("decideConsoleGrant", "200", "独立审批决定与更新后的申请", true)
	decide.Params = []openapi.Param{match}
	decide.Body = serviceCatalogBody([]string{"decision"}, false)
	revoke := operation("revokeConsoleGrant", "200", "已撤销的访问申请", true)
	revoke.Params = []openapi.Param{match}
	for _, endpoint := range []*openapi.Op{&request, &decide, &revoke} {
		endpoint.Responses["412"] = openapi.Response{Description: "当前资源或授权版本已失效"}
	}
	delete(revoke.Responses, "413")
	delete(revoke.Responses, "415")
	hints["GET "+nativeRoute] = list
	hints["POST "+nativeRoute] = request
	hints["GET "+nativeRoute+"/:grant"] = get
	hints["POST "+nativeRoute+"/:grant/approval"] = decide
	hints["POST "+nativeRoute+"/:grant/revoke"] = revoke
	if console.nativeConnect != nil {
		stream := operation("streamNativeInstanceConsole", "101", "同源 WebSocket 中转，单次使用已批准的 noVNC 授权", false)
		stream.Security = []openapi.SecurityRequirement{{"consoleSession": {}}}
		stream.Responses["400"] = openapi.Response{Description: "无效 WebSocket 握手"}
		stream.Responses["412"] = openapi.Response{Description: "访问授权过期、已使用或已撤销"}
		hints["GET "+nativeRoute+"/:grant/stream"] = stream
	}
	if console.nativeSerial != nil {
		issue := operation("activateAWSSerialConsoleKey", "200", "AWS 已接收 SSH 公钥，返回授权和短期 SSH 连接参数", true)
		issue.Params = []openapi.Param{match}
		issue.Body = serviceCatalogBody([]string{"ssh_public_key"}, false)
		issue.Responses["412"] = openapi.Response{Description: "访问授权过期、已使用、已撤销或版本变更"}
		issue.Responses["503"] = openapi.Response{Description: "签发失败或厂商结果不确定，可查询授权状态"}
		delete(issue.Responses, "409")
		hints["POST "+nativeRoute+"/:grant/serial-key"] = issue
	}
}

func alertOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	list := operation("listProjectAlerts", "200", "项目告警及按发生时间倒序的续读位置", false)
	list.Params = []openapi.Param{
		{Name: "status", In: "query", Description: "active、open、acknowledged、resolved 或 all，默认为 active", Schema: openapi.String},
		{Name: "rule", In: "query", Description: "operation_review、operation_delayed、scan_failures、scan_stale 或管理员可用的 billing_budget_warning、billing_budget_exceeded", Schema: openapi.String},
		{Name: "after", In: "query", Schema: openapi.String},
		{Name: "limit", In: "query", Schema: openapi.Integer},
	}
	list.Responses["400"] = openapi.Response{Description: "筛选条件或续读位置无效"}
	get := operation("getProjectAlert", "200", "告警详情以及当前修订号 ETag，预算告警包含账期、来源、币种和成本口径", false)
	get.Responses["400"] = openapi.Response{Description: "告警 ID 无效"}
	ack := operation("acknowledgeProjectAlert", "200", "管理员已确认告警，返回新修订号 ETag", true)
	ack.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	ack.Responses["412"] = openapi.Response{Description: "需要当前待确认告警的修订号"}
	delete(ack.Responses, "409")
	delete(ack.Responses, "413")
	delete(ack.Responses, "415")
	hints["GET "+alertsRoute] = list
	hints["GET "+alertsRoute+"/:alert"] = get
	hints["POST "+alertsRoute+"/:alert/acknowledgement"] = ack
}

func catalogOpenAPI(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	list := operation("listServiceCatalog", "200", "按模板 ID 排序的项目目录页", false)
	list.Params = []openapi.Param{
		{Name: "after", In: "query", Schema: openapi.String},
		{Name: "limit", In: "query", Schema: openapi.Integer},
		{Name: "include_disabled", In: "query", Schema: openapi.Boolean},
	}
	list.Responses["400"] = openapi.Response{Description: "查询参数无效"}
	current := operation("getServiceCatalogTemplate", "200", "当前模板及其修订号 ETag", false)
	history := operation("getServiceCatalogVersion", "200", "管理员读取不可变模板版本", false)
	for _, endpoint := range []openapi.Op{current, history} {
		endpoint.Responses["400"] = openapi.Response{Description: "模板 ID 或版本无效"}
	}
	create := operation("createServiceCatalogTemplate", "201", "已发布首个模板版本，并返回 ETag 与 Location", true)
	publish := operation("publishServiceCatalogVersion", "200", "新版本已发布，并返回当前修订号 ETag", true)
	retire := operation("retireServiceCatalogTemplate", "200", "模板已停用，并返回当前修订号 ETag", true)
	specFields := []string{"title", "account", "region", "image_id", "size", "network_id", "security_group_id"}
	create.Body = serviceCatalogBody(append([]string{"id"}, specFields...), false)
	publish.Body = serviceCatalogBody(specFields, false)
	for _, endpoint := range []*openapi.Op{&publish, &retire} {
		endpoint.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
		endpoint.Responses["412"] = openapi.Response{Description: "需要当前目录修订号"}
		delete(endpoint.Responses, "409")
	}
	delete(retire.Responses, "413")
	delete(retire.Responses, "415")
	launch := operation("launchServiceCatalogInstance", "202", "已持久受理的实例操作及模板来源", true)
	launch.Params = []openapi.Param{{Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String}}
	launch.Body = serviceCatalogBody([]string{"version", "name"}, true)
	launch.Responses["409"] = openapi.Response{Description: "幂等键已用于其他申请"}
	launch.Responses["412"] = openapi.Response{Description: "新申请需要当前启用的模板版本"}
	launch.Responses["429"] = openapi.Response{Description: "活动操作或已管理实例配额已满"}
	hints["GET "+catalogRoute] = list
	hints["POST "+catalogRoute] = create
	hints["GET "+catalogItemRoute] = current
	hints["PUT "+catalogItemRoute] = publish
	hints["DELETE "+catalogItemRoute] = retire
	hints["GET "+catalogItemRoute+"/versions/:version"] = history
	hints["POST "+catalogItemRoute+"/launch"] = launch
}

func serviceCatalogBody(required []string, versioned bool) *openapi.RequestBody {
	fields := make(map[string]openapi.Schema, len(required))
	for _, field := range required {
		fields[field] = openapi.String
	}
	if versioned {
		fields["version"] = openapi.Integer
	}
	return &openapi.RequestBody{Required: true, Description: "必填字段包括 " + strings.Join(required, "、"),
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: fields}}}}
}
