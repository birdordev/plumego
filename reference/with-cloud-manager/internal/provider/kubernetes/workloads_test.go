package kubernetes

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

type workloadFixture struct {
	mode         string
	listRequests atomic.Int32
	anchorReads  atomic.Int32
}

func (f *workloadFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+testToken {
		t.Error("unexpected Kubernetes method or authentication")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == "/api/v1/namespaces/anchor" {
		count := f.anchorReads.Add(1)
		uid := "anchor-fixture-uid"
		if f.mode == "wrong-anchor" || (f.mode == "changed-anchor" && count == 2) {
			uid = "wrong-cluster-uid"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Namespace", "apiVersion": "v1",
			"metadata": map[string]string{"name": "anchor", "uid": uid}})
		return
	}
	f.listRequests.Add(1)
	if r.URL.Query().Get("limit") != "100" {
		t.Error("unbounded Kubernetes list")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	for _, source := range workloadCollections {
		if r.URL.Path == "/apis/"+source.version+"/namespaces/work/"+source.resource {
			f.writePage(t, w, r, source)
			return
		}
	}
	t.Error("collection escaped the workload namespace")
	w.WriteHeader(http.StatusForbidden)
}

func (f *workloadFixture) writePage(t *testing.T, w http.ResponseWriter, r *http.Request, source collection) {
	t.Helper()
	cursor := r.URL.Query().Get("continue")
	meta := map[string]string{"resourceVersion": "7"}
	items := []any{}
	spec, status := map[string]any{}, map[string]any{}
	name := "fixture"
	ns := "work"
	switch source.kind {
	case "Deployment":
		if cursor == "" {
			meta["continue"] = "opaque+/= &"
		} else if cursor != "opaque+/= &" {
			t.Error("opaque continuation was modified")
		} else {
			switch f.mode {
			case "expired":
				w.WriteHeader(http.StatusGone)
				return
			case "throttled":
				w.Header().Set("Retry-After", "23")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			case "changed-version":
				meta["resourceVersion"] = "8"
			case "repeated-cursor":
				meta["continue"] = cursor
			case "wrong-namespace":
				ns = "other"
			}
			if f.mode != "duplicate" {
				name = "second"
			}
		}
		spec["replicas"], status["readyReplicas"] = 0, 0
	case "StatefulSet":
		spec["replicas"], status["readyReplicas"] = 2, 1
	case "DaemonSet":
		status["desiredNumberScheduled"], status["numberReady"] = 3, 0
	case "Job":
		spec["completions"], spec["suspend"] = 3, false
		status["active"], status["succeeded"], status["failed"] = 0, 1, 2
	case "CronJob":
		spec["suspend"] = true
		status["active"] = []map[string]string{{"name": "current-job", "uid": "never-published-reference"}}
	}
	items = append(items, map[string]any{"metadata": map[string]any{"uid": source.kind + "-" + name,
		"name": name, "namespace": ns, "annotations": map[string]string{"record": testToken}},
		"spec": spec, "status": status})
	err := json.NewEncoder(w).Encode(map[string]any{"kind": source.kind + "List", "apiVersion": source.version,
		"metadata": meta, "items": items})
	if err != nil {
		t.Error(err)
	}
}

func TestReadWorkloadsUsesConsistentPagesAndSafeProjection(t *testing.T) {
	f := &workloadFixture{}
	cfg := fixtureConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	items, err := client.ReadWorkloads(t.Context())
	if err != nil || len(items) != 6 || f.listRequests.Load() != 6 || f.anchorReads.Load() != 2 {
		t.Fatalf("workloads=%+v calls=%d anchors=%d error=%v", items, f.listRequests.Load(), f.anchorReads.Load(), err)
	}
	itemsByKind := map[string]provider.ClusterWorkload{}
	for _, item := range items {
		itemsByKind[item.Kind] = item
	}
	c := itemsByKind["CronJob"]
	j := itemsByKind["Job"]
	d := itemsByKind["DaemonSet"]
	if c.Active == nil || *c.Active != 1 || c.Suspended == nil || !*c.Suspended ||
		j.Active == nil || *j.Active != 0 || j.Suspended == nil || *j.Suspended ||
		d.Desired == nil || *d.Desired != 3 || d.Ready == nil || *d.Ready != 0 {
		t.Fatalf("incorrect workload counts: %+v", itemsByKind)
	}
}

func TestReadWorkloadsRejectsIncompleteSnapshots(t *testing.T) {
	for mode, want := range map[string]provider.FailureKind{
		"wrong-anchor": provider.Unauthorized, "changed-anchor": provider.Unauthorized,
		"changed-version": provider.InvalidResponse, "repeated-cursor": provider.InvalidResponse,
		"wrong-namespace": provider.InvalidResponse, "duplicate": provider.InvalidResponse,
		"expired": provider.ExpiredCursor, "throttled": provider.Throttled,
	} {
		t.Run(mode, func(t *testing.T) {
			f := &workloadFixture{mode: mode}
			cfg := fixtureConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
			client, err := NewClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			items, err := client.ReadWorkloads(t.Context())
			if items != nil || !provider.IsFailure(err, want) {
				t.Fatalf("incomplete workload list escaped: rows=%d error=%v", len(items), err)
			}
			if mode == "wrong-anchor" && f.listRequests.Load() != 0 {
				t.Fatal("client listed workloads before verifying cluster identity")
			}
			var failure *provider.Failure
			if mode == "throttled" && (!errors.As(err, &failure) || failure.RetryAfter != 23*time.Second) {
				t.Fatalf("Retry-After was lost: %v", err)
			}
		})
	}
}
