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

#### A6 执行记录（2026-09-03）—— 第一版做错了形状，已按用户反馈重做

##### 第一版错在哪

第一版把 agent 做成了一个**切换进去的独立模式**：右上角一个「agent」按钮，点了之后
整个界面换成单独一页（选目录、选分组/模型/沙箱/审批、一份平行的转录）。

用户看了截图纠正：agent 不该是模式，而应该是给**当前这个对话**挂一个工作目录——挂上
之后这个对话的发送就走 codex，界面还是同一个 PawChatPane，同一个消息列表，同一套
Markdown/气泡渲染，只是多了工具调用产生的正文。沙箱/审批策略是设置，该进设置弹窗，
不该占聊天工具条的位置；如果需要，工具条那一行（分组/模型/推理 chip 所在的那一行）
再加一个审批的 chip 就够了。

##### 重做之后的形状

composer 工具条那一行新增两个 chip（`ActionButton`，和分组/模型样式一致）：

- **工作目录**：未挂时点它直接弹系统目录选择器；挂上之后点它弹一个小菜单
  （复用现成的 `PawSelector` 弹层）——「更换工作目录」/「结束 agent 会话」。
  **分组、模型直接复用这一行已有的选择器**，不重复造一套。
- **待批准 N**：只在有待处理审批时出现，点开是个锚定在 chip 下方的小面板，
  逐条显示 reason/命令原文，两个按钮同意/拒绝。

沙箱与审批策略挪进 `PawSettingsModal`（新挂目录时读取当前设置，已经挂上的对话
不受后续修改影响）。

**核心实现是 `usePawClient.ts` 里四个新增的纯函数**
（`beginAgentTurn`/`appendAgentDelta`/`finishAgentTurn`/`appendAgentNotice`，外加
`ensureActiveConversationId`）：agent 产生的文本通过它们写回**普通的会话消息列表**
（`PawConversationMessage`），不是一套平行的展示组件——这样 Markdown、推理折叠块全部
免费复用。它们和 `handleSend`/`sending` 状态机完全分开：agent 走 Rust 桥，不经过
`sendPawChat`，混在一起会让"发送中"这件事对不上号。

编排逻辑在新文件 `client/agent/useAgentSession.ts`：`bindings`（每个对话的
目录/沙箱/审批策略）+ `liveConversationId`（哪个对话拥有当前那条线程——
`AgentBridge` 本来就只支持一条，这里不假装能更多，别的对话想发就提示先结束当前会话）+
事件订阅（把 `agentText`/`reasoning` 接进对应消息，`commandOutput` 按 itemId 攒够
一段再落成 fenced code block，不逐字节撒进正文）。

`PawApp.tsx` 用 `agent.armed`（当前对话挂没挂目录）决定 `onSend`/`onStop`/`sending`/
`canSend` 走哪条路——两条路径合流在同一组 props 上，`PawChatPane` 本身几乎不用感知
"现在是不是 agent 模式"这件事。

##### 动手之前先补的一个洞

`StartParams` 一直带着 `appDir` 和 `codexBinary`。A9 把「凭据只落在自己程序目录下」
做成了结构性保证（`CodexHome::under_app_dir`），可只要前端还能传 `appDir`，
那个保证就**离一次 `invoke` 只有一步之遥** —— 网页那侧能指到哪里，凭据就能落到哪里。
A9 的文档注释因此写的是一件代码并没有做到的事。

两条现在都在 Rust 侧定（`AgentPaths`：数据目录来自 `app_data_dir()`，二进制来自随包
资源）。`deny_unknown_fields` 做执法者 —— 再塞回来会当场反序列化失败，而不是被静默
忽略、让人以为自己传的值生效了。

`COFLY_CODEX_BINARY` 可以覆盖二进制路径，给开发和端到端用。它能存在，是因为
**环境变量由启动进程的人设，网页设不了** —— 这和在 `invoke` 里点名路径是两回事。

##### 三条是被约束逼出来的

1. **Tauri 判断必须在 effect 里做。** PWA 与桌面端共用同一份静态产物；模块顶层算出来
   的答案会在构建机上被烤成「我不是桌面端」，于是桌面端永远看不到 agent。
2. **`@tauri-apps` 只能动态 import。** 已验证：含 `__TAURI_INTERNALS__` 的三个 chunk
   都不被首页引用 —— PWA 那条路从头到尾不取、也不求值它们。
