package config

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/birdordev/plumego/log"
)

type watchResult struct {
	old     string
	new     string
	current string
}

type immediateWatchSource struct {
	name string
	data map[string]any
}

func (s immediateWatchSource) Load(context.Context) (map[string]any, error) {
	return s.data, nil
}

func (s immediateWatchSource) Watch(context.Context) <-chan WatchResult {
	results := make(chan WatchResult, 1)
	results <- WatchResult{Data: s.data}
	close(results)
	return results
}

func (s immediateWatchSource) Name() string {
	return s.name
}

func TestConfigWatchersReceiveUpdates(t *testing.T) {
	cm := NewManager(log.NewLogger())
	cm.data["foo"] = "old"

	resultCh := make(chan watchResult, 1)
	globalCh := make(chan map[string]any, 1)

	if err := cm.Watch("foo", func(old, new any) {
		resultCh <- watchResult{
			old:     fmt.Sprint(old),
			new:     fmt.Sprint(new),
			current: cm.Get("foo"),
		}
	}); err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if err := cm.WatchGlobal(func(_, new any) {
		if data, ok := new.(map[string]any); ok {
			globalCh <- data
			return
		}
		globalCh <- map[string]any{}
	}); err != nil {
		t.Fatalf("WatchGlobal failed: %v", err)
	}

	cm.handleSourceUpdate("test", map[string]any{"foo": "new"})

	deadline := time.After(200 * time.Millisecond)
	var gotResult bool
	var gotGlobal bool

	for !gotResult || !gotGlobal {
		select {
		case res := <-resultCh:
			gotResult = true
			if res.old != "old" || res.new != "new" || res.current != "new" {
				t.Fatalf("unexpected watch result: %+v", res)
			}
		case data := <-globalCh:
			gotGlobal = true
			if data["foo"] != "new" {
				t.Fatalf("unexpected global data: %v", data["foo"])
			}
		case <-deadline:
			t.Fatal("watchers did not fire in time")
		}
	}
}

func TestStartWatchersAppliesImmediateResult(t *testing.T) {
	cm := NewManager(log.NewLogger())
	cm.data["foo"] = "old"
	if err := cm.AddSource(immediateWatchSource{
		name: "immediate",
		data: map[string]any{"foo": "new"},
	}); err != nil {
		t.Fatalf("AddSource failed: %v", err)
	}

	resultCh := make(chan watchResult, 1)
	if err := cm.Watch("foo", func(old, new any) {
		resultCh <- watchResult{
			old:     fmt.Sprint(old),
			new:     fmt.Sprint(new),
			current: cm.Get("foo"),
		}
	}); err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	if err := cm.StartWatchers(t.Context()); err != nil {
		t.Fatalf("StartWatchers failed: %v", err)
	}

	select {
	case res := <-resultCh:
		if res.old != "old" || res.new != "new" || res.current != "new" {
			t.Fatalf("unexpected watch result: %+v", res)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("immediate watch result was not applied")
	}
}

func TestConfigWatchersNormalizeKeys(t *testing.T) {
	cm := NewManager(log.NewLogger())
	cm.data["foo_bar"] = "old"

	resultCh := make(chan watchResult, 1)
	if err := cm.Watch("FooBar", func(old, new any) {
		resultCh <- watchResult{
			old:     fmt.Sprint(old),
			new:     fmt.Sprint(new),
			current: cm.Get("foo_bar"),
		}
	}); err != nil {
		t.Fatalf("Watch failed: %v", err)
	}

	cm.handleSourceUpdate("test", map[string]any{"FOO_BAR": "new"})

	select {
	case res := <-resultCh:
		if res.old != "old" || res.new != "new" || res.current != "new" {
			t.Fatalf("unexpected watch result: %+v", res)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watchers did not fire in time")
	}
}
