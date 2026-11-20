package rest

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestBatchProcessorLimitAndPartialFailures(t *testing.T) {
	processor := NewBatchProcessor(3)
	ctx := t.Context()
	var calls []any
	fail := errors.New("processing failed")
	run := func(gotCtx context.Context, item any) error {
		if gotCtx != ctx {
			t.Fatal("callback received a different context")
		}
		calls = append(calls, item)
		if item == "bad" {
			return fail
		}
		return nil
	}

	tooLarge := processor.Process(ctx, []any{"first", "bad", "third", "extra"}, run)
	if len(calls) != 0 || tooLarge.Total != 4 || tooLarge.Failed != 4 || tooLarge.Successful != 0 ||
		len(tooLarge.Errors) != 1 || tooLarge.Errors[0].Code != "BATCH_TOO_LARGE" {
		t.Fatalf("over capacity = %+v, calls = %v", tooLarge, calls)
	}
	items := []any{"first", "bad", "third"}
	partial := processor.Process(ctx, items, run)
	if !slices.Equal(calls, items) || partial.Total != 3 || partial.Successful != 2 || partial.Failed != 1 ||
		len(partial.Errors) != 1 || partial.Errors[0] != (BatchError{Index: 1, Code: "PROCESSING_ERROR", Message: fail.Error()}) {
		t.Fatalf("partial = %+v, calls = %v", partial, calls)
	}
}
