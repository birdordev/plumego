package websocket

import (
	"bytes"
	"context"
	"errors"
	"io"
	stdlog "log"
	"strings"
	"testing"
	"time"
)

// --- Stop idempotency ---

func TestHub_Stop_Idempotent(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	hub.Stop()
	hub.Stop() // must not panic
}

func TestHub_TryJoinRejectsInvalidRoomNames(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	conn := newMockConn()
	defer conn.Close()

	invalidRooms := []string{
		"",
		"bad/room",
		"bad room",
		"bad\nroom",
		strings.Repeat("a", defaultMaxRoomNameLength+1),
	}
	for _, room := range invalidRooms {
		if err := hub.TryJoin(room, conn); !errors.Is(err, ErrInvalidRoomName) {
			t.Fatalf("TryJoin(%q) error = %v, want ErrInvalidRoomName", room, err)
		}
		if err := hub.CanJoin(room); !errors.Is(err, ErrInvalidRoomName) {
			t.Fatalf("CanJoin(%q) error = %v, want ErrInvalidRoomName", room, err)
		}
		if result := hub.TryBroadcastRoom(room, OpcodeText, []byte("ignored")); !result.Invalid || !result.Rejected() {
			t.Fatalf("TryBroadcastRoom(%q) = %+v, want invalid rejected result", room, result)
		}
	}
}

func TestHubCustomRoomNameValidatorAppliesToPublicAPIs(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{
		WorkerCount:  1,
		JobQueueSize: 4,
		RoomNameValidator: func(room string) error {
			if room == "team/blue" {
				return nil
			}
			return ErrInvalidRoomName
		},
	})
	defer hub.Stop()

	conn := newMockConn()
	defer conn.Close()

	if err := hub.TryJoin("team/blue", conn); err != nil {
		t.Fatalf("TryJoin with custom room validator: %v", err)
	}
	if count := hub.GetRoomCount("team/blue"); count != 1 {
		t.Fatalf("GetRoomCount = %d, want 1", count)
	}
	visited := 0
	hub.RangeConns("team/blue", func(*Conn) bool {
		visited++
		return true
	})
	if visited != 1 {
		t.Fatalf("RangeConns visited %d, want 1", visited)
	}
	result := hub.TryBroadcastRoom("team/blue", OpcodeText, []byte("hello"))
	if result.Invalid || result.Stopped || result.Attempted != 1 || result.Enqueued != 1 {
		t.Fatalf("unexpected broadcast result: %+v", result)
	}
	hub.Leave("team/blue", conn)
	if count := hub.GetRoomCount("team/blue"); count != 0 {
		t.Fatalf("GetRoomCount after Leave = %d, want 0", count)
	}
}

// --- BroadcastRoom/BroadcastAll after Stop ---

func TestHub_BroadcastRoom_AfterStop_NoOp(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	conn := newMockConn()
	defer conn.Close()
	if err := hub.TryJoin("room", conn); err != nil {
		t.Fatalf("TryJoin before stop: %v", err)
	}

	hub.Stop()
	// Must not panic; dropped silently when hub is stopped.
	hub.BroadcastRoom("room", OpcodeText, []byte("hello"))
	if result := hub.TryBroadcastRoom("room", OpcodeText, []byte("hello")); !result.Stopped || !result.Rejected() {
		t.Fatalf("TryBroadcastRoom after Stop = %+v, want stopped rejected result", result)
	}
}

func TestHub_BroadcastAll_AfterStop_NoOp(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	hub.Stop()
	hub.BroadcastAll(OpcodeText, []byte("world")) // must not panic
	if result := hub.TryBroadcastAll(OpcodeText, []byte("world")); !result.Stopped || !result.Rejected() {
		t.Fatalf("TryBroadcastAll after Stop = %+v, want stopped rejected result", result)
	}
}

