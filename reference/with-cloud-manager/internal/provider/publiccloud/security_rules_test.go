package publiccloud

import (
	"context"
	"fmt"
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestTencentRuleIdentityIgnoresIndices(t *testing.T) {
	index, priority := 0, 1
	row := tencentPolicy{
		tencentPolicyMatch: tencentPolicyMatch{Protocol: "TCP", Port: "443", CidrBlock: "10.1.0.0/24", Action: "ACCEPT"},
		PolicyIndex:        &index, Priority: &priority,
	}
	original, err := tencentRule("sg-fixture", "ingress", row, make(map[string]int))
	if err != nil {
		t.Fatal(err)
	}
	index, priority = 3, 9
	seen := make(map[string]int)
	moved, err := tencentRule("sg-fixture", "ingress", row, seen)
	if err != nil || moved.ID != original.ID || moved.Name == original.Name {
		t.Fatalf("reordered policy: ID %s, name %s, error %v", moved.ID, moved.Name, err)
	}
	duplicate, err := tencentRule("sg-fixture", "ingress", row, seen)
	if err != nil || duplicate.ID == original.ID {
		t.Fatalf("duplicate policy: ID %s, error %v", duplicate.ID, err)
	}
}

func TestTencentPolicyPageRequiresUnchangedSnapshot(t *testing.T) {
	objects := make([]provider.Object, provider.MaxPageObjects+1)
	for index := range objects {
		objects[index] = provider.Object{ID: fmt.Sprintf("tc-sgr-fixture-%d", index), Name: "ingress tcp any", Status: "available"}
	}
	first, err := tencentPolicyPage(objects, "")
	if err != nil || len(first.Objects) != provider.MaxPageObjects || first.Next == "" {
		t.Fatalf("first rule page: objects %d, next %q, error %v", len(first.Objects), first.Next, err)
	}
	last, err := tencentPolicyPage(objects, first.Next)
	if err != nil || len(last.Objects) != 1 || last.Objects[0].ID != objects[provider.MaxPageObjects].ID || last.Next != "" {
		t.Fatalf("last rule page: %+v, error %v", last, err)
	}
	objects[0].Name = "ingress tcp any action=drop"
	if _, err := tencentPolicyPage(objects, first.Next); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("changed rule snapshot accepted: %v", err)
	}
}

func TestRuleWalkRejectsChangedParent(t *testing.T) {
	group := func(context.Context, string, string, int) (provider.Page, error) {
		return provider.Page{Objects: []provider.Object{{ID: "sg-other"}}}, nil
	}
	rules := func(context.Context, string, string, string) (provider.Page, error) {
		t.Fatal("requested rules for a changed parent")
		return provider.Page{}, nil
	}
	if _, err := walkSecurityRules(t.Context(), "fixture-region", `{"group_id":"sg-expected","rule_page":"token"}`, group, rules); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("changed security group accepted: %v", err)
	}
}
