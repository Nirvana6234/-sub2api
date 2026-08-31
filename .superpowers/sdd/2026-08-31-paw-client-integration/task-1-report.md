# Task 1 Report: Freeze the Paw API Contract

## Files changed

- `backend/internal/server/routes/paw_contract_test.go`: JSON marshal/unmarshal contract tests for configuration, chat requests, attachment references, and errors.
- `backend/internal/server/routes/paw_types.go`: typed Paw server-facing structs, explicit JSON tags, and the eight stable error-code constants.

## TDD

RED command:

```text
cd backend
go test ./internal/server/routes -run Paw -count=1
```

Output: `FAIL ... [build failed]`; compilation failed with expected `undefined` errors for the missing Paw contract types, beginning with `PawConfigResponse`, `PawConfigData`, and `PawUser`.

GREEN command:

```text
cd backend
gofmt -w internal/server/routes/paw_types.go internal/server/routes/paw_contract_test.go
go test ./internal/server/routes -run Paw -count=1
```

Output: `ok github.com/Wei-Shaw/sub2api/internal/server/routes 2.133s`.

## Tests

- Focused Paw contract tests: pass.
- Full `go test ./internal/server/routes -count=1`: not clean because the environment has no Redis at `127.0.0.1:1` and unrelated existing route tests fail (`TestChannelMonitorModeV2Guard`, composite platform tests, Grok media/voice route tests, and prompt-audit coverage).
- `gofmt -d` on both changed Go files: clean.

## Self-review and concerns

The contract uses integer IDs for users/groups and string IDs for models/attachments, matching the specification and existing entity conventions. JSON names are explicit, including `owned_by`, `image_generation`, `file_input`, `group_id`, and the nested `error` envelope. No handler or route behavior was added in this task.

Concern: full package verification is blocked by unrelated route failures and unavailable Redis; the focused Paw test command is independently green.
