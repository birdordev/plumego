package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spcent/plumego/internal/checks/checkutil"
)

const requiredReleaseCount = 2

var knownBlockers = map[string]struct{}{
	"release_history_missing": {},
	"api_snapshot_missing":    {},
	"owner_signoff_missing":   {},
}

type candidate struct {
	Module        string
	Subpackage    string
	Surface       string
	Package       string
	Owner         string
	CurrentStatus string
	CurrentTier   string
	EvidenceDoc   string
	ReleaseRefs   []string
	APISnapshots  []string
	OwnerSignoff  string
	Blockers      []string
}

type moduleManifest struct {
	Name   string
	Owner  string
	Status string
	Tiers  map[string][]string
}

func main() {
	repoRoot, err := os.Getwd()
	if err != nil {
		failf("resolve working directory: %v", err)
	}

	report, violations, err := run(repoRoot)
	if err != nil {
		failf("run extension beta evidence check: %v", err)
	}
	for _, line := range report {
		fmt.Println(line)
	}
	if len(violations) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "extension-beta-evidence check failed:")
	for _, violation := range violations {
		fmt.Fprintf(os.Stderr, "- %s\n", violation)
	}
	os.Exit(1)
}

func run(repoRoot string) ([]string, []string, error) {
	roots, err := readEvidenceCandidateRoots(repoRoot)
	if err != nil {
		return nil, nil, err
	}

	candidates, err := readCandidates(filepath.Join(repoRoot, "specs", "extension-beta-evidence.yaml"))
	if err != nil {
		return nil, nil, err
	}
	if len(candidates) == 0 {
		return nil, []string{"specs/extension-beta-evidence.yaml has no candidates"}, nil
	}

	var report []string
	var violations []string
	for _, cand := range candidates {
		candViolations, err := validateCandidate(repoRoot, roots, cand)
		if err != nil {
			return nil, nil, err
		}
		violations = append(violations, candViolations...)
		report = append(report, candidateReport(cand))
	}
	sort.Strings(report)
	sort.Strings(violations)
	return report, violations, nil
}

