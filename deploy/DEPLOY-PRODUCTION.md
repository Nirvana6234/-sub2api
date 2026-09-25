# 生产部署手册（sub2api + TransitHub）

> 本文取代 `DEPLOY-13.212.118.49.md`。**不要把 IP 写进文件名或流程里** —— 该实例没有绑
> Elastic IP，每次 Stop/Start 都会换 IP（2026-08-28 已从 `13.212.118.49` 变为 `52.76.22.21`）。
>
> 全文分两部分：**Part A 搬到新服务器**（从零部署）、**Part B 日常发版**（更新已有服务器）。
> 基础设施流程于 2026-08-28 对照运行中的生产机逐条核对过；最近一次主应用发版核验于 2026-09-20 完成。

```bash
PRODKEY="E:/sub2api云服务/枫迹云-154.9.26.202/vpn-manager/keys/52.76.22.21.pem"
PRODIP="52.76.22.21"      # 每次重启都要确认，别照抄
```

- 登录用户是 **`ec2-user`**，root 会被明确拒绝；特权命令一律 `sudo`
- 密钥不在 `~/.ssh/` 下，路径见上。当前文件名为 `52.76.22.21.pem`，使用前先确认文件存在；
  下次 IP 变化时先用 `Get-ChildItem vpn-manager/keys -Filter *.pem` 核实真实文件名，不要直接照抄旧路径

---

# Part A · 搬到新服务器

## A1. 服务器规格（血泪教训，别再选小了）

2026-08-28 生产机因资源耗尽彻底卡死（TCP 握手能通但 sshd 发不出 banner，只能从控制台
Stop/Start）。事后 sar 显示它**长期 24 小时 100% CPU 零余量运行**。

当时的配置和问题：

| 项 | 旧配置 | 问题 | 新机器建议 |
|---|---|---|---|
| 实例 | **t3.small**（2 vCPU / 2 GB） | 跑 7 个容器 + nginx，CPU 常年满载，`%idle` 只有 0.02–0.87% | **至少 t3.medium**，稳妥用 `m5.large` / `c5.large`（非突发型，CPU 不限流） |
| 根卷 | gp3 8 GB | 用到 83%，日志一涨就告急 | gp3 **30 GB 起** |
| swap | **无** | 内存一触顶就直接卡死，无缓冲 | **必须配**（见 A3） |
| 弹性 IP | 无 | 每次重启换 IP，DNS 全废 | **务必绑 Elastic IP** |

> t3 是「可突发」实例，CPU credit 用完后靠 unlimited 模式付超额费维持。旧机器的
> `CPUCreditBalance` 长期为 0 —— 等于一直在额外付费还跑不动。**别用 t 系列跑 7×24 生产。**

## A2. 系统初始化

```bash
# 必装包（版本为旧机器实测基线）
sudo dnf install -y docker nginx certbot python3-certbot-nginx sysstat logrotate
sudo systemctl enable --now docker nginx
sudo systemctl enable --now sysstat-collect.timer   # 关键：出事后靠它回溯历史
sudo usermod -aG docker ec2-user                     # 重新登录生效
```

**`sysstat` 一定要装并启用 timer。** 这次能定位根因全靠它每 10 分钟自动采集的历史数据：

```bash
sar -f /var/log/sa/sa<日>       # CPU
sar -r -f ...                    # 内存（含 page cache）
sar -q -f ...                    # load / 运行队列
sar -w -f ...                    # 进程创建 / 上下文切换
sar -n SOCK -f ...               # TIME_WAIT ← 排查连接复用必看
sar -n DEV -f ...                # 网络流量
```

> ⚠️ **AL2023 默认不跑 `crond`**（`systemctl is-active crond` = inactive）。
> 往 `/etc/cron.d/` 放任务**不会执行**，必须用 systemd timer。旧机器上那条
> `/etc/cron.d/xray-user-expiry` 恐怕从来没生效过。

## A3. swap（根卷是 xfs，有坑）

```bash
sudo dd if=/dev/zero of=/swapfile bs=1M count=2048 status=none
sudo chmod 600 /swapfile
sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
echo 'vm.swappiness=10' | sudo tee /etc/sysctl.d/99-swap.conf
sudo sysctl -q vm.swappiness=10
```

- **必须用 `dd`，不能用 `fallocate`** —— 根文件系统是 xfs，`fallocate` 生成的 swapfile
  启用时会报 "swapfile has holes"
- `swappiness=10`：swap 只做 OOM 兜底，不做常规内存扩展（避免额外磁盘 IO）

## A4. docker 日志上限（不配会吃满磁盘）

日志驱动是 `json-file` 且**默认无任何大小限制**。旧机器上单个容器日志涨到过 212 MB。

```bash
# 方式一：daemon.json —— 只对之后新建的容器生效，且需重启 dockerd
sudo mkdir -p /etc/docker
sudo tee /etc/docker/daemon.json <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "20m", "max-file": "2" }
}
EOF
sudo systemctl restart docker    # 新机器部署时做，此时还没有容器，无损
```

### ⛔ 绝对不要用 logrotate + copytruncate 管理 docker 日志

本文档早期版本推荐过这个方案，**它在 2026-08-28 直接导致了一次生产 CPU 满载事故**，
已作废。留在这里是为了防止有人重新想出这个"好主意"。

```bash
# ❌ 有害示例，切勿使用
/var/lib/docker/containers/*/*-json.log {
    size 20M
    copytruncate      # ← 元凶
    ...
}
```

**故障机理**：`copytruncate` 是"先把文件复制成 `.1`，再把原文件截断为 0"。而
`docker logs` 的服务端实现（`loggerutils.(*LogFile).readLogsLocked` →
`tailfile.NewTailReader`）会持有一个记录了**原始文件大小**的 `SectionReader`。
一旦文件在读取途中被截断：

- 读取器仍按旧偏移（事故当时是 86 MB）去 `pread`，永远读到 0 字节
- `tailfile` 的反向扫描器因此**陷入死循环**
- 多个并发 `docker logs` 请求还要争抢 `readLogsLocked` 的锁

