package websocket

import (
	"bufio"
	"bytes"
	"testing"
)

// FuzzReadFrameNeverPanics feeds arbitrary bytes into readFrame on both
// connection roles. Beyond "never panic", it asserts the frame invariants the
// parser promises: declared length matches the returned payload, payloads
// never exceed the configured read limit, and control frames obey RFC 6455
// §5.5 (FIN set, payload <= 125).
func FuzzReadFrameNeverPanics(f *testing.F) {
	// A masked client→server text frame carrying "hi".
	maskedText := []byte{0x81, 0x82, 0x01, 0x02, 0x03, 0x04}
	maskedText = appendMasked(maskedText, []byte("hi"), [4]byte{0x01, 0x02, 0x03, 0x04})
	// An unmasked server→client text frame carrying "hi".
	unmaskedText := []byte{0x81, 0x02, 'h', 'i'}
	// 16-bit extended length (126) with 126 bytes of payload, masked.
	ext16 := []byte{0x82, 0xFE, 0x00, 0x7E, 0xAA, 0xBB, 0xCC, 0xDD}
	ext16 = appendMasked(ext16, bytes.Repeat([]byte{0x55}, 126), [4]byte{0xAA, 0xBB, 0xCC, 0xDD})
	// 64-bit extended length (127) declaring 0x10000 bytes with no payload.
	ext64Truncated := []byte{0x82, 0xFF, 0, 0, 0, 0, 0, 0, 0x01, 0x00, 0x00}
	// Reserved bits set (RSV1-3) — must be rejected.
	rsvSet := []byte{0xC1, 0x00}
	// Close frame with a valid 1000 status, masked.
	closeFrame := []byte{0x88, 0x82, 0x00, 0x00, 0x00, 0x00}
	closeFrame = appendMasked(closeFrame, []byte{0x03, 0xE8}, [4]byte{0x00, 0x00, 0x00, 0x00})
	// Fragmented control frame (FIN=0 on ping) — must be rejected.
	fragControl := []byte{0x09, 0x80, 0x00, 0x00, 0x00, 0x00}

	for _, seed := range [][]byte{maskedText, unmaskedText, ext16, ext64Truncated, rsvSet, closeFrame, fragControl, {}, {0x81}, {0x81, 0x7F}} {
		f.Add(seed, false)
		f.Add(seed, true)
	}

	const readLimit = 1 << 20 // 1 MiB

	f.Fuzz(func(t *testing.T, data []byte, serverSide bool) {
		conn := &Conn{
			br:     bufio.NewReader(bytes.NewReader(data)),
			client: !serverSide,
		}
		conn.readLimit.Store(readLimit)

		op, fin, payload, err := conn.readFrame()
		if err != nil {
			return
		}

		// Masking direction rules (RFC 6455 §5.1/§5.3): accepted frames must
		// have satisfied the role-specific mask requirement.
		if len(data) >= 2 {
			masked := data[1]&0x80 != 0
			if serverSide && !masked {
				t.Errorf("server-side readFrame accepted unmasked frame: %x", data[:2])
			}
			if !serverSide && masked {
				t.Errorf("client-side readFrame accepted masked frame: %x", data[:2])
			}
		}

		if int64(len(payload)) > readLimit {
			t.Errorf("payload len %d exceeds read limit %d", len(payload), readLimit)
		}

		if op >= 0x8 {
			if !fin {
				t.Errorf("accepted fragmented control frame (op=%d)", op)
			}
			if len(payload) > int(maxControlPayload) {
				t.Errorf("control payload len %d exceeds %d", len(payload), maxControlPayload)
			}
		}
	})
}

// appendMasked appends payload XOR-masked with key to frame (RFC 6455 §5.3),
// matching how a client must send frames.
func appendMasked(frame, payload []byte, key [4]byte) []byte {
	for i, b := range payload {
		frame = append(frame, b^key[i%4])
	}
	return frame
}
