package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/birdordev/plumego/internal/checks/checkutil"
)

type moduleState struct {
	Name   string
	Owner  string
	Status string
	Risk   string
}

type betaCandidate struct {
	Module      string
	EvidenceDoc string
	Blockers    []string
}

type maturitySignal struct {
	Module                string
	RecommendedEntrypoint string
	DocsSignal            string
	CoverageSignal        string
}

// surfaceCandidateStatus holds the package-level status fields from a
// surface_candidate entry in extension-beta-evidence.yaml.
type surfaceCandidateStatus struct {
	Module        string
	Surface       string
	Package       string
	CurrentStatus string
}

func (s surfaceCandidateStatus) Label() string {
	if s.Surface != "" {
		return s.Module + ":" + s.Surface
	}
	return s.Module
}

var validExtensionStatuses = map[string]bool{
	"ga":           true,
	"beta":         true,
	"experimental": true,
}

func main() {
	report := flag.Bool("report", false, "print deterministic dashboard source data")
	flag.Parse()

	repoRoot, err := os.Getwd()
	if err != nil {
		failf("resolve working directory: %v", err)
	}

	lines, violations, err := maturityReport(repoRoot)
	if err != nil {
		failf("run extension maturity check: %v", err)
	}
	if *report {
		for _, line := range lines {
			fmt.Println(line)
		}
	}
	if len(violations) == 0 {
		return
	}

	fmt.Fprintln(os.Stderr, "extension-maturity check failed:")
	for _, violation := range violations {
		fmt.Fprintf(os.Stderr, "- %s\n", violation)
	}
	os.Exit(1)
}

func maturityReport(repoRoot string) ([]string, []string, error) {
	roots, err := checkutil.ReadRepoExtensionRoots(repoRoot)
	if err != nil {
		return nil, nil, err
	}

	readmeBytes, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		return nil, nil, err
	}

	content, err := os.ReadFile(filepath.Join(repoRoot, "docs", "concepts", "extension-maturity.md"))
	if err != nil {
		return nil, nil, err
	}
	dashboard := string(content)

	evidencePath := filepath.Join(repoRoot, "specs", "extension-beta-evidence.yaml")
	candidates, err := readBetaCandidates(evidencePath)
	if err != nil {
		return nil, nil, err
	}
	signals, err := readMaturitySignals(filepath.Join(repoRoot, "specs", "extension-maturity.yaml"))
	if err != nil {
		return nil, nil, err
	}
	completedBeta, err := readCompletedBetaModules(evidencePath)
	if err != nil {
		return nil, nil, err
	}
	surfaceCands, err := readSurfaceCandidateStatuses(evidencePath)
	if err != nil {
		return nil, nil, err
	}

	var paths []string
	for path := range roots {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	states := make(map[string]moduleState, len(paths))
	var report []string
	var violations []string

	for _, path := range paths {
		state, err := readModuleState(filepath.Join(repoRoot, path, "module.yaml"))
		if err != nil {
			return nil, nil, err
		}
		states[path] = state
		report = append(report, fmt.Sprintf("%s\tstatus=%s\trisk=%s\towner=%s", path, state.Status, state.Risk, state.Owner))

		if state.Name != "" && state.Name != path {
			violations = append(violations, fmt.Sprintf("%s module.yaml name %q does not match declared path", path, state.Name))
		}
		row, ok := findDashboardRow(dashboard, path)
		if !ok {
			violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md missing row for %s", path))
			continue
		}
		if !strings.Contains(row, "| "+state.Status+" |") {
			violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md row for %s missing status %q", path, state.Status))
		}
		if !strings.Contains(row, "| "+state.Risk+" |") {
			violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md row for %s missing risk %q", path, state.Risk))
		}
		if !strings.Contains(row, "| "+state.Owner+" |") {
			violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md row for %s missing owner %q", path, state.Owner))
		}
		if candidate, ok := candidates[path]; ok {
			violations = append(violations, candidateDashboardViolations(row, candidate)...)
		}
		signal, ok := signals[path]
		if !ok {
			violations = append(violations, fmt.Sprintf("specs/extension-maturity.yaml missing signal entry for %s", path))
		} else {
			violations = append(violations, signalDashboardViolations(row, signal)...)
		}
	}

	// Aggregate checks across all extension roots.
	violations = append(violations, invalidStatusViolations(states)...)
	violations = append(violations, betaEvidenceViolations(states, completedBeta)...)
	violations = append(violations, readmeBetaListViolations(string(readmeBytes), states)...)
	surfaceViolations, err := surfaceCandidateStatusViolations(repoRoot, surfaceCands)
	if err != nil {
		return nil, nil, err
	}
	violations = append(violations, surfaceViolations...)

	sort.Strings(report)
	sort.Strings(violations)
	return report, violations, nil
}

