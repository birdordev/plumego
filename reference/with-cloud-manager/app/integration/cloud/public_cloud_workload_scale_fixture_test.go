package cloudtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

const scaleFixtureBearer = "fixture-limited-workload-writer"

const (
	scaleFixtureReady int32 = iota
	scaleFixtureWrongUID
	scaleFixtureConflict
	scaleFixtureLoseReceipt
	scaleFixtureDrift
)

type scaleFixture struct {
	mode, writes, replicas atomic.Int32
}

func (f *scaleFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.Header.Get("Authorization") != "Bearer "+scaleFixtureBearer {
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/anchor" {
		writeWorkloadFixture(t, w, map[string]any{"kind": "Namespace", "apiVersion": "v1",
			"metadata": map[string]string{"name": "anchor", "uid": "anchor-uid"}})
		return true
	}
	if r.URL.Path != "/apis/apps/v1/namespaces/allowed/deployments/billing/scale" {
		t.Error("write credential reached an unexpected route")
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	mode := f.mode.Load()
	uid := "uid-billing"
	if mode == scaleFixtureWrongUID {
		uid = "uid-replaced"
	}
	replicas := f.replicas.Load()
	if mode == scaleFixtureDrift {
		replicas = 9
	}
	if r.Method == http.MethodPut {
		f.writes.Add(1)
		var body struct {
			Kind     string            `json:"kind"`
			Version  string            `json:"apiVersion"`
			Metadata map[string]string `json:"metadata"`
			Spec     map[string]int32  `json:"spec"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		if err != nil || body.Kind != "Scale" || body.Version != "autoscaling/v1" ||
			body.Metadata["uid"] != uid || body.Metadata["resourceVersion"] != "100" ||
			body.Metadata["name"] != "billing" || body.Metadata["namespace"] != "allowed" ||
			body.Spec["replicas"] != 4 || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("incorrect scale PUT precondition or spec: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		if mode == scaleFixtureConflict {
			w.WriteHeader(http.StatusConflict)
			return true
		}
		f.replicas.Store(body.Spec["replicas"])
		replicas = body.Spec["replicas"]
		if mode == scaleFixtureLoseReceipt {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
			} else if err := conn.Close(); err != nil {
				t.Error(err)
			}
			return true
		}
	} else if r.Method != http.MethodGet {
		t.Error("unsupported scale method")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	writeWorkloadFixture(t, w, map[string]any{"kind": "Scale", "apiVersion": "autoscaling/v1",
		"metadata": map[string]string{"uid": uid, "name": "billing", "namespace": "allowed", "resourceVersion": "100"},
		"spec":     map[string]int32{"replicas": replicas}, "status": map[string]int{"replicas": 0}})
	return true
}

func workloadScaleHTTP(t *testing.T, handler http.Handler, method, path, token, key, revision string, body any, want int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, workloadScaleBody(t, body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	if revision != "" {
		request.Header.Set("If-Match", revision)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != want {
		t.Fatalf("%s %s got %d want %d: %s", method, path, recorder.Code, want, recorder.Body)
	}
	return recorder
}

func workloadScaleBody(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	if body == nil {
		return bytes.NewReader(nil)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(raw)
}