func TestHubTryBroadcastRoomReportsPartialDelivery(t *testing.T) {
	hub := newManualHub(1)
	c1 := &Conn{closeC: make(chan struct{})}
	c2 := &Conn{closeC: make(chan struct{})}
	hub.rooms["room"] = map[*Conn]struct{}{c1: {}, c2: {}}

	result := hub.TryBroadcastRoom("room", OpcodeText, []byte("hello"))

	if result.Attempted != 2 || result.Enqueued != 1 || result.Dropped != 1 || result.Rejected() {
		t.Fatalf("unexpected result: %+v", result)
	}
	metrics := hub.Metrics()
	if metrics.BroadcastAttempted != 2 || metrics.BroadcastEnqueued != 1 || metrics.BroadcastDropped != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

func TestHubTryBroadcastRoomCountsDrops(t *testing.T) {
	hub := newManualHub(1)
	conn := &Conn{closeC: make(chan struct{})}
	hub.rooms["room"] = map[*Conn]struct{}{conn: {}}
	hub.jobQueue <- hubJob{conn: conn, op: OpcodeText, data: []byte("full")}

	result := hub.TryBroadcastRoom("room", OpcodeText, []byte("hello"))

	if !result.Rejected() || result.Dropped != 1 {
		t.Fatalf("expected total rejection with one drop, got %+v", result)
	}
	if got := hub.Metrics().BroadcastDropped; got != 1 {
		t.Fatalf("BroadcastDropped = %d, want 1", got)
	}
}

// --- TryJoin capacity errors ---

func TestHub_TryJoin_HubFull(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{
		MaxRoomRegistrations: 1,
		WorkerCount:          1,
		JobQueueSize:         4,
	})
	defer hub.Stop()

	c1 := newMockConn()
	defer c1.Close()
	c2 := newMockConn()
	defer c2.Close()

	if err := hub.TryJoin("r", c1); err != nil {
		t.Fatalf("first join: %v", err)
	}
	err := hub.TryJoin("r", c2)
	if !errors.Is(err, ErrHubFull) {
		t.Errorf("expected ErrHubFull, got %v", err)
	}
}

func TestHub_TryJoin_RoomFull(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{
		MaxRoomConnections: 1,
		WorkerCount:        1,
		JobQueueSize:       4,
	})
	defer hub.Stop()

	c1 := newMockConn()
	defer c1.Close()
	c2 := newMockConn()
	defer c2.Close()

	if err := hub.TryJoin("r", c1); err != nil {
		t.Fatalf("first join: %v", err)
	}
	err := hub.TryJoin("r", c2)
	if !errors.Is(err, ErrRoomFull) {
		t.Errorf("expected ErrRoomFull, got %v", err)
	}
}

func TestHub_TryJoin_NilConn(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	err := hub.TryJoin("r", nil)
	if !errors.Is(err, ErrNilConn) {
		t.Fatalf("expected ErrNilConn, got %v", err)
	}
	if got := hub.GetRoomRegistrationCount(); got != 0 {
		t.Fatalf("nil join registered connection, got registrations=%d", got)
	}
}

func TestHub_TryJoin_DuplicateWinsOverCapacity(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{
		MaxRoomRegistrations: 1,
		WorkerCount:          1,
		JobQueueSize:         4,
	})
	defer hub.Stop()

	conn := newMockConn()
	defer conn.Close()
	if err := hub.TryJoin("r", conn); err != nil {
		t.Fatalf("first join: %v", err)
	}
	if err := hub.TryJoin("r", conn); err != nil {
		t.Fatalf("duplicate join should be idempotent at capacity: %v", err)
	}
	if got := hub.GetRoomRegistrationCount(); got != 1 {
		t.Fatalf("duplicate join changed registrations: got %d, want 1", got)
	}
}

func TestNewHubWithConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     HubConfig
		wantErr error
	}{
		{
			name:    "negative worker count",
			cfg:     HubConfig{WorkerCount: -1},
			wantErr: ErrNegativeWorkerCount,
		},
		{
			name:    "negative job queue",
			cfg:     HubConfig{JobQueueSize: -1},
			wantErr: ErrNegativeJobQueue,
		},
		{
			name:    "negative limit",
			cfg:     HubConfig{MaxRoomRegistrations: -1},
			wantErr: ErrNegativeLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub, err := NewHubWithConfig(tt.cfg)
			if hub != nil {
				hub.Stop()
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewHubWithConfigNormalizesZeroDefaults(t *testing.T) {
	hub, err := NewHubWithConfig(HubConfig{})
	if err != nil {
		t.Fatalf("NewHubWithConfig: %v", err)
	}
	defer hub.Stop()

	if hub.workers != 4 {
		t.Fatalf("workers = %d, want 4", hub.workers)
	}
	if cap(hub.jobQueue) != 1024 {
		t.Fatalf("jobQueue cap = %d, want 1024", cap(hub.jobQueue))
	}
}

func TestHubDefaultLoggerDiscardsOutput(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{})
	defer hub.Stop()

	if hub.logger.Writer() != io.Discard {
		t.Fatal("default hub logger must discard output")
	}
}

func TestHubUsesCallerProvidedLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := stdlog.New(&buf, "", 0)

	hub := mustNewHubConfig(t, HubConfig{
		MaxConnectionRate:  10,
		EnableDebugLogging: true,
		Logger:             logger,
	})
	defer hub.Stop()

	if !strings.Contains(buf.String(), "Rate limiter initialized") {
		t.Fatalf("expected caller logger output, got %q", buf.String())
	}
}

// --- CanJoin capacity errors ---

func TestHub_CanJoin_HubFull(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{
		MaxRoomRegistrations: 1,
		WorkerCount:          1,
		JobQueueSize:         4,
	})
	defer hub.Stop()

	c := newMockConn()
	defer c.Close()
	if err := hub.TryJoin("r", c); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := hub.CanJoin("r"); !errors.Is(err, ErrHubFull) {
		t.Errorf("expected ErrHubFull, got %v", err)
	}
}

// --- RangeConns ---

func TestHub_RangeConns_VisitsAll(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 8})
	defer hub.Stop()

	c1 := newMockConn()
	c2 := newMockConn()
	defer c1.Close()
	defer c2.Close()

	mustTryJoin(t, hub, "room", c1)
	mustTryJoin(t, hub, "room", c2)

	var count int
	hub.RangeConns("room", func(c *Conn) bool {
		count++
		return true
	})
	if count != 2 {
		t.Errorf("RangeConns visited %d conns, want 2", count)
	}
}

func TestHub_RangeConns_EarlyReturn(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 8})
	defer hub.Stop()

	for i := 0; i < 3; i++ {
		c := newMockConn()
		defer c.Close()
		mustTryJoin(t, hub, "room", c)
	}

	var count int
	hub.RangeConns("room", func(c *Conn) bool {
		count++
		return false // stop after first
	})
	if count != 1 {
		t.Errorf("RangeConns with early return visited %d, want 1", count)
	}
}

func TestHub_RangeConns_EmptyRoom_NoOp(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	var called bool
	hub.RangeConns("nonexistent", func(*Conn) bool {
		called = true
		return true
	})
	if called {
		t.Error("RangeConns on empty room should not invoke fn")
	}
}

// --- Shutdown ---

func TestHub_Shutdown_EmptyHub(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	if err := hub.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown empty hub: %v", err)
	}
}

func TestHub_Shutdown_NilContext(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	conn := newMockConn()
	mustTryJoin(t, hub, "r", conn)

	if err := hub.Shutdown(nil); err != nil {
		t.Fatalf("Shutdown(nil): %v", err)
	}
	if !conn.IsClosed() {
		t.Fatal("Shutdown(nil) must close registered connections")
	}
	if got := hub.GetRoomRegistrationCount(); got != 0 {
		t.Fatalf("Shutdown(nil) must clear room registrations, got %d", got)
	}
}

func TestHub_Shutdown_WithConnections(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 2, JobQueueSize: 8})

	c1 := newMockConn()
	c2 := newMockConn()
	mustTryJoin(t, hub, "r", c1)
	mustTryJoin(t, hub, "r", c2)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	if err := hub.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if !c1.IsClosed() || !c2.IsClosed() {
		t.Fatal("Shutdown must hard-close registered connections")
	}
	if got := hub.GetRoomRegistrationCount(); got != 0 {
		t.Fatalf("Shutdown must clear room registrations, got %d", got)
	}
	if got := hub.GetRoomCount("r"); got != 0 {
		t.Fatalf("Shutdown must clear room members, got %d", got)
	}
	if got := hub.Metrics(); got.ActiveConnections != 0 || got.RoomRegistrations != 0 || got.Rooms != 0 {
		t.Fatalf("Shutdown must clear metrics state, got %+v", got)
	}

	// After Shutdown the hub must be stopped: TryJoin must return ErrHubStopped.
	c3 := newMockConn()
	defer c3.Close()
	if err := hub.TryJoin("r", c3); !errors.Is(err, ErrHubStopped) {
		t.Errorf("expected ErrHubStopped after Shutdown, got %v", err)
	}
}

