package discovery

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestNewStatic(t *testing.T) {
	services := map[string][]string{
		"svc-a": {"http://a1:8080", "http://a2:8080"},
		"svc-b": {"http://b1:9090"},
	}
	s := NewStatic(services)
	if s == nil {
		t.Fatal("NewStatic returned nil")
	}
	if len(s.services) != 2 {
		t.Errorf("services count = %d, want 2", len(s.services))
	}
}

func TestNewStatic_Nil(t *testing.T) {
	s := NewStatic(nil)
	if s == nil {
		t.Fatal("NewStatic(nil) returned nil")
	}
	// NewStatic(nil) must initialize an empty map to prevent nil map panics on write
	if s.services == nil {
		t.Error("services is nil; NewStatic(nil) must initialize an empty map")
	}
	if len(s.services) != 0 {
		t.Errorf("services len = %d, want 0", len(s.services))
	}
}

func TestNewStatic_Nil_AddBackend(t *testing.T) {
	s := NewStatic(nil)
	// Must not panic when writing to a previously-nil-initialized static
	s.AddBackend("svc", "http://host:8080")

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 1 || backends[0] != "http://host:8080" {
		t.Errorf("backends = %v, want [http://host:8080]", backends)
	}
}

func TestNewStatic_Empty(t *testing.T) {
	s := NewStatic(map[string][]string{})
	if s == nil {
		t.Fatal("NewStatic returned nil")
	}
	if len(s.services) != 0 {
		t.Errorf("services count = %d, want 0", len(s.services))
	}
}

func TestStatic_Resolve_Found(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080", "http://host2:8080"},
	})

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("Resolve() returned %d backends, want 2", len(backends))
	}
	if backends[0] != "http://host1:8080" || backends[1] != "http://host2:8080" {
		t.Errorf("Resolve() = %v, want [http://host1:8080 http://host2:8080]", backends)
	}
}

func TestStatic_Resolve_NotFound(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080"},
	})

	_, err := s.Resolve(t.Context(), "missing")
	if err != ErrServiceNotFound {
		t.Errorf("Resolve() error = %v, want ErrServiceNotFound", err)
	}
}

func TestStatic_Resolve_EmptyBackends(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {},
	})

	_, err := s.Resolve(t.Context(), "svc")
	if err != ErrNoInstances {
		t.Errorf("Resolve() error = %v, want ErrNoInstances", err)
	}
}

func TestStatic_Resolve_ReturnsCopy(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080", "http://host2:8080"},
	})

	backends1, _ := s.Resolve(t.Context(), "svc")
	backends1[0] = "modified"

	backends2, _ := s.Resolve(t.Context(), "svc")
	if backends2[0] != "http://host1:8080" {
		t.Errorf("Resolve() did not return a copy; original was modified to %q", backends2[0])
	}
}

func TestStatic_Resolve_SingleBackend(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://only:8080"},
	})

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 1 || backends[0] != "http://only:8080" {
		t.Errorf("Resolve() = %v, want [http://only:8080]", backends)
	}
}

func TestStatic_Watch_Success(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080"},
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, err := s.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	select {
	case backends := <-ch:
		if len(backends) != 1 || backends[0] != "http://host1:8080" {
			t.Errorf("Watch() initial = %v, want [http://host1:8080]", backends)
		}
	case <-time.After(time.Second):
		t.Fatal("Watch() timed out waiting for initial state")
	}
}

func TestStatic_Watch_NotFound(t *testing.T) {
	s := NewStatic(map[string][]string{})

	ctx := t.Context()
	_, err := s.Watch(ctx, "missing")
	if err != ErrServiceNotFound {
		t.Errorf("Watch() error = %v, want ErrServiceNotFound", err)
	}
}

func TestStatic_Watch_EmptyBackends(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {},
	})

	ctx := t.Context()
	_, err := s.Watch(ctx, "svc")
	if err != ErrNoInstances {
		t.Errorf("Watch() error = %v, want ErrNoInstances", err)
	}
}

