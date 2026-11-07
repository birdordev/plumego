package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// WriteMessage enqueues a message to the send queue.
// Behavior on a full queue depends on c.sendBehavior.
// It waits up to sendTimeout when SendBlock behavior is chosen.
//
// Use WriteMessageContext when you need explicit cancellation or a custom
// deadline beyond the configured sendTimeout.
func (c *Conn) WriteMessage(op byte, data []byte) error {
	ctx := context.Background()
	if c.sendTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.sendTimeout)
		defer cancel()
	}
	return c.WriteMessageContext(ctx, op, data)
}

// WriteMessageContext enqueues message to sendQueue with context support.
// Behavior on full queue depends on c.sendBehavior.
//
// The context can be used for cancellation and custom timeouts:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	err := conn.WriteMessageContext(ctx, websocket.OpcodeText, []byte("hello"))
func (c *Conn) WriteMessageContext(ctx context.Context, op byte, data []byte) error {
	if !isApplicationWriteOpcode(op) {
		return ErrInvalidOpcode
	}
	owned := make([]byte, len(data))
	copy(owned, data)
	out := Outbound{Op: op, Data: owned}

	if err := c.tryEnqueue(out); err == nil {
		return nil
	} else if !errors.Is(err, ErrQueueFull) {
		return err
	}

	// Queue is full, handle according to behavior.
	switch c.sendBehavior {
	case SendBlock:
		return c.enqueueBlocking(ctx, out)
	case SendDrop:
		return ErrQueueFull
	case SendClose:
		_ = c.Close()
		return ErrQueueFullClosed
	default:
		return errors.New("unknown send behavior")
	}
}

func isApplicationWriteOpcode(op byte) bool {
	return op == OpcodeText || op == OpcodeBinary
}

func (c *Conn) tryEnqueue(out Outbound) error {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	if c.IsClosed() {
		return ErrConnClosed
	}
	select {
	case c.sendQueue <- out:
		return nil
	default:
		return ErrQueueFull
	}
}

func (c *Conn) enqueueBlocking(ctx context.Context, out Outbound) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if c.IsClosed() {
			return ErrConnClosed
		}
		select {
		case c.sendQueue <- out:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closeC:
			return ErrConnClosed
		}
	}
}

func positiveDurationOrDefault(raw int64, fallback time.Duration) time.Duration {
	d := time.Duration(raw)
	if d <= 0 {
		return fallback
	}
	return d
}

func positiveDurationThirdOrDefault(raw int64, fallback time.Duration) time.Duration {
	d := positiveDurationOrDefault(raw, fallback)
	period := d / 3
	if period <= 0 {
		return fallback / 3
	}
	return period
}

// WriteText sends a text message
func (c *Conn) WriteText(data string) error {
	return c.WriteMessage(OpcodeText, []byte(data))
}

// WriteBinary sends a binary message
func (c *Conn) WriteBinary(data []byte) error {
	return c.WriteMessage(OpcodeBinary, data)
}

// WriteJSON serializes and sends a JSON message
func (c *Conn) WriteJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteMessage(OpcodeText, data)
}

// writerPump consumes sendQueue and writes frames to client. It fragments large messages.
func (c *Conn) writerPump() {
	period := positiveDurationOrDefault(c.pingPeriod.Load(), defaultPingPeriod)
	ticker := time.NewTicker(period)
	defer func() {
		ticker.Stop()
		_ = c.Close()
	}()
	for {
		select {
		case <-c.closeC:
			return
		case out, ok := <-c.sendQueue:
			if !ok {
				return
			}
			// fragment if needed
			data := out.Data
			if len(data) <= maxFragmentSize {
				if err := c.writeFrame(out.Op, true, data); err != nil {
					_ = c.Close()
					return
				}
				continue
			}
			total := len(data)
			offset := 0
			first := true
			for offset < total {
				end := offset + maxFragmentSize
				if end > total {
					end = total
				}
				chunk := data[offset:end]
				fin := end == total
				var op byte
				if first {
					op = out.Op
					first = false
				} else {
					op = opcodeContinuation
				}
				if err := c.writeFrame(op, fin, chunk); err != nil {
					_ = c.Close()
					return
				}
				offset = end
			}
		case <-ticker.C:
			// Pick up any runtime change to ping period made via SetPingPeriod.
			if newPeriod := positiveDurationOrDefault(c.pingPeriod.Load(), defaultPingPeriod); newPeriod != period {
				period = newPeriod
				ticker.Reset(period)
			}
			if err := c.writeFrame(opcodePing, true, []byte("ping")); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

// pongMonitor closes the connection if no pong is received within pongWait.
// The check interval is pongWait/3 so that a dead connection is detected
// within at most 4/3 * pongWait of the last failed pong, regardless of how
// pingPeriod is configured.
func (c *Conn) pongMonitor() {
	period := positiveDurationThirdOrDefault(c.pongWait.Load(), defaultPongWait)
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-c.closeC:
			return
		case <-ticker.C:
			// Pick up any runtime change to pongWait made via SetPongWait.
			if newPeriod := positiveDurationThirdOrDefault(c.pongWait.Load(), defaultPongWait); newPeriod != period {
				period = newPeriod
				ticker.Reset(period)
			}
			last := time.Unix(0, c.lastPong.Load())
			if time.Since(last) > positiveDurationOrDefault(c.pongWait.Load(), defaultPongWait) {
				_ = c.Close()
				return
			}
		}
	}
}
