# 共飞 AI 工作台 —— 开发计划

> 配套文档：[`共飞AI工作台-总体规划.md`](共飞AI工作台-总体规划.md)（要做什么、为什么这么定）
> 本篇只讲**怎么做、按什么顺序、每步做完算什么**。
> 日期：2026-09-02

---

## 0. 起点与落位

**直接在 [`tools/chat`](../../chat) 上开工**，不新建工程。

```
tools/chat/
├─ app/ src/                     现有前端（登录与通讯层已可用），新增 agent 会话面
├─ src-tauri/                    现在 7 行的壳 → 演进为 agent 宿主
│  ├─ src/                       宿主：进程笼、生命周期、IPC 命令与事件桥
│  └─ crates/
│     └─ codex-adapter/          【新】唯一说 codex 协议的 crate，无 Tauri 依赖、可单测
└─ scripts/                      现有打包脚本 + 新增 codex 分发与升级闸门
```

**为什么 `codex-adapter` 放在 `src-tauri/crates/` 而不是 `tools/`**：它目前只有一个消费者。等真出现第二个（比如另一个客户端）再上移，不为想象中的复用先付结构成本。**但它自己必须干净**：不引用 Tauri、不认识 UI、可脱离壳跑测试。

---

## 1. 第 0 步：先把 Tauri 升到 2（阻塞项）

**现在迁移几乎零成本，越往后越贵。** 证据：

- `src-tauri/src/main.rs` **只有默认的 7 行**，没有任何自定义命令；
- 前端**一次 `@tauri-apps/api` 都没调用**（`package.json` 里只有 `@tauri-apps/cli` 这个 devDependency，`withGlobalTauri: false`）；
- 网络走的是普通 `fetch`（`pawRequest`），`http-all` 那条 allowlist 实际上没被用。

也就是说今天的迁移面**只有配置**。而我们马上要加的东西——sidecar / 进程管理、文件系统、托盘、IPC 命令——**恰好都是 Tauri 2 权限模型改动最大的部分**。先迁，再动手。

**T0 验收**：Tauri 2 下 `app:dev` 与 `app:build` 都通，桌面版能登录、能对话（回到今天的功能水位），CI 能产出安装包。

### T0 执行记录（2026-09-02）

**配置迁移已完成，且已用官方 schema 校验通过。** 改动只有四个文件：

| 文件 | 改动 |
|---|---|
| `src-tauri/tauri.conf.json` | 重写为 v2 结构：`identifier`/`productName`/`version`/`bundle` 提到顶层，`tauri.*` → `app.*`，`devPath`→`devUrl`、`distDir`→`frontendDist`、`withGlobalTauri` 移入 `app`；**allowlist 整块删除** |
| `src-tauri/Cargo.toml` | `tauri` 1.5→2.11、`tauri-build` 1.5→2.6，`rust-version` 1.60→**1.77.2**（v2 的 MSRV），删掉 v2 已不存在的 `custom-protocol` feature |
| `src-tauri/capabilities/default.json` | 新增，只有 `core:default` |
| `src-tauri/.gitignore` | 加 `/gen/schemas` |

`src/main.rs`（7 行）与 `build.rs` **一字未改**，在 v2 下本来就合法。`package.json` 里 `@tauri-apps/cli` → `^2.11.4`。

**不迁移 allowlist，一个插件都不加。** 原 v1 的 `http-all` / `shell-open` / 八个 window 权限**全是死的**——前端从未 import `@tauri-apps/api`，`withGlobalTauri: false`，也没有 `data-tauri-drag-region`。所以 v2 侧不引 `tauri-plugin-http`、不引 `tauri-plugin-shell`，`capabilities/default.json` 只有 `core:default`。以后要什么权限，显式加在这个文件里——这正是 v2 权限模型的用法。

**`dangerousUseHttpScheme` 是 v1 配置里唯一不是死的一行，v2 里正确做法是「不写」。** v2 把它改名并反转成了 per-window 的 `useHttpsScheme`，**默认 `false`**，即 Windows 上默认就是 `http://tauri.localhost`——和 v1 开着 `dangerousUseHttpScheme` 得到的行为一致（服务端是 `http://`，webview 若跑在 `https://` 源上会被混合内容拦掉）。所以**保持默认、不要写这个字段**；哪天真去写了，等于同时换掉 IndexedDB / cookie / localStorage 的存储位置。

**已验证**：
- `tauri.conf.json` 对 `@tauri-apps/cli@2.11.4` 自带的 `config.schema.json` **完整 JSON-Schema 校验通过**；
- `npx tauri info` 正确解析新配置（`tauri 🦀: 2.11`、frontendDist / devUrl / CSP 均识别正确）；
- `npm run export:desktop` 通过，`out/` 产物正常（`frontendDist` 指向它）；
- **`cargo check` 通过**（tauri 2.11.5 / tauri-build 2.6.3，Rust 1.98.0 msvc，6m13s 冷编译，零 warning）。`tauri-build` 顺带生成了 `gen/schemas/`，其中 `capabilities.json` 证明能力文件被正确读取（`windows: ["main"]`、`permissions: ["core:default"]`），`capabilities/default.json` 也对生成出来的 `desktop-schema.json` 校验通过。

### T0 闸门：已解决

**① Rust 工具链 —— 已装好。** `MSVC v143 14.43.34808`（VS Installer 里单勾一个组件，装在 D 盘 Community 17.13 上；Win11 SDK 22621 本来就完整）+ `Rust 1.98.0 msvc` + clippy/rustfmt。

两个坑记一下，都会在别的机器上重演：

