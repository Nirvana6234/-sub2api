//! 这台机器上这一次安装的身份 —— 托管 key 的名字要用它。
//!
//! 命名规则照搬小白端（`ManagedKeyNaming.cs`），因为那套规则里有几条是踩出来的：
//!
//! - **机器名**：一个账号的几台机器各自持有各自的租约，在一台上登出不会撤掉另一台的授权。
//! - **安装 ID**：同一台机器上前后两次安装分开，重装之后不会去收养一个自己说不清来历、
//!   却还在替它续期的租约。
//!
//! 两段都要，少哪段都会出上面那类问题。

use std::path::Path;

use crate::HostError;

/// 机器名 + 安装 ID。
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DeviceIdentity {
    pub machine_name: String,
    pub install_id: String,
}

const INSTALL_ID_FILE: &str = "install-id";

impl DeviceIdentity {
    /// 读出来，没有就生成一个存下。
    ///
    /// 安装 ID 存在程序数据目录里，跟着这次安装走：卸载重装会换一个新的，
    /// 那正是我们想要的 —— 新安装不该去续期旧安装留下的租约。
    ///
    /// # Errors
    /// 读写程序数据目录失败时返回。
    pub fn load_or_create(app_dir: impl AsRef<Path>) -> Result<Self, HostError> {
        let app_dir = app_dir.as_ref();
        std::fs::create_dir_all(app_dir)
            .map_err(|e| HostError::BadCodexHome(format!("建不出 {}: {e}", app_dir.display())))?;

        let path = app_dir.join(INSTALL_ID_FILE);
        let install_id = match std::fs::read_to_string(&path) {
            Ok(existing) if !existing.trim().is_empty() => existing.trim().to_owned(),
            _ => {
                let fresh = new_install_id();
                std::fs::write(&path, &fresh).map_err(|e| {
                    HostError::BadCodexHome(format!("写不了 {}: {e}", path.display()))
                })?;
                fresh
            }
        };

        Ok(DeviceIdentity {
            machine_name: machine_name(),
            install_id,
        })
    }
}

/// 机器名。**拿不到时给一个稳定的兜底**，而不是让整个身份解析失败 ——
/// 一个取不到主机名的环境不该导致 agent 完全不能用。
fn machine_name() -> String {
    for var in ["COMPUTERNAME", "HOSTNAME"] {
        if let Ok(value) = std::env::var(var) {
            let value = value.trim();
            if !value.is_empty() {
                return value.to_owned();
            }
        }
    }
    "unknown".to_owned()
}

/// 生成一个安装 ID。
///
/// 不引 uuid 依赖：这个值只需要**够独特到区分同机的前后两次安装**，
/// 不承担任何安全职责（它会出现在 key 的名字里，本来就是给人看的）。
fn new_install_id() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    // 掺一点进程地址空间的随机性，免得同一纳秒内的两次安装撞上。
    let entropy = &new_install_id as *const _ as usize;
    format!("{:x}{:x}", nanos, entropy)
        .chars()
        .rev()
        .take(12)
        .collect()
}
