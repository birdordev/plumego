package openstack

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

func TestOpenStackInventoryProtocol(t *testing.T) {
	var origin string
	var deletes atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("POST /identity/v3/auth/tokens", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Auth struct {
				Identity struct {
					Methods    []string          `json:"methods"`
					Credential map[string]string `json:"application_credential"`
				} `json:"identity"`
			} `json:"auth"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
			len(body.Auth.Identity.Methods) != 1 || body.Auth.Identity.Methods[0] != "application_credential" ||
			body.Auth.Identity.Credential["id"] != "fixture-id" || body.Auth.Identity.Credential["secret"] != "fixture-secret" {
			t.Error("unexpected Keystone request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Subject-Token", "fixture-token")
		w.WriteHeader(http.StatusCreated)
		respond(w, fmt.Sprintf(`{"token":{"project":{"id":"test-project"},"catalog":[
			{"type":"compute","endpoints":[{"region_id":"RegionOne","interface":"public","url":%q}]},
			{"type":"volumev3","endpoints":[{"region_id":"RegionOne","interface":"public","url":%q}]},
			{"type":"network","endpoints":[{"region_id":"RegionOne","interface":"public","url":%q}]},
			{"type":"image","endpoints":[{"region_id":"RegionOne","interface":"public","url":%q},
				{"region_id":"Blocked","interface":"public","url":"https://blocked.invalid/"}]}
		]}}`, origin+"/compute/v2.1", origin+"/volume/v3/test-project", origin+"/network", origin+"/image/v2"), t)
	})
	mux.HandleFunc("GET /compute/v2.1/servers/detail", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-token" || r.URL.Query().Get("limit") != pageLimit {
			t.Error("unexpected Nova request")
		}
		switch r.URL.Query().Get("marker") {
		case "":
			respond(w, `{"servers":[{"id":"first","name":"first vm","status":"ACTIVE"}],
				"servers_links":[{"rel":"next","href":"?marker=first&limit=200"}]}`, t)
		case "first":
			respond(w, `{"servers":[{"id":"second","name":"second vm","status":"SHUTOFF"}]}`, t)
		case "cross-origin":
			respond(w, `{"servers":[],"servers_links":[{"rel":"next","href":"https://other.invalid/compute/v2.1/servers/detail?marker=x"}]}`, t)
		case "cross-path":
			respond(w, `{"servers":[],"servers_links":[{"rel":"next","href":"/compute/v2.1/other?marker=x"}]}`, t)
		default:
			t.Error("unexpected Nova marker")
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	mux.HandleFunc("GET /compute/v2.1/servers/first", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenStack-API-Version") == "compute 2.51" {
			respond(w, `{"server":{"id":"first","tenant_id":"test-project","os-extended-volumes:volumes_attached":[{"id":"volume-1","delete_on_termination":false}]}}`, t)
			return
		}
		respond(w, `{"server":{"id":"first","name":"first vm","status":"ACTIVE","OS-EXT-AZ:availability_zone":"zone-1","image":{"id":"image-1"},"tags":["team=ops"]}}`, t)
	})
	mux.HandleFunc("GET /compute/v2.1/servers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenStack-API-Version") != "compute 2.51" || r.Header.Get("X-Auth-Token") != "fixture-token" {
			t.Error("missing Nova detail microversion or authorization")
		}
		id := r.PathValue("id")
		project, volumes := "test-project", "[]"
		switch id {
		case "foreign":
			project = "foreign-project"
		case "protected":
			volumes = `[{"id":"volume-1","delete_on_termination":false},{"id":"volume-2","delete_on_termination":true}]`
		case "missing-policy":
			volumes = `[{"id":"volume-1"}]`
		case "missing-volume-list":
			volumes = "null"
		default:
			t.Error("server detail requested for an invalid target")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		respond(w, fmt.Sprintf(`{"server":{"id":%q,"tenant_id":%q,"os-extended-volumes:volumes_attached":%s}}`, id, project, volumes), t)
	})
	mux.HandleFunc("DELETE /compute/v2.1/servers/{id}", func(w http.ResponseWriter, r *http.Request) {
		deletes.Add(1)
		if r.PathValue("id") != "first" || r.Header.Get("X-Auth-Token") != "fixture-token" {
			t.Error("Nova delete sent for an unverified instance")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Openstack-Request-Id", "req-delete")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /compute/v2.1/servers/unknown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /compute/v2.1/servers/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirect-target", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("POST /compute/v2.1/servers/first/action", func(w http.ResponseWriter, r *http.Request) {
		var cmd map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil || len(cmd) != 1 || r.Header.Get("X-Auth-Token") != "fixture-token" {
			t.Error("invalid Nova action request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("X-Openstack-Request-Id", "req-fixture-action")
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /compute/v2.1/servers/first/os-instance-actions/{request}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenStack-API-Version") != "compute 2.51" || r.Header.Get("X-Auth-Token") != "fixture-token" {
			t.Error("missing Nova observation microversion or authorization")
		}
		request := r.PathValue("request")
		project, finish, result := "test-project", "2026-11-25T12:30:45.012345", "Success"
		action, event := "reboot", "compute_reboot_instance"
		switch request {
		case "req-delete":
			action, event = "delete", "compute_terminate_instance"
		case "req-pending":
			finish = ""
		case "req-failed":
			result = "Error"
		case "req-wrong-project":
			project = "foreign"
		case "req-malformed-time":
			finish = "bad timestamp"
		case "req-mismatched-id":
			request = "req-other"
		}
		respond(w, fmt.Sprintf(`{"instanceAction":{"action":%q,"instance_uuid":"first","project_id":%q,
			"request_id":%q,"events":[{"event":%q,"finish_time":%q,"result":%q}]}}`, action, project, request, event, finish, result), t)
	})
	mux.HandleFunc("GET /redirect-target", func(w http.ResponseWriter, r *http.Request) {
		t.Error("redirect was followed")
	})
	mux.HandleFunc("GET /compute/v2.1/os-availability-zone", func(w http.ResponseWriter, r *http.Request) {
		respond(w, `{"availabilityZoneInfo":[{"zoneName":"zone-1","zoneState":{"available":true}}]}`, t)
	})
	mux.HandleFunc("GET /volume/v3/test-project/volumes/detail", func(w http.ResponseWriter, r *http.Request) {
		respond(w, `{"volumes":[{"id":"volume-1","name":null,"status":"available"}]}`, t)
	})
	mux.HandleFunc("GET /network/v2.0/networks", func(w http.ResponseWriter, r *http.Request) {
		respond(w, `{"networks":[{"id":"network-1","name":"default","status":"ACTIVE"}]}`, t)
	})
	mux.HandleFunc("GET /network/v2.0/subnets", func(w http.ResponseWriter, r *http.Request) {
		respond(w, `{"subnets":[{"id":"subnet-1","name":"default"}]}`, t)
	})
	mux.HandleFunc("GET /network/v2.0/security-groups", func(w http.ResponseWriter, r *http.Request) {
		respond(w, `{"security_groups":[{"id":"security-1","name":"default"}]}`, t)
	})
	mux.HandleFunc("GET /image/v2/images", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("marker") {
		case "rate":
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			respond(w, `{"images":[{"id":"image-1","name":"image","status":"active"}],
				"next":"/image/v2/images?limit=200&marker=image-1"}`, t)
		}
	})
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	origin = server.URL
	cfg := Config{
		Site: "site", Project: "test-project", IdentityURL: origin + "/identity/v3",
		ApplicationID: "fixture-id", Secret: "fixture-secret", EndpointOrigins: []string{origin},
	}
	client, err := Connect(t.Context(), cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Project = "wrong"
	if _, err := Connect(t.Context(), cfg, server.Client()); !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("mismatched Keystone project accepted: %v", err)
	}
	for _, tc := range []struct {
		kind         provider.Kind
		region, id   string
		cursor, next string
	}{
		{kind: provider.Region, id: "RegionOne"},
		{kind: provider.Zone, region: "RegionOne", id: "zone-1"},
		{kind: provider.Instance, region: "RegionOne", id: "first", next: "first"},
		{kind: provider.Instance, region: "RegionOne", id: "second", cursor: "first"},
		{kind: provider.Volume, region: "RegionOne", id: "volume-1"},
		{kind: provider.Network, region: "RegionOne", id: "network-1"},
		{kind: provider.Subnet, region: "RegionOne", id: "subnet-1"},
		{kind: provider.SecurityGroup, region: "RegionOne", id: "security-1"},
		{kind: provider.Image, region: "RegionOne", id: "image-1", next: "image-1"},
	} {
		t.Run(string(tc.kind)+"/"+tc.id, func(t *testing.T) {
			got, err := client.Discover(t.Context(), provider.Scope{
				Provider: Name, RemoteIdentity: "site:test-project", Region: tc.region, Kind: tc.kind,
			}, tc.cursor)
			if err != nil || len(got.Objects) != 1 || got.Objects[0].ID != tc.id || got.Next != tc.next {
				t.Fatalf("page %+v, error %v", got, err)
			}
		})
	}

	scope := provider.Scope{Provider: Name, RemoteIdentity: client.Identity(), Region: "RegionOne", Kind: provider.Instance}
	for _, cursor := range []string{"cross-origin", "cross-path"} {
		if _, err := client.Discover(t.Context(), scope, cursor); !provider.IsFailure(err, provider.InvalidResponse) {
			t.Fatalf("bad next link accepted: %v", err)
		}
	}
	if _, err := client.Observe(t.Context(), scope, "unknown"); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("404 must remain unknown: %v", err)
	}
	if _, err := client.Observe(t.Context(), scope, "redirect"); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("redirect outcome: %v", err)
	}
	for _, action := range []string{"start", "stop", "reboot"} {
		if receipt, err := client.ApplyInstance(t.Context(), scope, "first", action); err != nil || receipt != "req-fixture-action" {
			t.Fatalf("Nova %s receipt=%q error=%v", action, receipt, err)
		}
	}
	if _, err := client.ApplyInstance(t.Context(), scope, "first", "delete"); !provider.IsFailure(err, provider.DeletionDisabled) || deletes.Load() != 0 {
		t.Fatalf("Nova deletion enabled by default: %v", err)
	}
	cfg.Project, cfg.AllowInstanceDelete = "test-project", true
	client, err = Connect(t.Context(), cfg, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"protected", "missing-policy", "missing-volume-list", "foreign"} {
		if _, err := client.ApplyInstance(t.Context(), scope, id, "delete"); !provider.IsFailure(err, provider.DeletionBlocked) {
			t.Fatalf("unsafe Nova deletion %s: %v", id, err)
		}
	}
	receipt, err := client.ApplyInstance(t.Context(), scope, "first", "delete")
	if err != nil || receipt != "req-delete" || deletes.Load() != 1 {
		t.Fatalf("Nova delete calls=%d receipt=%q error=%v", deletes.Load(), receipt, err)
	}
	if result, err := client.ObserveDeletion(t.Context(), scope, "first", receipt); err != nil || result != provider.ActionSucceeded {
		t.Fatalf("Nova deletion evidence=%q error=%v", result, err)
	}
	if _, err := client.ObserveDeletion(t.Context(), scope, "first", "req-success"); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("Nova reboot evidence confirmed deletion: %v", err)
	}
	for request, want := range map[string]provider.ActionResult{
		"req-success": provider.ActionSucceeded, "req-failed": provider.ActionFailed, "req-pending": provider.ActionPending,
	} {
		if got, err := client.ObserveReboot(t.Context(), scope, "first", request); err != nil || got != want {
			t.Fatalf("Nova reboot %s got %s want %s error=%v", request, got, want, err)
		}
	}
	for _, request := range []string{"req-wrong-project", "req-malformed-time", "req-mismatched-id"} {
		if _, err := client.ObserveReboot(t.Context(), scope, "first", request); !provider.IsFailure(err, provider.InvalidResponse) {
			t.Fatalf("invalid Nova evidence %s: %v", request, err)
		}
	}
	if got, err := client.Observe(t.Context(), scope, "first"); got.ID != "first" || err != nil || len(got.Links) != 2 || got.Links[1].ID != "image-1" || got.Tags["team"] != "ops" {
		t.Fatalf("eventual detail: %+v, %v", got, err)
	}
	scope.Kind = provider.Image
	var failure *provider.Failure
	if _, err := client.Discover(t.Context(), scope, "rate"); !errors.As(err, &failure) || failure.Kind != provider.Throttled || failure.RetryAfter != 5*time.Second {
		t.Fatalf("incorrect rate limit classification: %v", err)
	}
	scope.Region = "Blocked"
	if _, err := client.Discover(t.Context(), scope, ""); !provider.IsFailure(err, provider.Unsupported) {
		t.Fatalf("unapproved endpoint contacted: %v", err)
	}
	scope.RemoteIdentity = "other:test-project"
	if _, err := client.Discover(t.Context(), scope, ""); !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("wrong scope: %v", err)
	}
}

func respond(w http.ResponseWriter, body string, t *testing.T) {
	t.Helper()
	if _, err := fmt.Fprint(w, body); err != nil {
		t.Error("write fixture response")
	}
}
