package router

import (
	"net/http"
	"sync"
	"testing"
)

func TestRouteTagsAreDetached(t *testing.T) {
	rt := NewRouter()
	input := []string{"public"}
	if err := rt.AddRoute("GET", "/", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), func(meta *RouteMeta) { meta.Tags = input }); err != nil {
		t.Fatal(err)
	}
	input[0] = "changed-input"
	rt.Freeze()
	snapshot := rt.Routes()
	if snapshot[0].Meta.Tags[0] != "public" {
		t.Fatal("registration retained mutable tags")
	}
	snapshot[0].Meta.Tags[0] = "changed-output"
	if rt.Routes()[0].Meta.Tags[0] != "public" {
		t.Fatal("snapshot retained internal tags")
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				routes := rt.Routes()
				if routes[0].Meta.Tags[0] != "public" {
					t.Error("snapshot changed")
				}
				routes[0].Meta.Tags[0] = "private-copy"
			}
		}()
	}
	wg.Wait()
}