- **`static.rust-lang.org` 对 rustup 自己不通**（`tls handshake eof`），但同一 URL 用 `Invoke-WebRequest` 是 200 —— TLS 栈差异，不是网络不通。已持久化用户级 `RUSTUP_DIST_SERVER=https://rsproxy.cn` / `RUSTUP_UPDATE_ROOT=https://rsproxy.cn/rustup`。
- **TUNA 镜像不能用**：channel toml 取得到，实际 tarball **403**。
- 反过来 **cargo 自己不需要镜像**：`index.crates.io`（sparse index）和 `static.crates.io` 直连都正常，没动 `~/.cargo/config.toml`。

`npx tauri migrate` 依旧跑不了（要 `cargo metadata`），但配置已经是 v2、且过了 schema 全量校验，**不要再补跑**。

**② 编译与打包 —— 都通了。**

| | 结果 |
|---|---|
| `cargo check` | exit 0 |
| `npm run app:dev` | 355 crate / **2m10s**，`chat.exe` 起窗，登录已实测通过 |
| `npm run app:build` | release **4m08s** → `chat.exe` 10 MB；MSI 4.6 MB + NSIS setup 3.6 MB |
| 正式包实测 | **登录 + 发消息通过**（`http://tauri.localhost` origin，2026-09-02 用户实测） |

`gen/schemas/capabilities.json` 里能读到 `default` 这条被**按写的解析**了（`windows:["main"]` + `permissions:["core:default"]` + `local:true`）。注意光看「文件生成了」不够 —— 那只证明 tauri-build 跑了；`windows` 写错导致匹配不到任何窗口，生成的文件看起来是一样的。

**T0 至此闭环。** 唯一没验到的是显式 origin 白名单那条（见下），因为本地后端 CORS 是通配。

路上踩到两个**与迁移无关、但会在 CI 上必炸**的东西：

- **[build-tauri.mjs](../../chat/scripts/build-tauri.mjs) 在 Node ≥ 20.12 上必挂**：`spawnSync npx.cmd EINVAL`。CVE-2024-27980 之后 Node 不再允许直接 spawn `.cmd`/`.bat`。**已修**（Windows 上加 `shell: true`）。这脚本大概从写出来就没在新 Node 上跑过。
- **WiX 下载抖了一次**（`failed to bundle project: io: unexpected end of file`）。手动下同一个 zip 是 39 MB / 3.8 秒，NSIS 那两个文件后来也自己下成功了 —— 所以**是抖动不是墙**，重跑通常就好。

### 打包工具链：CI 要知道的事

Tauri v2 首次打包会现拉工具，缓存在 `%LOCALAPPDATA%\tauri\`。版本与哈希是**从 CLI 二进制里读出来的**（`@tauri-apps/cli` 2.11.4）：

| 工具 | 来源 | 落地 |
|---|---|---|
| WiX 3.14.1 | `wixtoolset/wix3` releases | `%LOCALAPPDATA%\tauri\WixTools314` |
| NSIS 3.11 | `tauri-apps/binary-releases` | `%LOCALAPPDATA%\tauri\NSIS` |
| `nsis_tauri_utils.dll` v0.5.3 | `tauri-apps/nsis-tauri-utils` | NSIS 的 `Plugins\x86-unicode\additional\` |

**两个逃生口**（同样是从二进制里读到的，官方文档没怎么写）：

- `TAURI_BUNDLER_TOOLS_GITHUB_MIRROR` / `TAURI_BUNDLER_TOOLS_GITHUB_MIRROR_TEMPLATE` —— 把这三个 GitHub 下载整体改道镜像；匹配的是 `https://github.com/([^/]+)/([^/]+)/releases/download/([^/]+)/(.*)`。**CI 在受限网络里跑就靠它**，别去手工摆目录。
- `bundle.useLocalToolsDir: true` —— 工具缓存改放项目 `target/` 下，便于随构建缓存一起复用。

哈希对不上时 Tauri 会自己重下（`NSIS directory contains mis-hashed files. Redownloading them.`），所以手工预置的目录必须**完全**匹配，否则等于白做 —— 又一个"优先用镜像变量"的理由。

### 桌面壳的 origin：已确认

不再是推测。从编译出的 `chat.exe` 里直接读到 Tauri 自己拼 origin 的那段：

```js
`${protocolScheme}://${protocol}.localhost/${path}`   // Windows / Android
: `${protocol}://localhost/${path}`                   // macOS / Linux
```

二进制里 `://tauri.localhost` 与 `tauri://localhost` 两个字面量同时存在。结合 `useHttpsScheme` 默认 `false`：

- **Windows：`http://tauri.localhost`**
- **macOS / Linux：`tauri://localhost`**

**注意 `app:dev` 验不到这个**：dev 模式下 webview 加载的是 `devUrl`，origin 是 `http://127.0.0.1:3100`。只有走 `frontendDist` 的正式包才用自定义协议。所以"dev 能登录"不构成对 CORS 那条的验收。

### 交给部署侧的前置项（不属于 T0 验收）

[backend/internal/server/middleware/cors.go](../../../backend/internal/server/middleware/cors.go) 是**精确字符串匹配 + 默认空表 + 预检不在表里直接 403**，Paw 请求带 `Authorization`，必然触发预检。

- 本地 dev 无事：`.local/sub2api-data/config.yaml` 是 `["*"]`；
- **`deploy/config.example.yaml` 默认 `[]`**，线上不加就登不上：

```yaml
cors:
  allowed_origins:
    - "http://tauri.localhost"   # Windows 桌面壳
    - "tauri://localhost"        # macOS / Linux 桌面壳
```