实测后果：`pread64` 每秒 3.9 万次、`futex` 占 64% CPU、dockerd 吃掉 **115% CPU**
（2 核共 200%），load 冲到 8.9，而日志写入量其实只有 6.2 KB/s。
**症状极具迷惑性**：CPU 满载时业务进程（transithub-api、sub2api、postgres）占用也很高，
容易误判成业务负载问题，但那只是它们在排队等 CPU。

**唯一的判据**：只重启 dockerd（不动任何业务容器），若 load 立刻回落，就是这个问题。

### ✅ 正确做法：用 docker 原生轮转

就用上面 daemon.json 里的 `max-size` / `max-file`。docker 自己管理日志文件的轮转，
它清楚自己有哪些读取器，会正确协调，不存在截断竞态。

代价是**对已存在的容器不生效**——`log-opts` 只在容器创建时读取。注意区分：

| 操作 | 效果 |
|---|---|
| `systemctl restart docker` | ❌ 不够。容器被 restart，沿用旧的空 LogConfig |
| `docker compose up -d --force-recreate` | ✅ 容器被**重建**，才会应用 daemon.json |

所以新机器部署时要**先写 daemon.json 再创建容器**，一劳永逸。对已运行的老机器，
在下次发版重建容器时自然生效即可；在那之前宁可让日志涨着（磁盘够就行），
**也不要用 copytruncate 去"救急"**。

## A5. 目录布局

应用根目录是 **`/opt/sub2api`**（不是某些旧文档写的 `/www/sub2api`）：

```
/opt/sub2api/
├── DEPLOYED_COMMIT          # 记录已上线 commit，发版后必须更新
├── Dockerfile.binary        # 镜像模式用
├── backend/bin/             # 二进制，按日期命名，保留旧版供回滚
├── backups/                 # pg_dump 存放处
├── deploy/
│   ├── .env                 # 密钥（600，不入库）
│   ├── docker-compose.local.yml
│   └── data/                # → 挂载为容器内 /app/data
│       ├── config.yaml      # 精简配置（见 A6）
│       ├── logs/  plugins/  public/  pages/
│       └── model_pricing.json
├── client-release/  releases/  scripts/
```

## A6. 配置：三条路径与优先级（务必读完）

程序用 viper，配置有三个来源，**优先级：环境变量 > config.yaml > 代码默认值**：

```go
viper.SetConfigName("config")                            // 找 config.yaml
viper.AutomaticEnv()                                     // 读环境变量
viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))   // 点 → 下划线
```

映射规则：`gateway.connection_pool_isolation` ←→ `GATEWAY_CONNECTION_POOL_ISOLATION`

**生产上 `data/config.yaml` 只是一份 855 字节的精简配置**，仅含
`server / database / redis / jwt / default / rate_limit / timezone / fallback_pool_alert`
八段，**没有 `gateway` 段**。上游那份带完整中英文选型注释的
`deploy/config.example.yaml` **从未部署到服务器上**。

> 这就是踩坑的根源：上游把「各选项适合什么场景」写在 `config.example.yaml` 的注释里，
> 而生产走的是 compose 环境变量 + 精简 yaml，那些注释在部署路径上根本看不到，
> 于是所有 `gateway.*` 项都静默用了代码默认值。**新部署务必对照下一节逐项确认。**

### ⚠️ 必须显式设置的配置项

**`GATEWAY_CONNECTION_POOL_ISOLATION`（最重要，2026-08-28 事故根因）**

```yaml
- GATEWAY_CONNECTION_POOL_ISOLATION=${GATEWAY_CONNECTION_POOL_ISOLATION:-proxy}
```

| 取值 | 含义 | 适用 |
|---|---|---|
| `proxy` | 按代理隔离，同一代理共享连接池 | **代理少、账户多 ← 我们的情况** |
| `account` | 按账户隔离 | 账户少、需严格隔离 |
| `account_proxy` | 账户+代理组合，最细粒度 | **上游默认值，我们踩的就是它** |

**为什么必须改**：生产有 219 个账号，其中 **213 个没配代理、全部直连同一批上游**。
用 `account_proxy` 会为每个账号建独立连接池 → 219 个池 → 账号切换时无法复用连接 →
每次都要完整 TLS 握手。实测症状：

```
%user 67 / %system 31 / %idle 0.3     24 小时恒定，与业务量无关
cswch/s 15000                          上下文切换是正常值的 5 倍
tcp-tw 1034–1442                       TIME_WAIT 堆积（正常应在 20–50）
```

改成 `proxy` 后连接池从 219 个降到 2 个。**验证判据**：业务高峰时
`sudo sar -n SOCK -f /var/log/sa/sa<日>` 的 `tcp-tw` 峰值应稳定在两三百以内。

**容器内存上限（旧机器全是 `MemLimit=0` 不限，是内存被抽干的直接原因）**

Go 服务必须 `mem_limit` 和 `GOMEMLIMIT` **成对设置**。只设 cgroup 限制而不告诉 Go，
它不知情会照样扩堆，撞上限被内核 OOM kill（硬中断）；设了 `GOMEMLIMIT`，Go 会在接近时
主动加强 GC（只是变慢，不中断）。

```yaml
sub2api:
  mem_limit: 512m
  environment:
    - GOMEMLIMIT=400MiB     # 留出差值给 goroutine 栈等非堆内存
```

实测各容器内存（供定值参考，按 2 GB 机器算，总和留 30% 给系统和 page cache）：

| 容器 | 实测常态 | 建议上限 |
|---|---|---|
| sub2api | 44 MB（峰值曾达 1045 MB） | 512m + `GOMEMLIMIT=400MiB` |
| sub2api-postgres | 96 MB | 256m |
| transithub | 17 MB | 192m |
| transithub-postgres | 50 MB | 160m |
| transithub-gpt56-detector | 30 MB | 128m |
| sub2api-redis | 10 MB | 64m |
| transithub-redis | 7 MB | 48m |

> sub2api 常态 44 MB 但峰值到过 1045 MB —— Go 的 `GOGC` 默认让堆长到存活对象的 2 倍，
> **无上限时会一路吃到系统内存耗尽**。建议先只给 sub2api 设，观察一周无 `OOMKilled` 再推广。