3. **不复用 `paw.conversations`。** 那是 PWA 的状态（localStorage、跨端同步、没有
   cwd/threadId/审批队列）；agent 会话是 Rust 持有的，关掉 Chat 就没了。并成一个数组
   会让 PWA 用户看到点不开的幽灵条目，而那套对账逻辑会变成 bug 温床。

##### 令牌同步钉在存储层，不在组件里

`savePawSession` / `clearPawSession` 里推给转发层，**不是**在组件里用 effect 盯
`session` —— 因为**静默刷新只写 localStorage、根本不碰 React 状态**，盯 session 恰好
会漏掉最要紧的那一条。漏掉的表现是转发层攥着过期 JWT → 后端 401 → codex 进重连循环，
界面上只剩一句「正在重试」，没有任何线索。存储是所有令牌变化的唯一咽喉。

##### 这一版刻意没做的两件事

- **审批只做到「不卡死」**（不答复的话整轮会永远挂着）。A7 才正经做：要区分
  `command` 与 `writeStdin`（0.153.0 新增的 `CommandExecutionApprovalKind`）、要画
  文件改动的 diff、要把「同意等于什么」说清。这里**不提供 `approveForSession`** ——
  那等于整个会话把整台机器授出去，在能好好措辞之前不该给。
- **不支持 `thread/resume`**。历史列表点一下是「照这个配置再开一条」，不是接着上次
  那条跑 —— 桥还没有 resume 这条路，不假装能做。

##### 验收状态

- Rust 72 passed / 8 ignored / 0 failed（本次重做没碰 Rust 侧，数字不变），
  clippy 零 warning；`npm run typecheck` 干净；**PWA 静态导出通过**，
  且重新验过 Tauri 那几个 chunk 不进首屏。
- **未做：桌面端实跑。** 和 T0 一样，这条只能由你启动客户端点一遍 —— 挂目录、起会话、
  看正文是不是逐字出来、停止本轮、结束会话。开发时要先指好二进制：
  `COFLY_CODEX_BINARY=<官方 app-server 包>/bin/codex-app-server.exe`。
- 顺带仍然欠着：**换分组的端到端验收**（A 组一轮 → 切 B 组 → 新会话确实走 B 组），
  现在有界面了，可以一并验。

### A9 — 凭据托管

登录授权 → 申请/复用托管 key → **`account/login/start {type:"apiKey"}`** 注入（**不是环境变量**，`CODEX_API_KEY` 对 app-server 无效）→ 到期续租 → 换分组只发 `group_id` → 登出作废。

**验收**：全程不读不写用户的 `~/.codex`；换分组后新会话用新分组，旧租约按预期作废。

#### A9 执行记录（2026-09-03）—— **已被后面的 A9′ 取代，保留作为经过**

##### 先纠正这一步原本写错的前提

A9 原文写「申请/复用托管 key」，但 **Chat PWA 是刻意不要 API key 的**（不存 key 就没有存 key 的问题）。一度想让 codex 也走 JWT 直连，查下来走不通，两条都是实测：

- **codex 的 agent 循环＝它自己调模型。** 不存在「只要 agent 逻辑、模型调用走 Chat 现有通道」这种用法。
  （**2026-09-03 更正**：这里原本写「除非在中间写协议翻译，那正是我们一直躲开的活」——**不准确**。Responses↔ChatCompletions 的互转本仓库 `internal/pkg/apicompat/chatcompletions_responses_bridge.go` 里两个方向都现成。真正的阻塞是**Paw 的请求体装不下工具**：`PawChatRequest` 没有 tools 字段、`PawChatMessage.Content` 是纯字符串、`Prepare` 拼给上游的 body 也没有 tools。走那条路 codex 会拿到一个**一个工具都没有的模型**，永远调不出 `exec_command`。见 A9′。）
- **codex 0.144.2 只会说 Responses 一种线协议。** 从**真二进制**里查的：`chat/completions` 出现 **0 次**。而 Paw 面只有 `/chat/completions`（`paw.go` 明写只认账号会话），后端所有 `/responses` 都挂 `apiKeyAuth`，连 Playground 都没有 `/responses`。

**定下来的形态**：复用小白端那套托管 key 规则（两个端二选一，后端可直接用同一把）；**凭据可以落盘，但只能落在我们自己的程序目录下** —— 我们是 codex 的宿主，这本来就该我们管。

##### 落盘的位置做成了结构性保证

`CodexHome` 只提供 `under_app_dir(app_dir)` 一个构造函数：`CODEX_HOME` 的位置由程序数据目录推出来（`<app_dir>/codex-home`），**调用方没有机会指到别处**，也就没有机会把凭据写到我们管不着的地方。`StartParams` 相应地传 `app_dir` 而不是完整的 `codex_home` 路径。

