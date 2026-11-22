package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	origin, err := secureURL(os.Getenv("BROWSER_PUBLIC_ORIGIN"))
	if err != nil || (origin.Path != "" && origin.Path != "/") {
		return errors.New("BROWSER_PUBLIC_ORIGIN must be an HTTPS origin or loopback HTTP origin")
	}
	issuer, err := secureURL(os.Getenv("OIDC_ISSUER"))
	if err != nil {
		return errors.New("OIDC_ISSUER must use HTTPS or loopback HTTP")
	}
	callback := origin.ResolveReference(&url.URL{Path: "/auth/callback"})
	discoveryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	provider, err := newOIDC(discoveryCtx, issuer.String(), os.Getenv("OIDC_CLIENT_ID"), os.Getenv("OIDC_CLIENT_SECRET"), callback.String(), os.Getenv("OIDC_ADMIN_GROUP"))
	if err != nil {
		return err
	}

	address := os.Getenv("BROWSER_LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:8091"
	}
	app, err := newBrowser(address, origin, provider, newSessions(time.Now))
	if err != nil {
		return err
	}
	if err := app.Prepare(); err != nil {
		return err
	}
	server, err := app.Server()
	if err != nil {
		return err
	}
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", address)
	if err != nil {
		return err
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	select {
	case err := <-serveDone:
		return err
	case <-ctx.Done():
		drain, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		err := app.Shutdown(drain)
		if err != nil {
			err = errors.Join(err, server.Close())
		}
		if serveErr := <-serveDone; !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	}
}

func secureURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return nil, errors.New("invalid browser URL")
	}
	host := parsed.Hostname()
	address, _ := netip.ParseAddr(host)
	if parsed.Scheme == "https" || parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || address.IsLoopback()) {
		return parsed, nil
	}
	return nil, errors.New("browser URL must use HTTPS")
}
