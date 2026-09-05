# 共飞直连客户端（小白版）—— macOS / 跨平台方案

> 状态：**方案草案，未动代码**。最后整理 2026-08-27（第 4 轮）。
> 对应实现：`tools/codex-relay-client/`（Ver0.1 正式版最近一次发布 20260823-r1）。

---

## 0. 结论摘要

| 问题 | 结论 |
|---|---|
| 用什么做跨平台？ | **Avalonia UI 11 + .NET 8**，不换语言、不重写。 |
| 能复用多少？ | 业务逻辑约 **85% 直接复用**（含 246 个现有测试），成本集中在 **UI 层与打包分发**。 |
| 一套代码还是两套？ | **一套。** Avalonia 达到对等后退役 WPF，统一 `net8.0`、单一 UI、单一流水线（可行性见 §5.1）。 |
| 包体为什么大？ | 944 MB 里 **759 MB（92%）是转发的 OpenAI 官方安装包**，不是我们的代码。而**在线下载的代码早已写完并发布**，只是被内置文件盖住了（§2）。 |
| 包体怎么解决？ | **Phase −1（半天）**：删内置包 → 825 MB 降到 66 MB。**终局**：Avalonia 裁剪 → **约 20 MB**（WPF 无法裁剪，163 MB 是硬地板）。 |
| 不用 Mac 构建？ | ✅ **可行**：Windows 交叉编译 `osx-arm64` + `rcodesign` 临时签名。**必须放弃 NativeAOT**，且**不签名的交叉编译产物在苹果芯片上会被内核直接杀掉**（§8）。 |
| 不买开发者账号？ | ✅ 可行，⚠️ **有代价**：公证无法绕过。改用"终端一行命令"分发绕开 quarantine。**账号决定可推迟到 Phase 4。** |
| GitHub Actions？ | ✅ 而且**因为不买账号，CI 反而极简——没有任何签名密钥要管**。终局整条流水线只需 `ubuntu-latest`（1× 分钟）。⚠️ 但仓库**根目录目前没有 `.github/workflows/`**，现有 workflow 在子目录里、实际不会触发（§9.5）。 |
| 最大风险？ | **不是 UI，是 macOS 版 ChatGPT 桌面版是否读 `~/.codex/`**（§3 G-1）。cc-switch 已佐证一半，剩下半条需借一台 Mac 实测十分钟。 |
| 工作量 | 约 **32–42 人日**（Avalonia UI 约 15、打包约 5、JSON 源生成 1–2）；Phase −1 半天。 |

### 0.1 本轮整理发现的 14 处遗漏

按严重程度排序。每条都已折进对应章节，此处仅作索引。
**三条 🔴 的共同特征是「静默失败」**——不报错、不崩溃，用户只看到一个坏掉的产品。

| # | 遗漏 | 严重度 | 归属 |
|---|---|---|---|
| **1** | **裁剪打断反射式 JSON** —— 契约静默绑成空对象，且就在"20 MB 目标"的关键路径上 | 🔴 高 | §2.4 / Phase 1 |
| **2** | **Avalonia 默认反射绑定，裁剪后界面静默空白** —— 与第 1 条同一危险类别 | 🔴 高 | §2.4 ② / Phase 3 出口 |
| **3** | **mac 每次更新都要重撞 Gatekeeper**，不只是首次安装 | 🔴 高 | §8.5 |
| **4** | `AiSwitch.Injection` 有 3 个其他消费者，直接改 TFM 会波及 WPF 工作台应用 | 🟡 中 | §5.1 |
| **5** | `Info.plist` 缺 TCC 用途说明键（`NSAppleEventsUsageDescription` 等） | 🟡 中 | §8.1.1 |
| **6** | macOS TCC 授权与 ad-hoc 签名相互作用，重签可能导致授权失效 | 🟡 中 | §6.1 |
| **7** | **仓库根目录没有 `.github/workflows/`**，现有 workflow 位于子目录、实际不会被触发 | 🟡 中 | §9.5.1 |
| **8** | `TEST_SERVER` 条件编译若不随 `ClientOptions` 迁移，测试渠道会静默编译成生产地址 | 🟡 中 | Phase 1 |
| **9** | 发布前配置检查清单缺失（服务器地址仍是 `127.0.0.1` 占位符） | 🟡 中 | §9.5.5 / Phase 4 |
| **10** | `Markdig` 的裁剪兼容性未验证（其扩展注册是反射驱动） | 🟡 中 | §2.4 ③ / Phase 1 |
| **11** | 卸载时本地残留（LaunchAgent、数据目录）无清理脚本 | 🟡 中 | §6 / §6.2 |
| **12** | CI 无 mac runner 就跑不了 mac 平台实现的测试 | 🟡 中 | §9 Phase 2 / §9.5.3 |
| **13** | Avalonia 的 macOS 中文输入法（IME）需实测 | 🟢 低 | §7 |
| **14** | Intel Mac 用户下到 arm64 包会静默失败 | 🟢 低 | §8.4 |

> **关于第 1、2 条，需要更正我前几轮的说法**：我把反射绑定问题描述成"只影响 NativeAOT"。
> **这是错的**——它同样影响 `PublishTrimmed`，而裁剪正是 v1 达成 20 MB 的**唯一手段**。
> 排除 AOT 时这条要求本应留下，却跟着一起被删掉了。详见 §2.4。

---

## 1. 现状盘点：这个客户端到底做了什么

它不是聊天客户端，是**官方 ChatGPT 桌面版的"接管器"**。核心机制四步：

1. 邮箱登录共飞中转站，拿到会话令牌；
2. 在中转站申请一把**托管 API key**（1 天租约，`ManagedKeyNaming` + `CodexStartup` 负责续租与回收）；
3. 把 key 和中转站 base URL 写进 **`~/.codex/config.toml` 与 `~/.codex/auth.json`**（`CodexConfigWriter`，带完整快照与原子恢复）；
4. 拉起官方 ChatGPT 桌面版，`CodexRouteGuard` 持续守护路由不被改回去。

面板（余额 / 用量 / 分组倍率 / 订阅 / 公告 / 充值）是这套机制的外壳。

### 1.1 代码量与可移植性

| 工程 | 行数 | 目标框架 | 可移植性 |
|---|---|---|---|
| `LanAi.RelayClient.Server` | 2,175 | `net8.0`，**零 NuGet 依赖** | ✅ 复用 |
| `LanAi.RelayClient.CodexBinding` | 1,061 | `net8.0` | ✅ 复用（`CodexPaths` 已用 `UserProfile`，mac 上即 `~/.codex`） |
| `LanAi.RelayClient`（WPF 主程序） | 9,869 | `net8.0-windows` | ⚠️ 混合 |
| `tests/`（246 个用例） | 7,707 | 多数 `net8.0` | ✅ 随 Core 走 |
| `AiSwitch.Injection`（注入） | — | `net8.0-windows` | ✅ 可改 `net8.0`（§5.1） |

主程序 9,869 行里，**真正绑死 Windows 的只有 4 个文件、492 行**：
`DpapiSessionStore` 135 + `DpapiSnapshotProtector` 23 + `StartupRegistration` 88 + `TrayPresence` 246。

### 1.2 这套代码为跨平台准备得比预期好

影响工作量估算的事实，不是恭维：

- `LanAi.RelayClient.Server` 当初就被**刻意**定为零依赖 `net8.0` 库、不引用 WPF，为的是脱离 UI 单测——现在直接成为跨平台资产。
- 现成接缝可直接挂 macOS 实现：`ISnapshotProtector`（→ Keychain）、`ICodexAppLauncher`（→ `open -b`）、`ICodexEnhancementHost`（已有 `NullCodexEnhancementHost`）、`IProcessLauncher`、`IQRCodeRenderer`。
- `AnnouncementMarkdownParser`（375 行）输出**框架中立的 AST**（`AnnouncementNode` 等 record），不是 WPF 类型。只有渲染器 `AnnouncementDocumentBuilder`（226 行）绑 WPF。
- `UsageLineChart`（841 行）中 `UsageLineChartLayout` + `UsageLineChartGeometry`（约 185 行纯计算）已与绘制分离。
- `QRCoderRenderer` 已用 `PngByteQRCode` 出字节流，仅最后包 `BitmapSource` 那几行绑 WPF。

---

## 2. 体积问题：944 MB 是怎么来的

### 2.1 实测拆解（Ver0.1-正式-20260823-r1）

| 文件 | 大小 | 性质 |
|---|---|---|
| `codex-installer/Codex-Windows-x64.msix` | **759 MB** | OpenAI 官方安装包，**不是我们的代码** |
| `codex-relay-client_v0.1_x64.zip` | 66 MB | 客户端分发单元，`unzip -l` 实测**只含**下面那个 exe |
| `共飞-ChatGPT助手.exe` | 163 MB | 上面 zip 的解压产物，不重复计入下载量 |

**用户今天实际下载约 825 MB，其中 92% 是我们转发的第三方安装包。**

### 2.2 关键发现：不这么大的代码早就写完了，只是被自己盖住

`CodexInstaller.cs:53` 写着 `DownloadUrl = "https://codexapp.agentsmirror.com/latest/win-x64"`，
完整下载实现（含 `IProgress<CodexDownloadProgress>`）已接入 `DashboardViewModel.CodexDownloadProgressText`，
进度文案直接显示在启动按钮上。**在线下载已经写完、已经发布了。**

它不生效只因为 `Inspect()` 会**先**在 `codex-installer/` 找到内置 msix，下载分支永远走不到。

git 历史确认是遗留而非设计：

- `9dc923d`（2026-08-05）`build(client): include supplied Codex installer in releases`
- `7a8b703`（2026-08-09）`客户端改为在线下载chatgpt`

**改成在线下载后，csproj 里那条 `*.msix` glob 从未删掉。**
`codex-installer/README.txt` 至今仍写着「The client does not download an installer automatically」，同样没跟上。

删除风险低，因为**三级降级本来就存在**：~~内置~~ → **自动下载**（已实现）→ **手工放入目录**（已实现，README 已引导）。
只需从 csproj 去掉 `*.msix` 等 glob，**保留目录与手工放置作离线兜底**。

### 2.3 客户端本体的硬地板：WPF 不能裁剪

163 MB 不是配置疏忽，是**地板**：ILLink 不支持 WPF（XAML 反射构造无法静态分析），`PublishTrimmed` 在 WPF 上不可用。

| 形态 | 磁盘 | zip 下载量 |
|---|---|---|
| 现状：WPF 自包含单文件 | 163 MB | 66 MB（无法再降） |
| **Avalonia 自包含 + `PublishTrimmed`** | 约 35–45 MB | **约 20 MB ← v1 目标** |
| ~~Avalonia + NativeAOT~~ | — | ❌ **已排除**：NativeAOT 无法跨平台编译，与"不用 Mac 构建"冲突（§8.1） |

**压缩开关不必考虑**：`EnableCompressionInSingleFile` 能把 exe 压到 66 MB 上下，但你已用 zip 分发，
用户下载量本来就是 66 MB。它只改善磁盘占用，代价是首次启动解压整个运行时、拖慢冷启动。对小白客户端是净负。

### 2.4 🔴 遗漏 1：裁剪会打断反射式 JSON —— 这在关键路径上

**这是本轮最重要的发现，也是对我上一轮说法的更正。**

`RelayServerClient` 全程用**反射式** `System.Text.Json`：

```csharp
private static readonly JsonSerializerOptions SerializerOptions = new(JsonSerializerDefaults.Web);
// ...
bound = data.Deserialize<T>(SerializerOptions);       // RelayServerClient.cs:697，单一泛型入口
```

`ClientVersionChecker` 亦然（`GetFromJsonAsync<VersionManifest>`）。涉及约 **25 个契约类型**
（`Contracts.cs` 10 + `PanelContracts.cs` 11 + `PaymentContracts.cs` 4）。

**两个后果，都落在 20 MB 目标的必经之路上：**

1. **运行期静默失败。** `PublishTrimmed` 下 trimmer 会裁掉没有静态引用的属性 setter 与构造函数，
   反序列化会得到空对象或抛异常。**这恰恰是 README 记录过的那类最难查的 bug**——
   当年 `AuthTokens` 因为"每个属性都有默认值"而把畸形响应干净地绑成空对象，报告"登录成功"再全程 401。
   裁剪会**重新制造同一类故障**，而且这次遍及全部 25 个契约。
2. **构建直接失败。** 所有 csproj 都开着 `TreatWarningsAsErrors=true`，
   裁剪产生的 IL2026 会把构建打断——这反而是好事，不会静默溜过去。

**修法**：迁移到源生成的 `JsonSerializerContext`。

> ### ⚠️ 2026-08-27 实测更正：这一步比估的贵，而且卡在一个需要你拍板的决定上
>
> 已实际动手做到底，类型清单（26 个）与全部调用点都已核对完成，代码在
> `RelayJsonContext.cs`。过程中撞到一个**必须由人决定**的阻塞点：
>
> **源生成无法为 `init`-only 属性赋值**，报
> `Setting init-only properties is not supported in source generation mode`。
> 它转而走「带参构造函数」转换器，于是**服务器省略的字段会变成 `default`（`null`），
> 属性初始化器根本不执行**。
>
> 实测而非推测。对 `{"access_token":"at","token_type":"Bearer"}`：
> 反射得到 `RefreshToken == ""`，源生成得到 `RefreshToken == null`。
>
> **受影响的是 41 个属性**（38 个 `= string.Empty` + 3 个集合初始化器），
> 遍布全部 26 个契约。集合返回 `null` 比字符串更糟——直接 NRE。
>
> 只有 `AnAccessTokenWithoutARefreshTokenIsAccepted` 一个测试抓到了它，
> 因为它是唯一断言「被省略字段」的测试。**其余 40 个属性没有测试守着。**
>
> 三个选项：
>
> | 方案 | 代价 |
> |---|---|
> | ① `init` → `set`（41 处） | диff 最小，行为完全复原；**但公共契约失去不可变性** |
> | ② 改成位置记录 + 参数默认值 | 源生成尊重构造函数默认值，**行为与不可变性都保住**；26 个契约全部重写 |
> | ③ 放弃裁剪 | 不可行——20 MB 目标就没了（§2.3） |
>
> **倾向 ②**：它是唯一同时守住行为和设计意图的，且这些契约本就该是不可变的。
> 但它要动全部 26 个公共契约，**属于产品级决定，已停下等你选**。
>
> 当前仓库状态：**409 个测试全绿**，序列化仍走反射；
> `RelayJsonContext` 已写好但**尚未接线**，文件头写明了上述阻塞点。
>
> 修正估时：**不是 1–2 人日，按选项 ② 约 3–4 人日**（含 26 个契约重写与回归）。
>
> ### ✅ 已按选项 ② 完成（2026-08-27）
>
> 实施时找到一个比"位置记录"更好的写法：**保留现有属性声明与全部文档注释不动，
> 只加一个 `[JsonConstructor]` + 带默认值的参数**。源生成尊重构造函数默认值，
> 于是 `init` 不可变性、逐属性的 XML 文档、`[JsonPropertyName]` 全部原样保住——
> 位置记录会把那些长篇 `<remarks>` 挤进 `<param>`，而这些注释正是这个代码库最有价值的部分。
>
> 落地范围：**16 个契约新增构造函数**，覆盖 41 个依赖初始化器的属性。
> 两个**跨行声明**的属性（`PublicSettings.LoginAgreementDocuments`、
> `PaymentCheckoutInfo.Methods`）正则没抓到，靠"构造函数参数数必须等于 `init` 属性数"
> 的审计脚本查出来——漏掉任何一个都会让该类型在源生成下直接失败。
>
> `Array.Empty<T>()` 和 `new Dictionary<>(…)` **不是编译期常量**，不能直接做参数默认值；
> 这些参数改为可空、默认 `null`，在构造函数体里 `?? 原默认值`。
> 副带好处：服务器显式发 `null` 时也落到空值，比原先反射行为更稳。
>
> **结果**：`LanAi.RelayClient.Server` 已开启 `IsTrimmable` + `EnableTrimAnalyzer`，
> 在 `TreatWarningsAsErrors` 下**构建零警告**——裁剪可行性从此由编译器守着，
> 而不是等发布后才发现。测试 **413 个全绿**（新增 4 个 `ContractDefaultsTests`，
> 专门锁住"字段被省略时的回退值"；此前 41 个相关属性里只有 1 个有覆盖）。

