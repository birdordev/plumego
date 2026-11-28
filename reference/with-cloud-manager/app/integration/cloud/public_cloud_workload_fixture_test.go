package cloudtest

import (
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/kubernetes"
	"with-cloud-manager/internal/provider/publiccloud"
)

const (
	workloadBearer = "fixture-sensitive-cluster-bearer"
	workloadValue  = "private-container-env-fixture"
	workloadRef    = "fixture/snapshot-delete"
)

type workloadFixture struct {
	mode        atomic.Int32
	calls       atomic.Int32
	lists       atomic.Int32
	watch       func(http.ResponseWriter, *http.Request, string)
	listVersion func(string) string
	mutation    func(http.ResponseWriter, *http.Request) bool
}

const (
	workloadHealthy int32 = iota
	workloadExpired
	workloadChanged
	workloadThrottled
)

func (f *workloadFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if f.mutation != nil && f.mutation(w, r) {
		return
	}
	f.calls.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer "+workloadBearer {
		t.Error("unexpected Kubernetes request method or credential")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.URL.Path == "/api/v1/namespaces/anchor" {
		writeWorkloadFixture(t, w, map[string]any{"kind": "Namespace", "apiVersion": "v1",
			"metadata": map[string]string{"name": "anchor", "uid": "anchor-uid"}})
		return
	}
	version, kind, found := workloadListType(r.URL.Path)
	if found && f.watch != nil && r.URL.Query().Get("watch") == "true" {
		f.watch(w, r, kind)
		return
	}
	f.lists.Add(1)
	if !found || r.URL.Query().Get("limit") != "100" {
		t.Error("unexpected Kubernetes workload collection")
		w.WriteHeader(http.StatusForbidden)
		return
	}
	mode := f.mode.Load()
	versionID := "3"
	if f.listVersion != nil {
		versionID = f.listVersion(kind)
	}
	meta := map[string]string{"resourceVersion": versionID}
	items := []any{}
	if kind == "Deployment" {
		cursor := r.URL.Query().Get("continue")
		name := "billing"
		switch cursor {
		case "":
			meta["continue"] = "page/2+"
		case "page/2+":
			if mode == workloadExpired {
				w.WriteHeader(http.StatusGone)
				return
			}
			if mode == workloadThrottled {
				w.Header().Set("Retry-After", "45")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			name = "payments"
		default:
			t.Error("bad Kubernetes continuation")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if mode == workloadChanged {
			name += "-updated"
		}
		items = append(items, map[string]any{
			"metadata": map[string]any{"uid": "uid-" + name, "namespace": "allowed", "name": name,
				"annotations": map[string]string{"internal": workloadBearer}},
			"spec":   map[string]any{"replicas": 0, "template": map[string]string{"secret": workloadValue}},
			"status": map[string]int{"readyReplicas": 0},
		})
	}
	writeWorkloadFixture(t, w, map[string]any{"kind": kind + "List", "apiVersion": version, "metadata": meta, "items": items})
}

func workloadListType(path string) (string, string, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/apis/"), "/")
	if len(parts) != 5 || parts[1] != "v1" || parts[2] != "namespaces" || parts[3] != "allowed" {
		return "", "", false
	}
	switch parts[0] + "/" + parts[4] {
	case "apps/deployments":
		return "apps/v1", "Deployment", true
	case "apps/statefulsets":
		return "apps/v1", "StatefulSet", true
	case "apps/daemonsets":
		return "apps/v1", "DaemonSet", true
	case "batch/jobs":
		return "batch/v1", "Job", true
	case "batch/cronjobs":
		return "batch/v1", "CronJob", true
	}
	return "", "", false
}

func writeWorkloadFixture(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Error(err)
	}
}

func saveWorkloadProfile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func prepareWorkloadFixture(t *testing.T, db *sql.DB, cluster *clusterFixture, f *workloadFixture) (func() error, func() error, string, string, cloud.ClusterWorkloadProfile) {
	t.Helper()
	kube := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(kube.Close)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { cluster.serve(t, w, r) }))
	t.Cleanup(remote.Close)
	remoteURL, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: kube.Certificate().Raw})
	dir := t.TempDir()
	path, kubePath := filepath.Join(dir, "cloud.json"), filepath.Join(dir, "cluster.json")
	profile := cloud.PublicCloudProfile{Ref: workloadRef, Config: publiccloud.Config{AccountID: cluster.account, Regions: []string{cluster.region}},
		KubernetesWorkloads: map[string]string{cluster.region + "/" + cluster.id: kubePath}}
	if cluster.vendor == provider.HuaweiECS {
		profile.ProjectID, profile.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		profile.AccessKeyID, profile.AccessKeySecret, profile.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	credentials := cloud.ClusterWorkloadProfile{CloudIdentity: cluster.remoteIdentity(), CredentialRevision: 1,
		Config: kubernetes.Config{Endpoint: kube.URL, CA: string(cert), Token: workloadBearer,
			IdentityNamespace: "anchor", IdentityUID: "anchor-uid", Namespaces: []string{"allowed"}}}
	saveWorkloadProfile(t, path, profile)
	saveWorkloadProfile(t, kubePath, credentials)
	scan := func() error {
		return cloud.RunPublicCloud(t.Context(), db, []string{"scan-" + cluster.command, "tenant", cluster.vendor, cluster.region, string(provider.KubernetesCluster)},
			cluster.vendor, path, cloudFixtureTransport{remoteURL, remote.Client().Transport})
	}
	refresh := func() error {
		args := []string{"refresh-" + cluster.command + "-cluster-workloads", "tenant", cluster.vendor, cluster.region, cluster.id}
		vendor, _ := cloud.PublicCloudCommand(args[0])
		if vendor != cluster.vendor || !cloudmanager.ValidCommand(args) {
			t.Fatal("CLI workload command not registered for cloud")
		}
		return cloud.RunPublicCloud(t.Context(), db, args, vendor, path, nil)
	}
	return scan, refresh, path, kubePath, credentials
}
