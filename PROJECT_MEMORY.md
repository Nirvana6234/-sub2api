# Project Memory

Last verified: 2026-09-22

This file is the current project handoff. Prefer it, `DEV_GUIDE.md`, the client
README, and executable workflows over old design drafts.

## Repository Map

- `backend/`: Go/Gin/Ent Sub2API gateway, billing, accounts, groups, routing, and API.
- `frontend/`: Vue admin and user web application.
- `tools/codex-relay-client/`: the thin Windows-first "Xiaobai" desktop client.
- `tools/chat/`: a separate desktop chat/workbench application; it is not the Xiaobai client.

## Agent Skills

- The TypeSafe skill is installed at `.agents/skills/typesafe-ai/` and pinned in
  `skills-lock.json`.
- TypeSafe credentials stay in the user-scoped `TYPESAFE_API_KEY` environment
  variable; never commit the key or copy it into project files. The live quickstart
  is `https://docs.typesafe.ai/introduction/quickstart`.

## Xiaobai Client

The shipping entry point is `src/LanAi.RelayClient.App` (Avalonia). The WPF
`src/LanAi.RelayClient` project is a development/legacy head and must not be
used as the release entry point.

The normal request chain is:

```text
Codex or VS Code/Claude Code
  -> Context Filter (when bundled and enabled)
  -> 127.0.0.1 Paw Relay
  -> /api/v1/paw/responses or /api/v1/paw/messages
```

Responses and Anthropic Messages are different protocols, but they share the
same Context Filter lifecycle and compression accounting. Fixed plugin groups
remain separate from the Codex group; server-side group multipliers still apply
to the selected group. The plugin path must not bypass the Context Filter.

## Version 0.7

The effective client version is `0.7`. Keep all of these sources in sync:

- `tools/codex-relay-client/src/LanAi.RelayClient.Core/ClientOptions.cs`
- `tools/codex-relay-client/src/LanAi.RelayClient.App/LanAi.RelayClient.App.csproj`
- `tools/codex-relay-client/src/LanAi.RelayClient.App/app.manifest`
- `frontend/public/client-version.json`
- release tag: `client-v0.7`

Do not infer the release version from an artifact directory name alone. The
published executable must pass `packaging/check-server-address.py --channel
production` and the package must include the bundled
`context-filter/context-filter.exe` subdirectory.

## Packaging Rules

### Local Codex/Claude packaging

When a Codex or Claude task is asked to produce a package locally, produce only
the Windows `win-x64` package. For both formal and test builds, leave the
published files as an uncompressed output directory; do not create a local ZIP.
Publish `LanAi.RelayClient.App`, not the WPF project:

```powershell
dotnet publish src/LanAi.RelayClient.App/LanAi.RelayClient.App.csproj `
  -c Release -r win-x64 --self-contained true `
  -p:PublishSingleFile=true -p:IncludeNativeLibrariesForSelfExtract=true `
  -o <publish-dir>
python packaging/check-server-address.py --channel production <publish-dir>/LanAi.RelayClient.App.exe
```

For a test build, use the appropriate `TestServer` or `LocalServer` publish
property and matching address check. The local output directory is the package
to inspect or hand off; compression is reserved for the GitHub release workflow.

The Windows staging root contains the renamed main executable, the nested
`context-filter/` directory, `codex-installer/`, and the two shortcut scripts.
The authoritative staging layout is the Windows section of
`.github/workflows/client-release.yml`.

### GitHub Actions packaging

`.github/workflows/client-release.yml` is the complete formal release pipeline.
It remains the multi-platform release workflow (Windows plus signed macOS); a
local Windows-only request must not be used to narrow or remove its macOS steps.
It is triggered by `client-v<version>` tags or manually through Actions.

## Backend and Admin Startup

Read `DEV_GUIDE.md` before starting services. The normal local stack is:

1. PostgreSQL on `127.0.0.1:5432` and Redis on `127.0.0.1:6379`.
2. Backend in its own PowerShell:

   ```powershell
   Set-Location C:\Work\Git\AI-Fly\-sub2api\backend
   $env:DATA_DIR = 'C:\Work\Git\AI-Fly\-sub2api\.local\sub2api-data'
   go run ./cmd/server/
   ```

3. Admin frontend in another PowerShell:

   ```powershell
   Set-Location C:\Work\Git\AI-Fly\-sub2api\frontend
   pnpm.cmd dev --host 127.0.0.1 --port 3000
   ```

4. Optional separate Chat workbench:

   ```powershell
   Set-Location C:\Work\Git\AI-Fly\-sub2api\tools\chat
   npm.cmd run app:dev
   ```

   Its `tools/chat/.env.local` must contain
   `PAW_SERVICE_URL=http://127.0.0.1:8080`.

Check:

- `http://127.0.0.1:8080/health` returns `{"status":"ok"}`.
- `http://127.0.0.1:8080/setup/status` has `needs_setup: false`.
- Admin UI is `http://127.0.0.1:3000`.

Do not delete or move `.local/sub2api-data`, `config.yaml`, or `.installed` to
work around startup or migration errors. Do not edit an applied migration;
restore it or add a new migration. After Go backend changes, rebuild and restart
the running backend before testing Paw behavior.

## Current Gaps

- Model-list interception for the Codex picker is not implemented; do not assume
  `/v1/models` is supported by the local Relay.
- Real-machine Codex/VS Code smoke tests are still separate from the unit tests.
- The GitHub workflow is the release authority; local packages are validation
  artifacts, not a second release pipeline.