### ⚠️ 只活在数据库里、代码里看不出来的运行时开关（2026-09-09 事故教训）

以下几项是 `settings`/`groups` 表里的**运行时数据**，不是 config.yaml 或环境变量，代码里的默认值
往往是"关闭"——**只在从旧库 `pg_restore` 迁移时才会带过去**。如果是从源代码新建一个空库
（而不是拿旧库恢复），这些会静默回到关闭状态，且没有任何地方会报错提醒。新部署或怀疑
配置丢失时，照下表逐条核对：

| 表.字段 | 期望值 | 干什么用的 | 代码默认值 |
|---|---|---|---|
| `settings['openai_apikey_health_breaker_settings']` | `{"enabled":true,"window_minutes":30,"failure_threshold":3,"cooldown_minutes":10}` | OpenAI 拼车池账号健康熔断：窗口内连续失败达阈值自动 `temp_unschedulable`，到点自动恢复重试 | `enabled:false`（[openai_apikey_health_breaker.go](../backend/internal/service/openai_apikey_health_breaker.go)，代码里从未开过） |
| `settings['openai_latency_aware_fallback_enabled']` | `true` | 分组延迟感知主动兜底：账号"名义上还可调度"但实测尾延迟超阈值时，主动借兜底分组的号，不需要等到本分组账号彻底归零 | `false` |
| `groups.fallback_group_ids`（`id=2` "plus"） | `[29]`（兜底到 `puls-兜底`） | 见下方兜底分组说明 | `[]`（历史上建好了兜底分组但从没接线） |
| `groups.fallback_group_ids`（`id=12` "plus-专线"） | `[31]`（兜底到 `plus-专线兜底`） | 同上 | `[]` |

**分组兜底两件事必须都成立，缺一不可**：① 主分组的 `fallback_group_ids` 指向目标分组 id；
② 目标分组本身要 `is_fallback_pool=true` 且平台一致（[fallback_pool.go](../backend/internal/service/fallback_pool.go)
的 `fallbackGroupOK` 校验）。`is_fallback_pool` 这个标记通常已经在建组时设对，真正容易漏的是①。

查当前值：

```bash
docker exec sub2api-postgres psql -U sub2api -d sub2api -c \
  "select key, value from settings where key in ('openai_apikey_health_breaker_settings','openai_latency_aware_fallback_enabled');"
docker exec sub2api-postgres psql -U sub2api -d sub2api -c \
  "select id, name, fallback_group_ids, is_fallback_pool from groups where deleted_at is null order by id;"
```

### `.env` 需要的变量（值不入库，从旧机器安全迁移）

```
BIND_HOST  SERVER_PORT  TZ
POSTGRES_USER  POSTGRES_DB  POSTGRES_PASSWORD
ADMIN_EMAIL  ADMIN_PASSWORD
JWT_SECRET  TOTP_ENCRYPTION_KEY
```

`JWT_SECRET` 和 `TOTP_ENCRYPTION_KEY` **必须与旧机器一致**，否则所有用户 token 失效、
两步验证全部作废。文件权限 `600`。

## A7. 容器与端口

7 个容器（compose 在 `deploy/docker-compose.local.yml`）：

| 容器 | 端口 | 说明 |
|---|---|---|
| sub2api | `127.0.0.1:8080` | 主应用，仅本机监听，经 nginx 反代 |
| sub2api-postgres | 5432（内网） | postgres:18-alpine |
| sub2api-redis | 6379（内网） | redis:8-alpine |
| transithub | `127.0.0.1:10621` | |
| transithub-postgres | 内网 | postgres:16-alpine |
| transithub-redis | 内网 | redis:7-alpine |
| transithub-gpt56-detector | 8760（内网） | |

健康检查间隔：transithub / 两个 postgres / 两个 redis 都是 **10s**，sub2api 和 detector 是 30s。
在小机器上 10s 偏密（每次都要 dockerd fork 进程 exec 进容器），可考虑放宽到 30s。

## A8. 宿主机组件

**nginx**：`/etc/nginx/conf.d/` 下有 `sub2api.conf`、`transithub.conf`
（目录里堆了 5+ 个 `.bak-*` 历史备份，迁移时只带当前版本）。

### Nginx 访问日志过滤与保留上限

`/api/v1/admin/usage` 是管理端分页接口，查询参数不应让每次分页都写入访问日志。
本仓库提供了可复制的配置片段：

```text
deploy/nginx/sub2api-access-log.conf.example
deploy/logrotate/sub2api-nginx.example
```

在生产机应用时：

1. 将 `sub2api-access-log.conf.example` 中的 `map` 放到 `/etc/nginx/conf.d/`，并把
   `/etc/nginx/conf.d/sub2api.conf` 的 access log 改为：
   `access_log /var/log/nginx/sub2api_access.log main if=$sub2api_access_loggable;`
2. 在现有 `/etc/logrotate.d/nginx` 规则中把 `rotate 10` 改为 `rotate 20`。不要为同一个
   日志路径再创建第二条 logrotate 规则，否则会产生重复日志条目；`rotate 20` 表示最多
   保留最近 20 个轮转文件，不影响应用数据或数据库备份。
3. 先执行 `sudo nginx -t`，通过后执行 `sudo systemctl reload nginx`。

不要用 `copytruncate` 管理 Docker 日志，详见 A4 的事故说明。

**证书**（certbot，`/etc/letsencrypt/live/`）：

```
icode-xtu.ccwu.cc            主站
icode-xtu-manage.ccwu.cc     管理端
gongfeiai.com
```

新机器上重新签发即可（`certbot --nginx -d <域名>`），别直接拷贝证书目录。
**签发前确认 DNS 已指向新 IP**，否则 HTTP-01 校验过不了。

**`transit-host-guard`**（`/usr/local/sbin/transit-host-guard`，742 行 bash + systemd unit）
两个职责：

