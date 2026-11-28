package cloudtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

const powerBMC = "0fea9bd0-98bf-49a1-ad93-a98567937e16"
const powerSystem = "11112222-3333-4444-8666-777788889999"
const powerSystemPath = "/redfish/v1/Systems/fixture"

type redfishPowerFixture struct {
	on, drop, unsafeTarget, revoke, pending atomic.Bool
	writes                                  atomic.Int32
	pxeWrites                               atomic.Int32
	bootMode                                atomic.Int32
	weakETag, pxeConflict, dropPXE          atomic.Bool
	revokeApproval                          func() error
}

func (f *redfishPowerFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/redfish/v1/" {
		if r.Header.Get("Authorization") != "" {
			t.Error("credentials sent before verification")
		}
		f.respond(t, w, map[string]any{"UUID": powerBMC, "Systems": map[string]string{"@odata.id": "/redfish/v1/Systems"}})
		return
	}
	if user, pass, ok := r.BasicAuth(); !ok || user != "user" || pass != "fake-password" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/redfish/v1/Systems":
		f.respond(t, w, map[string]any{"Members@odata.count": 1, "Members": []map[string]string{{"@odata.id": powerSystemPath}}})
	case powerSystemPath:
		if r.Method == http.MethodPatch {
			f.patchPXE(t, w, r)
			return
		}
		if f.revoke.CompareAndSwap(true, false) {
			if err := f.revokeApproval(); err != nil {
				t.Error(err)
			}
		}
		power := "Off"
		if f.on.Load() {
			power = "On"
		}
		target := powerSystemPath + "/Actions/ComputerSystem.Reset"
		if f.unsafeTarget.Load() {
			target = "/redfish/v1/Systems/another/Actions/ComputerSystem.Reset"
		}
		mode := f.bootMode.Load()
		enabled, targetKind := "Disabled", "None"
		switch mode {
		case 1:
			enabled, targetKind = "Once", "Pxe"
		case 2:
			enabled, targetKind = "Continuous", "Hdd"
		}
		etag := fmt.Sprintf(`"boot-%d"`, mode)
		if f.weakETag.Load() {
			etag = "W/" + etag
		}
		w.Header().Set("ETag", etag)
		f.respond(t, w, map[string]any{"Boot": map[string]any{"BootSourceOverrideEnabled": enabled, "BootSourceOverrideTarget": targetKind,
			"BootSourceOverrideTarget@Redfish.AllowableValues": []string{"Hdd", "Pxe"}}, "@odata.id": powerSystemPath, "UUID": powerSystem, "Name": "server", "SystemType": "Physical", "PowerState": power,
			"Actions": map[string]any{"#ComputerSystem.Reset": map[string]string{"target": target, "@Redfish.ActionInfo": powerSystemPath + "/ResetActionInfo"}}})
	case powerSystemPath + "/ResetActionInfo":
		f.respond(t, w, map[string]any{"Parameters": []map[string]any{{"Name": "ResetType", "AllowableValues": []string{"On", "GracefulShutdown"}}}})
	case powerSystemPath + "/Actions/ComputerSystem.Reset":
		var payload map[string]string
		if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&payload) != nil || len(payload) != 1 {
			t.Error("invalid action request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch payload["ResetType"] {
		case "On":
			if !f.pending.Load() {
				f.on.Store(true)
				f.bootMode.CompareAndSwap(1, 0)
			}
		case "GracefulShutdown":
			f.on.Store(false)
		default:
			t.Error("unexpected reset type")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.writes.Add(1)
		if f.drop.CompareAndSwap(true, false) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		t.Errorf("unexpected Redfish target: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (*redfishPowerFixture) respond(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Error(err)
	}
}

func (f *redfishPowerFixture) patchPXE(t *testing.T, w http.ResponseWriter, r *http.Request) {
	mode := f.bootMode.Load()
	if r.Header.Get("If-Match") != fmt.Sprintf(`"boot-%d"`, mode) || f.on.Load() {
		t.Error("invalid PXE write preconditions")
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	var request map[string]map[string]string
	if json.NewDecoder(r.Body).Decode(&request) != nil || len(request) != 1 {
		t.Error("invalid PXE patch")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	boot := request["Boot"]
	f.pxeWrites.Add(1)
	if f.pxeConflict.Load() {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	switch {
	case mode == 0 && len(boot) == 2 && boot["BootSourceOverrideEnabled"] == "Once" && boot["BootSourceOverrideTarget"] == "Pxe":
		f.bootMode.Store(1)
	case mode == 1 && len(boot) == 1 && boot["BootSourceOverrideEnabled"] == "Disabled":
		f.bootMode.Store(0)
	default:
		t.Error("unexpected PXE patch")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if f.dropPXE.CompareAndSwap(true, false) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