func readCandidates(path string) ([]candidate, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var candidates []candidate
	var current *candidate
	var currentList string
	section := ""

	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if indent == 0 {
			switch trimmed {
			case "candidates:":
				section = "module"
				continue
			case "subpackage_candidates:":
				if current != nil {
					candidates = append(candidates, *current)
					current = nil
				}
				section = "subpackage"
				continue
			case "surface_candidates:":
				if current != nil {
					candidates = append(candidates, *current)
					current = nil
				}
				section = "surface"
				continue
			default:
				if section != "" {
					break
				}
			}
		}
		if section == "" {
			continue
		}

		if indent == 2 && strings.HasPrefix(trimmed, "- module:") {
			if current != nil {
				candidates = append(candidates, *current)
			}
			current = &candidate{Module: yamlScalar(trimmed)}
			currentList = ""
			continue
		}
		if current == nil {
			continue
		}

		if indent == 4 {
			key, value, ok := strings.Cut(trimmed, ":")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			currentList = ""
			switch key {
			case "surface":
				current.Surface = trimYAMLValue(value)
			case "package":
				current.Package = trimYAMLValue(value)
			case "subpackage":
				current.Subpackage = trimYAMLValue(value)
			case "owner":
				current.Owner = trimYAMLValue(value)
			case "current_status":
				current.CurrentStatus = trimYAMLValue(value)
			case "current_tier":
				current.CurrentTier = trimYAMLValue(value)
			case "evidence_doc":
				current.EvidenceDoc = trimYAMLValue(value)
			case "owner_signoff":
				current.OwnerSignoff = trimYAMLValue(value)
			case "release_refs":
				current.ReleaseRefs = parseInlineList(value)
				if value == "" {
					currentList = key
				}
			case "api_snapshots":
				current.APISnapshots = parseInlineList(value)
				if value == "" {
					currentList = key
				}
			case "blockers":
				current.Blockers = parseInlineList(value)
				if value == "" {
					currentList = key
				}
			}
			continue
		}

		if indent == 6 && currentList != "" && strings.HasPrefix(trimmed, "- ") {
			value := trimYAMLValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")))
			switch currentList {
			case "release_refs":
				current.ReleaseRefs = append(current.ReleaseRefs, value)
			case "api_snapshots":
				current.APISnapshots = append(current.APISnapshots, value)
			case "blockers":
				current.Blockers = append(current.Blockers, value)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		candidates = append(candidates, *current)
	}
	return candidates, nil
}

func validateCandidate(repoRoot string, roots map[string]struct{}, cand candidate) ([]string, error) {
	var violations []string
	label := cand.Label()
	if cand.Module == "" {
		return []string{"candidate missing module"}, nil
	}
	if _, ok := roots[cand.Module]; !ok {
		violations = append(violations, fmt.Sprintf("%s is not declared in specs/repo.yaml or specs/extension-taxonomy.yaml extension roots", label))
	}

	manifest, err := readModuleManifest(filepath.Join(repoRoot, cand.Module, "module.yaml"))
	if err != nil {
		return nil, err
	}
	if manifest.Name != cand.Module {
		violations = append(violations, fmt.Sprintf("%s module.yaml name %q does not match evidence module", label, manifest.Name))
	}
	if cand.Owner == "" {
		violations = append(violations, fmt.Sprintf("%s missing owner", label))
	} else if manifest.Owner != cand.Owner {
		violations = append(violations, fmt.Sprintf("%s owner mismatch: evidence %q, module.yaml %q", label, cand.Owner, manifest.Owner))
	}
	if cand.Surface != "" {
		if cand.Package == "" {
			violations = append(violations, fmt.Sprintf("%s missing package", label))
		} else {
			pkgPath := filepath.Join(repoRoot, filepath.FromSlash(cand.Package))
			if info, err := os.Stat(pkgPath); err != nil {
				if os.IsNotExist(err) {
					violations = append(violations, fmt.Sprintf("%s package does not exist: %s", label, cand.Package))
				} else {
					return nil, err
				}
			} else if !info.IsDir() {
				violations = append(violations, fmt.Sprintf("%s package is not a directory: %s", label, cand.Package))
			}
			if !strings.HasPrefix(filepath.ToSlash(cand.Package), cand.Module) {
				violations = append(violations, fmt.Sprintf("%s package %q is outside module %q", label, cand.Package, cand.Module))
			}
		}
		if cand.CurrentStatus == "" {
			violations = append(violations, fmt.Sprintf("%s missing current_status", label))
		} else {
			// When the package path differs from the module root and has its own
			// module.yaml, validate current_status against the package manifest.
			// This covers promoted sub-packages (e.g. x/data/file within x/data).
			statusManifest := manifest
			if cand.Package != "" && cand.Package != cand.Module {
				pkgManifestPath := filepath.Join(repoRoot, filepath.FromSlash(cand.Package), "module.yaml")
				if pkgManifest, pkgErr := readModuleManifest(pkgManifestPath); pkgErr == nil {
					statusManifest = pkgManifest
				} else if !os.IsNotExist(pkgErr) {
					return nil, pkgErr
				}
			}
			if statusManifest.Status != cand.CurrentStatus {
				violations = append(violations, fmt.Sprintf("%s status mismatch: evidence %q, module.yaml %q", label, cand.CurrentStatus, statusManifest.Status))
			}
		}
	} else if cand.Subpackage == "" {
		if cand.CurrentStatus == "" {
			violations = append(violations, fmt.Sprintf("%s missing current_status", label))
		} else if manifest.Status != cand.CurrentStatus {
			violations = append(violations, fmt.Sprintf("%s status mismatch: evidence %q, module.yaml %q", label, cand.CurrentStatus, manifest.Status))
		}
	} else {
		if cand.CurrentTier == "" {
			violations = append(violations, fmt.Sprintf("%s missing current_tier", label))
		} else if !manifest.HasTier(cand.CurrentTier, cand.Subpackage) {
			violations = append(violations, fmt.Sprintf("%s is not listed under module.yaml stability_tiers.%s", label, cand.CurrentTier))
		}
	}
	if cand.EvidenceDoc == "" {
		violations = append(violations, fmt.Sprintf("%s missing evidence_doc", label))
	} else if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(cand.EvidenceDoc))); err != nil {
		if os.IsNotExist(err) {
			violations = append(violations, fmt.Sprintf("%s evidence_doc does not exist: %s", label, cand.EvidenceDoc))
		} else {
			return nil, err
		}
	}
	violations = append(violations, releaseRefViolations(repoRoot, cand)...)
	violations = append(violations, apiSnapshotViolations(repoRoot, cand)...)

	violations = append(violations, blockerViolations(cand)...)
	return violations, nil
}

