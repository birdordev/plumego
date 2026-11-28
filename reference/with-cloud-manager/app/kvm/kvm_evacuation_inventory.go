package kvmapp

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmEvacuationCandidatesRoute = kvmHostFenceRoute + "/evacuation-candidates"

func (h kvmHTTP) listEvacuationCandidates(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	page, err := h.store.ListEvacuationCandidates(ctx, token, scope, site, router.Param(r, "host"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func (h kvmHTTP) listEvacuationBatches(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	page, err := h.store.ListEvacuationBatches(ctx, token, scope, site, router.Param(r, "host"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func (h kvmHTTP) listEvacuationPlans(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	page, err := h.store.ListEvacuationPlans(ctx, token, scope, site, router.Param(r, "host"), query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func kvmEvacuationInventoryHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	historyFields := map[string]openapi.Schema{
		"id": openapi.String, "source_fence_generation": openapi.Integer, "requester": openapi.String,
		"recorded_at": {Type: "string", Format: "date-time"}, "status": openapi.String, "batch_id": openapi.String,
	}
	for _, entry := range []struct {
		path, id, description string
		fields                map[string]openapi.Schema
	}{
		{kvmEvacuationCandidatesRoute, "listKVMEvacuationCandidates", "管理员读取同源候选虚拟机和隔离快照，mode 为 plan、batch 或 unavailable。", map[string]openapi.Schema{
			"vm_id": {Type: "string", Format: "uuid"}, "vm_revision": openapi.Integer, "disk_mib": openapi.Integer,
			"state": openapi.String, "network": openapi.String, "ipv4": {Type: "string", Format: "ipv4"},
		}},
		{kvmEvacuationBatchesRoute, "listKVMEvacuationBatches", "按受理时间倒序读取源宿主机疏散批次。", historyFields},
		{kvmEvacuationPlansRoute, "listKVMEvacuationPlans", "按登记时间倒序读取源宿主机疏散预案及其状态。", historyFields},
	} {
		op := operation(entry.id, "200", entry.description, false)
		op.Params = []openapi.Param{
			{Name: "after", In: "query", Schema: openapi.String, Description: "同一列表上一页返回的 next。"},
			{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
		}
		item := openapi.Schema{Type: "object", Properties: entry.fields}
		fields := map[string]openapi.Schema{"items": {Type: "array", Items: &item}, "next": openapi.String}
		if entry.path == kvmEvacuationCandidatesRoute {
			fields["mode"] = openapi.String
			fields["host_revision"] = openapi.Integer
			fields["source_fence_generation"] = openapi.Integer
		}
		response := op.Responses["200"]
		response.Content = map[string]openapi.MediaType{
			"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
				"data": {Type: "object", Properties: fields},
			}}},
		}
		op.Responses["200"] = response
		op.Responses["400"] = openapi.Response{Description: "分页参数无效。"}
		hints["GET "+entry.path] = op
	}
}
