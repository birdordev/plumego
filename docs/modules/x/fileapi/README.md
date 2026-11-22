# x/fileapi

## Purpose

`x/fileapi` is the app-facing transport surface for tenant-aware file upload, download, metadata lookup, and temporary URL endpoints.

## v1 Status

- `experimental` in the Plumego v1 support matrix
- Included in repository release scope, but compatibility is not frozen

## Use this module when

- the task is file upload or download HTTP behavior
- the task is multipart parsing, file response headers, or file URL transport
- the task is request-context-based tenant extraction for file APIs

## Do not use this module for

- pure storage interfaces
- tenant-aware storage backend implementation
- application bootstrap
- business-specific file workflows

## First files to read

- `x/fileapi/module.yaml`
- `x/fileapi/handler.go`
- `specs/task-routing.yaml`

## Public entrypoints

- `Handler`
- `NewHandler`
- `WithUserID`
- `UserIDFromContext`

## Main risks when changing this module

- tenant extraction regression
- multipart request parsing regression
- streaming response regression
- non-canonical error handling

## Canonical change shape

- keep handler behavior transport-only
- read tenant identity from request context, not body or path
- use `x/tenant/core.WithTenantID(...)` for tenant attachment and `x/fileapi.WithUserID(...)` when uploader identity is needed
- delegate storage and metadata behavior to `x/data/file`
- write JSON success bodies through `contract.WriteResponse`; keep `Download` as an explicit streaming response
- return stable validation errors for malformed query parameters (`page`, `page_size`, time filters, `expiry`) and avoid exposing backend error text in client responses
- keep stable storage contracts, shared types, and errors in `store/file`
- keep route wiring in the application layer; bind `Handler.Upload`, `Download`, `GetInfo`, `GetURL`, `Delete`, and `List` explicitly instead of relying on library-owned route tables

## Boundary with data and store

- `x/fileapi` is the app-facing file transport entrypoint
- `x/data/file` owns tenant-aware storage, metadata, and temporary URL implementations
- `x/data/file.DBMetadataManager` is the PostgreSQL-backed metadata implementation;
  use its error-returning constructor for dynamic wiring
- `store/file` owns transport-agnostic interfaces, errors, and shared types
- if the change is about multipart parsing, status codes, or response headers, keep it here
- if the change is about tenant path layout, metadata persistence, or storage backends, move it to `x/data/file`
- if the change is about stable storage contracts or shared file types, keep it in `store/file`

`Delete` is intentionally a transport-level soft-delete endpoint: after tenant
authorization it delegates to `MetadataManager.Delete` and does not call
`Storage.Delete` directly. Object lifecycle policy, hard deletion, background
cleanup, and backend-specific retention behavior belong in `x/data/file` or the
application wiring around it.

## Validation commands

- `go test -race -timeout 60s ./x/fileapi/...`
- `go test -timeout 20s ./x/fileapi/...`
- `go vet ./x/fileapi/...`

## Upload and download limits

`Handler.Upload` accepts files up to 100 MiB by default. `WithMaxSize` sets
the file byte limit. Zero allows only empty files; a negative value restores
the default. The multipart request has a separate total limit of the file
limit plus 1 MiB for form fields and framing. Exceeding either limit returns
HTTP 413.

Download responses use `Cache-Control: private, no-store`, including authorization and range failures.

`Handler.GetURL` returns the storage provider's URL as a JSON string.
Decode the JSON response normally to obtain the full URL, including any
query separators.

For large multipart uploads parsed by the handler, the S3 backend can borrow
the validated multipart file. It hashes and sends that file without a second
disk staging pass. The handler owns cleanup of the multipart form.

`Handler.EnableDeferredAccessUpdates` can be configured before serving to
move best-effort access tracking to a bounded queue. Repeated updates for a
pending file coalesce, and a full queue drops new updates. The returned stop
function cancels the worker and waits for it to exit. Metadata implementations
must honor cancellation on the context passed to each update.

HEAD returns headers without a body after the same tenant authorization as GET.
A stored content hash provides an ETag when available, and the file modification
time provides Last-Modified. Conditional requests work with seekable readers
and sequential readers with a known size. GET supports byte ranges on seekable
readers. The S3 backend supports single byte ranges through a signed ranged
read. It serves HEAD from object metadata and avoids opening an object body for
304 and 416 responses. Multi-range S3 requests fall back to the full
representation, as do range requests to sequential-only backends.

For large multipart uploads, a storage backend implementing
`PutStaged(context.Context, file.PutOptions, io.ReaderAt)` can reuse the
already staged file. Backends can also handle uploads through the ordinary
streaming `Put` method.
