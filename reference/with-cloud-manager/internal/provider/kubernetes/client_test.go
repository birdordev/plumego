package kubernetes

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

const testToken = "fixture-private-kube-token"

func fixtureConfig(t *testing.T, handler http.Handler) Config {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	return Config{Endpoint: server.URL, CA: string(cert), Token: testToken,
		IdentityNamespace: "anchor", IdentityUID: "anchor-fixture-uid", Namespaces: []string{"work"}}
}

func TestClientRejectsInvalidConfiguration(t *testing.T) {
	cfg := fixtureConfig(t, http.NotFoundHandler())
	for _, endpoint := range []string{"http://example.com", "https://x@host", "https://host:0",
		"https://host:65536", "https://host?q=secret", "https://host#", "https://host/base", "https://host/%2f"} {
		invalid := cfg
		invalid.Endpoint = endpoint
		if _, err := NewClient(invalid); err == nil {
			t.Errorf("invalid Kubernetes endpoint accepted: %q", endpoint)
		}
	}
	for _, modify := range []func(*Config){
		func(c *Config) { c.Token = "fixture\r\nheader" },
		func(c *Config) { c.Namespaces = []string{"work", "work"} },
		func(c *Config) { c.Namespaces = []string{"../work"} },
		func(c *Config) { c.IdentityUID = "" },
		func(c *Config) { c.CA = "invalid-ca" },
	} {
		invalid := cfg
		modify(&invalid)
		if _, err := NewClient(invalid); err == nil {
			t.Fatal("invalid Kubernetes credential profile accepted")
		}
	}
}

func TestClientRefusesRedirectAndHidesRemoteBody(t *testing.T) {
	var redirected atomic.Bool
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Store(true)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer destination.Close()
	cfg := fixtureConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte(testToken))
	}))
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if _, err := client.ReadWorkloads(t.Context()); redirected.Load() || !provider.IsFailure(err, provider.InvalidResponse) ||
		(err != nil && strings.Contains(err.Error(), testToken)) {
		t.Fatalf("unsafe redirect handling: redirect=%t error=%v", redirected.Load(), err)
	}
}