1. **拦截生产机上的构建命令** —— 所以 `docker compose up` **必须带 `--no-build`**，否则：
   ```
   blocked: Docker Compose up must include --no-build on production hosts
   ```
   被拦时容器不会重启，但 `docker ps` 的 Status 仍显示旧运行时长 —— **别误读成"起好了"**，
   用 `docker inspect` 看 `/app/sub2api` 的实际挂载源确认
2. **OOM 保护 + 看门狗** —— 给 sshd 设 `OOMScoreAdjust=-900`、应用容器 `-800`
   （保证 OOM 时留住 SSH）；健康检查连续失败 5 次则重启容器

> **已修复的 bug（2026-08-28）**：`check_application_health` 里冷却期时间戳原本只在重启
> 成功时记录，而系统过载时 `timeout 45 docker restart` 必然超时返回非 0 → 300 秒冷却期
> 永久失效 → 连续重启雪崩（实测 68 秒内重启同一容器两次）。修复是把时间戳记录移出 `if`：
>
> ```bash
> HEALTH_LAST_RESTART["${container}"]=${now}
> HEALTH_FAILURES["${container}"]=0
> if ! restart_application_container "${container}"; then
>   log "restart attempt failed; cooldown applied anyway name=${container}"
> fi
> ```
>
> **迁移到新机器时记得带上修复后的版本**，别从旧备份复制回未修复的。

## A9. 数据迁移

```bash
# 旧机器导出
ssh -i "$PRODKEY" ec2-user@<旧IP> "sudo docker exec sub2api-postgres pg_dump -U sub2api -d sub2api -Fc -f /tmp/migrate.dump && sudo docker cp sub2api-postgres:/tmp/migrate.dump /opt/sub2api/backups/"
scp -i "$PRODKEY" ec2-user@<旧IP>:/opt/sub2api/backups/migrate.dump ./

# 新机器导入（容器起来之后）
scp -i "$PRODKEY" ./migrate.dump ec2-user@<新IP>:/tmp/
ssh -i "$PRODKEY" ec2-user@<新IP> "sudo docker cp /tmp/migrate.dump sub2api-postgres:/tmp/ && sudo docker exec sub2api-postgres pg_restore -U sub2api -d sub2api --clean --if-exists /tmp/migrate.dump"
```

同时要带走：`deploy/.env`、`deploy/data/`（config.yaml、plugins、public、model_pricing.json）、
`backend/bin/` 里当前和上一版二进制。TransitHub 的库同理单独 dump。

---

# Part B · 日常发版

## B0. 构建前必查：迁移校验和体检（2026-09-06 事故教训）

**这一步比 B1 更早，跳过它可能导致新二进制在生产上直接崩溃重启循环。**

`internal/repository/migrations_runner.go` 在每次启动时都会用 sha256(TrimSpace(content))
重新校验*全部*已应用迁移文件，跟生产库 `schema_migrations` 表记的校验和逐条比对，
不匹配就 `Failed to initialize application` 直接退出——不是只查新迁移。仓库里有历史遗留：
某次"发布公开快照"的提交清理过迁移文件里的真实数据（比如具体的下载直链），
悄悄改变了文件内容和校验和，但生产库记的还是发布前的旧值。2026-09-06 就因为这个
（233 号迁移）导致一次新二进制部署后容器崩溃重启循环，被迫紧急回滚。

构建前跑一遍体检，本地几秒钟就能发现，不用等部署到生产才炸：

```bash
ssh -i "$PRODKEY" ec2-user@$PRODIP "sudo docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc \"select filename || '|' || checksum from schema_migrations order by filename;\"" > /tmp/prod_migration_checksums.txt
cd "E:/sub2api云服务/枫迹云-154.9.26.202/sub2api/backend"
go run ./cmd/checkmigrations migrations /tmp/prod_migration_checksums.txt
```

输出里只要有 `MISMATCH`（而不是 `WHITELISTED`）就**不要继续构建部署**：先按提示把这两个
checksum 加进 `migrationChecksumCompatibilityRules`（照抄文件里已有的十几条写法），
确认这份差异确实是历史上的合法改动，不是真的数据损坏或恶意篡改，跑体检工具确认
变成 `0 unresolved mismatches` 再继续 B1。

## B1. 构建

```bash
cd "E:/sub2api云服务/枫迹云-154.9.26.202/sub2api/frontend"
node scripts/clean-out-dir.mjs
node node_modules/vite/bin/vite.js build

cd "E:/sub2api云服务/枫迹云-154.9.26.202/sub2api/backend"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags embed -trimpath -o bin/sub2api-linux-amd64 ./cmd/server
```

- **必须先清前端输出再构建**（E: 盘上 `fs.rmSync` 会让 Node 硬崩，vite 的 `emptyOutDir` 走的就是它）
- **Go 构建必须带 `-tags embed`**，否则前端不会被打进二进制
- 用 `grep -a` 而不是 `strings` 校验产物里是否包含本次改动的标志串
- Git Bash 会把 Windows 的硬崩溃报成 `EXIT=127`，拿真实退出码要用 PowerShell

## B2. 备份并上传

```bash
ssh -i "$PRODKEY" ec2-user@$PRODIP "sudo docker exec sub2api-postgres pg_dump -U sub2api -d sub2api -Fc -f /tmp/pre-deploy.dump && sudo docker cp sub2api-postgres:/tmp/pre-deploy.dump /opt/sub2api/backups/ && sudo docker exec sub2api-postgres rm -f /tmp/pre-deploy.dump"
ssh -i "$PRODKEY" ec2-user@$PRODIP "cd /opt/sub2api && cp deploy/docker-compose.local.yml deploy/docker-compose.local.yml.bak-\$(date +%Y%m%d-%H%M%S)"
scp -i "$PRODKEY" "…/backend/bin/sub2api-linux-amd64" ec2-user@$PRODIP:/opt/sub2api/backend/bin/sub2api-YYYYMMDD-<feature>-r1
ssh -i "$PRODKEY" ec2-user@$PRODIP "sudo chmod 755 /opt/sub2api/backend/bin/sub2api-YYYYMMDD-<feature>-r1"
```

**永远用新文件名上传，绝不覆盖旧二进制**（回滚就靠它）。

