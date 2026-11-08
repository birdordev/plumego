package webhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/messaging/pubsub"
)

type failingPublishBroker struct {
	pubsub.Broker
	calls int
}

func (b *failingPublishBroker) Publish(string, pubsub.Message) error {
	b.calls++
	return errors.New("password=publish-secret\r\nFORGED")
}

func TestInboundPublishFailureLogsSafely(t *testing.T) {
	for _, format := range []log.LoggerFormat{log.LoggerFormatText, log.LoggerFormatJSON} {
		for _, provider := range []string{"github", "stripe"} {
			t.Run(string(format)+"/"+provider, func(t *testing.T) {
				var output bytes.Buffer
				broker := &failingPublishBroker{}
				handler := NewInbound(WebhookInConfig{Enabled: true, Pub: broker, GitHubSecret: "signing-secret", StripeSecret: "signing-secret", StripeTolerance: time.Minute}, broker, log.NewLogger(log.LoggerConfig{Format: format, Output: &output}))
				var req *http.Request
				eventID := "evt_1\nFORGED"
				if provider == "github" {
					req = signedGitHubRequest([]byte(`{}`), "signing-secret", "push", eventID)
				} else {
					body, err := json.Marshal(map[string]string{"id": eventID, "type": "payment_intent.succeeded"})
					if err != nil {
						t.Fatal(err)
					}
					req = signedStripeRequest(body, "signing-secret", time.Now())
				}
				response := httptest.NewRecorder()
				if provider == "github" {
					handler.webhookInGitHub(response, req)
				} else {
					handler.webhookInStripe(response, req)
				}
				if response.Code != http.StatusInternalServerError || broker.calls != 1 {
					t.Fatalf("status=%d publish calls=%d", response.Code, broker.calls)
				}
				for _, secret := range []string{"publish-secret", "signing-secret", "password="} {
					if strings.Contains(output.String()+response.Body.String(), secret) {
						t.Fatalf("sensitive value leaked: %q", secret)
					}
				}
				if strings.Contains(output.String(), "\nFORGED") || strings.Contains(output.String(), "\r") {
					t.Fatal("unescaped control character in log")
				}
				if !strings.Contains(output.String(), "error_type") || !strings.Contains(output.String(), "event_id") {
					t.Fatalf("missing safe diagnostic fields: %s", output.String())
				}
				if !strings.Contains(response.Body.String(), "failed to forward event to internal bus") {
					t.Fatalf("unexpected response: %s", response.Body.String())
				}
			})
		}
	}
}
