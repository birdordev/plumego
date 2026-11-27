package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type inventorySampleTransport func(*http.Request) (*http.Response, error)

func (f inventorySampleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInventorySampleCoverage(t *testing.T) {
	for _, test := range []struct {
		name         string
		total, fresh int
		pass         bool
	}{
		{"exact threshold", 20, 19, true},
		{"too few fresh", 20, 18, false},
		{"denominator drift", 21, 20, false},
		{"no eligible scopes", 0, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			args := []string{"inventory-sample", "lab", "compute", "--expected-scopes", "20", "--provider", "tencent-cvm",
				"--account", "tc", "--samples", "1"}
			err := runGovernanceClient(t.Context(), args, "https://fixture.example", "fixture-observer", nil, &output,
				inventorySampleTransport(func(request *http.Request) (*http.Response, error) {
					q := request.URL.Query()
					if request.Method != http.MethodGet || request.URL.Path != freshnessPath ||
						request.Header.Get("Authorization") != "Bearer fixture-observer" || q.Get("window_seconds") != "900" ||
						q.Get("limit") != "1" || q.Get("provider") != "tencent-cvm" || q.Get("account") != "tc" {
						t.Fatalf("unexpected sample request: %s %s", request.Method, request.URL)
					}
					return sampleFixtureResponse(test.total, test.fresh), nil
				}))
			if (err == nil) != test.pass {
				t.Fatalf("pass=%t err=%v", test.pass, err)
			}
			decoder := json.NewDecoder(&output)
			var sample inventorySample
			var summary inventorySampleResult
			if decoder.Decode(&sample) != nil || decoder.Decode(&summary) != nil || decoder.Decode(new(any)) != io.EOF {
				t.Fatal("expected exactly two NDJSON records")
			}
			if sample.Type != "sample" || sample.Number != 1 || sample.ExpectedScopes != 20 || sample.Passed != test.pass ||
				sample.Tenant != "lab" || sample.Project != "compute" || sample.Provider != "tencent-cvm" || sample.Account != "tc" ||
				sample.Summary.Total != int64(test.total) || sample.Dispatch.Scheduled != int64(test.total) ||
				summary.Type != "summary" || summary.Samples != 1 || summary.MinPercent != 95 ||
				(summary.Passed == 1) != test.pass || summary.ThresholdMet != test.pass {
				t.Fatalf("unexpected sample records: %+v %+v", sample, summary)
			}
		})
	}
}

func TestInventorySampleRejectsRepeatedSnapshot(t *testing.T) {
	var output bytes.Buffer
	requests := 0
	err := runGovernanceClient(t.Context(), []string{"inventory-sample", "lab", "compute", "--expected-scopes", "20",
		"--samples", "2", "--interval", "1s"}, "https://fixture.example", "fixture-observer", nil, &output,
		inventorySampleTransport(func(*http.Request) (*http.Response, error) {
			requests++
			return sampleFixtureResponse(20, 19), nil
		}))
	if err == nil || requests != 2 || bytes.Count(output.Bytes(), []byte("\n")) != 1 ||
		strings.Contains(output.String(), "\"type\":\"summary\"") {
		t.Fatalf("repeated snapshot must leave only the first sample: requests=%d error=%v output=%s", requests, err, &output)
	}
}

func TestInventorySampleOptionsBounded(t *testing.T) {
	for _, args := range [][]string{
		nil, {"--expected-scopes", "0"}, {"--expected-scopes", "1000000001"},
		{"--expected-scopes", "1", "--samples", "0"}, {"--expected-scopes", "1", "--interval", "999ms"},
		{"--expected-scopes", "1", "--samples", "26", "--interval", "1h"},
		{"--expected-scopes", "1", "--window-seconds", "1800"},
	} {
		if _, ok := inventorySampleOptions(args); ok {
			t.Fatalf("unexpectedly accepted %+v", args)
		}
	}
}

func TestInventorySampleCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var output bytes.Buffer
	requests := 0
	err := runGovernanceClient(ctx, []string{"inventory-sample", "lab", "compute", "--expected-scopes", "20",
		"--samples", "2", "--interval", "1h"}, "https://fixture.example", "fixture-observer", nil, &output,
		inventorySampleTransport(func(*http.Request) (*http.Response, error) {
			requests++
			cancel()
			return sampleFixtureResponse(20, 19), nil
		}))
	if err != context.Canceled || requests != 1 || strings.Contains(output.String(), "\"type\":\"summary\"") {
		t.Fatalf("cancellation did not stop the run: requests=%d error=%v output=%s", requests, err, &output)
	}
}

func TestInventorySampleRejectsInvalidManifestsBeforeRequest(t *testing.T) {
	for _, test := range []struct {
		name  string
		raw   string
		extra []string
	}{
		{name: "wrong project", raw: strings.Replace(fixtureScopeManifest, "\"compute\"", "\"other\"", 1)},
		{name: "wrong count", raw: fixtureScopeManifest, extra: []string{"--expected-scopes", "2"}},
		{name: "wrong provider", raw: fixtureScopeManifest, extra: []string{"--provider", "alibaba-ecs"}},
		{name: "duplicate", raw: `{"tenant":"lab","project":"compute","scopes":[
			{"provider":"tencent-cvm","account":"tc","region":"ap-test","kind":"instance"},
			{"provider":"tencent-cvm","account":"tc","region":"ap-test","kind":"instance"}
		]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"inventory-sample", "lab", "compute", "--scope-manifest", "--samples", "1"}, test.extra...)
			err := runGovernanceClient(t.Context(), args, "https://fixture.example", "fixture-observer", strings.NewReader(test.raw), io.Discard,
				inventorySampleTransport(func(*http.Request) (*http.Response, error) {
					t.Fatal("invalid manifest made an API request")
					return nil, nil
				}))
			if err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func sampleFixtureResponse(total, fresh int) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"data":{"as_of":"2026-11-27T01:00:00Z","window_seconds":900,
			"summary":{"total":%d,"fresh":%d,"stale":%d,"pending":0},
			"dispatch_summary":{"scheduled":%d,"ready":0,"account_busy":0,"cooldown":0,"leased":0,"longest_ready_overdue_seconds":0}}}`,
			total, fresh, total-fresh, total)))}
}
