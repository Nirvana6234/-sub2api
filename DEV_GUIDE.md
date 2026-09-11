# sub2api 项目开发指南

> 本文档记录项目环境配置、常见坑点和注意事项，供 Claude Code 和团队成员参考。

## 一、项目基本信息

| 项目 | 说明 |
|------|------|
| **上游仓库** | Wei-Shaw/sub2api |
| **Fork 仓库** | bayma888/sub2api-bmai |
| **技术栈** | Go 后端 (Ent ORM + Gin) + Vue3 前端 (pnpm) |
| **数据库** | PostgreSQL 16 + Redis |
| **包管理** | 后端: go modules, 前端: **pnpm**（不是 npm） |

## 二、本地环境配置

### PostgreSQL 16 (Windows 服务)

| 配置项 | 值 |
|--------|-----|
| 端口 | 5432 |
| psql 路径 | `C:\Program Files\PostgreSQL\16\bin\psql.exe` |
| pg_hba.conf | `C:\Program Files\PostgreSQL\16\data\pg_hba.conf` |
| 数据库凭据 | user=`sub2api`, password=`sub2api`, dbname=`sub2api` |
| 超级用户 | user=`postgres`, password=`postgres` |

### Redis

| 配置项 | 值 |
|--------|-----|
| 端口 | 6379 |
| 密码 | 无 |

### 开发工具

```bash
# golangci-lint（CI 用 v2.13，本地建议装同一版以免版本差异带来的噪音）
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13

# pnpm (前端包管理)
npm install -g pnpm
```

## 三、CI/CD 流水线

### GitHub Actions Workflows

| Workflow | 触发条件 | 检查内容 |
|----------|----------|----------|
| **backend-ci.yml** | push, pull_request | 单元测试 + 集成测试 + golangci-lint v2.13 |
| **security-scan.yml** | push, pull_request, 每周一 | govulncheck + gosec + pnpm audit |
| **release.yml** | tag `v*` | 构建发布（PR 不触发） |

### CI 要求

- Go 版本必须是 **1.27.0**：三个 workflow 都用 `go-version-file: backend/go.mod` 取版本，随后硬断言 `go version | grep -q 'go1.27.0'`。升级 Go 时要同时改 `backend/go.mod`、`backend-ci.yml`（两处）、`release.yml`、`security-scan.yml` 里的这句断言，**以及三个 Dockerfile 里的 Go 构建镜像**（`Dockerfile` / `deploy/Dockerfile` 的 `ARG GOLANG_IMAGE`、`backend/Dockerfile` 的 `FROM golang:`）。前者漏了 CI 会在版本校验步骤直接失败；**后者漏了 CI 不会报，而是等到有人用这些 Dockerfile 构建时才失败**（`go.mod requires go >= X (running Y; GOTOOLCHAIN=local)`）。
- 前端使用 `pnpm install --frozen-lockfile`，必须提交 `pnpm-lock.yaml`

### 本地测试命令

```bash
# 后端单元测试
cd backend && go test -tags=unit ./...

# 后端集成测试
cd backend && go test -tags=integration ./...

# 代码质量检查
cd backend && golangci-lint run ./...

# 前端依赖安装（必须用 pnpm）
cd frontend && pnpm install
```

## 四、常见坑点 & 解决方案

### 坑 1：pnpm-lock.yaml 必须同步提交

**问题**：`package.json` 新增依赖后，CI 的 `pnpm install --frozen-lockfile` 失败。

**原因**：上游 CI 使用 pnpm，lock 文件不同步会报错。

**解决**：
```bash
cd frontend
pnpm install  # 更新 pnpm-lock.yaml
git add pnpm-lock.yaml
git commit -m "chore: update pnpm-lock.yaml"
```

---

### 坑 2：npm 和 pnpm 的 node_modules 冲突

**问题**：之前用 npm 装过 `node_modules`，pnpm install 报 `EPERM` 错误。

**解决**：
```bash
cd frontend
rm -rf node_modules  # 或 PowerShell: Remove-Item -Recurse -Force node_modules
pnpm install
```

---

### 坑 3：PowerShell 中 bcrypt hash 的 `$` 被转义

**问题**：bcrypt hash 格式如 `$2a$10$xxx...`，PowerShell 把 `$2a` 当变量解析，导致数据丢失。

**解决**：将 SQL 写入文件，用 `psql -f` 执行：
```bash
# 错误示范（PowerShell 会吃掉 $）
psql -c "INSERT INTO users ... VALUES ('$2a$10$...')"

# 正确做法
echo "INSERT INTO users ... VALUES ('\$2a\$10\$...')" > temp.sql
psql -U sub2api -h 127.0.0.1 -d sub2api -f temp.sql
```

