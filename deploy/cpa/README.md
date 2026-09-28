# CPA production deployment

CPA runs in `/opt/cli-proxy-api` on the existing `deploy_sub2api-network`.
Sub2API uses `http://cli-proxy-api:8317`; administration uses
`https://icode-xtu.cc.cd/management.html`. Host port 8317 is loopback-only.
Nginx exposes the management UI/API, retains the Certbot HTTP-01 renewal
location, and disables access logging to avoid recording OAuth callback codes.

The management key is `/opt/cli-proxy-api/management.key`; `config.yaml` stores
its bcrypt hash after startup. Client API keys are separate and belong in
Sub2API. Keep all keys and account tokens on the server, outside source control.

## Paired custom releases

The current release (2026-09-27) is `20260927-a303e91-concurrency-37e6b5b`,
pairing backend `a303e91` with management UI `37e6b5b`. It adds the Sub2API
scheduling source (disabled until configured) and mounts
`/opt/cli-proxy-api/sub2api-sync` at `/CLIProxyAPI/sub2api-sync` with
`CPA_SUB2API_SYNC_DIR` pointing at it, so sync settings survive container
recreation.

Sync was enabled on 2026-09-27 with no account mappings, so only the global
policy (weights, waiting, retries, cooldowns) comes from Sub2API; the CPA
account keeps its local limit. Sub2API reads `CPA_SCHEDULING_SYNC_TOKEN` and
`CPA_SCHEDULING_SOURCE_ID=sub2api-prod` from `/opt/sub2api/deploy/.env` via
`docker-compose.local.yml`; CPA stores the same token (write-only) with source
`http://sub2api:8080` in `/opt/cli-proxy-api/sub2api-sync/`. Rotating the token
requires updating both sides. To stop following Sub2API, disable the source in
the CPA management panel; the configured `routing.strategy` then applies again. The Linux backend is
cross-compiled on Windows with CGO via the zig toolchain in
`CLIProxyAPI/.git/production-toolchain` (`zig cc -target x86_64-linux-gnu.2.36`). Build the Linux backend with CGO
enabled against Debian Bookworm (matching the pinned production image), and
build the frontend as one HTML
file. The production image remains pinned; bind-mount the versioned backend
and `management.html` read-only into `/CLIProxyAPI/CLIProxyAPI` and
`/CLIProxyAPI/static/management.html`. Preserve the existing plugin mount.

Release files live under `releases/<release>/`. `manifest.json` records
`release`, `backend_commit`, `frontend_commit`, `sha256` (keys
`cpa-linux-amd64` and `management.html`), and validation results.
`DEPLOYED_RELEASE` contains the release basename; `DEPLOYED_COMMIT` records
`backend=<commit>` and `frontend=<commit>` on separate lines.

Set `remote-management.disable-auto-update-panel: true` and keep
`disable-control-panel: false`. Check `MANAGEMENT_STATIC_PATH` and runtime
writable-directory overrides before choosing the panel mount destination.
Install the HTML before startup; a missing panel may trigger an initial download.

This backend denies unbound keys and excludes accounts outside every pool.
For the initial upgrade, explicitly register the `plus` pool, bind both existing
client keys to `[plus]`, and add `credential_group: plus` to the existing Plus
OAuth account. Preserve all tokens and unrelated fields. Keep the existing
Sub2API account's URL and key. Subsequent releases must preserve operator pool
choices rather than repeating this initial migration. Explicit `[]` denies a key.

## Management workflow

The management UI follows the Sub2API workflow through three separate pages:

- **分组管理** (`#/credential-pools`): create/delete groups and follow account/key
  counts to the corresponding filtered lists. This page has no account editor.
- **账号管理** (`#/auth-files`): filter accounts by group and select multiple
  groups for an account or batch. Individual edits preselect all current groups;
  batch saves explicitly replace memberships. Clearing groups is deliberate,
  and failed targets remain available for retry.
- **API 密钥** (`#/api-keys`): create a key with one group. Click its group in the
  table, search if needed, and select another group to save immediately without
  changing the key. Existing multi-group keys retain all memberships until an
  explicit switch. Rotation preserves all bindings; disabling writes `[]`.

The account editor requires the paired backend's `credential_groups: string[]`
PATCH contract. It validates and replaces memberships through `UpdateManagedAuth`,
preserving tokens and unrelated metadata. `[]` clears membership; the legacy
`credential_group` string remains supported. Deploy this frontend and backend
together; do not repeat the initial pool migration during upgrades.

Create the groups, assign accounts, and then select those groups for each key
before using it in Sub2API. When replacing a key, also update the corresponding
Sub2API upstream credential; rotating a CPA key does not update Sub2API for you.

## Release procedure

### Adaptive account scheduling release

The **Account management** cards show active concurrency, the configured ceiling,
recent generation error rate, semantic first-output latency and account/model
cooldowns. Unknown or expired observations are not displayed as zero. Visible
pages poll every three seconds; leaving the page cancels polling. These metrics
belong to this CPA process, not the upstream provider or another CPA replica.

