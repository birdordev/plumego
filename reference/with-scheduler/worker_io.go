package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type workerIO func(context.Context, time.Duration) error

func newWorkerHTTPIO(endpoint, token string) (workerIO, error) {
	dest, err := referenceEndpoint(endpoint, token)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout:       32 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return func(ctx context.Context, delay time.Duration) error {
		body := strings.NewReader(strconv.FormatInt(delay.Milliseconds(), 10))
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, dest, body)
		if err != nil {
			return errors.New("could not build downstream I/O request")
		}
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("downstream I/O unavailable")
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != http.StatusNoContent {
			return errors.New("downstream I/O did not complete")
		}
		return nil
	}, nil
}