---

### 坑 4：psql 不支持中文路径

**问题**：`psql -f "D:\中文路径\file.sql"` 报错找不到文件。

**解决**：复制到纯英文路径再执行：
```bash
cp "D:\中文路径\file.sql" "C:\temp.sql"
psql -f "C:\temp.sql"
```

---

### 坑 5：PostgreSQL 密码重置流程

**场景**：忘记 PostgreSQL 密码。

**步骤**：
1. 修改 `C:\Program Files\PostgreSQL\16\data\pg_hba.conf`
   ```
   # 将 scram-sha-256 改为 trust
   host    all    all    127.0.0.1/32    trust
   ```
2. 重启 PostgreSQL 服务
   ```powershell
   Restart-Service postgresql-x64-16
   ```
3. 无密码登录并重置
   ```bash
   psql -U postgres -h 127.0.0.1
   ALTER USER sub2api WITH PASSWORD 'sub2api';
   ALTER USER postgres WITH PASSWORD 'postgres';
   ```
4. 改回 `scram-sha-256` 并重启

---

### 坑 6：Go interface 新增方法后 test stub 必须补全

**问题**：给 interface 新增方法后，编译报错 `does not implement interface (missing method XXX)`。

**原因**：所有测试文件中实现该 interface 的 stub/mock 都必须补上新方法。

**解决**：
```bash
# 搜索所有实现该 interface 的 struct
cd backend
grep -r "type.*Stub.*struct" internal/
grep -r "type.*Mock.*struct" internal/

# 逐一补全新方法
```

---

### 坑 7：Windows 上 psql 连 localhost 的 IPv6 问题

**问题**：psql 连 `localhost` 先尝试 IPv6 (::1)，可能报错后再回退 IPv4。

**建议**：直接用 `127.0.0.1` 代替 `localhost`。

---

### 坑 8：Windows 没有 make 命令

**问题**：CI 里用 `make test-unit`，本地 Windows 没有 make。

**解决**：直接用 Makefile 里的原始命令：
```bash
# 代替 make test-unit
go test -tags=unit ./...

# 代替 make test-integration
go test -tags=integration ./...
```

---

### 坑 9：Ent Schema 修改后必须重新生成

**问题**：修改 `ent/schema/*.go` 后，代码不生效。

**解决**：
```bash
cd backend
go generate ./ent  # 重新生成 ent 代码（json.RawMessage 字段会生成为同类型的 jsontext.Value，属预期）
git add ent/       # 生成的文件也要提交
```

---

### 坑 10：前端测试看似正常，但后端调用失败（模型映射被批量误改）

**典型现象**：
- 前端按钮点测看起来正常；
- 实际通过 API/客户端调用时返回 `Service temporarily unavailable` 或提示无可用账号；
- 常见于 OpenAI 账号（例如 Codex 模型）在批量修改后突然不可用。

**根因**：
- OpenAI 账号编辑页默认不显式展示映射规则，容易让人误以为“没映射也没关系”；
- 但在**批量修改同时选中不同平台账号**（OpenAI + Antigravity/Gemini）时，模型白名单/映射可能被跨平台策略覆盖；
- 结果是 OpenAI 账号的关键模型映射丢失或被改坏，后端选不到可用账号。

**修复方案（按优先级）**：
1. **快速修复（推荐）**：在批量修改中补回正确的透传映射（例如 `gpt-5.3-codex -> gpt-5.3-codex-spark`）。
2. **彻底重建**：删除并重新添加全部相关账号（最稳但成本高）。

**关键经验**：
- 如果某模型已被软件内置默认映射覆盖，通常不需要额外再加透传；
- 但当上游模型更新快于本仓库默认映射时，**手动批量添加透传映射**是最简单、最低风险的临时兜底方案；
- 批量操作前尽量按平台分组，不要混选不同平台账号。

---

### 坑 11：PR 提交前检查清单

提交 PR 前务必本地验证：

- [ ] `go test -tags=unit ./...` 通过
- [ ] `go test -tags=integration ./...` 通过
- [ ] `golangci-lint run ./...` 无新增问题
- [ ] `pnpm-lock.yaml` 已同步（如果改了 package.json）
- [ ] 所有 test stub 补全新接口方法（如果改了 interface）
- [ ] Ent 生成的代码已提交（如果改了 schema）

---

### 坑 12：本地启动必须区分首次安装和日常启动

本地开发固定使用以下数据目录：

```text
C:\Work\Git\AI-Fly\-sub2api\.local\sub2api-data
```

**日常启动顺序**

