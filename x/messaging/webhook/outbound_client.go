// Package webhookout provides reliable webhook delivery with retry and persistence.
//
// This package implements a production-ready webhook delivery system featuring:
//   - Automatic retry with exponential backoff
//   - Persistent queue for delivery guarantees
//   - HMAC signature generation for security
//   - Delivery tracking and metrics
//   - Configurable timeout and retry policies
//   - Dead letter queue for failed deliveries
//
// The package ensures reliable webhook delivery even in the face of temporary
// failures or network issues. All webhooks are signed with HMAC for security.
//
// Example usage:
//
//	import "github.com/birdordev/plumego/x/messaging/webhook"
//
//	// Create a webhook sender
//	sender := webhookout.NewSender(webhookout.Config{
//		Secret:      []byte("webhook-signing-secret"),
//		MaxRetries:  3,
//		Timeout:     10 * time.Second,
//		StoragePath: "/data/webhooks",
//	})
//
//	// Send a webhook
//	err := sender.Send(webhookout.Event{
//		URL:     "https://customer.example.com/webhook",
//		Payload: eventData,
//	})
package webhook

import (
	"net"
	"net/http"
	"time"
)

func NewHTTPClient(timeout time.Duration) *http.Client {
	// Shared transport for performance; tune as needed
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
	}
}
