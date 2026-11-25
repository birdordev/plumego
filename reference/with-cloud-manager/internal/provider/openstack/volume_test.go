package openstack

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestNovaVolumeChanges(t *testing.T) {
	const instance = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	const volume = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	const project = "project"
	const serverPath = "/compute/v2.1/servers/" + instance
	var state atomic.Value
	state.Store("available")
	var attachCalls, detachCalls atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+serverPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenStack-API-Version") != novaVolumeVersion || r.Header.Get("X-Auth-Token") != "fixture-auth" {
			t.Error("invalid Nova proof scope")
		}
		parent, disks, image := project, "[]", `{"id":"cccccccc-cccc-cccc-cccc-cccccccccccc"}`
		switch state.Load() {
		case "attached", "nova-only", "root", "unknown-root", "destructive":
			disks = fmt.Sprintf(`[{"id":%q,"delete_on_termination":%t}]`, volume, state.Load() == "destructive")
		case "wrong-project":
			parent = "foreign-project"
		}
		if state.Load() == "unknown-root" {
			image = `""`
		}
		respond(w, fmt.Sprintf(`{"server":{"id":%q,"tenant_id":%q,"status":"ACTIVE","image":%s,"os-extended-volumes:volumes_attached":%s}}`,
			instance, parent, image, disks), t)
	})
	mux.HandleFunc("GET /cinder/v3/"+project+"/volumes/"+volume, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-auth" {
			t.Error("unscoped Cinder read")
		}
		stateValue := state.Load().(string)
		if stateValue == "not-found" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status, attachments, bootable := "available", "[]", "false"
		if stateValue == "attached" || stateValue == "root" || stateValue == "unknown-root" || stateValue == "destructive" {
			status = "in-use"
			attachments = fmt.Sprintf(`[{"server_id":%q,"volume_id":%q,"device":"/dev/vdb"}]`, instance, volume)
		}
		if stateValue == "root" {
			bootable = "true"
		}
		respond(w, fmt.Sprintf(`{"volume":{"id":%q,"os-vol-tenant-attr:tenant_id":%q,"status":%q,"bootable":%q,"multiattach":%t,"attachments":%s}}`,
			volume, project, status, bootable, stateValue == "multiattach", attachments), t)
	})
	mux.HandleFunc("POST "+serverPath+"/os-volume_attachments", func(w http.ResponseWriter, r *http.Request) {
		attachCalls.Add(1)
		var payload struct {
			Attachment struct {
				Volume  string `json:"volumeId"`
				Destroy *bool  `json:"delete_on_termination"`
			} `json:"volumeAttachment"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Attachment.Volume != volume ||
			payload.Attachment.Destroy == nil || *payload.Attachment.Destroy || r.Header.Get("OpenStack-API-Version") != novaVolumeVersion {
			t.Errorf("unsafe Nova attach request: %v", err)
		}
		w.Header().Set("X-Openstack-Request-Id", "req-attach")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("DELETE "+serverPath+"/os-volume_attachments/"+volume, func(w http.ResponseWriter, r *http.Request) {
		detachCalls.Add(1)
		if r.URL.RawQuery != "" || r.Header.Get("X-Auth-Token") != "fixture-auth" || r.Header.Get("OpenStack-API-Version") != novaVolumeVersion {
			t.Error("unsafe Nova detach request")
		}
		w.Header().Set("X-Openstack-Request-Id", "req-detach")
		w.WriteHeader(http.StatusAccepted)
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scope := provider.Scope{Provider: Name, RemoteIdentity: "site:" + project, Region: "RegionOne", Kind: provider.Instance}
	client := &Client{http: *server.Client(), identity: scope.RemoteIdentity, token: "fixture-auth", endpoints: map[string]map[string]*url.URL{
		"RegionOne": {"compute": origin.JoinPath("compute", "v2.1"), "volumev3": origin.JoinPath("cinder", "v3", project)},
	}}
	attach := provider.VolumeChange{InstanceID: instance, VolumeID: volume, Attach: true}
	detach := provider.VolumeChange{InstanceID: instance, VolumeID: volume}
	if _, err := client.ApplyVolume(t.Context(), scope, attach); !provider.IsFailure(err, provider.VolumeBlocked) {
		t.Fatalf("Nova writes enabled by default: %v", err)
	}
	client.allowVolumeChanges = true
	for _, invalid := range []string{"wrong-project", "multiattach"} {
		state.Store(invalid)
		if _, err := client.ApplyVolume(t.Context(), scope, attach); !provider.IsFailure(err, provider.VolumeBlocked) || attachCalls.Load() != 0 {
			t.Fatalf("invalid Nova attach %s: %v", invalid, err)
		}
	}
	state.Store("available")
	if id, err := client.ApplyVolume(t.Context(), scope, attach); err != nil || id != "req-attach" || attachCalls.Load() != 1 {
		t.Fatalf("attach id=%q error=%v", id, err)
	}
	state.Store("nova-only")
	if reached, err := client.ObserveVolume(t.Context(), scope, attach); reached || err != nil {
		t.Fatalf("one service was treated as sufficient proof: %t %v", reached, err)
	}
	for _, invalid := range []string{"root", "unknown-root", "destructive"} {
		state.Store(invalid)
		if _, err := client.ApplyVolume(t.Context(), scope, detach); !provider.IsFailure(err, provider.VolumeBlocked) || detachCalls.Load() != 0 {
			t.Fatalf("unsafe detach %s: %v", invalid, err)
		}
	}
	state.Store("attached")
	if done, err := client.ObserveVolume(t.Context(), scope, attach); !done || err != nil {
		t.Fatalf("matching attachment was not confirmed: %t %v", done, err)
	}
	if id, err := client.ApplyVolume(t.Context(), scope, detach); err != nil || id != "req-detach" || detachCalls.Load() != 1 {
		t.Fatalf("detach id=%q error=%v", id, err)
	}
	state.Store("not-found")
	if done, err := client.ObserveVolume(t.Context(), scope, detach); done || err == nil {
		t.Fatalf("absence was accepted as detachment: %t %v", done, err)
	}
	state.Store("available")
	if done, err := client.ObserveVolume(t.Context(), scope, detach); !done || err != nil {
		t.Fatalf("expected intact unbound volume: %t %v", done, err)
	}
}