1. 确认 PostgreSQL `5432` 和 Redis `6379` 已启动。
2. 在独立 PowerShell 窗口启动后端：
   ```powershell
   cd C:\Work\Git\AI-Fly\-sub2api\backend
   $env:DATA_DIR="C:\Work\Git\AI-Fly\-sub2api\.local\sub2api-data"
   go run ./cmd/server/
   ```
3. 在独立窗口启动管理后台：
   ```powershell
   cd C:\Work\Git\AI-Fly\-sub2api\frontend
   pnpm.cmd dev --host 127.0.0.1 --port 3000
   ```
4. 启动 Chat 桌面端：
   ```powershell
   cd C:\Work\Git\AI-Fly\-sub2api\tools\chat
   npm.cmd run app:dev
   ```

Chat 独立页面必须在 `tools/chat/.env.local` 中配置：

```env
PAW_SERVICE_URL=http://127.0.0.1:8080
```

修改后必须重启 Chat 开发进程。

**启动检查**

- 后端：`http://127.0.0.1:8080/health` 返回 `{"status":"ok"}`。
- 安装状态：`http://127.0.0.1:8080/setup/status` 返回 `needs_setup: false`。
- 管理后台：`http://127.0.0.1:3000`。
- Chat 页面：由 `npm.cmd run app:dev` 输出的本地地址为准，通常是 `http://127.0.0.1:3100`。

**管理员和数据库规则**

- 管理员账号存储在 PostgreSQL 的 `users` 表中，不会从 `config.yaml` 的 `default.admin_email` 或 `default.admin_password` 自动创建。
- 日常启动时必须保留 `DATA_DIR`、`config.yaml` 和 `.installed`；不要删除或移动它们。
- 不要为了绕过迁移错误直接删除或重建 `sub2api` 数据库。这样会删除管理员、渠道、设置和业务数据。
- 迁移 checksum 不一致时，恢复被修改的迁移文件，或创建新的迁移文件；不要修改已应用迁移。
- 只有全新安装或明确的本地恢复才使用 setup 向导。空库需要通过向导重新创建管理员，不能只保留已有 `config.yaml` 后直接启动。

---

### 坑 13：Paw 分组或模型显示“当前选择已失效”

**典型现象**：
- Paw 能登录，但聊天区提示“当前选择已失效，请重新选择分组或模型”；
- `/api/v1/paw/config` 返回 200，但 `groups` 为空或分组没有模型；
- 后台可以看到用户 API Key 已绑定分组。

**根因**：
- Paw 配置接口不能用历史 `paw_defaults` 做严格校验，否则旧默认值会阻断当前分组和模型加载；
- 部分本地数据只有分组的 `models_list_config`，没有 `channels` 记录；如果配置服务无渠道就直接跳过分组，前端只能得到空列表；
- 修改 Go 源码后如果没有重新构建并重启 `.local/sub2api-paw-server-next.exe`，8080 仍然运行旧逻辑。

**正确行为**：
- `/api/v1/paw/config` 使用“可用配置”读取路径，保留历史默认值供前端判断，但不因默认值失效而返回错误；
- 有渠道时使用渠道映射和定价模型，无渠道时回退到分组 `models_list_config`，没有自定义列表时再使用服务端模型目录；
- Paw 已有选择失效时清空选择并提示用户重新选择，不自动切换到其它分组或模型。

**源码修改后的验证**：
```powershell
cd backend
go test ./internal/service -run PawConfig -count=1
go test ./internal/server/routes -run PawConfig -count=1
go build -o ..\.local\sub2api-paw-server-next.exe .\cmd\server
```
然后停止旧进程并重新启动后端，再重新加载 `http://127.0.0.1:3101/`。

### 坑 14：OpenAI 字节保真透传"开了但没生效"

**典型现象**：账号上勾了「字节保真模式」，但上游看到的请求和普通自动透传没区别——
没有 zstd、请求头仍按白名单裁剪。

**先确认一件事：这可能根本不是故障。** strict 是**逐请求**判定的：

- 这条请求判定为官方 Codex 客户端发出 → 走 strict；
- 不是 → **回落到普通自动透传，照常服务**。

所以同一个账号上，Codex CLI 的请求走 strict、网页/curl/SDK 的请求走 auth_only，
是设计内的正常行为，不是配置错了。

**判定条件**（与 codex_cli_only **完全独立**，那个开关是"不是 Codex 就 403"，
这个是"不是 Codex 就降级"，可以单开、也可以都开）：

1. 官方 UA 前缀 / 官方 originator / 全局白名单 / app-server 客户端，四取一；
2. 官方候选还要过版本门（`codex_cli_only_min/max_version`）；
3. 再过引擎指纹门。**默认种子只勾了 `header_prefix: x-codex-`，是必需项**——
   光有官方 UA、不带任何 `x-codex-*` 头，判定不过。
