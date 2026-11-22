# with-frontend Scenario Reference

`reference/with-frontend` is a non-canonical scenario reference.

It shows how to serve a Single-Page Application alongside a JSON API from the
same plumego service using `x/frontend`. API routes take precedence over the
SPA catch-all so clean-URL routing works without a separate server.

`x/frontend` is beta-stable. Use this scenario reference as wiring guidance.

## What It Demonstrates

- JSON API endpoint (`GET /api/status`) and SPA catch-all coexisting in one process
- Dedicated API router mounted at `API_PREFIX` before the frontend mount
- In-memory `fstest.MapFS` as a self-contained demo asset source; replace with
  `embed.FS` or `frontend.RegisterFromDir()` for real builds
- Cache-control headers: `immutable` for versioned assets, `no-cache` for `index.html`
- SPA fallback for browser routes outside the API prefix. The API router returns JSON 404 or 405 even when the client accepts HTML.
- `API_PREFIX`, `UI_PREFIX`, and `ASSETS_DIR` configurable via environment variables

## Routes

- `GET /api/status` — JSON status response
- `GET /*` — serves embedded assets; unknown paths fall back to `index.html`

## Run

```bash
cd reference/with-frontend
go run .
```

Then open `http://localhost:8088` in a browser to see the embedded SPA, and
`http://localhost:8088/api/status` for the JSON API endpoint.

## Configuration

| Variable | Default | Notes |
|----------|---------|-------|
| `APP_ADDR` | `:8088` | Listen address |
| `API_PREFIX` | `/api` | Non-root URL prefix reserved for JSON API routes |
| `UI_PREFIX` | `/` | URL prefix for static assets |
| `ASSETS_DIR` | `./assets` | Asset directory (used with `RegisterFromDir`) |

Copy `env.example` to `.env` to override defaults locally.

## Swapping in Real Assets

Replace `fstest.MapFS` in `internal/app/routes.go` with your build output:

```go
// Using embed.FS (reproducible builds, assets compiled into the binary):
//go:embed dist/*
var dist embed.FS
frontend.RegisterFS(a.Core, http.FS(dist), ...)

// Using a filesystem directory (hot-reload friendly):
frontend.RegisterFromDir(a.Core, "./dist", ...)
```

See [x/frontend documentation](../../docs/modules/x/frontend/) for full options.

## Graduating to Production

- Replace `fstest.MapFS` with `embed.FS` or `frontend.RegisterFromDir`.
- Set `APP_ADDR`, `API_PREFIX`, and `UI_PREFIX` via environment variables.
- Add auth middleware to API routes before the frontend mount if needed.
- Review cache-control headers to match your CDN and deployment strategy.
