# Paw Client Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an independent Paw PWA and Windows desktop client from GongfeiChat that uses only sub2api account authentication and server-side API-key routing.

**Architecture:** Add a `/api/v1/paw` adapter layer to the existing Go server and reuse its JWT, group/model authorization, gateway, quota, billing and audit services. Adapt GongfeiChat into a Paw-specific frontend that reads server-provided capabilities, stores conversations locally, and sends only user selections plus messages and attachment references. Serve the exported Paw frontend at `/paw` while preserving the Vue management frontend at the root.

**Tech Stack:** Go, Gin, existing sub2api services and middleware, Vue/Vite backend frontend conventions for server integration, Next.js 14, React 18, TypeScript, Zustand, IndexedDB-compatible local persistence, Tauri v1, SSE.

**Spec:** `docs/superpowers/specs/paw-client-integration.md`

## Global Constraints

- Paw must never expose or accept provider API keys, Base URLs or internal Playground Key IDs.
- Group, model and reasoning authorization must be checked on the server for every request.
- Invalid configuration must prompt the user to reselect; no silent fallback is allowed.
- Chat history remains local-only in v1.
- PWA is served at `/paw`; the existing management frontend remains at `/`.
- v1 supports text chat, image generation, image input, PDF, Word, TXT and Markdown uploads.
- Windows desktop is the first Tauri target.
- Do not revert unrelated existing worktree changes in either repository.

---

### Task 1: Freeze the Paw API contract in server-facing types and tests

**Files:**
- Create: `backend/internal/server/routes/paw_types.go`
- Create: `backend/internal/server/routes/paw_contract_test.go`
- Reference: `docs/superpowers/specs/paw-client-integration.md`
- Modify: `backend/internal/server/routes/common.go` only if a shared response/error helper is required

**Interfaces:**
- Produces `PawConfigResponse`, `PawGroup`, `PawModel`, `PawReasoningCapability`, `PawDefaults`, `PawChatRequest`, `PawAttachmentReference`, and `PawError`.
- Produces stable error codes: `CONFIG_UNAVAILABLE`, `GROUP_FORBIDDEN`, `MODEL_UNAVAILABLE`, `REASONING_UNSUPPORTED`, `ATTACHMENT_INVALID`, `QUOTA_EXCEEDED`, `UPSTREAM_UNAVAILABLE`, and `AUTH_REQUIRED`.

- [ ] **Step 1: Write contract tests for the JSON shapes**

Add tests that marshal and unmarshal a representative config, chat request, attachment reference and error response. Assert that IDs, capability flags, default reasoning and error codes retain their exact JSON names.

- [ ] **Step 2: Run the focused Go tests**

Run:

```text
cd backend
go test ./internal/server/routes -run Paw -count=1
```

Expected: the new tests fail because the Paw types do not exist.

- [ ] **Step 3: Add the minimal typed contract**

Define the structs with explicit `json` tags. Keep server-side identifiers as integers where existing sub2api entities use integers, and expose model IDs as strings because the gateway model identifier is a string.

- [ ] **Step 4: Run the focused Go tests again**

Run the same command. Expected: PASS.

- [ ] **Step 5: Commit the contract**

```text
git add backend/internal/server/routes/paw_types.go backend/internal/server/routes/paw_contract_test.go docs/superpowers/specs/paw-client-integration.md
git commit -m "docs: define Paw API contract"
```

### Task 2: Add the Paw configuration service and route

**Files:**
- Create: `backend/internal/service/paw_config_service.go`
- Create: `backend/internal/service/paw_config_service_test.go`
- Create: `backend/internal/server/routes/paw.go`
- Create: `backend/internal/server/routes/paw_config_test.go`
- Modify: `backend/internal/server/router.go`
- Modify: existing user-setting service files only where the established persistence API requires a Paw default field

**Interfaces:**
- Consumes the existing user identity from JWT middleware.
- Consumes existing group, model, channel and user-permission services.
- Produces `GET /api/v1/paw/config` and `PUT /api/v1/paw/config/defaults`.
- Provides a service method equivalent to `GetConfig(ctx, userID)` and `SaveDefaults(ctx, userID, defaults)`.

- [ ] **Step 1: Write failing service tests**