会话结束时 `wipe_credentials()`：先用等长的零覆盖再删（删除只摘目录项，内容还躺在扇区上）。

**为什么是「结束时」而不是「握手后立刻」** —— 这是被实测数据改掉的一版设计：

```
login: ok
auth.json 存在: True
删除后还存在: False
turn 收束: True          ← 握手后立刻删掉，一轮照样跑完
assistant 正文: 'PONG'
结束时又被写回来了吗: False
```

技术上「毫秒级存活」是可达的。但那条路只验过**一条连接上的一轮**：codex 在 401 重试、断线重连、`thread/resume` 时会不会回头重读 `auth.json`，**没有验过**。既然落在自己程序目录下是可接受的，就没必要为一个不必要的约束去扛「长会话中途莫名失效」那个风险。**会话期间留着，结束时抹掉。**

##### 识别规则照搬小白端，里面几条是踩出来的

`ManagedKeyNaming.cs` 那套，一条不改地搬进 `src/client/paw/agentKey.ts`：

- **按名字认，不按值认**：列表接口会把明文 key 原样返回，所以任何地方都不需要缓存它。
- 名字 `共飞直连客户端-<机器名>-<安装ID>`。**机器名**让一个账号的几台机器各持各的租约（在一台上登出不撤另一台）；**安装 ID** 让同机前后两次安装分开（重装后不去续期一个说不清来历的租约）。这两段由 Rust 侧的 `DeviceIdentity` 给出（`agent_device_identity` 命令），安装 ID 存在程序数据目录里，跟着这次安装走。
- **没有过期时间的 key 排最后，不是最前。** 租约模型下「永不过期」是**缺陷**（授权活得比客户端还久，多半是某次更新清掉了 `expires_at`）。把它当最佳候选，等于让客户端正好收养了租约模型要防的那个东西，然后永远替它续下去。

**和小白端共用同一把租约是刻意的**（两端二选一），所以沿用同一个产品前缀，能直接认领并续期，不会在用户列表里堆两条。代价写进代码注释了：**我们绝不删别的安装留下的 key** —— 小白端会清理同机孤儿，我们不清，因为那可能正是另一个端在用的，删掉等于把它悄悄登出。

##### 一个会静默把租约变永久的三态陷阱

`PUT /keys/:id` 的 `expires_at` 是三态（`api_key_handler.go` 实测确认）：

| 传什么 | 后端理解为 |
|---|---|
| 字段缺席 | 不动 |
| **空字符串** | **清除过期时间 —— 从此永不过期** |
| RFC3339 | 设成这个时间 |

所以续期与换组的请求体里**只放那一个字段**。序列化一个「`expiresAt` 默认为 `""`」的对象过去，会在用户以为只是换个分组的动作里，把一天的租约悄悄变成永久授权，且毫无提示。用只有一个键的字面量，就长不出那种默认值。

##### 分组绑在 key 上 —— 一个分组一把 key

用户指出的坑，查证属实而且比预想的硬：

- **分组就是 `apiKey.GroupID`**，网关里**没有任何按请求选分组的入口**（没有 `X-Group` 头、没有查询参数）。
- 唯一的按请求变化是 `auto_group`：开了之后由**请求体里的 model** 在 `auto_group_ids` 里解析分组（`ResolveAutoGroupForModel`）。那是「按模型自动路由」，而 Chat 的界面里**分组和模型是两个独立选择**，覆盖不了。

由此推出一条**绝不能违反**的规矩：

> **要让不同会话用不同分组，只能换一把 key，绝不能去改某把 key 的分组。**

改分组是在改一个**共享租约**：正在跑的那一轮会中途换到另一个分组去，同时用这把 key 的小白端也会跟着变，两处都毫无提示。所以**删掉了 `switchWorkbenchKeyGroup`**（我先前那版正是这个错误实现），只留「按分组取 key」。切分组＝取另一把＝下一个会话生效 —— 这也正是 codex 那侧的语义，它根本不认识分组。

##### 选择规则挪到了 Rust，因为它不该没人看着

`findCurrent` 加上分组之后变成三层筛选，而它正是上面那个 bug 的所在。这个仓库的前端**没有测试运行器**，所以规则搬到 `codex_host::keylease`，前端只做 HTTP、通过 `agent_pick_key` / `agent_key_name` / `agent_key_needs_renewal` 三个命令调用它。

