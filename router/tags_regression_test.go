package router

import (
	"net/http"
	"sync"
	"testing"
)

func TestRouteTagsAreDetached(t *testing.T) {
	rt := NewRouter()
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	input := []string{"public"}
	if err := rt.AddRoute("GET", "/", handler, func(meta *RouteMeta) { meta.Tags = input }); err != nil {
		t.Fatal(err)
	}
	input[0] = "changed-input"
	snapshot := rt.Routes()
	if snapshot[0].Meta.Tags[0] != "public" {
		t.Fatal("registration retained mutable tags")
	}
	snapshot[0].Meta.Tags[0] = "changed-output"
	if rt.Routes()[0].Meta.Tags[0] != "public" {
		t.Fatal("snapshot retained internal tags")
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Go(func() {
		<-start
		if err := rt.AddRoute("GET", "/later", handler); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			<-start
			for j := 0; j < 50; j++ {
				routes := rt.Routes()
				if routes[0].Meta.Tags[0] != "public" {
					t.Error("snapshot changed")
				}
				routes[0].Meta.Tags[0] = "private-copy"
			}
		})
	}
	close(start)
	wg.Wait()
	rt.Freeze()
	if routes := rt.Routes(); len(routes) != 2 || routes[0].Path != "/" || routes[1].Path != "/later" {
		t.Fatalf("unexpected routes after registration and freeze: %v", routes)
	}
}