Cover:

1. only groups authorized for the user are returned;
2. models are scoped to each group;
3. unsupported reasoning values are not advertised;
4. an invalid default is rejected without changing the previous default;
5. a valid default is persisted through the existing user settings mechanism.

- [ ] **Step 2: Run the service tests**

Run:

```text
cd backend
go test ./internal/service -run PawConfig -count=1
```

Expected: FAIL because the service and test fixtures do not exist.

- [ ] **Step 3: Implement the service using existing repositories**

Read current group/channel/model services and map their existing capability fields into the Paw response. Do not create a duplicate Paw group table. Keep reasoning capability mapping in one helper so chat validation and config rendering share the same rule.

- [ ] **Step 4: Add route tests for authentication and persistence**

Assert that unauthenticated requests receive the existing authentication response, authorized requests receive the config envelope, and invalid defaults return `CONFIG_UNAVAILABLE`.

- [ ] **Step 5: Register the routes**

Add `RegisterPawRoutes` in `backend/internal/server/router.go` under the existing `/api/v1` group and protect it with the existing JWT middleware and panel rate limiter.

- [ ] **Step 6: Run the focused and package tests**

Run:

```text
cd backend
go test ./internal/service ./internal/server/routes -run Paw -count=1
```

Expected: PASS.

### Task 3: Add server-side Paw chat execution

**Files:**
- Create: `backend/internal/service/paw_chat_service.go`
- Create: `backend/internal/service/paw_chat_service_test.go`
- Modify: `backend/internal/server/routes/paw.go`
- Modify: `backend/internal/server/router.go` if dependency wiring is needed
- Reuse: existing Playground/gateway service implementation rather than copying provider execution logic

**Interfaces:**
- Consumes `PawChatRequest`, authenticated user ID, existing group/model capability resolver and gateway execution service.
- Produces `POST /api/v1/paw/chat/completions` with JSON response for non-streaming requests and OpenAI-compatible SSE for streaming requests.
- Produces structured configuration and quota errors without leaking internal key identifiers.

- [ ] **Step 1: Write failing validation tests**

Cover:

1. a valid group/model/reasoning request reaches the gateway;
2. a forbidden group is rejected;
3. a model outside the selected group is rejected;
4. unsupported reasoning is rejected;
5. a request containing an API key or Playground Key ID is rejected or ignored according to the security policy;
6. the authenticated user ID is always used for route resolution.

- [ ] **Step 2: Run the validation tests**

Run:

```text
cd backend
go test ./internal/service -run PawChat -count=1
```

Expected: FAIL before the service exists.

- [ ] **Step 3: Implement validation and route resolution**

Resolve the internal route from the existing group/channel services. Pass the resolved context into the existing gateway/playground executor. Do not add a Paw-specific API key parser that accepts browser-provided key IDs.

- [ ] **Step 4: Implement streaming passthrough**

Set the SSE content type, flush upstream chunks as they arrive, preserve `[DONE]`, and translate upstream errors into the stable Paw error envelope before headers are committed when possible.

- [ ] **Step 5: Add route-level tests**

Assert status codes, content types, no-key request behavior, stable error codes and the presence of `[DONE]` in a successful stream.

- [ ] **Step 6: Run focused tests**

Run:

```text
cd backend
go test ./internal/service ./internal/server/routes -run PawChat -count=1
```

Expected: PASS.

### Task 4: Add Paw image generation and temporary attachments

**Files:**
- Create: `backend/internal/service/paw_attachment_service.go`
- Create: `backend/internal/service/paw_image_service.go`
- Create: `backend/internal/service/paw_attachment_service_test.go`
- Create: `backend/internal/service/paw_image_service_test.go`
- Modify: `backend/internal/server/routes/paw.go`
- Modify: `backend/internal/server/router.go` for service wiring

**Interfaces:**
- Produces `POST /api/v1/paw/files`, `POST /api/v1/paw/images/generations`, and `POST /api/v1/paw/images/edits`.
- Attachment references contain an opaque ID, filename, MIME type, size and expiration.
- Image results preserve the existing `data[].url`, `data[].b64_json` and `data[].revised_prompt` shape.

