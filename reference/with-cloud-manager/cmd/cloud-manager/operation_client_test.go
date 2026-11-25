package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOperationsClientOriginAndErrors(t *testing.T) {
	for _, origin := range []string{"http://example.com", "https://person:secret@api.example.com", "https://api.example.com/?key=secret", "//api.example.com", "https://:443"} {
		if _, err := operationClientBase(origin); err == nil {
			t.Fatalf("invalid API address accepted: %s", origin)
		}
	}
	for _, origin := range []string{"https://api.example.com/prefix", "http://127.0.0.1:8096", "http://[::1]:8096"} {
		if _, err := operationClientBase(origin); err != nil {
			t.Fatalf("valid API address rejected: %s, %v", origin, err)
		}
	}
	const token = "fixture-cli-service-token"
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing service authentication")
		}
		switch r.URL.Path {
		case "/prefix/v1/tenants/tenant/projects/project/operations/redirect":
			http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
		case "/prefix/v1/tenants/tenant/projects/project/operations/forbidden":
			w.WriteHeader(http.StatusForbidden)
			if _, err := io.WriteString(w, token); err != nil {
				t.Error(err)
			}
		case "/prefix/v1/tenants/tenant/projects/project/operations/large":
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, strings.Repeat(" ", (1<<20)+1)); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected operation URL %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer tls.Close()
	var output bytes.Buffer
	for _, tc := range []struct{ id, want string }{
		{"redirect", "HTTP 307"},
		{"forbidden", "HTTP 403"},
		{"large", "cannot read operations API response"},
		{"../foreign", "usage:"},
	} {
		args := []string{"get", "tenant", "project", tc.id}
		err := runOperationsClient(t.Context(), args, tls.URL+"/prefix", token, nil, &output, tls.Client().Transport)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), token) || output.Len() != 0 {
			t.Fatalf("invalid client outcome for %s output=%q error=%v", tc.id, output.String(), err)
		}
	}
	if forwarded.Load() != 0 {
		t.Fatal("operation client followed a redirect")
	}
}

func TestOperationsCommandUsesAPICredentials(t *testing.T) {
	t.Setenv("CLOUD_MANAGER_API_URL", "http://127.0.0.1:8096")
	t.Setenv("CLOUD_MANAGER_API_TOKEN", "")
	t.Setenv("CLOUD_MANAGER_DATABASE_URL", "")
	err := run(t.Context(), []string{"operations", "list", "tenant", "project"})
	if err == nil || !strings.Contains(err.Error(), "CLOUD_MANAGER_API_TOKEN") {
		t.Fatalf("operation command did not dispatch directly to the API client: %v", err)
	}
}

func TestOperationSubmissionValidationAndRedirect(t *testing.T) {
	const token = "fixture-operation-submission-token"
	const payload = `{"account":"one","action":"stop"}`
	var attempts, forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer destination.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload || r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Idempotency-Key") != "same-key" || r.Header.Get("If-Match") != `"12"` ||
			r.Header.Get("Authorization") != "Bearer "+token || r.URL.Path != "/v1/tenants/tenant/projects/project/instance-operations" {
			t.Error("unexpected submitted request")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	submit := []string{"submit", "tenant", "project", "--idempotency-key", "same-key", "--if-match", "12"}
	var output bytes.Buffer
	for _, in := range []string{"", "null", `{}` + `{}`, strings.Repeat(" ", 4096) + "{}"} {
		if err := runOperationsClient(t.Context(), submit, origin.URL, token, strings.NewReader(in), &output, origin.Client().Transport); err == nil {
			t.Fatalf("invalid input of length %d was accepted", len(in))
		}
	}
	for _, badArgs := range [][]string{submit[:5], append(append([]string{}, submit[:6]...), "012")} {
		if err := runOperationsClient(t.Context(), badArgs, origin.URL, token, strings.NewReader(payload), &output, origin.Client().Transport); err == nil {
			t.Fatal("invalid if-match was accepted")
		}
	}
	if attempts.Load() != 0 || output.Len() != 0 {
		t.Fatal("locally invalid requests reached the server")
	}
	err := runOperationsClient(t.Context(), submit, origin.URL, token, strings.NewReader(" \n"+payload+"\n"), &output, origin.Client().Transport)
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") || attempts.Load() != 1 || forwarded.Load() != 0 || output.Len() != 0 {
		t.Fatalf("unexpected redirect behavior: attempts=%d forwarded=%d output=%q error=%v", attempts.Load(), forwarded.Load(), output.String(), err)
	}
}
