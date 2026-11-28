package kvmapp

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmProjectHostsRoute = kvmProjectRoute + "/hosts"
const kvmProjectVMsRoute = kvmProjectRoute + "/vms"

func (h kvmHTTP) listProjectHosts(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, _ string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil {
		return err
	}
	page, err := h.store.ListHosts(ctx, token, scope, query)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func (h kvmHTTP) listProjectVMs(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, _ string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil {
		return err
	}
	page, err := h.store.ListVMs(ctx, token, scope, query)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func kvmInventoryQuery(raw string) (kvm.InventoryQuery, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return kvm.InventoryQuery{}, kvm.ErrInvalid
	}
	result := kvm.InventoryQuery{Limit: 50}
	for key, values := range query {
		if len(values) != 1 || values[0] == "" {
			return kvm.InventoryQuery{}, kvm.ErrInvalid
		}
		switch key {
		case "site":
			result.Site = values[0]
		case "after":
			result.After = values[0]
		case "limit":
			result.Limit, err = strconv.Atoi(values[0])
			if err != nil {
				return kvm.InventoryQuery{}, kvm.ErrInvalid
			}
		default:
			return kvm.InventoryQuery{}, kvm.ErrInvalid
		}
	}
	return result, nil
}

func kvmInventoryHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	for _, item := range []struct {
		path, id, description string
		fields                map[string]openapi.Schema
	}{
		{kvmProjectHostsRoute, "listProjectKVMHosts", "按站点和宿主机 ID 排序的项目宿主机摘要。", map[string]openapi.Schema{
			"id": openapi.String, "site": openapi.String, "failure_domain": openapi.String, "fence_state": openapi.String,
			"draining": {Type: "boolean"}, "reported_at": {Type: "string", Format: "date-time"},
		}},
		{kvmProjectVMsRoute, "listProjectKVMVMs", "按站点和虚拟机 UUID 排序的项目虚拟机摘要，包含当前放置和状态。", map[string]openapi.Schema{
			"id": {Type: "string", Format: "uuid"}, "site": openapi.String, "host": openapi.String,
			"state": openapi.String, "ipv4": {Type: "string", Format: "ipv4"},
		}},
	} {
		op := operation(item.id, "200", item.description+"有更多记录时返回 next。", false)
		op.Params = []openapi.Param{
			{Name: "site", In: "query", Schema: openapi.String, Description: "仅查询此站点。缺省查询整个项目。"},
			{Name: "after", In: "query", Schema: openapi.String, Description: "同一列表上一页返回的 next。"},
			{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
		}
		entry := openapi.Schema{Type: "object", Properties: item.fields}
		page := openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{
			"items": {Type: "array", Items: &entry}, "next": openapi.String,
		}}
		response := op.Responses["200"]
		response.Content = map[string]openapi.MediaType{
			"application/json": {Schema: openapi.Schema{Type: "object", Properties: map[string]openapi.Schema{"data": page}}},
		}
		op.Responses["200"] = response
		op.Responses["400"] = openapi.Response{Description: "分页参数或站点标识无效。"}
		hints["GET "+item.path] = op
	}
}