- [ ] **Step 1: Write failing upload and image tests**

Cover accepted image/PDF/Word/TXT/Markdown MIME types, rejected types, request-size limits, user ownership checks, expired attachment rejection, image generation validation and image edit multipart parsing.

- [ ] **Step 2: Run focused tests**

Run:

```text
cd backend
go test ./internal/service ./internal/server/routes -run 'Paw(Attachment|Image)' -count=1
```

Expected: FAIL before the services exist.

- [ ] **Step 3: Implement temporary attachment lifecycle**

Store only the temporary metadata and content needed by the existing gateway integration. Scope every attachment to the authenticated user, set an expiration, and expose no provider storage URL to the browser unless the existing image contract requires one.

- [ ] **Step 4: Implement image service delegation**

Map Paw group/model selection into the existing image-generation and image-edit execution path. Preserve quota, billing and audit behavior.

- [ ] **Step 5: Run focused tests**

Run the same command. Expected: PASS.

### Task 5: Adapt GongfeiChat authentication and client transport

**Files:**
- Create: `C:\Work\Git\GongfeiChat\app\client\paw\api.ts`
- Create: `C:\Work\Git\GongfeiChat\app\client\paw\types.ts`
- Create: `C:\Work\Git\GongfeiChat\app\client\paw\auth.ts`
- Create: `C:\Work\Git\GongfeiChat\app\client\paw\config.ts`
- Create: `C:\Work\Git\GongfeiChat\app\client\paw\sse.ts`
- Create: `C:\Work\Git\GongfeiChat\test\paw-api.test.ts`
- Modify: `C:\Work\Git\GongfeiChat\app\client\api.ts`
- Modify: `C:\Work\Git\GongfeiChat\app\config\client.ts` if build-mode configuration needs a Paw server URL
- Modify: `C:\Work\Git\GongfeiChat\next.config.mjs`

**Interfaces:**
- `pawFetch(input, init): Promise<Response>` adds the JWT and performs one guarded token refresh.
- `fetchPawConfig(): Promise<PawConfig>`.
- `savePawDefaults(defaults): Promise<void>`.
- `sendPawChat(request, options): Promise<PawCompletionResult>`.
- `generatePawImage(request): Promise<PawImageResult[]>`.
- `uploadPawFile(file): Promise<PawAttachment>`.

- [ ] **Step 1: Write failing client tests**

Mock `fetch` and cover login token storage, one refresh retry after `401`, redirect/expired-session behavior, config parsing, stable error parsing and SSE content/reasoning deltas.

- [ ] **Step 2: Run the client tests**

Run:

```text
yarn test:ci --runInBand test/paw-api.test.ts
```

Expected: FAIL before the Paw transport exists.

- [ ] **Step 3: Implement the Paw transport**

Use the existing auth API behavior from GongfeiChat's local client conventions, but remove provider-specific headers and all API-key reads. Use a single Paw base path for web and an injected official service URL for Tauri export builds.

- [ ] **Step 4: Implement the SSE parser**

Reuse the existing streaming conventions where possible. Parse `data:` frames, accept `[DONE]`, accumulate content and reasoning separately, and surface machine-readable Paw errors.

- [ ] **Step 5: Run focused tests**

Run the same command. Expected: PASS.

### Task 6: Replace provider configuration with Paw chat configuration

