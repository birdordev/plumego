package cloudtest

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAssetClientRejectsInvalidOptions(t *testing.T) {
	for _, args := range [][]string{
		{"get", "tenant", "project"}, {"list", "tenant/other", "project"}, {"list", "tenant", "project", "--kind", "unknown"},
		{"list", "tenant", "project", "--limit", "201"}, {"topology", "tenant", "project", "--limit", "51"},
		{"changes", "tenant", "project", "--cursor", strings.Repeat("a", 1025)},
		{"export", "tenant", "project", "--cursor", "a"}, {"export", "tenant", "project", "--limit", "1"},
	} {
		err := commands.RunAssetsClient(t.Context(), args, "https://fixture.example", "fixture-api-key", io.Discard,
			inventorySampleTransport(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid asset command read a remote endpoint")
				return nil, nil
			}))
		if err == nil {
			t.Fatalf("invalid option accepted: %v", args[:min(len(args), 4)])
		}
	}
}

func TestAssetClientHTTPSContract(t *testing.T) {
	const query = "异常 + 磁盘*&x"
	const token = "fixture-asset-cli-credential-token"
	payload := inventory.Topology{Next: "page-2"}
	for i := range 30 {
		node := inventory.TopologyNode{Asset: inventory.Asset{ID: "vm-" + strconv.Itoa(i), Kind: provider.Instance}}
		for j := range 100 {
			node.Links = append(node.Links, inventory.TopologyLink{Link: provider.Link{Kind: provider.Volume, ID: "disk-" + strconv.Itoa(j), Relation: "uses_volume"},
				Name: strings.Repeat("v", 384)})
		}
		payload.Nodes = append(payload.Nodes, node)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/proxy/v1/tenants/tenant/projects/project/assets/topology" ||
			r.Header.Get("Authorization") != "Bearer "+token || r.URL.Query().Get("q") != query ||
			r.URL.Query().Get("region") != "cn-test" || r.URL.Query().Get("cursor") != "page-token" || r.URL.Query().Get("limit") != "50" {
			t.Error("asset request did not preserve its scope and filters")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(map[string]inventory.Topology{"data": payload}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	args := []string{"topology", "tenant", "project", "--q", query, "--region", "cn-test", "--cursor", "page-token"}
	var output bytes.Buffer
	if !cloudmanager.ValidCommand(append([]string{"assets"}, args...)) {
		t.Fatal("asset command is not available from the main program")
	}
	if err := commands.RunAssetsClient(t.Context(), args, server.URL+"/proxy", token, &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var topology inventory.Topology
	if err := json.Unmarshal(output.Bytes(), &topology); err != nil || output.Len() <= (1<<20) || topology.Next != "page-2" || len(topology.Nodes) != 30 {
		t.Fatalf("asset response was truncated: %v", err)
	}
}

func checkProjectAssetCLI(t *testing.T, handler http.Handler, token string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	read := func(command, project string, flags ...string) []byte {
		t.Helper()
		var output bytes.Buffer
		args := append([]string{command, "tenant", project}, flags...)
		if err := commands.RunAssetsClient(t.Context(), args, server.URL, token, &output, server.Client().Transport); err != nil {
			t.Fatal(err)
		}
		return output.Bytes()
	}
	var first, next inventory.Assets
	flags := []string{"--kind", "instance", "--region", "us-east-1", "--limit", "1"}
	if err := json.Unmarshal(read("list", "shared", flags...), &first); err != nil || len(first.Items) != 1 || first.Next == "" {
		t.Fatalf("missing first shared page: %+v %v", first, err)
	}
	if err := json.Unmarshal(read("list", "shared", append(flags, "--cursor", first.Next)...), &next); err != nil ||
		len(next.Items) != 1 || next.Items[0].ID == first.Items[0].ID || next.Items[0].Account != "owned" {
		t.Fatalf("wrong continuation: %+v %v", next, err)
	}
	var history inventory.Changes
	if err := json.Unmarshal(read("changes", "project", "--kind", "instance", "--q", "one", "--limit", "1"), &history); err != nil ||
		len(history.Items) != 1 || history.Items[0].Action != "changed" || history.Next == "" {
		t.Fatalf("wrong history page: %+v %v", history, err)
	}
	var topology inventory.Topology
	if err := json.Unmarshal(read("topology", "shared", "--kind", "instance", "--q", "1+1"), &topology); err != nil ||
		len(topology.Nodes) != 1 || len(topology.Nodes[0].Links) != 1 || topology.Nodes[0].Links[0].ID != "target" {
		t.Fatalf("hidden edge was returned or the visible edge is missing: %+v %v", topology, err)
	}
	rows, err := csv.NewReader(bytes.NewReader(read("export", "shared", "--kind", "instance", "--q", "1+1"))).ReadAll()
	if err != nil || len(rows) != 2 || rows[1][4] != "' =1+1" {
		t.Fatalf("export lost the exact authorized CSV: %+v %v", rows, err)
	}
	for _, probe := range []struct {
		args []string
		code string
	}{
		{[]string{"export", "tenant", "shared"}, "422"},
		{[]string{"list", "other", "project"}, "403"},
		{[]string{"list", "tenant", "shared", "--kind", "volume", "--cursor", first.Next}, "400"},
	} {
		var output bytes.Buffer
		err := commands.RunAssetsClient(t.Context(), probe.args, server.URL, token, &output, server.Client().Transport)
		if err == nil || !strings.Contains(err.Error(), "HTTP "+probe.code) || output.Len() != 0 {
			t.Fatalf("HTTP error yielded a partial or ambiguous result: %v", err)
		}
	}
}