`allow_credentials: true` 与 `"*"` 互斥（中间件会自动关掉 credentials），所以线上走显式列举。上面两个值来自二进制字面量；**首次上线时仍建议在服务端把第一个预检的 `c.GetHeader("Origin")` 打一次日志核对** —— 观测值永远比推导值可靠。

### 已知但不阻塞

`npm run dev` 渲染 `/` 时会抛一次 `TypeError: Cannot read properties of undefined (reading 'run')`（Next 的 `app-page.runtime.dev.js`，AsyncLocalStorage 那套），随后 `GET / 200` 正常、页面可用。**判断是 Node 24 与 Next 14.2 的版本错配**（Next 14 早于 Node 22/24），与 Tauri 迁移无关 —— 迁移没碰任何前端文件。**未隔离验证**，升 Next 15 时一并处理。

---

## 2. 两条线，可并行

| | A 线：agent 能力 | B 线：账号与个人中心 |
|---|---|---|
| 依赖 | T0 | **无**（纯前端 + 现成后端接口） |
| 谁能做 | 要 Rust | 只要前端 |
| 对应里程碑 | M0 → M1b | M1a |

**B 线从第一天就能开工**，不必等 A 线。

---

## 3. A 线任务分解

### A1 — `codex-adapter`：类型与传输

- 用 `codex app-server generate-json-schema` / `generate-ts` 的产物**挑最小子集**（不是全量 87+68+10 个方法）：
  `initialize`、`account/login/start`、`thread/start`、`thread/resume`、`thread/list`、`thread/archive`、`turn/start`、`turn/interrupt`；
  通知取 `thread/started`、`thread/status/changed`、`turn/started`、`turn/completed`、`item/started`、`item/completed`、`item/agentMessage/delta`、`item/reasoning/*`、`item/commandExecution/*`、`error`、`serverRequest/resolved`；
- JSONL over stdio 的收发、请求/响应配对、并发安全；
- **错误分类**（照 paseo-adapter 的经验，但代码重写）：进程不在 / 协议不认 / 鉴权失败 / 目录非法 / 模型侧错误（如 401）要分得开。

**验收**：用录制的报文做单测；`cargo test` 绿。不需要真进程。

#### A1 执行记录（2026-09-02）—— **已完成**

落位 `tools/chat/src-tauri/crates/codex-adapter/`（`src-tauri` 因此成为 cargo workspace，`members = ["crates/*"]`）。`cargo test` **11 绿**，workspace clippy 零 warning。

**为什么不复用上游的 crate。** 查过了：`codex-app-server-protocol` 在 crates.io 上**只有一个 0.63.0**（单次发布，而 CLI 已到 0.144.2），上游 workspace 版本号是 `0.0.0` —— 他们根本不按 crate 发版，只发二进制。而 `codex-app-server-client`（上游自带客户端）**没发布**，且依赖 `codex-core` + `codex-app-server`，用它等于把整个 agent 拖进我们的构建。
但真正决定性的理由不是这些：**真值来源是我们 spawn 的那个二进制，不是任何 repo tag。** 打了 tag 的 git 依赖给的是那个 tag 的源码，而实际跑的 `codex.exe` 未必对应任何 tag。所以 fixture 从真进程录、schema 从真二进制导，比钉 tag 更能抓漂移。

**目录结构**：

| | 作用 |
|---|---|
| `protocol/0.144.2/` | 从那个二进制 `generate-json-schema` 导出的 39 个 schema |
| `scripts/capture-fixtures.py` | 对着真进程录报文的工具，bump 版本时重跑 |
| `tests/fixtures/*.jsonl` | 三份真实录制（正常轮+审批拒绝 / 无效凭据 / 非法目录），API key 已脱敏 |
| `src/protocol.rs` | 最小子集的类型与投影，**每条通知都保留 `raw`** |
| `src/transport.rs` | JSONL 收发、请求响应配对；架在 `AsyncRead`/`AsyncWrite` 上，**不认识进程** |
| `src/error.rs` | 错误分类 |

**收发面刻意不 spawn 进程**：这样单测能拿录制报文喂 `&[u8]` 跑完整条链路，A4 再把真 `ChildStdin`/`ChildStdout` 接进来，这一层不用改。

##### 三条只有跑真进程才能发现的事

**① 上游错误（含 401）走的是 `error` 通知，不是 JSON-RPC error 响应。**
实测：拿一个无效 API key 发一轮，`turn/start` **返回成功**，401 随后以一串通知到达：

```json
{"method":"error","params":{
  "error":{"message":"Reconnecting... 1/5",
           "codexErrorInfo":{"responseStreamDisconnected":{"httpStatusCode":401}},
           "additionalDetails":"...INVALID_API_KEY..."},
  "willRetry":true,"threadId":"…","turnId":"…"}}
```

三个后果，每个都会咬人：
- **只盯请求响应做错误处理，会把整类上游错误漏掉。**
- 文案在 `params.error.message`，**不在** `params.message`（我第一版就读错了层级）。
- 有 `httpStatusCode` 这个**结构化**信号，别去匹配文案；还有 `willRetry` —— 「Reconnecting… 1/5」**不是终态**，UI 该显示「正在重试」而不是「登录失效」。

**② codex 不校验 `cwd`。** `thread/start` 传一个根本不存在的目录，会话照样建起来、`thread/started` 照发。所以**目录合法性只能宿主自己在起会话前验**（A8），指望上游报错等于没验。`AdapterError::InvalidPath` 因此改成只由我们自己产生。