// invalidStatusViolations reports extension roots whose module.yaml status is
// not one of the valid values defined in specs/module-manifest.schema.yaml.
func invalidStatusViolations(states map[string]moduleState) []string {
	var violations []string
	for path, state := range states {
		if !validExtensionStatuses[state.Status] {
			violations = append(violations, fmt.Sprintf(
				"%s/module.yaml status %q is not a valid extension status; must be one of: ga, beta, experimental",
				path, state.Status,
			))
		}
	}
	return violations
}

// betaEvidenceViolations reports extension roots that carry status "beta" in
// module.yaml but have no completed entry in specs/extension-beta-evidence.yaml.
// A completed entry has current_status "beta", no blockers, and an evidence_doc.
func betaEvidenceViolations(states map[string]moduleState, completed map[string]bool) []string {
	var violations []string
	for path, state := range states {
		if state.Status != "beta" {
			continue
		}
		if !completed[path] {
			violations = append(violations, fmt.Sprintf(
				"%s has status \"beta\" in module.yaml but has no completed entry in specs/extension-beta-evidence.yaml",
				path,
			))
		}
	}
	return violations
}

// readmeBetaListViolations reports drift between the beta extension families
// listed in README.md and the actual beta modules from their module.yaml files.
func readmeBetaListViolations(readme string, states map[string]moduleState) []string {
	listedBeta := parseReadmeBetaModules(readme)

	actualBeta := map[string]bool{}
	for path, state := range states {
		if state.Status == "beta" {
			actualBeta[path] = true
		}
	}

	if len(listedBeta) == 0 {
		return []string{"README.md: beta extension families paragraph not found or lists no x/* modules"}
	}

	listedSet := map[string]bool{}
	for _, m := range listedBeta {
		listedSet[m] = true
	}

	var violations []string
	for _, m := range listedBeta {
		if !actualBeta[m] {
			violations = append(violations, fmt.Sprintf(
				"README.md lists %q as a beta extension family but %s/module.yaml status is not beta",
				m, m,
			))
		}
	}
	for m := range actualBeta {
		if !listedSet[m] {
			violations = append(violations, fmt.Sprintf(
				"README.md does not list %q but %s/module.yaml status is beta",
				m, m,
			))
		}
	}
	return violations
}

