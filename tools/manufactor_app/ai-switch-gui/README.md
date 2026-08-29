# 本地中转管理工具

这是一个原生 Windows WinForms 工具，用于在 Windows 机器上图形化管理本机 Sub2API，并临时切换 AI 客户端配置：

版权所有：归年

- 本地中转 profile，可配置多个来源，例如本机 `127.0.0.1` 和另一台局域网机器

首版只覆盖：

- `Codex`
- `Claude Code`

## 功能

- 在 GUI 中直接编辑多个本地中转来源的地址、Key、备注
- 本地中转默认提供“本机中转”和“局域网中转”两个来源，也可以继续新增来源
- 切换前自动保存 `profiles.json`
- 切换前自动备份当前生效文件到 `backups/时间戳/`，最多保留最近 2 份备份
- 支持从当前主配置导入到当前选中的本地中转来源
- 内置“本地中转站”面板，可查看 Sub2API / Postgres / Redis 状态，并一键启动、重启、停止、打开后台
- 优先使用项目根目录下的 Windows 原生启动脚本，避免启动 Docker/WSL2 影响 Intel XTU；找不到原生脚本时才回退到 Docker Compose
- `native-path.txt` 可填写 Sub2API 项目根目录；点击“启动并打开”时会调用其中的 `start-sub2api-local.ps1`，停止时调用 `stop-sub2api-local.ps1`
- 仅在找不到原生脚本时才检查并使用 Docker Desktop
- 打开时记录当前 Codex / Claude Code 主配置，真正退出工具时自动恢复打开前配置，包括 `.codex/config.toml`、`.codex/auth.json` 和 `.claude/settings.json`
- 支持正常最小化到 Windows 任务栏；最小化不会触发配置恢复，只有彻底关闭窗口才会恢复
- 支持收起到任务栏小图标；可在工具内设置关闭按钮是收起到小图标还是直接退出，托盘菜单里的“退出”始终是真退出
- 支持单实例运行；重复打开时会唤醒正在运行的窗口，不会再启动第二份
- 未安装 Docker 时仍可打开工具，但本地中转站启动、重启、停止、重建等按钮会置灰
- 实际写入：
  - `C:\Users\Administrator\.codex\config.toml`
  - `C:\Users\Administrator\.codex\auth.json`
  - `C:\Users\Administrator\.claude\settings.json`
- 切换后自动验证：
  - `Codex`: `GET {base_url}/models`
  - `Claude Code`: `GET {base_url}/v1/models`
- 支持恢复最近一次备份

## 本地配置目录

程序默认使用：

```text
C:\Users\Administrator\ai-switch-gui
```

其中包含：

```text
profiles.json
appsettings.json
backups\
```

## 构建

开发机需要 `.NET SDK 8` 或更新版本。

当前仓库环境已验证通过：

```powershell
dotnet build tools/manufactor_app/ai-switch-gui/AiSwitchGui.csproj
```

正式发布命令：

```powershell
dotnet publish tools/manufactor_app/ai-switch-gui/AiSwitchGui.csproj -c Release
```

项目默认生成 `win-x64` 单文件发布包。

发布产物默认在：

```text
tools\manufactor_app\ai-switch-gui\bin\Release\net8.0-windows\win-x64\publish\
```

分发时只使用 `publish` 目录中的文件。不要把 `bin\Debug\...` 或 `bin\Release\...` 下普通构建输出里的 `LocalGatewayManager.exe` 单独拷到远端运行，否则会出现要求安装 `.NET` 运行库的提示。

## 使用说明

1. 打开 `LocalGatewayManager.exe`
2. 如需首次导入现有配置，在“本地中转”页选择来源后导入
3. 检查并编辑：
   - Codex 地址
   - Codex Key
   - Claude 地址
   - Claude Key
   - 备注
   - 本地中转来源名称
4. 点击 `保存配置`
5. 点击 `应用当前模式`
6. 在右侧状态区和日志区确认结果
7. 如需管理本地中转站，在左下角“本地中转站”面板点击 `启动并打开` 或 `claude三方反代`

## 注意事项

- 文件统一按 `UTF-8 without BOM` 写入，避免 JSON/TOML 解析异常。
- 首版不自动重启 `Codex` 或 `Claude Code`。如果桌面端仍占用旧配置，手动关闭再打开即可。
- 这个工具只认三处主配置文件，不会同步旧的镜像副本目录。
