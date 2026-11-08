package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type generateSpecOptions struct {
	Dir        string
	OutputPath string
	Format     string
	AppPath    string
}

type goModuleInfo struct {
	Path    string        `json:"Path"`
	Dir     string        `json:"Dir"`
	Main    bool          `json:"Main"`
	Replace *goModuleInfo `json:"Replace"`
}

func runGenerateSpec(ctx *Context, opts generateSpecOptions) error {
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "yaml" {
		return ctx.Out.Error(fmt.Sprintf("unsupported spec format: %s", opts.Format), 1)
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		outputPath = "-"
	}
	if outputPath != "-" && !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(opts.Dir, outputPath)
	}

	modules, err := listGoModules(opts.Dir)
	if err != nil {
		return ctx.Out.Error(fmt.Sprintf("failed to inspect Go modules: %v", err), 1)
	}
	target, err := resolveSpecTarget(opts.Dir, opts.AppPath, modules)
	if err != nil {
		return ctx.Out.Error(err.Error(), 1)
	}

	if err := runSpecHelper(opts.Dir, target, modules, outputPath, format); err != nil {
		return ctx.Out.Error(fmt.Sprintf("spec generation failed: %v", err), 1)
	}
	if outputPath == "-" {
		return nil
	}
	return ctx.Out.Success("OpenAPI spec generated", map[string]string{
		"app":    target.AppImport,
		"format": format,
		"output": outputPath,
	})
}

type specTarget struct {
	ModulePath   string
	AppImport    string
	ConfigImport string
}

func resolveSpecTarget(dir, appPath string, modules []goModuleInfo) (specTarget, error) {
	mainModule := goModuleInfo{}
	for _, module := range modules {
		if module.Main {
			mainModule = module
			break
		}
	}
	if mainModule.Path == "" {
		return specTarget{}, errors.New("failed to locate target module")
	}

	appPath = strings.TrimSpace(appPath)
	if appPath == "" || appPath == "." {
		appPath = "./internal/app"
	}

	appImport := appPath
	switch {
	case appPath == ".":
		appImport = mainModule.Path
	case strings.HasPrefix(appPath, "./"):
		appImport = mainModule.Path + "/" + strings.TrimPrefix(appPath, "./")
	case strings.HasPrefix(appPath, "../") || filepath.IsAbs(appPath):
		return specTarget{}, fmt.Errorf("unsupported app package path: %s", appPath)
	}

	configImport := mainModule.Path + "/internal/config"
	if appImport == mainModule.Path {
		configImport = filepath.ToSlash(filepath.Join(mainModule.Path, "internal/config"))
	}

	return specTarget{
		ModulePath:   mainModule.Path,
		AppImport:    appImport,
		ConfigImport: configImport,
	}, nil
}

func listGoModules(dir string) ([]goModuleInfo, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}

	dec := json.NewDecoder(bytes.NewReader(out))
	var modules []goModuleInfo
	for {
		var module goModuleInfo
		if err := dec.Decode(&module); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		modules = append(modules, module)
	}
	return modules, nil
}

func runSpecHelper(appDir string, target specTarget, modules []goModuleInfo, outputPath, format string) error {
	tmpDir, err := os.MkdirTemp("", "plumego-spec-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(specHelperGoMod(appDir, target, modules)), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(specHelperSource(target)), 0644); err != nil {
		return err
	}

	hintsPath := filepath.Join(appDir, "plumego.spec.yaml")
	return runSpecHelperCommand(tmpDir, "go", "run", "-mod=mod", ".", "--output", outputPath, "--format", format, "--hints", hintsPath)
}

func runSpecHelperCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "GOWORK=off")
	return cmd.Run()
}

func specHelperGoMod(appDir string, target specTarget, modules []goModuleInfo) string {
	replaces := map[string]string{
		target.ModulePath: appDir,
	}
	for _, module := range modules {
		if module.Replace == nil || module.Replace.Dir == "" {
			continue
		}
		replaces[module.Path] = module.Replace.Dir
	}

	keys := make([]string, 0, len(replaces))
	for key := range replaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var b strings.Builder
	fmt.Fprintf(&b, "module %s/plumego_spec_helper\n\n", target.ModulePath)
	b.WriteString("go 1.26.0\n\n")
	b.WriteString("require (\n")
	fmt.Fprintf(&b, "\t%s v0.0.0\n", target.ModulePath)
	b.WriteString("\tgithub.com/birdordev/plumego v0.0.0\n")
	b.WriteString("\tgopkg.in/yaml.v3 v3.0.1\n")
	b.WriteString(")\n\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "replace %s => %s\n", key, filepath.ToSlash(replaces[key]))
	}
	return b.String()
}

func specHelperSource(target specTarget) string {
	return fmt.Sprintf(`package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	apppkg %q
	configpkg %q
	"github.com/birdordev/plumego/x/openapi"
	"gopkg.in/yaml.v3"
)

func main() {
	output := flag.String("output", "-", "output path")
	format := flag.String("format", "json", "output format")
	hints := flag.String("hints", "plumego.spec.yaml", "operation hints file")
	flag.Parse()

	cfg, err := configpkg.Load()
	if err != nil {
		fatalf("load config: %%v", err)
	}
	app, err := apppkg.New(cfg)
	if err != nil {
		fatalf("initialize app: %%v", err)
	}
	if err := app.RegisterRoutes(); err != nil {
		fatalf("register routes: %%v", err)
	}

	info, ops, err := loadSpecFile(*hints)
	if err != nil {
		fatalf("load spec hints: %%v", err)
	}
	doc := openapi.New().Generate(app.Core.Routes(), ops)
	if info.Title != "" {
		doc.Info.Title = info.Title
	}
	if info.Version != "" {
		doc.Info.Version = info.Version
	}

	out := os.Stdout
	if *output != "-" {
		file, err := os.Create(*output)
		if err != nil {
			fatalf("create output: %%v", err)
		}
		defer file.Close()
		out = file
	}

	switch *format {
	case "json":
		err = openapi.WriteJSON(out, doc)
	case "yaml":
		err = openapi.WriteYAML(out, doc)
	default:
		err = fmt.Errorf("unsupported format: %%s", *format)
	}
	if err != nil {
		fatalf("write spec: %%v", err)
	}
}

func loadSpecFile(path string) (openapi.Info, map[string]openapi.Op, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return openapi.Info{}, nil, nil
		}
		return openapi.Info{}, nil, err
	}
	var raw any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return openapi.Info{}, nil, err
	}
	jsonData, err := json.Marshal(normalizeYAML(raw))
	if err != nil {
		return openapi.Info{}, nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(jsonData, &fields); err != nil {
		return openapi.Info{}, nil, err
	}

	var info openapi.Info
	if rawInfo := fields["info"]; len(rawInfo) > 0 {
		if err := json.Unmarshal(rawInfo, &info); err != nil {
			return openapi.Info{}, nil, err
		}
	}

	var ops map[string]openapi.Op
	rawOps := fields["operations"]
	if len(rawOps) == 0 {
		rawOps = fields["ops"]
	}
	if len(rawOps) > 0 {
		if err := json.Unmarshal(rawOps, &ops); err != nil {
			return openapi.Info{}, nil, err
		}
	}
	return info, ops, nil
}

func normalizeYAML(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[key] = normalizeYAML(child)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			out[fmt.Sprint(key)] = normalizeYAML(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = normalizeYAML(child)
		}
		return out
	default:
		return value
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
`, target.AppImport, target.ConfigImport)
}