**IPC 上只传元数据，不传密钥**（id / 名字 / 分组 / 过期时间）：选完返回一个 id，前端拿 id 去自己那份列表里取值。密钥每多走一趟就多一处可能被日志或崩溃转储带出去。

9 条测试盯着那几条**写反了不会报错**的规则：

| 规则 | 写反的症状 |
|---|---|
| 无过期时间排**最后** | 收养并永远续期一把永久 key —— 正是租约模型要防的 |
| 分组看 `group_id` **数据**不看名字 | 用户改个名就跑到别的分组去 |
| 不指定分组＝找**绑定为空**那把 | 会话跑在一个用户没选的分组上 |
| 本次安装优先于同机旧安装 | 一直续期一把说不清来历的租约 |
| 别的机器 / 无关的 key 一概不碰 | 在这台机器上把另一台悄悄登出 |

##### 还没做 / 没验

- **会话结束时撤销 key**：`revokeWorkbenchKey` 写好了但桥的 `stop()` 没接（要把租约 id 带下去），等 A6 接界面时一并做。现在靠 30 天租期 + 下次启动认领同一把兜底。
- **换分组的端到端验收**（「换分组后新会话用新分组」）：规则有测试，但**没有对真中转站跑过**一次「A 组一轮 → 切 B 组 → 新会话确实走 B 组」。
- `~/.codex` 全程不读不写这半在 A1 就由私有 `CODEX_HOME` 保证了。

#### A9′ 重做（2026-09-03，用户拍板）—— **壳做一层转发，客户端一把 key 都不要**

上一版把中转站地址和一把真 key 直接交给 codex。用户定的新形态：**给它壳自己的访问地址和一把本地 key，请求打回壳里来，壳再转一次** —— 这样 Chat 的通讯逻辑是一致的，且可以直接复用 paw 那条现成链路。

##### 先把地基测了（`codex-host/scripts/probe-local-proxy.py`）

一个假的回环 SSE 服务器，不连中转站、不要 key、不花钱，跑通了整轮（`turn/completed status=completed`）。四个地基问题一次性回完：

| 问题 | 实测 |
|---|---|
| 明文 `http://` 回环能不能用 | **能**，codex 不挑协议 |
| `account/login/start` 校不校验 key 形状 | **不校验**，任意不透明串照收 |
| 追加什么路径 | **`<base_url>/responses`**，没有 `/backend-api/codex` 前缀 |
| 起来时还打不打别的接口 | **不打**，整轮只有 1 个 HTTP 请求 |

最有价值的一条在请求头里：**codex 每个请求都自报 `thread-id`**。于是转发层能**按 thread 路由** —— 一个 codex 进程就能让不同会话走不同分组，不必一会话一进程。另外它**不发 `Origin`**，所以「带 Origin 一律拒」这道闸可以设死。

##### 后端：`POST /api/v1/paw/responses`

查 `paw.go` 发现后端**早就有这个机制**，只是没开在 `/responses` 上：`pawChatHandler` 就是「JWT 进 → 按请求校验分组 → `ReplaceAuthenticatedAPIKey` 就地换上服务端自己的 key → 交给同一个网关 handler」。新路照搬这个形状，两处不同都是被 codex 逼出来的：

- **请求体原样透传**（完整 Responses 载荷，实测 ~47KB），只读出来看一眼取 model；
- **分组走 `X-Paw-Group-Id` 请求头**，因为请求体不归我们支配。

composite 在 handler 里就地解析（和 chat 那条一致）—— **不能**改用网关那条路的 `autoGroupModelRouting` 中间件，那条会按请求体里的 model 自己选分组，把调用方明确指定的覆盖掉。

**刻意没有照搬 reasoning 校验。** chat 那条的 reasoning 是用户在界面上选的，挡下来是帮用户；这条的是 codex 每轮自带的。都校一遍的结果是，只要 Paw 模型目录没列出那个档位，**每一轮**都被退回来 —— 而上游其实接得住。第一版正是这么写的，被测试当场逮住。

##### 壳：`codex_host::LocalRelay`

codex 拿到 `http://127.0.0.1:<随机端口>/v1` 加一个**随机本地令牌**。令牌照样会落进 `auth.json`，但它离开这台机器一文不值，而真正的账号会话从头到尾没进过 codex 的地址空间。

