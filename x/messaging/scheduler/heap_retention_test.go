package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestScheduleHeapBoundsReplacementsAndFarFutureCancellations(t *testing.T) {
	now := time.Date(2036, time.March, 1, 0, 0, 0, 0, time.UTC)
	s := New(WithClock(fixedSchedulerClock{now: now}))
	run := func(context.Context) error { return nil }
	id, err := s.AddCron("cron", "0 0 1 1 *", run)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TriggerNow(id); err != nil {
		t.Fatal(err)
	}
	const replacements = 5000
	for i := range replacements {
		expression := "0 0 1 1 *"
		if i%2 == 1 {
			expression = "0 0 2 1 *"
		}
		if err := s.UpdateCron(id, expression); err != nil {
			t.Fatal(err)
		}
	}
	if count := s.queue.Len(); count != 2 {
		t.Fatalf("%d replacements retained %d heap entries, want the planned and manual occurrences", replacements, count)
	}
	if run := s.popDue(now); run == nil || run.job.id != id || run.version != 0 || s.queue.Len() != 1 {
		t.Fatalf("calendar updates invalidated manual dispatch: %+v", run)
	}
	const future = 4096
	keys := make([]JobID, 0, future)
	for i := range future {
		id, err := s.Schedule(JobID(fmt.Sprintf("future-%d", i)), now.Add(time.Hour*8760+time.Duration(i)*time.Second), run)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, id)
	}
	peak := cap(s.queue)
	for _, id := range keys {
		if !s.Cancel(id) {
			t.Fatalf("cancel %s", id)
		}
	}
	if len(s.queue) != 1 || cap(s.queue) >= 256 {
		t.Fatalf("future cancellations retained heap entries=%d capacity=%d (peak=%d)", len(s.queue), cap(s.queue), peak)
	}
	for _, stale := range s.queue[:cap(s.queue)][1:] {
		if stale != nil {
			t.Fatal("removed heap node remains referenced by unused capacity")
		}
	}
	t.Logf("%d calendar replacements kept one planned node; %d far-future cancellations shrank heap capacity from %d to %d",
		replacements, future, peak, cap(s.queue))
	for range 16 {
		if err := s.TriggerNow(id); err != nil {
			t.Fatal(err)
		}
	}
	if !s.Cancel(id) || len(s.queue) != 0 || s.jobs[id].manual != nil || s.jobs[id].planned != nil {
		t.Fatal("canceled job retained manual or planned heap nodes")
	}
}