func TestStatic_Watch_ContextCancel(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080"},
	})

	ctx, cancel := context.WithCancel(t.Context())
	ch, err := s.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("Watch() error = %v", err)
	}

	// Drain initial value
	<-ch

	// Cancel context
	cancel()

	// Channel should eventually close
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel to be closed after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after context cancel")
	}
}

func TestStatic_Register(t *testing.T) {
	s := NewStatic(map[string][]string{})
	err := s.Register(t.Context(), Instance{})
	if err != ErrNotSupported {
		t.Errorf("Register() = %v, want ErrNotSupported", err)
	}
}

func TestStatic_Deregister(t *testing.T) {
	s := NewStatic(map[string][]string{})
	err := s.Deregister(t.Context(), "id")
	if err != ErrNotSupported {
		t.Errorf("Deregister() = %v, want ErrNotSupported", err)
	}
}

func TestStatic_Health(t *testing.T) {
	s := NewStatic(map[string][]string{})
	err := s.Health(t.Context(), "id", true)
	if err != ErrNotSupported {
		t.Errorf("Health() = %v, want ErrNotSupported", err)
	}
}

func TestStatic_Close(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080"},
	})
	if err := s.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestStatic_UpdateService(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://old:8080"},
	})

	s.UpdateService("svc", []string{"http://new1:8080", "http://new2:8080"})

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("got %d backends, want 2", len(backends))
	}
	if backends[0] != "http://new1:8080" || backends[1] != "http://new2:8080" {
		t.Errorf("UpdateService() backends = %v", backends)
	}
}

func TestStatic_UpdateService_NewService(t *testing.T) {
	s := NewStatic(map[string][]string{})

	s.UpdateService("new-svc", []string{"http://host:8080"})

	backends, err := s.Resolve(t.Context(), "new-svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 1 || backends[0] != "http://host:8080" {
		t.Errorf("backends = %v, want [http://host:8080]", backends)
	}
}

func TestStatic_RemoveService(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host:8080"},
	})

	s.RemoveService("svc")

	_, err := s.Resolve(t.Context(), "svc")
	if err != ErrServiceNotFound {
		t.Errorf("Resolve() after remove = %v, want ErrServiceNotFound", err)
	}
}

func TestStatic_RemoveService_NonExistent(t *testing.T) {
	s := NewStatic(map[string][]string{})

	// Should not panic
	s.RemoveService("nonexistent")
}

func TestStatic_AddBackend_ExistingService(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080"},
	})

	s.AddBackend("svc", "http://host2:8080")

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("got %d backends, want 2", len(backends))
	}
	if backends[1] != "http://host2:8080" {
		t.Errorf("added backend = %q, want %q", backends[1], "http://host2:8080")
	}
}

func TestStatic_AddBackend_NewService(t *testing.T) {
	s := NewStatic(map[string][]string{})

	s.AddBackend("new-svc", "http://host:8080")

	backends, err := s.Resolve(t.Context(), "new-svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 1 || backends[0] != "http://host:8080" {
		t.Errorf("backends = %v, want [http://host:8080]", backends)
	}
}

func TestStatic_RemoveBackend(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080", "http://host2:8080", "http://host3:8080"},
	})

	s.RemoveBackend("svc", "http://host2:8080")

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 2 {
		t.Fatalf("got %d backends, want 2", len(backends))
	}
	if backends[0] != "http://host1:8080" || backends[1] != "http://host3:8080" {
		t.Errorf("backends = %v, want [http://host1:8080 http://host3:8080]", backends)
	}
}

func TestStatic_RemoveBackend_NonExistentService(t *testing.T) {
	s := NewStatic(map[string][]string{})

	// Should not panic
	s.RemoveBackend("nonexistent", "http://host:8080")
}

