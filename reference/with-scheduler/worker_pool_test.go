package main

import (
	"encoding/json"
	"testing"
)

func TestWorkerPoolsRouteTaskContractsAndValidateSettings(t *testing.T) {
	for _, env := range []string{"SCHEDULER_WORKER_CONCURRENCY", "SCHEDULER_WORKER_DB_MAX_OPEN", "SCHEDULER_WORKER_DB_MAX_IDLE"} {
		t.Setenv(env, "")
	}
	for _, tc := range []struct {
		pool, versions string
		topics         []string
	}{
		{"", "2", []string{"demo.record.v2", "demo.reserve", "demo.release"}},
		{"short", "", []string{"demo.record", "demo.record.v2", "demo.reserve", "demo.release"}},
		{"cpu", "", []string{"demo.compute", "demo.checkpoint"}},
		{"io", "1", []string{"demo.io"}},
	} {
		pool, err := loadWorkerPool(tc.pool, tc.versions)
		if err != nil || len(pool.contracts) != len(tc.topics) {
			t.Fatalf("pool %+v: %v", pool, err)
		}
		for index, topic := range tc.topics {
			if pool.contracts[index].topic != topic {
				t.Fatalf("pool %s routes %s, wanted %s", tc.pool, pool.contracts[index].topic, topic)
			}
		}
	}
	t.Setenv("SCHEDULER_WORKER_CONCURRENCY", "3")
	t.Setenv("SCHEDULER_WORKER_DB_MAX_OPEN", "2")
	pool, err := loadWorkerPool("io", "")
	if err != nil || pool.concurrency != 3 || pool.maxOpen != 2 || pool.maxIdle != 2 {
		t.Fatalf("overrides %+v: %v", pool, err)
	}
	t.Setenv("SCHEDULER_WORKER_DB_MAX_IDLE", "3")
	if _, err := loadWorkerPool("io", ""); err == nil {
		t.Fatal("idle count above open limit accepted")
	}
}

func TestLongIOContractBoundsTheDemonstrationPayload(t *testing.T) {
	for _, payload := range []string{`null`, `{"delay_ms":0}`, `{"delay_ms":30001}`, `{"unknown":1}`} {
		if _, _, err := validateTaskContract(ioTaskType, json.RawMessage(payload), taskOptions{}); err == nil {
			t.Errorf("accepted invalid I/O payload %q", payload)
		}
	}
	for _, payload := range []string{`{}`, `{"delay_ms":8000}`, `{"delay_ms":30000}`} {
		if _, _, err := validateTaskContract(ioTaskType, json.RawMessage(payload), taskOptions{}); err != nil {
			t.Errorf("rejected valid I/O payload %q: %v", payload, err)
		}
	}
}