**③ `availableDecisions` 不是合法值全集。** 录到的那次只给了 `accept` / `acceptWithExecpolicyAmendment` / `cancel`，但 schema 里 `decline` 一直合法，我们发过去也确实生效。它是**给 UI 的显示提示**，不是校验依据。
顺带把两个否定选项的区别钉死：**`decline` = 拒绝但这一轮继续**（agent 会自己说明做不了），**`cancel` = 拒绝并立即中断整轮**。A7 的审批 UI 要给两个按钮。

##### 两个编码陷阱（写错会被静默拒绝）

- `AskForApproval` 是 **kebab-case**（`untrusted` / `on-request` / `never`），**没有 `on-failure`**（老版本有过）。我第一版写成 camelCase，是「拿我们拼的参数对比真实发出去的字节」那条测试抓出来的。
- 同一个「沙箱」概念有两种编码：`thread/start` 的 `sandbox` 用 kebab 的 `SandboxMode`，`turn/start` 的 `sandboxPolicy` 用 camel 的 `SandboxPolicy`。别混用。

##### 遗留

`item/fileChange/requestApproval` 与 `item/permissions/requestApproval` **没有录制报文**（这轮场景没触发）。类型是照 schema 写的，A3 要补录这两个场景 —— 权限那个的响应形状和另外两个**完全不同**（`{permissions, scope, strictAutoReview}`，没有 `decision` 字段）。

### A2 — `codex-adapter`：会话面

起会话 → 发提问 → 收流 → 停止 → 续跑（`thread/resume`）。事件投影成我们自己的类型，**保留 `raw`**（本机链路要能显示上游新事件，§2.2 的第 3 条）。

**验收**：对**真 codex 进程**跑通一轮，拿到 assistant 文本与 `turn/completed`。

#### A2 执行记录（2026-09-02）—— **已完成**

`src/session.rs`。**验收超额**：除了要求的一轮对话，打断与续跑也都对真进程验过了。

| 端到端测试（真 codex 0.144.2 + 真中转站） | 结果 |
|---|---|
| `runs_a_real_turn_and_gets_assistant_text` | 拿到 `PONG`，`Completed("completed")`，16 事件 |
| `resumes_a_thread_and_keeps_talking` | 续跑后答出先前记的数字，上下文没丢 |
| `interrupts_a_running_turn` | 打断后 13.8s 收束（原本要数到 500） |

端到端全部挂 `#[ignore]`：普通 `cargo test` 会显示 `3 ignored`，**不会出现「一个真进程测试都没跑却全绿」**。真跑用 `cargo test --test e2e -- --ignored`，环境变量缺了直接报错说明缺哪个，不静默跳过。

##### 会话层替上层记住两件它总会忘的事

- **`turn/interrupt` 必须带 `turnId`**，而 turnId 从两条路各来一次（`turn/start` 响应、`turn/started` 通知），**通知可能先到**。谁先到算谁的，否则用户早早点停止会打空。
- `current_turn()` 是**「此刻」**的状态，不是「消费者读到哪」的状态：翻译任务会跑在事件消费者前面。对「停止」这正是想要的语义（停的是此刻真在跑的那轮），但**别拿它判断某个事件属于哪一轮** —— 那要用事件自带的 `turn_id`。这条是被自己的测试撞出来的。

##### 又两个只有跑真进程才知道的事

**① 被打断的一轮仍然走 `turn/completed`，只是 `status` 是 `"interrupted"`** —— 不是 `turn/failed`。所以「跑完了」和「被停掉了」只能靠这个字符串分辨（`TurnStatus::is_success()` / `is_interrupted()`）。保留原始字符串而不是穷举枚举，因为上游还会加状态。

**② 打断已经结束的轮次，上游回 `-32600 "no active turn to interrupt"`。** 也就是说 codex 会**用标准 JSON-RPC 码表达语义错误**（-32600 本义是 Invalid Request）。所以不能一看到标准码就当协议漂移处理。
这个是被第一版测试撞出来的：当时用「等 turnId 出现后固定睡 3 秒」来选打断时机，结果那一轮在 3 秒里就跑完了。改成**看到第一个 assistant 增量就立刻打断** —— 那是「确实正在产出」的最早证据。**别用睡眠猜时机。**

##### 只等 `turn/completed` 会挂死

`drive_turn` 的终止条件是 `turn/completed` / 不再重试的错误 / **连续重试超限**。第三条是我们自己的护栏：`bad-credentials` 那份录制里，鉴权失败时上游一直 `Reconnecting... n/5`，`turn/completed` **不会来**。上游最终会不会自己收束、以什么形式收束，我们没观测到，所以按次数兜底。重试通知会**透出给调用方**而不是在等待期间被吞掉 —— 用户要看到「正在重试」。

##### 顺手补上的漂移闸门

新增 `tests/params_schema.rs`：把**每一个**我们会发的方法的参数，对着 `protocol/0.144.2/` 里的 schema 校一遍（必填字段在不在、有没有 schema 不认识的键）。

原因是 A1 那条「比对真实发出去的字节」只能覆盖**录制里恰好出现过**的方法，而这正是两个 bug 溜过去的原因：**`turn/interrupt` 漏了必填的 `turnId`**、**`thread/list` 把 `limit` 写成了 `pageSize`**。两个都编译得过、跑得起来、会被服务端静默拒绝。新闸门不用真跑调用就能抓到，并且自带一条反向测试（故意写错必须被抓住），免得校验逻辑本身失灵还一直绿。

**新增方法必须在 `all_requests()` 里加一行**，有一条测试专门盯着别漏。

### A3 — `codex-adapter`：审批面

三类 `requestApproval`（commandExecution / fileChange / permissions）的请求与响应、`serverRequest/resolved` 广播、`waitingOnApproval` 状态透出。

