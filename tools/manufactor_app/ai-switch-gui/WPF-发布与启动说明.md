# 局域网 AI 工作台：发布与启动说明

这是新版 WPF“局域网 AI 工作台”的第一阶段发布说明。旧版 `LocalGatewayManager.exe` 仍然保留，可继续作为回退版本使用。

## 直接启动

发布包解压后，可以双击：

```text
Start-LanAi-Workspace.cmd
```

也可以直接运行：

```text
LanAi.Workspace.exe
```

请保留发布目录中的全部文件，不要只复制 EXE。更新程序时先退出正在运行的“局域网 AI 工作台”，再整体替换发布目录。

## 运行要求

默认发布包是 Windows x64 的 framework-dependent 版本，目标电脑需要安装：

- Windows 10/11 x64
- .NET 8 Desktop Runtime x64
- 需要使用的官方 CLI：Codex、Claude Code 或 Gemini CLI

API 连接、项目索引和官方历史会话均使用当前 Windows 用户的数据目录。替换程序包不会主动删除这些数据，也不会删除官方 CLI 的历史会话。

只有用户在“项目”页面二次确认“永久删除”时，程序才会同步删除该项目的 Codex、Claude Code、Gemini CLI 官方历史；三类删除全部成功后才移除本机 SQLite 项目记录，源码文件夹始终保留。

## 本版主要入口

- 项目中心：选择项目后先进入“项目会话”，可继续该项目的历史会话，或明确新建会话；点击会话后才进入 AI 对话。AI 对话不再作为侧栏独立入口。历史正文会先立即显示，首次发送时才恢复对应的官方 CLI 会话。
- 连接中心：新增、编辑、删除远程来源；维护固定的本机/局域网来源；设置当前来源、验证、应用到官方客户端，并支持 Codex、Claude Code、Gemini CLI 分流。新建对话和高级终端会自动使用“当前来源”；有明确历史绑定的会话会继续使用其原来源。
- 中转服务：启动、停止、重启、刷新状态、查看日志、打开本机或局域网后台。局域网后台地址在“连接中心 → 编辑局域网中转”单独设置；它与 API 地址分开保存，避免把 API 的 `:8080/v1` 错当成后台网页地址。旧的原生 `:8080` API 配置会默认对应 `:3000/dashboard`，其他端口请按实际部署填写后台地址。
- 总览：优先显示这台电脑由工作台发起的今日/近 7 日 Token、请求成功率、平均响应耗时、当前来源与网络健康状态。
- 流量统计：顶部统一选择今天、近 7 天或近 30 天；本地与云端仪表盘的请求、Token、缓存、费用、平均耗时、趋势、模型与近期活动均跟随同一日历范围。云端历史累计只作为明确标注的参考，不会替代当前范围数据；最近调用记录已隐藏 API、请求 ID、IP、地址、提示词和回复。

本地统计保存在当前用户的 `LocalAppData\LanAi.Workspace\Data\telemetry.db`，默认最多保留 90 天。它只保存请求数量、输入/输出/缓存 Token、成功状态、耗时和最近的健康探测；不会保存提示词、回复正文、API 密钥、完整接口地址、项目路径或官方 CLI 历史正文。它只统计工作台实际发起且带有当前连接来源的会话；官方 CLI 的 JSONL 历史存在累计快照与副本，不能可靠代表近期逐次用量，因此不会导入本地仪表盘。

连接密钥不回显。编辑已有来源时会显示受限前后缀和短指纹，便于辨认已保存的密钥；密码框留空代表保留原密钥，勾选“清除”才会删除。混合分流会先保存来源组合，点击“应用分流”才会写入三类官方客户端配置。

## 默认发布命令

在 `ai-switch-gui` 目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\publish-wpf.ps1
```

默认产物：

```text
artifacts\LanAi.Workspace\LanAi.Workspace-win-x64-framework-dependent\
artifacts\LanAi.Workspace\LanAi.Workspace-win-x64-framework-dependent.zip
```

脚本会执行 Release 发布、隐藏窗口启动烟雾测试、退出测试进程并生成 ZIP。

## 可选的自包含包

如果目标电脑不方便安装 .NET 8 Desktop Runtime，可以生成体积更大的 self-contained 包：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\publish-wpf.ps1 -Mode SelfContained
```

对应产物：

```text
artifacts\LanAi.Workspace\LanAi.Workspace-win-x64-self-contained\
artifacts\LanAi.Workspace\LanAi.Workspace-win-x64-self-contained.zip
```

只在已单独完成启动验证时，才建议使用 `-SkipSmokeTest` 跳过烟雾测试。

## 等价的手工发布命令

小体积依赖版：

```powershell
dotnet publish .\src\AiSwitch.Wpf\AiSwitch.Wpf.csproj -c Release -r win-x64 --self-contained false -o .\artifacts\LanAi.Workspace\LanAi.Workspace-win-x64-framework-dependent -p:PublishSingleFile=false -p:PublishTrimmed=false -p:PublishReadyToRun=false
```

自包含版：

```powershell
dotnet publish .\src\AiSwitch.Wpf\AiSwitch.Wpf.csproj -c Release -r win-x64 --self-contained true -o .\artifacts\LanAi.Workspace\LanAi.Workspace-win-x64-self-contained -p:PublishSingleFile=false -p:PublishTrimmed=false -p:PublishReadyToRun=false
```

WPF 和终端组件当前不启用裁剪。这样可以避免反射、XAML 和原生终端依赖在裁剪后出现运行期缺失。
