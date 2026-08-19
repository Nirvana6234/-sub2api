# TransitHub Upstream Upgrade Checklist

This file is the source of truth for local root-cause fixes that intentionally
diverge from the upstream project. Before merging, rebasing, or replacing any
TransitHub version, compare every protected behavior below against the incoming
code. Preserve the implementation and its regression test unless the upstream
has an equivalent fix that passes the stated acceptance checks.

## UR-2026-08-01: Dashboard Cost Source

**Upstream defect:** the dashboard card read cached site-level
`upstream_sites.metrics.todayConsume`, while the cost drill-down summed actual
per-key upstream usage. Cache freshness and sync timing allowed the two views to
show different costs.

**Protected behavior:** homepage cost, drill-down cost, and daily snapshots use
per-key usage as the authoritative source. Cached site metrics are allowed only
when every per-key collection attempt is unavailable.

**Protected locations:**

- `backend/internal/modules/upstream/service.go`:
  `KeyUsageTodayForAccount` and the `KeyUsageToday` delegation.
- `backend/internal/modules/dashboard/metrics_service.go`:
  `UpstreamLister.KeyUsageTodayForAccount`, `sumKeyUsageToday`,
  `todayPurchaseForAccount`, and its use from `LiveMetrics` and `snapshotAll`.
- `backend/internal/modules/dashboard/metrics_service_upstream_drilldown_test.go`:
  regression case for `0.67 + 9.35 = 10.02`.

**Upgrade acceptance check:** `todayPurchase` must not primarily derive from
`site.Metrics.TodayConsume.Value * site.RechargeRate`.

## UR-2026-08-02: Sub2API External Token Authentication

**Upstream defect:** `LoginWithToken` refreshed whenever a Refresh Token was
present before it tested the supplied Access Token. A stale Refresh Token could
therefore reject an otherwise valid Access Token. `RefreshSession` also forced
refresh for external Access Tokens whose expiry was unknown. With `auto +
token`, a failed setup was labeled `newapi` instead of `sub2api`.

**Protected behavior:**

1. `LoginWithToken` tests a non-empty Access Token by fetching Sub2API metrics.
2. Refresh is attempted only after that Access Token fails with an
   authentication error and a Refresh Token exists.
3. `RefreshSession` preserves external Access Tokens when `ExpiresAt` is nil.
4. `auto + token` resolves to `sub2api` during both create and update, including
   a failed connection attempt.

**Protected locations:**

- `backend/internal/modules/upstream/platform_service.go`:
  access-first token login and the nil-`ExpiresAt` refresh guard.
- `backend/internal/modules/upstream/service.go`:
  `resolvedPlatformForAuthMode` used by both `Create` and `Update`.
- `backend/internal/modules/upstream/platform_service_key_auth_test.go`:
  `TestLoginWithTokenUsesValidAccessTokenBeforeRefreshToken` and
  `TestResolvedPlatformForTokenAuth`.
- `frontend/src/modules/admin/views/UpstreamView.vue`:
  `handleEditSite` must default a resolved or requested Sub2API site to token
  authentication rather than silently resetting the edit form to password.

**Upgrade acceptance check:** a valid Access Token plus an expired Refresh Token
must connect without calling `/api/v1/auth/refresh`; a later sync with unknown
token expiry must also avoid refresh.

**Credential boundary:** YOUC's API key from the user-facing `/keys` page is a
model-call credential and is not the login-session JWT required by
`/api/v1/auth/me`, `/api/v1/usage/dashboard/stats`, and `/api/v1/keys`. A
`sk-...` API key must not be presented as TransitHub's upstream Access Token.
Password login remains subject to YOUC's Cloudflare Turnstile, so the supported
non-interactive path requires a valid login-session Access Token (and optional
matching Refresh Token).

## UR-2026-08-03: Sub2API Group Health and OpenAI Responses Probes

**Incident:** a Sub2API account could pass its native model test while
TransitHub's group-health probe reported `invalid_response`. In the observed
case, `SJ-GPT-0.05` at `https://keiko.lol` returned the valid SSE event
`response.created` for `gpt-5.6-sol`. That event proves that the upstream
accepted the credentials, route, model, and request, but the old parser only
accepted a completed response.

