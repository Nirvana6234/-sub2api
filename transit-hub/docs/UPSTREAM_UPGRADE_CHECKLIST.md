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

## UR-2026-08-06: TransitHub-Owned Auto Priority Scoring

**Root cause:** the old priority path reused upstream/Sub2API priority values as
ranking inputs, so paused and healthy accounts could retain the same score and
group-specific account priorities could not be managed independently. It also
did not expose a stable price/balanced/speed choice based on TransitHub-owned
cost and latency observations.

**Protected behavior:**

1. Rank each Sub2API group independently. Existing upstream priority is only a
   manual-conflict checkpoint and must never feed the score.
2. Use the actual upstream API-key group multiplier as cost. Price mode ranks
   lower multipliers first; balanced mode uses 70% price and 30% speed; speed
   mode ranks lower latency first. Conflicting strategies in one ordered group
   fall back deterministically to price mode.
3. Read real-request TTFT from Sub2API admin `/api/v1/admin/usage`, preferring
   the current group's one-hour window. If that group has fewer than three
   samples, fall back to the same account's cross-group requests from the last
   24 hours. In both cases use at most 20 newest samples per account, at least
   three samples, P95, and a 30-second latency normalization ceiling. Paginate
   the usage feed so quieter accounts are not hidden behind a busy account.
4. Prefer Sub2API usage P95, then fall back to TransitHub's already persisted
   probe-event P95. Priority scoring must never launch an additional probe.
5. Put suspended, disabled, unschedulable, and zero-weight managed targets in
   the shared final Sub2API slot (`10000`).
6. If a successfully loaded group no longer contains an account, delete its
   stale group-priority checkpoint without trying to restore a binding that no
   longer exists. Do not repeatedly call the upstream group-priority endpoint.

**Protected locations:**

- `backend/internal/modules/connection_health/priority_strategy.go`
- `backend/internal/modules/upstream/platform_accounts.go`
- `backend/internal/modules/connection_health/admin_groups.go`
- `frontend/src/modules/admin/components/dashboard/AdminGroupHealthDetail.vue`
- `frontend/src/modules/admin/components/dashboard/GroupHealthSetupDrawer.vue`
- `frontend/src/modules/admin/components/dashboard/PolicyConfigDrawer.vue`
- `backend/internal/modules/connection_health/group_priority_strategy_test.go`
- `backend/internal/modules/upstream/platform_accounts_test.go`

**Upgrade acceptance checks:**

1. `go test ./...` and the frontend typecheck/build pass locally.
2. Admin-group JSON contains `usageP95FirstTokenMs` and `usageSampleCount`.
3. Managed usable price-mode targets have priorities `100, 200, ...` in
   nondecreasing multiplier order; all managed blocked targets use `10000`.
4. The cloud strategy drawer renders price, balanced, and speed choices, and
   its help text states that scoring does not launch extra probes.
5. Startup and reconciliation logs contain no priority-write 500 loop.

**Deployment record:** cloud `13.250.65.133` runs locally built Linux/amd64
image `transithub:auto-priority-usage-20260806-r3`. The exported archive is
`.artifacts/transithub-auto-priority-usage-20260806-r3.tar`, SHA256
`5BA386292F433FA1AC4858F8AF4BAC4DBBD0E801178D8A08CFC7500772CD2035`.
Only the `app` container was recreated; Postgres, Redis, and the ticket-upload
volume were preserved. Roll back with
`transithub:priority-admin-api-key-fix-20260805` and the saved compose backup.

## UR-2026-08-14: Sub2API Browser-Session Token Acceptance

**Root cause:** Turnstile-enabled Sub2API sites require a browser-generated
`turnstile_token`, but TransitHub's server-side password login sent only email
and password. The manual-token path then treated usage-statistics and group-rate
initialization as part of token validation, so a valid `/api/v1/auth/me` token
was rejected when any optional endpoint failed. Dashboard error mapping also
collapsed rejected tokens, non-admin roles, interactive login requirements, and
invalid upstream responses into one misleading admin error.

**Protected behavior:**

1. Detect `turnstile_enabled` through `/api/v1/settings/public` and require a
   browser-captured login token or Admin API Key instead of submitting an
   incomplete password request.
2. `/api/v1/auth/me` is the token-validity boundary. Usage stats and group-rate
   failures must leave a validated session intact and produce unavailable
   metrics rather than an authentication failure.
3. Preserve the earlier access-before-refresh rule: a valid Access Token wins
   over a stale Refresh Token. Normalize a pasted `Bearer <token>` value and
   derive `ExpiresAt` from a JWT `exp` claim when available.
4. Keep non-admin roles distinct from rejected tokens and interactive-login,
   request, and response failures so the UI can provide actionable guidance.
5. Do not add browser Cookie or User-Agent replay as a generic workaround.
   Deployments that enforce IP/UA session binding must use an Admin API Key or a
   local browser authorization agent; server-side token parsing cannot repair a
   network-fingerprint mismatch.

**Protected locations:**

- `backend/internal/modules/upstream/platform_service.go`
- `backend/internal/modules/upstream/types.go`
- `backend/internal/modules/dashboard/service.go`
- `backend/internal/modules/dashboard/types.go`
- `frontend/src/locales/zh-CN.ts`
- `frontend/src/locales/en-US.ts`
- `backend/internal/modules/upstream/platform_service_key_auth_test.go`
- `backend/internal/modules/dashboard/service_platform_error_test.go`

**Upgrade acceptance checks:**

1. A Turnstile-enabled public settings response prevents the incomplete
   server-side password login request and returns the interactive-login error.
2. A successful `/api/v1/auth/me` keeps the session valid when dashboard stats
   or group endpoints return 5xx.
3. Pasting `Bearer <jwt>` sends exactly one Bearer prefix and persists the JWT
   expiry in milliseconds.
4. A valid `role=user` token reports the admin-required error, while a rejected
   token reports the authentication-rejected error.
5. `go test ./...` and the frontend build pass locally before deployment.

## Required Upgrade Procedure

1. Read this checklist before applying upstream changes.
2. Compare each protected location and retain all protected regression tests.
3. Run `go test ./...` in `backend` and `npm run build` in `frontend` locally.
   For `UR-2026-08-03`, also run the targeted manual probe acceptance check.
4. Build the deployment artifact locally. The cloud host `13.250.65.133` may
   receive and run that artifact, but must not compile or perform heavy work.
5. Record any upstream equivalent fix in the relevant entry instead of deleting
   the entry without evidence.