**回环不等于私有** —— 这台机器上任何进程、用户浏览器里任何网页都能往这个端口发。它们读不到响应（没有 CORS 头），但**发得出去就已经在花钱了**。所以令牌之外还有三道闸：只认 `POST /v1/responses` 一条路；带 `Origin` 一律拒；`Host` 必须是我们自己（挡 DNS 重绑定）。令牌用 32 字节 CSPRNG、定长比较，**没有**照搬 `identity.rs` 那个「纳秒+地址」的凑法 —— 那个值只需要独特，这个值需要猜不出来。

往上游带的头是**白名单不是黑名单**（codex 那些 `installation_id` / `window_id` 一条不带）；`Authorization` 是**换掉不是追加**，把 codex 那个本地令牌当凭据发上去是这条路上最容易犯的错，有一条测试专门盯着它。

**刷新逻辑只留在前端**：`agent_set_session_token` 让前端在刷新后推一次新 token，会话跑着也能推。在 Rust 里再实现一份 refresh，两份迟早对不上，而对不上的表现是「偶尔莫名其妙要重新登录」。

##### 于是那个分组坑，到这一层是真修掉了

不再是「一个分组一把 key」的变通，而是**分组回到按请求选**。代价是 `codex_host::keylease` 与 `client/paw/agentKey.ts` **成了死代码** —— 暂不删，等对真中转站验过一次再单独一版删，万一要回滚也容易。

##### 走 paw 这条路顺带继承的两条限制（查了配置，都不用改，但要知道）

- **请求体上限 256MB**（`gateway.max_body_size` 默认值）。第一轮实测 47KB，历史和工具结果会涨，但离这个数还很远。
- **`/api/v1/paw/*` 整组挂着按用户限流，默认 240 次/分钟**，而且**和 Chat 界面自己的请求共用一个桶**。一轮带 10 次工具调用就是 10+ 个 `/responses`，按真实节奏（每次都要等模型往返）够用；但这是个天花板，撞上时的表现是**半路 429**，不好查。真撞上了是调设置，不是改代码。

##### 这一版给 A6 留下的两条硬要求

1. **必须调 `agent_set_session_token`。** 命令有了，还没有调用方：前端刷新 token 之后要推一次，登出要推 `null`。不推的话转发层握着一个过期 JWT → 后端 401 → codex 进 `willRetry` 重连循环，界面上只会显示「Reconnecting…」，没有任何解释。
2. **模型必须从 `/api/v1/paw/config` 里选。** `PrepareResponses` 就是拿这份目录校验的，从别处拿来的模型名会让**每一轮**都以 `MODEL_UNAVAILABLE` 400 掉。

##### 验收状态

- 后端 9 条、转发层 9 条、真报文闸门 6 条，全绿；工作区 67 passed / 8 ignored / 0 failed，clippy 零 warning。
- 分派的两半都验了：OpenAI/Grok → OpenAI 网关，其余 → 通用网关。和 `gateway.go` 里 `/responses` 用的 `isOpenAIResponsesCompatibleGatewayPlatform` 是同一套判定，写法不同但必须同答案 —— 只测一种平台的话，分歧了也看不出来。
- 线上契约（`/api/v1/paw/responses` 与 `X-Paw-Group-Id`）在 Go 与 Rust 两侧**各钉了一次字面量**：这两个常量靠肉眼对齐，改名的话两边测试都还是绿的、而产品 404。
- **未做**：对真中转站跑一次端到端（A 组一轮 → 切 B 组 → 新会话确实走 B 组）。这条要等 A6 有界面才好验。

### A10 — 打包与版本工程

codex 二进制的分发方式（随包 / 首启下载，取决于 V-13 的体积）、版本清单 + 校验值 + 回滚、**升级闸门冒烟**（起会话 → 事件到货 → 审批拒绝生效 → 错误分类），升级与在跑会话的协调。

**验收**：一台干净机器装完即用；把 codex 版本号 bump 一格，闸门脚本能跑并给出通过/失败。

---

#### A10 执行记录（2026-09-03）—— 打包形态定了，**沙箱语义的结论要改 A7/A8**

##### 「325MB 塞不进安装包」这个前提是错的

release profile 里 `strip = false` 且带 line-tables 调试信息（注释写明由打包阶段
归档符号后再 strip）。但 strip **只省下约三分之一** —— 219MB 才是 app-server 的
真实体积。真正要看的数是**压缩后**：

| | |
|---|---|
| `codex-app-server.exe`（上游已 strip） | 218.4 MB |
| `.exe.zst` | 53.6 MB |
| 完整包 `tar.zst` / `tar.gz` | 81.6 / 108.9 MB |
| 现在的安装包 | 4.8 MB |

