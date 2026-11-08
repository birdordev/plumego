// Package websocket provides an experimental WebSocket server with room-based broadcasting.
//
// This package implements a high-performance WebSocket hub featuring:
//   - Room-based message broadcasting
//   - JWT authentication for secure connections when required by configuration
//   - Connection lifecycle management
//   - Worker pool for concurrent message delivery
//   - Connection limits (total and per-room)
//   - Metrics collection and monitoring
//   - Security event tracking
//
// The hub manages WebSocket connections organized into rooms, allowing efficient
// message broadcasting to specific groups of clients. By default, ServeWSWithConfig
// requires a JWT token; callers must explicitly set AllowUnauthenticated when
// using room-password-only development flows. Query-string JWT transport is
// disabled unless callers explicitly set AllowQueryToken.
//
// Example usage:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	// Create a WebSocket hub
//	hub, err := websocket.NewHubWithConfig(websocket.HubConfig{
//		WorkerCount:        4,
//		JobQueueSize:       1024,
//		MaxRoomRegistrations:     10000,
//		MaxRoomConnections: 1000,
//	})
//	if err != nil {
//		return err
//	}
//	defer hub.Stop()
//
//	tokenAuth, err := websocket.NewSimpleHS256TokenAuth([]byte("this-is-a-secret-key-that-is-at-least-32-bytes"))
//	if err != nil {
//		return err
//	}
//	roomAuth := websocket.NewSimpleRoomAuth()
//
//	// Serve a WebSocket endpoint with auth and origin checks.
//	// Inside the package handler, successful connections are registered to rooms.
//	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
//		websocket.ServeRoomFanoutWS(w, r, websocket.ServerConfig{
//			Hub:            hub,
//			TokenAuth:      tokenAuth,
//			RoomAuth:       roomAuth,
//			QueueSize:      256,
//			SendTimeout:    200 * time.Millisecond,
//			SendBehavior:   websocket.SendBlock,
//			AllowedOrigins: []string{"https://app.example.com"},
//		})
//	})
//
//	// Broadcast to a room
//	hub.BroadcastRoom("chat:general", websocket.OpcodeText, []byte("Hello"))
package websocket
