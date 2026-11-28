package cloudtest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const rolloutWorkerApp = "cloud-manager-rollout-worker-"

func rolloutWorkerProcesses(t *testing.T, dsn, profile string, fixture *powerFixture, hosts []string) func(string, string) func() {
	t.Helper()
	certificate, authority := rolloutProviderCertificate(t, hosts)
	providerServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.serve(t, w, r)
	}))
	providerServer.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	providerServer.StartTLS()
	t.Cleanup(providerServer.Close)
	proxy := httptest.NewServer(rolloutProviderProxy(providerServer.Listener.Addr().String(), hosts))
	t.Cleanup(proxy.Close)

	manifest, err := json.Marshal(map[string]string{"fixture/pitr": profile})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	env := rolloutProcessEnv(dsn, "")
	for i := len(env) - 1; i >= 0; i-- {
		key, _, _ := strings.Cut(env[i], "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUEST_METHOD":
			env = append(env[:i], env[i+1:]...)
		}
	}
	env = append(env, "HTTP_PROXY="+proxy.URL, "HTTPS_PROXY="+proxy.URL, "NO_PROXY=",
		"SSL_CERT_FILE="+authority, "SSL_CERT_DIR="+t.TempDir(),
		"CLOUD_MANAGER_WORKER_PROFILES="+path)
	return func(binary, mode string) func() {
		processEnv := append(slices.Clone(env), "CLOUD_MANAGER_WORKER_MODE="+mode, "PGAPPNAME="+rolloutWorkerApp+mode)
		stop, _ := startRolloutProcess(t, binary, "worker", processEnv)
		return stop
	}
}

func waitRolloutObservationPoll(t *testing.T, db *sql.DB) {
	t.Helper()
	pitrWait(t, "observation worker queue poll", func(ctx context.Context) (bool, error) {
		var observed bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='idle' AND lower(query)='commit'
		)`, rolloutWorkerApp+"observe-only").Scan(&observed)
		return observed, err
	})
}

func assertUnclaimedRolloutTask(t *testing.T, db *sql.DB, operation string, wantDue bool) {
	t.Helper()
	var status string
	var due bool
	var attempts int
	err := db.QueryRowContext(t.Context(), "SELECT status,available_at<=clock_timestamp(),attempts FROM cmp_operation_tasks WHERE id=$1",
		operation).Scan(&status, &due, &attempts)
	if err != nil || status != "queued" || due != wantDue || attempts != 0 {
		t.Fatalf("rollout task was not retained unclaimed: status=%s due=%t want due=%t attempts=%d error=%v", status, due, wantDue, attempts, err)
	}
}

func rolloutProviderCertificate(t *testing.T, hosts []string) (tls.Certificate, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		DNSNames:              hosts,
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}, path
}

func rolloutProviderProxy(target string, hosts []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, err := net.SplitHostPort(r.Host)
		if r.Method != http.MethodConnect || err != nil || port != "443" || !slices.Contains(hosts, host) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		dial := net.Dialer{Timeout: time.Second}
		local, err := dial.DialContext(r.Context(), "tcp", target)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer func() { _ = local.Close() }()
		client, buffered, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = io.Copy(local, buffered)
			_ = local.Close()
		}()
		_, _ = io.Copy(client, local)
		_ = client.Close()
		<-done
	})
}
