//! 私有 `CODEX_HOME`。

use std::path::{Path, PathBuf};

use crate::HostError;

/// 一个**已经建好、且不在系统 temp 下**的 `CODEX_HOME`。
///
/// # 为什么要有这个类型
///
/// 两条都是实测出来的，靠注释提醒会忘：
///
/// 1. **必须给 codex 一个私有的 `CODEX_HOME`。** 指到自己的目录之后，codex 把
///    `auth.json` / `sessions/` / 几个 sqlite 都放进去，**用户自己的 `~/.codex`
///    全程不读不写**。于是「快照用户登录态→替换→还回去」那一整套机器都不需要。
/// 2. **不能放系统 temp。** 实测 codex 在 temp 下会拒绝创建 PATH 别名并告警：
///    `Refusing to create helper binaries under temporary dir`。那时它仍然能跑，
///    但已经是降级状态 —— 这种「能用但不对」最难查，所以直接拒掉。
///
/// 正确位置是应用数据目录（Windows 上是 `%APPDATA%` 一系）。具体给哪个由调用方决定，
/// 这个类型只负责把明显错的挡住。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CodexHome(PathBuf);

impl CodexHome {
    /// 校验并（按需）创建这个目录。
    ///
    /// # Errors
    /// 路径在系统 temp 之下、已存在但不是目录、或者创建失败时返回
    /// [`HostError::BadCodexHome`]。
    pub fn new(path: impl AsRef<Path>) -> Result<Self, HostError> {
        let path = path.as_ref();

        if is_under_temp(path) {
            return Err(HostError::BadCodexHome(format!(
                "{} 在系统 temp 下 —— codex 会拒绝在那里建 helper 并降级运行，\
                 请用应用数据目录",
                path.display()
            )));
        }

        if path.exists() {
            if !path.is_dir() {
                return Err(HostError::BadCodexHome(format!(
                    "{} 已存在但不是目录",
                    path.display()
                )));
            }
        } else {
            std::fs::create_dir_all(path).map_err(|e| {
                HostError::BadCodexHome(format!("建不出 {}: {e}", path.display()))
            })?;
        }

        // 后面要塞进环境变量，非 UTF-8 走不通。
        match path.to_str() {
            Some(_) => Ok(CodexHome(path.to_path_buf())),
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

    /// **握手之后立刻调用**：把 codex 刚写下的凭据从磁盘上抹掉。
    ///
    /// # 为什么必须做这件事
    ///
    /// 产品约定是「key 在内存里传递，不落盘」。但这个承诺**光靠调用方守不住** ——
    /// 实测：`account/login/start` 一送进去，codex 自己就把 key 写进
    /// `CODEX_HOME/auth.json`（96 字节，明文）。不管调用方多小心，文件就在那儿。
    ///
    /// 好在也实测了另一半：**登录之后立刻删掉这个文件，会话照常跑完一轮，
    /// 而且 codex 不会把它写回来**（拿到了 assistant 正文，结束时文件仍不存在）。
    /// 于是 key 在磁盘上的存活时间被压到毫秒级。
    ///
    /// 删之前先用零覆盖同样长度：删除只是摘掉目录项，内容还躺在扇区上。
    /// 这挡不住取证级恢复，但挡得住「翻一眼硬盘就看见」。
    ///
    /// # 尚未验证
    ///
    /// 只验过**一条连接上的一轮**。codex 在 401 重试、断线重连、`thread/resume`
    /// 时会不会回头重读这个文件，**没有验过**。真出现「删了之后长会话中途失效」，
    /// 第一个要怀疑的就是这里。
    ///
    /// # Errors
    /// 文件存在却删不掉时返回 —— **这一条不能吞**：删不掉就等于承诺没兑现。
    pub fn purge_credentials(&self) -> Result<bool, HostError> {
        let path = self.credentials_file();
        if !path.exists() {
            return Ok(false);
        }

        if let Ok(meta) = std::fs::metadata(&path) {
            let len = meta.len() as usize;
            // 覆盖失败不算致命（下一步的删除才是），但值得尽力。
            let _ = std::fs::write(&path, vec![0u8; len]);
        }

        std::fs::remove_file(&path).map_err(|e| {
            HostError::CredentialLeak(format!(
                "删不掉 {}：凭据留在了磁盘上，而我们承诺过不落盘（{e}）",
                path.display()
            ))
        })?;
        Ok(true)
    }
}

/// 这个路径是不是落在系统临时目录之下。
///
/// 只做前缀比较，不解析符号链接 —— 目的是挡住「顺手用了 temp」，不是防绕过。
fn is_under_temp(path: &Path) -> bool {
    let temp = std::env::temp_dir();
    // 规范化失败（比如目录还不存在）就退回到原始路径比较，宁可比得糙一点，
    // 也不要因为路径还没建就漏判。
    let canon = |p: &Path| p.canonicalize().unwrap_or_else(|_| p.to_path_buf());
    let temp = canon(&temp);

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