**Files:**
- Create: `C:\Work\Git\GongfeiChat\app\store\paw.ts`
- Create: `C:\Work\Git\GongfeiChat\app\components\paw\PawModelControls.tsx`
- Create: `C:\Work\Git\GongfeiChat\app\components\paw\PawLogin.tsx`
- Create: `C:\Work\Git\GongfeiChat\app\components\paw\PawSettings.tsx`
- Modify: `C:\Work\Git\GongfeiChat\app\store\access.ts`
- Modify: `C:\Work\Git\GongfeiChat\app\store\chat.ts`
- Modify: `C:\Work\Git\GongfeiChat\app\store\config.ts`
- Modify: `C:\Work\Git\GongfeiChat\app\client\controller.ts`
- Modify: relevant route/page files under `C:\Work\Git\GongfeiChat\app\`

**Interfaces:**
- `usePawStore` owns `config`, `selectedGroupId`, `selectedModelId`, `selectedReasoning`, loading and invalid-selection state.
- Chat controller submits a `PawChatRequest` and receives streaming deltas through `sendPawChat`.
- UI controls expose group, model and reasoning selection without provider/API-key settings.

- [ ] **Step 1: Write failing store tests**

Cover group filtering, model reset when the group changes, reasoning capability filtering, invalid-selection state after config refresh and preserving the current draft when the server returns `CONFIG_UNAVAILABLE`.

- [ ] **Step 2: Run store tests**

Run:

```text
yarn test:ci --runInBand test/paw-store.test.ts
```

Expected: FAIL before the Paw store exists.

- [ ] **Step 3: Implement the Paw store**

Keep account defaults separate from per-conversation overrides. Do not place any provider API key or Base URL field in the Paw store.

- [ ] **Step 4: Connect the chat controller**

Build requests from the active conversation, selected group/model/reasoning and local attachments. Preserve existing message rendering and streaming cancellation behavior.

- [ ] **Step 5: Implement login and settings**

Use an embedded login screen. Settings contain account, defaults, appearance, local data management and about sections only.

- [ ] **Step 6: Implement invalid-selection UX**

When the API returns `CONFIG_UNAVAILABLE`, keep messages and draft intact, refresh the config, disable send until a valid selection is made, and show a clear reselect prompt.

- [ ] **Step 7: Run focused client tests**

Run:

```text
yarn test:ci --runInBand test/paw-store.test.ts test/paw-api.test.ts
```

Expected: PASS.

### Task 7: Add local-only history, attachments and export behavior

**Files:**
- Create: `C:\Work\Git\GongfeiChat\app\store\pawPersistence.ts`
- Create: `C:\Work\Git\GongfeiChat\app\utils\pawExport.ts`
- Create: `C:\Work\Git\GongfeiChat\test\paw-persistence.test.ts`
- Create: `C:\Work\Git\GongfeiChat\test\paw-export.test.ts`
- Modify: existing local conversation persistence files under `C:\Work\Git\GongfeiChat\app\store\` and `C:\Work\Git\GongfeiChat\app\utils\`

**Interfaces:**
- `loadPawState(): Promise<PawPersistedState>`.
- `savePawState(state): Promise<void>`.
- `clearPawLocalData(): Promise<void>`.
- `exportConversation(conversation, format): Blob`.

- [ ] **Step 1: Write failing persistence and export tests**

Cover reload persistence, logout retaining local history, explicit clear removing history, attachment payload size limits, and exact Markdown/JSON/plain-text export output for a conversation containing reasoning and images.

- [ ] **Step 2: Run focused tests**

Run:

```text
yarn test:ci --runInBand test/paw-persistence.test.ts test/paw-export.test.ts
```

Expected: FAIL before the Paw persistence modules exist.

- [ ] **Step 3: Implement IndexedDB-compatible persistence**

Use the existing `idb-keyval` dependency or the repository's established persistence helper. Persist conversation metadata and messages locally; keep large transient attachment payloads out of the durable conversation record unless the existing cache path already supports them.

- [ ] **Step 4: Implement export formats**

Markdown must include role labels and preserve code blocks. JSON must preserve the full local conversation object. Plain text must omit transport metadata and include readable role headings.

- [ ] **Step 5: Run focused tests**

Run the same command. Expected: PASS.

### Task 8: Mount the Paw static frontend at `/paw`

**Files:**
- Create: `backend/internal/web/paw_embed_on.go`
- Create: `backend/internal/web/paw_embed_off.go`
- Create: `backend/internal/web/paw_static.go`
- Create: `backend/internal/web/paw_static_test.go`
- Modify: `backend/internal/server/router.go`
- Modify: `backend/internal/web/embed_on.go`
- Modify: `backend/internal/web/embed_off.go`
- Modify: build scripts or Make targets that assemble embedded frontend assets

**Interfaces:**
- `HasEmbeddedPawFrontend() bool`.
- `ServeEmbeddedPawFrontend() gin.HandlerFunc`.
- Paw requests under `/paw` and `/paw/*` are handled before the root frontend fallback.

- [ ] **Step 1: Write failing static-serving tests**

Cover `/paw`, `/paw/`, `/paw/chat`, hashed asset paths, missing asset behavior, API bypass and root frontend behavior remaining unchanged.

- [ ] **Step 2: Run focused web tests**

Run:

```text
cd backend
go test ./internal/web -run Paw -count=1
```

Expected: FAIL before the Paw static server exists.

- [ ] **Step 3: Implement embedded Paw filesystem handling**

Embed a distinct Paw distribution directory and apply SPA fallback only within the `/paw` prefix. Strip the prefix before looking up files. Keep `/api/` and gateway paths bypassed.

- [ ] **Step 4: Register middleware in the correct order**

Ensure Paw static handling occurs without intercepting API routes and before the existing root frontend middleware handles unmatched non-API paths.

- [ ] **Step 5: Run focused and server tests**

Run:

```text
cd backend
go test ./internal/web ./internal/server -run 'Paw|Router' -count=1
```

Expected: PASS.

### Task 9: Configure PWA and Windows Tauri builds

**Files:**
- Modify: `C:\Work\Git\GongfeiChat\next.config.mjs`
- Modify: `C:\Work\Git\GongfeiChat\src-tauri\tauri.conf.json`
- Modify: `C:\Work\Git\GongfeiChat\app\config\client.ts`
- Modify: `C:\Work\Git\AI-Fly\-sub2api\Makefile`
- Modify: relevant Docker/build files that package `backend/internal/web/dist` and the Paw distribution
- Create: `C:\Work\Git\GongfeiChat\.env.paw.example`

**Interfaces:**
- Web build emits assets that can be mounted below `/paw`.
- Tauri export receives one official service URL at build time.
- Build scripts clearly distinguish root Vue frontend assets from Paw assets.

- [ ] **Step 1: Add build configuration tests or validation scripts**

Verify the exported HTML uses the `/paw` asset base path for web packaging and that a missing official Tauri service URL fails the desktop build configuration instead of silently pointing at a provider.

- [ ] **Step 2: Configure web asset paths**

Set the Paw export base path and asset prefix without changing the root management frontend's URLs. Confirm browser refresh on `/paw/chat` resolves to the Paw entry document.

- [ ] **Step 3: Configure Tauri service URL injection**

Add a build-time environment value for the official sub2api URL. Do not add a runtime settings control for it.

- [ ] **Step 4: Add packaging commands**

Provide commands for building Paw, copying its output into the Go embed directory, and building the Windows Tauri installer. Keep the commands deterministic and document their order.

- [ ] **Step 5: Run web and desktop build validation**

Run:

```text
yarn export
yarn app:build
cd C:\Work\Git\AI-Fly\-sub2api\backend
go test ./internal/web ./internal/server
```

Expected: the web export completes, the Windows bundle is produced in the existing Tauri target directory, and server static tests pass.

### Task 10: End-to-end verification and release checklist

**Files:**
- Create: `docs/PAW.md`
- Create: `backend/internal/server/paw_e2e_test.go`
- Create: `C:\Work\Git\GongfeiChat\test\paw-smoke.test.ts`
- Modify: `README_CN.md` with a short Paw build and deployment reference

- [ ] **Step 1: Add server integration coverage**

Exercise login, config loading, valid chat submission, invalid configuration response, image generation authorization and attachment ownership with test doubles for upstream execution.

- [ ] **Step 2: Add frontend smoke coverage**

Verify login screen, config loading, group/model/reasoning controls, draft preservation on `CONFIG_UNAVAILABLE`, local history reload and export action.

- [ ] **Step 3: Run all focused verification**

Run:

```text
cd backend
go test ./internal/server ./internal/service ./internal/web
yarn test:ci --runInBand
yarn lint
```

- [ ] **Step 4: Manually verify the two published surfaces**

Verify:

1. root management frontend still opens at `/`;
2. Paw opens at `/paw`;
3. nested Paw route refresh works;
4. browser network logs contain no provider API key;
5. Windows desktop connects to the configured official service;
6. local chat history is not sent during login or config loading.

- [ ] **Step 5: Write the release checklist**

Document build order, required environment values, supported file types, known v1 exclusions, rollback behavior and the fact that chat history is local-only.