// parseReadmeBetaModules extracts the backtick-quoted x/* module names from the
// beta extension families paragraph in README.md.
func parseReadmeBetaModules(content string) []string {
	lines := strings.Split(content, "\n")
	var modules []string
	collecting := false

	for _, line := range lines {
		if !collecting && strings.Contains(line, "**beta**") && strings.Contains(line, "extension famil") {
			collecting = true
		}
		if !collecting {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if (trimmed == "" || strings.HasPrefix(trimmed, "#")) && len(modules) > 0 {
			break
		}

		parts := strings.Split(line, "`")
		for i := 1; i < len(parts); i += 2 {
			name := parts[i]
			if strings.HasPrefix(name, "x/") && !strings.Contains(name, "*") && !strings.Contains(name, " ") && len(name) > 2 {
				modules = append(modules, name)
			}
		}
	}
	return modules
}

// surfaceCandidateStatusViolations reports cases where a surface_candidate in
// specs/extension-beta-evidence.yaml specifies a package path that has its own
// module.yaml, but that module.yaml status does not match current_status.
func surfaceCandidateStatusViolations(repoRoot string, candidates []surfaceCandidateStatus) ([]string, error) {
	var violations []string
	for _, cand := range candidates {
		if cand.Package == "" || cand.CurrentStatus == "" {
			continue
		}
		manifestPath := filepath.Join(repoRoot, filepath.FromSlash(cand.Package), "module.yaml")
		if _, err := os.Stat(manifestPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		state, err := readModuleState(manifestPath)
		if err != nil {
			return nil, err
		}
		if state.Status != cand.CurrentStatus {
			violations = append(violations, fmt.Sprintf(
				"specs/extension-beta-evidence.yaml surface_candidate %s current_status %q does not match %s/module.yaml status %q",
				cand.Label(), cand.CurrentStatus, cand.Package, state.Status,
			))
		}
	}
	return violations, nil
}

// readCompletedBetaModules reads the candidates and surface_candidates sections
// of extension-beta-evidence.yaml and returns the set of module paths that have
// completed beta evidence (current_status "beta", no blockers, evidence_doc present).
func readCompletedBetaModules(path string) (map[string]bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	type betaEntry struct {
		Module        string
		CurrentStatus string
		HasBlockers   bool
		EvidenceDoc   string
	}

	var entries []betaEntry
	var current *betaEntry
	var currentListKey string
	inSection := false

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
			case "candidates:", "surface_candidates:":
				if current != nil {
					entries = append(entries, *current)
					current = nil
				}
				inSection = true
				continue
			default:
				if inSection {
					if current != nil {
						entries = append(entries, *current)
						current = nil
					}
					inSection = false
				}
			}
		}
		if !inSection {
			continue
		}

		if indent == 2 && strings.HasPrefix(trimmed, "- module:") {
			if current != nil {
				entries = append(entries, *current)
			}
			current = &betaEntry{Module: yamlScalar(trimmed)}
			currentListKey = ""
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
			currentListKey = ""
			switch key {
			case "current_status":
				current.CurrentStatus = trimYAMLValue(value)
			case "evidence_doc":
				current.EvidenceDoc = trimYAMLValue(value)
			case "blockers":
				parsed := parseInlineList(value)
				current.HasBlockers = len(parsed) > 0
				if strings.TrimSpace(value) == "" {
					currentListKey = key
				}
			}
			continue
		}
		if indent == 6 && currentListKey == "blockers" && strings.HasPrefix(trimmed, "- ") {
			current.HasBlockers = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		entries = append(entries, *current)
	}

	completed := map[string]bool{}
	for _, e := range entries {
		if e.CurrentStatus == "beta" && !e.HasBlockers && e.EvidenceDoc != "" {
			completed[e.Module] = true
		}
	}
	return completed, nil
}