##### 不自己编，用上游 release 制品

上游 release **同时发两个包**（`.github/workflows/rust-release-windows.yml`）：
primary bundle 是多合一 `codex.exe`；**app-server bundle 是 `codex-app-server.exe`**。
后者本身就是 app-server，是上游的一等公民，不是我们自创的路子。

本地从 main 快照编过一份（29 分钟，175 个工作区 crate + ~1000 依赖；下载只占 212MB，
不是瓶颈），结论是**和官方 rust-v0.153.0 同一代**：工具集、body 顶层键、请求头集合
完全相同，只差 195 字提示词，体积 218.9 vs 218.4 MB。

**但仍然用官方那份**，理由只有一条：官方包的 `codex-package.json` 里
`version=0.153.0`，而 main 快照的 workspace version 写死 `0.0.0`（发布时才打）。
**一个说不出版本的二进制不该随包发。** 身份记在
`crates/codex-host/tests/fixtures/bundled-codex.json`（tag + 逐文件 sha256 + 体积）。

包里我们真需要的：

| 文件 | |
|---|---|
| `bin/codex-app-server.exe` | 必需 |
| `codex-resources/codex-command-runner.exe` | 必需 —— 执行链路 |
| `codex-resources/codex-windows-sandbox-setup.exe` | 必需 —— 执行链路 |
| `codex-path/rg.exe` | 可选，给 agent 的 shell 用，codex 自己不依赖 |
| `bin/codex-code-mode-host.exe` | **用不上**，`CodeModeHostTransport::Local` 不 spawn 它，省 69MB |

顺带修掉一个会直接炸的地方：`engine.rs` 写死了 `cmd.arg("app-server")`，而精简二进制
**自己就是** app-server，多这个参数会被 clap 当未知参数拒掉，我们这侧只看得到「起不来」。
现按文件名判断（`CodexBinaryKind`）。

##### **沙箱不是安全边界，审批才是** —— 这条改 A7/A8 的做法

用真进程量的（假中转站返回一次 `exec_command` 工具调用，让 agent 真去写文件；
官方 0.153.0 包与本地构建结果完全一致）：

| sandbox | 决定 | 往**工作区外**写 |
|---|---|---|
| `read-only` | accept | **成功** |
| `workspace-write` | accept | **成功** |
| `read-only` | decline | 失败，`status: declined`，盘上无痕迹 |

代码侧对得上：`core/src/tools/sandboxing.rs` 里
`requires_escalated_permissions() -> SandboxOverride::BypassSandboxFirstAttempt`。

推论：

- **`sandbox` 参数只约束「不经审批就跑」的命令。** 一旦有人点了同意，命令就带着
  Chat 进程的全部权限跑，工作区边界不存在。
- **A8 的目录白名单没法靠 codex 的沙箱实现。** 要么我们自己在审批那一层检查命令，
  要么就不要承诺这件事 —— 承诺了又靠沙箱兜底，是个平时看不出来、出事才发现的假承诺。
- **A7 的审批 UI 不能暗示「同意＝在沙箱里跑一下」**；`acceptForSession` 等于整个
  会话把整台机器授出去，措辞必须说清。

还有一个容易混的点：`approvalPolicy=never` 时，没被自动放行的命令会在**进沙箱之前**
就被 `exec_policy` 拒掉（"blocked by policy"）—— 看着像沙箱拦住了，其实沙箱根本没
被调到。第一版探针正是栽在这里。两个探针都收进了 `crates/codex-host/scripts/`。

##### 欠的账：fixture 与 schema 还停在 0.144.2

0.153.0 的工具集是 `exec_command`/`write_stdin`，0.144.2 是 `shell_command`/`update_plan`，
**0.144.2 对 `exec_command` 直接回 `unsupported call`**。所以：

- 报文 fixture 已重录到 0.153.0，并**新增一条测试钉住工具集** —— 补的是一个真把我
  骗过去的缺口（拿 fixture 比对得出「零漂移」，因为 fixture 只存了 body 的顶层**键名**；
  键名一个没变，内容换了两个工具）。**比对只能证明你真比了的那部分。**
- **A2/A3 的审批 fixture 与 306 份 JSON schema 仍录自 0.144.2，尚未重录。**
  schema 导出（`generate-json-schema`）在 `codex` CLI 里而不在 app-server 里，
  要重录得另外取 CLI 二进制。好消息是审批 fixture 现在可以对着**假中转站**录，
  不再需要真 key。

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
