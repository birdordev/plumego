package contract

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestTraceContextValidityHelpers(t *testing.T) {
	validTraceID := "1234567890abcdef1234567890abcdef"
	validSpanID := "1234567890abcdef"

	tests := []struct {
		name      string
		tc        TraceContext
		wantTrace bool
		wantSpan  bool
		wantValid bool
	}{
		{
			name:      "full valid context",
			tc:        TraceContext{TraceID: validTraceID, SpanID: validSpanID},
			wantTrace: true,
			wantSpan:  true,
			wantValid: true,
		},
		{
			name:      "mixed case",
			tc:        TraceContext{TraceID: "1234567890AbCdEf1234567890ABCDEF", SpanID: "1234567890ABCDEF"},
			wantTrace: true,
			wantSpan:  true,
			wantValid: true,
		},
		{
			name: "all zero ids",
			tc:   TraceContext{TraceID: strings.Repeat("0", 32), SpanID: strings.Repeat("0", 16)},
		},
		{
			name: "overlong ids",
			tc:   TraceContext{TraceID: validTraceID + "0", SpanID: validSpanID + "0"},
		},
		{
			name: "non-ASCII ids with exact byte lengths",
			tc:   TraceContext{TraceID: strings.Repeat("a", 30) + "é", SpanID: strings.Repeat("a", 14) + "é"},
		},
		{
			name:      "trace only",
			tc:        TraceContext{TraceID: validTraceID},
			wantTrace: true,
		},
		{
			name:     "span only",
			tc:       TraceContext{SpanID: validSpanID},
			wantSpan: true,
		},
		{
			name: "malformed ids",
			tc:   TraceContext{TraceID: "not-a-trace", SpanID: "not-span"},
		},
		{
			name: "empty context",
			tc:   TraceContext{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tc.HasTraceID(); got != tt.wantTrace {
				t.Fatalf("HasTraceID() = %v, want %v", got, tt.wantTrace)
			}
			if got := tt.tc.HasSpanID(); got != tt.wantSpan {
				t.Fatalf("HasSpanID() = %v, want %v", got, tt.wantSpan)
			}
			if got := tt.tc.Valid(); got != tt.wantValid {
				t.Fatalf("Valid() = %v, want %v", got, tt.wantValid)
			}
		})
	}
}

func TestTraceContextManagement(t *testing.T) {
	var originalCtx context.Context
	traceContext := TraceContext{
		TraceID: "test-trace",
		SpanID:  "test-span",
		Flags:   traceFlagsSampled,
		Baggage: map[string]string{
			"user.id":    "123",
			"request.id": "abc",
		},
	}

	ctx := WithTraceContext(originalCtx, traceContext)
	retrieved, ok := TraceContextFromContext(ctx)
	if !ok {
		t.Fatalf("expected trace context to be retrieved")
	}
	if retrieved.TraceID != "test-trace" {
		t.Fatalf("expected trace ID to match")
	}
	if retrieved.SpanID != "test-span" {
		t.Fatalf("expected span ID to match")
	}
	if retrieved.Flags != traceFlagsSampled {
		t.Fatalf("expected flags to match")
	}
	if !retrieved.IsSampled() {
		t.Fatalf("expected sampled flag to be true")
	}
	if retrieved.Baggage["user.id"] != "123" {
		t.Fatalf("expected baggage to be preserved")
	}
}

func TestTraceContextFromContextNilSafe(t *testing.T) {
	if _, ok := TraceContextFromContext(nil); ok {
		t.Fatalf("expected no trace context for nil context")
	}
	if _, ok := TraceContextFromContext(t.Context()); ok {
		t.Fatalf("expected no trace context for empty context")
	}
}

func TestTraceContextUsesDefensiveCopies(t *testing.T) {
	parent := "1111111111111111"
	baggage := map[string]string{"user.id": "123"}
	ctx := WithTraceContext(t.Context(), TraceContext{
		TraceID:      "trace-abc",
		SpanID:       "2222222222222222",
		ParentSpanID: &parent,
		Baggage:      baggage,
	})

	parent = "3333333333333333"
	baggage["user.id"] = "mutated"

	got, ok := TraceContextFromContext(ctx)
	if !ok {
		t.Fatal("expected TraceContext")
	}
	if got.ParentSpanID == nil || *got.ParentSpanID != "1111111111111111" {
		t.Fatalf("expected stored parent span to be isolated, got %#v", got.ParentSpanID)
	}
	if got.Baggage["user.id"] != "123" {
		t.Fatalf("expected stored baggage to be isolated, got %#v", got.Baggage)
	}

	*got.ParentSpanID = "4444444444444444"
	got.Baggage["user.id"] = "returned-mutated"

	again, ok := TraceContextFromContext(ctx)
	if !ok {
		t.Fatal("expected TraceContext on second lookup")
	}
	if again.ParentSpanID == nil || *again.ParentSpanID != "1111111111111111" {
		t.Fatalf("expected returned parent span mutation to be isolated, got %#v", again.ParentSpanID)
	}
	if again.Baggage["user.id"] != "123" {
		t.Fatalf("expected returned baggage mutation to be isolated, got %#v", again.Baggage)
	}
}

