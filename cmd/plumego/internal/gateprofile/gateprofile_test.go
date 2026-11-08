package gateprofile

import (
	"slices"
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
		{"router alone", []string{"router/dispatch.go"}, "router", false, "router_middleware_security"},
		{"store alone", []string{"store/file/types.go"}, "store", false, "stable_root_change"},
		{"extension alone", []string{"x/tenant/resolver.go"}, "x/tenant", false, "single_module_behavior"},
		{"documentation across roots", []string{"docs/guide.md", "tasks/cards/test.md"}, "docs", true, "docs_only"},
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
			if tt.want == "cross_module_public_api_release" {
				for _, command := range []string{"go test -race -timeout 60s ./...", "go test -timeout 20s ./...", "go vet ./..."} {
					if !slices.Contains(profile.Commands, command) {
						t.Errorf("cross-module profile missing %q", command)
					}
				}
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
	doc.SelectionRules = []SelectionRule{rules["docs_only"], rules["rms_stable"], rules["other_stable"], rules["cross_module"], rules["default"]}
	for _, primary := range []string{"router", "middleware", "security", "core", "contract", "store", "health", "log", "metrics"} {
		t.Run(primary, func(t *testing.T) {
			name, _ := Select(doc, []string{primary + "/changed.go", "x/tenant/changed.go"}, primary, true)
			if name != "cross_module_public_api_release" {
				t.Fatalf("profile = %q, want cross_module_public_api_release", name)
			}
		})
	}
}
