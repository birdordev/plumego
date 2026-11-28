//go:build linux || darwin

package kvmtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
	"with-cloud-manager/internal/kvm"
)

func TestKVMImageMirror(t *testing.T) {
	raw := []byte("catalog raw boot image")
	spec := testKVMImage(raw)
	fileName := spec.SHA256 + ".raw"

	for _, name := range []string{"valid", "truncated", "extra bytes", "wrong digest", "redirect", "encoded", "catalog revoked", "wrong site", "canceled"} {
		t.Run(name, func(t *testing.T) {
			root := imageFixtureDir(t)
			var catalogReads, downloads atomic.Int32
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/tenants/tenant/projects/project/kvm/sites/site/images/" + spec.ID:
					count := catalogReads.Add(1)
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Error("catalog request did not use the expected method and credential")
					}
					if name == "catalog revoked" && count == 3 {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					image := kvm.BootImage{BootImageSpec: spec, Site: "site"}
					if name == "wrong site" {
						image.Site = "other"
					}
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(map[string]any{"data": image}); err != nil {
						t.Error(err)
					}
				case "/mirror/" + fileName:
					downloads.Add(1)
					if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Accept-Encoding") != "identity" {
						t.Error("mirror request had unexpected method, credentials or encoding")
					}
					switch name {
					case "truncated", "extra bytes":
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
						content := append(bytes.Clone(raw), '!')
						if name == "truncated" {
							content = raw[:len(raw)-1]
						}
						_, _ = w.Write(content)
					case "wrong digest":
						_, _ = w.Write(bytes.Repeat([]byte("x"), len(raw)))
					case "redirect":
						http.Redirect(w, r, "/other", http.StatusFound)
					case "encoded":
						w.Header().Set("Content-Encoding", "gzip")
						_, _ = w.Write(raw)
					case "canceled":
						cancel()
					default:
						_, _ = w.Write(raw)
					}
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			profile, err := json.Marshal(kvmapp.KVMImageMirrorProfile{MirrorURL: server.URL + "/mirror", ImageRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			run := func(output io.Writer) error {
				return kvmapp.RunKVMClient(ctx, []string{"mirror-image", "tenant", "project", "site", spec.ID}, server.URL, "fixture-token",
					bytes.NewReader(profile), output, server.Client().Transport)
			}
			target := filepath.Join(root, fileName)
			var out bytes.Buffer
			err = run(&out)
			if name != "valid" {
				if err == nil || out.Len() != 0 {
					t.Fatalf("unsuccessful mirror returned a receipt: err=%v output=%q", err, &out)
				}
				if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unsuccessful mirror published a target: %v", err)
				}
				if name == "wrong site" && downloads.Load() != 0 {
					t.Fatal("mismatched catalog identity accessed the mirror")
				}
				return
			}
			if err != nil || !strings.Contains(out.String(), spec.SHA256) {
				t.Fatalf("mirror failed: %v %q", err, &out)
			}
			before, err := os.Stat(target)
			if err != nil || kvmapp.KVMImageLinkCount(before) != 1 {
				t.Fatalf("invalid published image: %v", err)
			}
			if err := run(io.Discard); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(target)
			got, readErr := os.ReadFile(target)
			if err != nil || readErr != nil || !os.SameFile(before, after) || !bytes.Equal(raw, got) || downloads.Load() != 1 {
				t.Fatalf("mirror retry did not reuse the original file: %v %v", err, readErr)
			}
			if err := os.WriteFile(target, bytes.Repeat([]byte("x"), len(raw)), 0600); err != nil {
				t.Fatal(err)
			}
			if err := run(io.Discard); !errors.Is(err, kvmapp.ErrKVMImageMismatch) || downloads.Load() != 1 {
				t.Fatalf("mirror replaced or accepted a damaged existing file: %v", err)
			}
		})
	}
}
