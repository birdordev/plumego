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
- JSON output owns the reserved `time`, `level`, and `msg` keys. Values
  supplied for those keys are skipped before field encoding.
- JSON output preserves integer precision and replaces unsupported field values
  with a fixed placeholder, preserving safe siblings in common nested maps and
  slices. Ordinary nil maps and slices encode as `null`, while non-nil empty maps and
  slices keep their empty JSON shape. Invalid floating-point values retain their
  numeric string form.
- JSON normalization and text container formatting use bounded traversal;
  cycles and excessive nesting produce truncation placeholders instead of
  unbounded recursion. Text formatting also bounds pointers and struct fields.
  Custom `fmt.Formatter`, `fmt.Stringer`, and `error` methods retain their
  formatting behavior and own their termination; callers must keep them safe.
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

## Security

Text log messages escape control characters and Unicode line separators to prevent forged log entries. Structured fields retain their existing escaping rules. Custom `StructuredLogger` implementations must provide safe encoding.

JSON normalization stops traversal of arrays, ordinary slices, and string-keyed maps when the per-field node budget is exhausted, instead of allocating placeholders for every remaining element. Truncated containers contain a fixed marker; retained map entries are unspecified because map iteration order is unspecified. The budget applies to containers handled by the normalizer. The standard JSON encoder still handles byte slices, structs, other values, and custom encoding methods with their usual semantics. The budget does not cap individual strings.

Custom backends receive original, unescaped values. Encode messages and field keys/values at the output boundary; escape control characters and Unicode line separators for text, or use a JSON encoder. Do not interpolate raw values into record delimiters. Callers must exclude secrets before calling the logger; safe encoding does not redact credentials or backend error messages.

Text field traversal consumes its node budget even when visiting a value at the depth limit. Wide containers at that boundary remain bounded before temporary entries are collected or sorted. Custom formatters still own their execution.
