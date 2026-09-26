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

The current September 26 release is `20260926-4a27790-s2ui-0b7bdcf`, pairing
backend `4a27790` with management UI `0b7bdcf`. Build the Linux backend with CGO
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

- **分组管理** (`#/credential-pools`): create groups, manage their accounts, and
  follow account/key counts to the corresponding filtered lists.
- **账号管理** (`#/auth-files`): filter accounts by group and assign a group to
  one account or a selected batch. Assignment replaces an OAuth account's
  existing memberships; confirm replacement when moving a legacy multi-group
  account.
- **API 密钥** (`#/api-keys`): create keys with explicit groups, edit group
  access, disable/enable keys, and rotate keys while preserving their bindings.
  Keys can access multiple groups; a disabled key has an empty binding (`[]`).

Create the groups, assign accounts, and then select those groups for each key
before using it in Sub2API. When replacing a key, also update the corresponding
Sub2API upstream credential; rotating a CPA key does not update Sub2API for you.

## Release procedure

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
