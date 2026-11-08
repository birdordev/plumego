# with-websocket

`reference/with-websocket` shows how to add WebSocket support to a service that
follows the same bootstrap structure as `reference/standard-service`.

**This is a non-canonical scenario reference.** See `reference/standard-service`
for the canonical app layout.

## Purpose

Demonstrate WebSocket lifecycle management using `x/websocket`: HTTP upgrade,
hub registration, read loop, safe write path, JSON message encode/decode,
ping/pong heartbeat, and graceful shutdown. The endpoint echoes every message
back to the sender; JSON text frames with `{"type":"ping"}` receive a typed
`{"type":"pong"}` response.

## What it demonstrates

- HTTP route to WebSocket upgrade via `ws.RegisterRoutes`
- Explicit connection lifecycle: upgrade → hub registration → read loop → writer goroutine
- Read loop with frame validation (1 MB default message-size limit, UTF-8 enforcement,
  control-character rejection)
- Safe write path via `conn.WriteMessage` → bounded send queue → `writerPump` goroutine
- JSON message encode/decode: `message.Decode` / `conn.WriteJSON` in `echoHandler`
- Graceful close: `conn.WriteClose` signals the peer; `WS.Shutdown` drains all
  connections before the HTTP server stops
- Automatic ping/pong heartbeat with pong-wait timeout that closes stale connections
- Signal-driven shutdown (`SIGINT`/`SIGTERM`) via `signal.NotifyContext`
- Config-precedence chain: `Defaults < .env < process env < flags`

## What it intentionally excludes

