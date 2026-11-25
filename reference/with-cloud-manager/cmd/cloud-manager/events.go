package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/birdordev/plumego/contract"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/router"

	"with-cloud-manager/internal/delivery"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
)

func runEventCommand(ctx context.Context, db *sql.DB, args []string, profile string, out io.Writer) error {
	var configs []delivery.Config
	if err := loadPrivateProfile(profile, &configs); err != nil {
		return err
	}
	configured := make(map[string]delivery.Destination, len(configs))
	destinations := make([]delivery.Destination, 0, len(configs))
	for _, cfg := range configs {
		destination, err := delivery.NewDestination(cfg)
		if err != nil {
			return err
		}
		if _, duplicate := configured[cfg.ID]; duplicate {
			return delivery.ErrBinding
		}
		configured[cfg.ID] = destination
		destinations = append(destinations, destination)
	}
	store := delivery.NewStore(db)
	lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if args[0] == "event-publisher" {
		publisher, err := delivery.NewPublisher(lookup, store, destinations, nil)
		if err != nil {
			return safeDeliveryError(err)
		}
		return safeDeliveryError(publisher.Run(ctx))
	}
	dest, exists := configured[args[1]]
	if !exists {
		return delivery.ErrBinding
	}
	return safeDeliveryError(manageEventSink(lookup, store, dest, args, out))
}

func manageEventSink(ctx context.Context, store delivery.Store, dest delivery.Destination, args []string, out io.Writer) error {
	switch args[0] {
	case "event-register":
		return store.Register(ctx, dest)
	case "event-disable":
		return store.Disable(ctx, dest)
	case "event-retry":
		return store.Retry(ctx, dest, args[2])
	case "event-failures":
		events, err := store.Failures(ctx, dest)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(events)
	}
	return errors.New("invalid event command")
}

func safeDeliveryError(err error) error {
	if err == nil || errors.Is(err, delivery.ErrBinding) || errors.Is(err, delivery.ErrNotFailed) {
		return err
	}
	return errors.New("event delivery is unavailable")
}

func operationEventsHandler(store operations.Store, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		token, authenticated := requestCredential(r)
		if !authenticated {
			writeAccountError(w, r, identity.ErrUnauthenticated, logger)
			return
		}
		after, limit, err := eventQuery(r.URL.RawQuery)
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		scope := identity.Scope{Tenant: router.Param(r, "tenant"), Project: router.Param(r, "project")}
		value, err := store.Events(ctx, token, scope, router.Param(r, "operation"), after, limit)
		if errors.Is(err, operations.ErrInvalid) {
			err = identity.ErrAccountInvalid
		}
		if err != nil {
			writeAccountError(w, r, err, logger)
			return
		}
		if err := contract.WriteResponse(w, r, http.StatusOK, value, nil); err != nil {
			logger.Warn("operation events response write failed", plumelog.Fields{})
		}
	})
}

func eventQuery(raw string) (int64, int, error) {
	if len(raw) > 1024 {
		return 0, 0, identity.ErrAccountInvalid
	}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return 0, 0, identity.ErrAccountInvalid
	}
	var after int64
	limit := 50
	for key, values := range query {
		if len(values) != 1 {
			return 0, 0, identity.ErrAccountInvalid
		}
		n, err := strconv.ParseUint(values[0], 10, 63)
		if err != nil {
			return 0, 0, identity.ErrAccountInvalid
		}
		switch key {
		case "after":
			after = int64(n)
		case "limit":
			if n < 1 || n > 100 {
				return 0, 0, identity.ErrAccountInvalid
			}
			limit = int(n)
		default:
			return 0, 0, identity.ErrAccountInvalid
		}
	}
	return after, limit, nil
}