**验收**：**复现探针脚本的结论**——`decline` 后命令未执行、文件未生成、turn 正常收束。这条已经在 2026-09-02 用 0.144.2 验过，A3 只是把它变成产品代码 + 常驻测试。

#### A3 执行记录（2026-09-03）—— **已完成**（`permissions` 一类除外，见下）

**验收达成**（真进程）：被问审批 1 次 → 拒绝 → 2 个 item 标为 `declined` → **磁盘上文件确实没有** → 整轮 `Completed`。报文说「declined」是一回事，文件系统同意是另一回事，所以这条端到端非跑不可。
新增 `tests/approval.rs`（回放）与 `e2e.rs` 里的 `declining_actually_prevents_the_side_effect`（真进程）。全套 **25 绿 + 4 ignored**，clippy 零 warning。

##### 「答复」这件事比想象中危险得多

10 种服务端请求里，**只有 4 种是 `{decision}` 形状**。而且——

**① 同一个「拒绝」，对不同方法要说不同的词。**

| | `item/*/requestApproval`（v2） | `execCommandApproval` / `applyPatchApproval`（旧） |
|---|---|---|
| 同意 | `accept` | `approved` |
| 本会话同意 | `acceptForSession` | `approved_for_session` |
| 拒绝（本轮继续） | `decline` | `denied` |
| 拒绝并中断整轮 | `cancel` | `abort` |

拿一套的词答另一套会被拒。

**② 权限申请那一类根本没有「拒绝」这个值** —— 响应是 `{permissions, scope, strictAutoReview}`，拒绝＝授一个**空档案**。

**③ 有两种请求我们根本答不上来**：`attestation/generate` 要一个证书 token、`account/chatgptAuthTokens/refresh` 要 ChatGPT 的 access token。这两个只能回 **JSON-RPC 错误**，编一个假值送上去比说「我不行」糟得多。

所以答复不再是「传一个 `Value`」，而是 `ServerRequest::approve()` / `deny()` / `grant_permissions()` 生成一个 [`Answer`]，`Answer` **只能从那条请求本身拿到** —— 拿甲类的响应去答乙类在类型上就写不出来（和 `WorkspaceDir` 同一个手法）。`deny()` 是**全函数**：每一类请求（含我们不认识的）都有一个它自己形状的「不」。这一点要紧，因为**任何一条服务端请求不答复，整轮就永远卡着**。

之前 `drive_turn` 对所有非 permissions 的请求一律回 `{"decision":"decline"}`，包括 `item/tool/call`、`mcpServer/elicitation/request` 这些形状完全不同的 —— 那是个真 bug，已修。

##### 审批请求是**指针**，不是载荷

实测 `item/fileChange/requestApproval` 的参数：

```json
{"threadId":"…","turnId":"…","itemId":"call_rHSz…","startedAtMs":…,
 "reason":null,"grantRoot":null}
```

**没有 diff、没有路径、reason 是 null。** 光靠这条请求，审批 UI 没有任何东西可以显示给用户。

真正「改了什么」在同 `itemId` 的 `item/started` 里：

```json
{"item":{"type":"fileChange","id":"call_rHSz…",
  "changes":[{"path":"…\\NOTES.md","kind":{"type":"add"},"diff":"hello\n"}],
  "status":"inProgress"}}
```

**所以 A7 的审批 UI 必须按 `itemId` 关联回查**，否则就是让用户在看不见内容的情况下点同意。为此把 item 事件从「没投影」提升为一等事件（`SessionEvent::ItemStarted` / `ItemCompleted`，带完整 `item`）—— 之前它们和未知通知混在一起，是错的。

顺带：拒绝之后 `item/completed` 的 `status` 变成 `"declined"`，这是**逐项**的生效证据，比整轮状态精确。

##### 仍未验证：`item/permissions/requestApproval`

**没能触发。** 让 agent 申请网络与工作区外读权限时，它转而去试 shell 命令，走的是 `item/commandExecution/requestApproval`。按约定只试一次就停，没有继续猎奇。

所以这一类的字段名、可空性、以及 `deny()` 给的空档案答复**都还没有实测报文佐证**，代码里和 `tests/fixtures/manifest.json` 里都标了。哪天真触发了，先录 fixture 再回来改。

##### 测试提问要明确到具体动作

第一版端到端用「创建一个文件…」这种含糊说法，结果**一次审批都没触发**（agent 自己绕过去了），测试等于什么都没验还显示失败。换成录 fixture 时验证过必然触发的那句明确命令后才稳定。**别让含糊的提问决定测试覆盖了什么。**

### A4 — 宿主：进程生命周期与笼

- spawn `codex-app-server`（或 `codex app-server`），私有 `CODEX_HOME`（**放应用数据目录，不能放系统 temp**——temp 下 codex 会拒绝建 PATH 别名并告警）；
- **Job Object 进程笼绑到 Chat 进程本身**；
- 健康探测、退避重启、有序停止。

**验收**：`turn` 进行中**强杀 Chat**，任务管理器里**没有残留 codex 进程**。这条是 D-4 那句「关掉 Chat 就够不着」的唯一实证。

#### A4 执行记录（2026-09-03）—— **已完成**

**验收原文照做并通过**：真 codex（pid 37168）**正在产出**时 `TerminateProcess` 掉宿主 → codex 随之消失。全工作区 **34 绿 + 5 ignored**，clippy 零 warning。

落位新 crate `crates/codex-host/`，**不引 Tauri**（和 `codex-adapter` 一样能脱开壳单测）。A5 的 IPC 桥才是唯一要碰 Tauri 的地方。

