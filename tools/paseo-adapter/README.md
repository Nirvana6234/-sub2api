# Paseo Adapter —— 把 Paseo 接进共飞客户端的适配层

一层与 UI、与具体客户端无关的适配器。**小白助手、完整版 chat 客户端、
以及将来共飞服务端给 Paw 用的适配层，共用同一份契约。**

设计依据见
[`../codex-relay-client/doc/共飞客户端 × Paseo 核心封装架构设计.md`](../codex-relay-client/doc/共飞客户端%20×%20Paseo%20核心封装架构设计.md)
与
[`../codex-relay-client/doc/共飞 Paw 远程操作整体架构.md`](../codex-relay-client/doc/共飞%20Paw%20远程操作整体架构.md)。

## 为什么放在这里，而不是放进 codex-relay-client

它有**两个以上的消费者**，谁都不该拥有它：

- PC 端小白客户端（C#，本机管道）；
- 将来更完整的 chat 客户端（同一个 C# 库）；
- 共飞服务端给 Paw 用的适配层（Node，直接消费同一个 bridge）。

放进任何一个客户端的目录，第二个消费者就得先做一次搬迁。

## 结构

```
tools/paseo-adapter/
├─ bridge/                       # cofly-paseo-bridge：唯一允许 import Paseo 类型的地方
│  └─ src/                       # TypeScript，锁定 @getpaseo/client 精确版本
├─ src/
│  ├─ LanAi.Paseo.Adapter/       # 窄契约的 C# 客户端（net8.0，零 NuGet，无 UI 依赖）
│  └─ LanAi.Paseo.Adapter.Host/  # 进程监管：拉起 bridge/daemon、进程笼、配置生成（后续分期）
└─ tests/LanAi.Paseo.Adapter.Tests/
```

## 三条不可让步的规则

1. **C# 侧永远不解析 Paseo 的任何 schema。** 契约里的类型是我们自己的 record。
2. **`LanAi.Paseo.Adapter` 不引用任何 UI 框架、不引用任何具体客户端的概念。**
   一旦它认识"小白版某个流程"，复用目标就没了。
3. **bridge 不做业务判断**：不选目录、不定沙箱、不决定何时起会话。
4. **路径只进不出，且只从宿主进**：消费者用 `cwdKey` 指目录，
   key→path 的表由**启动 bridge 的那个进程**（拿到用户同意的那个）通过环境变量交进来。
   服务端给 Paw 用的那份也一样——谁都伪造不了一个没被同意过的目录。

## 当前进度

**窄契约的会话面已经完整**：握手、健康、目录、会话生命周期、两路事件。

