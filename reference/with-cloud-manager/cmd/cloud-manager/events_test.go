package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/delivery"
)

func TestAcceptanceEventSinkCommands(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant'); INSERT INTO cmp_projects VALUES ('tenant','project')`); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "event-profiles.json")
	configure := func(includeAlerts bool) {
		t.Helper()
		data, err := json.Marshal([]delivery.Config{{ID: "audit", Tenant: "tenant", Project: "project", Endpoint: "https://receiver.invalid/events",
			Secret: strings.Repeat("x", 32), IncludeAlerts: includeAlerts}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(profile, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := runEventCommand(ctx, db, []string{"event-register", "audit"}, profile, nil, io.Discard); err != nil {
			t.Fatal(err)
		}
		var enabled bool
		if err := db.QueryRowContext(ctx, "SELECT include_alerts FROM cmp_operation_event_sinks WHERE sink_id='audit'").Scan(&enabled); err != nil || enabled != includeAlerts {
			t.Fatalf("unexpected alerts subscription state %t: %v", enabled, err)
		}
	}
	if !validCommand([]string{"event-publisher"}) || !validCommand([]string{"event-retry", "audit", "id"}) ||
		!validCommand([]string{"event-reconcile", "audit"}) || validCommand([]string{"event-disable"}) {
		t.Fatal("unexpected event command registration")
	}
	configure(false)
	configure(true)
	var output bytes.Buffer
	if err := runEventCommand(ctx, db, []string{"event-failures", "audit"}, profile, nil, &output); err != nil || strings.TrimSpace(output.String()) != "[]" {
		t.Fatalf("unexpected diagnostics output %q %v", output.String(), err)
	}
	if err := runEventCommand(ctx, db, []string{"event-disable", "audit"}, profile, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := db.QueryRowContext(ctx, "SELECT enabled FROM cmp_operation_event_sinks WHERE sink_id='audit'").Scan(&enabled); err != nil || enabled {
		t.Fatalf("sink was not disabled: %v", err)
	}
}
