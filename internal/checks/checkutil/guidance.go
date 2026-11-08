package checkutil

import (
	"fmt"
	"strings"
)

// FormatViolations formats violations with structured fix guidance.
// Each violation is annotated with FIX, RULE, and GUIDE fields where applicable.
func FormatViolations(checkName string, violations []string) string {
	if len(violations) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s check failed: %d violation(s)\n", checkName, len(violations))
	for _, v := range violations {
		fmt.Fprintf(&b, "\n%s\n", formatSingleViolation(checkName, v))
	}
	return b.String()
}

func formatSingleViolation(checkName, violation string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  ❌  %s\n", violation)

	guidance := annotate(checkName, violation)
	if guidance.fix != "" {
		fmt.Fprintf(&b, "\n     FIX:   %s\n", guidance.fix)
	}
	if guidance.rule != "" {
		fmt.Fprintf(&b, "     RULE:  %s\n", guidance.rule)
	}
	if guidance.guide != "" {
		fmt.Fprintf(&b, "     GUIDE: %s\n", guidance.guide)
	}
	return b.String()
}

type annotation struct {
	fix   string
	rule  string
	guide string
}

func annotate(checkName, violation string) annotation {
	switch checkName {
	case "dependency-rules":
		return annotateDepRules(violation)
	case "module-manifests":
		return annotateModuleManifests(violation)
	case "agent-workflow":
		return annotateAgentWorkflow(violation)
	case "reference-layout":
		return annotateReferenceLayout(violation)
	case "public-entrypoints-sync":
		return annotatePublicEntrypointsSync(violation)
	default:
		return annotation{}
	}
}

func annotateDepRules(v string) annotation {
	// Format: "<relPath>|<importPath>"
	if strings.Contains(v, "|") {
		parts := strings.SplitN(v, "|", 2)
		file := parts[0]
		imp := parts[1]

		if strings.Contains(imp, "/x/") {
			// Stable root importing extension package
			pkg := strings.TrimPrefix(imp, "github.com/birdordev/plumego/")
			module := strings.Split(file, "/")[0]
			return annotation{
				fix:   fmt.Sprintf("Move code using %s out of %s. Options: (1) pass behavior via constructor injection, (2) extract the interface to contract/, (3) move the feature to the owning x/* package.", pkg, module),
				rule:  "specs/dependency-rules.yaml — stable_must_not_depend_on_extension",
				guide: "docs/concepts/core-boundary.md",
			}
		}
		if strings.HasPrefix(imp, "github.com/birdordev/plumego") && strings.Contains(imp, "reference/") {
			return annotation{
				fix:   "Library packages must not import reference apps. Reference apps are application wiring examples, not library dependencies.",
				rule:  "specs/dependency-rules.yaml — library_must_not_depend_on_reference",
				guide: "docs/concepts/agent-first-repo-blueprint.md",
			}
		}
		if imp == "github.com/birdordev/plumego" {
			return annotation{
				fix:   "Import the specific sub-package (e.g. github.com/birdordev/plumego/contract) rather than the root module path.",
				rule:  "specs/dependency-rules.yaml — forbidden_import_patterns",
				guide: "AGENTS.md",
			}
		}
		return annotation{
			fix:   fmt.Sprintf("Check the allow/deny list for your module in specs/dependency-rules.yaml and remove or relocate the import of %s.", imp),
			rule:  "specs/dependency-rules.yaml — modules.<your-module>.deny",
			guide: "docs/concepts/core-boundary.md",
		}
	}

	// Format: "<manifest>: allowed_imports entry <x> is denied by..."
	if strings.Contains(v, "allowed_imports entry") && strings.Contains(v, "is denied") {
		return annotation{
			fix:   "The module.yaml allowed_imports field lists a package that specs/dependency-rules.yaml denies. Remove the entry from allowed_imports or update the deny rule with approval.",
			rule:  "specs/dependency-rules.yaml — modules.<module>.deny",
			guide: "docs/concepts/core-boundary.md",
		}
	}

	// Format: "specs/dependency-rules.yaml module <x> allows <y> but <manifest> forbids it"
	if strings.Contains(v, "allows") && strings.Contains(v, "but") && strings.Contains(v, "forbids") {
		return annotation{
			fix:   "The module.yaml forbidden_imports field conflicts with the allow list in specs/dependency-rules.yaml. Align both files: either remove from forbidden_imports or remove from allow.",
			rule:  "specs/dependency-rules.yaml",
			guide: "docs/concepts/core-boundary.md",
		}
	}

	// Format: "FORBIDDEN_PATH|<path>"
	if strings.HasPrefix(v, "FORBIDDEN_PATH|") {
		path := strings.TrimPrefix(v, "FORBIDDEN_PATH|")
		return annotation{
			fix:   fmt.Sprintf("Remove or relocate %s. Forbidden paths are listed in specs/dependency-rules.yaml special_rules.forbidden_paths.", path),
			rule:  "specs/dependency-rules.yaml — special_rules.forbidden_paths",
			guide: "AGENTS.md",
		}
	}

	return annotation{}
}

