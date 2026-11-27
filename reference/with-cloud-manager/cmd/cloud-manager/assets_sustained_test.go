package main

import (
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

const assetCapacityWindow = 30 * time.Second

func TestCapacityProjectAssetsAtTargetRate(t *testing.T) {
	requireAssetCapacity(t)
	server, cases, _ := startAssetCapacity(t)
	runAssetCapacityRate(t, server, cases, time.Now())
}

func requireAssetCapacity(t *testing.T) {
	t.Helper()
	if os.Getenv("CLOUD_MANAGER_TEST_ASSET_TARGET_RATE") != "1" {
		t.Skip("CLOUD_MANAGER_TEST_ASSET_TARGET_RATE=1 enables the sustained HTTP capacity test")
	}
	if os.Getenv("CLOUD_MANAGER_TEST_DATABASE_URL") == "" {
		t.Fatal("CLOUD_MANAGER_TEST_DATABASE_URL is required for the sustained HTTP capacity test")
	}
}

func runAssetCapacityRate(t *testing.T, server *httptest.Server, cases []assetCapacityCase, start time.Time) {
	t.Helper()
	const rate = 200
	const interval = time.Second / rate
	const total = int(assetCapacityWindow / interval)
	const maxP95 = 250 * time.Millisecond
	const workers = 16
	const waiting = int(maxP95/interval) - workers
	type arrival struct {
		index int
		at    time.Time
	}
	latencies := make([]time.Duration, total)
	outcomes := make([]error, total)
	queue := make(chan arrival, waiting)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for request := range queue {
				outcomes[request.index] = readCapacityAssets(t.Context(), server.Client(), server.URL, cases[request.index%len(cases)])
				latencies[request.index] = time.Since(request.at)
			}
		})
	}
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	dropped := 0
	for i := range total {
		at := start.Add(time.Duration(i) * interval)
		if i != 0 {
			timer.Reset(time.Until(at))
		}
		<-timer.C
		select {
		case queue <- arrival{i, at}:
		default:
			dropped++
		}
	}
	close(queue)
	group.Wait()

	failures := 0
	for _, err := range outcomes {
		if err != nil {
			if failures == 0 {
				t.Logf("first HTTP failure: %v", err)
			}
			failures++
		}
	}
	t.Logf("target=%d requests/s duration=%s succeeded=%d dropped=%d failed=%d elapsed=%s",
		rate, assetCapacityWindow, total-dropped-failures, dropped, failures, time.Since(start).Round(time.Millisecond))
	if dropped != 0 || failures != 0 {
		t.Errorf("expected %d dispatches and successful replies", total)
	}
	for offset, sample := range cases {
		p95 := assetCapacityP95(latencies, offset, len(cases))
		t.Logf("%s p95=%s", sample.name, p95.Round(time.Millisecond))
		if p95 == 0 || p95 > maxP95 {
			t.Errorf("%s: expected a positive p95 no higher than %s", sample.name, maxP95)
		}
	}
}