**Set limit** edits only the account identity and `max_concurrency`, preserving
refreshed tokens and group memberships. A positive value enforces a ceiling;
`0` means no configured ceiling. The limit editor also offers Sub2API's local
account-creation default: 10 for ordinary accounts, 1 for Grok (`xai`). Applying
this preset requires Save and does not overwrite existing values on deployment.
Sub2API has no Plus/Pro default-concurrency table. Full imports retain their
configured concurrency; other import paths can use different defaults.

The **Configuration management / Network** page exposes `adaptive` routing and
all scoring, session-escape, health-expiry, queue and attempt settings. Initial
release settings use Top-K 7, load/queue/error/TTFT weights 1/0.7/0.8/0.5,
health TTL 300 seconds, a 1500 ms acquisition window, 32 waiting requests and
3 conductor executor attempts per inbound request. The acquisition window never
becomes a response or established-stream deadline. Waiting is only before the
first upstream attempt; set its timeout to 0 to reject immediately when full.

Selection keeps the highest eligible priority tier and healthy session affinity
within Top-K. Full or unhealthy session accounts may fall back only within the
Key's authenticated groups and protocol pin constraints. Queued requests recheck
current account state when woken. The original authenticated Key scope does not
expand in flight; Key reassignment applies to the next authenticated request.

The attempt budget includes credential/model/OAuth retries, credits fallback and
handler stream bootstrap replay. It does not override transport retries inside
an executor or Sub2API's separate retries. Local queue/attempt errors return 429
with `Retry-After: 1` and never degrade account health. Delivered stream output
is never replayed. General waiters count globally; an account's waiting number
only counts requests explicitly pinned to it.

Health uses an EWMA of real generation outcomes and first semantic stream output.
Cancellation, client errors, local admission failures, token-count calls and
management probes do not become account failures. Nonstream duration is not TTFT.
Duplex generations are observed individually; an automatic successor without a
request start has unknown TTFT. Realtime/VAD concurrency can only react after a
response-created event; WebRTC without a CPA sideband remains unobserved.
CLIProxyAPIHome continues to own its admission and reports unknown local metrics.

Deploy paired artifacts and retain the existing account limits, tokens, groups,
Key assignments and Sub2API settings. Backend details are documented in
`CLIProxyAPI/docs/account-adaptive-scheduling.md` in the local workspace.

### Applying a paired release

1. Verify clean source revisions, backend tests/build, frontend tests/lint/build,
   and the browser integration test using the matching pair. Hash the artifacts.
2. Stage a candidate using the same pinned image with `--network none`. Use
   private copies of configuration, accounts, plugins, and logs; disable copied
   accounts so the candidate cannot refresh or use production tokens. Never
   share writable production mounts. Enter only its network namespace with
   host Python to check management endpoints, memberships, plugins, and hashes.
   Stop and remove the candidate after verification.
3. Make a private backup of config, auths, compose, plugins, management key,
   release markers, and any previous custom panel. Validate the proposed
   compose with `docker compose config --quiet`; ensure its pinned image exists
   locally and the new executable runs against that image's shared libraries.
4. Gracefully stop only CPA. Take the final config/auth snapshot after shutdown
   so token refreshes completed during staging are retained. Apply the initial
   pool migration only if required. Atomically replace the compose/config/auth
   files with prepared files on the same filesystem; keep old release files.
5. Start only CPA using
   `docker compose up -d --no-deps --no-build --pull never cli-proxy-api`.
   Verify the actual binary/panel mounts and running plugin registration.
   Write release markers for the selected artifacts, then run verification.
   An intentionally small generation through the existing Sub2API connection
   is a separate release smoke check; never add generation to routine verification.
6. On startup or validation failure, stop CPA and restore the matching compose,
   config, plugins, panel and markers before restarting with the same flags.
   Before restoring accounts after any real request, save the newest auth files:
   never overwrite newly refreshed OAuth access/refresh tokens with stale backup
   tokens. Restore only routing metadata on those latest account records, or
   merge the current token fields into the backup before using it.

## Read-only verification

Install `verify.sh` and `verify.py` together in `/opt/cli-proxy-api`. The host
needs Python 3, PyYAML, Docker and `nsenter`; run as root:

```sh
sudo bash /opt/cli-proxy-api/verify.sh
```

Verification parses YAML structurally, checks every configured key's explicit
pool/deny binding against the running management API, account memberships,
enabled plugin registration, TLS hostname/certificate validity through local
Nginx, and the running backend commit plus served HTML against the active
release manifest when present.
It also requests model lists using the keys currently configured in CPA, from
the actual Sub2API network namespace over the shared Docker network. This does
not check whether Sub2API's stored upstream credential still matches a current
CPA key. Separately compare that credential with CPA's current configuration in
a read-only check before declaring the configured Sub2API connection healthy.
Keep values in memory and report only match status; never automatically restore
a key removed by the operator to resolve a mismatch.
Keys pass only in memory/private stdin, never argv or logs. Output contains
counts and check status, not keys, accounts or tokens.
Model lists verify authentication/reachability, not upstream generation health.

The original `provision.sh` and compose template describe first installation
of the official image; do not use them to overwrite an existing custom release.
When installing this updated verifier separately, copy both verification files.
Rotate management credentials with `reset-management-key.sh`. If the public IP
changes, update DNS; the optional IP certificate can be regenerated with
`enable-ip-console.sh NEW_IP`, followed by `nginx -t` and an Nginx reload.