#### 裁剪的前置条件其实有三条，不是一条

反射式 JSON 只是其中最明显的一条。**同一类危险还有两处，都是"静默失败"而非报错**：

| # | 裁剪前置 | 不做的后果 | 处理时机 |
|---|---|---|---|
| ① | `System.Text.Json` 源生成 | 契约绑成空对象 | **Phase 1** |
| ② | **Avalonia 编译期绑定**（`x:CompileBindings`） | Avalonia 默认走**反射绑定**，trimmer 裁掉无静态引用的属性访问器后，**界面静默空白**，不抛异常 | **Phase 3 出口标准** |
| ③ | **`Markdig` 的裁剪兼容性** | 其扩展 / 渲染器注册是反射驱动的，可能需要 `TrimmerRootAssembly` 兜底 | **Phase 1 顺带验证** |

②的做法：在 Avalonia 工程置
`<AvaloniaUseCompiledBindingsByDefault>true</AvaloniaUseCompiledBindingsByDefault>`，
并把「**全部绑定编译期可校验、`PublishTrimmed` 在 `TreatWarningsAsErrors` 下构建干净**」
写进 **Phase 3 的出口标准**——而不是留到 Phase 4 打包时才发现界面是空的。

③建议在 Phase 1 做 JSON 时**顺手验证**：公告渲染器要建在 `Markdig` 之上（§7），
与其等渲染器写完再发现要加裁剪白名单，不如先花半小时确认。

> 顺带：这三条做完，NativeAOT 的主要障碍也一并清掉。虽然 v1 不用 AOT（§8.1），
> 但将来若有 Mac 可用，AOT 门槛会低很多。

### 2.5 终局与收敛

**825 MB → 约 20 MB（降约 97.6%），同一套代码顺带出 macOS 版。**

值得注意：§6 里为 macOS 写的「不内置分发第三方安装包」本来就是 mac 强加的约束——
没人会在 dmg 里塞 759 MB 的 ChatGPT 安装包。所以体积治理不是新方向，而是
**Windows 采纳 mac 无论如何都要走的策略：一条策略，两个平台。**

---

## 3. Phase 0：可行性前置（阻塞项）

### G-1（阻塞）macOS 版 ChatGPT 桌面版是否读 `~/.codex/config.toml` 与 `auth.json`？

- Windows 上 MSIX 版 ChatGPT 内置 Codex 并读用户目录下 `.codex`。
- macOS 上**未经实机验证**，且有一个具体坏情况：**Mac App Store 版是沙箱应用**，
  `~` 被重定向到 `~/Library/Containers/<bundle-id>/Data/`，**读不到真实 `~/.codex`**。
  官网 `.dmg` 版通常未沙箱化，行为可能与 Windows 一致。

**三种结果对应三种产品：**

| G-1 结果 | macOS 版形态 |
|---|---|
| dmg 版读 `~/.codex` | 与 Windows 对等（除注入浮层），本方案照常 |
| 仅沙箱路径可写 | `CodexPaths` 加平台分支；用户必须装 dmg 版而非 App Store 版，引导要写清楚 |
| 完全不读 | 退化为绑定 **`codex` CLI**——面向终端用户，不是小白产品。**属产品决策，工程补不了** |

#### cc-switch 佐证：`~/.codex` 的跨平台一致性已被现成项目证明