// readSurfaceCandidateStatuses reads the surface_candidates section of
// extension-beta-evidence.yaml and returns entries that have both a package
// path and a current_status.
func readSurfaceCandidateStatuses(path string) ([]surfaceCandidateStatus, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var candidates []surfaceCandidateStatus
	var current *surfaceCandidateStatus
	inSection := false

	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if indent == 0 {
			if trimmed == "surface_candidates:" {
				if current != nil {
					candidates = append(candidates, *current)
					current = nil
				}
				inSection = true
				continue
			}
			if inSection {
				if current != nil {
					candidates = append(candidates, *current)
					current = nil
				}
				inSection = false
			}
			continue
		}
		if !inSection {
			continue
		}

		if indent == 2 && strings.HasPrefix(trimmed, "- module:") {
			if current != nil {
				candidates = append(candidates, *current)
			}
			current = &surfaceCandidateStatus{Module: yamlScalar(trimmed)}
			continue
		}
		if current == nil || indent != 4 {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "surface":
			current.Surface = trimYAMLValue(value)
		case "package":
			current.Package = trimYAMLValue(value)
		case "current_status":
			current.CurrentStatus = trimYAMLValue(value)
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

func readMaturitySignals(path string) (map[string]maturitySignal, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	signals := map[string]maturitySignal{}
	var current *maturitySignal
	inSignals := false

	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if indent == 0 && trimmed == "signals:" {
			inSignals = true
			continue
		}
		if !inSignals {
			continue
		}
		if indent == 0 {
			break
		}
		if indent == 2 && strings.HasPrefix(trimmed, "- module:") {
			if current != nil {
				signals[current.Module] = *current
			}
			current = &maturitySignal{Module: yamlScalar(trimmed)}
			continue
		}
		if current == nil || indent != 4 {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "recommended_entrypoint":
			current.RecommendedEntrypoint = trimYAMLValue(value)
		case "docs_signal":
			current.DocsSignal = trimYAMLValue(value)
		case "coverage_signal":
			current.CoverageSignal = trimYAMLValue(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		signals[current.Module] = *current
	}
	return signals, nil
}

func readModuleState(path string) (moduleState, error) {
	file, err := os.Open(path)
	if err != nil {
		return moduleState{}, err
	}
	defer func() { _ = file.Close() }()

	var state moduleState
	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "name:") {
			state.Name = yamlScalar(line)
		}
		if strings.HasPrefix(line, "owner:") {
			state.Owner = yamlScalar(line)
		}
		if strings.HasPrefix(line, "status:") {
			state.Status = yamlScalar(line)
		}
		if strings.HasPrefix(line, "risk:") {
			state.Risk = yamlScalar(line)
		}
	}
	if err := scanner.Err(); err != nil {
		return moduleState{}, err
	}
	if state.Status == "" {
		return moduleState{}, fmt.Errorf("%s missing status", path)
	}
	if state.Risk == "" {
		return moduleState{}, fmt.Errorf("%s missing risk", path)
	}
	if state.Owner == "" {
		return moduleState{}, fmt.Errorf("%s missing owner", path)
	}
	return state, nil
}

func readBetaCandidates(path string) (map[string]betaCandidate, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	candidates := map[string]betaCandidate{}
	var current *betaCandidate
	var currentList string
	inCandidates := false

	scanner := checkutil.NewLineScanner(file)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), " \t")
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if indent == 0 && trimmed == "candidates:" {
			inCandidates = true
			continue
		}
		if !inCandidates {
			continue
		}
		if indent == 0 {
			break
		}

		if indent == 2 && strings.HasPrefix(trimmed, "- module:") {
			if current != nil {
				candidates[current.Module] = *current
			}
			current = &betaCandidate{Module: yamlScalar(trimmed)}
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
			case "evidence_doc":
				current.EvidenceDoc = trimYAMLValue(value)
			case "blockers":
				current.Blockers = parseInlineList(value)
				if value == "" {
					currentList = key
				}
			}
			continue
		}
		if indent == 6 && currentList == "blockers" && strings.HasPrefix(trimmed, "- ") {
			current.Blockers = append(current.Blockers, trimYAMLValue(strings.TrimPrefix(trimmed, "- ")))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		candidates[current.Module] = *current
	}
	return candidates, nil
}

func candidateDashboardViolations(row string, candidate betaCandidate) []string {
	var violations []string
	link := strings.TrimPrefix(filepath.ToSlash(candidate.EvidenceDoc), "docs/")
	if candidate.EvidenceDoc == "" {
		violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md row for %s has beta candidate without evidence_doc", candidate.Module))
	} else if !strings.Contains(row, "("+link+")") && !strings.Contains(row, "(../"+link+")") {
		violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md row for %s missing evidence link %s", candidate.Module, link))
	}

	requiredText := map[string]string{
		"release_history_missing": "release history",
		"api_snapshot_missing":    "API snapshot",
		"owner_signoff_missing":   "owner sign-off",
	}
	for _, blocker := range candidate.Blockers {
		text, ok := requiredText[blocker]
		if !ok {
			continue
		}
		if !strings.Contains(row, text) {
			violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md row for %s missing blocker text %q", candidate.Module, text))
		}
	}
	return violations
}

func signalDashboardViolations(row string, signal maturitySignal) []string {
	required := map[string]string{
		"recommended_entrypoint": signal.RecommendedEntrypoint,
		"docs_signal":            signal.DocsSignal,
		"coverage_signal":        signal.CoverageSignal,
	}

	var violations []string
	for field, value := range required {
		if value == "" {
			violations = append(violations, fmt.Sprintf("specs/extension-maturity.yaml %s missing %s", signal.Module, field))
			continue
		}
		if !strings.Contains(row, value) {
			violations = append(violations, fmt.Sprintf("docs/concepts/extension-maturity.md row for %s missing %s %q", signal.Module, field, value))
		}
	}
	return violations
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

func yamlScalar(line string) string {
	_, value, _ := strings.Cut(line, ":")
	return trimYAMLValue(value)
}

func trimYAMLValue(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"'`)
}

func findDashboardRow(dashboard, path string) (string, bool) {
	needle := "| `" + path + "` |"
	for _, line := range strings.Split(dashboard, "\n") {
		if strings.Contains(line, needle) {
			return line, true
		}
	}
	return "", false
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