> `deploy/` 下已堆积 15+ 个 `docker-compose.local.yml.bak-*`，定期清理一下。

## B3. 切换

当前是 **bind mount 模式**（二进制挂载进容器），只需改挂载的文件名：

```bash
ssh -i "$PRODKEY" ec2-user@$PRODIP "grep -n ':/app/sub2api:ro' /opt/sub2api/deploy/docker-compose.local.yml"
ssh -i "$PRODKEY" ec2-user@$PRODIP "cd /opt/sub2api && sed -i 's|bin/OLD_NAME:/app/sub2api:ro|bin/sub2api-YYYYMMDD-<feature>-r1:/app/sub2api:ro|' deploy/docker-compose.local.yml"
ssh -i "$PRODKEY" ec2-user@$PRODIP "cd /opt/sub2api && sudo docker compose -f deploy/docker-compose.local.yml up -d --no-deps --no-build sub2api"
```

`--no-deps` 避免连带重启数据库，`--no-build` 是 guard 的硬性要求，两个都不能省。

镜像模式（当前未使用）需 `DOCKER_BUILDKIT=1 docker build -f Dockerfile.binary`，
但**在生产机上构建会被 guard 拦截且会拖垮机器**，建议本地构建后推镜像。

## B4. 验证

```bash
# 健康与容器状态
ssh -i "$PRODKEY" ec2-user@$PRODIP "sleep 45; sudo docker ps --format '{{.Names}}: {{.Status}}'; curl -s -o /dev/null -w 'health=%{http_code}\n' http://127.0.0.1:8080/health"

# 迁移（不要写死版本号，看最新几条即可）
ssh -i "$PRODKEY" ec2-user@$PRODIP "sudo docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc 'select filename from schema_migrations order by filename desc limit 4;'"

# 接口
ssh -i "$PRODKEY" ec2-user@$PRODIP "curl -s -o /dev/null -w 'tickets=%{http_code} (期望401)\n' http://127.0.0.1:8080/api/v1/tickets; curl -s -o /dev/null -w 'download=%{http_code} (期望200)\n' http://127.0.0.1:8080/download"

# 关键配置有没有丢
ssh -i "$PRODKEY" ec2-user@$PRODIP "sudo docker inspect sub2api --format '{{range .Config.Env}}{{println .}}{{end}}' | grep ISOLATION"
```

> **2026-09-20 核对**：本次生产库最新迁移为 **256**，最近三条为：
> `256_remove_legacy_openai_oauth_passthrough.sql`、`255_tickets.sql`、
> `254_bump_client_download_to_v0_5.sql`。迁移编号会继续变化；验证时看最新几条是否符合本次发布预期，
> 不要把 `256` 写死到脚本中。

成功后更新 `/opt/sub2api/DEPLOYED_COMMIT`（它的旧值可能是旧的），并记录本次实际构建标识：

```bash
ssh -i "$PRODKEY" ec2-user@$PRODIP \
  "printf '%s\n' '<local-commit-or-build-id>' | sudo tee /opt/sub2api/DEPLOYED_COMMIT"
```

### B4.1 最近一次发版记录（2026-09-20）

本次使用本地构建的二进制 `sub2api-20260920-state-kit-playground-r1`，服务器路径为：

```text
/opt/sub2api/backend/bin/sub2api-20260920-state-kit-playground-r1
```

本地与服务器 SHA256 必须一致；本次校验值为：

```text
428068ac2c54a01d013ec514d09b6724ed526522580da6196dda1a39d6949a05
```

本次还验证了 `sub2api=healthy`、`/health=200`、`/api/v1/tickets=401`、
`/download=200`。`transithub-gpt56-detector` 的 `unhealthy` 状态在发版前已存在，
本次没有重启或修改 TransitHub；排查 TransitHub 时不要把它当作主应用发版失败。

## B5. 回滚

bind mount 模式：把 compose 里挂载的文件名改回上一个二进制，重跑 B3 最后那条命令即可。
增量迁移对旧二进制是无害的（旧代码直接忽略新表/新列），**只有确实要整体回退时才恢复 dump**。

## B6. 发布范围警告

二进制会嵌入当前工作区的**全部**改动，包括 TransitHub、黑名单、Kiro 兼容等。
只想发某一个特性时，**必须先确保工作树干净**再构建。

---

# Part C · TransitHub 发版（与 sub2api 流程不同）

TransitHub 是**另一套 compose 项目**，部署形态也和 sub2api 不一样，别照抄 Part B。

| | sub2api | TransitHub |
|---|---|---|
| compose | `/opt/sub2api/deploy/docker-compose.local.yml` | `/opt/transit-hub/docker-compose.yml` |
| 服务名 | `sub2api` | `app` |
| 二进制位置 | `/opt/sub2api/backend/bin/<名字>` | `/opt/transithub-releases/<版本>/transithub-api` |
| 前端 | **编进二进制**（`-tags embed`） | **独立 `public/` 目录**，单独挂载 |
| 端口 | `127.0.0.1:8080` | `127.0.0.1:10621` |
| 健康检查 | `/health` | `/api/health` |

TransitHub 用 **release 目录**组织：每次发版建一个 `/opt/transithub-releases/<版本>/`，
里面放 `transithub-api` 和 `public/`，然后改 compose 的挂载路径。

## C1. 构建（前后端都要）

```bash
# 后端（注意：不需要 -tags embed，前端是独立目录）
cd sub2api/transit-hub/backend
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
  -o bin/transithub-api-YYYYMMDD-<feature>-r1 ./cmd/api

# 前端（输出到 C 盘，避开 E 盘 fs.rmSync 崩溃）
cd sub2api/transit-hub/frontend
node node_modules/vite/bin/vite.js build --outDir C:/tmp-th-dist --emptyOutDir

# 打包 public（结构是 dist 内容直接在根，即 ./index.html + ./assets/）
cd sub2api/transit-hub/backend/bin
tar -czf transithub-public-YYYYMMDD-<feature>-r1.tar.gz -C C:/tmp-th-dist ./
```

