# Playground API

The Playground is an authenticated panel feature for testing a user's existing
API keys without exposing an API-key secret to the browser. It is disabled by
default. An administrator must enable **Playground** in the feature settings
before the page and its API adapters are available.

## Authentication and key selection

The browser authenticates requests with the normal panel JWT:

```http
Authorization: Bearer <panel-access-token>
X-Sub2API-Playground-Key-ID: <owned-api-key-id>
```

`X-Sub2API-Playground-Key-ID` must be a positive integer for an API key owned
by the authenticated panel user. A missing, malformed, or foreign key ID is
rejected before gateway dispatch. Foreign IDs return `404` so the endpoint
cannot be used to enumerate other users' keys.

Do not send an API-key secret to these endpoints. `x-api-key`,
`x-goog-api-key`, API-key query parameters, and top-level credential selector
fields in a chat body are rejected. The selected-key header is consumed at the
adapter boundary and is not forwarded upstream.

## Endpoints

```text
GET  /api/v1/playground/models
POST /api/v1/playground/chat/completions
POST /api/v1/playground/images/generations
```

Both endpoints require panel JWT authentication, user backend-mode access, the
panel rate limiter, the server-side Playground feature gate, and the selected
key header.

### Conversation history

The current Playground UI stores projects, conversations, drafts, and request
parameters only in the browser's LocalStorage, scoped by the signed-in user and
selected API key. It never uploads conversation history to PostgreSQL. Saves
are debounced and the complete browser snapshot is capped at 1 MiB; old
conversations are removed first when the limit is reached.

Image URLs can be kept in browser history, but Base64 image payloads are never
persisted. Clearing browser site data or using a different browser/device does
not restore this history.

The legacy `GET` and `PUT /api/v1/playground/history` endpoints remain for
older clients, but the current UI does not call them.

### List models

`GET /api/v1/playground/models` returns the same OpenAI-style model list that
the selected key receives from `GET /v1/models`. The selected key's assigned
group remains the authorization principal.

### Chat completions

`POST /api/v1/playground/chat/completions` accepts the ordinary OpenAI Chat
Completions JSON body. There is no Playground-specific request envelope, so
compatible optional fields remain intact. For example:

```json
{
  "model": "gpt-5.4",
  "messages": [{"role": "user", "content": "Explain a mutex."}],
  "stream": true,
  "temperature": 0.7
}
```

With `stream: true`, the response is the normal `text/event-stream` sequence
including the terminal `data: [DONE]`. When the browser cancels the fetch, the
request context is cancelled through the normal gateway relay path. The client
must not retry a partially consumed stream.

Without streaming, the endpoint returns the usual OpenAI-compatible JSON
completion response.

The bundled UI requests and renders one completion choice. It does not expose
an `n` control; an unexpected multi-choice response is reported instead of
being silently truncated.

## Gateway semantics

Playground requests use the same resolved API-key authorization and gateway
dispatch as external requests. Key status, ownership, IP ACLs, group and
exclusive-group eligibility, subscriptions, balance and quota checks, rate
limits, accounting, usage logs, audit context, and upstream routing all remain
in effect. Playground does not introduce a temporary key, a billing exemption,
or a separate public gateway surface.

For gateway accounting and error reporting, the adapter normalizes its request
context to `/v1/models` or `/v1/chat/completions`; the actual browser-facing
route remains under `/api/v1/playground`.
