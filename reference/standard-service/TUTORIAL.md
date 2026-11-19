# Build a CRUD API with the Standard Service

This walkthrough uses Plumego's [standard application](./README.md) to show how
configuration, HTTP routing, and an in-memory item repository fit together.

## Run the service

From the repository root:

```bash
go -C reference/standard-service run .
```

The service listens on port 8080 by default. In another terminal, check liveness
and readiness:

```bash
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
```

The default configuration allows writes without a key for local development.
See [write-key setup](./README.md#with-write-key-enabled) before exposing the service.

## Try the API

Create an item and copy the ID from the response's `data.id` field:

```bash
curl -fsS -X POST http://localhost:8080/api/v1/items \
  -H 'Content-Type: application/json' \
  -d '{"name":"Widget","description":"A small widget"}'
```

Replace `YOUR_ITEM_ID` with that ID, then list, read, update, and delete the item:

```bash
item_id=YOUR_ITEM_ID
items=http://localhost:8080/api/v1/items

curl -fsS "$items"
curl -fsS "$items/$item_id"
curl -fsS -X PUT "$items/$item_id" \
  -H 'Content-Type: application/json' \
  -d '{"name":"New widget","description":"Updated description"}'
curl -fsS -X DELETE "$items/$item_id"
curl -si "$items/$item_id"
```

The delete returns HTTP 204. The final request returns HTTP 404 with a structured
`item.not_found` error. Items are stored in memory and disappear when the process exits.

## Read the code

1. [`main.go`](./main.go) loads configuration, registers routes, and starts the service with signal-driven shutdown.
2. [`internal/config/config.go`](./internal/config/config.go) defines defaults and validates configuration.
3. [`internal/app/app.go`](./internal/app/app.go) assembles middleware. [`routes.go`](./internal/app/routes.go) registers the versioned routes and wires the item handler to its service.
4. [`internal/handler/items.go`](./internal/handler/items.go) extracts path parameters, decodes JSON, and writes structured responses through the `contract` package.
5. [`internal/domain/item`](./internal/domain/item) owns the item model, service, and repository. Use the repository interface when adapting the example to persistent storage.

When adapting the service, start with the [production checklist](../../docs/start/production-checklist.md)
and consult the [reference application catalog](../README.md) for integration examples.