**Protected behavior:**

1. Resolve Sub2API probe credentials server-side from the single-account
   export. Preserve the upstream account's `platform`, `type`, `extra`, and
   safe header overrides in the short-lived `ProbeCredential`; never persist,
   log, or return its API key.
2. Route OpenAI OAuth/setup-token accounts to the Codex Responses endpoint with
   the required Codex request headers. Route OpenAI API-key accounts to
   `/v1/responses` by default, unless `extra.openai_responses_mode` forces Chat
   Completions or `extra.openai_responses_supported` is explicitly `false`.
   Other providers continue to use OpenAI-compatible Chat Completions.
3. Apply only explicitly enabled, non-sensitive header overrides. Overrides
   must not replace authentication, cookies, host, transfer headers, or
   provider session/conversation headers.
4. Treat Responses SSE `response.created`, `response.completed`, and
   `response.done` as successful lightweight probes. `response.failed` and
   `error` remain failures; arbitrary JSON SSE remains insufficient without a
   Chat Completions choice plus `[DONE]`.
5. Pass the complete credential metadata through scheduled probes, persisted
   target probes, and one-shot manual probes. A manual probe must remain
   non-persistent and must not trigger remote actions.
6. Prefer the model list already supplied by the upstream account, after
   trimming, deduplicating, and sorting it. Only fall back to `/v1/models` when
   the account has no known model list, because some relays do not expose that
   endpoint.

**Protected locations:**

- `backend/internal/modules/upstream/probe_credentials.go`:
  `ProbeCredential`, Sub2API account export parsing, OpenAI fallback base URLs,
  `extra`, and safe header-override filtering.
- `backend/internal/modules/connection_health/probe_runner.go`:
  protocol-aware request construction and Responses SSE classification.
- `backend/internal/modules/connection_health/admin_targets.go` and
  `manual_probe.go`: propagation of credential metadata to every probe path.
- `backend/internal/modules/connection_health/model_discovery.go`: use known
  account models before requesting `/v1/models`.
- `backend/internal/modules/upstream/probe_credentials_test.go`,
  `backend/internal/modules/connection_health/probe_runner_test.go`, and
  `backend/internal/modules/connection_health/model_discovery_test.go`:
  regression coverage for the protected behavior.

**Upgrade acceptance checks:**

1. An OpenAI API-key account returning only
   `data: {"type":"response.created",...}` from `/v1/responses` must produce
   `ResultOK` and `healthy: true`.
2. A `response.failed` or `error` event must not be classified as healthy.
3. An OpenAI OAuth account must use `/backend-api/codex/responses`; an API-key
   account with `openai_responses_supported: false` must use
   `/v1/chat/completions`.
4. A known account model list such as
   `gpt-5.6-sol,gpt-5.4,gpt-5.6-sol` must be returned as a deduplicated,
   sorted list without calling `/v1/models`.
5. Re-run the production manual probe for `SJ-GPT-0.05 / gpt-5.6-sol` and
   confirm `HTTP 200`, `result: ok`, and `healthy: true`. Then confirm the
   account appears in the admin group-health response with `probeAvailable`.

**Deployment record:** the verified cloud image is
`transithub:manual-multiplier-complete-20260804-22c5017` on `13.250.65.133`.
It includes commits `787bb40` and `22c5017`, which fix decimal manual
upstream-key multiplier entry and add migration `000019` for its persistent
storage. The deployment was built as Linux/amd64 locally, transferred as a
Docker image tarball, imported remotely, and applied by recreating only the
`app` container. The cloud health endpoint and migration table were verified
afterward. Do not compile on the cloud host and do not recreate the Postgres or
Redis volumes while deploying this fix.

## Required Upgrade Procedure

1. Read this checklist before applying upstream changes.
2. Compare each protected location and retain all protected regression tests.
3. Run `go test ./...` in `backend` and `npm run build` in `frontend` locally.
   For `UR-2026-08-03`, also run the targeted manual probe acceptance check.
4. Build the deployment artifact locally. The cloud host `13.250.65.133` may
   receive and run that artifact, but must not compile or perform heavy work.
5. Record any upstream equivalent fix in the relevant entry instead of deleting
   the entry without evidence.