func annotateModuleManifests(v string) annotation {
	if strings.Contains(v, "missing required key") {
		key := extractQuoted(v)
		return annotation{
			fix:   fmt.Sprintf("Add the required field %q to your module.yaml. See specs/module-manifest.schema.yaml for the full list of required fields and allowed values.", key),
			rule:  "specs/module-manifest.schema.yaml — required",
			guide: "AGENTS.md — module manifest contract",
		}
	}
	if strings.Contains(v, "required list") && strings.Contains(v, "must not be empty") {
		key := extractQuoted(v)
		return annotation{
			fix:   fmt.Sprintf("The %q list in module.yaml must have at least one entry. Add the appropriate value(s).", key),
			rule:  "specs/module-manifest.schema.yaml — required lists",
			guide: "AGENTS.md",
		}
	}
	if strings.Contains(v, "list") && strings.Contains(v, "exceeds limit") {
		key := extractQuoted(v)
		return annotation{
			fix:   fmt.Sprintf("The %q list in module.yaml has too many entries. Consolidate entries to stay within the schema limit. See specs/module-manifest.schema.yaml for the max.", key),
			rule:  "specs/module-manifest.schema.yaml — limits",
			guide: "AGENTS.md",
		}
	}
	if strings.Contains(v, "invalid") && strings.Contains(v, "layer") {
		return annotation{
			fix:   "Set layer to one of: stable, extension, tooling, reference.",
			rule:  "specs/module-manifest.schema.yaml — enums.layer",
			guide: "AGENTS.md",
		}
	}
	if strings.Contains(v, "invalid") && strings.Contains(v, "status") {
		return annotation{
			fix:   "Set status to one of: ga, beta, experimental.",
			rule:  "specs/module-manifest.schema.yaml — enums.status",
			guide: "AGENTS.md",
		}
	}
	if strings.Contains(v, "doc_paths target") && strings.Contains(v, "does not exist") {
		return annotation{
			fix:   "Create the documentation file referenced in doc_paths, or update doc_paths to point to an existing file.",
			rule:  "specs/module-manifest.schema.yaml",
			guide: "docs/operations/codex-workflow.md — docs sync",
		}
	}
	if strings.Contains(v, "missing strict_boundary") {
		return annotation{
			fix:   "Add strict_boundary: <boundary-name> to the stable root module.yaml. Examples: kernel, transport_contracts, route_matching, transport_middleware.",
			rule:  "specs/module-manifest.schema.yaml",
			guide: "docs/concepts/core-boundary.md",
		}
	}
	if strings.Contains(v, "missing module.yaml") {
		return annotation{
			fix:   "Create a module.yaml in the package directory. Copy from an existing module.yaml and fill in the required fields listed in specs/module-manifest.schema.yaml.",
			rule:  "specs/module-manifest.schema.yaml — required",
			guide: "AGENTS.md",
		}
	}
	return annotation{}
}

