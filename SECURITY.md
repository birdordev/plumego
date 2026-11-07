# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 1.1.x   | :white_check_mark: |
| 1.0.x   | Security fixes only |
| < 1.0   | :x:                |

The `main` branch always receives security fixes first; fixes are backported
to the supported release lines listed above.

Experimental extension families under `x/` (`ai`, `data`, `fileapi`,
`openapi`, `resilience`, `rpc`, `validate`) do not carry a stability or
support guarantee — see
[`docs/concepts/extension-maturity.md`](docs/concepts/extension-maturity.md)
for the current maturity of each family.

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub
issues.**

Use [GitHub private vulnerability reporting](../../security/advisories/new)
(the "Report a vulnerability" button on the Security tab) instead. This
creates a private advisory that only maintainers can see until a fix is
released.

Please include:

- A description of the vulnerability and its potential impact.
- The affected version(s) and package path(s) (e.g. `security/jwt`,
  `middleware/bodylimit`).
- Steps to reproduce or a proof-of-concept, if available.
- Any suggested mitigation you have in mind.

You can expect:

- **Acknowledgment** within 48 hours.
- **A status update** at least once every 7 days until resolution.
- **Coordinated disclosure**: we will agree on a public disclosure date with
  you after a fix is available, and credit you in the release notes unless
  you prefer otherwise.

If you are unsure whether something is a security issue, err on the side of
reporting it privately.

## Security Design Principles

The codebase is held to the following rules (enforced by review and by the
checks under `internal/checks/`):

- **Fail closed.** Authentication, verification, and policy errors deny
  access by default; a misconfigured security component returns an error
  rather than silently permitting requests.
- **Timing-safe comparisons.** Secret, token, and signature comparisons use
  `crypto/subtle` — never `==` on secret material.
- **No secrets in logs or responses.** Tokens, signatures, private keys, and
  derived values that can replay credentials are never logged or returned in
  error messages.
- **Bounded inputs.** Request bodies, remote fetches, and decoded payloads
  are size-capped by default; see the body-limit middleware and the
  `Max*Bytes` constants in the relevant packages.
- **Stdlib-only core.** The nine stable roots (`core`, `router`, `contract`,
  `middleware`, `security`, `store`, `health`, `log`, `metrics`) have zero
  third-party dependencies, which keeps the auditable supply-chain surface
  of the core minimal. External dependencies are confined to `x/` extensions
  and `reference/` applications.

## Scope Notes

- Vulnerabilities in `x/*` extensions that depend on third-party packages
  are still in scope; please report them the same way.
- Vulnerabilities in `reference/` example applications matter less (they are
  teaching tools, not production services), but misconfigurations that a
  reader would copy into production are in scope.

Thank you for helping keep Plumego and its users safe.
