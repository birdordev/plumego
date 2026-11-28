package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

const fixtureCacheRenameReceipt = "cache-rename-fixture-request"

type cacheRenameFixture struct {
	cacheFixture
	name        atomic.Value
	writes      atomic.Int32
	dropReply   atomic.Bool
	wrongTarget atomic.Bool
}

func (f *cacheRenameFixture) serveRename(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var name string
	var valid bool
	query := r.URL.Query()
	switch {
	case r.Header.Get("X-TC-Action") == "ModifyInstance":
		var body struct {
			Operation                  string
			InstanceIds, InstanceNames []string
		}
		data, err := io.ReadAll(r.Body)
		if err == nil && json.Unmarshal(data, &body) == nil && len(body.InstanceNames) == 1 {
			name = body.InstanceNames[0]
		}
		var fields map[string]json.RawMessage
		valid = json.Unmarshal(data, &fields) == nil && len(fields) == 3 &&
			f.vendor == provider.TencentCVM && r.Method == http.MethodPost && r.Host == "redis.tencentcloudapi.com" &&
			r.URL.RequestURI() == "/" && r.Header.Get("X-TC-Region") == f.region &&
			r.Header.Get("X-TC-Version") == "2018-04-12" && r.Header.Get("X-TC-Token") == fixtureLaunchSession &&
			body.Operation == "rename" && len(body.InstanceIds) == 1 && body.InstanceIds[0] == f.id &&
			validFixtureTencentSignature(r, data, fixtureLaunchKey, fixtureLaunchSecret)
	case r.Header.Get("X-ACS-Action") == "ModifyInstanceAttribute":
		name = query.Get("InstanceName")
		valid = f.vendor == provider.AlibabaECS && r.Method == http.MethodPost &&
			r.Host == "r-kvstore."+f.region+".aliyuncs.com" && r.URL.Path == "/" &&
			len(query) == 2 && query.Get("InstanceId") == f.id && r.Header.Get("X-ACS-Version") == "2015-01-01" &&
			r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession &&
			validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
	case r.Method == http.MethodPut:
		var fields map[string]string
		valid = f.vendor == provider.HuaweiECS && r.Host == "dcs."+f.region+".myhuaweicloud.com" &&
			r.URL.RequestURI() == "/v2/"+fixtureHuaweiProject+"/instances/"+f.id &&
			r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM && r.Header.Get("Content-Type") == "application/json" &&
			json.NewDecoder(r.Body).Decode(&fields) == nil && len(fields) == 1
		name = fields["name"]
	default:
		f.serveRenameRead(t, w, r)
		return
	}
	if !valid || !provider.ValidCacheRenameName(name) {
		t.Errorf("invalid cache write for %s: %s %s", f.vendor, r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.writes.Add(1)
	f.name.Store(name)
	if f.dropReply.Load() {
		f.wrongTarget.Store(true)
		panic(http.ErrAbortHandler)
	}
	switch f.vendor {
	case provider.TencentCVM:
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]string{"RequestId": fixtureCacheRenameReceipt}})
	case provider.AlibabaECS:
		testsupport.WriteJSON(t, w, map[string]string{"RequestId": fixtureCacheRenameReceipt})
	case provider.HuaweiECS:
		w.Header().Set("X-Request-Id", fixtureCacheRenameReceipt)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *cacheRenameFixture) serveRenameRead(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" && f.tencent(t, w, r) ||
		r.Host == "sts.aliyuncs.com" && f.alibaba(t, w, r) ||
		r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
		return
	}
	id, offset, valid := f.request(r)
	if !valid || id != f.id || offset != 0 {
		t.Errorf("cache rename expected an exact read: %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	index := 0
	if f.wrongTarget.Load() {
		index = 2
	}
	row := f.row(index, false)
	if f.vendor == provider.HuaweiECS {
		row["name"] = f.name.Load().(string)
	} else {
		row["InstanceName"] = f.name.Load().(string)
	}
	f.writePage(t, w, 0, 1, []map[string]any{row})
}
