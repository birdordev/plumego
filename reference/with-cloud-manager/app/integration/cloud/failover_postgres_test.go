package cloudtest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"

	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/delivery"
)

func TestAcceptanceCloudManagerDatabaseFailover(t *testing.T) {
	if os.Getenv("CLOUD_MANAGER_TEST_FAILOVER") != "1" {
		t.Skip("set CLOUD_MANAGER_TEST_FAILOVER=1 to run the PostgreSQL promotion drill")
	}
	primary, standby, _ := newPITRClusters(t)
	source := primary.start(t)
	cloud := seedPITRPower(t, source)
	sink, err := delivery.NewDestination(delivery.Config{ID: "failover-fixture", Tenant: "tenant", Project: "project",
		Endpoint: "https://receiver.invalid/events", Secret: "fixture-failover-key-with-more-than-32-bytes"})
	if err != nil {
		t.Fatal(err)
	}
	if err := delivery.NewStore(source).Register(t.Context(), sink); err != nil {
		t.Fatal(err)
	}
	replica := startCloudFailoverStandby(t, primary, standby, source)
	standbyAPI, err := routers.NewAPI(replica, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	requireFailoverProbe(t, standbyAPI, "/healthz", http.StatusOK)
	requireFailoverProbe(t, standbyAPI, "/readyz", http.StatusServiceUnavailable)

	writer := cloudFailoverWriter(t, primary, standby)
	api := pitrAPI(t, writer)
	apiServer := httptest.NewServer(api)
	t.Cleanup(apiServer.Close)
	oldConnection, err := source.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := oldConnection.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Error(err)
		}
	})

	providerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloud.serve(t, w, r)
	}))
	t.Cleanup(providerServer.Close)
	providerURL, err := url.Parse(providerServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	providerTransport := cloudFixtureTransport{providerURL, providerServer.Client().Transport}
	effectApplied, releaseResponse := make(chan struct{}), make(chan struct{})
	oldTransport := pitrPowerTransport(func(req *http.Request) (*http.Response, error) {
		response, err := providerTransport.RoundTrip(req)
		if req.Header.Get("X-TC-Action") == "StopInstances" && cloud.writes.Load() == 2 {
			close(effectApplied)
			select {
			case <-releaseResponse:
			case <-req.Context().Done():
			}
		}
		return response, err
	})
	profile := pitrWorkerProfile(t, cloud)
	stopOldWorker := startPITRWorker(t, writer, profile, "dispatch", oldTransport)
	confirmed := submitPITRPower(t, api, cloud, "start", "before-failover")
	waitPITRDone(t, writer, confirmed.ID)
	confirmedDelivery := assertPITREvidence(t, writer, confirmed.ID, "succeeded", "done", true)
	inflight := submitPITRPower(t, api, cloud, "stop", "during-failover")
	select {
	case <-effectApplied:
	case <-time.After(8 * time.Second):
		t.Fatal("provider fixture did not apply the in-flight stop")
	}
	assertPITREvidence(t, replica, inflight.ID, "applying", "leased", false)
	oldOwner := failoverLeaseOwner(t, replica, inflight.ID)
	if got := assertPITREvidence(t, replica, confirmed.ID, "succeeded", "done", true); got != confirmedDelivery {
		t.Fatal("synchronous standby did not have the confirmed delivery")
	}

	disruptedAt := time.Now()
	pitrCommand(t, "pg_ctl", "-D", primary.data, "-m", "immediate", "-w", "stop")
	assertCloudPrimaryStopped(t, primary)
	probeContext, cancelProbe := context.WithTimeout(t.Context(), 2*time.Second)
	_, staleResult := oldConnection.ExecContext(probeContext, "SELECT 1")
	cancelProbe()
	if staleResult == nil {
		t.Fatal("an existing connection to the stopped primary still worked")
	}
	requireFailoverProbe(t, api, "/readyz", http.StatusServiceUnavailable)
	pitrCommand(t, "pg_ctl", "-D", standby.data, "-w", "-t", "20", "promote")
	pitrWait(t, "existing API server to reconnect to the promoted database", func(ctx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiServer.URL+"/readyz", nil)
		if err != nil {
			return false, err
		}
		resp, err := apiServer.Client().Do(req)
		if err != nil {
			return false, err
		}
		return resp.StatusCode == http.StatusOK, resp.Body.Close()
	})
	apiRecovery := time.Since(disruptedAt)
	requireFailoverProbe(t, standbyAPI, "/readyz", http.StatusOK)
	assertCloudPrimaryStopped(t, primary)
	if got := submitPITRPower(t, api, cloud, "start", "before-failover"); got.ID != confirmed.ID || got.Status != "succeeded" {
		t.Fatalf("confirmed operation identity changed: %+v", got)
	}
	assertPITREvidence(t, writer, inflight.ID, "applying", "leased", false)
	if got := failoverLeaseOwner(t, writer, inflight.ID); got != oldOwner {
		t.Fatal("in-flight lease identity changed before recovery")
	}

	pitrExec(t, writer, "UPDATE cmp_operation_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1 AND status='leased'", inflight.ID)
	recoveryStarted := time.Now()
	observed, releaseObservation := make(chan struct{}), make(chan struct{})
	signalObservation := sync.OnceFunc(func() { close(observed) })
	recoverTransport := pitrPowerTransport(func(req *http.Request) (*http.Response, error) {
		response, err := providerTransport.RoundTrip(req)
		if req.Header.Get("X-TC-Action") == "DescribeInstances" {
			signalObservation()
			select {
			case <-releaseObservation:
			case <-req.Context().Done():
			}
		}
		return response, err
	})
	recoverWorker := startPITRWorker(t, writer, profile, "observe-only", recoverTransport)
	select {
	case <-observed:
	case <-time.After(8 * time.Second):
		t.Fatal("promoted worker did not observe the cloud instance")
	}
	if got := failoverLeaseOwner(t, writer, inflight.ID); got == oldOwner {
		t.Fatal("recovery did not issue a new lease owner")
	}
	cfg := mqstore.DefaultSQLConfig()
	cfg.Table = "cmp_operation_tasks"
	if err := mqstore.NewSQL(writer, cfg).Ack(t.Context(), inflight.ID, oldOwner, time.Now()); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("superseded consumer could acknowledge a recovered task: %v", err)
	}
	close(releaseObservation)
	waitPITRDone(t, writer, inflight.ID)
	recoverWorker()
	outcomeRecovery := time.Since(recoveryStarted)
	close(releaseResponse)
	stopOldWorker()
	assertPITREvidence(t, writer, inflight.ID, "succeeded", "done", true)
	assertPITRProjection(t, writer, cloud.instance, "STOPPED")
	if cloud.writes.Load() != 2 || cloud.running.Load() {
		t.Fatal("database failover repeated or lost a provider effect")
	}
	t.Logf("confirmed operations lost=0, duplicate cloud effects=0, API recovery=%s, read-only settlement=%s",
		apiRecovery.Round(time.Millisecond), outcomeRecovery.Round(time.Millisecond))
}

func requireFailoverProbe(t *testing.T, handler http.Handler, path string, status int) {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	if w.Code != status {
		t.Fatalf("probe %s returned %d, want %d: %s", path, w.Code, status, w.Body)
	}
}

func failoverLeaseOwner(t *testing.T, db *sql.DB, operation string) string {
	t.Helper()
	var owner string
	if err := db.QueryRowContext(t.Context(), "SELECT lease_owner FROM cmp_operation_tasks WHERE id=$1 AND status='leased'",
		operation).Scan(&owner); err != nil || owner == "" {
		t.Fatalf("could not read the in-flight lease owner: %v", err)
	}
	return owner
}
