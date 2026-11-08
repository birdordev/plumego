package file

import (
	"reflect"
	"testing"
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
