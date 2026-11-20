package websocket

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestHubStopBlocksNewJoins(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	hub.Stop()

	conn := newMockConn()
	defer conn.Close()

	err := hub.TryJoin("room", conn)
	if !errors.Is(err, ErrHubStopped) {
		t.Fatalf("expected ErrHubStopped, got %v", err)
	}

	if got := hub.Metrics().RejectedTotal; got != 1 {
		t.Fatalf("expected RejectedTotal=1, got %d", got)
	}
}

func TestHubCanJoinAfterStop(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	hub.Stop()

	err := hub.CanJoin("room")
	if !errors.Is(err, ErrHubStopped) {
		t.Fatalf("expected ErrHubStopped, got %v", err)
	}
}

func TestHubStopClearsRoomRegistrations(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	conn := newMockConn()
	defer conn.Close()

	mustTryJoin(t, hub, "room", conn)
	if got := hub.GetRoomRegistrationCount(); got != 1 {
		t.Fatalf("registration count before Stop = %d, want 1", got)
	}

	hub.Stop()

	metrics := hub.Metrics()
	if metrics.RoomRegistrations != 0 || metrics.ActiveConnections != 0 || metrics.Rooms != 0 {
		t.Fatalf("Stop must clear room metrics, got %+v", metrics)
	}
	if got := hub.GetRoomCount("room"); got != 0 {
		t.Fatalf("Stop must clear room members, got %d", got)
	}
}

func TestHubTryJoinAfterStopDoesNotRegister(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 4})
	hub.Stop()

	conn := newMockConn()
	defer conn.Close()

	if err := hub.TryJoin("room", conn); !errors.Is(err, ErrHubStopped) {
		t.Fatalf("expected ErrHubStopped, got %v", err)
	}

	if got := hub.GetRoomRegistrationCount(); got != 0 {
		t.Fatalf("expected no registrations after stop, got %d", got)
	}
}

func TestHubStopReturnsWhenBlockingSendQueueIsFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 1})
		conn := &Conn{
			sendQueue:    make(chan Outbound, 1),
			closeC:       make(chan struct{}),
			sendBehavior: SendBlock,
		}
		conn.sendQueue <- Outbound{Op: OpcodeText, Data: []byte("full")}
		mustTryJoin(t, hub, "room", conn)

		result := hub.TryBroadcastRoom("room", OpcodeText, []byte("blocked"))
		if result.Enqueued != 1 {
			t.Fatalf("broadcast result = %+v", result)
		}
		synctest.Wait()
		if len(hub.jobQueue) != 0 || len(conn.sendQueue) != cap(conn.sendQueue) {
			t.Fatal("worker did not take the job while the connection queue was full")
		}

		start := time.Now()
		hub.Stop()
		if elapsed := time.Since(start); elapsed > hubWorkerDefaultSendTimeout {
			t.Fatalf("hub stop took %v", elapsed)
		}
	})
}

func TestHubStopConcurrentBroadcast(t *testing.T) {
	hub := mustNewHubConfig(t, HubConfig{WorkerCount: 1, JobQueueSize: 16})
	conn := newMockConn()
	defer conn.Close()
	mustTryJoin(t, hub, "room", conn)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_ = hub.TryBroadcastRoom("room", OpcodeText, []byte("hello"))
		}
	}()

	hub.Stop()
	<-done

	if result := hub.TryBroadcastRoom("room", OpcodeText, []byte("after")); result.Enqueued != 0 {
		t.Fatalf("stopped hub reported enqueued jobs: %+v", result)
	}
}