func TestHub_Shutdown_ContextCancel_ReturnsCtxErr(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})

	// Add many connections to slow down Shutdown iteration.
	for i := 0; i < 5; i++ {
		c := newMockConn()
		mustTryJoin(t, hub, "r", c)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already cancelled

	err := hub.Shutdown(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled or nil, got %v", err)
	}
	if err == nil {
		t.Fatal("expected context.Canceled")
	}
	if got := hub.Metrics(); got.ActiveConnections != 0 || got.RoomRegistrations != 0 || got.Rooms != 0 {
		t.Fatalf("cancelled Shutdown must leave stopped hub without registrations, got %+v", got)
	}
	c := newMockConn()
	defer c.Close()
	if err := hub.TryJoin("r", c); !errors.Is(err, ErrHubStopped) {
		t.Fatalf("TryJoin after cancelled Shutdown = %v, want ErrHubStopped", err)
	}
}

// --- Metrics ---

func TestHub_Metrics_InitialState(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	m := hub.Metrics()
	if m.ActiveConnections != 0 {
		t.Errorf("ActiveConnections = %d, want 0", m.ActiveConnections)
	}
	if m.RejectedTotal != 0 {
		t.Errorf("RejectedTotal = %d, want 0", m.RejectedTotal)
	}
}

func TestHub_Metrics_DistinguishesUniqueConnectionsAndRoomRegistrations(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()
	conn := newMockConn()
	defer conn.Close()

	if err := hub.TryJoin("room-a", conn); err != nil {
		t.Fatalf("join room-a: %v", err)
	}
	if err := hub.TryJoin("room-b", conn); err != nil {
		t.Fatalf("join room-b: %v", err)
	}

	m := hub.Metrics()
	if m.ActiveConnections != 1 {
		t.Fatalf("ActiveConnections = %d, want unique connection count 1", m.ActiveConnections)
	}
	if m.RoomRegistrations != 2 {
		t.Fatalf("RoomRegistrations = %d, want 2", m.RoomRegistrations)
	}
}

func TestHub_GetRooms_Empty(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	rooms := hub.GetRooms()
	if len(rooms) != 0 {
		t.Errorf("expected no rooms, got %v", rooms)
	}
}

func TestHub_Leave_NonMember_NoOp(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	conn := newMockConn()
	defer conn.Close()
	hub.Leave("room", conn) // conn was never joined — must not panic
}

func TestHub_RemoveConn_NotInAnyRoom_NoOp(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	defer hub.Stop()

	conn := newMockConn()
	defer conn.Close()
	hub.RemoveConn(conn) // must not panic
}

func TestHubRoomLogsEscapeControls(t *testing.T) {
	var buf bytes.Buffer
	hub := mustNewHubConfig(t, HubConfig{
		EnableDebugLogging: true,
		Logger:             stdlog.New(&buf, "", 0),
		RoomNameValidator:  func(string) error { return nil },
	})
	defer hub.Stop()
	c := newMockConn()
	defer c.Close()
	room := "room\r\nFORGED\x1b[2J"
	for i := 0; i < 2; i++ {
		if err := hub.TryJoin(room, c); err != nil {
			t.Fatal(err)
		}
	}
	if strings.ContainsAny(buf.String(), "\r\x1b") || strings.Contains(buf.String(), "\nFORGED") {
		t.Fatalf("日志注入：%q", buf.String())
	}
	if !strings.Contains(buf.String(), `room\r\nFORGED\x1b[2J`) {
		t.Fatal("缺少转义后的房间名")
	}
}
