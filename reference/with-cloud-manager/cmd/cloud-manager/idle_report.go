package main

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/inventory"
)

const idleInstancesRoute = governanceRoute + "/idle-instances"

func idleInstancesHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	return serveInventory(func(r *http.Request) (inventory.IdleQuery, error) {
		query, state, err := parseGovernanceReportQuery(r, false)
		return inventory.IdleQuery{Query: query, State: state}, err
	}, store.IdleInstances, func(w http.ResponseWriter, r *http.Request, page inventory.IdleInstances) error {
		return contract.WriteResponse(w, r, http.StatusOK, page, nil)
	}, logger)
}

func idleReportOpenAPI(readAuth []openapi.SecurityRequirement) openapi.Op {
	return openapi.Op{OperationID: "listIdleInstances", Security: readAuth,
		Params: []openapi.Param{
			{Name: "state", In: "query", Schema: openapi.String, Description: "candidate、active、insufficient 或 not_running"},
			{Name: "account", In: "query", Schema: openapi.String},
			{Name: "region", In: "query", Schema: openapi.String},
			{Name: "cursor", In: "query", Schema: openapi.String},
			{Name: "limit", In: "query", Schema: openapi.Integer},
		}, Responses: map[string]openapi.Response{
			"200": {Description: "项目内的七天实例利用率分析、样本覆盖、指标峰值、判断阈值和续页游标"},
			"400": {Description: "筛选项或游标无效"},
			"401": {Description: "凭据缺失、过期或无效"},
			"403": {Description: "项目权限不足"},
			"503": {Description: "盘点或分析存储不可用"},
		}}
}
