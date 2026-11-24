package main

import (
	"testing"
	"time"
)

func TestScaleTargetAccountsForDemandAndDownstreamLimits(t *testing.T) {
	cfg := autoscaleConfig{
		pool: workerPoolConfig{concurrency: 2}, minWorkers: 1, maxWorkers: 5,
		age: time.Second, settle: 2 * time.Second,
	}
	for _, tc := range []struct {
		name    string
		signals scaleSnapshot
		current int
		idle    time.Duration
		want    int
	}{
		{"warm minimum", scaleSnapshot{}, 0, 0, 1},
		{"burst at saturation", scaleSnapshot{ready: 30, active: 2, arrivals: 20, processed: 4, downstreamSlots: -1}, 1, 0, 5},
		{"aged backlog despite quiet arrivals", scaleSnapshot{ready: 4, active: 2, oldest: 3, processed: 20, downstreamSlots: -1}, 1, 0, 2},
		{"spare worker capacity", scaleSnapshot{ready: 1, oldest: 3, downstreamSlots: -1}, 2, 0, 2},
		{"downstream slots limit additions", scaleSnapshot{ready: 30, active: 2, arrivals: 20, processed: 4, downstreamSlots: 2}, 1, 0, 2},
		{"downstream rate exhausted", scaleSnapshot{ready: 30, active: 2, arrivals: 20, downstreamSlots: -1, downstreamStarts: 4, downstreamMinStartMS: 250}, 1, 0, 1},
		{"settling", scaleSnapshot{}, 3, time.Second, 3},
		{"drain one worker", scaleSnapshot{active: 1}, 3, 3 * time.Second, 2},
		{"idle floor", scaleSnapshot{}, 1, 3 * time.Second, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scaleTarget(cfg, tc.signals, tc.current, tc.idle); got != tc.want {
				t.Fatalf("got %d workers, want %d", got, tc.want)
			}
		})
	}
}
