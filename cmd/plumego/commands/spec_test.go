package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCLI_GenerateSpecWritesJSONAndYAML(t *testing.T) {
	appDir := specTestAppDir()
	tmpDir := t.TempDir()

	jsonPath := filepath.Join(tmpDir, "openapi.json")
	stdout, _, err := runCLI(t, []string{
		"generate", "spec",
		"--dir", appDir,
		"--output", jsonPath,
		"--format", "json",
	}, "")
	if err != nil {
		t.Fatalf("generate json spec failed: %v\noutput: %s", err, stdout)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json spec: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("generated spec is not JSON: %s", data)
	}
	if !strings.Contains(string(data), `"/api/hello"`) {
		t.Fatalf("generated spec missing standard-service route: %s", data)
	}

	yamlPath := filepath.Join(tmpDir, "openapi.yaml")
	stdout, _, err = runCLI(t, []string{
		"generate", "spec",
		"--dir", appDir,
		"--output", yamlPath,
		"--format", "yaml",
		"--strict",
	}, "")
	if err != nil {
		t.Fatalf("generate yaml spec failed: %v\noutput: %s", err, stdout)
	}
	yamlData, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read yaml spec: %v", err)
	}
	yamlOut := string(yamlData)
	if !strings.Contains(yamlOut, `openapi: "3.1.0"`) || !strings.Contains(yamlOut, "/api/hello:") ||
		!strings.Contains(yamlOut, "securitySchemes:") || !strings.Contains(yamlOut, "Item deleted") {
		t.Fatalf("generated spec is not expected YAML: %s", yamlOut)
	}
	if runtime.GOOS != "windows" && !testing.Short() {
		verifyStandardServiceContract(t, appDir, tmpDir, data, yamlData)
	}

}

func specTestAppDir() string {
	repoRoot := filepath.Clean("../../..")
	return filepath.Join(repoRoot, "reference", "standard-service")
}

func verifyStandardServiceContract(t *testing.T, appDir, tmpDir string, jsonData, yamlData []byte) {
	t.Helper()
	type operation struct {
		Responses map[string]any        `json:"responses" yaml:"responses"`
		Security  []map[string][]string `json:"security" yaml:"security"`
	}
	type document struct {
		Paths      map[string]map[string]operation `json:"paths" yaml:"paths"`
		Components struct {
			SecuritySchemes map[string]map[string]string `json:"securitySchemes" yaml:"securitySchemes"`
		} `json:"components" yaml:"components"`
	}
	var jsonSpec, yamlSpec document
	if err := json.Unmarshal(jsonData, &jsonSpec); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(yamlData, &yamlSpec); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []document{jsonSpec, yamlSpec} {
		scheme := spec.Components.SecuritySchemes["writeKey"]
		if scheme["type"] != "apiKey" || scheme["in"] != "header" || scheme["name"] != "X-Write-Key" {
			t.Fatalf("incorrect write-key declaration: %v", scheme)
		}
	}

	binary := filepath.Join(tmpDir, "standard-service")
	runGoCommand(t, appDir, "build", "-o", binary, ".")
	t.Setenv("APP_WRITE_KEY", "fixture-write-key")
	t.Setenv("APP_TLS_ENABLED", "false")
	t.Setenv("APP_STRICT_JSON", "true")
	ctx, client, base, shutdown := startSmokeServer(t, binary, appDir)
	const collection = "/api/v1/items"
	const payload = `{"name":"fixture","description":"contract smoke test"}`
	do := func(method, path, template, body string, authorized bool, status int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if authorized {
			req.Header.Set("X-Write-Key", "fixture-write-key")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		data, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != status || (status == http.StatusNoContent && len(data) != 0) {
			t.Fatalf("%s %s: status=%d body=%q err=%v", method, path, resp.StatusCode, data, err)
		}
		for _, spec := range []document{jsonSpec, yamlSpec} {
			op := spec.Paths[template][strings.ToLower(method)]
			if _, ok := op.Responses[strconv.Itoa(resp.StatusCode)]; !ok {
				t.Fatalf("%s %s does not declare status %d", method, template, resp.StatusCode)
			}
			if method != http.MethodGet {
				if len(op.Security) != 1 {
					t.Fatalf("%s %s does not require the write key", method, template)
				}
				if _, ok := op.Security[0]["writeKey"]; !ok {
					t.Fatalf("%s %s has wrong security requirements: %v", method, template, op.Security)
				}
			}
		}
		return data
	}
	do(http.MethodPost, collection, collection, payload, false, http.StatusUnauthorized)
	created := do(http.MethodPost, collection, collection, payload, true, http.StatusCreated)
	var item struct {
		Data struct{ ID string }
	}
	if err := json.Unmarshal(created, &item); err != nil || item.Data.ID == "" {
		t.Fatalf("create response did not contain an ID: %v", err)
	}
	member := collection + "/" + item.Data.ID
	template := collection + "/{id}"
	do(http.MethodGet, member, template, "", false, http.StatusOK)
	do(http.MethodPut, member, template, payload, true, http.StatusOK)
	do(http.MethodPatch, member, template, `{"name":"updated"}`, true, http.StatusOK)
	do(http.MethodDelete, member, template, "", true, http.StatusNoContent)
	do(http.MethodGet, member, template, "", false, http.StatusNotFound)
	shutdown(nil)
}

func TestSpecHelperUsesTargetModuleResolution(t *testing.T) {
	const plumego = "github.com/birdordev/plumego"
	app := "/fixtures/project with spaces"
	target := specTarget{ModulePath: "example.com/project"}
	modules := []goModuleInfo{
		{Path: plumego, Version: "v1.2.3"},
		{Path: "example.com/original", Version: "v1.0.0",
			Replace: &goModuleInfo{Path: "example.com/fork", Version: "v1.0.1"}},
		{Path: "example.com/local", Replace: &goModuleInfo{Dir: "/fixtures/local checkout"}},
	}
	got := specHelperGoMod(app, target, modules)
	for _, want := range []string{
		plumego + " v1.2.3",
		`replace example.com/project => "/fixtures/project with spaces"`,
		"replace example.com/original => example.com/fork v1.0.1",
		`replace example.com/local => "/fixtures/local checkout"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("helper go.mod is missing %q:\n%s", want, got)
		}
	}
}
