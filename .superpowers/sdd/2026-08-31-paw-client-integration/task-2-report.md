# Task 2 Report

## Files changed

- `backend/internal/service/paw_config_service.go`
- `backend/internal/service/paw_config_service_test.go`
- `backend/internal/server/routes/paw.go`
- `backend/internal/server/routes/paw_config_test.go`
- `backend/internal/server/router.go`
- `backend/internal/handler/handler.go`
- `backend/cmd/server/wire_gen.go`

The existing user-attribute persistence mechanism stores the per-user Paw defaults. No new group, permission, or settings database was introduced. The reasoning values are sourced through the reusable `PawReasoningValues` helper.

## TDD evidence

RED command, run before the service implementation:

```text
cd backend
go test ./internal/service -run PawConfig -count=1
```

Result: FAIL at build time because `PawConfigService`, `PawDefaults`, and `PawDefaultsStore` did not exist.

GREEN command after implementation and route tests:

```text
cd backend
go test ./internal/service ./internal/server/routes -run Paw -count=1
```

Result:

```text
ok github.com/Wei-Shaw/sub2api/internal/service 2.077s
ok github.com/Wei-Shaw/sub2api/internal/server/routes 2.062s
```

Additional compile check:

```text
go test ./cmd/server -run '^$'
```

Result: `ok github.com/Wei-Shaw/sub2api/cmd/server (cached) [no tests to run]`.

`go test ./internal/service ./internal/server/routes -count=1` was also attempted; the existing broader suite exceeded the command timeout while emitting unrelated existing test logs. The required Paw-focused tests passed independently afterward.

## Self-review and concerns

- Authorized groups come from `APIKeyService.GetAvailableGroups` through an adapter.
- Models are derived only from the active channel pricing attached to each authorized group.
- Invalid defaults are validated before the persistence call, so the previous default is unchanged.
- Routes use JWT authentication, `BackendModeUserGuard`, and `PanelRateLimiter`.
- Model capability flags initially used conservative defaults; the fix round now maps the capability signals available from the current group and channel data.
- The generated server wiring was updated manually to construct and attach the service.

## Fix round after review

- Reasoning values are advertised only for recognized GPT-5 OpenAI models; unknown OpenAI model IDs no longer inherit the full reasoning list.
- Persisted defaults are validated against the current authorized group/model/reasoning set. Stale defaults make config loading return `CONFIG_UNAVAILABLE`.
- Saving a replacement default builds the current authorized config without blocking on a stale persisted default, so the user can recover by reselecting.
- User-attribute definition lookup errors are propagated, while a missing definition still behaves as an unset default.
- A nil defaults store returns `CONFIG_UNAVAILABLE` instead of panicking.
- Capability mapping now considers group image-generation permission, channel image pricing, and native image-model identity. Vision and file-input flags are derived only from explicit image-input pricing.

Verification:

```text
go test ./internal/service -run PawConfig -count=1
go test ./internal/service ./internal/server/routes -run Paw -count=1
go test ./cmd/server -run '^$'
```

All three commands passed on 2026-08-31.

## Additional fix round

- Paw now reads model capabilities from `PricingService` and the bundled LiteLLM model catalog instead of inferring support from model-name prefixes or billing fields.
- Reasoning values are filtered by the model's explicit reasoning capability flags, including `minimal`, `xhigh`, and `max`.
- Vision, PDF/file input, and image generation are mapped from their corresponding catalog capabilities. Image generation also requires the group's `AllowImageGeneration` permission and an image-generation mode or image output modality.
- Saving new defaults builds the current available model list without reading the old persisted defaults first, so malformed or stale saved JSON can be replaced by a valid selection.
- Added regression coverage for parsing all Paw capability fields and for replacing malformed persisted defaults.

Verification:

```text
go test ./internal/service -run TestParsePricingData_ParsesPawModelCapabilities -count=1
```

Result: passed on 2026-08-31.
