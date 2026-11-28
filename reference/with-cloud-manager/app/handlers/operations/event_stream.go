package operationhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/app/handlers/transport"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

const OperationEventStreamRoute = ProjectOperationsRoute + "/:operation/events/stream"

const (
	eventStreamLifetime = 8 * time.Second
	eventStreamPoll     = 2 * time.Second
	eventStreamPageSize = 100
)

func OperationEventStreamHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, ok := transport.RequestCredential(r)
		if !ok {
			writeOperationEventError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		cursor, err := operationStreamCursor(r)
		if err != nil {
			writeOperationEventError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), eventStreamLifetime)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		id := router.Param(r, "operation")
		read := func() (operations.EventPage, error) {
			queryCtx, cancel := context.WithTimeout(ctx, eventStreamPoll)
			defer cancel()
			return store.Events(queryCtx, token, scope, id, cursor, eventStreamPageSize)
		}
		page, err := read()
		if err != nil {
			writeOperationEventError(w, r, err, logger)
			return
		}
		controller := http.NewResponseController(w)
		setDeadline := func() error {
			end, _ := ctx.Deadline()
			limit := time.Now().Add(eventStreamPoll)
			if end.Before(limit) {
				limit = end
			}
			return controller.SetWriteDeadline(limit)
		}
		if r.Method != http.MethodHead {
			if err := setDeadline(); err != nil {
				writeOperationEventError(w, r, err, logger)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Accel-Buffering", "no")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		send := func(frame string) bool {
			if ctx.Err() != nil || setDeadline() != nil {
				return false
			}
			if _, err := io.WriteString(w, frame); err != nil || controller.Flush() != nil {
				return false
			}
			return controller.SetWriteDeadline(time.Time{}) == nil
		}
		if !send("retry: 2000\n: connected\n\n") {
			return
		}
		ticker := time.NewTicker(eventStreamPoll)
		defer ticker.Stop()
		for written := 0; ; {
			for _, event := range page.Events {
				data, err := json.Marshal(event)
				if err != nil || !send(fmt.Sprintf("id: %d\nevent: operation\ndata: %s\n\n", event.Sequence, data)) {
					return
				}
				cursor = event.Sequence
				written++
				if written >= eventStreamPageSize {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			page, err = read()
			if err != nil {
				if errors.Is(err, identity.ErrUnauthenticated) || errors.Is(err, identity.ErrForbidden) {
					send("event: access-revoked\ndata: {}\n\n")
				}
				return
			}
		}
	})
}

func operationStreamCursor(r *http.Request) (int64, error) {
	if len(r.URL.RawQuery) > 32 {
		return 0, operations.ErrInvalid
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, operations.ErrInvalid
	}
	after := values["after"]
	if len(values) > 1 || (r.URL.RawQuery != "" && len(after) != 1) {
		return 0, operations.ErrInvalid
	}
	var cursor int64
	for _, field := range [][]string{after, r.Header.Values("Last-Event-ID")} {
		if len(field) == 0 {
			continue
		}
		if len(field) != 1 || len(field[0]) > 19 {
			return 0, operations.ErrInvalid
		}
		parsed, err := strconv.ParseUint(field[0], 10, 63)
		if err != nil || strconv.FormatUint(parsed, 10) != field[0] {
			return 0, operations.ErrInvalid
		}
		cursor = int64(parsed)
	}
	return cursor, nil
}
