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
			"200": {Description: "项目可见的资产页和后续游标"},
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
	history.Responses["200"] = openapi.Response{Description: "项目可见的资产差异和后续游标"}
	export := list
	export.OperationID = "exportProjectAssets"
	export.Params = export.Params[:4]
	export.Responses = map[string]openapi.Response{}
	for status, response := range list.Responses {
		export.Responses[status] = response
	}
	export.Responses["200"] = openapi.Response{Description: "筛选后的资产 CSV", Content: map[string]openapi.MediaType{"text/csv": {Schema: openapi.String}}}
	export.Responses["422"] = openapi.Response{Description: "匹配资产超过单次导出上限"}
	topology := list
	topology.OperationID = "listProjectAssetTopology"
	topology.Summary = "查询已盘点的资源及其关联，每页最多 50 个源资源"
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
	patchAccount := accountOp("changeCloudAccount", "200", "变更后的账号", true)
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
	create := accountOp("createInstance", "202", "创建意图和配额预留已持久受理", true)
	create.Responses["429"] = openapi.Response{Description: "活动操作或已管理实例配额已满"}
	create.Params = []openapi.Param{{Name: "Idempotency-Key", In: "header", Required: true, Schema: openapi.String}}
	createFields := map[string]openapi.Schema{}
	for _, field := range []string{"account", "region", "name", "image_id", "size", "network_id", "security_group_id"} {
		createFields[field] = openapi.String
	}
	create.Body = &openapi.RequestBody{Required: true, Description: "在 AWS 或 OpenStack 的账号和区域创建一个实例。AWS 的 network_id 是子网 ID，OpenStack 的 network_id 是 Neutron 网络 UUID，size 是 Nova flavor ID。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: createFields}}}}
	review := accountOp("resolveInstanceOperation", "200", "已记录独立审批人的处置结果", true)
	review.Body = &openapi.RequestBody{Required: true, Description: "提交 decision、evidence_ref 及适用的 provider_request_id",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"decision": openapi.String, "evidence_ref": openapi.String, "provider_request_id": openapi.String,
		}}}}}
	retryObservation := accountOp("retryInstanceCreationObservation", "200", "已恢复对创建操作的只读观测", true)
	retryObservation.Responses["412"] = openapi.Response{Description: "续查授权在处理期间发生变化"}
	retryObservation.Body = &openapi.RequestBody{Required: true, Description: "独立审批人提交排查证据引用 evidence_ref。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{"evidence_ref": openapi.String}}}}}
	approval := accountOp("decideInstanceApproval", "200", "已记录发送前审批决定", true)
	approval.Responses["412"] = openapi.Response{Description: "申请所依据的资源或授权版本已失效"}
	approval.Responses["409"] = openapi.Response{Description: "审批决定冲突或实例受删除保护"}
	approval.Body = &openapi.RequestBody{Required: true, Description: "提交 approve 或 reject",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{"decision": openapi.String}}}}}
	preview := accountOp("previewInstanceDeletion", "200", "实例本地删除影响预览", false)
	preview.Responses["400"] = openapi.Response{Description: "无效的实例坐标"}
	for _, name := range []string{"account", "region", "asset_id"} {
		preview.Params = append(preview.Params, openapi.Param{Name: name, In: "query", Required: true, Schema: openapi.String})
	}
	events := accountOp("listInstanceOperationEvents", "200", "按序记录的项目操作事件及续读游标", false)
	events.Responses["400"] = openapi.Response{Description: "事件游标或页大小无效"}
	events.Params = []openapi.Param{{Name: "after", In: "query", Schema: openapi.String}, {Name: "limit", In: "query", Schema: openapi.Integer}}
	network := accountOp("createNetworkResource", "202", "已持久受理的网络、IPv4 子网、安全组或安全组规则创建意图", true)
	network.Responses["412"] = openapi.Response{Description: "父网络或父安全组需要当前归属版本"}
	network.Responses["429"] = submit.Responses["429"]
	network.Params = []openapi.Param{create.Params[0], {Name: "If-Match", In: "header", Schema: openapi.String}}
	rule := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
		"direction": openapi.String, "protocol": openapi.String, "from_port": openapi.Integer, "to_port": openapi.Integer, "remote_cidr": openapi.String,
	}}
	network.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、kind。网络、子网和组创建需要 name。AWS VPC 需要 cidr，子网额外需要 parent_id、cidr、If-Match。AWS 安全组需要父 VPC 版本。security-group-rule 需要 rule、父安全组 parent_id 和 If-Match，并始终独立审批。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object",
			Properties: map[string]openapi.Schema{"account": openapi.String, "region": openapi.String, "kind": openapi.String,
				"name": openapi.String, "parent_id": openapi.String, "cidr": openapi.String, "rule": rule}}}}}
	revokeRule := accountOp("revokeSecurityGroupRule", "202", "已持久受理的安全组规则撤销操作，需独立审批", true)
	revokeRule.Responses["412"], revokeRule.Responses["429"] = network.Responses["412"], submit.Responses["429"]
	revokeRule.Params = submit.Params
	revokeRule.Body = &openapi.RequestBody{Required: true, Description: "必填 account、region、parent_id、rule_id。If-Match 使用父安全组当前归属版本。",
		Content: map[string]openapi.MediaType{"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"account": openapi.String, "region": openapi.String, "parent_id": openapi.String, "rule_id": openapi.String,
		}}}}}
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
		"POST " + accountsRoute:                                            accountOp("registerCloudAccount", "201", "待校验账号", true),
		"GET " + accountsRoute + "/:account":                               accountOp("getCloudAccount", "200", "账号元数据", false),
		"PATCH " + accountsRoute + "/:account":                             patchAccount,
		"POST " + instancesRoute:                                           create,
		"GET " + operationsRoute + "/deletion-preview":                     preview,
		"POST " + operationsRoute:                                          submit,
		"GET " + operationsRoute + "/:operation":                           accountOp("getInstanceOperation", "200", "项目操作状态", false),
		"POST " + operationsRoute + "/:operation/approval":                 approval,
		"POST " + operationsRoute + "/:operation/resolution":               review,
		"GET " + operationsRoute + "/:operation/events":                    events,
		"POST " + operationsRoute + "/:operation/retry-observation":        retryObservation,
		"POST " + networkOperationsRoute:                                   network,
		"POST " + networkOperationsRoute + "/revoke-rule":                  revokeRule,
		"GET " + networkOperationsRoute + "/:operation":                    accountOp("getNetworkOperation", "200", "项目网络操作状态", false),
		"POST " + networkOperationsRoute + "/:operation/approval":          networkApproval,
		"POST " + networkOperationsRoute + "/:operation/resolution":        networkReview,
		"GET " + networkOperationsRoute + "/:operation/events":             networkEvents,
		"POST " + networkOperationsRoute + "/:operation/retry-observation": networkRetry,
		"GET " + projectOperationsRoute:                                    projectOperations,
		"GET " + projectOperationsRoute + "/:operation":                    accountOp("getProjectOperation", "200", "项目操作状态，安全组规则操作附带不可变的 network_intent", false),
		"GET " + projectOperationsRoute + "/:operation/events":             projectEvents,
		"POST " + projectOperationsRoute + "/:operation/approval":          projectApproval,
		"POST " + projectOperationsRoute + "/:operation/resolution":        projectReview,
		"POST " + projectOperationsRoute + "/:operation/retry-observation": projectRetry,
		"POST " + operationBatchesRoute + "/preview":                       batchPreview,
		"POST " + operationBatchesRoute:                                    batchSubmit,
		"GET " + operationBatchesRoute + "/:batch":                         accountOp("getOperationBatch", "200", "项目批次及当前子操作状态", false),
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
	catalogOpenAPI(hints, accountOp)
	alertOpenAPI(hints, accountOp)
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
		{Name: "rule", In: "query", Description: "operation_review、operation_delayed、scan_failures 或 scan_stale", Schema: openapi.String},
		{Name: "after", In: "query", Schema: openapi.String},
		{Name: "limit", In: "query", Schema: openapi.Integer},
	}
	list.Responses["400"] = openapi.Response{Description: "筛选条件或续读位置无效"}
	get := operation("getProjectAlert", "200", "告警详情以及当前修订号 ETag", false)
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
