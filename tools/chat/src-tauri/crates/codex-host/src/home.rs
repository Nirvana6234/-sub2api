//! 私有 `CODEX_HOME`。

use std::path::{Path, PathBuf};

use crate::HostError;

/// codex 的私有工作目录 —— **只能开在我们自己的程序数据目录之下**。
///
/// # 两条规矩，都是实测逼出来的
///
/// **① 必须私有，不能用用户的 `~/.codex`。** 指到自己的目录之后，codex 把
/// `auth.json`/`sessions/`/几个 sqlite 都放进去，用户自己的 `~/.codex` 全程不读不写。
/// 于是「快照用户登录态 → 替换 → 还回去」那一整套机器都不需要。
/// 更要紧的是：codex 是**按 `auth.json` 里有什么**选凭据的，OAuth tokens 优先于旁边的
/// key —— 共用 home 会得到一个「看着正常、其实在扣用户 ChatGPT 套餐」的配置。
///
/// **② 必须在我们自己的程序目录下。** 凭据落盘是可以接受的（我们是 codex 的宿主，
/// 这本来就该我们管），但**落在哪儿不能随便**。所以这个类型只提供
/// [`CodexHome::under_app_dir`] 一个构造函数：位置由程序数据目录推出来，
/// 调用方没有机会把它指到别处 —— 也就没有机会把凭据写到一个我们管不着的地方。
///
/// 顺带挡掉系统 temp：实测 codex 在 temp 下会拒绝创建 PATH 别名并告警
/// （`Refusing to create helper binaries under temporary dir`），之后仍然能跑，
/// 但已是降级状态。这种「能用但不对」最难查。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CodexHome(PathBuf);

/// `CODEX_HOME` 在程序数据目录下的固定位置。
const HOME_SUBDIR: &str = "codex-home";

impl CodexHome {
    /// 在程序数据目录下开一个私有 `CODEX_HOME`（按需创建）。
    ///
    /// `app_dir` 是**我们这个程序**的数据目录（Tauri 那边由 `app_data_dir()` 给出）。
    ///
    /// # Errors
    /// `app_dir` 落在系统 temp 之下、路径被文件占着、创建失败，或路径不是合法 UTF-8。
    pub fn under_app_dir(app_dir: impl AsRef<Path>) -> Result<Self, HostError> {
        let app_dir = app_dir.as_ref();

        if is_under_temp(app_dir) {
            return Err(HostError::BadCodexHome(format!(
                "{} 在系统 temp 下 —— codex 会拒绝在那里建 helper 并降级运行，\
                 请传程序自己的数据目录",
                app_dir.display()
            )));
        }

        let path = app_dir.join(HOME_SUBDIR);

        if path.exists() {
            if !path.is_dir() {
                return Err(HostError::BadCodexHome(format!(
                    "{} 已存在但不是目录",
                    path.display()
                )));
            }
        } else {
            std::fs::create_dir_all(&path).map_err(|e| {
                HostError::BadCodexHome(format!("建不出 {}: {e}", path.display()))
            })?;
        }

        // 后面要塞进环境变量，非 UTF-8 走不通。
        match path.to_str() {
            Some(_) => Ok(CodexHome(path)),
            None => Err(HostError::BadCodexHome(format!(
                "路径不是合法 UTF-8: {}",
                path.display()
            ))),
        }
    }

    pub fn as_path(&self) -> &Path {
        &self.0
    }

    /// 凭据在这个目录里的落点。
    pub fn credentials_file(&self) -> PathBuf {
        self.0.join("auth.json")
    }

