package kubernetes

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

func TestScaleStatefulSetChecksNamespaceAndLimits(t *testing.T) {
	var calls, replicas atomic.Int32
	replicas.Store(2)
	cfg := fixtureConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/namespaces/anchor" {
			writeWatchFixture(t, w, map[string]any{"kind": "Namespace", "apiVersion": "v1",
				"metadata": map[string]string{"name": "anchor", "uid": "anchor-fixture-uid"}})
			return
		}
		if r.URL.Path != "/apis/apps/v1/namespaces/work/statefulsets/queue/scale" {
			t.Error("unexpected StatefulSet scale request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPut {
			var body scaleObject
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Metadata.ResourceVersion != "52" ||
				body.Metadata.UID != "uid-fixture" || body.Spec.Replicas == nil || *body.Spec.Replicas != 3 {
				t.Error("StatefulSet scale preconditions were lost")
				w.WriteHeader(http.StatusConflict)
				return
			}
			replicas.Store(3)
		} else if r.Method != http.MethodGet {
			t.Error("unexpected StatefulSet scale method")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeWatchFixture(t, w, map[string]any{"kind": "Scale", "apiVersion": "autoscaling/v1",
			"metadata": map[string]string{"name": "queue", "namespace": "work", "uid": "uid-fixture", "resourceVersion": "52"},
			"spec":     map[string]int32{"replicas": replicas.Load()}})
	}))
	client, err := NewScaleClient(cfg, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	change := provider.WorkloadScaleChange{Current: 2, Desired: 3, WorkloadScaleTarget: provider.WorkloadScaleTarget{
		Namespace: "foreign", Kind: "StatefulSet", Name: "queue", UID: "uid-fixture",
	}}
	if _, err := client.PrepareWorkloadScale(t.Context(), change); !provider.IsFailure(err, provider.Unauthorized) || calls.Load() != 0 {
		t.Fatalf("cross-namespace scale preflight made a request: %v", err)
	}
	change.Namespace, change.Desired = "work", 5
	if _, err := client.PrepareWorkloadScale(t.Context(), change); !provider.IsFailure(err, provider.PreconditionFailed) || calls.Load() != 0 {
		t.Fatalf("scale limit failed: %v", err)
	}
	change.Desired = 3
	send, err := client.PrepareWorkloadScale(t.Context(), change)
	if err != nil || send == nil || calls.Load() != 2 {
		t.Fatalf("valid StatefulSet scale preflight failed: %v", err)
	}
	if _, err := send(t.Context()); err != nil || replicas.Load() != 3 {
		t.Fatalf("StatefulSet scale was not applied: %v", err)
	}
	if observed, err := client.ObserveWorkloadScale(t.Context(), change); err != nil || observed != provider.ActionSucceeded {
		t.Fatalf("StatefulSet scale was not observed: %s %v", observed, err)
	}
}