> **`tar` 的坑**：直接写 `tar -czf "E:/路径/x.tar.gz"` 会失败并报
> `Cannot connect to E: resolve failed` —— tar 把 `E:` 当成了远程主机（`host:path` 语法）。
> 必须先 `cd` 到目标目录用相对文件名，配合 `-C <源目录>` 指定内容来源。

## C2. ⚠️ 构建产物必须校验（2026-08-28 的教训）

**那次差点把旧二进制当新版本部署上去。** 现象是：文件修改时间是新的，但内容根本没变。

```bash
# ① 和上一版比 MD5 —— 必须不同
md5sum bin/transithub-api-YYYYMMDD-<feature>-r1 bin/transithub-api-<上一版>

# ② 用 grep -a 确认本次改动的标志串真的进去了（用 grep -a，不要用 strings）
for p in <本次新增的字符串1> <字符串2>; do
  printf '%-30s ' "$p"
  grep -aqF "$p" bin/transithub-api-YYYYMMDD-<feature>-r1 && echo FOUND || echo MISSING
done
```

**两项都过了才允许上传。** 当时 MD5 与上一版完全一致、所有标志串 MISSING，说明源码写了但
根本没重新编译（很可能构建到别处或构建步骤被跳过）。文件时间戳会骗人，MD5 不会。

**另一个连带问题**：如果本次改动含前端 UI，而 `frontend/dist` 已被清理，
**必须重新构建前端**，否则后端更新了、界面还是旧的。

## C3. 上传与安装

```bash
scp -i "$PRODKEY" bin/transithub-api-YYYYMMDD-<feature>-r1 ec2-user@$PRODIP:/tmp/
scp -i "$PRODKEY" bin/transithub-public-YYYYMMDD-<feature>-r1.tar.gz ec2-user@$PRODIP:/tmp/

# 服务器端
REL=/opt/transithub-releases/YYYYMMDD-<feature>-r1
sudo mkdir -p $REL/public
sudo install -o root -g root -m 0755 /tmp/transithub-api-YYYYMMDD-<feature>-r1 $REL/transithub-api
sudo tar -xzf /tmp/transithub-public-YYYYMMDD-<feature>-r1.tar.gz -C $REL/public
sudo chown -R root:root $REL/public      # scp 上来的属主是 Windows uid，要修正
rm -f /tmp/transithub-*                   # 清理
```

发版前先备份数据库（**注意用户/库名是 `transithub` 不是 `sub2api`**）：

```bash
sudo docker exec transithub-postgres pg_dump -U transithub -d transithub -Fc -f /tmp/pre-deploy.dump
sudo docker cp transithub-postgres:/tmp/pre-deploy.dump /opt/transithub-releases/backups/
sudo cp -a /opt/transit-hub/docker-compose.yml /opt/transit-hub/docker-compose.yml.bak-$(date +%Y%m%d-%H%M%S)
```

## C4. 切换挂载（务必限定行号）

```bash
sudo grep -n 'transithub-releases' /opt/transit-hub/docker-compose.yml
```

会看到三行，**只有前两行是 app 的，第三行是 gpt56-detector 的，绝对不能一起替换**：

```
32:  - /opt/transithub-releases/<版本>/transithub-api:/app/transithub-api:ro   ← 改
33:  - /opt/transithub-releases/<版本>/public:/app/public:ro                   ← 改
57:  - /opt/transithub-releases/<另一版本>/gpt56-detector:/app:ro              ← 不要动
```

所以 sed 必须限定行号，不能全局替换：

```bash
sudo sed -i '32,33s|<旧版本>|<新版本>|' /opt/transit-hub/docker-compose.yml
sudo sed -n '32,33p;57p' /opt/transit-hub/docker-compose.yml    # 确认 57 行没被改
cd /opt/transit-hub && sudo docker compose -f docker-compose.yml config --quiet && echo OK
```

## C5. 重建并验证

```bash
cd /opt/transit-hub
sudo docker compose -f docker-compose.yml up -d --no-deps --no-build --force-recreate app
```

验证：

```bash
sudo docker inspect transithub --format '{{.State.Health.Status}}'
sudo docker inspect transithub --format '{{range .Mounts}}{{if eq .Destination "/app/transithub-api"}}{{.Source}}{{end}}{{end}}'
curl -s -o /dev/null -w 'transithub=%{http_code}\n' http://127.0.0.1:10621/api/health
```

## C6. `--force-recreate` 的额外价值

普通发版用 `up -d` 即可（挂载路径变了会自动重建）。但如果改过 `/etc/docker/daemon.json`
（例如日志上限），**必须 `--force-recreate` 才会应用**：

| 操作 | LogConfig |
|---|---|
| `systemctl restart docker` | ❌ 容器沿用旧配置，`opts=map[]` |
| `up -d --force-recreate` | ✅ `opts=map[max-file:2 max-size:20m]` |

2026-08-28 实测确认。所以**改完 daemon.json 后，趁下次发版顺带 force-recreate 一遍**，
比专门停机去做划算。注意数据库/redis 容器如果没跟着重建，它们的日志配置仍是旧的
（这几个日志量极小，可以留到以后）。

---

## C7. 管理员 API 内网路由与检测器目录（2026-09-25）

TransitHub 的本站管理员请求通过容器网络访问 Sub2API。生产配置为：

```env
SUB2API_INTERNAL_ADMIN_ORIGINS=https://icode-xtu.ccwu.cc,https://gongfeiai.com
SUB2API_INTERNAL_ADMIN_URL=http://sub2api-internal:8080
```

仅匹配上述 origin 的 `/api/v1/admin`、`/api/v1/auth` 请求改走内网，持久化
站点 URL 保持公网地址，第三方站点请求保持原路径。内部请求禁用环境代理并
拒绝重定向，配置缺一项会阻止启动。抽奖发奖及到期倍率清理的管理员请求也使用
同一内网路由；未配置站点仍经过原有 SSRF 防护，不开放任意私网目标。

**Sub2API 和 TransitHub 必须都加入 `service-integration` 网络。** Sub2API
使用 `sub2api-internal` 网络别名。生产已把此设置写入主 compose 文件；新机器
可参考 `docker-compose.transithub-internal.yml` 叠加配置。仅执行一次
`docker network connect` 不够，下次重建会丢失网络，必须同时持久化 compose。

