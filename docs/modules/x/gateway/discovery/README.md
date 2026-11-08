# x/gateway/discovery — Service Discovery

> **Import path:** `github.com/birdordev/plumego/x/gateway/discovery` — sub-package of [`x/gateway`](../README.md). This primer mirrors the package path under `docs/modules/x/`.

**Purpose:** Service discovery primitives and adapters within the `x/gateway` family. Resolves logical service names to backend instance URLs via static config, Consul, etcd, or Kubernetes Endpoints.

**Status:** Experimental — API may change. Start feature discovery from [`x/gateway`](../README.md) before opening this package directly.

---

## First files to read

- `x/gateway/discovery/module.yaml`
- `x/gateway/discovery/discovery.go` — `Discovery` interface and `Instance` type
- `x/gateway/module.yaml`

---

## Key types

| Type / Constructor | Description |
|---|---|
| `Discovery` | Interface: `Resolve(ctx, serviceName) ([]Instance, error)` and `Watch` |
| `Instance` | Resolved backend with `URL()` and health metadata |
| `Static` / `NewStatic` | Fixed service-to-URL map; useful for local dev and tests |
| `Consul` / `NewConsul` | HashiCorp Consul health-passing service discovery |
| `Etcd` / `NewEtcd` | etcd v3 HTTP gateway service discovery |
| `Kubernetes` / `NewKubernetes` | Kubernetes Endpoints API discovery |

---

## Boundary rules

- Start new gateway/proxy work from `x/gateway`, not here.
- Keep transport concerns out of discovery; `Discovery` resolves names to URLs only.
- Do not require `core` knowledge or inject tenant policy.
- Static discovery is the safe default for tests and fixed-topology deployments.

---

## Validation

```bash
go test -race -timeout 60s ./x/gateway/discovery/...
go vet ./x/gateway/discovery/...
```
