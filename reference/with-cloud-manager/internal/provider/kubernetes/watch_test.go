package kubernetes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/internal/provider"
)

const absentWorkloadFixture = "partial-expired-event"

type subscriptionFixture struct {
	phase atomic.Int32
	lists atomic.Int32
}

func (f *subscriptionFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+testToken {
		t.Error("unexpected Kubernetes subscription request")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == "/api/v1/namespaces/anchor" {
		writeWatchFixture(t, w, map[string]any{"kind": "Namespace", "apiVersion": "v1",
			"metadata": map[string]string{"name": "anchor", "uid": "anchor-fixture-uid"}})
		return
	}
	for _, source := range workloadCollections {
		if r.URL.Path != (workloadSource{"work", source}).path() {
			continue
		}
		if r.URL.Query().Get("watch") == "true" {
			f.serveWatch(t, w, r, source)
			return
		}
		f.lists.Add(1)
		items := []any{}
		version := "1"
		if source.kind == "Deployment" {
			phase := f.phase.Load()
			version = strconv.Itoa(int(phase)*10 + 1)
			items = append(items, watchFixtureObject("fixture-"+version, "uid-"+version, version, "work"))
		}
		writeWatchFixture(t, w, map[string]any{"kind": source.kind + "List", "apiVersion": source.version,
			"metadata": map[string]string{"resourceVersion": version}, "items": items})
		return
	}
	t.Error("subscription escaped its namespace")
	w.WriteHeader(http.StatusForbidden)
}

func (f *subscriptionFixture) serveWatch(t *testing.T, w http.ResponseWriter, r *http.Request, source collection) {
	t.Helper()
	query := r.URL.Query()
	if query.Get("allowWatchBookmarks") != "true" || query.Get("timeoutSeconds") != "5" {
		t.Error("watch protocol options missing")
	}
	version := query.Get("resourceVersion")
	if source.kind != "Deployment" {
		if version != "1" {
			t.Errorf("other collection cursor changed: %q", version)
		}
		return
	}
	send := func(event string, body any) { writeWatchFixture(t, w, map[string]any{"type": event, "object": body}) }
	switch f.phase.Load() {
	case 0:
		if version != "1" {
			t.Errorf("initial watch version %q", version)
		}
		send("MODIFIED", watchFixtureObject("fixture-1", "uid-1", "2", "work"))
		send("ADDED", watchFixtureObject("ephemeral", "ephemeral-uid", "3", "work"))
		send("DELETED", watchFixtureObject("ephemeral", "ephemeral-uid", "4", "work"))
		send("BOOKMARK", map[string]any{"metadata": map[string]string{"resourceVersion": "opaque+next/5"}})
		// 模拟收到了完整事件后，连接在下一条消息中途断开。
		_, _ = fmt.Fprint(w, `{"type":"ADDED","object":`)
	case 1:
		switch version {
		case "opaque+next/5":
			w.WriteHeader(http.StatusGone)
		case "11":
			send("BOOKMARK", map[string]any{"metadata": map[string]string{"resourceVersion": "12"}})
		default:
			t.Errorf("HTTP 410 was not recovered from %q", version)
		}
	case 2:
		switch version {
		case "12":
			send("ADDED", watchFixtureObject(absentWorkloadFixture, "expired-uid", "13", "work"))
			send("ERROR", map[string]any{"kind": "Status", "code": 410, "reason": "Expired", "message": testToken})
		case "21":
			send("BOOKMARK", map[string]any{"metadata": map[string]string{"resourceVersion": "22"}})
		default:
			t.Errorf("in-stream 410 was not recovered from %q", version)
		}
	case 3:
		if version != "22" {
			t.Errorf("429 cursor = %q", version)
		}
		send("ERROR", map[string]any{"kind": "Status", "code": 429, "message": testToken,
			"details": map[string]int{"retryAfterSeconds": 27}})
	case 4:
		send("MODIFIED", watchFixtureObject("fixture-21", "uid-21", "23", "foreign"))
	}
}

func writeWatchFixture(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func watchFixtureObject(name, uid, version, ns string) map[string]any {
	return map[string]any{"kind": "Deployment", "apiVersion": "apps/v1",
		"metadata": map[string]any{"name": name, "uid": uid, "namespace": ns, "resourceVersion": version,
			"annotations": map[string]string{"secret": testToken}}, "status": map[string]int{"readyReplicas": 0}}
}

func TestWatchResumesAndRelistsExpiredCollections(t *testing.T) {
	f := &subscriptionFixture{}
	cfg := fixtureConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	base := provider.WorkloadObservation{}
	for phase, want := range []struct{ item, rv string }{{"fixture-1", "opaque+next/5"}, {"fixture-11", "12"}, {"fixture-21", "22"}} {
		f.phase.Store(int32(phase))
		got, err := client.ObserveWorkloads(t.Context(), base)
		if err != nil || len(got.Items) != 1 || len(got.Versions) != 5 ||
			got.Items[0].Name != want.item || got.Versions[0].Version != want.rv {
			t.Fatalf("phase %d got %+v error=%v", phase, got, err)
		}
		base = got
	}
	if f.lists.Load() != 7 {
		t.Fatalf("unaffected collections were re-listed: %d", f.lists.Load())
	}
	f.phase.Store(3)
	got, err := client.ObserveWorkloads(t.Context(), base)
	var failure *provider.Failure
	if got.Items != nil || !errors.As(err, &failure) || failure.Kind != provider.Throttled ||
		failure.RetryAfter != 27*time.Second || strings.Contains(err.Error(), testToken) {
		t.Fatalf("incorrect watch status mapping: %+v, error=%v", got, err)
	}
	f.phase.Store(4)
	if got, err := client.ObserveWorkloads(t.Context(), base); got.Items != nil || !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("cross-namespace watch event accepted: %+v, error=%v", got, err)
	}
}

func TestWatchBindingKeepsTokenRotationAndDetectsScopeChanges(t *testing.T) {
	cfg := fixtureConfig(t, http.NotFoundHandler())
	first, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Token = "rotated-fixture-token"
	second, err := NewClient(cfg)
	if err != nil || first.ObservationBinding() != second.ObservationBinding() {
		t.Fatal("credential refresh discarded a valid subscription")
	}
	cfg.Namespaces = []string{"different"}
	third, err := NewClient(cfg)
	if err != nil || first.ObservationBinding() == third.ObservationBinding() {
		t.Fatal("scope change reused a checkpoint")
	}
}
