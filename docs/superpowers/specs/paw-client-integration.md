# Paw Client Integration Specification

**Date:** 2026-08-31

## Goal

将 `C:\Work\Git\GongfeiChat\` 改造成独立的 Paw 客户端，并通过 sub2api 专用接口使用现有账号、分组、模型、路由和计费能力。Paw 以 PWA 和 Windows Tauri 桌面端交付，不向普通用户暴露任何供应商 API Key 或服务器配置。

## Product Scope

### Included in v1

- sub2api 账号登录、Token 刷新和退出登录；
- 文本聊天和 SSE 流式响应；
- 在聊天界面选择现有分组、模型和推理强度；
- 图片生成；
- 图片作为视觉输入；
- PDF、Word、TXT、Markdown 文件上传；
- 本地会话历史、草稿和附件缓存；
- Markdown、JSON、纯文本导出；
- `/paw` PWA；
- Windows Tauri 桌面端。

### Excluded from v1

- API Key 输入、展示或管理；
- 用户自定义供应商和 Base URL；
- MCP；
- 插件市场；
- 实时语音；
- Artifacts；
- 云端聊天记录同步；
- 自动切换失效分组或模型。

## Architecture

```text
Paw PWA / Tauri
  ├─ Built-in login page
  ├─ Local chat persistence
  ├─ Group / model / reasoning controls
  └─ /api/v1/paw/* requests with JWT
             |
             v
sub2api Paw API layer
  ├─ Reuse existing JWT authentication
  ├─ Read existing group and model permissions
  ├─ Validate the selected group/model/reasoning
  ├─ Resolve internal route and API key on the server
  ├─ Reuse existing gateway/playground execution services
  └─ Preserve quota, billing, audit and streaming behavior
```

The Paw API layer is an adapter. It owns the Paw-facing request and response contract, while the existing gateway remains responsible for provider-specific execution. The existing Playground API remains available for the Vue management frontend and is not exposed as a client-side API-key workflow for Paw.

## API Contract

All endpoints below are under `/api/v1/paw`. Protected endpoints use:

```http
Authorization: Bearer <sub2api-jwt>
```

### Authentication

Paw reuses the existing authentication endpoints:

```http
POST /api/v1/auth/login
POST /api/v1/auth/refresh
```

The client must use the existing response envelope and token semantics. Paw must not create a second account or token format.

### Client Configuration

```http
GET /api/v1/paw/config
```

Response shape:

```json
{
  "data": {
    "user": {
      "id": 42,
      "name": "user",
      "email": "user@example.com"
    },
    "groups": [
      {
        "id": 7,
        "name": "Balanced",
        "description": "",
        "models": [
          {
            "id": "model-id",
            "name": "Model Name",
            "owned_by": "provider",
            "reasoning": {
              "supported": true,
              "values": ["low", "medium", "high"],
              "default": "medium"
            },
            "vision": true,
            "image_generation": false,
            "file_input": true
          }
        ]
      }
    ],
    "defaults": {
      "group_id": 7,
      "model_id": "model-id",
      "reasoning": "medium"
    }
  }
}
```

The actual implementation must map this response from existing sub2api group, channel and model data. Paw must not duplicate the permission model in its own database.

### Default Configuration

```http
PUT /api/v1/paw/config/defaults
```

Request:

```json
{
  "group_id": 7,
  "model_id": "model-id",
  "reasoning": "medium"
}
```

The server validates the selection before saving it to the existing user settings storage. An invalid default must not overwrite the previous valid default.

### Chat Completions

```http
POST /api/v1/paw/chat/completions
Accept: text/event-stream
Content-Type: application/json
```

Request:

```json
{
  "group_id": 7,
  "model_id": "model-id",
  "reasoning": "medium",
  "messages": [
    {
      "role": "user",
      "content": "Hello"
    }
  ],
  "stream": true,
  "attachments": []
}
```

The server must validate the group, model, reasoning value, message size and attachment ownership before invoking the existing gateway path. The server chooses internal API keys and routes; the request must never contain a client-supplied API key or Playground Key ID.

Streaming uses the existing OpenAI-compatible SSE shape so the adapted NextChat streaming parser can consume deltas:

```text
data: {"choices":[{"delta":{"content":"Hello"}}]}

data: [DONE]
```

Reasoning deltas may use the existing gateway field supported by the project, such as `reasoning_content`. The client parser must tolerate content-only, reasoning-only and mixed chunks.

### Image Generation

```http
POST /api/v1/paw/images/generations
Content-Type: application/json
```

Request:

```json
{
  "group_id": 7,
  "model_id": "image-model",
  "prompt": "A simple illustration",
  "size": "1024x1024",
  "quality": "standard",
  "style": "vivid",
  "n": 1
}
```

The response keeps the existing image response shape:

```json
{
  "created": 1720000000,
  "data": [
    {
      "b64_json": "...",
      "revised_prompt": "..."
    }
  ]
}
```

Reference-image editing, when supported by the current gateway, uses:

```http
POST /api/v1/paw/images/edits
Content-Type: multipart/form-data
```

### File Uploads

```http
POST /api/v1/paw/files
Content-Type: multipart/form-data
```

Supported v1 formats:

- Images: `image/png`, `image/jpeg`, `image/webp`, `image/gif`;
- Documents: PDF, Word, TXT and Markdown.

The server validates type and size, creates a temporary attachment record, and returns:

```json
{
  "data": {
    "id": "attachment-id",
    "filename": "document.pdf",
    "mime_type": "application/pdf",
    "size": 123456,
    "expires_at": "2026-08-31T12:00:00Z"
  }
}
```

Chat requests reference `attachment-id`. The server may translate the attachment to provider-specific content. Uploaded files are temporary and are not part of cloud chat history.

## Error Semantics

Paw API errors use a stable machine-readable code and human-readable message:

```json
{
  "error": {
    "code": "CONFIG_UNAVAILABLE",
    "message": "当前配置不可用，请重新选择"
  }
}
```

Required codes:

- `CONFIG_UNAVAILABLE`: group, model or reasoning selection is no longer valid;
- `GROUP_FORBIDDEN`: the user cannot use the group;
- `MODEL_UNAVAILABLE`: the model is disabled or unavailable;
- `REASONING_UNSUPPORTED`: the selected model does not support the value;
- `ATTACHMENT_INVALID`: type, size or content is invalid;
- `QUOTA_EXCEEDED`: the account cannot submit the request;
- `UPSTREAM_UNAVAILABLE`: all eligible upstream routes failed;
- `AUTH_REQUIRED`: the JWT is missing or expired.

When configuration is invalid, the client preserves the draft and messages, refreshes `/paw/config`, marks the send action unavailable, and asks the user to choose again. It must not silently select another group or model and must not overwrite the account default.

## Client State Model

The Paw client separates server state from local conversation state.

Server-synchronized state:

- authenticated user;
- available groups and model capabilities;
- account defaults;
- quota summary when provided by the server.

Local-only state:

- conversation list;
- messages and reasoning content;
- draft text;
- local attachment cache;
- theme and UI preferences;
- export preferences.

Use the existing Zustand and IndexedDB-compatible persistence patterns from GongfeiChat. Removing a server-side history sync path is part of the Paw adaptation; local history must remain available after logout unless the user explicitly clears it.

## UI Rules

The main chat screen exposes:

```text
[Group] [Model] [Reasoning]
```

Changing the group filters the model list. Changing the model refreshes the available reasoning values and attachment capabilities. A session may override account defaults; a deliberate user selection may be saved as the new account default through the settings or an explicit save action.

The Paw settings screen contains account, defaults, appearance, local-data management, and about sections. It does not contain provider, API key, Base URL or Playground Key controls.

## Packaging and Serving

PWA output is built from GongfeiChat and copied or embedded as a second static frontend under the `/paw` path. The existing Vue frontend remains the root management frontend.

The Go server must route `/paw` and `/paw/*` to the Paw static assets with SPA fallback while keeping `/api/*` and gateway routes untouched. Asset paths must work when Paw is served below `/paw`, and browser refreshes on client routes must return the Paw entry document.

The Tauri build uses the same exported Paw assets and injects the official sub2api service URL through a build-time configuration. End users cannot edit that URL in the application.

## Security and Privacy

- Never send provider API keys to Paw;
- Never accept a client-supplied internal Playground Key ID in Paw endpoints;
- Enforce group/model/reasoning authorization server-side on every request;
- Apply existing JWT, request-size, rate-limit, quota, billing and audit middleware;
- Keep file attachments temporary and scoped to the authenticated user;
- Do not upload local chat history unless a future feature explicitly enables it.

## Acceptance Criteria

- A user can log in through Paw without entering an API key;
- Paw shows only groups and models authorized by sub2api;
- A chat request succeeds using only JWT, group, model, reasoning and messages;
- A streaming response renders incremental content and reasoning correctly;
- Invalid configuration produces a reselect prompt and preserves the draft;
- Image generation and supported file uploads work through Paw endpoints;
- Existing Playground and gateway behavior remain available;
- `/paw` serves the PWA and supports browser refresh on nested routes;
- Windows Tauri build connects to the configured official service;
- Local chat history survives refresh and is not uploaded to the server.
