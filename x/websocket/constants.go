package websocket

import "time"

// Opcode constants for WebSocket frame types.
//
// Example:
//
//	import "github.com/birdordev/plumego/x/websocket"
//
//	// Send a text message
//	err := conn.WriteMessage(websocket.OpcodeText, []byte("Hello"))
//
//	// Send a binary message
//	err := conn.WriteMessage(websocket.OpcodeBinary, []byte{0x01, 0x02, 0x03})
const (
	// OpcodeText is used for UTF-8 encoded text messages
	OpcodeText byte = 0x1

	// OpcodeBinary is used for binary data messages
	OpcodeBinary byte = 0x2
)

// WebSocket protocol constants
const (
	guid               = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	opcodeContinuation = byte(0x0)
	opcodeClose        = byte(0x8)
	opcodePing         = byte(0x9)
	opcodePong         = byte(0xA)
	finBit             = byte(0x80)
	defaultBufSize     = 4096
	defaultPingPeriod  = 20 * time.Second
	defaultPongWait    = 30 * time.Second
	defaultWriteWait   = 10 * time.Second
	maxControlPayload  = int64(125)
	maxFragmentSize    = 64 * 1024 // 64KB
)

// WebSocket close status codes as defined in RFC 6455, Section 7.4.1.
// Pass these to WriteClose to signal why a connection is being terminated.
//
// Example:
//
//	conn.WriteClose(websocket.CloseNormalClosure, "goodbye")
//	conn.WriteClose(websocket.CloseGoingAway, "server shutting down")
const (
	// CloseNormalClosure indicates a normal, clean shutdown.
	CloseNormalClosure uint16 = 1000

	// CloseGoingAway indicates the server is going down or the browser navigated away.
	CloseGoingAway uint16 = 1001

	// CloseProtocolError indicates a protocol error was detected.
	CloseProtocolError uint16 = 1002

	// CloseUnsupportedData indicates the endpoint cannot accept the data type.
	CloseUnsupportedData uint16 = 1003

	// CloseInvalidPayload indicates the message payload is not consistent with its type.
	CloseInvalidPayload uint16 = 1007

	// ClosePolicyViolation indicates a policy was violated (generic catch-all).
	ClosePolicyViolation uint16 = 1008

	// CloseMessageTooBig indicates the message is too large to process.
	CloseMessageTooBig uint16 = 1009

	// CloseServerError indicates an unexpected server-side error.
	CloseServerError uint16 = 1011
)