- ✅ `hello`：契约版本协商 + 一次性 token 校验，版本不符直接拒绝
- ✅ `health`：daemon 是否在（`running`/`down`/**`unauthorized`**）、监听地址、失败原因、codex 是否就绪
- ✅ `workdirs.list`：**只回 key 与显示名，不回路径**
- ✅ `agents.list` / `agents.create` / `agents.send` / `agents.stop` / `agents.archive`
- ✅ `timeline.subscribe` / `timeline.unsubscribe`（批量投递，带丢弃计数）
- ✅ `notifications.subscribe`（`finished` / `error` / `permission`）
- ✅ 错误模型：`CONTRACT_MISMATCH` / `UNAUTHORIZED` / `DAEMON_DOWN` / `CODEX_MISSING` / `BAD_REQUEST` / `TRANSPORT_DOWN` / `INTERNAL`
- ✅ 测试：C# **82** 项（适配器 45 + Host 37）+ bridge 13 项全绿；两个端到端冒烟都跑过真实 daemon
- ✅ `relay.status` / `relay.pair` / `relay.disable`：远程访问开关与配对 offer；
  **只有 `pair` 需要宿主授权**（`AllowRelayOperations`），读状态与关闭永远不需要
- ✅ `LanAi.Paseo.Adapter.Host`：一行 `PaseoRuntime.StartAsync` 起私有 daemon + bridge + 加了 ACL 的管道 +
  连好的客户端；Job Object 进程笼、锁死的 `config.json` 生成、健康探测、退避重启与 `Faulted`、有序停止

### 已验证到什么程度（不要读成"全都验过了"）

| 面 | 状态 |
|---|---|
| 握手 / 健康 / 目录 / 列表 / 建会话 | ✅ 对**真实 daemon** 端到端验过 |
| 其余每个操作（send / stop / archive / subscribe / unsubscribe） | ✅ 用一个不存在的 agent id 跑过真实 bridge：验的不是成功，而是**这条路真的执行了并且回了个分类过的结果** |
| 错误分类（口令错、daemon 不在、codex 缺失、目录键非法） | ✅ 对真实 daemon 端到端验过 |
| 时间线与通知事件 | ✅ **2026-09-01 用一轮真实 codex 会话验过**（`--live-turn`）：
  时间线收到 `TurnStarted → User → Assistant("ok")`，通知收到 `Finished`，`dropped=0`。
  这一轮同时抓到一个真 bug（见下）。 |
| relay 开关与配对 | ✅ **对自建 relay 端到端验过**：`pair` 后 relay 服务端日志出现
  `v2:server(control) connected to session srv_…`，`disable` 后断开；未授权时 `pair` 被拒、
  `status` 仍可读。**没跑过官方公共 relay**，冒烟只连我们自己起的那个。 |
| 通知的 `shouldNotify` | ⚠️ 实测恒为 `false`：daemon 依据**心跳上报的在场状态**选一个接收者，而适配器目前不上报心跳。
  当作建议值用，别当开关。补上心跳是后续项。 |

## 怎么用（消费者视角）

```csharp
await using var runtime = await PaseoRuntime.StartAsync(new PaseoRuntimeOptions
{
    NodeExecutablePath = @"...\node\node.exe",          // 随包私有 node，不是系统 Node
    DaemonEntryPath    = @"...\@getpaseo\cli\dist\index.js",
    BridgeEntryPath    = @"...\bridge\dist\index.js",
    PaseoHomePath      = @"%LOCALAPPDATA%\LanAi.RelayClient\paseo-home",
    Workdirs           = [new WorkdirRegistration("default", workspacePath, "默认工作区")],
});

runtime.Supervisor.StateChanged += (_, e) => ShowState(e.State, e.Detail, e.LogPath);
var health = await runtime.Client.GetHealthAsync();
var agentId = await runtime.Client.CreateAgentAsync("default", prompt: "……");
```

`DisposeAsync` 按 **客户端 → bridge → daemon → 进程笼** 的顺序收尾，返回后 daemon 才真的没了。
**还原用户 `~/.codex` 必须排在它之后**——否则还活着的 codex 进程会读到半新半旧的配置。

## 关于 relay 的三条硬规定

1. **offer 是凭据，不是链接。** 它的 fragment 里带着开一个 owner 会话所需的全部东西——
   实测：经 relay 连入的客户端**完全不带口令**也能列会话、驱动 agent，
   而同一个 daemon 对未鉴权的本机 HTTP 请求返回 401。按密码对待：不落日志、不进工单、不截图。
2. **`disable` 不等于吊销。** 关掉只是让 daemon 不再拨出去；已经发出去的 offer
   在下次打开时照样能用。真正吊销要换掉 Paseo home（`serverId` 变）。
   所以 API 直接返回 `offerRevoked=false`，而不是写在注释里——
   写"吊销访问"按钮的人必须看见这件事。
3. **只有 `pair` 要宿主授权**（`AllowRelayOperations`，默认关）。
   它是唯一会扩大"谁能够到这台机器"的操作。读状态不需要（否则界面连"远程：关闭"都显示不了），
   **关闭更不需要**——降权永远不该被权限挡住。

另外：relay 端点在**生成配置时就写死**，而不是等到有人开启时再决定。
晚一步决定就等于多一次弄错的机会，而弄错的后果是拨到公共 relay 而不是我们自己的。

## 三个已定的实现决定（含理由）

### 管道方向：C# 当服务端，bridge 当客户端

Windows 上 .NET 的 `NamedPipeServerStream` 能带 `PipeSecurity`
把 ACL 精确限制到当前用户；Node/libuv 建的命名管道拿不到同等控制。
所以**由 C# 建管道并设 ACL**，bridge 用 `net.connect` 接进来。
（架构文档里写的是"命名管道"，没写方向；这里把方向定死为安全性更好的那一侧。）

### bridge 与 daemon 是兄弟进程，不是父子

bridge 不负责拉起 daemon，两者都由 Host 拉起、都进同一个 Job Object。
理由是失败域要分开：bridge 崩溃不应导致 daemon 重启（会打断正在跑的会话），
daemon 崩溃时 bridge 要能自己退避重连。

### 依赖方向：Host → Adapter，反向禁止

窄客户端必须能单独用——服务端那份适配层连的是它没有拉起过的 bridge，
根本没有进程监管这回事。反过来允许 Host 依赖 Adapter，是为了让
"建管道、发 token、拉起 bridge、握手" 这三十行不必在每个消费者里重写一遍，
那正是这层适配器要消灭的重复。

## 构建与自测

```bash
# bridge
cd tools/paseo-adapter/bridge && npm install && npm run build

# C#
dotnet build tools/paseo-adapter/PaseoAdapter.sln
dotnet test  tools/paseo-adapter/PaseoAdapter.sln
```

端到端冒烟（需要一个真实 daemon，见 `tests/smoke/README.md`）：

```powershell
pwsh tools/paseo-adapter/tests/smoke/run-spine-smoke.ps1
```
