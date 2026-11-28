package billing

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

func TestAllocationSignedRounding(t *testing.T) {
	shares, err := canonicalShares([]AllocationShare{{"z", 3333}, {"b", 3333}, {"a", 3334}}, make(map[string]bool))
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"0", "1", "2", "-1", "-2", "99999999999999999999999999999999999999", "-99999999999999999999999999999999999999"} {
		amount, _ := new(big.Int).SetString(input, 10)
		var sum big.Int
		result := splitAmount(amount, shares)
		for _, part := range result {
			sum.Add(&sum, &part)
		}
		if sum.Cmp(amount) != 0 {
			t.Fatalf("allocation of %s sums to %s", input, &sum)
		}
		if input == "2" && (result[0].Int64() != 1 || result[1].Int64() != 1 || result[2].Sign() != 0) {
			t.Fatal("equal remainders must choose the lexicographically first center")
		}
	}
}

func TestAllocationTagPrecedence(t *testing.T) {
	policy, _, err := canonicalPolicy(AllocationPolicy{Rules: []AllocationRule{
		{Service: "compute", Tags: map[string]json.RawMessage{"empty": []byte("null"), "enabled": []byte("true")},
			Shares: []AllocationShare{{"a", 10000}}},
		{Service: "compute", Shares: []AllocationShare{{"b", 10000}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		tags    string
		want    int
		invalid bool
	}{
		{`{"empty":null,"enabled":true}`, 0, false},
		{`{"enabled":true}`, 1, false},
		{`{"empty":null,"enabled":"true"}`, 1, false},
		{`{"empty":null,"enabled":true,"enabled":false}`, 1, true},
		{`{"enabled":true} {"empty":null}`, 1, true},
		{`team:storage`, 1, true},
	} {
		index, invalid := allocationMatch(policy.Rules, allocationLine{service: "compute", tags: test.tags})
		if index != test.want || invalid != test.invalid {
			t.Fatalf("allocation match: %d invalid=%v for %s", index, invalid, test.tags)
		}
	}
	for _, shares := range [][]AllocationShare{{{"unallocated", 10000}}, {{"a", 9999}}, {{"a", 5000}, {"a", 5000}}} {
		_, _, err := canonicalPolicy(AllocationPolicy{Default: shares})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid weights accepted: %v", err)
		}
	}
}

func ledgerAllocationFixture(t *testing.T) (Store, Key, AllocationPolicy) {
	t.Helper()
	db := billingDB(t)
	if err := MigrateAllocations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	key, manifest := testLedger()
	manifest.ExpectedBilled = map[string]string{"EUR": "0"}
	manifest.Columns.Resource, manifest.Columns.Service, manifest.Columns.Tags, manifest.Columns.List = "Resource", "Service", "Tags", "List"
	rows := [][]string{
		{"Account", "Month", "Currency", "Billed", "Effective", "List", "Resource", "Service", "Tags"},
		{"10002", key.Period, "CNY", "0.000000000001", "0.000000000003", "0.000000000005", "vm1", "compute", `{"team":"core"}`},
		{"10002", key.Period, "USD", "-0.000000000001", "-2", "-3", "vm1", "compute", `{"team":"core"}`},
		{"10002", key.Period, "CNY", "10", "", "12", "vm2", "compute", "opaque-tag"},
		{"10002", key.Period, "CNY", "5", "4", "", "vm3", "unknown", ""},
	}
	var csvBody strings.Builder
	if err := csv.NewWriter(&csvBody).WriteAll(rows); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(t.Context(), key, manifest, 0, [][]byte{[]byte(csvBody.String())}); err != nil {
		t.Fatal(err)
	}
	return store, key, AllocationPolicy{Rules: []AllocationRule{
		{Service: "compute", Tags: map[string]json.RawMessage{"team": []byte(`"core"`)},
			Shares: []AllocationShare{{"team-b", 5000}, {"team-a", 5000}}},
		{Service: "compute", Shares: []AllocationShare{{"operations", 10000}}},
	}}
}

func TestAllocationLedgerReplayAndFencing(t *testing.T) {
	store, key, policy := ledgerAllocationFixture(t)
	ctx := t.Context()
	first, err := store.BuildAllocation(ctx, key, 1, 0, policy)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceRows != 4 || len(first.Totals) != 7 || first.InvalidTagRows != 1 || !first.Current || first.Revision != 1 {
		t.Fatalf("unexpected allocation: %+v", first)
	}
	for _, total := range first.Totals {
		switch total.Currency + "/" + total.Center {
		case "CNY/team-a":
			if total.Billed != "0.000000000001" || total.Effective == nil || *total.Effective != "0.000000000002" ||
				total.ListMinusBilled == nil || *total.ListMinusBilled != "0.000000000002" {
				t.Fatalf("rounding mismatch: %+v", total)
			}
		case "CNY/operations":
			if total.Billed != "10.000000000000" || total.Effective != nil || total.ListMinusBilled == nil || *total.ListMinusBilled != "2.000000000000" {
				t.Fatalf("optional basis mismatch: %+v", total)
			}
		case "USD/team-a":
			if total.Billed != "-0.000000000001" {
				t.Fatalf("credit mismatch: %+v", total)
			}
		case "EUR/unallocated":
			if total.Billed != "0.000000000000" || total.Rows != 0 {
				t.Fatalf("control-only currency mismatch: %+v", total)
			}
		}
	}
	verified, err := store.VerifyAllocation(ctx, key, 1, 1)
	if err != nil || !reflect.DeepEqual(first, verified.Allocation) {
		t.Fatalf("replay mismatch: %+v %v", verified, err)
	}
	edit := AllocationPolicy{Default: []AllocationShare{{"replacement", 10000}}}
	if _, err := store.BuildAllocation(ctx, key, 1, 0, edit); !errors.Is(err, ErrAllocationConflict) {
		t.Fatalf("stale allocation update: %v", err)
	}
	if _, err := store.BuildAllocation(ctx, key, 1, 1, edit); err != nil {
		t.Fatal(err)
	}
	second := policy.Rules[0].Shares
	second[0], second[1] = second[1], second[0]
	old, err := store.BuildAllocation(ctx, key, 1, 2, policy)
	if err != nil || !old.Replayed || old.Current || old.Revision != 1 || old.PolicyDigest != first.PolicyDigest {
		t.Fatalf("idempotent replay republished older allocation: %+v %v", old, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.BuildAllocation(cancelled, key, 1, 2, policy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled calculation succeeded: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE cmp_billing_allocation_totals SET billed=billed+1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifyAllocation(ctx, key, 1, 1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("damaged projection passed verification: %v", err)
	}
}

func TestAllocationConcurrentPublish(t *testing.T) {
	store, key, _ := ledgerAllocationFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, center := range []string{"a", "b"} {
		go func() {
			<-start
			_, err := store.BuildAllocation(t.Context(), key, 1, 0, AllocationPolicy{Default: []AllocationShare{{center, 10000}}})
			results <- err
		}()
	}
	close(start)
	a, b := <-results, <-results
	onePublishWon := a == nil && errors.Is(b, ErrAllocationConflict) || b == nil && errors.Is(a, ErrAllocationConflict)
	if !onePublishWon {
		t.Fatalf("one writer must win: %v %v", a, b)
	}
}
