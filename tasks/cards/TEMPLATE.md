# Card XXXX

Milestone:
Recipe: specs/change-recipes/<recipe>.yaml
Context Package:
Priority:
State: active
Primary Module:
Owned Files:
Depends On:

## Goal

<!-- One sentence. Observable end-state, not steps. -->

## Scope

<!-- Exact files or packages in scope. One primary module; max 5 files. -->

## Non-goals

<!-- Hard stops. Agent must not cross these. -->

## Files

<!-- Max 5 files. List each on its own line. -->

## Impact and Ownership

<!-- Public API, dependencies, behavior, security, docs, and mutable-data ownership.
     For lifecycle/storage: starting state, event, success/failure state,
     retained resources, commit boundary, and legal retry.
     For a finding: trigger, observed/expected behavior, and evidence status
     (reproduced / source-confirmed / unverified). -->

## Acceptance Tests

<!-- Pre-written failing test function(s) that define done.
     Format: <file>: <TestFunctionName>
     Example:
       middleware/timeout_test.go: TestTimeoutHeaderPresent
       contract/errors_test.go: TestWriteErrorCode
     Leave blank if the card has no behavior change. -->

## Tests

<!-- Additional tests beyond acceptance tests (edge cases, negative paths). -->

## Docs Sync

<!-- Docs files to update if behavior/API/config/security changes. -->

## Validation

<!-- Exact commands that must exit 0.
     Max 3 commands. Minimum for behavior changes:
     go test -race -timeout 60s ./<module>/... -->

## Done Definition

- [ ] Acceptance Tests pass.
- [ ] All Validation commands exit 0.
- [ ] gofmt -l . produces no output.
- [ ] Docs Sync targets updated (if applicable).

## Outcome

<!-- Agent fills this after completion: what changed and why; actual commands
     and status; skipped checks; residual risks. Tests passing do not establish
     that an entire module has been audited. -->
