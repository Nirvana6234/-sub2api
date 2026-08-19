# Transit production host protection

This package is a last-resort safety net for weak production hosts. The release
rule remains unchanged: build and test locally, upload only verified runtime
artifacts or images, then use `docker load` and Compose `--no-build` remotely.

The guard provides four protections:

1. `/usr/local/bin/docker` rejects `docker build`, Buildx builds, Compose builds,
   and Compose `up/create` commands that omit `--no-build`.
2. A systemd service scans `/proc` for direct compiler/build tool invocations
   that bypass the Docker wrapper and terminates their process trees.
3. SSH and configured application container processes receive strongly negative
   OOM scores so the kernel preserves them during memory pressure.
4. After repeated health failures, the guard may restart only explicitly named
   application containers. A cooldown prevents restart loops.

PostgreSQL and Redis names are rejected by the guard. The program never runs
Compose `down`, removes containers, removes volumes, prunes Docker data, edits
`.env`, or reads/writes production data directories.

## Install on the current server

Run this from Windows. The server is an argument and is not embedded in the
guard or installer:

```powershell
$server = "13.212.118.49"
$key = "G:\path\to\unchanged-private-key.pem"
.\install-remote.ps1 -Server $server -SshKey $key
```

The installer waits up to ten minutes for acceptable load, at least 96 MiB of
available memory, and at least 64 MiB of free root-disk space. SSH retries are
spaced 15 seconds apart, the tiny upload is capped at 512 Kbit/s, and the install
runs with idle I/O and reduced CPU priority. It does not upload source code or
execute a build.

Check status later without changing the server:

```powershell
.\install-remote.ps1 -Server $server -SshKey $key -StatusOnly
```

## Configuration

Edit `/etc/transit-host-guard.conf`, then restart only the guard:

```bash
sudo systemctl restart transit-host-guard.service
sudo journalctl -u transit-host-guard.service -n 100 --no-pager
```

`PROTECTED_APP_CONTAINERS` controls OOM protection. `HEALTHCHECKS` controls which
application containers can be restarted. TransitHub can be added after its
actual loopback health endpoint is confirmed, for example:

```bash
PROTECTED_APP_CONTAINERS="sub2api transithub"
HEALTHCHECKS="sub2api|http://127.0.0.1:8080/health;transithub|http://127.0.0.1:10621/api/health"
```

Do not add database or cache services. Even if a PostgreSQL/Redis-like name is
added accidentally, the guard refuses to manage it.

## Verify the command policy

Simulation does not execute the supplied command:

```bash
sudo transit-host-guard simulate -- docker build -t forbidden .
sudo transit-host-guard simulate -- docker compose up -d
sudo transit-host-guard simulate -- docker compose up -d --no-build sub2api
```

The first two commands report `FORBIDDEN`; the last reports `ALLOWED`.

## Roll back

Uninstalling does not touch application containers or data:

```bash
sudo transit-host-guard uninstall
```

The configuration remains at `/etc/transit-host-guard.conf`. Use
`uninstall --purge` only when the configuration should also be removed.

## Residual limits

No user-space watchdog can guarantee survival after a privileged user disables
it, invokes the Docker API directly, or creates extreme pressure faster than the
scan interval. The local-build-only deployment rule is still mandatory. The
Docker wrapper and one-second process scanner are defense in depth, not approval
to build on the server.
