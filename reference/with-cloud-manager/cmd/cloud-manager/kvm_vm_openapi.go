package main

import "github.com/birdordev/plumego/x/openapi"

func kvmVMHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	kvmLiveMigrationHints(hints, operation)
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
	fenced := operation("requestKVMFencedVMRecovery", "202", "记录隔离宿主机上的未决虚拟机现场观测", true)
	fenced.Params = review.Params
	fenced.Responses["412"] = openapi.Response{Description: "需要当前虚拟机版本和隔离代次"}
	fenced.Body = serviceCatalogBody([]string{"recovery_id", "host_uuid", "observed", "evidence_sha256", "second_evidence_sha256"}, false)
	fenced.Body.Content["application/json"].Schema.Properties["fence_generation"] = openapi.Integer
	fenced.Body.Description = "提交恢复 ID、宿主机 UUID、隔离代次、观测和证据摘要。缺失观测另需独立的第二份证据摘要。"
	hints["POST "+kvmFencedVMRecoveryRoute] = fenced
	hints["GET "+kvmFencedVMRecoveryRoute] = operation("getKVMFencedVMRecovery", "200", "本虚拟机最新恢复证据与审批结果", false)
	fenceReview := operation("reviewKVMFencedVMRecovery", "200", "批准时原子提交虚拟机观测和恢复决定", true)
	fenceReview.Params = review.Params
	fenceReview.Responses["412"] = fenced.Responses["412"]
	fenceReview.Body = serviceCatalogBody([]string{"recovery_id", "decision"}, false)
	hints["POST "+kvmFencedVMRecoveryRoute+"/review"] = fenceReview
	migration := operation("requestKVMColdMigration", "202", "固定源虚拟机版本、目标预留和 raw 备份的冷迁移申请", true)
	migration.Params = review.Params
	migration.Responses["412"] = review.Responses["412"]
	migration.Responses["409"] = openapi.Response{Description: "源域缺失条件、备份或目标预留不满足冷迁移要求"}
	migration.Body = serviceCatalogBody([]string{"migration_id", "target_placement_id", "image_id", "evidence_sha256"}, false)
	migration.Body.Content["application/json"].Schema.Properties["source_fence_generation"] = openapi.Integer
	migration.Body.Content["application/json"].Schema.Properties["require_verified_backup"] = openapi.Schema{Type: "boolean"}
	migration.Body.Description = "故障疏散需提供独立确认的源隔离代次。省略 target_placement_id 时，服务自动在其他故障域预留同规格容量。"
	hints["POST "+kvmColdMigrationRoute] = migration
	hints["GET "+kvmColdMigrationRoute] = operation("getKVMColdMigration", "200", "本虚拟机最新冷迁移申请及审批结果", false)
	hints["GET "+kvmColdMigrationReceiptRoute] = operation("getKVMColdMigrationReceipt", "200", "按迁移 ID 读取申请、审批及源端清理回执", false)
	hints["GET "+kvmColdMigrationEligibilityRoute] = operation("getKVMColdMigrationEligibility", "200", "读取 VM 当前来源资格和已确认隔离代次", false)
	history := operation("listKVMColdMigrations", "200", "按申请时间倒序分页读取虚拟机的冷迁移和疏散历史", false)
	history.Params = []openapi.Param{{Name: "after", In: "query", Schema: openapi.String}, {Name: "limit", In: "query", Schema: openapi.Integer}}
	history.Responses["400"] = openapi.Response{Description: "分页参数无效"}
	hints["GET "+kvmColdMigrationsRoute] = history
	migrationReview := operation("reviewKVMColdMigration", "200", "独立审批。同事务切换放置和受理目标宿主机工作。", true)
	migrationReview.Params = review.Params
	migrationReview.Responses["412"] = openapi.Response{Description: "需要原虚拟机版本和目标宿主机代次"}
	migrationReview.Responses["409"] = migration.Responses["409"]
	migrationReview.Body = serviceCatalogBody([]string{"migration_id", "decision"}, false)
	hints["POST "+kvmColdMigrationRoute+"/review"] = migrationReview

	for _, item := range []struct{ method, path, id, description string }{
		{"POST", kvmVMPollRoute, "pollKVMVM", "宿主机代理领取一个任务描述。返回空对象表示没有任务。"},
		{"POST", kvmVMJobRoute + "/arm", "armKVMVM", "登记下一项宿主机写入，仅允许一次。"},
		{"POST", kvmVMBackupRoute, "authorizeKVMVMBackup", "绑定代理核对同一虚拟机版本及已保留启动卷，供本机冷备份。"},
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
