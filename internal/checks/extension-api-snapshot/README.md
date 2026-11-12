# extension-api-snapshot

`extension-api-snapshot` records the exported Go API surface for an extension
package tree, compares two recorded snapshots, and checks the stable API
against the repository baselines.

It is a local evidence tool for `docs/reference/extension-stability-policy.md`. It does
not fetch tags, decide promotion status, or update module manifests.

Generate a snapshot:

```bash
go run ./internal/checks/extension-api-snapshot \
  -module ./x/rest/... \
  -out /tmp/plumego-x-rest-api.snapshot
```

Compare two snapshots:

```bash
go run ./internal/checks/extension-api-snapshot \
  -compare /tmp/plumego-x-rest-api.old /tmp/plumego-x-rest-api.new
```

The snapshot is deterministic text. It includes exported top-level constants,
variables, functions, types, exported struct fields, and methods on exported
receiver types. It excludes test files and unexported implementation details.

Run `make api-check` to check every stable root against its committed snapshot.
