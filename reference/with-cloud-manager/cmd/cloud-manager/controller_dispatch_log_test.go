package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
)

func TestInventoryDispatchLogger(t *testing.T) {
	var output bytes.Buffer
	due := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	claimed := due.Add(18250 * time.Millisecond)
	inventoryDispatchLogger(&output)(controller.DispatchObservation{
		Tenant: "tenant-a", Account: "shared", Provider: "tencent-cvm", Kind: "instance",
		ScheduleID: 42, Fence: 3, DueAt: due, ClaimedAt: claimed,
	})
	var record map[string]json.RawMessage
	if err := json.Unmarshal(output.Bytes(), &record); err != nil || bytes.Count(output.Bytes(), []byte{'\n'}) != 1 {
		t.Fatal("expected one complete NDJSON record")
	}
	expected := map[string]string{
		"level": `"INFO"`, "msg": `"inventory_dispatch_claimed"`, "schema_version": "1",
		"tenant": `"tenant-a"`, "account": `"shared"`, "provider": `"tencent-cvm"`, "kind": `"instance"`,
		"schedule_id": "42", "fence": "3", "due_at": `"2026-10-05T10:00:00Z"`,
		"claimed_at": `"2026-10-05T10:00:18.25Z"`, "due_wait_ms": "18250",
	}
	var loggedAt time.Time
	if len(record) != len(expected)+1 || json.Unmarshal(record["time"], &loggedAt) != nil || loggedAt.IsZero() {
		t.Fatal("expected only approved log fields and the logger timestamp")
	}
	for key, want := range expected {
		if got := string(record[key]); got != want {
			t.Errorf("field %s = %s, want %s", key, got, want)
		}
	}
}
