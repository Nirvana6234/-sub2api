# Chat

Independent Chat multi-platform application workspace.

- Uses sub2api JWT session flow.
- Keeps local conversation state on the client.
- Lives under `tools/` to stay separate from the main app and reference sources.

## Run

```bash
npm install
npm run dev
```

Open `http://127.0.0.1:3100`. The web and PWA builds use the current site for
their sub2api API. Windows and macOS desktop builds receive the official
service address at build time; the address is not editable inside Chat.

## Desktop (agent) build

`npm run dev` only gives you the plain web/PWA view — the agent panel needs
the real Tauri window plus a codex binary, neither of which a browser tab has.

```bash
npm run codex:vendor   # once per machine/CI run: fetches src-tauri/vendor/codex/
npm run app:dev        # opens the native window with agent support wired up
```

`app:dev` auto-detects the vendored codex binary and points `COFLY_CODEX_BINARY`
at it; you don't need to set that env var by hand. If `codex:vendor` hasn't
been run yet, Chat itself still starts — the agent panel just won't have an
engine to talk to (it prints a warning telling you to run `codex:vendor`).

```bash
npm run app:build   # packaged install: fetches codex + bundles it as a resource
```

`app:build` also requires `PAW_SERVICE_URL`, same as the web/PWA build:

```bash
PAW_SERVICE_URL=https://sub2api.example.com npm run app:build
```

## Follow-up Development Plan

### Skill command hierarchy

- `/` opens the first-level skill menu.
- `/compact` starts context compaction for the active desktop agent thread.
- `/prompt` opens the second-level prompt menu.
- Prompt selection only fills the composer and never sends automatically.
- ArrowRight or Tab enters a submenu, ArrowLeft returns, and Escape closes or returns.

### Local conversation storage

- During a running turn, keep the complete in-memory record so the active UI remains inspectable.
- After a turn is complete, the durable projection removes reasoning, command output, search results, temporary diff/plan data, approval details and notification raw payloads while retaining user messages, final assistant content, attachments and images.
- If the normal localStorage write exceeds quota, retry with progressively smaller durable projections.
- The final fallback keeps the active conversation and pinned-message conversations, then removes the oldest removable conversations before notifying the user.

### Agent protocol and notification surface

- Add `thread/compact/start` from the desktop command menu through the Rust adapter and Tauri bridge.
- Treat `contextCompaction` as a first-class lightweight status in the active conversation.
- Keep user-facing warning notifications in the warning surface and preserve structured agent panels for diff, file changes, plans, terminal interaction and moderation metadata.
