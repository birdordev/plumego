package main

import (
	"context"
	"net/http"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

const kvmProvisioningHostsRoute = kvmProjectRoute + "/provisioning-hosts"
const kvmProvisioningStatusRoute = kvmPlacementRoute + "/provisioning"

func (h kvmHTTP) listProvisioningHosts(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, _ string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil {
		return err
	}
	page, err := h.store.ListProvisioningHosts(ctx, token, scope, query)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func (h kvmHTTP) listBootImages(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	query, err := kvmInventoryQuery(r.URL.RawQuery)
	if err != nil || query.Site != "" {
		return kvm.ErrInvalid
	}
	page, err := h.store.ListBootImages(ctx, token, scope, site, query.After, query.Limit)
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, page)
	return nil
}

func (h kvmHTTP) getProvisioningStatus(ctx context.Context, w http.ResponseWriter, r *http.Request, token string, scope identity.Scope, site string) error {
	result, err := h.store.GetProvisioningStatus(ctx, token, scope, site, router.Param(r, "placement"))
	if err != nil {
		return err
	}
	h.write(w, r, http.StatusOK, result)
	return nil
}

func kvmProvisioningHints(hints map[string]openapi.Op, operation func(string, string, string, bool) openapi.Op) {
	for _, item := range []struct{ route, id, description string }{
		{kvmProvisioningHostsRoute, "listKVMProvisioningHosts", "项目执行者读取上报仍有效的宿主机、网络和存储池。预留时核对实际剩余容量。"},
		{kvmBootImagesRoute, "listKVMBootImages", "项目成员分页读取站点登记的 raw 镜像。"},
	} {
		op := operation(item.id, "200", item.description, false)
		op.Params = []openapi.Param{
			{Name: "after", In: "query", Schema: openapi.String, Description: "上一页的 next。"},
			{Name: "limit", In: "query", Schema: openapi.Integer, Description: "默认 50，最大 100。"},
		}
		if item.route == kvmProvisioningHostsRoute {
			op.Params = append(op.Params, openapi.Param{Name: "site", In: "query", Schema: openapi.String})
		}
		op.Responses["400"] = openapi.Response{Description: "查询参数无效"}
		hints["GET "+item.route] = op
	}
	hints["GET "+kvmProvisioningStatusRoute] = operation("getKVMProvisioningStatus", "200",
		"原预留人读取预留状态及已提交的 VM ID。冷迁移后仍使用同一 VM ID。", false)
}
