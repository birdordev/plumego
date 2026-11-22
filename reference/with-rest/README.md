# reference/with-rest

Canonical Plumego example for building a JSON REST API with `x/rest` resource
controllers alongside plain HTTP handlers.

It shows how to add `x/rest` resource controllers to a service that still starts
from Plumego's canonical application shape.

`x/rest` is a beta extension. Use this scenario reference as wiring guidance.

## What It Demonstrates

| Concern | Where |
|---|---|
| Route registration (explicit, one line per route) | `internal/app/routes.go` |
| JSON request decoding via `x/validate.Bind[T]` | `internal/handler/create_item.go` |
| JSON response writing via `contract.WriteResponse` | all handlers |
| Canonical success response envelope | all handlers |
| Canonical error response via `contract.WriteError` | `create_item.go`, `x/rest` controllers |
| Validation errors with per-field detail | `create_item.go` + `playground/adapter.go` |
| `x/rest.DBResourceController` for CRUD | `internal/app/app.go`, `internal/app/routes.go` |
| In-memory repository implementing `rest.Repository[T]` | `internal/domain/user/user.go` |
| Handler/service separation | `internal/handler/` vs `internal/domain/` |
| Standard bootstrap: config → app → routes → start | `main.go` + `internal/app/app.go` |

## What It Intentionally Excludes

- Database integration (all storage is in-memory)
- Authentication or authorization
- Tenant isolation
- OpenAPI generation (the `make spec` target is a placeholder)
- Cursor-based pagination
- Frontend, background jobs, messaging, or RPC
- Non-canonical response shapes

## API

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness probe |
| `GET` | `/readyz` | Readiness probe |
| `GET` | `/api/hello` | Demo greeting endpoint |
| `GET` | `/api/users` | List users |
| `GET` | `/api/users/:id` | Get user by ID |
| `POST` | `/api/users` | Create user |
| `PUT` | `/api/users/:id` | Replace user |
| `DELETE` | `/api/users/:id` | Delete user |
| `POST` | `/api/items` | Create item (plain handler with x/validate.Bind[T]) |

`GET /api/users` supports either `page` and `page_size` or `limit` and `offset`.
The repository returns a stable ID-ordered page and reports the full item
count in pagination metadata. Requests that mix the two modes return 400.

## Run

```bash
cd reference/with-rest
go run .
```

The server listens on `:8084` by default. Override with `APP_ADDR=:9000`.

## curl Examples

### Success cases

List users:
```bash
curl -s http://localhost:8084/api/users | jq .
```
```json
{
  "data": [{"id":"u_1","name":"Ada"}],
  "meta": {
    "pagination": {"page":1,"page_size":20,"total_items":1,"total_pages":1,"has_next":false,"has_prev":false}
  }
}
```

Get user by ID:
```bash
curl -s http://localhost:8084/api/users/u_1 | jq .
```
```json
{"data":{"id":"u_1","name":"Ada"}}
```

Create user:
```bash
curl -s -X POST http://localhost:8084/api/users \
  -H 'Content-Type: application/json' \
  -d '{"name":"Bob"}' | jq .
```
```json
{"data":{"id":"u_2","name":"Bob"}}
```

Replace user:
```bash
curl -s -X PUT http://localhost:8084/api/users/u_1 \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ada Lovelace"}' | jq .
```
```json
{"data":{"id":"u_1","name":"Ada Lovelace"}}
```

Delete user:
```bash
curl -s -o /dev/null -w "%{http_code}" -X DELETE http://localhost:8084/api/users/u_1
# 204
```

Create item (plain handler, shows x/validate.Bind[T]):
```bash
curl -s -X POST http://localhost:8084/api/items \
  -H 'Content-Type: application/json' \
  -d '{"name":"widget"}' | jq .
```
```json
{"data":{"id":"demo-item","name":"widget"}}
```

### Failure cases

Not found:
```bash
curl -s http://localhost:8084/api/users/nonexistent | jq .
```
```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "record not found",
    "category": "client_error",
    "type": "resource_not_found"
  }
}
```

Invalid JSON:
```bash
curl -s -X POST http://localhost:8084/api/items \
  -H 'Content-Type: application/json' \
  -d '{"name":' | jq .
```
```json
{
  "error": {
    "code": "INVALID_JSON",
    "message": "invalid request body",
    "category": "client_error",
    "type": "bad_request"
  }
}
```

Missing required field:
```bash
curl -s -X POST http://localhost:8084/api/items \
  -H 'Content-Type: application/json' \
  -d '{}' | jq .
```
```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "message": "validation failed",
    "category": "validation_error",
    "type": "validation_failure",
    "details": {
      "fields": [{"field":"Name","tag":"required","message":"Name is required"}]
    }
  }
}
```

## Tests

```bash
cd reference/with-rest
go test -race ./...
```

Tests cover:
- `internal/handler` — `CreateItem` handler: valid request, missing required field (field-level error detail), malformed JSON (invalid_json code)
- `internal/app` — route registration shape (`app_test.go`); full HTTP round-trips for all user CRUD paths (`routes_http_test.go`): list, get, not-found, create, invalid-JSON, update, update-not-found, delete, delete-not-found

## How to Extend

**Add a new `x/rest` resource:**
1. Add model and in-memory repository in `internal/domain/<name>/`.
   Implement `rest.Repository[T]`; return `sql.ErrNoRows` for not-found in
   `FindByID`, `Update`, and `Delete`.
2. Create a `*rest.DBResourceController[T]` in `app.New` (`internal/app/app.go`).
3. Register each action explicitly in `RegisterRoutes` (`internal/app/routes.go`).
4. Add HTTP round-trip tests in `internal/app/routes_http_test.go` following the existing pattern.

**Add a plain HTTP handler (no x/rest):**
1. Implement the handler in `internal/handler/`.
2. Decode with `x/validate.Bind[T]` (or `json.NewDecoder`) and respond with
   `contract.WriteResponse` / `contract.WriteError`.
3. Register in `routes.go`.
4. Add handler tests.

**Swap in-memory storage for a real database:**
See `PRODUCTION_CHECKLIST.md`. Replace `internal/domain/user/user.go` with a
repository that calls your database; keep the same `rest.Repository[T]` interface.
No other files need to change.
