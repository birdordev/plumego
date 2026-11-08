# x/data/file

> **Import path:** `github.com/birdordev/plumego/x/data/file` — sub-package of [`x/data`](../README.md).

## Purpose

`x/data/file` provides tenant-aware file storage and metadata implementations
behind the stable `store/file` contracts. It owns local filesystem and
S3-compatible backends along with a PostgreSQL metadata manager.

## Status

`experimental` — API not frozen. See [`docs/concepts/extension-maturity.md`](../../../../concepts/extension-maturity.md).

## Use this module when

- implementing tenant-isolated local or S3 file storage
- managing file metadata in PostgreSQL alongside file bytes
- generating AWS Signature V4 signed URLs for S3 requests
- adapting a storage backend to the `store/file.Storage` interface

## Do not use this module for

- HTTP upload/download handling — use `x/fileapi`
- storage interface definitions — those live in `store/file`
- business-level file repositories or domain-specific metadata schemas

## Entry points

| Symbol | Purpose |
|---|---|
| `LocalStorage` | Tenant-isolated local filesystem backend |
| `S3Storage` | Tenant-isolated S3-compatible backend with SigV4 signing |
| `DBMetadataManager` | PostgreSQL-backed file metadata manager |

## Notes

- Tenant path isolation is enforced in storage path construction; verify it in
  code review when changing path-building logic.
- `NewDBMetadataManagerE` is preferred over `NewDBMetadataManager` when dynamic
  database wiring is required (it returns a constructor error rather than panicking).

## Validation

```bash
go test -race -timeout 60s ./x/data/file/...
go vet ./x/data/file/...
```
