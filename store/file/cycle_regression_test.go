package file

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"runtime/debug"
	"testing"
	"time"
)

func TestCloneCyclicMetadata(t *testing.T) {
	root := map[string]any{"safe": "original"}
	child := map[string]any{"parent": root}
	items := make([]any, 2)
	items[0] = items
	items[1] = root
	root["self"] = root
	root["child"] = child
	root["items"] = items
	for _, clone := range []map[string]any{(File{Metadata: root}).Clone().Metadata, (PutOptions{Metadata: root}).CloneMetadata()} {
		if reflect.ValueOf(clone).Pointer() == reflect.ValueOf(root).Pointer() {
			t.Fatal("clone shared root")
		}
		if reflect.ValueOf(clone["self"]).Pointer() != reflect.ValueOf(clone).Pointer() {
			t.Fatal("self reference not preserved")
		}
		if reflect.ValueOf(clone["child"].(map[string]any)["parent"]).Pointer() != reflect.ValueOf(clone).Pointer() {
			t.Fatal("cross reference not preserved")
		}
		got := clone["items"].([]any)
		if reflect.ValueOf(got[0]).Pointer() != reflect.ValueOf(got).Pointer() {
			t.Fatal("slice cycle not preserved")
		}
		clone["self"].(map[string]any)["safe"] = "changed"
		got[1].(map[string]any)["extra"] = true
		if root["safe"] != "original" || root["extra"] != nil {
			t.Fatal("clone mutated input")
		}
	}
}

func TestCloneDeepMetadataWithBoundedStack(t *testing.T) {
	if os.Getenv("PLUMEGO_METADATA_STACK_PROBE") == "1" {
		// A small stack isolates regressions without exhausting the test runner.
		debug.SetMaxStack(256 << 10)
		const depth = 20000
		root := map[string]any{}
		current := root
		for i := 0; i < depth; i++ {
			items := make([]any, 1)
			next := map[string]any{}
			current["next"] = items
			items[0] = next
			current = next
		}
		current["value"] = "leaf"
		current["root"] = root
		for _, copied := range []map[string]any{(File{Metadata: root}).Clone().Metadata, (PutOptions{Metadata: root}).CloneMetadata()} {
			leaf := copied
			for i := 0; i < depth; i++ {
				leaf = leaf["next"].([]any)[0].(map[string]any)
			}
			if leaf["value"] != "leaf" || reflect.ValueOf(leaf["root"]).Pointer() != reflect.ValueOf(copied).Pointer() {
				t.Fatal("deep graph was truncated or lost its back reference")
			}
			leaf["value"] = "changed"
			if current["value"] != "leaf" {
				t.Fatal("deep clone mutated the original leaf")
			}
		}
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCloneDeepMetadataWithBoundedStack$")
	cmd.Env = append(os.Environ(), "PLUMEGO_METADATA_STACK_PROBE=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) > 2000 {
			output = output[:2000]
		}
		t.Fatalf("deep metadata subprocess failed: %v\n%s", err, output)
	}
}
