package main

import "github.com/birdordev/plumego/x/openapi"

func kvmVolumeCleanupHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	request := operation("requestKVMVolumeCleanup", "202", "登记已释放放置的保留卷清理", true)
	request.Params = []openapi.Param{{Name: "If-Match", In: "header", Required: true, Schema: openapi.String}}
	request.Responses["412"] = openapi.Response{Description: "需要当前宿主机版本"}
	request.Body = serviceCatalogBody([]string{"cleanup_id", "backup_image_id", "evidence_sha256"}, false)
	hints["POST "+kvmVolumeCleanupsRoute] = request
	hints["GET "+kvmVolumeCleanupRoute] = operation("getKVMVolumeCleanup", "200", "读取审批、派发和容量结算回执", false)
	list := operation("listKVMVolumeCleanups", "200", "分页读取宿主机仍在进行的卷清理", false)
	list.Responses["400"] = openapi.Response{Description: "分页选项无效"}
	list.Params = []openapi.Param{{Name: "after", In: "query", Schema: openapi.String}, {Name: "limit", In: "query", Schema: openapi.Integer}}
	hints["GET "+kvmHostVolumeCleanupsRoute] = list
	for _, item := range []struct{ route, id, summary string }{
		{kvmRetainedVolumesRoute, "listKVMRetainedVolumes", "分页读取 VM 当前或历史保留卷及已清理记录，显示原宿主机和备份尺寸约束。"},
		{kvmVolumeCleanupsRoute, "listKVMVolumeCleanupHistory", "分页读取放置上的清理历史，最近申请优先。"},
	} {
		op := operation(item.id, "200", item.summary, false)
		op.Params = list.Params
		op.Responses["400"] = list.Responses["400"]
		hints["GET "+item.route] = op
	}
	review := operation("reviewKVMVolumeCleanup", "200", "独立批准，或在派发前关闭清理", true)
	review.Params = request.Params
	review.Responses["412"] = openapi.Response{Description: "需要当前清理版本"}
	review.Body = serviceCatalogBody([]string{"decision"}, false)
	hints["POST "+kvmVolumeCleanupRoute+"/review"] = review
	resolve := operation("resolveFencedKVMVolumeCleanup", "200", "确认隔离后以现场证据结算未知删除结果", true)
	resolve.Params = request.Params
	resolve.Responses["412"] = review.Responses["412"]
	resolve.Body = serviceCatalogBody([]string{"observed", "evidence_sha256", "second_evidence_sha256"}, false)
	hints["POST "+kvmVolumeCleanupRoute+"/resolve"] = resolve
	agent := operation("applyKVMVolumeCleanupCommand", "200", "绑定代理领取一次删除并回报卷是否存在", true)
	agent.Security = []openapi.SecurityRequirement{{"serviceBearer": {}}}
	agent.Responses["412"] = openapi.Response{Description: "代理代次或清理版本过期"}
	agent.Body = serviceCatalogBody([]string{"uuid", "action", "pool_sha256"}, false)
	agent.Body.Content["application/json"].Schema.Properties["generation"] = openapi.Integer
	agent.Body.Content["application/json"].Schema.Properties["revision"] = openapi.Integer
	hints["POST "+kvmVolumeCleanupAgentRoute] = agent
}
