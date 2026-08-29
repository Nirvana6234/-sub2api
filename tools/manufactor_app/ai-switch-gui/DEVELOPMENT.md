# 本地中转管理工具开发者快速上手

这份文档给后续接手 `tools/ai-switch-gui` 的开发者使用。目标不是讲 WinForms 或 .NET 基础，而是让你在 10 到 15 分钟内知道：

- 这个工具在解决什么问题
- 关键源码在哪里
- 本地如何跑起来
- 切换、备份、验证这几条主链路现在怎么工作
- 改 UI、改逻辑、改部署时应该从哪里下手

用户使用说明看 [README.md](E:\write2026\HRTI\code\HRTI\tools\ai-switch-gui\README.md)。本文件只服务开发者。

## 工具目标

本地中转管理工具是一个原生 Windows WinForms 工具，用来在同一台远端机器上管理本机 Sub2API / Kiro Gateway 和 AI 客户端配置：

- 云端 profile，其中可以维护多个来源组
- 本地中转 profile，其中可以维护多个来源，例如本机 `127.0.0.1` 和另一台局域网机器

首版只管理两个客户端：

- `Codex`
- `Claude Code`

这个 GUI 负责四件事：

1. 编辑多个云端来源组和多个本地中转来源的地址、密钥和备注
2. 将 profile 写入 GUI 自己的 `profiles.json`
3. 执行切换：备份当前主配置、写入 `.codex` / `.claude`
4. 对切换结果做 HTTP 验证，并把结果显示在右侧状态区

## 目录与职责

工具源码目录就是：

```text
tools/ai-switch-gui
```

关键文件分工如下：

- `Program.cs`
  - 应用入口
  - 启动 `MainForm`

- `MainForm.cs`
  - 主界面布局和交互入口
  - 按钮事件、状态徽标、日志区、Profile 编辑区都在这里
  - 如果要改界面排版、字体、视觉层级，从这里开始

- `SwitchService.cs`
  - 核心业务逻辑
  - 包括：
    - 切换 profile
    - 导入当前真实生效配置
    - 备份 / 恢复
    - HTTP 验证
    - 判断当前状态是云端、本地、两套相同还是漂移

- `ProfileRepository.cs`
  - `profiles.json` 的读取与保存
  - 备份目录的最新项读取
  - 旧备份清理逻辑
  - 当前策略：自动只保留最近 `5` 份备份

- `ConfigPaths.cs`
  - 集中定义运行时相关路径
  - 包括：
    - GUI 自己的配置根目录
    - `.codex/config.toml`
    - `.codex/auth.json`
    - `.claude/settings.json`

- `Models.cs`
  - GUI 用到的数据模型
  - 包括：
    - `ProfileStore`
    - `ProfileDefinition`
    - `ClientProfile`
    - `LiveStatus`
    - `OperationResult`
    - `ValidationDetail`

- `JsonFile.cs`
  - JSON/文本文件的统一读写工具
  - 重点是确保写文件时使用 `UTF-8 without BOM`

- `Assets/app.ico`
  - 应用图标资源

## 本地运行与调试

### 1. 构建

在仓库根目录执行：

```powershell
dotnet build tools/ai-switch-gui/AiSwitchGui.csproj
```

当前项目目标框架是：

```text
net8.0-windows
```

项目文件在：

[AiSwitchGui.csproj](E:\write2026\HRTI\code\HRTI\tools\ai-switch-gui\AiSwitchGui.csproj)

### 2. 调试产物

普通构建产物通常在：

```text
tools\ai-switch-gui\bin\Debug\net8.0-windows\win-x64\
```

可执行文件示例：

[AiSwitchGui.exe](E:\write2026\HRTI\code\HRTI\tools\ai-switch-gui\bin\Debug\net8.0-windows\win-x64\AiSwitchGui.exe)

### 3. 正式发布

发布命令：

```powershell
dotnet publish tools/ai-switch-gui/AiSwitchGui.csproj `
  -c Release `
  --self-contained true `
  /p:PublishSingleFile=true
