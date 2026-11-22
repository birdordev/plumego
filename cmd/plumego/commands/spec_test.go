package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
}

func specTestAppDir() string {
	repoRoot := filepath.Clean("../../..")
	return filepath.Join(repoRoot, "reference", "standard-service")
}