    /// 为 Windows PowerShell 准备一份只属于 Chat 的 UTF-8 profile。
    ///
    /// Windows PowerShell 5.1 默认会沿用系统代码页。Codex 的命令执行器会把
    /// PowerShell 输出当 UTF-8 处理，因此中文文件内容会在进入模型和界面前就变成乱码。
    /// profile 放在私有 `CODEX_HOME` 下，配合启动时注入的 `USERPROFILE` 使用，不碰用户
    /// 全局的 `Documents\WindowsPowerShell`。
    pub fn prepare_powershell_utf8_profile(&self) -> Result<(), HostError> {
        #[cfg(windows)]
        {
            let profile_dir = self.0.join("Documents").join("WindowsPowerShell");
            std::fs::create_dir_all(&profile_dir).map_err(|e| {
                HostError::BadCodexHome(format!(
                    "建不出 PowerShell 私有配置目录 {}: {e}",
                    profile_dir.display()
                ))
            })?;

            let profile = profile_dir.join("Microsoft.PowerShell_profile.ps1");
            // UTF-8 BOM 让 Windows PowerShell 5.1 正确识别这份脚本的编码。
            //
            // 两行分别管两件不同的事，**都要有**，少一行就是一半乱码：
            // - `[Console]::OutputEncoding` / `$OutputEncoding` 管的是"控制台
            //   这一层"的编解码——PowerShell 自己往外写字符、以及读取外部命令
            //   （比如 `git log`）stdout 时用它。
            // - `$PSDefaultParameterValues['*:Encoding']` 管的是**文件 I/O**——
            //   `Get-Content`/`Set-Content`/`Out-File` 这些 cmdlet 在 Windows
            //   PowerShell 5.1 里默认读写用的是系统 ANSI 代码页，不是 UTF-8，
            //   跟控制台编码毫不相干。第一版只设了前者，实测撞见：
            //   `Get-Content README.md` 读中文 markdown 文件直接读成乱码，
            //   而同一轮里模型自己生成的文字（没经过文件读取）显示正常——
            //   两种编码分别管两条不同的路径，只堵一条另一条照样漏。
            const PROFILE: &[u8] = b"\xEF\xBB\xBF\
$utf8 = New-Object System.Text.UTF8Encoding($false)\n\
[Console]::OutputEncoding = $utf8\n\
$OutputEncoding = $utf8\n\
$PSDefaultParameterValues['*:Encoding'] = 'utf8'\n";
            std::fs::write(&profile, PROFILE).map_err(|e| {
                HostError::BadCodexHome(format!(
                    "写不出 PowerShell UTF-8 配置 {}: {e}",
                    profile.display()
                ))
            })?;
        }

        Ok(())
    }

    /// 把凭据从磁盘上抹掉。**会话结束 / 登出时调。**
    ///
    /// # 为什么是「结束时」而不是「握手后立刻」
    ///
    /// 实测：`account/login/start` 一送进去，codex 自己就把明文 key 写进
    /// `CODEX_HOME/auth.json`。也实测过**握手后立刻删掉，一轮照样跑得完**，
    /// codex 不会把它写回来。
    ///
    /// 但那条路只验过**一条连接上的一轮**：codex 在 401 重试、断线重连、
    /// `thread/resume` 时会不会回头重读这个文件，**没有验过**。既然凭据落在
    /// 我们自己的程序目录下是可接受的，就没必要为一个不必要的约束去扛那个
    /// 「长会话中途莫名失效」的风险。
    ///
    /// 所以：**会话期间留着，结束时抹掉。** 删之前先用等长的零覆盖 ——
    /// 删除只摘掉目录项，内容还躺在扇区上。挡不住取证级恢复，但挡得住随手一翻。
    ///
    /// # Errors
    /// 文件存在却删不掉时返回。
    pub fn wipe_credentials(&self) -> Result<bool, HostError> {
        let path = self.credentials_file();
        if !path.exists() {
            return Ok(false);
        }

        if let Ok(meta) = std::fs::metadata(&path) {
            // 覆盖失败不算致命（真正要紧的是下一步的删除），但值得尽力。
            let _ = std::fs::write(&path, vec![0u8; meta.len() as usize]);
        }

        std::fs::remove_file(&path).map_err(|e| {
            HostError::CredentialLeak(format!("删不掉 {}：凭据留在磁盘上（{e}）", path.display()))
        })?;
        Ok(true)
    }
}

/// 这个路径是不是落在系统临时目录之下。
///
/// 只做前缀比较，不解析符号链接 —— 目的是挡住「顺手用了 temp」，不是防绕过。
fn is_under_temp(path: &Path) -> bool {
    let canon = |p: &Path| p.canonicalize().unwrap_or_else(|_| p.to_path_buf());
    let temp = canon(&std::env::temp_dir());

    let mut cursor = path.to_path_buf();
    loop {
        if canon(&cursor) == temp {
            return true;
        }
        match cursor.parent() {
            Some(parent) if parent != cursor => cursor = parent.to_path_buf(),
            _ => return false,
        }
    }
}
