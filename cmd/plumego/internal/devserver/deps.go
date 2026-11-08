package devserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/contract"
)

const depsCacheTTL = 30 * time.Second
const maxDepsDiagnosticBytes = 64 * 1024

type depsCache struct {
	mu       sync.Mutex
	graph    *DependencyGraph
	updated  time.Time
	inFlight *depsInFlight
	build    func(context.Context, string) (*DependencyGraph, error)
}

type depsInFlight struct {
	done  chan struct{}
	graph *DependencyGraph
	err   error
}

func newDepsCache() *depsCache {
	return &depsCache{build: buildDependencyGraph}
}

func (c *depsCache) Get(ctx context.Context, dir string, refresh bool) (*DependencyGraph, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	if c.graph != nil && !refresh && time.Since(c.updated) < depsCacheTTL {
		graph := c.graph
		c.mu.Unlock()
		return graph, nil
	}
	if c.inFlight != nil {
		inFlight := c.inFlight
		c.mu.Unlock()
		select {
		case <-inFlight.done:
			return inFlight.graph, inFlight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	inFlight := &depsInFlight{done: make(chan struct{})}
	c.inFlight = inFlight
	build := c.build
	if build == nil {
		build = buildDependencyGraph
	}
	c.mu.Unlock()

	graph, err := build(ctx, dir)

	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		c.graph = graph
		c.updated = time.Now()
	}
	inFlight.graph = graph
	inFlight.err = err
	if c.inFlight == inFlight {
		c.inFlight = nil
	}
	close(inFlight.done)
	return graph, err
}

type DependencyGraph struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Summary     DepSummary     `json:"summary"`
	Nodes       []DepNode      `json:"nodes"`
	Edges       []DepEdge      `json:"edges"`
	Errors      []string       `json:"errors,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type DepSummary struct {
	MainModule      string `json:"main"`
	TotalModules    int    `json:"total_modules"`
	DirectModules   int    `json:"direct_modules"`
	IndirectModules int    `json:"indirect_modules"`
	TotalPackages   int    `json:"total_packages"`
	StdlibPackages  int    `json:"stdlib_packages"`
	TotalEdges      int    `json:"total_edges"`
}

type DepNode struct {
	ID       string `json:"id"`
	Version  string `json:"version,omitempty"`
	Replace  string `json:"replace,omitempty"`
	Packages int    `json:"packages"`
	Direct   bool   `json:"direct"`
	Main     bool   `json:"main"`
	Stdlib   bool   `json:"stdlib"`
}

type DepEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

type goModule struct {
	Path     string    `json:"Path"`
	Version  string    `json:"Version"`
	Main     bool      `json:"Main"`
	Indirect bool      `json:"Indirect"`
	Replace  *goModule `json:"Replace"`
}

type goPackage struct {
	ImportPath string    `json:"ImportPath"`
	Standard   bool      `json:"Standard"`
	Module     *goModule `json:"Module"`
	Imports    []string  `json:"Imports"`
}

type pkgInfo struct {
	module  string
	imports []string
}

func (d *Dashboard) handleDeps(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	includeStd := strings.TrimSpace(query.Get("include_std"))
	includeStdlib := includeStd == "" || includeStd == "1" || strings.EqualFold(includeStd, "true")

	maxNodes := 0
	if maxRaw := strings.TrimSpace(query.Get("max_nodes")); maxRaw != "" {
		parsed, err := strconv.Atoi(maxRaw)
		if err != nil || parsed <= 0 {
			writeDevserverError(w, r, contract.TypeValidation, devserverCodeDependencyGraphFailed, "invalid dependency graph request")
			return
		}
		maxNodes = parsed
	}

	refresh := strings.TrimSpace(query.Get("refresh"))
	refreshEnabled := refresh == "1" || strings.EqualFold(refresh, "true")

	timeoutCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	graph, err := d.depsCache.Get(timeoutCtx, d.projectDir, refreshEnabled)
	if err != nil {
		writeDevserverError(w, r, contract.TypeInternal, devserverCodeDependencyGraphFailed, "dependency graph unavailable")
		return
	}

	filtered := filterDependencyGraph(graph, includeStdlib, maxNodes)
	_ = contract.WriteResponse(w, r, http.StatusOK, filtered, nil)
}

func buildDependencyGraph(ctx context.Context, dir string) (*DependencyGraph, error) {
	mainModule, modules, err := loadModules(ctx, dir)
	if err != nil {
		return nil, err
	}

	packages, pkgModule, pkgCounts, stdlibCount, err := loadPackages(ctx, dir, mainModule)
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]*DepNode)
	for _, mod := range modules {
		node := &DepNode{
			ID:      mod.Path,
			Version: mod.Version,
			Direct:  !mod.Indirect,
			Main:    mod.Main,
		}
		if mod.Replace != nil {
			repl := mod.Replace.Path
			if mod.Replace.Version != "" {
				repl = fmt.Sprintf("%s@%s", repl, mod.Replace.Version)
			}
			node.Replace = repl
		}
		nodes[mod.Path] = node
	}

	if mainModule != "" {
		node := nodes[mainModule]
		if node == nil {
			node = &DepNode{ID: mainModule}
			nodes[mainModule] = node
		}
		node.Main = true
		node.Direct = true
	}

	totalPackages := 0
	for moduleID, count := range pkgCounts {
		node := nodes[moduleID]
		if node == nil {
			node = &DepNode{ID: moduleID}
			nodes[moduleID] = node
		}
		node.Packages = count
		totalPackages += count
	}

	if stdlibCount > 0 {
		node := nodes["stdlib"]
		if node == nil {
			node = &DepNode{ID: "stdlib"}
			nodes["stdlib"] = node
		}
		node.Stdlib = true
		node.Packages = stdlibCount
		totalPackages += stdlibCount
	}

	edgeCounts := make(map[string]*DepEdge)
	for _, pkg := range packages {
		from := pkg.module
		if from == "" {
			continue
		}
		for _, imp := range pkg.imports {
			to := pkgModule[imp]
			if to == "" {
				if isStdlibImport(imp) {
					to = "stdlib"
				} else {
					continue
				}
			}
			if from == to {
				continue
			}
			key := from + "->" + to
			edge := edgeCounts[key]
			if edge == nil {
				edge = &DepEdge{From: from, To: to}
				edgeCounts[key] = edge
			}
			edge.Count++
		}
	}

	nodesSlice := make([]DepNode, 0, len(nodes))
	directModules := 0
	indirectModules := 0
	for _, node := range nodes {
		if node.Stdlib {
			nodesSlice = append(nodesSlice, *node)
			continue
		}
		if node.Main || node.Direct {
			directModules++
		} else {
			indirectModules++
		}
		nodesSlice = append(nodesSlice, *node)
	}

	sort.Slice(nodesSlice, func(i, j int) bool {
		if nodesSlice[i].Packages == nodesSlice[j].Packages {
			return nodesSlice[i].ID < nodesSlice[j].ID
		}
		return nodesSlice[i].Packages > nodesSlice[j].Packages
	})

	edgesSlice := make([]DepEdge, 0, len(edgeCounts))
	for _, edge := range edgeCounts {
		edgesSlice = append(edgesSlice, *edge)
	}
	sort.Slice(edgesSlice, func(i, j int) bool {
		if edgesSlice[i].Count == edgesSlice[j].Count {
			return edgesSlice[i].From+edgesSlice[i].To < edgesSlice[j].From+edgesSlice[j].To
		}
		return edgesSlice[i].Count > edgesSlice[j].Count
	})

	graph := &DependencyGraph{
		GeneratedAt: time.Now(),
		Summary: DepSummary{
			MainModule:      mainModule,
			TotalModules:    len(nodesSlice),
			DirectModules:   directModules,
			IndirectModules: indirectModules,
			TotalPackages:   totalPackages,
			StdlibPackages:  stdlibCount,
			TotalEdges:      len(edgesSlice),
		},
		Nodes: nodesSlice,
		Edges: edgesSlice,
	}

	return graph, nil
}

func filterDependencyGraph(graph *DependencyGraph, includeStd bool, maxNodes int) *DependencyGraph {
	if graph == nil {
		return graph
	}

	if includeStd && maxNodes <= 0 {
		return graph
	}

	selected := make(map[string]bool)
	main := graph.Summary.MainModule
	if main != "" {
		selected[main] = true
	}

	if includeStd {
		for _, node := range graph.Nodes {
			if node.Stdlib {
				selected[node.ID] = true
			}
		}
	}

	if maxNodes <= 0 {
		for _, node := range graph.Nodes {
			if node.Stdlib && !includeStd {
				continue
			}
			selected[node.ID] = true
		}
	} else {
		remaining := maxNodes - len(selected)
		if remaining > 0 {
			candidates := make([]DepNode, 0, len(graph.Nodes))
			for _, node := range graph.Nodes {
				if selected[node.ID] {
					continue
				}
				if node.Stdlib && !includeStd {
					continue
				}
				candidates = append(candidates, node)
			}
			sort.Slice(candidates, func(i, j int) bool {
				if candidates[i].Packages == candidates[j].Packages {
					return candidates[i].ID < candidates[j].ID
				}
				return candidates[i].Packages > candidates[j].Packages
			})

			for i := 0; i < len(candidates) && remaining > 0; i++ {
				selected[candidates[i].ID] = true
				remaining--
			}
		}
	}

	filtered := &DependencyGraph{
		GeneratedAt: graph.GeneratedAt,
		Summary:     graph.Summary,
		Errors:      graph.Errors,
		Metadata:    graph.Metadata,
	}

	totalPackages := 0
	directModules := 0
	indirectModules := 0
	stdlibPackages := 0

	for _, node := range graph.Nodes {
		if !selected[node.ID] {
			continue
		}
		if node.Stdlib {
			stdlibPackages += node.Packages
		} else if node.Main || node.Direct {
			directModules++
		} else {
			indirectModules++
		}
		totalPackages += node.Packages
		filtered.Nodes = append(filtered.Nodes, node)
	}

	for _, edge := range graph.Edges {
		if selected[edge.From] && selected[edge.To] {
			filtered.Edges = append(filtered.Edges, edge)
		}
	}

	filtered.Summary = DepSummary{
		MainModule:      graph.Summary.MainModule,
		TotalModules:    len(filtered.Nodes),
		DirectModules:   directModules,
		IndirectModules: indirectModules,
		TotalPackages:   totalPackages,
		StdlibPackages:  stdlibPackages,
		TotalEdges:      len(filtered.Edges),
	}

	return filtered
}

func loadModules(ctx context.Context, dir string) (string, map[string]goModule, error) {
	mainMod, err := loadMainModule(ctx, dir)
	if err != nil {
		return "", nil, err
	}

	modules, err := loadAllModules(ctx, dir)
	if err != nil {
		return "", nil, err
	}

	if mainMod.Path != "" {
		modules[mainMod.Path] = mainMod
	}

	return mainMod.Path, modules, nil
}

func loadMainModule(ctx context.Context, dir string) (goModule, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json")
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		return goModule{}, fmt.Errorf("go list -m failed: %w", err)
	}

	var mod goModule
	if err := json.Unmarshal(output, &mod); err != nil {
		return goModule{}, fmt.Errorf("parse main module: %w", err)
	}
	return mod, nil
}

func loadAllModules(ctx context.Context, dir string) (map[string]goModule, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "all")
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stderrBuf, stderrDone := drainDependencyStderr(stderr)

	modules := make(map[string]goModule)
	dec := json.NewDecoder(stdout)
	for {
		var mod goModule
		if err := dec.Decode(&mod); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			_ = cmd.Wait()
			<-stderrDone
			return nil, fmt.Errorf("parse modules: %w", err)
		}
		if mod.Path != "" {
			modules[mod.Path] = mod
		}
	}

	if err := cmd.Wait(); err != nil {
		stderrErr := <-stderrDone
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" && stderrErr != nil {
			msg = stderrErr.Error()
		}
		if msg == "" {
			return nil, fmt.Errorf("go list -m all failed: %w", err)
		}
		return nil, fmt.Errorf("go list -m all failed: %s", msg)
	}
	<-stderrDone

	return modules, nil
}

func loadPackages(ctx context.Context, dir string, mainModule string) ([]pkgInfo, map[string]string, map[string]int, int, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-json", "./...")
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, 0, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, nil, 0, err
	}
	stderrBuf, stderrDone := drainDependencyStderr(stderr)

	dec := json.NewDecoder(stdout)
	pkgModule := make(map[string]string)
	pkgCounts := make(map[string]int)
	pkgInfos := make([]pkgInfo, 0, 256)
	stdlibCount := 0

	for {
		var pkg goPackage
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			_ = cmd.Wait()
			<-stderrDone
			return nil, nil, nil, 0, fmt.Errorf("parse packages: %w", err)
		}

		moduleID := resolvePackageModule(pkg, mainModule)
		if moduleID == "stdlib" {
			stdlibCount++
		} else if moduleID != "" {
			pkgCounts[moduleID]++
		}

		if pkg.ImportPath != "" && moduleID != "" {
			pkgModule[pkg.ImportPath] = moduleID
		}
		pkgInfos = append(pkgInfos, pkgInfo{
			module:  moduleID,
			imports: pkg.Imports,
		})
	}

	if err := cmd.Wait(); err != nil {
		stderrErr := <-stderrDone
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" && stderrErr != nil {
			msg = stderrErr.Error()
		}
		if msg == "" {
			return nil, nil, nil, 0, fmt.Errorf("go list -deps failed: %w", err)
		}
		return nil, nil, nil, 0, fmt.Errorf("go list -deps failed: %s", msg)
	}
	<-stderrDone

	return pkgInfos, pkgModule, pkgCounts, stdlibCount, nil
}

func drainDependencyStderr(stderr io.Reader) (*dependencyDiagnosticBuffer, <-chan error) {
	buf := newDependencyDiagnosticBuffer(maxDepsDiagnosticBytes)
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(buf, stderr)
		done <- err
		close(done)
	}()
	return buf, done
}

type dependencyDiagnosticBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated int
}

func newDependencyDiagnosticBuffer(limit int) *dependencyDiagnosticBuffer {
	return &dependencyDiagnosticBuffer{limit: limit}
}

func (b *dependencyDiagnosticBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if extra := len(p) - max(remaining, 0); extra > 0 {
		b.truncated += extra
	}
	return len(p), nil
}

func (b *dependencyDiagnosticBuffer) String() string {
	out := b.buf.String()
	if b.truncated > 0 {
		out += fmt.Sprintf("\n[plumego: dependency diagnostic output truncated after %d bytes; %d bytes omitted]\n", b.limit, b.truncated)
	}
	return out
}

func resolvePackageModule(pkg goPackage, mainModule string) string {
	if pkg.Standard {
		return "stdlib"
	}
	if pkg.Module != nil && pkg.Module.Path != "" {
		return pkg.Module.Path
	}
	if mainModule != "" && strings.HasPrefix(pkg.ImportPath, mainModule) {
		return mainModule
	}
	if isStdlibImport(pkg.ImportPath) {
		return "stdlib"
	}
	return ""
}

func isStdlibImport(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "C") {
		return true
	}
	if strings.Contains(path, ".") {
		return false
	}
	return true
}
