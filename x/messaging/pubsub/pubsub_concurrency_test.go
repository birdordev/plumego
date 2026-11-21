package pubsub

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestPubSub_ConcurrentPublish_NoPanic(t *testing.T) {
	ps := New()
	defer ps.Close()

	sub, err := ps.Subscribe(t.Context(), "t", SubOptions{BufferSize: 64, Policy: DropOldest})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Cancel()

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = ps.Publish("t", Message{ID: "x"})
			}
		}()
	}
	wg.Wait()

	// Ensure we can still receive without deadlock
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case <-sub.C():
			return
		case <-timeout:
			t.Fatalf("no message received")
		}
	}
}

func TestWorkerPoolTrySubmitRespectsCapacityAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool := newWorkerPool(1)
		defer pool.Close()
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
		defer releaseWorker()

		var runs int
		if !pool.TrySubmit(func() { <-release; runs++ }) {
			t.Fatal("empty pool rejected a task")
		}
		rejected := make(chan bool, 1)
		go func() {
			rejected <- pool.TrySubmit(func() { t.Error("full pool ran a rejected task") })
		}()
		synctest.Wait()
		select {
		case accepted := <-rejected:
			if accepted {
				t.Error("full pool accepted a task")
			}
		default:
			t.Error("TrySubmit blocked with a full pool")
		}

		releaseWorker()
		pool.Wait()
		if !pool.TrySubmit(func() { runs++ }) {
			t.Fatal("released slot could not be reused")
		}
		pool.Close()
		if runs != 2 {
			t.Errorf("tasks ran %d times; want two", runs)
		}
		if pool.TrySubmit(func() { t.Error("closed pool ran a task") }) {
			t.Error("closed pool accepted a task")
		}
	})
}
