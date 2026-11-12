package main

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/birdordev/plumego/internal/checks/checkutil"
)

func TestCheckStableSnapshots(t *testing.T) {
	repo := t.TempDir()
	const module = "example.com/toolkit"
	snapshots := filepath.Join(repo, "docs", "evidence", "stable-api", "snapshots")
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range checkutil.StableRoots() {
		write(filepath.Join(repo, root, "api.go"), "package fixture\ntype Public int\n")
		write(filepath.Join(snapshots, root+"-head.snapshot"),
			symbolLine(module+"/"+root, "type", "Public", "type Public int")+"\n")
	}
	write(filepath.Join(repo, "core", "helper", "api.go"), "package helper\ntype OutsideSnapshot int\n")
	write(filepath.Join(repo, "router", "helper", "api.go"), "package helper\ntype OutsideSnapshot int\n")
	if err := checkStableSnapshots(repo, module, io.Discard); err != nil {
		t.Fatalf("matching stable snapshots: %v", err)
	}

	contractBaseline := filepath.Join(snapshots, "contract-head.snapshot")
	if err := os.Remove(contractBaseline); err != nil {
		t.Fatal(err)
	}
	if err := checkStableSnapshots(repo, module, io.Discard); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "contract-head.snapshot") {
		t.Fatalf("missing snapshot error = %v", err)
	}
	write(contractBaseline, symbolLine(module+"/contract", "type", "Public", "type Public int")+"\n")
	write(filepath.Join(repo, "contract", "helper", "api.go"), "package helper\ntype Added int\n")
	var diff strings.Builder
	if err := checkStableSnapshots(repo, module, &diff); err == nil || !strings.Contains(diff.String(), "+ added symbol\t"+module+"/contract/helper\ttype\tAdded") {
		t.Fatalf("subpackage API change: error = %v, diff = %s", err, diff.String())
	}
}

func TestSnapshotGenDeclOmitsUnexportedStructFields(t *testing.T) {
	src := `package sample

type Exported struct {
	Visible string
	hidden string
	AlsoVisible, alsoHidden int
	*Embedded
	*hiddenEmbedded
}

type Embedded struct{}
type hiddenEmbedded struct{}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse source: %v", err)
	}

	var lines []string
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok {
			lines = append(lines, snapshotGenDecl(fset, "example.com/sample", gen)...)
		}
	}

	got := strings.Join(lines, "\n")
	if !strings.Contains(got, "Visible string") {
		t.Fatalf("expected exported field in snapshot, got:\n%s", got)
	}
	if !strings.Contains(got, "AlsoVisible int") {
		t.Fatalf("expected exported field from mixed field list in snapshot, got:\n%s", got)
	}
	if !strings.Contains(got, "*Embedded") {
		t.Fatalf("expected exported embedded field in snapshot, got:\n%s", got)
	}
	for _, forbidden := range []string{"hidden string", "alsoHidden", "hiddenEmbedded"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("snapshot includes unexported field %q:\n%s", forbidden, got)
		}
	}
}
