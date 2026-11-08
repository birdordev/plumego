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