4. 全局黑名单命中即拒。
5. `force_codex_cli` **不算数**：那是无条件放行，证明不了来路。

**排查顺序**：

1. 看访问日志（`http request completed`，`component: http.access`）里的 `passthrough_mode`
   字段：`strict` / `auth_only` / `off`。**2026-09-11 之前这个字段不存在**——
   `ops_openai_passthrough_mode` 原来只 `c.Set` 进 gin.Context，从没有任何代码把它读出来
   落地到日志、指标或任何持久化的地方，纯粹写入、没人读。是排查一个真实账号时发现日志里
   一条相关记录都没有才补上的（`internal/server/middleware/logger.go`）。旧版本二进制
   上这个字段查不到，不代表 strict 没生效，只代表看不出来。
2. 不是 `strict` 就看同一条日志的 `strict_degraded_reason` 字段：
   - `account_strict_disabled` —— 账号没开，或没开父级的自动透传；
   - `client_gate_not_matched` —— **这条请求不是 Codex 发的**。绝大多数情况就到此为止，
     属正常。真要让它过，对着上面 1–4 逐条比；
   - `client_gate_not_evaluated` —— 判定压根没跑，属代码路径缺陷。**只有这一种会打 WARN**
     （`openai.passthrough_strict_degraded`），该查调用链。

**已知的不生效场景（都是预期行为）**：WebSocket 入站走的是 upgrade 请求自己的
gin.Context，永不流经 `Forward`，因此 WS 路径上 strict 恒为关闭。

**开启 strict 的部署建议同时收紧引擎指纹门**（全局设置
`codex_cli_only_engine_fingerprint_signals`，纯配置、不用改代码）：把默认列表里那两条
`body_path` 信号也设为必需——`client_metadata.x-codex-window-id`、
`client_metadata.x-codex-installation-id`。默认种子只看头；而 strict 的效果正是把 body
原样送上去，body 形状才是真正的风险面。头装得像、body 却是个 chat/completions 翻译产物，
那才会给账号招来上游的注意。

**一条有意接受的残留风险**：判定依据里的 UA / originator 是客户端自报的，可以伪造。
伪造的后果是该请求的头按黑名单放行、body 原样上送；凭据与身份类头仍然一律剥除。
2026-09-11 与需求方确认后接受，不是疏漏。

## 五、常用命令速查

### 数据库操作

```bash
# 连接数据库
psql -U sub2api -h 127.0.0.1 -d sub2api

# 查看所有用户
psql -U postgres -h 127.0.0.1 -c "\du"

# 查看所有数据库
psql -U postgres -h 127.0.0.1 -c "\l"

# 执行 SQL 文件
psql -U sub2api -h 127.0.0.1 -d sub2api -f migration.sql
```

### Git 操作

```bash
# 同步上游
git fetch upstream
git checkout main
git merge upstream/main
git push origin main

# 创建功能分支
git checkout -b feature/xxx

# Rebase 到最新 main
git fetch upstream
git rebase upstream/main
```

### 前端操作

```bash
# 安装依赖（必须用 pnpm）
cd frontend
pnpm install

# 开发服务器
pnpm dev

# 构建
pnpm build
```

### 后端操作

```bash
# 运行服务器
cd backend
go run ./cmd/server/

# 生成 Ent 代码
go generate ./ent

# 运行测试
go test -tags=unit ./...
go test -tags=integration ./...

# Lint 检查
golangci-lint run ./...
```

## 六、项目结构速览

```
sub2api-bmai/
├── backend/
│   ├── cmd/server/          # 主程序入口
│   ├── ent/                 # Ent ORM 生成代码
│   │   └── schema/          # 数据库 Schema 定义
│   ├── internal/
│   │   ├── handler/         # HTTP 处理器
│   │   ├── service/         # 业务逻辑
│   │   ├── repository/      # 数据访问层
│   │   └── server/          # 服务器配置
│   ├── migrations/          # 数据库迁移脚本
│   └── config.yaml          # 配置文件
├── frontend/
│   ├── src/
│   │   ├── api/             # API 调用
│   │   ├── components/      # Vue 组件
│   │   ├── views/           # 页面视图
│   │   ├── types/           # TypeScript 类型
│   │   └── i18n/            # 国际化
│   ├── package.json         # 依赖配置
│   └── pnpm-lock.yaml       # pnpm 锁文件（必须提交）
└── .claude/
    └── CLAUDE.md            # 本文档
```

## 七、参考资源

- [上游仓库](https://github.com/Wei-Shaw/sub2api)
- [Ent 文档](https://entgo.io/docs/getting-started)
- [Vue3 文档](https://vuejs.org/)
- [pnpm 文档](https://pnpm.io/)
