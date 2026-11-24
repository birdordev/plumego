package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"os"
	"time"
)

func workerDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, errors.New("invalid " + name)
	}
	return value, nil
}

func startWorkerProbe(address string, db *sql.DB, accepting func() bool) (func() error, <-chan error, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", readinessHandler(db, accepting))
	return startSchedulerHTTP(address, mux)
}

func startSchedulerHTTP(address string, handler http.Handler) (func() error, <-chan error, error) {
	if address == "" {
		return func() error { return nil }, nil, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, nil, errors.New("could not bind scheduler diagnostics listener")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	done := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			done <- errors.New("scheduler diagnostics listener failed")
		}
	}()
	closeProbe := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			return errors.New("scheduler diagnostics listener did not shut down")
		}
		return nil
	}
	return closeProbe, done, nil
}