检测器使用固定版本镜像内的检测资源，持久化报告卷 `/data/runs`。原有代理、
请求头和中断恢复适配以五个只读单文件挂载保留在固定目录，详见检测器 README
和 `docker-compose.adapters.yml`。不要再次添加指向历史发布目录的整个 `/app`
bind mount；目录被清理会使档位预估及检测失败。
除 `/api/health` 外，发版还要用登录会话检查 `/api/purity-check/tiers` 返回
三个有效档位，以及 `/api/purity-check/targets` 可读取账号。

# Part D · 搬服务器 / 换 IP 完整清单

> **照这份做，不需要再全盘扫描一遍。** 内容于 2026-08-28 逐项实测确认。
> 该实例没绑 Elastic IP，历史上 IP 从 `13.212.118.49` 变成过 `52.76.22.21`。

## D1. 服务器上必须有的东西（含容易漏掉的依赖）

### 容器（7 个，两套 compose）

| compose | 服务 |
|---|---|
| `/opt/sub2api/deploy/docker-compose.local.yml` | `sub2api`、`postgres`、`redis` |
| `/opt/transit-hub/docker-compose.yml` | `app`、`postgres`、`redis`、`gpt56-detector` |

### 宿主机 systemd 服务（**这里最容易漏**）

| 服务 | 性质 | 说明 |
|---|---|---|
| `docker` / `containerd` | 基础 | |
| `nginx` | 基础 | 配置在 `/etc/nginx/conf.d/{sub2api,transithub}.conf` |
| `xray.service` | VPN 服务端 | 监听 8443，配置 `config.json` |
| **`xray@ar-client.service`** | **⚠️ 业务依赖，不是 VPN** | 见下方警告 |
| `transit-host-guard.service` | 构建守卫 + 看门狗 | 见 A8 |
| `certbot-renew.timer` | 证书续期 | |
| `sub2api-log-cleanup.timer` | 日志清理 | |
| `qqbot.service` | QQ 机器人 | 2026-08-28 起已停用（stop + disable） |

> ### ⚠️⚠️ `xray@ar-client` 是 sub2api 的出网代理，停了会断业务
>
> 名字里有 xray，但**它和 VPN 无关**。它跑的是 `ar-client.json`（客户端配置），
> 在 `172.18.0.1:10808`（docker 网桥）提供 SOCKS5，**sub2api 有 6 个上游账号靠它出网**：
>
> ```sql
> select p.id,p.protocol,p.host,p.port,count(a.id) from proxies p
>   left join accounts a on a.proxy_id=p.id group by 1,2,3,4;
> -- 1 | socks5 | 172.18.0.1 | 10808 | 6
> ```
>
> **2026-08-28 的教训**：被要求"把 xray 拿掉"时一并停了它，导致那 6 个账号
> 1 个半小时无法出网。事后还发现它虽然在跑但 `enabled=disabled`——**服务器一重启就会静默失效**。
>
> **搬服务器时必须部署它，并确认 `systemctl is-enabled xray@ar-client` 是 `enabled`。**

### 证书（certbot，3 张）

```
icode-xtu.ccwu.cc          icode-xtu-manage.ccwu.cc          gongfeiai.com
```

新机器上**重新签发**（`certbot --nginx -d <域名>`），别拷贝证书目录。
**签发前 DNS 必须已指向新 IP**，否则 HTTP-01 校验过不了。

## D2. 换 IP 时要改的本地文件（精确清单）

