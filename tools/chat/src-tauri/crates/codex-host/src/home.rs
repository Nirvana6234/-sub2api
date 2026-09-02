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
