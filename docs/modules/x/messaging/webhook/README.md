# x/messaging/webhook — Webhook Ingress and Egress

> **Import path:** `github.com/birdordev/plumego/x/messaging/webhook` — sub-package of [`x/messaging`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

**Purpose:** Webhook ingress, egress, and bridge primitives within the `x/messaging` family. Handles inbound signature verification (GitHub, Stripe, HMAC), outbound delivery service with retry and store, and a pub/sub bridge for routing inbound events to outbound targets.

**Status:** Experimental — API may change. Start app-facing messaging feature discovery from [`x/messaging`](../README.md) before opening this package directly.

---

## First files to read

- `x/messaging/webhook/module.yaml`
- `x/messaging/webhook/in.go` — `Inbound` (inbound receiver and route registrar)
- `x/messaging/webhook/out.go` — `Outbound` (outbound management endpoints)
- `x/messaging/webhook/bridge.go` — `WebhookBridge` (pubsub-to-webhook routing)
- `x/messaging/module.yaml`

---

## Key types

| Type / Constructor | Description |
|---|---|
| `Service` / `NewService` | Outbound webhook delivery service with queue and retry |
| `Inbound` | Inbound webhook receiver with HMAC/provider signature verification |
| `Outbound` / `NewOutbound` | Management endpoint handler for outbound webhooks |
| `WebhookBridge` | Routes inbound pubsub events to outbound webhook targets |
| `NewMemStore` | In-memory webhook subscription store for local dev and tests |
| `VerifyHMAC` | Generic HMAC-SHA256 signature verification |
| `VerifyGitHub` | GitHub webhook signature verification (`X-Hub-Signature-256`) |
| `VerifyStripe` | Stripe webhook signature verification with timestamp tolerance |

---

## Boundary rules

- Fail closed on verification errors; do not allow unverified events to be published.
- Never log secrets, signatures, or raw payloads; scrub before logging.
- Route registration must be explicit; no auto-discovery or reflection-based mounting.
- Keep this as a subordinate entry point; start new app-facing messaging work from `x/messaging`.
- Keep business-specific webhook workflows in application code, not in this package.

---

## Validation

```bash
go test -race -timeout 60s ./x/messaging/webhook/...
go vet ./x/messaging/webhook/...
```
