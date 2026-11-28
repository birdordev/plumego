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

const fixtureDatabaseReceipt = "fixture-database-request"

type databaseRenameFixture struct {
	databaseFixture
	name               atomic.Value
	writes             atomic.Int32
	wrongTarget        atomic.Bool
	mismatchAfterWrite atomic.Bool
	dropReply          atomic.Bool
}

func (f *databaseRenameFixture) serveRename(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var validation bool
	var id, name string
	switch {
	case r.Header.Get("X-TC-Action") == "ModifyDBInstanceName":
		service, version, idKey := "cdb", "2017-03-20", "InstanceId"
		switch f.product {
		case provider.TencentPostgreSQL:
			service, version, idKey = "postgres", "2017-03-12", "DBInstanceId"
		case provider.TencentSQLServer:
			service, version = "sqlserver", "2018-03-28"
		}
		var input map[string]string
		body, err := io.ReadAll(r.Body)
		validation = err == nil && json.Unmarshal(body, &input) == nil && len(input) == 2 &&
			r.Method == http.MethodPost && r.Host == service+".tencentcloudapi.com" && r.URL.RequestURI() == "/" &&
			r.Header.Get("X-TC-Region") == f.region && r.Header.Get("X-TC-Version") == version &&
			r.Header.Get("X-TC-Token") == fixtureLaunchSession && validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret)
		id, name = input[idKey], input["InstanceName"]
	case r.Header.Get("X-ACS-Action") == "ModifyDBInstanceDescription":
		query := r.URL.Query()
		validation = f.product == provider.AlibabaRDS && r.Method == http.MethodPost &&
			r.Host == "rds.aliyuncs.com" && r.URL.Path == "/" && len(query) == 2 &&
			r.Header.Get("X-ACS-Version") == "2014-08-15" && r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession &&
			validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
		id, name = query.Get("DBInstanceId"), query.Get("DBInstanceDescription")
	case r.Method == http.MethodPut:
		var input map[string]string
		validation = f.product == provider.HuaweiRDS && r.Host == "rds."+f.region+".myhuaweicloud.com" &&
			r.URL.RequestURI() == "/v3/"+fixtureHuaweiProject+"/instances/"+f.id+"/name" &&
			r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM && json.NewDecoder(r.Body).Decode(&input) == nil && len(input) == 1
		id, name = f.id, input["name"]
	default:
		f.serveDatabase(t, w, r)
		return
	}
	if !validation || id != f.id || !provider.ValidDatabaseRenameName(name) {
		t.Errorf("incorrect database rename request for %s: %s %s", f.product, r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.writes.Add(1)
	f.name.Store(name)
	if f.mismatchAfterWrite.Load() {
		f.wrongTarget.Store(true)
	}
	if f.dropReply.Load() {
		panic(http.ErrAbortHandler)
	}
	switch f.vendor {
	case provider.TencentCVM:
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]string{"RequestId": fixtureDatabaseReceipt}})
	case provider.AlibabaECS:
		testsupport.WriteJSON(t, w, map[string]string{"RequestId": fixtureDatabaseReceipt})
	case provider.HuaweiECS:
		w.Header().Set("X-Request-Id", fixtureDatabaseReceipt)
		w.WriteHeader(http.StatusOK)
	}
}

func (f *databaseRenameFixture) readTarget(product string) map[string]any {
	id := f.id
	if f.wrongTarget.Load() {
		id += "-wrong"
	}
	return f.databaseRow(product, id, f.name.Load().(string), fixtureDatabaseVersion, f.region)
}
