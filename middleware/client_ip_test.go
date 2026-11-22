package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/birdordev/plumego/middleware"
)

func TestTrustedProxyClientIP(t *testing.T) {
	resolve, err := middleware.TrustedProxyClientIP(netip.MustParsePrefix("192.0.2.0/24"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		peer string
		hops []string
		want string
	}{
		{"198.51.100.12:1234", []string{"192.0.2.99"}, "198.51.100.12"},
		{"192.0.2.1:1234", []string{"203.0.113.20", "198.51.100.12,192.0.2.2"}, "198.51.100.12"},
		{"192.0.2.1:1234", []string{"198.51.100.12, unknown"}, "192.0.2.1"},
		{"[::ffff:192.0.2.1]:1234", []string{"2001:db8::34"}, "2001:db8::34"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.peer
		r.Header["X-Forwarded-For"] = tc.hops
		if got := resolve(r); got != tc.want {
			t.Errorf("resolver returned %q for %q, want %q", got, tc.peer, tc.want)
		}
	}
}