```

发布产物目录：

```text
tools\ai-switch-gui\bin\Release\net8.0-windows\win-x64\publish\
```

注意：

- 项目设置了 `RuntimeIdentifier = win-x64`
- 发布目录里虽然主入口是单个 `AiSwitchGui.exe`，但当前仍可能带少量附属 DLL
- 如果你改了图标、程序集元数据或打包策略，优先验证 `publish` 目录而不是只看 `build`

### 4. 首次运行会生成什么

程序自己的运行时目录默认是：

```text
C:\Users\Administrator\ai-switch-gui
```

首次运行时会生成：

- `profiles.json`
- `appsettings.json`
- `backups\`

这个目录是 GUI 自己的持久化目录，不等于客户端真正生效的配置目录。

## 配置与数据流

这里是最关键的一条链路。

### 1. GUI 自己的配置源

GUI 编辑区里的内容先保存到：

```text
C:\Users\Administrator\ai-switch-gui\profiles.json
```

这里保存的是云端来源组列表、本地中转来源列表和混合模式选择：

- `Cloud`
- `CloudSources`
- `SelectedCloudSourceId`
- `Local`
- `LocalSources`
- `SelectedLocalSourceId`
- `Mixed`

每个 profile / source 里又分别保存：

- `Codex.BaseUrl`
- `Codex.Secret`
- `Claude.BaseUrl`
- `Claude.Secret`
- `Notes`

### 2. 点击切换后的执行顺序

当前实现顺序是：

1. 读取当前表单值，组装 `ProfileStore`
2. 把表单值保存到 `profiles.json`
3. 仅校验当前要切换的那一套 profile
4. 备份当前真实生效文件
5. 写入 `.codex` 和 `.claude` 主配置
6. 对目标接口发起 HTTP 验证
7. 更新顶部状态和右侧日志

### 3. 实际生效文件

GUI 最终写入的是这三处主配置：

- `C:\Users\Administrator\.codex\config.toml`
- `C:\Users\Administrator\.codex\auth.json`
- `C:\Users\Administrator\.claude\settings.json`

当前设计明确只认这三处，不再同步历史镜像副本目录。

### 4. 当前状态判定规则

顶部状态不是看 `profiles.json`，而是读真实主配置文件后再判断。

当前判断规则：

- 匹配 `Cloud`：显示 `云端`
- 匹配 `Local`：显示 `本地中转`
- 同时匹配 `Cloud` 和 `Local`：显示 `云端/本地相同`
- 两边都不完整匹配：显示 `自定义/漂移`

这条规则在：

[SwitchService.cs](E:\write2026\HRTI\code\HRTI\tools\ai-switch-gui\SwitchService.cs)

### 5. 导入当前主配置

“将当前主配置导入到云端 / 本地”按钮不是从 `profiles.json` 复制，而是直接读取当前真实生效值：

- `.codex/config.toml`
- `.codex/auth.json`
- `.claude/settings.json`

然后回填到某一套 profile 里。

这意味着一个常见现象：

- 如果你先把当前主配置导入到本地
- 又把同一份当前主配置导入到云端

那么两套 profile 会完全一样，顶部状态就会变成：

```text
云端/本地相同
```

这不是 bug，是当前数据状态本身导致的。

## 远端部署流程

当前远端部署策略不是自动安装包，而是本地发布后把产物同步到固定目录。

### 1. 本地发布

先在开发机执行：

```powershell
dotnet publish tools/ai-switch-gui/AiSwitchGui.csproj `
  -c Release `
  --self-contained true `
  /p:PublishSingleFile=true
```

### 2. 同步到远端

把 `publish` 目录里的文件同步到远端临时目录，再覆盖到固定部署目录：

```text
C:\Users\Administrator\ai-switch-gui-app
```

### 3. 桌面入口

远端桌面快捷方式固定指向：

```text
C:\Users\Administrator\Desktop\AI Switcher.lnk
```

快捷方式目标应为：

```text
C:\Users\Administrator\ai-switch-gui-app\AiSwitchGui.exe
```

### 4. 覆盖部署前的注意事项

如果远端 exe 正在运行，直接覆盖会失败。部署前需要：

1. 结束远端 `AiSwitchGui.exe`
2. 清空旧部署目录
3. 再拷贝新文件

如果你看到“访问被拒绝”或“文件正在被另一个进程使用”，优先排查这个问题。

## 已知约束与后续开发建议

### 已知约束

- 云端和本地中转都支持多个来源；本地中转默认包含“本机中转”和“局域网中转”
- 当前只管理 `Codex` 和 `Claude Code`
- 当前只认三处主配置文件，不再同步镜像副本
- 切换前自动备份，但自动只保留最近 `5` 份
- `保存配置` 允许某一套 profile 暂时为空
- `切到云端` 只校验当前选中的云端来源组
- `切到本地中转` 只校验当前选中的本地中转来源

### 近期踩坑记录

- UI 仍在迭代，布局问题优先用截图回归，不要只靠代码想象
- 中文字符串和编码问题以前出现过，后续编辑必须确认文件是 UTF-8
- `README.md` 在历史上有过乱码版本，后续不要复制乱码文本继续扩散
- 顶部状态如果“看起来没变”，先检查是不是把两套 profile 导成了相同值

### 推荐的下一步迭代

- 增加“测试当前 profile 但不切换”的按钮
- 增加首次启动向导，而不是只靠导入按钮
- 将验证结果做成更明确的状态灯，而不只是日志行
- 给 profile 增加“重置为推荐默认值”
- 让备份保留数变成一个可配置项，而不是硬编码 `5`
