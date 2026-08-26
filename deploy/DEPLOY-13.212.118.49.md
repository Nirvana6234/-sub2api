# Production Deployment: 13.212.118.49

访问方式（2026-08-26 实测，本文档此前写的 `root@` + `/www/sub2api` 是错的）：

```bash
PRODKEY="E:/sub2api云服务/枫迹云-154.9.26.202/vpn-manager/keys/13.212.118.49_my rsa.pem"
```

- 登录用户是 **`ec2-user`**，root 会被明确拒绝并提示改用 ec2-user；特权命令一律 `sudo`
- 密钥不在 `~/.ssh/` 下，路径见上（文件名含空格，必须加引号）
- 应用根目录是 **`/opt/sub2api`**，compose 在 `deploy/docker-compose.local.yml`
- `docker compose up` 必须带 **`--no-build`**，否则被 `transit-host-guard` 拦下：
  `blocked: Docker Compose up must include --no-build on production hosts`。
  被拦时容器不会重启，`docker ps` 的 Status 还是旧的运行时长——别把它误读成"起好了"，
  用 `docker inspect` 看 `/app/sub2api` 的实际挂载源来确认。

Do not assume this host has the same layout as 154.9.26.202. Inspect first:

```bash
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "uname -m; sudo docker ps --format '{{.Names}}\t{{.Image}}\t{{.Status}}'; ls /opt/sub2api 2>/dev/null || echo NOT_FOUND"
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "sudo docker inspect sub2api --format '{{.Config.Image}}{{println}}{{range .Mounts}}{{.Type}} {{.Source}} -> {{.Destination}}{{println}}{{end}}{{index .Config.Labels \"com.docker.compose.project.config_files\"}}'"
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "sudo docker exec sub2api printenv | grep -E '^DATABASE_(HOST|DBNAME|USER)'"
```

If a binary is bind-mounted to `/app/sub2api`, use the bind-mount procedure. If
not, rebuild the image with `Dockerfile.binary`.

## Build

```bash
cd "E:/sub2api云服务/枫迹云-154.9.26.202/sub2api/frontend"
node scripts/clean-out-dir.mjs
node node_modules/vite/bin/vite.js build

cd "E:/sub2api云服务/枫迹云-154.9.26.202/sub2api/backend"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -trimpath -o bin/sub2api-linux-amd64 ./cmd/server
for p in TicketsView 231_seed_client_download client_download_direct_url; do
  printf "%-32s " "$p"; grep -aqF "$p" bin/sub2api-linux-amd64 && echo FOUND || echo MISSING
done
```

The three markers must be `FOUND`. Use `grep -a`, not `strings`. On this
Windows workspace Git Bash can report a hard Node crash as `EXIT=127`; use
PowerShell to obtain the real exit code.

## Backup and upload

```bash
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "sudo docker exec sub2api-postgres pg_dump -U sub2api -d sub2api -Fc -f /tmp/pre-deploy.dump && sudo docker cp sub2api-postgres:/tmp/pre-deploy.dump /opt/sub2api/backups/ && sudo docker exec sub2api-postgres rm -f /tmp/pre-deploy.dump"
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "cd /opt/sub2api && cp deploy/docker-compose.local.yml deploy/docker-compose.local.yml.bak-$(date +%Y%m%d)"
scp -i "$PRODKEY" "E:/sub2api云服务/枫迹云-154.9.26.202/sub2api/backend/bin/sub2api-linux-amd64" ec2-user@13.212.118.49:/opt/sub2api/backend/bin/sub2api-YYYYMMDD-release
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "sudo chmod 755 /opt/sub2api/backend/bin/sub2api-YYYYMMDD-release; grep -aqF TicketsView /opt/sub2api/backend/bin/sub2api-YYYYMMDD-release && echo OK || echo BAD"
```

Never overwrite the old binary; keep it for rollback.

## Switch

For a bind mount, inspect and replace only the mounted filename:

```bash
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "grep -n ':/app/sub2api:ro' /opt/sub2api/deploy/docker-compose.local.yml"
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "cd /opt/sub2api && sed -i 's|bin/OLD_NAME:/app/sub2api:ro|bin/sub2api-YYYYMMDD-release:/app/sub2api:ro|' deploy/docker-compose.local.yml"
```

For an image deployment:

```bash
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "cd /opt/sub2api && cp backend/bin/sub2api-YYYYMMDD-release backend/bin/sub2api-linux-amd64 && DOCKER_BUILDKIT=1 sudo docker build -f Dockerfile.binary -t sub2api:YYYYMMDD-release ."
```

`DOCKER_BUILDKIT=1` is required by the Dockerfile syntax and `COPY --chmod`.
Then update the compose `image:` tag. Both modes finish with:

```bash
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "cd /opt/sub2api && sudo docker compose -f deploy/docker-compose.local.yml up -d --no-deps --no-build sub2api"
```

## Verify

```bash
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "sleep 45; sudo docker ps --filter name=^sub2api\\$ --format '{{.Status}}'; curl -s -o /dev/null -w 'health=%{http_code}\\n' http://127.0.0.1:8080/health"
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "sudo docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc \"select filename from schema_migrations order by filename desc limit 3;\"; sudo docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc \"select table_name from information_schema.tables where table_name in ('tickets','ticket_messages');\""
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "curl -s http://127.0.0.1:8080/api/v1/settings/public | python3 -m json.tool | grep -E 'client_download|backup_payment'"
ssh -i "$PRODKEY" ec2-user@13.212.118.49 "curl -s -o /dev/null -w 'tickets=%{http_code} (expected 401)\\n' http://127.0.0.1:8080/api/v1/tickets; curl -s -o /dev/null -w 'download=%{http_code} (expected 200)\\n' http://127.0.0.1:8080/download; curl -s -o /dev/null -w 'dlapi=%{http_code}\\n' http://127.0.0.1:8080/api/v1/download/client"
curl -s https://icode-xtu.ccwu.cc/api/v1/settings/public | grep -o 'client_download_direct_url[^,]*'
```

Expect migrations `229`, `230`, `231`, both ticket tables, tickets `401`,
download page `200`, and a reachable download API. Update
`/opt/sub2api/DEPLOYED_COMMIT` after success; its old value may be stale.

## Rollback

For bind mounts, restore the old mounted filename. For image deployments,
restore the previous image tag and run the same compose command. Migrations
229-231 are additive, so old binaries ignore them; restore the dump only for a
deliberate full rollback.

## Scope warning

The binary embeds the current workspace, including TransitHub, blacklist, and
Kiro compatibility changes. A ticket-only release requires a clean worktree
before building.