func readEvidenceCandidateRoots(repoRoot string) (map[string]struct{}, error) {
	roots, err := checkutil.ReadRepoExtensionRoots(repoRoot)
	if err != nil {
		return nil, err
	}
	taxonomy, err := checkutil.ReadExtensionTaxonomy(repoRoot)
	if err != nil {
		return nil, err
	}
	for root := range taxonomy.RootToCanonical {
		roots[root] = struct{}{}
	}
	return roots, nil
}

func releaseRefViolations(repoRoot string, cand candidate) []string {
	var violations []string
	for _, ref := range cand.ReleaseRefs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if commitResolves(repoRoot, ref) {
			continue
		}
		// On a shallow checkout (CI, remote execution) the older baseline ref
		// may sit outside the fetched history. Attempt a best-effort, depth-1
		// fetch of just this commit before declaring it unresolved, so the gate
		// stays meaningful on shallow clones without being weakened: a full
		// clone resolves immediately and never reaches this path.
		if isShallowRepo(repoRoot) {
			fetchCommit(repoRoot, ref)
			if commitResolves(repoRoot, ref) {
				continue
			}
		}
		violations = append(violations, fmt.Sprintf("%s release_ref does not resolve to a commit: %s", cand.Label(), ref))
	}
	return violations
}

// commitResolves reports whether ref names a commit object in the local repo.
func commitResolves(repoRoot, ref string) bool {
	return exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}").Run() == nil
}

// isShallowRepo reports whether the repository is a shallow clone.
func isShallowRepo(repoRoot string) bool {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--is-shallow-repository").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// fetchCommit best-effort fetches a single commit by SHA from origin. Errors
// (offline, ref absent on origin) are ignored; the caller re-verifies and
// reports a violation if the commit is still missing afterwards.
func fetchCommit(repoRoot, ref string) {
	_ = exec.Command("git", "-C", repoRoot, "fetch", "--depth=1", "origin", ref).Run()
}

func apiSnapshotViolations(repoRoot string, cand candidate) []string {
	var violations []string
	for _, snapshot := range cand.APISnapshots {
		snapshot = filepath.ToSlash(strings.TrimSpace(snapshot))
		if snapshot == "" {
			continue
		}
		if !strings.HasPrefix(snapshot, "docs/evidence/extension/snapshots/") {
			violations = append(violations, fmt.Sprintf("%s api_snapshot must live under docs/evidence/extension/snapshots/: %s", cand.Label(), snapshot))
			continue
		}
		info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(snapshot)))
		if err != nil {
			if os.IsNotExist(err) {
				violations = append(violations, fmt.Sprintf("%s api_snapshot does not exist: %s", cand.Label(), snapshot))
				continue
			}
			violations = append(violations, fmt.Sprintf("%s api_snapshot cannot be read: %s: %v", cand.Label(), snapshot, err))
			continue
		}
		if info.IsDir() {
			violations = append(violations, fmt.Sprintf("%s api_snapshot is a directory, want file: %s", cand.Label(), snapshot))
		}
	}
	return violations
}