参考 [farion1231/cc-switch](https://github.com/farion1231/cc-switch)（Tauri 2 + React + Rust，支持 Windows / macOS / Linux）。
其 `src-tauri/src/codex_config.rs` 的路径解析**完全无平台分支**：

```rust
pub fn get_codex_config_dir() -> PathBuf {
    if let Some(custom) = crate::settings::get_codex_override_dir() {
        return custom;                       // 允许自定义目录覆盖
    }
    get_home_dir().join(".codex")            // 三个平台一律 ~/.codex
}
pub fn get_codex_auth_path()   -> PathBuf { get_codex_config_dir().join("auth.json") }
pub fn get_codex_config_path() -> PathBuf { get_codex_config_dir().join("config.toml") }
```

**确认了两件事：** ① `~/.codex` 这套机制在 macOS 上就是标准做法，我们现有的 `CodexPaths` 开箱即对；
② 一个有实际用户量的项目长期这么做且能用，说明写入侧不存在权限或路径陷阱。

**但要诚实区分它没确认的那一半：** cc-switch 主要面向 **Codex CLI**，
我们要接管的是**官方 ChatGPT 桌面版**。「`~/.codex` 是 mac 上的正确路径」已成立；
「mac 版 ChatGPT 桌面版读这个路径」仍需实机确认。

**G-1 剩余验证收窄为一句话**：装官网 dmg 版 ChatGPT，写入 `~/.codex`，看中转站侧是否有流量。
风险等级从「产品可能不成立」降为「大概率成立，待实测」。

> 可借鉴：cc-switch 提供 `get_codex_override_dir()` **自定义目录覆盖**。
> 若落到"沙箱版路径不同"那支，这正是现成兜底形状——我们的 `CodexPaths` 构造函数
> 本来就接受 `codexHome` 参数，加一个用户可见设置即可，不改架构。

### G-2 进程名与启动方式

`CodexStartup.cs:266` 用 `Process.GetProcessesByName("ChatGPT")` 判断运行状态。
macOS 上需确认进程名（很可能仍是 `ChatGPT`）与 bundle id，并改用 `open -b <bundle-id>` 启动。

### G-3 ~~Apple 开发者资格与构建机~~（**已由 2026-08-27 约束解除**）

不用 Mac 构建**完全可行**，不买账号**也可行**（代价见 §8）。**此项不再是阻塞门**，账号决定可推迟到 Phase 4。

> ⚠️ 但 **G-1 的实测仍需一台 Mac**。不构建 ≠ 不验证——借一台、或找 mac 用户帮忙跑十分钟即可，
> **这一条不能靠交叉编译绕过**。

### G-4 CDP 注入（预期为否，仅确认）

macOS 版 ChatGPT 是原生 AppKit 应用，`--remote-debugging-port` 大概率无效。确认后即定案"mac v1 不做注入"。
**Windows 侧不受影响，注入浮层保留**（§5.1）。

---

## 4. 技术选型：为什么是 Avalonia

| 方案 | 逻辑复用 | 测试复用 | 下载体积 | 评价 |
|---|---|---|---|---|
| **Avalonia UI 11** | ~85% | 246 个全留 | **约 20 MB** | ✅ **推荐** |
| .NET MAUI | 高 | 高 | 类似 | ❌ Mac Catalyst 桌面体验弱，tray / 菜单栏受限 |
| Tauri 2 重写（如 cc-switch） | **0%** | **0%** | 约 5–10 MB | ❌ 见 §4.1 |
| Electron 重写 | 0% | 0% | 80–120 MB | ❌ 既丢资产又不小 |
| WPF + 另写 Swift 版 | 0%（mac 侧） | 0% | — | ❌ 违背"一个项目"，逻辑双份维护 |

### 4.1 关于 Tauri：体积确实更小，但这笔账算不过来

你给的 cc-switch 正是 Tauri 2，产物确实能到 5–10 MB。诚实地说，**Tauri 在纯体积维度上赢 Avalonia**。
但放进完整的账：

| 阶段 | 下载量 | 代价 |
|---|---|---|
| 现状 | 825 MB | — |
| 删内置 msix | **66 MB** | 半天。**与技术栈无关，任何方案都先吃这一步** |
| Avalonia + 裁剪 | **约 20 MB** | 已含在跨平台工作量内，逻辑与测试零损失 |
| Tauri 重写 | 约 8 MB | **重写 9,000+ 行 C#、丢弃 246 个测试** |

**最后一步用一次完整重写换约 12 MB。** 被丢掉的 246 个测试锁的不是样板代码，
是 README 逐条记录、靠真机踩出来的契约陷阱：`expires_at` 三态语义（切分组少发一个字段 =
把 1 天租约变成永久 key）、卡片必须各自失败（否则一张卡 401 就把用户踢下线）、
下拉框"程序设选中 vs 用户切换"（否则每 60 秒往服务器写一次）、高峰倍率只对订阅组生效且按服务器时区。
这些坑重写要重踩一遍，而且是在一个**已有正式版用户**的产品上踩。

**92% 的体积收益来自删内置包，与技术栈无关；技术栈只决定最后那 46 MB 怎么分。**

### 4.2 两个诉求指向同一个动作

- **一个项目**：Avalonia 单代码库同时出 win-x64 与 osx-arm64，无需两套 UI（§5）；
- **包体小**：WPF 无法裁剪，163 MB 是硬地板；Avalonia 支持 `PublishTrimmed`，
  是把客户端压到 20 MB 级的**唯一 .NET 路径**。

不是权衡，是收敛。

---

## 5. 目标工程结构

**一个项目、一个目标框架（`net8.0`）、一套 UI、一条流水线**，靠运行时判断分平台，而非 TFM 分裂：

```
tools/codex-relay-client/
├── src/
│   ├── LanAi.RelayClient.Server/          # net8.0  不变（+ JSON 源生成，§2.4）
│   ├── LanAi.RelayClient.CodexBinding/    # net8.0  不变
│   ├── LanAi.RelayClient.Core/            # 【新】net8.0 —— 全部业务逻辑
│   │   ├── Services/  ViewModels/  Announcements/  Charts/
│   │   └── Platform/                      #   平台抽象接口（§6）
│   │       ├── Windows/                   #   DPAPI / 注册表自启（[SupportedOSPlatform("windows")]）
│   │       └── MacOS/                     #   Keychain / LaunchAgent（[SupportedOSPlatform("macos")]）
│   └── LanAi.RelayClient.App/             # 【新】唯一的 Avalonia 头，win-x64 + osx-arm64
└── tests/                                 # 246 个原样保留 + 平台实现测试
```

（`LanAi.RelayClient` 这个 WPF 工程在 Phase 3 结束、Avalonia 头功能对等后删除。）

### 5.1 已核实：`net8.0-windows` 可以整个去掉

"一个项目"原本的两处技术障碍，均已核实可解：

| 原以为需要 Windows TFM | 实际情况 |
|---|---|
| **DPAPI** | `System.Security.Cryptography.ProtectedData` **NuGet 包支持 `net8.0`**，非 Windows 上抛 `PlatformNotSupportedException`。`OperatingSystem.IsWindows()` 守卫即可。 |
| **CDP 注入**（`AiSwitch.Injection`，现 `net8.0-windows`） | 已核对源码：全项目**只用 `[ComImport]` COM 互操作**（`CodexAppLauncher.cs:233/246`），**无 WinForms / WPF / System.Drawing**。COM 互操作在纯 `net8.0` 上完全可用，加 `[SupportedOSPlatform("windows")]` 即可。⚠️ **但不能直接改，见下。** |

#### ⚠️ `AiSwitch.Injection` 有三个其他消费者，直接改 TFM 会波及别的应用

实测 `grep -rln "AiSwitch.Injection.csproj" tools/`，引用它的不止本客户端：

```
tools/codex-relay-client/src/LanAi.RelayClient/LanAi.RelayClient.csproj   ← 本客户端
tools/manufactor_app/ai-switch-gui/LanAiWorkspace.sln                     ← 另一个应用
tools/manufactor_app/ai-switch-gui/src/AiSwitch.Wpf/AiSwitch.Wpf.csproj   ← 另一个应用的 WPF 头
tools/manufactor_app/ai-switch-gui/tests/AiSwitch.Injection.Tests/…       ← 其 65 个测试
```

把它从 `net8.0-windows` 直接改成 `net8.0`，会同时改变 **WPF 工作台应用**的依赖，属于计划外的波及面。

**正确做法是多目标（multi-targeting），不是改目标：**

```xml
<TargetFrameworks>net8.0;net8.0-windows</TargetFrameworks>
```

我们的 Avalonia 头引 `net8.0` 那一份，WPF 工作台应用继续引 `net8.0-windows` 那一份，
**两边都不用改**，其 65 个测试也不受影响。对我们而言结果完全相同，但**爆炸半径归零**。
| WinForms 托盘（`TrayPresence`） | 由 Avalonia `TrayIcon` 取代，WinForms 依赖随 WPF 一起消失。 |

**结果**：统一到 `net8.0`，Windows 版**保住 CDP 注入浮层**（不必为跨平台而降级功能），mac 版走 `NullCodexEnhancementHost`。
比原方案（拆 `.Windows` / `.MacOS` 两个工程）更干净。

> 副作用是正面的：`net8.0-windows` 也是 `PublishTrimmed` 的障碍之一，去掉后裁剪路径更顺。

### 5.2 Windows 存量用户的迁移连续性

退役 WPF 会换掉 UI 框架，但**用户不会被登出、不会丢配置**：

- Windows 上数据目录仍是 `LocalApplicationData`；
- DPAPI 仍是 DPAPI，`auth.bin` / `config.bin` / 会话快照**原样可解**；
- `InstallId`、`GroupPreferenceStore`、公告已读状态全部沿用同一文件。

**硬约束：`AppPaths` 的 Windows 分支必须原样返回旧路径，不得"顺手规范化"。**
改一个字符，存量用户就等于全新安装。

---

## 6. 平台差异清单

| 能力 | Windows 现状 | macOS 实现 | 抽象接口 | 工作量 |
|---|---|---|---|---|
| 凭据加密 | DPAPI（158 行） | **Keychain**（`Security.framework` P/Invoke） | `ISecretProtector` | 中 |
| 开机自启 | 注册表 `HKCU\...\Run`（88 行） | **LaunchAgent** plist → `~/Library/LaunchAgents/` | `IStartupRegistration` | 小 |
| 托盘 / 菜单栏 | WinForms `NotifyIcon`（246 行） | Avalonia `TrayIcon`（mac 上即菜单栏项） | `ITrayPresence` | 中 |
| 桌面通知 | ~~`ShowBalloonTip`~~ → **自有隐藏通知项 + `Shell_NotifyIcon`**（已实现） | **`osascript -e 'display notification'`**（已实现，未实测） | `INotificationPresenter`（**已落地**） | 小 |
| 数据目录 | `LocalApplicationData` | .NET 在 mac 上映射到 `~/.local/share`（**Linux 形状**），应改 `~/Library/Application Support` | `AppPaths`（新增，**统一 9 处调用点**） | 小 |
| 单实例 | 命名 `Mutex`（141 行） | Unix 上语义不同且"激活已有窗口"无对应物 → **文件锁（`FileShare.None`）+ Unix domain socket 唤起** | `ISingleInstanceCoordinator` | 中，**按新写估算** |
| 启动 Codex 宿主 | COM 激活 MSIX | **`open -b` + `osascript` 退出（已实现，未实测）** | `ICodexAppLauncher` + `CodexHosts` 工厂 | 小 |
| 安装 Codex 宿主 | ~~内置 msix~~ → **在线下载**（已实现，**含 win-arm64**） | **在线下载 `.dmg`（已实现，镜像路径已实测）** | `ICodexInstaller` + `CodexPackageProfile` | 小 |
| CDP 注入浮层 | **Windows 保留**（改 `net8.0` + `[SupportedOSPlatform]`） | v1 不做，用 `NullCodexEnhancementHost` | `ICodexEnhancementHost`（已存在） | 小 |
| 路由守护 | `CodexRouteGuard` 文件监视 | 同（纯文件操作） | — | 零 |
| **卸载清理**（🟡 遗漏 3） | `删除桌面和开始菜单快捷方式.cmd` | **缺**：需 `uninstall-mac.sh` 清 LaunchAgent plist + `~/Library/Application Support` | — | 小 |

> **`AppPaths` 容易被漏掉**：`ClientLog`、`InstallId`、`GroupPreferenceStore`、`DpapiSessionStore`、
> `AnnouncementNotifyStateStore`、`AnnouncementImageLoader`、`CodexAccountStore`、`TrayPresence`、
> `CodexInstaller` 共 9 处直接调 `Environment.GetFolderPath`。不统一，mac 版会往 `~/.local/share` 丢东西。

### 6.1 🟡 遗漏 4：macOS TCC 权限弹窗，且与 ad-hoc 签名相互作用

两处会触发 macOS 隐私授权（TCC）：

1. `osascript` 发通知 / 用 Apple Events 控制 ChatGPT → 弹「"共飞助手"想要控制"ChatGPT"」；
2. 通知权限本身需用户同意。

**与 §8 的 ad-hoc 签名叠加会产生一个具体问题**：TCC 授权是**绑定代码签名标识**的。
ad-hoc 签名的标识不如正式证书稳定，**每次更新重新 ad-hoc 签名可能导致已授权失效、重新弹窗**。

- 需在 **Phase 2 实测**：连续两个 ad-hoc 版本之间，TCC 授权是否保留；
- 若不保留，首启引导页需说明"更新后可能需要重新授权一次"；
- 这也是 §11 里"买账号"的一个隐性收益（正式证书标识稳定）。

### 6.2 关于卸载：托管 key 的服务器侧其实已有兜底

用户直接把 `.app` 拖进废纸篓不会触发退出清理，本地会残留 LaunchAgent 与数据目录（需 `uninstall-mac.sh`）。
**但服务器侧不会积累垃圾 key**——托管 key 是 1 天租约，`RenewLeaseIfDueAsync` 只在客户端运行时执行，
客户端被删就不再续期，key 自然到期失效。**这是租约模型本来就覆盖的场景**，无需额外服务端工作。

---

## 7. UI 移植清单

| 视图 / 控件 | 行数 | Avalonia 对应 | 难度 |
|---|---|---|---|
| `SignIn` + `Registration` | — | 直译 AXAML | 低 |
| `MainWindow`（面板主体） | 519 XAML + 537 cs | 直译；`DataTemplate` / `Style` 语法有差异 | 中 |
| `SignOutConfirmationDialog` | 54 + 35 | 直译 | 低 |
| `PaymentWindow`（二维码充值） | 192 + 118 | 直译；`IQRCodeRenderer` 改返回 `byte[]` | 低 |
| `Converters.cs` | 14 | `IValueConverter` 接口几乎一致 | 低 |
| `UsageLineChart` | 841（约 185 行纯计算可复用） | Avalonia `Control.Render(DrawingContext)` 近似；`DependencyProperty` → `StyledProperty` | 中 |
| **`AnnouncementWindow` + `AnnouncementDocumentBuilder`** | 90 + 98 + 226 | ⚠️ **Avalonia 无 `FlowDocument`**，需用 `SelectableTextBlock` / `ItemsControl` + AST 自建渲染器 | **高，最贵的一块** |
| `AnnouncementImageLoader` | 331 | 缓存 / 下载逻辑可复用，`BitmapSource` → `Avalonia.Media.Imaging.Bitmap` | 中 |
| `RegistrationViewModel` | 280 | `DispatcherTimer` → `Avalonia.Threading.DispatcherTimer` | 低 |
| `PaymentViewModel` | 468 | 去掉 `BitmapSource` 依赖 | 低 |

**顺序：登录 → 面板 → 充值 → 公告。** 公告放最后——唯一需重写渲染器的部分，先做会让整个阶段看不到进展。

平台观感另需处理：窗口红绿灯按钮、原生菜单栏（`NativeMenu`）、字体（苹方 vs 微软雅黑）、深色模式。

> **🟢 遗漏 7：macOS 中文输入法（IME）需实测。** Avalonia 在 mac 上的 IME 支持历史上是弱项。
> 本产品输入字段以 ASCII 为主（邮箱、密码、金额），风险有限，但**登录页输入框的 IME 行为
> 应列入 Phase 3 出口标准**——小白遇到"打不出中文"会直接判定软件坏了。

---

## 8. 无 Mac 电脑、无开发者账号的构建与分发

> 约束（2026-08-27）：**不用 Mac 构建，不买开发者账号**。
> 结论：**"不用 Mac"完全可行；"不买账号"可行但有明确用户体验代价**，两者分开看。

### 8.1 不用 Mac 构建：完全可行 ✅

```bash
dotnet publish -r osx-arm64 --self-contained true -p:PublishTrimmed=true
```

| 事项 | 在 Windows 上怎么做 |
|---|---|
| `.app` bundle | 本质是目录结构 + `Info.plist`，Windows 上可直接组装，无需 Apple 工具。**但 `Info.plist` 的键不能只写最小集，见 §8.1.1** |
| `.dmg` | `hdiutil` 是 macOS 独有 → **改发 `.tar.gz`**，macOS 原生支持，且保留 Unix 可执行位（Windows 生成的普通 zip 会丢权限位） |
| 签名 | 见 §8.2，用 `rcodesign`（Rust 实现，Windows/Linux 可跑） |

**必须避开：不要用 NativeAOT。** 它需要目标平台原生工具链，**无法跨平台编译**。
本来就只承诺 `PublishTrimmed`（§2.3），**现正式排除 AOT**。

#### 8.1.1 `Info.plist` 必须包含 TCC 用途说明键（Phase 2 交付物）

方案里设计了 `osascript` 发通知与 `open -b` 拉起 ChatGPT，**这两件事都要经过 macOS 隐私授权（TCC）**，
而 TCC 会读 `Info.plist` 里的用途说明键。**缺键不是"用户拒绝"那么温和**，而是请求可能直接失败。

因此 `Info.plist` 至少需要：

| 键 | 用途 |
|---|---|
| `CFBundleIdentifier` | bundle 标识，也是 TCC 授权与 LaunchAgent 的身份依据 |
| `CFBundleExecutable` / `CFBundleName` / `CFBundleIconFile` | 基本身份与图标 |
| `CFBundleShortVersionString` / `CFBundleVersion` | 版本，供更新检查与 `client-version.json` 对齐 |
| `LSMinimumSystemVersion` | 最低系统版本（建议 12.0） |
| **`NSAppleEventsUsageDescription`** | **发送 Apple Events（控制 ChatGPT、osascript 通知）必需**，文案要用中文写给小白看 |

**这份键清单是 Phase 2 的明确交付物**，不是打包时顺手写写。
相应地，§6.1 的 TCC 实测要覆盖两件事：**授权弹窗是否会出现**（键写对了没有），
以及**重新 ad-hoc 签名后授权是否保留**。

### 8.2 关键陷阱：交叉编译的 arm64 产物**在苹果芯片上根本起不来**

不是"弹警告"，是**内核直接杀进程**。Apple Silicon 要求所有 arm64 可执行文件**必须带签名**（哪怕只是 ad-hoc）。
.NET 在 macOS 上构建会自动 ad-hoc 签名，**从 Windows 交叉编译时不会**
（[dotnet/sdk#34917](https://github.com/dotnet/sdk/issues/34917)、[dotnet/runtime#70780](https://github.com/dotnet/runtime/issues/70780)）。

解法不需要 Mac 也不需要账号——用
[`rcodesign`](https://github.com/indygreg/apple-platform-rs/tree/main/apple-codesign)（开源 Rust，Windows 上直接跑）做**临时签名**：

```bash
rcodesign sign "共飞-ChatGPT助手.app"
```

ad-hoc 签名**不需要任何 Apple 证书或账号**。**这一步是强制的，不是可选优化**，CI 应加一道校验。

### 8.3 不买账号：公证无法绕过

**公证需要 App Store Connect API Key，而 API Key 需要 Developer Program 会员资格**——
`rcodesign` 文档亦明确写了这点。**没有账号 = 没有公证，没有例外。**

后果具体：浏览器下载 → 文件被打 `com.apple.quarantine` → Gatekeeper 拦截。
而**自 macOS Sequoia（15）起 Apple 移除了"右键→打开"这个老捷径**，用户必须：

> 双击（被拦）→ 系统设置 → 隐私与安全性 → 点"仍要打开" → 再确认 → 输入管理员密码

**这与"小白客户端"的定位直接冲突**——被这五步劝退的正是目标用户，且流失发生在**用上产品之前**，
是最贵的流失位置。$99/年（约 ¥700）买掉的正是这个。**这是商业判断，不是技术问题。**

### 8.4 无账号下的最佳形态：绕开 quarantine，而非对抗 Gatekeeper

关键机制：**`com.apple.quarantine` 是浏览器/LaunchServices 打上去的，不是文件固有属性。**
不经浏览器下载的文件根本不会被打标，也就不触发拦截。

主路径推荐**终端一行命令**：

```bash
curl -fsSL https://<你的域名>/install-mac.sh | bash
```

脚本四步：`curl` 下载 `.tar.gz` → 解压到 `/Applications/` → `xattr -dr com.apple.quarantine`（双保险）→ 启动。
全程无浏览器参与，**用户看不到任何安全警告**，体验反而比"下载 dmg 再点开"更顺。

代价是让小白打开终端粘一行命令——配截图引导页，成本可控，**明显低于"系统设置翻五步 + 输密码"**。

> **🟢 遗漏 8：脚本必须检测架构。** Intel Mac 用户拿到 arm64 包会静默失败。
> `install-mac.sh` 应先 `uname -m`，非 `arm64` 时给出明确中文提示，而不是装完打不开。

三级分发：

| 路径 | 适用 | 体验 |
|---|---|---|
| **① 终端一行命令**（主推） | 绝大多数用户 | 无警告，最顺 |
| ② `.tar.gz` 手工下载 + 引导页 | 不愿用终端 | 需走系统设置"仍要打开" |
| ③ 将来买账号 → 公证 + `.dmg` | — | 双击即用 |

**三条路径共用同一份构建产物**，将来补账号只需在流水线末尾加两步（`rcodesign notary-submit` + staple），不返工。

### 8.5 🔴 遗漏 2：mac **每次更新**都要重撞一次 Gatekeeper

我上一轮把无账号的代价说成"首次安装的一次性成本"，**这低估了**。

核对 `ClientVersionChecker.cs`：它**不是自更新**，只是检查 `client-version.json` 并返回一个
`DownloadPage` 链接，由用户自己去下载。也就是说——

**用户每发一个新版本，就要重走一遍 §8.3 的 Gatekeeper 五步。** 不是装一次难受一次，是**每次升级都难受**。

对一个还在快速迭代（近一个月发了 4 个正式版）的产品，这个代价要乘以发版频率。

**应对（必须做，否则 mac 版的留存会被更新流程磨掉）：**

1. **`install-mac.sh` 同时作为更新脚本**——脚本本身幂等，重跑即升级；
2. 客户端检测到新版本时，**直接显示那行 `curl` 命令并提供"复制"按钮**，而不是打开下载页；
3. 与 §6.1 联动：确认 ad-hoc 签名变化是否会导致 TCC 授权失效，若会，更新提示里要一并说明；
4. 长期方向：若买账号，可上 Sparkle 式静默更新，这个问题彻底消失。

### 8.6 一个中间选项：GitHub Actions 免费 macOS runner

若只是不想**买 Mac**（而非排斥 macOS 环境），`macos-latest` runner 对公开仓库免费、私有仓库计分钟，
能跑真 `codesign` 与 `hdiutil`（可出 `.dmg`）。
**但它同样解决不了公证**——没有账号，真 Mac 上构建的产物与 `rcodesign` 交叉编译的产物在 Gatekeeper 眼里**一模一样**。
在"不买账号"前提下收益很小，不值得为它增加 CI 复杂度。

**例外**：它能解决 §9 Phase 2 的**测试**问题（遗漏 5），这一点比签名更值得考虑。

---

## 9. 分期计划

### Phase −1 — 去掉内置安装包（**半天**）

- 从 csproj `<Content Include>` 删除 `*.msix` / `*.msixbundle` / `*.appx` glob，**保留 `codex-installer/` 目录与 `README.txt`**；
- 更新 `README.txt` 中已过时的「does not download an installer automatically」；
- 真机验证"未装 ChatGPT → 自动下载 → 安装 → 接入"完整链路；
- 发小版本。
- **出口：用户下载量 825 MB → 66 MB（−92%）。**

> **已拍板**：两个平台都不内置 OpenAI 官方安装包。与 Core 抽取、macOS 前置门全部正交，不等任何决策。

### Phase 0 — 前置验证（1–2 天，不写代码）

- 回答 G-1 / G-2 / G-4（**需借一台 Mac，约十分钟**）；
- 出口：G-1 有明确答案。若为"完全不读"，§1 的产品定位需重写。

### Phase 1 — 抽取 `Core` + JSON 源生成（**4–7 天**，全程可在 Windows 验证）

- 除 4 个 Windows 文件外的服务、全部 ViewModel、公告 AST、图表计算移入 `LanAi.RelayClient.Core`；
- 新增 `Core/Platform` 接口 + `AppPaths`；
- **迁移 `LanAi.RelayClient.Server` 到源生成 `JsonSerializerContext`（§2.4，1–2 天）**——
  这是 `PublishTrimmed` 的前置，不做则 20 MB 目标不成立；
- 顺带验证 `Markdig` 的裁剪兼容性（§2.4 ③）；
- **`AiSwitch.Injection` 改多目标**（§5.1，不是改目标）；
- WPF 头改为引用 Core，**行为零变化，246 个测试全绿**。
- **无论最终一套 UI 还是两套都必须做，是性价比最高、风险最低的一步。**

> **测试拆分的实际规模比担心的小。** 实测 `grep -rln "System.Windows\|net8.0-windows" tests/`，
> 246 个测试里绑 Windows 的只有 **`PaymentViewModelTests.cs` 一个文件**（因 `BitmapSource`）
> 加上 `LanAi.RelayClient.Tests.csproj` 的 TFM。其余 245 个随 Core 平移即可。
> 拆分成本按**半天**估，已含在 4–7 天里。
>
> **另一件必须随迁的东西**：`TEST_SERVER` 条件编译目前在客户端 csproj 里
> （`dotnet publish -p:TestServer=true` 切到 `test.gongfeiai.com`），而 csproj 注释特意说明了
> 它必须是**按工程**的属性。`ClientOptions` 搬进 Core 时，`DefineConstants` 块**必须一起搬**——
> 否则测试渠道构建会**静默编译成生产地址**。这是 §9 Phase 4 检查清单里最容易漏的一条。

### Phase 2 — macOS 平台实现（4–6 天）

> ### ⚠️ 2026-08-27 估时更正：这个阶段没有 Mac 就无法按原计划收口
>
> 原估时假设每一项都能当场在 Mac 上验证。实际没有 Mac，所以 Phase 2 必须拆成两半：
>
> | | 内容 | 可验证性 |
> |---|---|---|
> | **A. 现在可做** | 接口抽取、plist 生成、`Info.plist` 键清单、纯逻辑部分 | ✅ Windows 上单测可覆盖 |
> | **B. 盲写** | Keychain P/Invoke（**已写，`SecKeychain*` 一族，约 30 行**）、`open -b`、`osascript` 通知（**已写**）、`uninstall-mac.sh`、TCC 行为 | ❌ **只能在借到 Mac 的那一次会话里验证并修** |
>
> B 部分应当**和 G-1 实测打包成同一次 Mac 行程**，且报告里必须标明"未验证"——
> 与"下载链路未实测"同等对待。盲写的代码首次真机运行大概率要改，这不是失败，是预期。
>
> **已完成（A 部分）：**
> - Phase 1 遗留的 `CodexBinding` 路径收口（见下方注记）；
> - `LaunchAgentPlist` 纯函数 + 8 个单测；`LaunchAgentStartupRegistration` 实现（B 部分，未验证）；
> - `packaging/macos/Info.plist` 完整键清单（含 `NSAppleEventsUsageDescription`）。
>
> - `ISingleInstanceCoordinator` 接口 + **跨平台文件锁实现**（`FileShare.None`，
>   Windows 上就能测，7 个单测）+ 按平台选择的工厂；`App.xaml.cs` 改为依赖接口。
>
> **未开始：** Keychain、`open -b` 启动器、`osascript` 通知、`uninstall-mac.sh`。
>
> **一条撤回的计划：`INotificationPresenter` 现在不做。** 核查后发现
> `NotifyLowBalance` / `NotifyNewAnnouncement` / `NotifyStillRunningOnce` 的调用方
> **全部在 `MainWindow.xaml.cs`（视图层），Core 侧没有任何消费者**。
> 现在抽它就是凭空造抽象，接口形状只能靠猜；等 Phase 3 有了 Avalonia 这个真实的
> 第二实现，形状自然会浮现。**留到 Phase 3 和 `TrayPresence` 一起做。**
>
> **（2026-08-29 回填：这条推迟是对的。）** 真正做的时候接口比当初能猜到的少一个成员——
> `NotifyStillRunningOnce` 根本不该进来（一次性提示用对话框更清楚），
> 而当初猜不到的那条**恰恰是最重要的**：`OnActivated` 必须写成"可能永远不触发"，
> 因为 macOS 的 `display notification` 压根没有点击回调。
>
> **单实例的取舍值得记一笔**：文件锁**只保证互斥，不做"唤起已有窗口"**。
> 这是有意的——互斥失效意味着两个客户端同时写 `~/.codex` 并争抢托管 key；
> 而唤起失效只是用户点了没反应、再点一次。前者必须对，后者可以先欠着。
> Windows 仍走原来的内核对象实现（两者都有），不因跨平台而降级。
>
> **关于 `CodexBinding` 路径**：收口时发现一个**既有的、高风险的**不一致——
> `CodexConfigWriter` 的快照根是 `%LOCALAPPDATA%\LanAi\RelayClient\codex-snapshot`（**两段**），
> 而其余一切都在 `LanAi.RelayClient`（**一段**）下。
> 那个目录存的是**用户原始 Codex 配置的备份**，即退出时把 ChatGPT 还给用户本人账号所依赖的副本。
> 挪动它不会报错，只会让用户在某天发现自己再也回不到自己的账号。
> 已按原样复现为 `AppPaths.CodexSnapshotRoot` 并加测试钉死，**统一两个根是一次迁移，不是改名**。

- Keychain、LaunchAgent、`AppPaths`、文件锁单实例、`open -b` 启动器、`osascript` 通知、`uninstall-mac.sh`；
- **实测 TCC 授权在 ad-hoc 签名更新间是否保留（§6.1）**。

> **🟡 遗漏 5：CI 只有 `windows-latest` 跑不了这些实现的测试。**
> 应对：把每项按**纯逻辑 + 薄 P/Invoke** 分层，逻辑部分在 Windows CI 上可测；
> 真机验证并入 Phase 0 那次借用 Mac 的行程。若嫌不够，`macos-latest` runner 是此处（而非签名）最值得花的地方（§8.6）。

### Phase 3 — Avalonia 头（12–18 天）

> ### ✅ 2026-08-28 实测：方案的两个核心论断都成立
>
> 在移植任何视图之前先验证了payoff，结果比估的好：
>
> | 论断 | 实测 |
> |---|---|
> | Avalonia 可裁剪 | ✅ **34.1 MB exe / 14.1 MB zip**（vs WPF 163.5 / 66.3） |
> | Windows 上可交叉编译 macOS | ✅ 产出**真正的 Mach-O ARM64** 可执行文件，42 MB |
> | Avalonia 版本 | 11.3.20，全套桌面包可还原 |
>
> **但 14.1 MB 是地板，不是终值。** 外壳只引用了 `AppPaths`，裁剪器把 Core 里没被引用的
> 代码几乎全删了。真实应用接上 ViewModel、Markdig、QRCoder 后会涨——
> 保守估计落在 **18–25 MB**，相对 66 MB 仍是 **62–73% 的降幅**。
>
> 已建 `src/LanAi.RelayClient.App/`（net8.0 + Avalonia，`AvaloniaUseCompiledBindingsByDefault=true`，
> 裁剪分析器开启），目前是可运行的空壳，视图尚未移植。
>
> **`BuiltInComInteropSupport` 保持关闭**：开启会产生 IL2026（内置 COM 互操作不支持裁剪）。
> 哪个功能真需要再开，并把那条警告当成真实代价。
>
> **裁剪又揪出第三个项目**：`CodexBinding.CodexFileSnapshot` 用反射写快照清单。
> 那份清单记录用户原本有没有自己的 `auth.json` / `config.toml`，恢复时据此决定放回什么。
> 裁剪后标志位可能序列化成默认的 `false`（"用户本来就没有配置"），
> 于是恢复变成删除，用户再也拿不回自己的账号——全程不抛异常。已接源生成并开启裁剪守卫。
>
> **一个被测试拦下的错误值得记录**：给该 context 顺手加了 `CamelCase` 命名策略，
> 而原调用 `SerializeToUtf8Bytes(manifest)` **没传 options**，存量清单是 PascalCase，
> 读取端用 `nameof(...)` 精确匹配。这一改会让**所有存量用户的快照清单判定为损坏**。
> 19 个测试当场变红。已还原并在文件里写明"没有命名策略"是刻意的。
>
> **遗留**：`AiSwitch.Injection`（CDP 注入）仍有 5 处反射 JSON（IL2026 警告）。
> 它是 Windows 专属、mac v1 不用，但 Windows 版 Avalonia 头会用到，需同样处理；
> 注意该项目与 `AiSwitch.Wpf` 共享，改动需多目标验证。

> ### ✅ 2026-08-28（同日，续）：登录视图已移植并跑通，体积落在估算区间内
>
> | 指标 | 空壳实测 | **接入登录页后实测** | WPF 版 |
> |---|---|---|---|
> | 发布目录 | 34.1 MB | **42 MB** | 163.5 MB |
> | zip | 14.1 MB | **17.6 MB** | 66.3 MB |
>
> 落在前一条预估的 18–25 MB 区间内（略好），**相对 66.3 MB 降幅 73%**。
> `osx-arm64` 交叉编译在接入真实视图后依旧产出 Mach-O ARM64（45 MB）。
>
> **已用真机截图确认渲染正确**，而不只是"进程没崩"。截图里"还没有账号？注册"与
> "忘记密码"两个按钮**可见**——它们由服务端 `/settings/public` 驱动，
> 这等于同时证明了 HTTP → 源生成 JSON → ViewModel → 编译期绑定这条链路
> **在裁剪后完好**。这正是 §2.4 担心的那种"不报错的空白"，现在有了正面证据。
>
> #### 移植前先解掉的三个前置（都不是视图工作）
>
> 1. **`ISessionStore` 只有 DPAPI 一个实现，且在 WPF 项目里。**
>    Avalonia 头是 `net8.0`，拿不到 Windows Desktop 共享框架自带的
>    `System.Security.Cryptography.ProtectedData`。**实测**该 NuGet 包（8.0.0）在
>    `net8.0` 上可还原、可在裁剪分析器 + `TreatWarningsAsErrors` 下发布干净、
>    可正确往返，且 `osx-arm64` 发布同样成功。阻塞解除，两个 DPAPI 实现已移入
>    `Core/Platform/Windows/` 并标注 `[SupportedOSPlatform("windows")]`。
>
> 2. **`RegistrationViewModel` 用 `System.Windows.Threading.DispatcherTimer`** ——
>    这是 ViewModel 层最后一个 WPF 类型。已抽象为 `IUiTimer` + `UiTimerFactory`
>    （两个头各给一个 dispatcher 实现），ViewModel 移入 Core。
>
> 3. **`ClientOptions`（服务器地址）原本长在 WPF 的 `App.xaml.cs` 里**，两个头都要用。
>    已移入 Core，并在 Core 的 csproj 补上同样的 `TestServer` 条件组
>    ——**否则 `-p:TestServer=true` 会构建出仍指向正式服的客户端**。
>    两个方向都已实测校验（`test.gongfeiai.com` / `https://gongfeiai.com/` 各自入包，互不串味）。
>
> #### 🔴 macOS 会话存储：**刻意选择"拒绝启动"而不是明文兜底**
>
> `SecureStorage.CreateSessionStore()` 在非 Windows 上**抛 `PlatformNotSupportedException`**。
> 看似方便的做法——Keychain 没写完之前先明文落盘——会把**可用的 access token 与
> refresh token 以明文写进用户主目录**，同机任何以该用户身份运行的进程都能读走，
> 而客户端不会有任何提示。那是"以占位符名义发布的凭据泄露"。
> 拒绝启动是诚实的失败：立即、且到不了用户磁盘。
>
> ⚠️ 但**在 macOS 上它并不"响亮"**：GUI 应用在建出窗口之前抛异常，
> 表现是 Dock 图标弹一下就消失，唯一痕迹是 `AppDomain.UnhandledException`
> 写进 `ClientLog`。选择本身仍然正确，只是接 Keychain 之前，
> mac 上的失败形态是"启动不了且看不出为什么"，需要在 Phase 2 补一条可见提示。
>
> #### 🟡 Avalonia 没有 `PasswordBox` —— 一个会静默倒退的安全点
>
> 替代物是 `<TextBox PasswordChar="●">`，而 **`TextBox.Text` 是可绑定属性**。
> 顺手写成 `Text="{Binding Password}"` 会编译通过、界面正常、测试全绿，
> 却**推翻 `SignInViewModel` 明文承诺的那条不变量**（密码不进可绑定属性、
> 不进变更通知、不进该对象的堆转储）。已保持**不绑定**，提交时从控件读取，
> 并在 AXAML 里写明原因。
>
> #### 移植中删掉的东西（WPF → Avalonia 的净简化）
>
> | WPF | Avalonia |
> |---|---|
> | `Visibility` + `BooleanToVisibilityConverter` | `IsVisible="{Binding X}"`，转换器消失 |
> | `{Binding X, ElementName=RootWindow}` | 随三面板合一而消失 |
> | 三个面板共用一个窗口、代码里切 `Visibility` | 每个界面一个 `UserControl`，`ShellWindow` 换内容 |
>
> **为什么必须拆成 `UserControl`**：编译期绑定**信任 `x:DataType` 标注，
> 不校验运行时真正赋进去的 `DataContext`**。三面板写法里 DataContext 是
> 代码后置赋的，标注错了不会报错，只会整片空白——正是本项目最怕的那类失败。
> 拆成 UserControl 后，类型写在构造函数签名上，由 C# 编译器把关。
>
> #### 顺带补上的测试（3 个此前**不可能**测到的）
>
> `RegistrationViewModel` 的倒计时——每秒递减、归零停表、重发按钮恢复——
> **此前一行都没被测到**：`DispatcherTimer` 在 xUnit 下没有 dispatcher 循环，永远不 tick，
> 原测试只断言了服务端回传的初始秒数。改为注入后用 `FakeUiTimer` 手动驱动，
> 逻辑本身验证无误（无潜藏 bug），但从此被钉住。
> 另加 2 个 `StoredSession` 源生成回归测试（缺字段不得变 null、磁盘字段名必须保持 snake_case）。
>
> #### 已知缺口（已在代码内就地记录，不留在待办清单里）
>
> - `SignInView` 的 `SignedIn` / `RegistrationRequested` 两个事件**尚无订阅者**
>   ——面板与注册页都还没移植。登录会成功，然后界面看上去没反应。
>   截图里那个"注册"按钮是能点的，所以这条必须写在代码旁边。
> - Avalonia 无 `DispatcherUnhandledException` 对应物。已补 `NoticeDialog`：
>   所有点击都经 `SafeAsyncRunner`，它本来就会记日志，现在还会弹提示
>   ——否则"点了没反应"这类反馈将不带任何可追查信息。
>   仍未覆盖的是**不经该 runner、直接在 UI 线程同步抛出**的故障。
>
> **当前状态**：解决方案 0 警告 0 错误，**441 个测试全绿**（Server 92 / CodexBinding 53 / Client 296）。
> WPF 版发布后实测仍能正常启动，未因这些移动而回归。
>
> **下一个视图**：面板（Dashboard）——最大的一个，且要连带处理 `UsageLineChart`
> 自绘控件与 `TrayPresence` 的跨平台替代。
>
> ### ✅ 2026-08-28（三）：图表数学已补测并做成共享源码
>
> #### 🔴 先说发现：`UsageLineChart` 816 行**一个测试都没有**
>
> 其中约 185 行是纯数学——决定每个点落在哪里：15% 顶部余量、单点居中、
> 贝塞尔控制点偏移、标签抽稀。**图表算错了仍然会画出一条看着很合理的线**，
> 这正是移植最容易静默弄坏的那类代码。讽刺的是原作者的注释写着
> "Keeping scaling here makes the visual behavior unit-testable"——意图有了，测试没写。
>
> **已补 26 个测试**，钉住当前行为（而非理想行为）。其中一个**测出的是我自己写错的期望值**：
> 8 个点抽 5 个标签的结果是 `[0,2,4,5,7]` 而不是我以为的 `[0,2,3,5,7]`
> ——`Math.Round(3.5)` 走银行家舍入得 4。间距因此是 2/2/1/2，并不真的均匀，已照实钉住并注明。
>
> #### ✅ 数学层做成共享源码，**无需重写、无需自造几何类型**
>
> 原打算在 Core 里自造 `ChartPoint`/`ChartRect` 等原语，然后把 185 行逐行改写。
> **否决了**：那等于把没有测试的算术重写一遍，新测试钉住的将是重写结果而非用户当前的行为。
>
> 实际做法：抽到 `src/Shared/UsageLineChartLayout.cs`，两个头各自
> `<Compile Include>` 进来，靠 `UI_WPF` / `UI_AVALONIA` 符号选择
> `using System.Windows;` 还是 `using Avalonia;`。
>
> **已实测通过**：WPF 与 Avalonia 的 `Point` / `Rect` / `Size` / `Thickness`
> 同名、同为 double、构造签名一致，**同一份源码在两个框架下都编译干净**。
> Core 引用不了任何 UI 框架，所以放不进 Core；共享源码是这里唯一不重复算术的办法。
>
> ⚠️ **代价要写明**：测试跑的是编译进 WPF 头的那一份。Avalonia 头编译的是同一份源码但
> 绑的是 Avalonia 的几何类型——若两个框架在**语义**而非命名上有差异，这里测不出来。
> 因此该文件刻意只用最朴素的成员。
>
> #### 顺带完成
>
> `StartupRegistration`（Windows 注册表）移入 `Core/Platform/Windows/` 并更名
> `WindowsStartupRegistration`，新增 `StartupRegistrations.Create()` 工厂
> （Windows 注册表 / macOS LaunchAgent / 其他回落到 `Unsupported`）。
> **实测 `Microsoft.Win32.Registry` 在 `net8.0` 上无需任何包引用**（在共享框架里），
> 且 `osx-arm64` 发布同样成功。
>
> 注意此处**故意与 `SecureStorage` 不对称**：未知平台这里回落而不抛异常。
> 存不了会话意味着凭据要落到不安全的地方；注册不了开机启动只是用户自己点一下。
> 一个是安全决定，一个是便利问题。
>
> **当前**：0 警告，**467 个测试全绿**（+26），裁剪发布干净。
>
> #### 面板的下一步：Windows 优先
>
> `DashboardViewModel` 的平台相关依赖**全部是接口**（`ICodexStartup` /
> `ICodexInstaller` / `ICodexAccountStore` / `IStartupRegistration`），具体实现现已都在 Core，
> 所以**在 Windows 上可以完整接线并截图验证**。macOS 上 `BuildShell` 会先在
> `SecureStorage` 拒绝，根本走不到面板。
>
> ⚠️ 因此要说清楚：**"面板移植完成"≠"mac 客户端快好了"**。
> 在 Phase 2 的 macOS 工作（Keychain）落地之前，Avalonia 头仍是 Windows-only。
>
> 剩余的约 630 行绘制代码是**真正的重写**，不是命名空间替换：
> `FormattedText` 构造签名不同、`StreamGeometryContext` 方法名与参数不同、
> `Pen`/`Brush`/`DrawRectangle` 重载不同。这部分无法用单元测试覆盖，需按重写预算。

> ### ✅ 2026-08-28（四）：面板 + 图表已移植并截图验证
>
> **确认口径（你 2026-08-28 拍板）**：mac 只是**不需要 CDP 注入浮层**，其余功能均按正常实现。
> 因此面板未做任何平台门控，一份 AXAML 两个平台共用。
>
> | | 登录页 | **+ 面板 + 图表 + 注入** |
> |---|---|---|
> | zip | 17.6 MB | **17.8 MB** |
>
> 加了整个面板、自绘图表和 CDP 注入只涨 0.2 MB —— 裁剪确实在起作用。
>
> #### 🔴 接入注入后，那 5 处 IL2026 从"遗留"变成"阻塞"
>
> 之前记录的 `AiSwitch.Injection` 反射 JSON 一直没影响，是因为没人把它拉进裁剪发布。
> 面板接上 `RelayInjectionHost` 后，`NETSDK1144 优化程序集大小失败`，发布直接断掉。
>
> 修法**不是**加源生成 context，而是**把反射整个去掉**：
>
> | 位置 | 原来 | 现在 |
> |---|---|---|
> | `CdpConnection` 请求信封 | `Serialize(Dictionary<string, object?>)` | `JsonObject` 直接 `ToJsonString()` |
> | `Runtime.evaluate` / `addScriptToEvaluateOnNewDocument` 参数 | 匿名类型 | `JsonObject` |
> | `CoflyOverlayInjector.PushStateAsync(object)` | `Serialize(object)` | 签名改收 `JsonNode` |
> | `/json/version`、`/json/list` 两处读取 | `GetFromJsonAsync<T>` | `CdpJsonContext` 源生成 |
>
> `Serialize(object)` 那处是唯一没法用源生成救的——裁剪器无从知道该保留哪些类型。
> 改签名收 `JsonNode` 后调用方显式写出键名，反而让浮层脚本依赖的字段名在调用点可见。
>
> **65 个注入测试全绿**，其中包含断言线上字节格式的用例
> （`params.source`、`"tokens":42`），等于证明了 JSON 输出未变。
> `LanAiWorkspace.sln`（AiSwitch.Wpf 那个应用）**同样 0 警告 0 错误**，多目标未受波及。
>
> #### 📷 截图验证（不是"进程没崩"）
>
> 1. **面板接真实账号数据渲染正确**：欢迎语、余额 ¥1.6996、分组 plus-free 0.060x、
>    ComboBox 展开项（当前使用中 / 说明 / 倍率说明）全部正确。
> 2. **`DataTrigger` 的替代方案确认可用**：公告铃铛因有未读而呈蓝色加粗
>    （`Classes.unread`），"去充值"因余额不低而保持灰色（`Classes.low` 未命中）。
>    Avalonia 没有 `Style.Triggers`，这套 `Classes.x="{Binding bool}"` 是等价替代。
> 3. **图表单独搭 harness 渲染验证**：该账号近 7 日无用量，`HasTrend` 为假、图表被隐藏，
>    所以面板截图证明不了图表。另建探针喂入合成数据后确认：贝塞尔平滑、渐变面积、
>    虚线网格、末点光晕、**悬停竖线 + 提示卡**均正常；标签抽稀实际显示
>    5 个（8/22、8/24、8/25、8/26、8/28），与 `GetLabelIndices` 的测试预期逐个吻合。
>
> #### 移植中修正的一处历史行为
>
> WPF 版在构造函数里调 `MoveRecentSpendCardToEnd()`，把"近 7 日消费"卡片
> 从 XAML 声明位置搬到最后——**markup 顺序与用户实际看到的顺序一直不一致**。
> AXAML 直接按真实渲染顺序声明，那段运行时重排随之删除。
>
> #### 已知缺口（已就地记录）
>
> - 「去充值」「公告」→ 弹窗说明"尚未移植"，而不是死按钮（充值/公告是后两个视图）；
> - 「退出」的第三个选项"最小化到托盘"暂缺，因为托盘还没移植，现降为普通二次确认；
> - 低余额提醒需要托盘气泡，暂时只取观测值不弹提示（否则托盘接上后会立刻误报）；**→ 2026-08-29 已接上 `INotificationPresenter`**；
> - `DashboardView.Start()` 做了幂等保护：恢复会话会同时触发 `StateChanged` 与显式调用，
>   不防会变成每轮轮询都跑两遍。
>
> **当前**：0 警告，**467 + 65 = 532 个测试全绿**，裁剪发布干净，WPF 版与工作台应用均未回归。

> ### ✅ 2026-08-28（五）：充值与公告已移植，Phase 3 主体完成
>
> 四个视图全部移植完毕：**登录 → 面板 → 充值 → 公告**。zip **18.0 MB**（WPF 版 66.3 MB，降 73%），
> `osx-arm64` 交叉编译仍产出 Mach-O ARM64。
>
> #### 充值：一处 WPF 耦合，删掉即可
>
> `PaymentViewModel` 468 行里只有一行是 WPF —— `BitmapSource? qrCodeImage`。
> `IQRCodeRenderer.Render` 改为返回 **PNG 字节**（`PngByteQRCode.GetGraphic` 本来就产出字节，
> WPF 版只是在外面包了一层），两个头各自解码两行。ViewModel 与 QR 渲染器随即整体移入 Core。
>
> 注意 `QRCoder` 要用 `PngByteQRCode` 而**不是** `QRCode` —— 后者走 `System.Drawing.Common`，
> 从 .NET 6 起是 Windows 专属，会把平台依赖塞进支付链路正中间。
>
> WPF 的 `DataTrigger Binding="{Binding Type}" Value="wxpay"`（微信绿）改为 VM 上的
> `IsWeChat` 具名属性 + `Classes.wxpay`。**这不是装饰**：一个显示微信绿却打开支付宝的按钮，
> 会让用户从错误的钱包付款。
>
> #### 公告：唯一需要重写渲染器的部分
>
> Avalonia 既无 `FlowDocument` 也无 `FlowDocumentScrollViewer`。新写
> `AnnouncementContentBuilder`，从同一份 AST 产出普通控件栈。
> **WPF 版的每个视觉决策都照抄**（标题 20/17/15.5/14.5、引用 3px 竖线 #D1D5DB + #4B5563 文字、
> 代码底色 #F3F4F6、图片 420px 上限、alt 文字先显示），这样两个阅读器呈现运营写的公告是一致的。
>
> #### 🔴 图片加载：一个差点被推给两个头的保证
>
> `IAnnouncementImageLoader` 原本返回解码后的 `BitmapSource`，"不是图片"这个判断是**免费**得到的
> —— 解码失败即返回 null。改为返回字节后，这个保证就得在**每个头**里各实现一遍，
> 而漏掉的那个会在必须能打开的阅读器里抛异常。
>
> 测试当场变红（`ABodyThatIsNotAnImageFailsToNullRatherThanThrowing`）。
> **没有改测试,而是把保证放回共享层**：在 loader 里做图片magic byte 签名校验
> （PNG/JPEG/GIF/BMP/WebP —— 取两个框架都能画的交集，只有一个支持的格式会在 Windows 正常、
> 在 macOS 静默失败，那比两边都拒绝更糟）。原测试遂原封不动地通过。
> 附带收益：运营撰写的任意字节不再直接喂给图像解码器。
>
> #### 🟡 渲染实测揪出一个单元测试永远看不到的缺陷
>
> Avalonia 的 `Run` 没有指针事件、也没有 `Hyperlink` 内联。第一版把链接塞进
> `InlineUIContainer` —— 能点，但**托管控件不落在周围文字的基线上**
> （`Baseline` 与 `TextBottom` 都试过，都不行），每个链接都浮高几像素，
> 且其文字脱离了整块的选择范围。
>
> 改为**对排版后的文本做命中测试**：链接仍是普通 `Run`（基线正确、可选中可复制），
> 点击位置经 `TextLayout.HitTestPoint` 换算成字符下标，再映射回对应的链接区间。
> 两个问题一并解决。**这个缺陷是靠渲染样张发现的，任何单元测试都看不到内联排版。**
>
> #### 验证手法的改进：不再截屏，直接渲染成位图
>
> 合成鼠标点击打不到 Avalonia 的按钮，且屏幕截图会受 z-order 影响
> （两次拍到的是窗口背后的内容）。改用 `RenderTargetBitmap` 直接把窗口内容画进 PNG——
> 与 z-order、前台窗口、显示缩放全都无关，也不会把无关的桌面内容摄入。
>
> 已用此法确认充值窗口：3 列快捷金额网格、水印输入框（替代 WPF 的 `ControlTemplate` 技巧）、
> 费用明细、以及**禁用态样式**（`Button.pay:disabled`）均正确。
>
> #### 仍未移植
>
> - **托盘**（`TrayPresence`，WinForms `NotifyIcon` → Avalonia `TrayIcon`）——
>   连带影响：退出对话框的"最小化到托盘"选项、低余额气泡提醒；
> - **注册页**（`RegistrationViewModel` 已在 Core，只差视图）；
> - 微信绿按钮变体未获实测（该账号服务端只提供支付宝）。
>
> **当前**：0 警告，**473 + 65 = 538 个测试全绿**，裁剪发布干净，两个平台均可发布。

> ### ✅ 2026-08-28（六）：托盘补齐，已出 Windows 预览测试包
>
> #### 🔴 打包前发现的阻塞项：关闭按钮会杀掉中转
>
> | | 点窗口关闭按钮 |
> |---|---|
> | WPF 版 | `Hide()` 最小化到托盘，客户端继续运行 |
> | Avalonia 版（补托盘前） | **进程退出** —— Codex 随即失去中转 |
>
> 面板上明写着「保持客户端运行，ChatGPT 才能继续使用共飞额度」，
> 而窗口自己的关闭按钮违反了这句话。**不是数据损失**
> （`ReleaseBeforeProcessExit()` 会阻塞到清理完成，原始 Codex 配置照常恢复、托管 key 照常回收），
> 但对测试者是纯粹的困惑。因此托盘从「Phase 3 之后」提前到「打包之前」。
>
> #### 托盘：菜单与图标照搬，气泡不照搬
>
> Avalonia `TrayIcon` + `NativeMenu`，菜单项、状态行、图标、双击唤起均与 WinForms 版一致，
> 并沿用**同一个** `tray-tip-shown` 标记文件——从 WPF 版升级过来的用户不会被再提示一次。
>
> **气泡通知没有照搬**：Avalonia 的 `TrayIcon` 没有通知 API，
> 而两个平台的路径完全不同（Windows 要 `Shell_NotifyIcon`，macOS 菜单栏根本没有气泡，
> 需 `osascript` 或 `UNUserNotificationCenter`）。与其写一条 Windows 专属路径、
> 让 macOS 静默地没有，不如按信息量分开处理：
>
> - 首次最小化提示 → 改为**一次性对话框**（只出现一次的东西，对话框其实比气泡更清楚）；
> - 低余额提醒 → **暂缺**，与通知抽象一起延后；余额仍在卡片上，观测仍在进行。
>
> **（2026-08-29 回填：通知已按抽象补齐，见当日条目。）** 结论没变——
> 气泡确实不该长在 `TrayPresence` 里，因为 Avalonia 的 `TrayIcon` 连窗口句柄和图标 id 都不给。
> 变的是"暂缺"：现在客户端自己注册一个**隐藏的**通知项来发通知。
>
> #### 📦 已出包：`artifacts/Ver0.1-预览-Avalonia-20260828/`
>
> | | 正式版（WPF） | **本预览包** |
> |---|---|---|
> | 单文件 exe | 163.5 MB | **37.8 MB** |
> | zip | 66.3 MB | **15.5 MB** |
>
> 单文件用 `IncludeNativeLibrariesForSelfExtract=true`，
> 否则 Avalonia 的三个原生库（Skia / HarfBuzz / ANGLE）会留在 exe 外面。
> 已核验：**正式服地址入包、测试服地址不在包内**；打包产物实测可启动、关窗即最小化到托盘。
>
> **刻意与正式包区分**：目录名与 exe 名都带「预览」，且**不含快捷方式脚本**——
> 那两个脚本按「同目录下第一个非 Setup 的 exe」定位客户端，
> 混进来会覆盖正式版已注册的桌面/开始菜单快捷方式。
>
> 包内附 `预览版说明.txt`，写明三处尚未移植（注册页、气泡通知、"最小化到托盘"选项），
> 以及**不能与正式版同时运行**（两个客户端会争抢 `~/.codex`，单实例锁本身也会挡住）。
>
> #### 仍未移植
>
> - ~~**注册页**（`RegistrationViewModel` 已在 Core，只差视图）~~ → 2026-08-29 已补；
> - ~~**气泡通知**（需 `INotificationPresenter`，两平台各一实现）~~ → 2026-08-29 已补；
> - 微信绿支付按钮变体未获实测（该账号服务端只提供支付宝）。

> ### 🔴 2026-08-28（七）：预览包实测「启动 ChatGPT」失效 —— 我自己埋的雷
>
> **现象**：点「启动 ChatGPT」没反应。日志给出确切原因：
>
> ```
> System.NotSupportedException: Built-in COM has been disabled via a feature switch.
>    at LanAi.Workspace.Injection.CodexAppLauncher.Activate(String, String)
> ```
>
> `CodexAppLauncher` 通过 COM 的 `IApplicationActivationManager.ActivateApplication`
> 激活 MSIX 版 ChatGPT。而 Avalonia 头的 csproj 里写着
> `BuiltInComInteropSupport=false`，附带注释：
> *"Turn it on only if a feature actually needs it."* —— **核心功能就需要它。**
>
> 当初关掉它是为了消掉一条 IL2026。代价是客户端丢了主功能。
>
> #### 失败形态值得记录
>
> 日志显示流程走到了**很后面**才炸：
>
> ```
> INFO   签发新授权
> INFO   已写入 ChatGPT 配置，授权 259     ← ~/.codex 已被换掉
> ERROR  启动 Codex 失败                    ← 到这里才失败
> ```
>
> 也就是说：**中转 key 已签发、用户的 `~/.codex` 已被替换，然后才启动失败**。
> 退出时 `已撤销托管授权 259` / `已恢复用户原始 Codex 配置` 正常执行，所以没有留下烂摊子，
> 但"看起来一切正常直到最后一步"正是本项目反复出现的失败形状。
>
> #### 修法：开 COM，但**只降级链接器那一条**警告
>
> 开启 `BuiltInComInteropSupport` 会让运行时自身的 `ComActivator` 产生一条无法避免的
> IL2026（"Built-in COM support is not trim compatible"）。那条警告说的是
> **COM 服务端激活**（把托管类暴露为 COM 对象）；本客户端只**消费**一个 Windows 内置
> COM 类，接口带显式 GUID 且被直接引用，会被裁剪器保留。
>
> **没有用 `NoWarn`**：`$(NoWarn)` 会同时流进编译期裁剪分析器和链接器，
> 那样会把本项目自己代码里的 IL2026 一并屏蔽 —— 而本次移植中最严重的三个 bug
> （反射 JSON）全都是自己代码里的 IL2026，且全部是运行时静默失败。
>
> 改用 `ILLinkTreatWarningsAsErrors=false`，**只降级链接器那一趟**：
> 编译期分析器对自己代码依然报错。发布输出必须人工过一眼——
> 任何提到 `LanAi.*` 的 IL2026 都是真缺陷，不是这一条。
>
> #### 已验证
>
> - 发布产物 runtimeconfig：`BuiltInComInterop.IsSupported = True`；
> - 用**完全相同的裁剪设置**写了探针，实测那个先前抛异常的
>   `new ApplicationActivationManager()` 现已成功（异常正是从这一步抛出的）；
> - 发布输出只剩 `ComActivator` 一条 IL2026，不含任何 `LanAi.*`；
> - 重新出包，实测可启动、正式服地址入包、测试服地址不在包内。
>
> **教训**：`BuiltInComInteropSupport=false` 当时消掉的是一条警告，
> 埋下的是主功能失效。**关掉一个功能开关来消警告，等于把编译期问题换成运行时问题。**

> ### ✅ 2026-08-28（八）：新旧两个包互斥 —— 已实测，并补上缺失的一半
>
> **结论：两个包互斥，只能运行一个，双向都成立。**
> 两个头共用同一对内核对象名（`Global\LanAi.RelayClient.SingleInstance` /
> `...Activate`），这是刻意的：**两个客户端必须互相排斥，而不是各自排斥自己**——
> 同时运行会各写一次 `~/.codex`、各自争夺托管 key 的归属。
>
> | 实测 | 结果 |
> |---|---|
> | 先 WPF，后 Avalonia | WPF 存活，Avalonia 退出 |
> | 先 Avalonia，后 WPF | Avalonia 存活，WPF 退出 |
>
> #### 🟡 但"退出"只是互斥的一半，另一半我漏了
>
> Avalonia 头的单实例回调当初写的是 `activate: () => { }` —— **空的**。
> 于是当 Avalonia 版在托盘里运行、用户去双击 WPF 版时：
> WPF 静默退出、日志写下"已请求现有客户端显示主界面"，
> 而**正在运行的那个客户端什么也不做**。用户双击图标，屏幕上毫无反应。
>
> 又是同一个失败形状：日志说成功了，用户看到的是没反应。
>
> 已接上 `RaiseExistingWindow()`（在监听线程收到信号，Post 到 UI 线程唤起窗口）。
> 实测完整链路：Avalonia 隐藏在托盘 → 启动 WPF → WPF 退出 → **Avalonia 窗口被弹到前台**。
>
> 包内 `预览版说明.txt` 已改为描述这个实测行为，而不是先前那句推断的"不能同时运行"。

> ### 🟡 2026-08-29：「退出」按钮语义修正 —— 我把三个含义压成了一个
>
> **反馈**：点「退出」的提示不对，应该二次确认「完全退出助手」还是「最小化到托盘」，
> 选完全退出就该结束进程。
>
> #### 我当初的简化是错的
>
> WPF 版有 `SignOutConfirmationDialog`，三选一（退出账号 / 最小化到托盘 / 取消）。
> 移植面板时托盘还没做，我把它降成了一个是非确认框——问的是「要退出登录吗？」。
>
> **这个按钮有三种合理含义，而且互不可替代**：
>
> | 用户想的 | 得到的（简化后） | 代价 |
> |---|---|---|
> | 把窗口收起来 | 退出登录 | 要重新找密码登录 |
> | 停掉助手 | 退出登录，助手仍在跑 | **仍在计费** |
> | 换个账号 | 正确 | — |
>
> 两个方向都错，而且错得不对称：一个是麻烦，一个是继续花钱。
>
> #### 现在的行为
>
> 新建 `ExitConfirmationDialog`，面板「退出」与托盘「退出」**走同一条路**：
>
> - **完全退出助手** —— 释放托管授权、恢复用户原始 Codex 配置，然后结束进程；
> - **最小化到托盘** —— 默认按钮，助手继续中转；
> - **退出登录（切换账号）** —— 次要位置的文字按钮。
>
> 关于第三项：反馈里只要求两个选项。**保留它是因为这是全客户端唯一的切换账号入口**，
> 删掉会让有两个账号的用户没有出路；但它被放在次要位置，不与那两个主选项争。
> 若不需要可以去掉。
>
> 提示文案按 `IsCodexRunning` 分两种写法——ChatGPT 正在跑时，这个选择的代价不一样。
> **关掉对话框什么也不做**：一个关于"要不要退出"的问题，其关闭按钮绝不能被当成答案。
>
> #### 「杀进程」已实测
>
> 用临时钩子直接走真实 quit 路径：**进程结束，退出码 0**。
>
> 一并核对了退出耗时——不慢。有 Codex 在跑时的真实日志：
> `已撤销托管授权 259` → `已恢复用户原始 Codex 配置` → `客户端退出` 共 **23 毫秒**。
> （首次测出的"10 秒"是启动耗时加钩子延迟，不是退出。）
>
> #### 一条实现约束
>
> 面板退出与托盘退出**共用一个 `QuitAsync()`**，不是各写一遍。
> 两份实现迟早会不一致，而写错的那份会让用户的 ChatGPT 指向一个已被吊销的 key。

> ### ✅ 2026-08-29（二）：注册页与通知抽象已补，Windows 侧与 WPF 版对等
>
> 一次平行对账（点击处理器 / 绑定 / 托盘公开方法三路交叉）给出的缺口只有两类，本日全部补齐。
>
> #### 注册页：照搬之外修了一个 WPF 版的真缺陷
>
> `RegistrationViewModel` 本就在 Core（已有 12 个单测），只差视图。
> 密码与确认密码沿用登录页的做法：`PasswordChar` 的 `TextBox`，**`Text` 不绑定**，
> 提交时从控件读——`SubmitAsync(password, confirm)` 取参数就是为了这个。
>
> **渲染验证抽出了一个编译不报的缺陷**：把服务器端可开关的字段全部打开
> （验证码 + 邀请码 + 优惠码）再加错误行与 Turnstile 提示，表单高 **768px**，窗口只有 720px。
> **WPF 版直接把「返回登录」切在窗外**，误进注册页的用户没有路回去。
> Avalonia 版包了 `ScrollViewer`——**这个缺陷只有渲染才能发现，构建和测试都不会报。**
>
> 另两处顺手收口：
>
> - 倒计时文案改成视图模型上的 `VerifyCodeCountdownText`（原本是 XAML 里的 `StringFormat`）。
>   中文 + 花括号的格式串穿过 markup 是**静默空白**而非报错；现在它有测试。
> - `signInView.SurfaceLoaded` 事件：以前「重新获取配置」重新拉了 surface，
>   却**没有任何人重新 `ApplySettings`**——面板一直是这个毛病，注册页接上去会一起中招。
>   现在两个消费方走同一个 `ApplyEffectiveSettings()`。
>
> #### 通知：先探针，再定接口
>
> 接口形状取决于 Windows 究竟有没有路，所以**先写探针**。
> 验证手段是 `NIN_BALLOONSHOW` 回调，不是截屏——通知真的显示了才会收到它，
> 而且不需要拍用户的桌面。
>
> **探针第一轮四个变体全部「未显示」——读数是错的。**
> 对照实验（WinForms `NotifyIcon`，即 WPF 版用的那一套）当场就亮了，
> 证明不是本机关了通知。真因：**shell 是用 `SendNotifyMessage` 语义派发回调的**，
> 它直接进窗口过程，**不会以 `MSG` 形式从 `PeekMessage` 返回**。
> 改在 `WndProc` 里记录后，四个变体全部通过。
> ——这正是对照实验的价值：没它我会把「探针写错」当成「Windows 不支持」写进结论。
>
> **实测结论（Windows 11 26200）**：带 `NIS_HIDDEN` 的通知项**照样能弹通知**。
> 所以客户端自己注册一个**隐藏的**通知项，用户托盘里仍然只有 Avalonia 那一个图标。
> 不去碰 Avalonia `TrayIcon`（它不暴露 `hWnd` / `uID`，而反射取值正是本项目到处在删的东西）。
>
> #### 接口的形状，与当初猜的不同
>
> | | 当初以为 | 实际 |
> |---|---|---|
> | 成员数 | 3（照搬 `Notify*`） | **1 个 `Show`** |
> | 首次最小化提示 | 走通知 | 不走——一次性对话框更清楚 |
> | 点击回调 | 理所当然有 | **写进契约：可能永不触发** |
>
> 最后一行是重点。macOS 的 `display notification` **没有任何点击回调**，
> Windows 在专注助手下也会静默抑制。因此接口文档里写死：
> **通知只能是快捷方式，不能是唯一通道**——未读角标与余额卡片才是。
>
> #### macOS 侧：转义是安全边界，不是格式问题
>
> 公告标题是**运营者写的、从网络来的**，而它要落进 AppleScript 字符串字面量。
> 一个未转义的引号就能闭合字面量，后面的东西会被当脚本执行。
> 因此：反斜杠先于引号转义（反过来就是漏洞本身），脚本走 `ArgumentList` 而不经 shell。
> 这半截**可以在 Windows 上测**，已单独拆出 `AppleScriptNotification` 并补 5 个测试（含注入用例）。
> `OsaScriptNotificationPresenter` 本体仍属 **§9 "B. 盲写"**，需真机验证。
>
> #### 核验
>
> 不只验了探针，还拿 **Core 里真正要发的那个类**跑了一遍：
> `Shell_NotifyIcon` 全部返回成功、日志无警告（`cbSize` 错了它会返回 false，
> 所以这同时钉住了 `ByValTStr` 结构体布局）。
> 注册页则在**真正的 `ShellWindow` 里**渲染，而不是随手套一个 `Window`——
> 这两者的高度约束路径不同，用后者验会把仍然溢出的布局判成通过。
> **334 个测试全绿，裁剪发布无新增警告**（只剩已知那条 COM 的 IL2026），
> win-x64 与 osx-arm64 双目标均通过，mac 产物核到 Mach-O ARM64 魔数。
>
> **没验到的一处，写明白**：`OnActivated` 的点击回调**未实测**——
> 没有办法把一次真实点击合成进 shell 的回调。
> 已验的是注册、显示、结构体布局；**点击是推理，不是实测**。
>
> #### 两个自查出来的隐患（已修）
>
> - **窗口类没有注销**。`RegisterClassExW` 返回 0 原本被当成"已注册，无害"。
>   实际不是：残留的注册里存着**上一个 presenter 的窗口过程 thunk**，
>   在它上面建窗口会派发进一个 GC 可能已经回收的委托——**硬崩溃，而且崩在点击那一刻**。
>   现改为 `Dispose` 时 `UnregisterClassW`（必须在 `DestroyWindow` 之后），
>   并已用"释放后重建"的用例实测。
> - **截断可能劈开代理对**。长度上限是 UTF-16 单位，
>   公告标题里一个 emoji 就够踩到；中文永远踩不到，所以人工检查永远发现不了。
>   现在切点会从低位代理往前退一格。
>
> #### 界面线程崩溃处理器：我之前的记录是错的
>
> 此前文档与代码注释都写着"Avalonia 没有 `DispatcherUnhandledException` 的对应物"。
> **这条是错的。** 它在 `Dispatcher` 上，不在 `Application` 上——
> `Dispatcher.UIThread.UnhandledException`，连 `Handled` 标志的形状都与 WPF 一致。
> 当初只看了 `Application` 就下了结论。
>
> 已补上，并**实测**：向 UI 线程 `Post` 一个同步抛出，处理器触发、`Handled = true` 之后进程存活。
> 这一步不能省——**接好但从不触发的崩溃处理器，和好用的那个在崩溃发生前长得一模一样。**
>
> 它盖住的是 `SafeAsyncRunner` 盖不到的那部分：布局、渲染、绑定回调里的同步异常。
> 不接的话，一个可能只是外观问题的 bug 会直接结束进程，连带把中转也带走。
>
> #### 🔴 轮询间隔在移植中被我改小了 —— 逐项对账查不出来的那一类
>
> | | WPF | Avalonia（移植后） | 倍数 |
> |---|---|---|---|
> | 卡片刷新 | 60 秒 | **30 秒** | 2× |
> | 公告检查 | 15 分钟 | **5 分钟** | 3× |
>
> **WPF 那两个值不是偏好，是预算。** 源码里写得很清楚：面板接口在**每用户限流器**后面，
> 60 秒"与其说是新鲜度目标，不如说是预算"；公告接口每次调用都跑一次每用户订阅查询，
> 而"网页面板根本不轮询——它在 20 分钟节流下按导航重取"。
>
> 我在移植时把它们重新敲了一遍，敲小了，没有任何理由。后果是**每个客户端对限流器的调用率翻倍**，
> 用户换来的不是更快，而是面板上那条 `IsRateLimited` 提示。已改回 60 秒 / 15 分钟，并把原因注释一并搬过来。
>
> **这一条值得记方法论**：之前三路对账（点击处理器 / 绑定 / 托盘公开方法）**发现不了它**——
> 名字全对、结构全对，只有常量的值不同。
> 逐项对账能证明"东西都在"，不能证明"东西一样"。
>
> #### 顺手发现，未修
>
> 重复启动时，第二个进程在成功唤起已有窗口之后，**以未处理异常退出（`0xE0434352`）**而不是 0。
> 已在**现有预览包上复现相同退出码，确认与本次改动无关**。
> 对用户不可见（无窗口、无对话框，唤起功能正常），但会在事件查看器里留崩溃记录。

> ### ✅ 2026-08-29（三）：Windows 正式包已出；macOS 安全存储落地（Phase 2 开工）
>
> #### Windows 正式包 `artifacts/Ver0.1-正式-20260829-r1/`
>
> | | 上一版正式（WPF） | **本版（Avalonia）** |
> |---|---|---|
> | 单文件 exe | 163.5 MB | **37.8 MB** |
> | zip | 66.3 MB | **15.6 MB** |
>
> 结构与上一版正式包一致：exe + zip（zip 内只有 exe）+ 两个快捷方式 `.cmd` + `codex-installer/README.txt`。
> 快捷方式脚本按 exe 的 BaseName 命名 `.lnk`，与现有正式包同名，**升级即原位覆盖，不产生任何备份副本**（合规 AGENTS.md）。
>
> **服务器地址核在出货字节上**，不是靠构建参数推断：
> 单文件包会把托管程序集打进 bundle，普通 ASCII grep 找不到——.NET 的字符串字面量在 `#US` 堆里是 UTF-16LE。
> 按 UTF-16LE 搜索的结果：`https://gongfeiai.com/` 命中 3 处，
> `http://test.gongfeiai.com/` 与 `http://127.0.0.1:8080/` 均为 0。
>
> #### 🔴 出包后发现：版本号对不上（已拍板改 0.2，见当日第五条）
>
> | 位置 | 当时的 version |
> |---|---|
> | `ClientOptions.CurrentVersion`（客户端自报） | **0.1** |
> | `frontend/public/client-version.json`（**跟踪中的源**） | **0.1** |
> | `backend/internal/web/dist/client-version.json`（构建产物，已 gitignore） | 0.2 |
>
> **一条要更正的说法**：我最初把 `dist/` 那份称作"实际被服务的"，这是错的。
> `backend/internal/web/dist/*` 在 `.gitignore` 里，是**本地构建输出**，
> 不能拿它当线上事实——线上是什么我没查过，也不应在未获要求时去碰远端。
> 另一处旁证：那份里的 `download_page` 是 `/download/client`，
> 而前端路由里只有 `/download`——它本身就是不可信的。
>
> 陷阱一条：`ClientUpdateViewModel.CurrentVersionText` 曾是**硬编码字符串 `"Ver0.1"`**，
> 与 `ClientOptions.CurrentVersion` 无关联。
>
> #### macOS：安全存储已落地（唯一的硬阻断）
>
> 在此之前 `SecureStorage.CreateSessionStore()` 在 macOS 上直接抛异常，客户端连窗口都建不出来。
>
> **关键认识：钥匙串不是 DPAPI 的对应物。** DPAPI 把"加密这段字节"和"用一把我从不经手的密钥"合成了一件事，
> macOS 把它们拆开了——`SecItem*` 存的是**一个秘密**，它不加密任意字节数组，
> 而 Codex 快照有好几 KB，根本不是 generic-password 项该有的形状。
>
> 因此结构是：**钥匙串只存一把 32 字节随机密钥，`AesGcm` 做实际加解密**，会话与快照共用这把密钥。
>
> 这样拆的回报是**可验证面积**：信封格式、nonce、防篡改、两条往返路径**全部在 Windows 上跑测试**
> （对着一个假的 `IMasterKeyStore`，和 `AppleScriptNotification` 同一个套路）。
> 只有约 30 行的钥匙串 P/Invoke 是盲写的。**若让钥匙串去做加密，则 100% 都是盲写。**
>
> #### 一个必须在设计时就避开的坑：`GetOrCreateKey`
>
> 最自然的写法是一个 `GetOrCreateKey()`。**在解密路径上它是毁灭性的**：
> 查不到就现造一把，会让所有用旧密钥写过的密文**永久且静默地**无法解读。
> 对会话，代价只是重新登录一次；**对 Codex 快照，代价是用户的 ChatGPT 账号回不去了**——
> 那个文件是用户原始配置的备份，而他会在"某天想切回自己账号"时才发现。
>
> 所以接口拆成两个方法：`ReadOrCreate()` 只在写路径用，`TryRead()` 读不到就是读不到。
> 会话存储把读失败降级成"没有会话"（重新登录即可），
> **快照保护器则直接抛**——返回 null 或空数组会让调用方用"空"覆盖掉用户的真实配置。
> 这条差异有专门的测试钉住（`UnprotectNeverCreatesAKey`）。
>
> 写失败一律抛、绝不降级：`SecureStorage` 的规矩是"宁可不启动，也不明文落盘"，
> 而不是 `StartupRegistrations` 那条"降级并记日志"。
>
> #### 钥匙串 API 的选择：按"便于翻案"来定，不是定死
>
> 选了 `SecKeychainAddGenericPassword` / `SecKeychainFindGenericPassword`：
> 平坦的 C 参数，不涉及 `CFDictionary` / `CFString` / CFRelease 纪律——
> 盲写面积大约是现代 `SecItem*` 那套的三分之一。
> 它们已被标记弃用，而**"弃用是否已经变成移除"恰恰是在 Windows 上无法确定的事**。
> 调用点是 `IMasterKeyStore` 接口，换成 `SecItem*` 是加一个文件、改工厂里一行。
>
> `/usr/bin/security add-generic-password` 作为第三条路被**否决**：
> 它用 `-w <value>` 传密钥，等于把密钥放进 `argv`，
> 同一用户下任何进程都能从 `ps` 里读走——这正是本层拒绝明文文件的同一类暴露。
>
> #### 进度
>
> **已完成**：`AppPaths`、`LaunchAgent` 自启、`osascript` 通知、`Info.plist`、**安全存储**。
> **未开始**：`CodexInstaller` 的平台分支、`open -b` 启动器、`.app` 组装、`install/uninstall-mac.sh`。
>
> **353 个测试全绿**（新增 19 个：信封 6、会话存储 8、快照保护 5）。

> ### ✅ 2026-08-29（四）：安装包按系统匹配 —— 顺带发现 Windows ARM64 也一直拿错包
>
> 拿到镜像站的完整地址表后补上了 `CodexPackageProfile`。
> 原先客户端**无条件请求 `win-x64`**，而那张表说明这不只是 mac 的问题：
>
> | 主机 | 镜像路径 | 补之前拿到的 |
> |---|---|---|
> | Windows x64 | `win-x64` | ✅ 正确 |
> | **Windows ARM64** | `win-arm64` | ❌ **x64 包** |
> | **Apple Silicon** | `mac-arm64` | ❌ **Windows `.msix`** |
> | **Intel Mac** | `mac-intel` | ❌ **Windows `.msix`** |
>
> **这类错误是安静的**：镜像站给你要的那个路径，下载成功、报告成功，
> 得到一个本机打不开的文件，全程没有任何异常。
>
> #### 实测（不是照着表抄）
>
> 四条路径逐个 HEAD，全部 302 到 `codexapp-r2`，再 200：
>
> | | content-type |
> |---|---|
> | `win-x64` / `win-arm64` | `application/vnd.ms-appx` |
> | `mac-arm64` / `mac-intel` | `application/x-apple-diskimage` |
>
> 与代码里的扩展名集合一致。**响应没有 `Content-Disposition`**，
> 所以落盘文件名走的是客户端内置的那个名字，扩展名必须自己给对。
>
> #### 三处随之改掉的
>
> - **架构读的是 OS 不是进程**：Rosetta 下进程架构报 x64，
>   会把一台 Apple Silicon 机器送去下 Intel 包。
> - **`msiexec` 分支加了 Windows 守卫**：macOS 上 `UseShellExecute` 映射到 `/usr/bin/open`，
>   正好用来挂载 `.dmg`。
> - **提示文案分平台**：`.dmg` 打开只是挂载，用户还要把 App 拖进「应用程序」。
>   在 mac 上说"请按安装向导完成安装"，是让用户等一个永远不会出现的向导。
>
> 无对应构建的主机**解析为 null 并拒绝下载**，而不是回退到 Windows 包——
> 回退正是这个类被写出来要消灭的东西。
>
> **364 个测试全绿**（新增 11），原有 CodexInstaller 测试未改动即通过，Windows 行为不变。

> ### ✅ 2026-08-29（五）：版本号升到 0.2，正式包重出
>
> `artifacts/Ver0.2-正式-20260829-r1/`（**37.9 MB exe / 15.6 MB zip**），
> 先前那个 0.1 的正式包已删除——留着一个标着"正式"却发不得的包，迟早会被发出去。
>
> #### 改了三处，其中一处是为了以后不用再改
>
> - `ClientOptions.CurrentVersion` → `0.2`；
> - `client-version.json`（**跟踪中的那份**，`frontend/public/`）→ `0.2`，
>   并把 release notes 改成真正描述本次发布的内容（原文写的是 0.1 的特性）；
> - `CurrentVersionText` **从硬编码字符串改为由 `CurrentVersion` 推导**。
>   原来它是字面量 `"Ver0.1"`——升版本会让界面上的版本号停在旧值，
>   于是客户端对更新检查报一个版本、对用户显示另一个，而**没有任何东西会暴露这个分歧**。
>
> #### 一个测试当场抓住了这次改动
>
> `UpdateViewModelExposesVersionAndDownloadPageAfterCheck` 断言的是字面量 `"Ver0.1"`——
> 它钉住的是那个硬编码字符串，**因此只在"没人发版"期间为真**，第一次升版本就必须手改。
> 已改为对着 `ClientOptions.CurrentVersion` 断言：现在它只在
> "界面显示的版本"与"更新检查用的版本"不一致时才失败，那才是值得抓的东西。
>
> 并补了一条 `AManifestMatchingThisBuildOffersNoUpdate`：清单与本机版本一致时不应提示更新。
> 钉的是发版清单里那条——**版本落后于清单的构建，会永远劝用户升级到他已经在跑的版本。**
>
> #### 核验（仍在出货字节上）
>
> - `https://gongfeiai.com/` 命中 3 处；`test.gongfeiai.com`、`127.0.0.1:8080` 各 0 处；
> - **`Ver0.1` 字面量已从包中消失**（`Ver0.2` 同样查不到——因为它现在是运行时由 `Version` 拼的，
>   这恰好反证了推导那处改动确实生效）；
> - 四条镜像路径 `win-x64` / `win-arm64` / `mac-arm64` / `mac-intel` 均在包内。
>
> **365 个测试全绿。**
>
> #### 仍未做的一件事
>
> 这个包**没有跑过运行冒烟测试**：预览版客户端仍占着单实例锁，
> 而用 `Stop-Process` 杀它会跳过退出清理，可能把用户的 ChatGPT 留在一个已吊销的 key 上。
> 需要从托盘正常退出后再装一次实测——**注册流程至今没连真服务器跑过**。

> ### ✅ 2026-08-29（六）：macOS 启动器落地，组合根不再提及任何平台
>
> #### `MacCodexAppLauncher`：共用接口，零行共用实现
>
> Windows 那个启动器存在的理由是**拿到 DevTools 端口**——CDP 浮层要靠它附着。
> **macOS v1 没有浮层**，所以没有端口要谈，它只需要把 App 拉起来。
>
> #### 但它仍然拒绝附着到已在运行的实例 —— 理由和 Windows 不同
>
> Windows 上的理由是"运行中的实例没有调试端口"。
> macOS 上的理由是：客户端刚刚重写了 `~/.codex`，
> 而**一个已经在运行的 ChatGPT 会不会重读这个文件，正是至今没在真机上验证过的事**（G-1）。
>
> 假设它会读，代价是：客户端报告"已就绪"，而 ChatGPT 其实还连着用户自己的账号。
> **这个失败没有任何症状**，用户会从 OpenAI 账单上发现。
> 因此选择"请求重启"——这是在两种可能下都正确的答案。
>
> G-1 若回答"运行中也会重读"，这里降为 `AttachedToExisting`、重启提示消失，是两行的改动。
> **这是一个值得站的方向**。
>
> #### 拆分方式与钥匙串一致
>
> `IMacCodexProcess`（四个成员：装没装 / 在不在跑 / 退出 / 启动）是盲写的那一小块；
> **决策表在 Windows 上有 8 个测试**，覆盖"没装""在跑且未获许可""获许可则退出再启动"
> "退不掉""启动失败"等分支。
>
> 两处刻意的选择：
>
> - **退出走 Apple Events（`tell application id … to quit`）而不是信号**，
>   让 App 按用户点「退出」的方式关闭。杀进程会丢掉用户手上正在进行的对话，
>   而客户端之所以要先问一句，正因为这个代价很贵。
> - **退出后要轮询确认它真的走了**。Apple Event 被接受不等于 App 已经退出，
>   报告成功却又去 `open` 一个从未离开的实例，只会得到一个前台窗口和一份旧配置。
> - **bundle id 是构造函数参数而非埋在里面的常量**，且"装没装"是查 `.app` 目录而不是查 id。
>   id 写错时的症状因此是"在明明装了的机器上说未安装"——一个看得懂的症状，
>   而不是"点了启动没反应"。
>
> #### `CodexHosts` 工厂：组合根不再提及平台
>
> `App.axaml.cs` 原先直接 `new CodexAppLauncher()` 和 `new RelayInjectionHost(...)`，
> 两者都是 Windows 专属类型。现在收到工厂后面，
> 且**启动器与浮层宿主一起决定**——浮层靠的那个端口只有 Windows 启动器会谈，
> 配错组合会让宿主一直等一个没人打开的端口。
>
> 至此组合根里**没有任何一处提到平台**：
> `SecureStorage`、`StartupRegistrations`、`NotificationPresenters`、`SingleInstance`、`CodexHosts`
> 五个工厂各自决定。文件在两个目标上读起来完全一样——
> 这正是"缺一个 macOS 实现"会表现为"某个工厂失败"而不是"接线里埋了个分支"的原因。
> 该文件顶部那段"目前仅 Windows"的说明已过时，一并改掉。
>
> **373 个测试全绿**（新增 8）；osx-arm64 裁剪发布**零警告**。
>
> #### macOS 剩余
>
> 代码侧只剩**打包**：`.app` 组装、`rcodesign` ad-hoc 签名、`install/uninstall-mac.sh`。
> 其余全部写完，且**盲写部分都在各自类里就地标注**（钥匙串、`open -b`、`osascript`、LaunchAgent）。

> ### ✅ 2026-08-29（七）：图标与平台用语 —— 以及一条我自己纠正的判断
>
> #### 先纠正：我说过"mac 上 ⌘V 粘贴不了密码"，这句没有被证实
>
> 那是推断，不是查证，而且大概率是错的。查 `Avalonia.Native` 的元数据后：
>
> - `NativePlatformSettings : DefaultPlatformSettings`，**不覆盖** `HotkeyConfiguration`——
>   Command 键的映射来自 Avalonia 核心的默认实现，不依赖任何菜单；
> - `AvaloniaNativeMenuExporter` **自带 `CreateDefaultAppMenu`**，菜单栏不会是空的。
>
> 所以"应用菜单"是观感问题，不是功能缺陷。**我把它从"必做"降级了。**
>
> #### 因此也决定：不盲写自定义应用菜单
>
> 框架已有默认菜单、快捷键不依赖它，而我无法在 mac 上验证自己写的那份。
> 盲写一个只可能比默认更差。顺带核了一件本以为要修的事：
> **⌘Q 的退出清理是通的**——macOS 的退出会触发 Avalonia 的 `Exit`，
> 而 `Exit` 已经接在 `ReleaseBeforeProcessExit()` 上，与其它退出同一条路。**没有 bug 要修。**
>
> #### 真正会安静失败的是图标
>
> `TrayPresence.LoadIcon()` 加载的是 `.ico`，而 **Avalonia 在 mac 上用 Skia 解码，
> Skia 没有义务认 `.ico`**；这个方法解码失败时**回退到"无图标"而不是抛异常**——
> 症状就是菜单栏上一片空白，日志里什么都没有。
>
> 处理方式：
>
> - 新增 `packaging/macos/build-icns.py`，从**同一个 `.ico`** 生成 `.icns` 与共享 PNG。
>   这不是图像转换而是**换容器**：`.ico` 里 8 个尺寸**本来就都是 PNG**（已核），
>   而现代 `.icns` 正好直接存 PNG。**不解码、不重采样、不重编码**——
>   两个平台因此不可能显示成不同的图标，也不需要 Pillow 依赖。
> - 脚本**写完自己读回来校验**：头部、chunk 长度、每个 payload 的实际像素尺寸。
>   一个畸形的 `.icns` 在 macOS 上不会报错，只会显示成通用应用图标，
>   而这边没有任何人能看见它发生。
> - 窗口图标与托盘图标都改用 PNG，并**实测**：构造真实的 `ShellWindow`
>   与 `WindowIcon`，两者都成功加载。
>
> 一处诚实的限制：源图最大只有 256×256，所以 512 / 1024 槽位留空、由 macOS 放大。
> 在这里自己放大只会更难看，还会掩盖"母图只有 256"这个事实。要清晰的 1024，
> 该换更大的源图，而不是换更聪明的脚本。
>
> #### 平台用语
>
> 「最小化到托盘」「从托盘图标可以…」——**mac 没有托盘**，那个图标在菜单栏。
> 新增 `PlatformWords.NotificationArea`（托盘 / 菜单栏），
> 用在退出对话框的按钮与两条后果说明、以及首次最小化提示上。
>
> 退出对话框的按钮文案因此从 AXAML 移到代码里——**按名字取控件失败会在用户第一次点「退出」时
> 抛空引用**，构建期不报，所以补了一条实测确认 `MinimizeButton` / `ConsequenceText` 都取得到。
>
> **376 个测试全绿**；win-x64 仅剩已知那条 COM 的 IL2026，osx-arm64 零警告。
>
> #### macOS 现状
>
> 代码侧只剩**打包**：`.app` 组装、`rcodesign` ad-hoc 签名、`install/uninstall-mac.sh`。
> 仓库外还欠两件（Windows 也中招）：**下载页只有 Windows 且写死了 `_v0.1_` 的文件名**，
> 以及 `client-version.json` **没有平台维度**。

> ### ✅ 2026-08-29（八）：`.app` 组装、`.tar.gz`、安装与卸载脚本
>
> `packaging/macos/build-app.py` + `install-mac.sh` + `uninstall-mac.sh`。
> 已在 Windows 上跑通：**组装成功、归档 18.9 MB、97 个条目、可执行位正确**。
>
> #### 组装时抓到的第一个 bug：`CFBundleExecutable` 名字是错的
>
> plist 里写的是 `LanAi.RelayClient`，而发布产物叫 `LanAi.RelayClient.App`（AssemblyName）。
> **写错不会有任何提示 —— 双击 App 毫无反应，就这样。**
>
> 所以脚本把「`Contents/MacOS/<CFBundleExecutable>` 存不存在」做成**构建期断言**，
> 而不是靠人记得对一眼。这类错误的唯一防线只能在构建机上。
>
> #### 为什么是 `.tar.gz` 而不是 `.zip`
>
> 不是格式偏好：**Windows 生成的普通 zip 不带 Unix 权限位**，
> 解出来的可执行文件没有执行位，App 直接起不来。
> 所以脚本**自己写 tar 条目并显式设置模式**，而不是调外部打包工具——
> 权限位在 Windows 上根本不存在，只能由我们指定。
> 主程序与 16 个 `.dylib` 置 0755，其余 0644，目录 0755；
> 归档后再读回来核对，确认可执行位、`Info.plist`、`AppIcon.icns` 都在。
>
> #### 签名闸门：宁可不出包
>
> Apple Silicon 要求每个 arm64 可执行文件带签名（哪怕 ad-hoc），**内核不警告，直接杀进程**。
> .NET 在 macOS 上构建会自动 ad-hoc 签名，从 Windows 交叉编译**不会**。
>
> 一个未签名的 tar.gz **在这边看起来完全正常**，到了每一台目标机器上都是死的。
> 因此脚本检查 `Contents/_CodeSignature/CodeResources`，**没有签名就拒绝出归档**，
> 并打印出该跑的 `rcodesign sign` 命令。`--allow-unsigned` 只用于看布局。
>
> 这条规矩写进工具，而不是写进记忆。
>
> #### 版本一致性也做成了构建期检查
>
> `build-app.py` 从 `ClientOptions.cs` 读版本号，并**与 `client-version.json` 交叉核对**，
> 不一致就构建失败。理由与 §8.5 同源：版本落后于清单的构建，
> 会永远劝用户升级到他已经在跑的版本，而这个症状在构建机上完全看不见。
> 本次实测输出 `manifest agrees: 0.2`。
>
> #### 两个脚本里最重要的一行，都不是删文件
>
> **是「正常退出，而不是强杀」。** 客户端退出时要把用户原本的 `~/.codex` 还回去、
> 回收托管的中转 key。安装脚本升级前、卸载脚本删除前，都先发 Apple Events 退出并**轮询确认它真的走了**；
> 30 秒还没退就**中止并让用户手动退出**，不强杀。
>
> 强杀会跳过还原，用户的 ChatGPT 停在一个已被吊销的 key 上——
> 而这个故障要到下次打开 ChatGPT 才显现。**卸载脚本自己制造故障，是最难被联系起来的那种。**
>
> #### 卸载默认不删数据
>
> `~/Library/Application Support/LanAi.RelayClient` 里有 `codex-snapshot`——
> 用户原本自己的 Codex 配置备份，也就是他停用共飞后回到自己账号的退路。
> 正常退出会自动还原，但**如果上一次是崩溃或强杀，这份备份就是唯一的退路**。
> 卸载时默认保留并明确告知，要删得显式 `--purge`（同时清掉钥匙串里的密钥）。
>
> #### 其它两处
>
> - **架构检查前置**：Intel 机器会「安装成功、打开没反应」，所以在**下载之前**就 `uname -m` 拦下。
> - **`pgrep -x` 换成 `pgrep -f`**：macOS 的 `-x` 比对进程记账名且有长度限制，
>   而可执行文件叫 `LanAi.RelayClient.App`（21 字符）。改用完整命令行里的应用路径比对。
> - 两个脚本均通过 `bash -n` 语法检查。
>
> #### 仍然缺的一件事
>
> **`rcodesign` 不在这台机器上**（也没有 cargo）。因此现在能产出 `.app` 与经过校验的归档逻辑，
> 但**出不了可分发的包**——闸门正确地拦着。装这个工具需要下载并运行一个第三方二进制，
> 已单独问过再决定。

> ### ✅ 2026-08-29（九）：GitHub Actions 出包流水线
>
> 根级 `.github/workflows/`（**不是** `sub2api/.github/workflows/`——那是子目录，Actions 从不读它）：
>
> | | 触发 | 作用 |
> |---|---|---|
> | `client-ci.yml` | push / PR（路径过滤） | 测试 + 两个目标的裁剪发布 + 图标生成物一致性 |
> | `client-release.yml` | `client-v*` tag 或手动 | 双平台出包、ad-hoc 签名、发 Release |
>
> #### runner：偏离了方案的建议，理由写在文件里
>
> §9.5.3 说终局可以整条搬到 `ubuntu-latest`（1× 计费），Avalonia 头两个目标都能在 Linux 上交叉编译。
> **那是对的，但没有人实测过。** 而这里的每一步都在 Windows 上端到端验证过。
> 发布是按 tag 触发的低频任务，**省下的分钟数不值得拿「出包能不能成」去换**。
> CI 那条本来就必须 `windows-latest`——测试项目与 WPF 头都是 `net8.0-windows`。
>
> #### 签名：`cargo install` 而不是下载预编译二进制
>
> `apple-codesign` 0.29.0（crates.io 当前稳定版），版本钉死、走 cargo 自带的校验和验证。
> 比下载 GitHub release 里的二进制更容易说清来源，也不需要人工核对 sha256。装完结果进缓存。
>
> 签完**回头验一次**，而不是相信 `rcodesign sign` 的退出码：漏签的产物在 CI 里完全看不出来，
> 只有到用户机器上才会被内核杀掉。
>
> #### `build-app.py` 改成三步，让签名有地方插入
>
> 原来「组装 + 归档」是一步，流水线里就变成「先归档一个未签名的、再归档一次」——
> 白干一遍，而且读起来像是绕过了闸门。现在是 `--assemble-only` → `rcodesign sign` → `--archive-only`，
> **闸门落在最后那步**（`--archive-only` 不带 `--allow-unsigned`，漏签就在那里失败）。
> 本地演练过：组装通过，未签名的归档以退出码 1 被拦下。
>
> #### CI 里的四道断言，都是「发出去才发现」那一类
>
> 1. **tag 与 `ClientOptions.CurrentVersion` 一致**——打错 tag 会发出一个版本号对不上的包；
> 2. **`ClientOptions.cs` 里没有 `127.0.0.1`**——占位符防呆；
> 3. **出货字节里有生产地址、没有测试服与占位地址**——抽成
>    `packaging/check-server-address.py`，按 UTF-16LE 搜索（单文件包会把程序集打进 bundle，
>    且 .NET 字面量是 UTF-16LE，普通 grep 找不到）。**正反两条路径都本地验过**：
>    对真实出货 exe 通过，对一个不含地址的文件退出码 1；
> 4. **图标生成物与源图一致**——重跑 `build-icns.py` 后 `git diff` 必须为空。
>    有人改了源图却没重新生成时，症状是 macOS 上图标变成通用应用图标，没有任何报错。
>
> 第 3 条从内联 heredoc 改成了脚本文件，顺带修掉一个真 bug：
> **YAML 里缩进过的 heredoc 终止符不会闭合**。
>
> #### 没有任何 secret
>
> 不买开发者账号 = 不公证 = 没有证书、私钥或 API Key 要管。ad-hoc 签名不需要任何 Apple 凭据。
>
> #### 首跑需要确认的两点
>
> 这两条我在 Windows 上无法验证，第一次跑会暴露：
>
> - `rcodesign print-signature-info` 的子命令名；
> - `rcodesign sign` 对 `.app` 目录（而非单个 Mach-O）的行为是否如预期。
>
> 两者若不对，都会是**响亮的失败**（步骤直接红），不会静默出一个坏包。

- 按 §7 顺序推进，每完成一个视图与 WPF 版逐项比对；
- 双平台各跑一遍真机链路（登录 → 建 key → 写 `~/.codex` → 拉起 ChatGPT → 守护路由 → 退出回收）；
- **出口标准（三条，缺一不可）：**
  1. 与 WPF 版功能逐项对等；
  2. **`AvaloniaUseCompiledBindingsByDefault=true`，且 `PublishTrimmed` 在 `TreatWarningsAsErrors` 下构建干净**（§2.4 ②）——
     否则界面会在裁剪后静默空白；
  3. macOS 中文 IME 行为正常（遗漏 7）。

### Phase 4 — 打包与分发（4–6 天）

- macOS：Windows 上交叉编译 → 组装 `.app` → **`rcodesign` ad-hoc 签名（强制）** → `.tar.gz` → `install-mac.sh`（含架构检测）；
- Windows：沿用现有 zip + 快捷方式 `.cmd`；
- CI：见 **§9.5**。过渡期 `windows-latest`（WPF 头要编）+ `ubuntu-latest`；**退役 WPF 后整条流水线只需 `ubuntu-latest`**；
- **遵守 AGENTS.md：安装 / 升级直接替换 `/Applications/共飞-ChatGPT助手.app`，不得创建任何备份或回滚副本。**

> **🟡 遗漏 6：需要一份发布前配置检查清单。** README 明确写着
> `ClientOptions.ServerAddress` 目前是 `http://127.0.0.1:8080/` **占位符**，发给用户的构建必须换成生产地址。
> mac 版还新增了 bundle id、`Info.plist` 版本号、`install-mac.sh` 里的下载地址、`client-version.json` 的 mac 条目。
> 没有清单，很容易发出一个连本机的包——**这类事故是静默的，用户只会看到"连不上服务器"**。

---

## 9.5 GitHub Actions 构建流水线

> 追加要求（2026-08-27）：**后续用 GitHub Actions 构建**。
> 好消息：本方案的技术选择恰好让 CI 变得非常简单——**没有任何签名密钥要管**。

### 9.5.1 先说一个现状问题：仓库根目录没有 `.github/workflows/`

远端是 `git@github.com:Nirvana6234/-sub2api.git`。现有 workflow 都在
**`sub2api/.github/workflows/`**（`backend-ci.yml`、`release.yml` 等）——那是**子目录**。

**GitHub Actions 只读取仓库根目录的 `.github/workflows/`**，
所以那批 workflow 目前**根本不会被触发**（多半是从上游开源项目一起 fork 进来的）。

因此客户端 CI 的第一步是**新建根级 `.github/workflows/`**，而不是往现有目录里加文件。
> **（2026-08-29 已建）** `client-ci.yml` 与 `client-release.yml` 已放在根级，见当日第九条。
顺带值得确认：那批 sub2api 的 workflow 是想启用还是想留作参考——若想启用，需移到根级并加路径过滤。

### 9.5.2 ad-hoc 签名让 CI 极大简化

因为不买开发者账号（§8.3），**流水线里没有任何 Apple 证书、私钥或 API Key 需要作为 secret 管理**。
这是无账号方案一个少见的正面副作用：

| | 有账号（公证） | **本方案（ad-hoc）** |
|---|---|---|
| 需要的 secrets | 证书 p12 + 密码 + App Store Connect API Key + issuer id | **无** |
| 密钥轮换 / 过期 | 每年 | 不涉及 |
| CI 复杂度 | 需导入钥匙串、处理公证轮询 | **一条 `rcodesign sign`** |

将来若买账号，只需加 secrets 并在末尾追加 `rcodesign notary-submit` + staple，流水线结构不变。

### 9.5.3 Runner 选择：注意私有仓库的分钟数倍率

私有仓库的 Actions 分钟数按倍率计费：**Linux 1× / Windows 2× / macOS 10×**。
这直接影响 runner 选择：

| 阶段 | 构建 Windows 产物 | 构建 macOS 产物 | 说明 |
|---|---|---|---|
| **过渡期**（WPF 仍在） | `windows-latest`（2×） | `ubuntu-latest`（1×） | WPF **只能**在 Windows 上构建 |
| **终局**（退役 WPF 后） | `ubuntu-latest`（1×） | `ubuntu-latest`（1×） | **两个平台都能在最便宜的 runner 上交叉编译** |

**这是退役 WPF 的一个额外收益，之前没提**：Avalonia 不依赖 `Microsoft.NET.Sdk.WindowsDesktop`，
`win-x64` 自包含产物可以在 Linux 上交叉编译。终局整条流水线**只需要 `ubuntu-latest`**，
`rcodesign` 也原生跑在 Linux 上。

**不需要 `macos-latest` 做构建或签名**（§8.6 已论证收益很小）。
它唯一值得考虑的用途是 **Phase 2 的平台实现测试**（遗漏 5）——即便如此，10× 倍率下建议只在
`workflow_dispatch` 手动触发或打 tag 时跑，不要挂在每次 push 上。

### 9.5.4 建议的 workflow 形态

```
.github/workflows/
├── client-ci.yml       # push / PR：跑 dotnet test（246 个）
│                       #   过渡期需 windows-latest（WPF 头要编），终局可全 ubuntu
└── client-release.yml  # 打 tag：交叉编译两平台 → rcodesign → 产物 → GitHub Release
```

`client-release.yml` 的关键步骤：

1. `dotnet publish -r win-x64 -p:PublishTrimmed=true` → zip
2. `dotnet publish -r osx-arm64 -p:PublishTrimmed=true` → 组装 `.app`（含 §8.1.1 的 `Info.plist`）
3. **`rcodesign sign`（强制，§8.2）** → `.tar.gz`（保留可执行位）
4. **校验步骤**：确认 `.app` 内可执行文件确实带签名——
   这一步不能省，**漏签的产物在苹果芯片上会被内核直接杀掉，而 CI 本身完全看不出来**
5. 生成 / 更新 `client-version.json`（含 mac 条目）
6. 发布到 GitHub Release

> **路径过滤**：客户端与 sub2api 后端同仓库，workflow 应加
> `paths: ['tools/codex-relay-client/**', 'tools/manufactor_app/**']`，
> 否则改后端也会触发客户端构建，白烧分钟数。

### 9.5.5 与"发布前配置检查清单"的联动（遗漏 6）

§9 Phase 4 那份清单**应该由 CI 强制**，而不是靠人记：

- 构建时断言 `ClientOptions.ServerAddress` **不是** `127.0.0.1`（占位符防呆）；
- 断言 `Info.plist` 版本号与 `client-version.json` 一致；
- 断言 `TEST_SERVER` 只在测试渠道 job 中被定义（Phase 1 的注记）。

这三条都是"发出去才发现"的静默事故，用 CI 挡住的成本远低于事后召回。

---

## 10. 风险

| 风险 | 影响 | 应对 |
|---|---|---|
| **G-1 为否** | macOS 产品形态改变 | Phase 0 先验证，不要边写边赌 |
| **裁剪打断反射式 JSON** | 契约静默绑空，重现历史上最难查的那类 bug | Phase 1 迁源生成 context；`TreatWarningsAsErrors` 会让问题在构建期暴露而非运行期 |
| **裁剪打断 Avalonia 反射绑定** | **界面静默空白，不抛异常** | `AvaloniaUseCompiledBindingsByDefault=true`，并写入 Phase 3 出口标准（§2.4 ②） |
| **CI 漏掉 `rcodesign` 签名步骤** | 产物在苹果芯片上被内核杀掉，**而 CI 自身完全看不出来** | `client-release.yml` 加显式签名校验步骤（§9.5.4 第 4 步） |
| 改 `AiSwitch.Injection` 的 TFM 波及 WPF 工作台应用 | 另一个应用及其 65 个测试被牵连 | 用多目标而非改目标（§5.1） |
| **交叉编译 arm64 未签名** | **苹果芯片上被内核杀掉，不是警告** | 流水线**强制** `rcodesign sign`，CI 加校验 |
| **未公证 → 每次更新都撞 Gatekeeper** | 首装与**每次升级**都流失 | `install-mac.sh` 兼作更新脚本；客户端给命令而非下载页（§8.5） |
| App Store 版沙箱导致路由不生效 | 用户装错版本，完全不工作且无法自诊断 | 启动时检测已装版本，提示"请安装官网下载版" |
| ad-hoc 签名变化导致 TCC 重新授权 | 更新后通知/控制权限失效 | Phase 2 实测；引导页说明 |
| `FlowDocument` 重写引入公告渲染回归 | 公告显示错乱 | 公告 AST 层测试已存在且可复用，渲染器另配快照比对 |
| 退役 WPF 影响存量用户 | 已发布产品回归 | Avalonia 对等前 Windows 继续发 WPF 版；`AppPaths` 保持旧路径（§5.2） |
| 无 Mac 做 G-1 实测 | 前置门无法关闭 | 借一台或找用户协助，十分钟；**不能靠交叉编译绕过** |
| Keychain 首次访问弹授权框 | 小白困惑 | 首启引导页提前说明 |

---

## 11. 待决策

### 已拍板（2026-08-27）

- ✅ **两平台都不内置 OpenAI 官方安装包**，一律在线下载 → Phase −1 可立即执行。
- ✅ **一个项目兼容两平台**：Avalonia 对等后退役 WPF，统一 `net8.0`（可行性见 §5.1）。
- ✅ macOS Codex 验证参考 cc-switch，`~/.codex` 路径约定已确认（§3）。
- ✅ **不用 Mac 构建，不买开发者账号** → 按 §8 执行；NativeAOT 正式排除。

### 仍需你决定

1. **开发者账号：建议先按无账号发 v1，看真实装机与更新转化率再定。**
   补账号只是流水线末尾加两步，不返工。但请注意 §8.5——代价要乘以发版频率，
   而你近一个月发了 4 个正式版。
2. **是否接受 macOS v1 无 CDP 注入浮层？** 建议接受；**Windows 侧保留**（§5.1）。
3. **macOS v1 是否只支持 `osx-arm64`？** 建议是，配合 §8.4 的架构检测提示。
4. **镜像 `codexapp.agentsmirror.com` 能否扛住全量用户？**
   删内置包后它从"兜底"变"主路径"，759 MB × 全部新用户的带宽与成功率需确认。
   不影响 Phase −1 是否要做，但影响**发版前要不要先扩容**。
5. **是否为 Phase 2 的测试启用 `macos-latest` CI runner？**（遗漏 12）
   比用它做签名更值得。但私有仓库 10× 倍率，建议只挂 `workflow_dispatch` / tag，不挂每次 push。
6. **`sub2api/.github/workflows/` 里那批 workflow 是想启用还是留作参考？**（遗漏 7）
   它们在子目录里，**GitHub Actions 不会执行**。若本就是 fork 带进来的残留，可以不管；
   若期望它们在跑（例如 `backend-ci.yml`），那后端 CI **一直是失效状态**，这是个独立于本方案的问题。

---

## 12. 立即可做的第一步

**Phase −1（去掉内置 msix）** 已拍板，可立刻做：半天，不依赖任何 macOS 结论、不依赖 Core 抽取，
把用户下载量从 825 MB 砍到 66 MB。

紧接着 **Phase 1（Core 抽取 + JSON 源生成）**：同样不依赖任何 macOS 前置结论，
在当前 Windows 机器上就能完整验证（246 个测试全绿即通过）。
它既是 Avalonia 头的前提，也是**"包体小"这个目标的必经之路**——
没有 JSON 源生成，`PublishTrimmed` 就不能开，20 MB 无从谈起。

**体积路线**：`825 MB → 66 MB（Phase −1，半天）→ 约 20 MB（Phase 1 + 3 + 4）`