| | 管什么 |
|---|---|
| `home::CodexHome` | 私有 `CODEX_HOME`；**拒绝系统 temp** |
| `cage::Cage` | Windows Job Object，`KILL_ON_JOB_CLOSE` |
| `engine::Engine` | 起进程、交 stdio、有序停止 |
| `supervisor::Supervisor` | 退避重试，且**让每次失败都被看见** |

##### 两个机制各管一半，谁也替不了谁

| | 覆盖 | 不覆盖 |
|---|---|---|
| `kill_on_drop(true)` | 正常退出、panic 展开 | **`TerminateProcess`** —— 析构函数根本不跑 |
| Job Object | **任务管理器「结束任务」这种强杀** | —— |

两个都要。以后谁觉得其中一个多余而删掉，删的就是「强杀」这条路上的唯一防线。已写进模块文档。

##### 一个会静默失效的坑

`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` 必须设在 `JOBOBJECT_EXTENDED_LIMIT_INFORMATION.BasicLimitInformation.LimitFlags` 上。**设在外层结构体上不会报错，只是不生效** —— 笼子看着建好了、测试也过了，直到线上强杀才发现根本没关住。

所以建完立刻 `QueryInformationJobObject` 回查一次，`Engine::spawn` 在标志没带上时**直接拒绝启动**。这一条不能降级成警告：笼子失效意味着「关掉 Chat 就够不着」变成一个平时看不出、出事才发现的假承诺，宁可起不来。

##### 明写的取舍：入笼有个窗口

`spawn` 返回到 `AssignProcessToJobObject` 之间，子进程理论上能生出笼外的孙进程。严格做法是 `CREATE_SUSPENDED` 起进程、入笼、再 `ResumeThread`，但拿主线程句柄要绕开 std 的 `Command` 自己调 `CreateProcess`。

选了不绕：app-server 起来后**先读 stdin**，真正生孙进程（命令执行器、沙箱助手）是一轮对话开始以后的事，离入笼已经很远。**这是取舍，不是遗漏**，代码里写明了。

##### 怎么证明的（方法本身值得记）

- **假靶子（`cage.rs`，常驻）**：探针把自己关进笼子、生一个孙进程，**孙进程继承同一根 stdout 管道**。测试强杀探针后去读管道 —— 读到 EOF 说明两个写端都关了，即孙进程也死了。**这样完全绕开 PID 复用**，不需要「查查看还有没有这个 pid」那种不可信的判断。
- **反向对照**：`--no-cage` 模式下孙进程**必须活下来**。没有这条，主测试可能因为无关原因通过（比如孙进程压根没起来），而我们不会知道 —— 一个永远绿的测试等于没有测试。
- **真 codex（`e2e_cage.rs`，`#[ignore]`）**：确认 codex **已经在产出**之后才动手，因为真 codex 会再生助手进程，那些才是可能逃出笼子的东西，假靶子测不到。判死活用 `OpenProcess` + `WaitForSingleObject`，**句柄在杀之前就开好** —— 杀完再开可能开到一个复用了同一 pid 的新进程。

##### 一个对产品有直接影响的意外发现

跑完测试查残留时，机器上确实有一个 `codex.exe ... app-server` 在跑 —— **那是用户自己的 Codex 桌面应用**（Store 版，父进程 `ChatGPT.exe`）。

所以：**任何「清理游离 codex 进程」的逻辑都绝不能按镜像名匹配**（`taskkill /IM codex.exe` 会把用户正在用的官方 Codex 一起杀掉）。测试里的清理已经改成按 PID。这条对将来做「上次没退干净」的自愈逻辑同样成立。

##### 健康探测与重启

协议里**没有便宜的心跳原语**，所以不发明一个：存活＝`try_wait()` 是 `None`，可用＝`initialize` 成功。拿 `thread/list` 当心跳要花一次真 RPC，而且它会因为跟健康无关的原因失败 —— 那种探测比不探测更坏。

**重启不是透明的**：新起来的 app-server 是个空进程，之前那些 thread 的内存状态全没了。所以 `Restarted` 带 `#[must_use]`，调用方要么 `thread/resume`（A2 已验过可用），要么如实告诉用户「引擎重启了」。悄悄接上去装作无事发生，会让用户以为上下文还在。

##### 平台

**笼子的保证目前只在 Windows 成立。** 非 Windows 只有 `kill_on_drop`，那是明显更弱的东西（macOS 没有等价物，Linux 的 `PR_SET_PDEATHSIG` 只对直接子进程有效）。`Cage::kills_on_close()` 在非 Windows 上**如实返回 false**，不假装有保证。别把代码里的 `#[cfg]` 当成跨平台对等。

### A5 — Tauri IPC 桥

Rust 侧命令（起会话/发消息/停止/答复审批）+ 事件推到前端。**前端不认识 codex 协议**，只认识我们的类型。

**验收**：前端一个按钮能起会话并看到流。

#### A5 执行记录（2026-09-03）—— **Rust 侧完成；那个按钮留给 A6**

**先说清楚验收的落差**：这一步验的是**同一条链路的全部实质**（命令、事件、流式正文、审批往返、拒绝生效），但**没有那个按钮**。真正的界面是 A6 的活，别把这条当成按钮已经有了。

不做按钮是有意的：新开一个 `app/` 路由会进静态导出，等于给 web 和 PWA 发一个 `invoke` 根本不存在的死页面，只为满足一条测试；而改 `PawApp.tsx` 会和正在写 B 线的那条线撞车。

##### 分层：只有一层认识 Tauri

```
前端（只认识 agent::dto 里的类型）
  ↕ invoke / emit          ← 就这一层
chat_lib::agent::AgentBridge（不认识 Tauri）
  ↕
codex-host（进程与笼子） + codex-adapter（协议与会话）
  ↕ JSONL over stdio
codex app-server
```