func annotateAgentWorkflow(v string) annotation {
	if strings.Contains(v, "exists in x/ but is not declared in specs/repo.yaml") {
		pkg := extractExtensionRoot(v)
		return annotation{
			fix:   fmt.Sprintf("Add %q to the paths list under layers.extension in specs/repo.yaml, OR remove the directory if it was created by mistake.", pkg),
			rule:  "specs/repo.yaml — layers.extension.paths",
			guide: "docs/concepts/agent-first-repo-blueprint.md",
		}
	}
	if strings.Contains(v, "references missing start_with path") {
		return annotation{
			fix:   "Create the missing file referenced in start_with, or update specs/task-routing.yaml to point to an existing path.",
			rule:  "specs/task-routing.yaml",
			guide: "docs/operations/codex-workflow.md",
		}
	}
	if strings.Contains(v, "must declare at least one start_with path") {
		return annotation{
			fix:   "Add a start_with list to the task entry in specs/task-routing.yaml with at least one valid file path.",
			rule:  "specs/task-routing.yaml",
			guide: "docs/operations/codex-workflow.md",
		}
	}
	if strings.Contains(v, "does not reference workflow recipe") {
		return annotation{
			fix:   "Add the recipe path to the change_recipes list in specs/repo.yaml.",
			rule:  "specs/repo.yaml — change_recipes",
			guide: "docs/operations/codex-workflow.md",
		}
	}
	if strings.Contains(v, "empty directory") {
		return annotation{
			fix:   "Remove the empty directory, or add real package contents (at minimum a doc.go or a .go source file).",
			rule:  "AGENTS.md",
			guide: "docs/concepts/agent-first-repo-blueprint.md",
		}
	}
	if strings.Contains(v, "primary family must declare subordinate_families") {
		return annotation{
			fix:   "Add a subordinate_families list to the primary family module.yaml listing its subordinate package paths.",
			rule:  "specs/extension-taxonomy.yaml",
			guide: "docs/concepts/extension-boundary.md",
		}
	}
	return annotation{}
}

func annotateReferenceLayout(v string) annotation {
	if strings.Contains(v, "canonical reference imports x/*") {
		imp := ""
		if idx := strings.LastIndex(v, "|"); idx >= 0 {
			imp = v[idx+1:]
		}
		return annotation{
			fix:   fmt.Sprintf("Remove the import of %q from reference/standard-service. The canonical reference app must depend only on stable roots and stdlib. Move this usage to a scenario reference app (reference/with-*) or to the owning x/* package.", imp),
			rule:  "specs/dependency-rules.yaml — library_must_not_depend_on_reference / reference app purity",
			guide: "docs/concepts/agent-first-repo-blueprint.md",
		}
	}
	if strings.Contains(v, "missing required path") {
		path := strings.TrimPrefix(v, "missing required path ")
		return annotation{
			fix:   fmt.Sprintf("Create the required file or directory: %s", path),
			rule:  "internal/checks/reference-layout",
			guide: "docs/concepts/agent-first-repo-blueprint.md",
		}
	}
	if strings.Contains(v, "x/* taxonomy") {
		return annotation{
			fix:   "Check that x/* module.yaml files correctly declare subordinate_families (primary) and parent_family (subordinate). See specs/extension-taxonomy.yaml.",
			rule:  "specs/extension-taxonomy.yaml",
			guide: "docs/concepts/extension-boundary.md",
		}
	}
	return annotation{}
}

// extractQuoted returns the first double-quoted string from s.
func extractQuoted(s string) string {
	start := strings.Index(s, `"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(s[start+1:], `"`)
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}

func annotatePublicEntrypointsSync(v string) annotation {
	if strings.Contains(v, "not found in") {
		return annotation{
			fix:   "Either remove the stale entry from module.yaml public_entrypoints, or ensure the exported symbol exists in the module source. If the symbol was renamed, update both source and module.yaml in the same PR.",
			rule:  "AGENTS.md — Symbol Change Protocol: update module.yaml when removing or renaming exported symbols",
			guide: "docs/reference/canonical-style-guide.md",
		}
	}
	return annotation{}
}

// extractExtensionRoot returns the x/* path from a violation about an orphaned root.
func extractExtensionRoot(v string) string {
	// "extension root x/foo exists in x/ but..."
	v = strings.TrimPrefix(v, "extension root ")
	if idx := strings.Index(v, " exists"); idx >= 0 {
		return v[:idx]
	}
	return ""
}
