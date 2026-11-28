package testsupport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func DecodeProjectData(t *testing.T, w *httptest.ResponseRecorder, output any) {
	t.Helper()
	var envelope struct{ Data json.RawMessage }
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope.Data, output); err != nil {
		t.Fatal(err)
	}
}

type consolePKI struct {
	roots                     *x509.CertPool
	CaFile, CertFile, KeyFile string
	Gateway, WrongDNS         tls.Certificate
}

func NewConsolePKI(t *testing.T, gatewayDNS string) consolePKI {
	t.Helper()
	now := time.Now()
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true,
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, &ca, &ca, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(serial int64, dns string, use x509.ExtKeyUsage) tls.Certificate {
		t.Helper()
		public, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter,
			DNSNames: []string{dns}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{use}}
		if use == x509.ExtKeyUsageServerAuth {
			template.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
		}
		der, err := x509.CreateCertificate(rand.Reader, &template, parsed, public, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "server.pem"), filepath.Join(dir, "key.pem")
	caFile := filepath.Join(dir, "ca.pem")
	server := issue(2, "localhost", x509.ExtKeyUsageServerAuth)
	key, err := x509.MarshalPKCS8PrivateKey(server.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return consolePKI{roots: roots, CaFile: caFile, CertFile: certFile, KeyFile: keyFile,
		Gateway: issue(3, gatewayDNS, x509.ExtKeyUsageClientAuth), WrongDNS: issue(4, "other.example.test", x509.ExtKeyUsageClientAuth)}
}

func (p consolePKI) Client(credential tls.Certificate) *http.Client {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.roots}
	if len(credential.Certificate) > 0 {
		tlsConfig.Certificates = []tls.Certificate{credential}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}}
}

func WriteJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}
