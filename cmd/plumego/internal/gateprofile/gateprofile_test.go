package gateprofile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSelectRepositoryProfiles(t *testing.T) {
	doc, err := Load("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		files   []string
		primary string
		multi   bool
		want    string
	}{
		{"gate configuration", []string{"specs/gate-profiles.yaml"}, "specs", false, "cross_module_public_api_release"},
		{"hook alone", []string{".githooks/pre-push"}, ".githooks", false, "cross_module_public_api_release"},
		{"cli alone", []string{"cmd/plumego/commands/agents.go"}, "cmd", false, "cross_module_public_api_release"},
		{"website script", []string{"website/scripts/check.mjs"}, "website", false, "website_only"},
		{"website content", []string{"website/src/content/docs/index.mdx"}, "website", false, "website_only"},
		{"reference alone", []string{"reference/standard-service/main.go"}, "reference/standard-service", false, "single_module_behavior"},
		{"router alone", []string{"router/dispatch.go"}, "router", false, "stable_root_change"},
		{"store alone", []string{"store/file/types.go"}, "store", false, "stable_root_change"},
		{"extension alone", []string{"x/tenant/resolver.go"}, "x/tenant", false, "single_module_behavior"},
		{"documentation across roots", []string{"docs/guide.md", "tasks/cards/test.md"}, "docs", true, "docs_only"},
		{"documentation source", []string{"docs/examples/main.go"}, "docs", false, "cross_module_public_api_release"},
		{"router and security", []string{"router/dispatch.go", "security/jwt/keys.go"}, "router", true, "cross_module_public_api_release"},
		{"core and router", []string{"core/app.go", "router/dispatch.go"}, "core", true, "cross_module_public_api_release"},
		{"store and extension", []string{"store/file/types.go", "x/tenant/resolver.go"}, "store", true, "cross_module_public_api_release"},
		{"extensions", []string{"x/tenant/resolver.go", "x/gateway/proxy.go"}, "x/tenant", true, "cross_module_public_api_release"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, profile := Select(doc, tt.files, tt.primary, tt.multi)
			if name != tt.want {
				t.Fatalf("profile = %q, want %q", name, tt.want)
			}
			if tt.want == "cross_module_public_api_release" && !slices.Equal(profile.Commands, []string{"make gates"}) {
				t.Errorf("cross-module commands = %v, want [make gates]", profile.Commands)
			}
		})
	}
}

func TestSelectMultiModuleSkipsSingleModuleRules(t *testing.T) {
	doc, err := Load("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce the original ordering independently of the repository configuration.
	rules := make(map[string]SelectionRule)
	for _, rule := range doc.SelectionRules {
		rules[rule.ID] = rule
	}
	doc.SelectionRules = []SelectionRule{rules["docs_only"], rules["stable_root"], rules["cross_module"], rules["default"]}
	for _, primary := range []string{"router", "middleware", "security", "core", "contract", "store", "health", "log", "metrics"} {
		t.Run(primary, func(t *testing.T) {
			name, _ := Select(doc, []string{primary + "/changed.go", "x/tenant/changed.go"}, primary, true)
			if name != "cross_module_public_api_release" {
				t.Fatalf("profile = %q, want cross_module_public_api_release", name)
			}
		})
	}
}

func TestLoadRejectsIncompleteProfiles(t *testing.T) {
	for name, config := range map[string]string{
		"missing rules":   "profiles: {}\n",
		"missing profile": "selection_rules:\n  - id: default\n    use: missing\n",
		"empty commands":  "profiles:\n  single_module_behavior: {}\nselection_rules:\n  - id: default\n    use: single_module_behavior\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "specs"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "specs/gate-profiles.yaml"), []byte(config), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatal("incomplete validation configuration was accepted")
			}
		})
	}
}

func TestNestedModuleCommandsRunInsideModule(t *testing.T) {
	doc, err := Load("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	_, profile := Select(doc, []string{"reference/standard-service/main.go"}, "reference/standard-service", false)
	for _, command := range profile.Commands {
		if !strings.HasPrefix(command, "go -C reference/standard-service ") {
			t.Fatalf("invalid nested module command: %q", command)
		}
	}
}

func TestMixedWebsiteChangesKeepBothGateFamilies(t *testing.T) {
	doc, err := Load("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	name, profile := Select(doc, []string{"router/dispatch.go", "website/scripts/check.mjs"}, "router", true)
	if name != "cross_module_public_api_release" || !slices.Equal(profile.Commands, []string{"make gates", "make website-gates"}) {
		t.Fatalf("mixed diff omitted a gate family: %s %v", name, profile.Commands)
	}
	if slices.Contains(doc.Profiles[name].Commands, "make website-gates") {
		t.Fatal("selection mutated shared profile commands")
	}
}