func TestStatic_RemoveBackend_NonExistentBackend(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080"},
	})

	s.RemoveBackend("svc", "http://missing:8080")

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 1 {
		t.Errorf("got %d backends, want 1 (should not have removed anything)", len(backends))
	}
}

func TestStatic_RemoveBackend_LastBackend(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host:8080"},
	})

	s.RemoveBackend("svc", "http://host:8080")

	_, err := s.Resolve(t.Context(), "svc")
	if err != ErrNoInstances {
		t.Errorf("Resolve() after removing last backend = %v, want ErrNoInstances", err)
	}
}

func TestStatic_Services(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc-a": {"http://a:8080"},
		"svc-b": {"http://b:8080"},
		"svc-c": {"http://c:8080"},
	})

	services := s.Services()
	if len(services) != 3 {
		t.Fatalf("Services() returned %d services, want 3", len(services))
	}

	sort.Strings(services)
	expected := []string{"svc-a", "svc-b", "svc-c"}
	for i, name := range services {
		if name != expected[i] {
			t.Errorf("Services()[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestStatic_Services_Empty(t *testing.T) {
	s := NewStatic(map[string][]string{})

	services := s.Services()
	if len(services) != 0 {
		t.Errorf("Services() = %v, want empty", services)
	}
}

func TestStatic_Services_AfterModification(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc-a": {"http://a:8080"},
	})

	s.UpdateService("svc-b", []string{"http://b:8080"})
	s.RemoveService("svc-a")

	services := s.Services()
	if len(services) != 1 || services[0] != "svc-b" {
		t.Errorf("Services() = %v, want [svc-b]", services)
	}
}

func TestStatic_ConcurrentResolve(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080", "http://host2:8080"},
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			backends, err := s.Resolve(t.Context(), "svc")
			if err != nil {
				t.Errorf("Resolve() error = %v", err)
				return
			}
			if len(backends) != 2 {
				t.Errorf("Resolve() returned %d backends, want 2", len(backends))
			}
		}()
	}
	wg.Wait()
}

func TestStatic_ConcurrentModify(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host:8080"},
	})

	var wg sync.WaitGroup

	// Concurrent reads
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Resolve(t.Context(), "svc")
		}()
	}

	// Concurrent writes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			switch n % 4 {
			case 0:
				s.AddBackend("svc", "http://new:8080")
			case 1:
				s.RemoveBackend("svc", "http://new:8080")
			case 2:
				s.UpdateService("svc", []string{"http://host:8080"})
			case 3:
				s.Services()
			}
		}(i)
	}

	wg.Wait()
}

func TestStatic_AddBackend_Multiple(t *testing.T) {
	s := NewStatic(map[string][]string{})

	s.AddBackend("svc", "http://host1:8080")
	s.AddBackend("svc", "http://host2:8080")
	s.AddBackend("svc", "http://host3:8080")

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 3 {
		t.Fatalf("got %d backends, want 3", len(backends))
	}
}

func TestStatic_UpdateService_ToEmpty(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host:8080"},
	})

	s.UpdateService("svc", []string{})

	_, err := s.Resolve(t.Context(), "svc")
	if err != ErrNoInstances {
		t.Errorf("Resolve() after update to empty = %v, want ErrNoInstances", err)
	}
}

func TestStatic_RemoveBackend_FirstElement(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080", "http://host2:8080"},
	})

	s.RemoveBackend("svc", "http://host1:8080")

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 1 || backends[0] != "http://host2:8080" {
		t.Errorf("backends = %v, want [http://host2:8080]", backends)
	}
}

func TestStatic_RemoveBackend_LastElement(t *testing.T) {
	s := NewStatic(map[string][]string{
		"svc": {"http://host1:8080", "http://host2:8080"},
	})

	s.RemoveBackend("svc", "http://host2:8080")

	backends, err := s.Resolve(t.Context(), "svc")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(backends) != 1 || backends[0] != "http://host1:8080" {
		t.Errorf("backends = %v, want [http://host1:8080]", backends)
	}
}
