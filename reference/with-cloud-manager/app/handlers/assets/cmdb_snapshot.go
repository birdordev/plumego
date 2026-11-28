package assets

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/cmdb"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

const CMDBSnapshotRoute = transport.GovernanceRoute + "/cmdb-snapshot"

func CMDBSnapshotHandler(store inventory.Store, logger plumelog.StructuredLogger) http.Handler {
	slots := make(chan struct{}, 2)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok || token == "" {
			transport.WriteAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		if r.URL.RawQuery != "" {
			transport.WriteAccountError(w, r, identity.ErrAccountInvalid, logger)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "2")
			transport.WriteAccountError(w, r, contract.NewErrorBuilder().Type(contract.TypeRateLimited).
				Message("CMDB snapshot capacity is busy").Build(), logger)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		buffer := bufio.NewWriterSize(w, 32<<10)
		stream := cmdb.NewSnapshotWriter(buffer, cmdb.Project{Tenant: scope.Tenant, Project: scope.Project})
		started := false
		start := func(captured time.Time) error {
			w.Header().Set("Content-Type", cmdb.MediaType+"; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			started = true
			if r.Method == http.MethodHead {
				return nil
			}
			return stream.Start(captured)
		}
		write := stream.Asset
		if r.Method == http.MethodHead {
			write = nil
		}
		err := store.StreamCMDB(ctx, token, scope, start, write)
		if err != nil && !started {
			transport.WriteAccountError(w, r, err, logger)
			return
		}
		if err == nil && write != nil {
			err = errors.Join(stream.Finish(), buffer.Flush())
		}
		if err != nil {
			logger.Warn("CMDB snapshot stream interrupted", plumelog.Fields{})
		}
	})
}

func CMDBSnapshotOpenAPI() openapi.Op {
	return openapi.Op{OperationID: "streamProjectCMDBSnapshot", Security: []openapi.SecurityRequirement{{"serviceBearer": {}}},
		Responses: map[string]openapi.Response{
			"200": {Description: "单个数据库快照中的项目资产及可见拓扑。NDJSON 依次包含 manifest、asset 和 complete，接收方须验证末帧条数与 SHA-256。",
				Content: map[string]openapi.MediaType{cmdb.MediaType: {Schema: openapi.String}}},
			"400": {Description: "存在不受支持的查询参数"},
			"401": {Description: "服务凭据缺失、无效、过期或撤销"},
			"403": {Description: "无项目查看权限"},
			"429": {Description: "该 API 副本的快照并发容量已满"},
			"503": {Description: "无法开始读取快照"},
		}}
}