| 文件 | 改什么 |
|---|---|
| `vpn-manager/config/servers.json` | 该服务器条目的 **`host`** 和 **`publicAddress`** |
| `vpn-manager/data/servers/<id>/users/*/import-link.txt` | 连接串里的 `@<IP>:8443` 和末尾 `#<IP>-<用户名>` |
| `tools/qqbot/README.md` | `scp`/`ssh` 命令里的 `ec2-user@<IP>`、IP 白名单说明 |
| `tools/qqbot/deploy/install.sh` | 顶部注释的执行目标 |
| `sub2api/deploy/host-protection/README.md` | `$server = "<IP>"` |
| `vpn-manager/使用教程.md` | 服务器列表里的 IP |
| `E:\sub2api云服务\亚马逊aws-<旧IP>\` 目录下 | `服务器架构与迁移清单.md`、`工具/deploy-log-cleanup.sh` 的 `HOST=`、`qqbot/README.md` |

> 这个目录名本身带旧 IP，但**它是真实目录且被别处引用，不要重命名**。

## D3. 外部系统（必须人工操作，脚本改不了）

| 系统 | 要做的事 | 不做的后果 |
|---|---|---|
| **AWS 控制台** | **绑定 Elastic IP** | 每次 Stop/Start 都换 IP，全部重来一遍 |
| **DNS 服务商** | 三个域名的 A 记录改指新 IP | 站点打不开；证书续期失败 |
| **QQ 开放平台后台** | 更新机器人 **IP 白名单** | 回调全部被拒，**机器人静默失效、不报错** |
| AWS 安全组 | 确认放行 22 / 80 / 443 / 8443 | 8443 是 xray，漏了 VPN 连不上 |

## D4. 绝对不要改的地方

| 位置 | 为什么 |
|---|---|
| `vpn-manager/config/servers.json` 的 **`id`** | 是主键，关联 `data/servers/<id>/` 目录，改了丢该服务器全部用户数据 |
| `keyFile` 及密钥文件名 | 当前密钥为 `vpn-manager/keys/52.76.22.21.pem`；IP 变化后先枚举 `vpn-manager/keys/*.pem`，再同步核实配置引用，不能猜文件名 |
| `backend/migrations/*.sql` | 已执行的迁移，改了破坏一致性 |
| `commit-msg-*.txt`、`progress.md`、`*.bak-*`、`排查记录/` | 历史记录，改了就失真 |

## D5. 搬完的验证清单

```bash
# 1. 容器与服务
sudo docker ps --format '{{.Names}}: {{.Status}}'          # 7 个都 healthy
for u in docker nginx xray xray@ar-client transit-host-guard; do
  printf '%-24s %s/%s\n' $u $(systemctl is-active $u) $(systemctl is-enabled $u)
done                                                        # 都要 active/enabled

# 2. 应用健康
curl -s -o /dev/null -w 'sub2api=%{http_code}\n'    http://127.0.0.1:8080/health
curl -s -o /dev/null -w 'transithub=%{http_code}\n' http://127.0.0.1:10621/api/health

# 3. 出网代理（漏了这条 = 6 个账号静默失效）
sudo ss -tlnp | grep '172.18.0.1:10808'

# 4. 数据完好
sudo docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc 'select count(*) from accounts;'
sudo docker exec sub2api-postgres psql -U sub2api -d sub2api -tAc \
  'select filename from schema_migrations order by filename desc limit 1;'

# 5. 关键配置没丢
sudo docker inspect sub2api --format '{{range .Config.Env}}{{println .}}{{end}}' | grep ISOLATION
                                                            # 必须是 proxy，见 A6
for c in $(sudo docker ps --format '{{.Names}}'); do
  printf '%-28s ' $c; sudo docker inspect $c --format '{{.HostConfig.LogConfig.Config}}'
done                                                        # 都要有 max-size:20m

# 6. 系统级
swapon --show                                               # 必须有 swap
systemctl is-active sysstat-collect.timer                   # 出事后靠它回溯
df -h /                                                     # 别又只给 8 GB
systemctl is-active sub2api-log-cleanup.timer sub2api-artifact-cleanup.timer  # 两个磁盘保护 timer 都要 active
```

## D5.5 磁盘容量保护：发版产物自动清理

2026-09-09 高频发版（一天十几次）把 15G 根盘写满到 100%，二进制上传中途报
"No space left on device"（因为每次发版都新建二进制 + pg_dump 备份、不覆盖旧文件，
为了保留回滚点）。应急清理后又补了一个 systemd timer，往后不用再靠人工发现磁盘写满才清理：

| Timer | 频率 | 做什么 | 单元文件 |
|---|---|---|---|
| `sub2api-log-cleanup.timer` | 每天 19:00 UTC | 清 `ops_system_logs`/`ops_error_logs`/`ops_alert_events` 三张运维日志表（保留最近 3 天） | 只装在服务器上，未入库 |
| `sub2api-artifact-cleanup.timer` | 每天 19:30 UTC（错开 30 分钟避免抢资源） | `/opt/sub2api/backend/bin` 只保留最近 5 个二进制（**外加当前 compose 挂载的那个，即使排不进前 5 也强制保留**）；`/opt/sub2api/backups` 只保留最近 5 份 `*.dump`（`.csv`/`cleanup-manifest-*.txt` 等小文件不动） | 源码在 [`sub2api/deploy/maintenance/`](maintenance/)，已同步部署到服务器 |

两者都是 `Type=oneshot` + `Nice=10` + `IOSchedulingClass=idle`，不跟业务抢资源；执行日志在
`/var/log/sub2api-artifact-cleanup.log`。改保留份数：改 `/opt/sub2api/scripts/prod-artifact-cleanup.sh`
里的 `KEEP_BIN`/`KEEP_DUMP` 默认值，或者用环境变量跑
`KEEP_BIN=8 sudo /opt/sub2api/scripts/prod-artifact-cleanup.sh`。想看会删什么但不真删，加 `--dry-run`。

当前根盘仍是 gp3 15G（2026-09-09 用到 70%，11G/15G）。有了这个 timer 后 bin+backups 稳态占用
从峰值 5.5G 降到约 1.2G，短期内不会再写满，但机型建议（见 A1）里的 "gp3 30 GB 起" 仍然成立——
这台机器的发版频率下 15G 长期看依然偏紧，条件允许时应该扩容，扩容步骤见文末「排查速查」。

## D6. 新机器一次性做对的顺序

1. 选**足够的机型**（别再用 t3.small，见 A1）+ **立刻绑 Elastic IP**
2. 装包、开 `sysstat-collect.timer`、加 swap（A2/A3）
3. **先写 `/etc/docker/daemon.json` 再创建容器**（A4）——这样日志上限天然生效，
   省掉后面 `--force-recreate` 的折腾
4. 迁数据（A9），部署容器
5. 部署宿主机服务，**特别是 `xray@ar-client`**，逐个确认 `enabled`
6. DNS 切过来 → 签证书 → 更新 QQ 白名单
7. 跑一遍 D5 验证清单

---

# 附录 · 排查速查

出问题时的正确顺序（2026-08-28 实战验证）：

```bash
uptime; free -m; df -h /                       # 先看三大资源
sudo sar -f /var/log/sa/sa<日>                 # CPU 历史（%idle 长期 <1% 即为饱和）
sudo sar -r -f /var/log/sa/sa<日>              # 内存（重点看 kbcached 是否被压缩）
sudo sar -n SOCK -f /var/log/sa/sa<日>         # tcp-tw（>1000 说明连接复用失效）
sudo sar -w -f /var/log/sa/sa<日>              # cswch/s（正常几千，上万即异常）
sudo pidstat 5 3                               # 瞬时进程 CPU
```

**几个容易误判的点：**

- **`ps aux` 的 `%CPU` 是进程生命周期平均值，不是瞬时值**，会严重误导。要瞬时值用 `pidstat`
- **SSH 报 "Connection timed out during banner exchange" 但端口能连通** = CPU 被打满，
  内核能完成 TCP 握手但用户态进程拿不到时间片。不是网络问题
- **`iowait` 高不一定是磁盘慢**。内存不足时内核会强行回收 page cache，
  之后数据全要重读磁盘 → iowait 飙升。先看 `sar -r` 的 `kbcached` 有没有被压缩
- **别按容量推断卷类型和 IOPS** —— 直接查，或问清楚。gp3 基线固定 3000 IOPS 与容量无关
- **EBS 扩容后要在 OS 层补两步**，否则空间拿不到：
  ```bash
  sudo growpart /dev/nvme0n1 1 && sudo xfs_growfs /
  ```
