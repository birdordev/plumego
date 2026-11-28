package events

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"time"

	"with-cloud-manager/app/clients/projecthttp"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/delivery"
)

func RunEventCommand(ctx context.Context, db *sql.DB, args []string, profile string, in io.Reader, out io.Writer) error {
	var configs []delivery.Config
	if err := cloud.LoadPrivateProfile(profile, &configs); err != nil {
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
	return safeDeliveryError(manageEventSink(lookup, store, dest, args, in, out))
}

func manageEventSink(ctx context.Context, store delivery.Store, dest delivery.Destination, args []string, in io.Reader, out io.Writer) error {
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
	case "event-reconcile":
		return reconcileEventReceipts(ctx, store, dest, in, out)
	}
	return errors.New("invalid event command")
}

var ErrEventReconciliationIncomplete = errors.New("terminal receipt reconciliation found differences")

func reconcileEventReceipts(ctx context.Context, store delivery.Store, dest delivery.Destination, in io.Reader, out io.Writer) error {
	raw, err := projecthttp.ReadInput(in, 1<<20)
	if err != nil {
		return delivery.ErrReceiptManifest
	}
	var manifest delivery.ReceiptManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return delivery.ErrReceiptManifest
	}
	report, err := store.ReconcileTerminalReceipts(ctx, dest, manifest)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return err
	}
	if !report.Clean {
		return ErrEventReconciliationIncomplete
	}
	return nil
}

func safeDeliveryError(err error) error {
	if err == nil || errors.Is(err, delivery.ErrBinding) || errors.Is(err, delivery.ErrNotFailed) ||
		errors.Is(err, delivery.ErrReceiptManifest) || errors.Is(err, delivery.ErrReconcileWindowTooLarge) ||
		errors.Is(err, ErrEventReconciliationIncomplete) {
		return err
	}
	return errors.New("event delivery is unavailable")
}
