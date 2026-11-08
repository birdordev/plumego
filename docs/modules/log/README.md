# log

## Purpose

`log` defines structured logging contracts and base implementations.

## v1 Status

- `ga` in the Plumego v1 support matrix
- Public compatibility is expected for the stable package surface

## Use this module when

- adjusting logger interfaces
- changing base logger behavior
- wiring structured fields at the library boundary

## Do not use this module for

- secret-bearing payload logging
- business event schema ownership
- feature-specific transport logic
- request id generation

## First files to read

- `log/module.yaml`
- `log/*.go`
- `AGENTS.md` security rules

## Stable behavior

- `NewLogger` is the canonical constructor for text, JSON, and discard logging.
- `LoggerConfig.Fields` are copied into each logger at construction.
- Per-entry fields override logger fields.
- When a log call receives multiple `Fields` arguments, they are merged in
  order and later maps override earlier maps.
- `WithFields` is the only derived-logger field attachment API; the v1
  contract does not keep one-field shortcut methods.
- Unknown `LoggerConfig.Format` values panic during construction.
- Text output sorts field keys and escapes ambiguous keys or values, including
  controls, Unicode whitespace, quotes, and backslashes. Simple
  ASCII keys made of letters, digits, `_`, `-`, `.`, or `/` stay unquoted.
- JSON output owns the reserved `time`, `level`, and `msg` keys.
- JSON output preserves integer precision and replaces unsupported field values
  with a fixed placeholder, preserving safe siblings in common nested maps and
  slices. Invalid floating-point values retain their numeric string form.
- Nested map and slice normalization is bounded; cycles and excessive nesting
  produce truncation placeholders instead of unbounded recursion.
- Output-write failure diagnostics are emitted once per logger family and omit
  raw writer errors, which may contain the rejected log payload.
- Callers own message and field selection: loggers do not automatically redact
  arbitrary valid messages or fields. Do not pass credentials or private keys.
  Field maps are copied shallowly; callers retain ownership of nested values
  and must not mutate them concurrently with logging.
- Context-aware methods keep the request-scoped call shape but do not read
  transport metadata from `context.Context`.
- The glog-style file backend, flag initialization, and rotation helpers are
  internal backend behavior. They are not application bootstrap APIs.

## Canonical change shape

- preserve `StructuredLogger` and the canonical `NewLogger` path
- use `LoggerConfig.Format` to select text/json/discard backends instead of parallel constructors
- keep unknown format handling deterministic and non-emitting
- keep text logger backend ownership constructor-local; do not route the stable path through a package-global singleton
- keep lifecycle/start-stop hooks and CLI flag bootstrap out of the stable public path
- never log secrets, tokens, signatures, or private keys
- keep reusable test logging helpers out of stable `log`; use `x/observability/testlog`
- attach `request_id` and other transport metadata explicitly at call sites; stable `log` must not read them from context