`src-tauri` 改成 **lib + bin**（Tauri v2 的标准布局）：逻辑在 `chat_lib`，`main.rs` 只剩一行。六个 `#[tauri::command]` 每个都只有三行转接，**逻辑全在桥里** —— 所以集成测试能直接驱动整座桥，不用起窗口、不用绕 invoke 机制。

实证了一件事：**应用自己的命令不需要写进 capabilities**。生成的 `desktop-schema.json` 里只有 `core:*` 权限，没有应用命令的条目 —— Tauri v2 的 ACL 只管插件命令。

##### 前端只发语义，不碰线上词汇

这是把 A2/A3 的教训固化进边界：

- 同一个「拒绝」，v2 方法说 `decline`、旧方法说 `denied`；
- 三类审批的响应形状互不相同，权限那类**没有「拒绝」这个值**；
- 被打断的一轮走的是 `turn/completed`，靠 `status` 字符串才分得出来。

所以前端只发 `approve` / `approveForSession` / `decline` / `cancel` 四个**语义**值，线上说哪个词由 `build_answer()` 照着那条请求决定 —— 这个映射**只存在于一个地方**。`turnCompleted` 事件同时带 `status` 原文和算好的 `success` / `interrupted`，前端不必自己判断（上游还会加新状态）。

##### 审批请求在 Rust 侧就补全内容

A3 的发现是「审批请求是指针不是载荷」。桥的事件泵维护一张 `itemId → item` 表，**发给前端之前就把 item 补上**。让每个界面自己重建一遍那张表，迟早有一个地方会建错，然后用户在看不见内容的情况下点了同意。

##### 待答复表的三条规矩

- **键是完整的 `RequestId`**：`Num(0)` 和 `Str("0")` 是两个键。前端传回来的字符串会按能否解析成整数还原。
- **答复后立刻移除**，重复答复返回 `NoSuchApproval` 而不是静默成功 —— 界面重渲染导致的重复提交必须能看见。
- **引擎停止/事件流断掉时清空**：那些 id 在新进程里不存在，界面上还挂着的话，用户点下去只会拿到一个莫名其妙的错误。

##### 事件是线格式，不是调试转储

每个变体都有显式 tag。`passthrough`（我们没投影的上游通知）与 `decodeError`（协议漂移）**必须能到达界面，但不能长得像正经 agent 事件** —— 否则 A6 会把一条上游新通知当成 agent 的输出画出来。

正文事件带 `turnId`：会话的 `current_turn()` 是**「此刻」**的状态、会跑在消费者前面，**不能用来判断事件归属**，归属信息只能在事件里。

##### 边界：两件事没有被 A5 吸收

`codex_binary` 与 `api_key` 都是**调用方传进来的参数**：前者归 **A10（打包与版本工程）** 决定放哪、怎么随包发；后者归 **A9（凭据托管）** 决定怎么从登录态换出来。A5 只负责把它们接上，不替它们做决定。

### A6 — 前端：agent 会话面（最小可用）

选目录键 → 发提问 → 流式渲染（复用现有 Markdown / 代码块渲染）→ 停止 → 会话列表。
**与普通对话分两个面**（D-1），共用登录态。

**分组 / 模型 / 推理强度的联动直接复用，不重做。** 这是共飞侧已经做好的能力：`/v1/paw/config` 下发 `groups → models → reasoning`，客户端 `PawSelectionState` 负责级联与校验，默认值存服务端。agent 面挂上去就行。

**codex 不认识「分组」这个概念**，它只要三样东西：模型名、reasoning、一把 key。用户选完之后我们翻译一下——模型名和 reasoning 每轮随请求传，key 在起进程时注入。

**一条实现约定**：切分组 = 换 key，**新会话用新 key，正在跑的那一轮不动**。

**验收**：不写代码的人能从界面完成一轮真实任务；切换模型 / 推理强度在下一轮立即生效。

### A7 — 前端：审批 UI

待审批队列、允许 / 拒绝、**超时倒计时**、`waitingOnApproval` 状态条。

**开工前要拿到的答案**：审批默认策略（`never`/`on-request`/`on-failure`/`untrusted`）与超时时长——两条都在总体规划 §8 待决里。

**验收**：拒绝生效；超时按既定策略处理并在界面上说清楚发生了什么。

### A8 — 目录白名单与用户同意

注册工作目录 → 生成**目录键**；键→真实路径的表只在宿主内存里，**IPC 与协议都够不到**；首次授权时告知爆炸半径。

**验收**：前端拿不到任何真实绝对路径；给一个没登记的键，宿主拒绝且报错可读。

### A9 — 凭据托管

登录授权 → 申请/复用托管 key → **`account/login/start {type:"apiKey"}`** 注入（**不是环境变量**，`CODEX_API_KEY` 对 app-server 无效）→ 到期续租 → 换分组只发 `group_id` → 登出作废。

**验收**：全程不读不写用户的 `~/.codex`；换分组后新会话用新分组，旧租约按预期作废。

### A10 — 打包与版本工程

codex 二进制的分发方式（随包 / 首启下载，取决于 V-13 的体积）、版本清单 + 校验值 + 回滚、**升级闸门冒烟**（起会话 → 事件到货 → 审批拒绝生效 → 错误分类），升级与在跑会话的协调。

**验收**：一台干净机器装完即用；把 codex 版本号 bump 一格，闸门脚本能跑并给出通过/失败。

---

## 3.5 codex 里值得补进 Chat 的能力（超出「最小子集」的部分）