func blockerViolations(cand candidate) []string {
	expected := expectedBlockers(cand)
	actual := stringSet(cand.Blockers)

	var violations []string
	for blocker := range actual {
		if _, ok := knownBlockers[blocker]; !ok {
			violations = append(violations, fmt.Sprintf("%s has unknown blocker %q", cand.Label(), blocker))
		}
	}
	for blocker := range expected {
		if _, ok := actual[blocker]; !ok {
			violations = append(violations, fmt.Sprintf("%s missing blocker %q", cand.Label(), blocker))
		}
	}
	for blocker := range actual {
		if _, ok := expected[blocker]; !ok {
			violations = append(violations, fmt.Sprintf("%s has stale blocker %q", cand.Label(), blocker))
		}
	}
	return violations
}

func expectedBlockers(cand candidate) map[string]struct{} {
	expected := map[string]struct{}{}
	if len(cand.ReleaseRefs) < requiredReleaseCount {
		expected["release_history_missing"] = struct{}{}
	}
	if len(cand.APISnapshots) < requiredReleaseCount {
		expected["api_snapshot_missing"] = struct{}{}
	}
	if !isOwnerSignoffComplete(cand.OwnerSignoff) {
		expected["owner_signoff_missing"] = struct{}{}
	}
	return expected
}

func isOwnerSignoffComplete(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "signed", "complete", "approved":
		return true
	default:
		return false
	}
}

func candidateReport(cand candidate) string {
	blockers := append([]string(nil), cand.Blockers...)
	sort.Strings(blockers)
	if len(blockers) == 0 {
		blockers = []string{"none"}
	}
	stateKey := "status"
	stateValue := cand.CurrentStatus
	if cand.Subpackage != "" {
		stateKey = "tier"
		stateValue = cand.CurrentTier
	}
	if cand.Surface != "" {
		stateKey = "surface"
		stateValue = cand.Surface
	}
	return fmt.Sprintf("%s\t%s=%s\towner=%s\trelease_refs=%d\tapi_snapshots=%d\tblockers=%s",
		cand.Label(),
		stateKey,
		stateValue,
		cand.Owner,
		len(cand.ReleaseRefs),
		len(cand.APISnapshots),
		strings.Join(blockers, ","),
	)
}

func readModuleManifest(path string) (moduleManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return moduleManifest{}, err
	}
	defer func() { _ = file.Close() }()

	manifest := moduleManifest{Tiers: map[string][]string{}}
	currentTier := ""
	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		line := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " "))
		switch {
		case strings.HasPrefix(line, "name:"):
			manifest.Name = yamlScalar(line)
		case strings.HasPrefix(line, "owner:"):
			manifest.Owner = yamlScalar(line)
		case strings.HasPrefix(line, "status:"):
			manifest.Status = yamlScalar(line)
		case indent == 2 && strings.HasSuffix(line, ":"):
			currentTier = strings.TrimSuffix(line, ":")
		case indent == 4 && currentTier != "" && strings.HasPrefix(line, "- "):
			manifest.Tiers[currentTier] = append(manifest.Tiers[currentTier], trimYAMLValue(strings.TrimPrefix(line, "- ")))
		}
	}
	if err := scanner.Err(); err != nil {
		return moduleManifest{}, err
	}
	return manifest, nil
}

func (m moduleManifest) HasTier(tier, subpackage string) bool {
	for _, value := range m.Tiers[tier] {
		if value == subpackage {
			return true
		}
	}
	return false
}

func (c candidate) Label() string {
	if c.Surface != "" {
		return c.Module + ":" + c.Surface
	}
	if c.Subpackage == "" {
		return c.Module
	}
	return c.Module + "/" + c.Subpackage
}

func parseInlineList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || value == "[]" {
		return nil
	}
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")

	var out []string
	for _, part := range strings.Split(value, ",") {
		if item := trimYAMLValue(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func yamlScalar(line string) string {
	_, value, _ := strings.Cut(line, ":")
	return trimYAMLValue(value)
}

func trimYAMLValue(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.Index(value, "#"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return strings.Trim(value, `"'`)
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
