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

```bash
npm run export
PAW_SERVICE_URL=https://sub2api.example.com npm run app:build
```