总体规划 §2.0 说了「只用 agent 循环」。但把协议面读完之后，有几样东西**成本极低、对 agent 面几乎是刚需**，值得从「多余」里挑回来。按性价比排：

| | 能力 | 协议 | 为什么值 | 建议 |
|---|---|---|---|---|
| 1 | **文件变更与 diff** | `turn/diff/updated`、`item/fileChange/patchUpdated` | agent 面最刚需的一件事：**它到底改了什么**。Chat 完全没有这个概念。没有它，用户只能靠读文字猜 | **M0 就要**（A6 里做） |
| 2 | **任务计划 / TODO** | `item/plan/delta`、`turn/plan/updated` | 让用户看懂「它现在干到哪一步」。**手机小屏上尤其值钱**——一屏放不下流式文本，但放得下 5 条勾选项 | **M0 就要** |
| 3 | **中途纠偏（steering）** | `turn/steer` | 跑歪了不用停掉重来，直接插一句「不对，用 X」。对远程场景是刚需（手机上重开一轮的代价更高） | **M0 或 M1b** |
| 4 | **每轮 token 用量** | `thread/tokenUsage/updated` | 直接接到 Chat 已有的额度/计费展示上，用户能看到「这一轮花了多少」 | **M1b** |
| 5 | **@ 提及文件** | `fuzzyFileSearch` | 输入体验立竿见影，成本低。注意：只在**本机**，远程链路不开 | M1b |
| 6 | **上下文压缩** | `thread/compact/start` | 长会话不至于撞上下文墙。Chat 目前**没有任何上下文管理** | M1b |
| 7 | **权限档 + 自动审批复核** | `permissionProfile/list`、`item/autoApprovalReview/*` | 直接缓解审批疲劳，和 §5.2 的「默认策略」是同一个问题的两半 | M1b（跟 A7 一起想） |
| 8 | **回滚 / 分叉** | `thread/rollback`、`thread/fork` | 「退回到它改坏之前」「从这里试另一条路」。配合 diff 很强，但 UI 不便宜 | M2 之后 |
| 9 | **代码审查模式** | `review/start` | 产品上是独立卖点，不是补 Chat 的短板 | 以后再说 |

**明确仍然不要**（§2.0 那张表继续有效）：MCP、plugins / marketplace、skills、hooks、goals、memories、realtime 语音、`fs/*` 文件服务、独立终端 `command/exec`。
其中 `fs/*` 与 `command/exec` 要特别说一句：**远程链路上永远不开**；本机也建议不开——有 diff 与 @ 提及之后，它们带来的爆炸半径远大于便利。

**反过来，Chat 已有而 agent 面要继承的**：提示词库、导出、Markdown / KaTeX / Mermaid 渲染、附件与图片、分组模型推理联动、快捷键、PWA。这些不是 codex 给的，是 Chat 自己的资产，**agent 面直接复用**。

## 4. B 线任务分解（纯前端，后端接口全现成）

| | 内容 | 备注 |
|---|---|---|
| **B1** | 公开设置驱动的注册入口 + 注册 + 邮箱验证码 + Turnstile | `registration_email_suffix_whitelist` 是**数组**不是逗号串——绑错类型会让整个 `/settings/public` 解析失败，注册入口直接消失 |
| **B2** | 2FA 登录 | 令牌响应缺 `access_token` 必须当场失败，别绑成空对象 |
| **B3** | 个人信息、用量与额度、用量趋势、模型用量 | |
| **B4** | 分组、专属倍率、高峰时段标签、API key 租约与换组 | **换组只发 `group_id`**；高峰倍率只对订阅类分组生效；**不在客户端算「当前是否高峰」**（服务器时区） |
| **B5** | 订阅汇总、充值下单与订单校验、公告 | |

**B4 的三个坑要写成回归测试**，它们都是线上出过事的形状（总体规划 §1.2）。

---

## 5. 顺序与并行

```
T0  Tauri 2 迁移  ──┬─► A1 ─► A2 ─┬─► A5 ─► A6 ─► A7 ─► A8
                    │             │
                    │             └─► A3（与 A4 并行）
                    │        A4 ───────────────┘
                    │
                    └─► A9（A2 之后随时）──► A10（最后收口）

B1 ─► B2 ─► B3 ─► B4 ─► B5     （全程与 A 线并行，只依赖 T0 出得了包）
```

**M0 达成 = T0 + A1..A8**，出口四条（总体规划 §7）：跑通一轮真实任务、沙箱挡得住、审批拒绝生效、强杀无残留。

---

## 6. 这周就能动手的三件事

1. **T0 Tauri 2 迁移**——今天迁最便宜，证据在 §1；
2. **A1 的第一半**：把 `generate-json-schema` / `generate-ts` 的产物挑成最小子集，定下我们自己的类型（不必等宿主）；
3. **把探针脚本变成 `tests/smoke` 的第一版**——它已经能跑通「起会话 → 审批 → 拒绝生效」，直接就是 A10 升级闸门的雏形。**先有闸门，再堆功能**，这样跟上游升版永远不是考古。

---

## 7. 开工前要拿到的答案（否则会返工）

| 问题 | 卡住谁 |
|---|---|
| 审批默认策略与超时时长 | A7 |
| 本机能力集边界（终端 / 文件与 diff 查看 / 多工作区） | A6 的范围 |
| codex 随包发还是首启下载（等 V-13 体积） | A10 |
| 服务端是否落盘会话内容、留多久（Q-1） | M2 的存储层，不卡 A 线 |

**不卡开工的**：mac 相关全部（构建机、签名、Developer Program、进程笼替代方案）——Windows 先行。