func TestTraceContextPreservesEmptyBaggageShape(t *testing.T) {
	for _, baggage := range []map[string]string{nil, {}} {
		ctx := WithTraceContext(t.Context(), TraceContext{Baggage: baggage})
		if baggage != nil {
			baggage["caller"] = "value"
		}

		got, ok := TraceContextFromContext(ctx)
		if !ok || (got.Baggage == nil) != (baggage == nil) || len(got.Baggage) != 0 {
			t.Fatalf("stored = %v, baggage = %#v, input = %#v", ok, got.Baggage, baggage)
		}
		if got.Baggage != nil {
			got.Baggage["reader"] = "value"
			again, _ := TraceContextFromContext(ctx)
			if len(again.Baggage) != 0 {
				t.Fatalf("reader changed stored baggage: %#v", again.Baggage)
			}
		}
	}
}

func TestTraceContextStableCarrierFields(t *testing.T) {
	rt := reflect.TypeOf(TraceContext{})
	want := []string{"TraceID", "SpanID", "ParentSpanID", "Baggage", "Flags"}
	if rt.NumField() != len(want) {
		t.Fatalf("TraceContext field count = %d, want %d", rt.NumField(), len(want))
	}
	for i, name := range want {
		if got := rt.Field(i).Name; got != name {
			t.Fatalf("TraceContext field %d = %s, want %s", i, got, name)
		}
	}
}

func TestWithTraceContextDoesNotValidateCarrierPolicy(t *testing.T) {
	ctx := WithTraceContext(t.Context(), TraceContext{
		TraceID: "caller-provided-trace",
		SpanID:  "caller-provided-span",
		Baggage: map[string]string{
			"":                          "kept",
			"oversized-or-policy-owned": strings.Repeat("x", 128),
		},
	})

	got, ok := TraceContextFromContext(ctx)
	if !ok {
		t.Fatal("expected TraceContext")
	}
	if got.Valid() {
		t.Fatalf("invalid caller-provided identifiers should remain carrier data, got valid context: %+v", got)
	}
	if got.Baggage[""] != "kept" {
		t.Fatalf("baggage policy should not be enforced by contract, got %+v", got.Baggage)
	}
	if got.Baggage["oversized-or-policy-owned"] != strings.Repeat("x", 128) {
		t.Fatalf("baggage value should round-trip without policy enforcement, got %+v", got.Baggage)
	}
}

func TestTraceContextReadPatternRequiresValid(t *testing.T) {
	invalid := WithTraceContext(t.Context(), TraceContext{
		TraceID: "not-a-trace-id",
		SpanID:  "not-a-span-id",
	})
	got, ok := TraceContextFromContext(invalid)
	if !ok {
		t.Fatal("expected invalid carrier data to be returned for caller inspection")
	} else if got.Valid() {
		t.Fatalf("caller must not treat invalid carrier data as valid trace context: %+v", got)
	}

	valid := WithTraceContext(t.Context(), TraceContext{
		TraceID: "1234567890abcdef1234567890abcdef",
		SpanID:  "1234567890abcdef",
	})
	if got, ok := TraceContextFromContext(valid); !ok || !got.Valid() {
		t.Fatalf("expected valid trace context after caller-provided valid ids, got %+v", got)
	}
}

func BenchmarkTraceBaggageCopies(b *testing.B) {
	for _, size := range []int{2, 64} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			baggage := make(map[string]string, size)
			for i := range size {
				baggage[fmt.Sprint(i)] = "fixture"
			}
			trace := TraceContext{Baggage: baggage}
			b.ReportAllocs()
			for b.Loop() {
				ctx := WithTraceContext(b.Context(), trace)
				copied, _ := TraceContextFromContext(ctx)
				if len(copied.Baggage) != size {
					b.Fatal("incomplete copy")
				}
				copied.Baggage["0"] = "new"
				if baggage["0"] != "fixture" {
					b.Fatal("copy shared input baggage")
				}
			}
		})
	}
}

func TestSiblingTraceSnapshotsKeepSamplingAndBaggageSeparate(t *testing.T) {
	parent := WithRequestID(t.Context(), "fixture")
	baggage := map[string]string{"owner": "first"}
	first := WithTraceContext(parent, TraceContext{Flags: traceFlagsSampled, Baggage: baggage})
	baggage["owner"] = "second"
	second := WithTraceContext(parent, TraceContext{Baggage: baggage})
	baggage["owner"] = "changed"
	a, okA := TraceContextFromContext(first)
	b, okB := TraceContextFromContext(second)
	if !okA || !okB || !a.IsSampled() || b.IsSampled() ||
		a.Baggage["owner"] != "first" || b.Baggage["owner"] != "second" ||
		RequestIDFromContext(first) != "fixture" || RequestIDFromContext(second) != "fixture" {
		t.Fatalf("first=%+v second=%+v", a, b)
	}
}
