package gateway_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/birdordev/plumego/router"
	"github.com/birdordev/plumego/x/gateway"
)

func ExampleRegisterProxy() {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "backend path: %s", r.URL.Path)
	}))
	defer backend.Close()

	r := router.NewRouter()

	_, err := gateway.RegisterProxy(r, "/bad", gateway.GatewayConfig{})
	fmt.Println("invalid config:", err != nil)

	proxy, err := gateway.RegisterProxy(r, "/edge", gateway.GatewayConfig{
		Targets: []string{backend.URL},
	})
	if err != nil {
		fmt.Println("register proxy:", err)
		return
	}
	defer proxy.Close()

	r.Freeze()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/edge", nil)
	r.ServeHTTP(rec, req)

	fmt.Println("status:", rec.Code)
	fmt.Println(rec.Body.String())

	// Output:
	// invalid config: true
	// status: 200
	// backend path: /edge
}

func ExampleNew() {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "forwarded:", r.URL.Path)
	}))
	defer backend.Close()

	proxy, err := gateway.New(gateway.Config{Targets: []string{backend.URL}})
	if err != nil {
		fmt.Println("construct proxy:", err)
		return
	}
	defer proxy.Close()

	routes := router.NewRouter()
	if err := routes.AddRoute(http.MethodGet, "/edge/*path", proxy); err != nil {
		fmt.Println("register proxy:", err)
		return
	}
	routes.Freeze()

	response := httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/edge/item", nil))
	fmt.Println("status:", response.Code)
	fmt.Print(response.Body.String())

	// Output:
	// status: 200
	// forwarded: /edge/item
}