- **Authentication**: `AllowUnauthenticated: true` is set for simplicity; JWT
  authentication requires one config change (see [Extending](#extending))
- **Room routing**: messages are echoed directly back to the sender; no broadcast
  or room fanout
- **Persistence, databases, Redis, or external state**
- **Multi-node or pub/sub coordination**
- **TLS**: configure `core.AppConfig.TLS` in production

## Layout

```
main.go
internal/
  config/   config.go config_test.go
  message/  message.go message_test.go   ← typed JSON envelope + pure tests
  app/      app.go routes.go app_test.go ws_echo_test.go
  handler/  health.go
```

## Configuration

| Variable     | Default  | Description                             |
|--------------|----------|-----------------------------------------|
| `APP_ADDR`   | `:8084`  | Listen address                          |
| `WS_SECRET`  | required | JWT signing secret (min 32 bytes)       |
| `APP_DEBUG`  | `false`  | Enable verbose request/response logging |

### Why `WS_SECRET` is required

`WS_SECRET` is mandatory even when `AllowUnauthenticated: true`. This ensures:

- The secret is already configured in your deployment pipeline so that enabling
  JWT authentication does not require a config redeployment.
- The application fails visibly at startup if the secret is missing or too weak,
  preventing silent downgrade to authentication-bypass mode.
- A production deployment can set `AllowUnauthenticated: false` immediately after
  migrating clients to send JWT tokens.

Generate a safe value:

```bash
openssl rand -base64 32
```

## Run

```bash
cd reference/with-websocket
WS_SECRET=$(openssl rand -base64 32) go run .
```

Or with a `.env` file:

```bash
cp env.example .env
# Edit .env: set WS_SECRET to a value of 32 bytes or more
go run .
```

## Tests

```bash
cd reference/with-websocket
go test -race -timeout 30s ./...
```

## Manual test

### Health check

```bash
curl http://localhost:8084/healthz
```

Expected:

```json
{"data":{"status":"ok","service":"with-websocket","timestamp":"..."}}
```

### WebSocket echo

Install [websocat](https://github.com/vi/websocat) or
[wscat](https://github.com/websockets/wscat) as optional CLI tools.

**websocat:**

```bash
websocat ws://localhost:8084/ws
```

**wscat:**

```bash
wscat -c ws://localhost:8084/ws
```

Type a message and press Enter. The server echoes it back.

**Example session (plain text echo):**

```
> hello
< hello
```

**Example session (typed JSON ping/pong):**

```
> {"type":"ping"}
< {"type":"pong"}
```

**Example session (unknown JSON type — echoed verbatim):**

```
> {"type":"custom","data":{"key":"value"}}
< {"type":"custom","data":{"key":"value"}}
```

Binary frames are also echoed verbatim.

**Verify upgrade rejection (non-WebSocket request):**

```bash
curl -i http://localhost:8084/ws
```

Expected: `400 Bad Request` with `{"error":{"code":"WEBSOCKET_BAD_UPGRADE",...}}`.

### Browser client (vanilla JavaScript)

Add to your HTML frontend:

```html
<script>
const ws = new WebSocket('ws://localhost:8084/ws');

ws.onopen = () => {
  console.log('Connected');
  ws.send(JSON.stringify({ type: 'ping' }));
};

ws.onmessage = (event) => {
  console.log('Received:', event.data);
};

ws.onerror = (event) => {
  console.error('WebSocket error:', event);
};

ws.onclose = () => {
  console.log('Disconnected');
};
</script>
```

Replace `localhost:8084` with your production domain when deploying. If you enable
JWT authentication, include the token in the URL or update the client to send
`Authorization: Bearer <jwt>` as an HTTP header during the WebSocket handshake
(modern browsers support custom headers via the `fetch` API during upgrade, though
standard WebSocket API limitations require using a helper library for header
control; alternatively, use the `?token=<jwt>` query parameter if you enable
`AllowQueryToken` in production config, though this is less secure).

## Connection lifecycle

```
Client                              Server
  │── GET /ws (Upgrade) ──────────>│  handshake: origin check, key validation
  │                                │  hub.tryJoin → registered in "default" room
  │<── 101 Switching Protocols ────│  writer goroutine + pong monitor started
  │── {"type":"ping"} ────────────>│  read loop → echoHandler → decode JSON
  │<── {"type":"pong"} ────────────│  WriteJSON → writerPump drains send queue
  │── text frame ─────────────────>│  read loop → echoHandler → raw echo
  │<── text frame (echo) ──────────│  writer goroutine drains send queue
  │     (idle)                     │
  │<── ping ───────────────────────│  writerPump sends periodic ping
  │── pong ────────────────────────│  pongMonitor resets timeout
  │── close frame ────────────────>│  read loop returns; cleanup goroutine
  │                                │  calls hub.RemoveConn
  │                                │
  │  (SIGTERM received)            │  hub.Shutdown closes all connections
  │<── TCP close ───────────────────│  HTTP server stops after WS drains
```

## Message format

The demo handler (`echoHandler` in `internal/app/app.go`) uses a simple
JSON envelope defined in `internal/message/message.go`:

```go
type Event struct {
    Type string          `json:"type"`
    Data json.RawMessage `json:"data,omitempty"`
}
```

Routing logic:

| Incoming frame    | Condition                  | Response                      |
|-------------------|----------------------------|-------------------------------|
| Text (`OpcodeText`) | Valid JSON, `type == "ping"` | `{"type":"pong"}`           |
| Text (`OpcodeText`) | Non-JSON or other type     | Verbatim echo                 |
| Binary (`OpcodeBinary`) | Any                    | Verbatim echo                 |

## Troubleshooting

### Handshake failures

WebSocket handshake errors return a structured JSON error response. Check the
HTTP status code and error code to diagnose:

| Status | Error Code | Cause | Fix |
|--------|-----------|-------|-----|
| 400 | `WEBSOCKET_BAD_UPGRADE` | Missing `Upgrade: websocket` or `Connection: upgrade` header | Ensure client is using WebSocket handshake; check for proxy stripping upgrade headers |
| 400 | `WEBSOCKET_KEY_MISSING` | Missing `Sec-WebSocket-Key` header | Standard WebSocket clients always send this; check network trace |
| 400 | `WEBSOCKET_KEY_INVALID` | `Sec-WebSocket-Key` is not base64 or wrong length | Client library issue; verify WebSocket library version |
| 400 | `WEBSOCKET_BAD_VERSION` | Missing or incorrect `Sec-WebSocket-Version: 13` header | Upgrade to a WebSocket library that supports RFC 6455 |
| 403 | `WEBSOCKET_FORBIDDEN_ORIGIN` | `Origin` header not in `AllowedOrigins` list | Add origin to `AllowedOrigins` in `internal/app/app.go`; use `AllowAllOrigins: true` only for development |
| 401 | `WEBSOCKET_TOKEN_REQUIRED` | No JWT token and `AllowUnauthenticated: false` | Send valid JWT in `Authorization: Bearer <token>` header or query string |
| 403 | `WEBSOCKET_INVALID_TOKEN` | JWT token provided but failed verification | Verify token is signed with the correct `WS_SECRET` and is not expired |
| 403 | `WEBSOCKET_ROOM_FORBIDDEN` | Room password incorrect or room access denied | Set correct `X-WebSocket-Room-Password` header; check room authorization policy |

Example error response:

```bash
curl -i http://localhost:8084/ws
```

```json
HTTP/1.1 400 Bad Request
Content-Type: application/json

{
  "error": {
    "code": "WEBSOCKET_BAD_UPGRADE",
    "message": "websocket upgrade required"
  }
}
```

### Connection closes immediately after 101

If the connection receives HTTP 101 Switching Protocols but closes immediately,
check these server-side conditions:

| Cause | Symptom | Fix |
|-------|---------|-----|
| Message validation (read limit, UTF-8, control chars) | Client receives close code 1009 (message too big), 1007 (invalid UTF-8), or 1008 (policy violation) | Increase `MessageValidation.MaxLength`; ensure client sends valid UTF-8; strip control characters from client messages |
| Read failure (malformed frames, unmasked client frames, invalid continuations) | Close code 1002 (protocol error) | Upgrade WebSocket client library; check for proxy that doesn't properly frame WebSocket |
| Message handler error | Close code 1011 (server error) | Check application logs for errors in `echoHandler` |
| Hub full or connection rate limit | Handshake fails before 101; see above table | Increase `MaxRoomRegistrations` or `MaxConnectionRate` in hub config |

Enable debug logging with `APP_DEBUG=true` and check server logs for `ReadMessageReader` or `OnMessage` errors.

### Slow reads or timeouts

| Symptom | Cause | Fix |
|---------|-------|-----|
| Client sends message but no echo received | Send queue full, connection timeout | Reduce `SendTimeout` in config; increase `SendQueueSize`; use `SendDrop` behavior for lossy fanout |
| Connection hangs or drops unexpectedly | Pong timeout (server ping not answered) | Check client handles incoming pings and sends pong frames; increase `PongWait` if network is slow |
| Reverse proxy closes connections | Proxy idle timeout or buffering limits | See reverse proxy config below |

## Production notes

- **Enable JWT auth**: set `AllowUnauthenticated: false` in `internal/app/app.go`
  and ensure `WS_SECRET` is a strong 32-byte minimum secret. Clients must send
  `Authorization: Bearer <jwt>` in the HTTP upgrade request. See [Extending](#extending).
- **Restrict origins**: replace `AllowAllOrigins: true` with
  `AllowedOrigins: []string{"https://your-domain.com"}` to prevent cross-origin
  WebSocket connections from browsers.
- **Message size**: `DefaultMessageValidationConfig` enforces a 1 MB limit and
  rejects control characters and invalid UTF-8. Adjust via `wsCfg.MessageValidation`
  in `internal/app/app.go`.
- **Backpressure**: a slow client with a full send queue triggers `SendBehavior`
  (default `SendBlock` with timeout). Use `SendDrop` or `SendClose` under load.
- **Connection limits**: set `MaxRoomRegistrations` and `MaxConnectionRate` on
  `HubConfig` to protect against connection floods.
- **Reverse proxy (nginx)**: add these headers to pass the upgrade:
  ```nginx
  proxy_http_version 1.1;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "upgrade";
  proxy_read_timeout 3600s;
  proxy_send_timeout 3600s;
  proxy_buffering off;
  ```
- **Reverse proxy (Caddy)**: use the `websocket` reverse proxy option:
  ```
  reverse_proxy localhost:8084 {
    header_up X-Forwarded-For {http.request.remote.host}
    header_up X-Forwarded-Proto {http.request.proto}
  }
  ```
  Caddy handles WebSocket upgrades automatically when the origin server sends
  the correct upgrade response.
- **TLS**: set `core.AppConfig.TLS.Enabled = true` with a certificate pair. Always
  use `wss://` (WebSocket Secure) in production; insecure `ws://` is unencrypted.

## Extending

**Enable JWT authentication:**

1. **Mint tokens**: Implement a `/login` endpoint that signs a JWT with `WS_SECRET`:
   ```go
   import "github.com/birdordev/plumego/x/websocket"
   
   secret := []byte(cfg.WSSecret)
   auth, err := websocket.NewSimpleHS256TokenAuth(secret)
   token, _ := auth.SignJWT(map[string]any{
     "sub": "user123",
     "aud": "websocket",
   })
   // Return token to client
   ```

2. **Require authentication**: Set `wsCfg.AllowUnauthenticated = false` in `internal/app/app.go`.

3. **Restrict origins**: Remove or replace `wsCfg.AllowAllOrigins = true` with
   `wsCfg.AllowedOrigins = []string{"https://your-domain.com"}`.

4. **Client sends token**: Include the JWT in the WebSocket upgrade request:
   ```javascript
   const ws = new WebSocket('ws://localhost:8084/ws', [], {
     headers: { Authorization: `Bearer ${token}` }
   });
   ```
   Note: Standard WebSocket API does not support custom headers; use a library like
   `ws` (Node.js) or implement a wrapper. Alternatively, send the token in the query
   string if you enable `AllowQueryToken: true` (less secure; only for trusted clients):
   ```javascript
   const ws = new WebSocket(`ws://localhost:8084/ws?token=${token}`);
   ```

5. **Reference**: See `x/websocket/auth.go` for `NewSimpleHS256TokenAuth`, `SignJWT`,
   and `TokenAuthenticator` interface.

**Add new message types:**

1. Define a new `type` string constant in `internal/message/message.go`.
2. Add a case in `echoHandler` in `internal/app/app.go`.
3. Add a focused integration test in `internal/app/ws_echo_test.go`.

**Broadcast from outside the connection handler:**

1. Store `a.WS.Hub()` in your service layer.
2. Call `hub.BroadcastAll(websocket.OpcodeText, payload)` or
   `hub.BroadcastRoom(room, ...)`.
3. Do not share the hub across process boundaries without an explicit message broker.

**Add room-based fanout:**

Remove the `OnMessage` handler from `wsCfg` in `app.go`. Without `OnMessage`,
`RegisterRoutes` automatically uses `ServeRoomFanoutWS`, which broadcasts each
client message to everyone in the same room (selected via `?room=` query parameter,
defaulting to `"default"`).

## Related references

- `reference/standard-service` — canonical layout this extends
- `x/websocket` — module docs and API reference (`docs/modules/x/websocket/README.md`)
- `reference/with-gateway` — reverse proxy and upstream routing
