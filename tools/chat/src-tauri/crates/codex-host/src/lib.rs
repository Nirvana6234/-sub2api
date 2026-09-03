//! 共飞 AI 工作台的 agent 宿主：**把 codex 关进笼子里养着**。
//!
//! [`codex_adapter`] 只说协议、刻意不认识进程；进程这一半在这里。两者都不认识 Tauri，
//! 所以都能脱开壳单测 —— A5 的 IPC 桥才是唯一要碰 Tauri 的地方。
//!
//! # 这个 crate 存在的理由只有一句话
//!
//! **关掉 Chat＝手机就够不着了。**
//!
//! 这是整个远程操作方案的安全前提。要让它成立，codex 进程的生命周期必须**严格套在**
//! Chat 进程里 —— 包括用户在任务管理器里点「结束任务」这种不给任何人跑析构函数机会的
//! 走法。见 [`cage`]。
//!
//! # 分工
//!
//! | | 管什么 |
//! |---|---|
//! | [`home::CodexHome`] | 私有 `CODEX_HOME`：不碰用户的 `~/.codex`，且**只能开在我们自己的程序目录下** |
//! | [`identity::DeviceIdentity`] | 机器名 + 安装 ID：托管 key 的名字要用它 |
//! | [`keylease`] | 托管 key 的**选择规则**（挑哪把、要不要续期）—— 只碰元数据，不碰密钥 |
//! | [`cage::Cage`] | 强杀宿主时连坐杀掉 codex（Windows Job Object） |
//! | [`engine::Engine`] | 起进程、交出 stdio、有序停止 |
//! | [`supervisor::Supervisor`] | 起不来时退避重试，并且**让失败被看见** |

pub mod cage;
pub mod engine;
pub mod home;
pub mod identity;
pub mod keylease;
pub mod supervisor;

pub use cage::Cage;
pub use engine::{Engine, EngineConfig};
pub use home::CodexHome;
pub use identity::DeviceIdentity;
pub use keylease::{key_name, needs_renewal, pick_current, KeyMeta};
pub use supervisor::{Backoff, FailedAttempt, Restarted, Supervisor};

/// 宿主这一层会出的错。
///
/// 和 [`codex_adapter::AdapterError`] 分开，是因为处置方式不同：协议错要报升级，
/// 这里的错要么是装配没做对（路径、二进制），要么是**安全前提没成立**（笼子没建上）。
#[derive(Debug, thiserror::Error)]
pub enum HostError {
    /// `CODEX_HOME` 不合法。
    #[error("CODEX_HOME 不合法: {0}")]
    BadCodexHome(String),

    /// 进程起不来。
    #[error("起不了 codex: {0}")]
    Spawn(String),

    /// 凭据没能从磁盘上抹掉。
    ///
    /// **这一条同样不能降级成警告。** 产品对用户的承诺是「key 只在内存里传，不落盘」；
    /// 抹不掉就意味着承诺没兑现，而用户无从知晓。宁可让这次会话起不来。
    #[error("凭据残留: {0}")]
    CredentialLeak(String),

    /// 笼子没建上 —— **这一条不能降级成警告**。
    ///
    /// 笼子失效意味着强杀 Chat 之后 codex 还活着，而「关掉 Chat 就够不着」这句话
    /// 会变成一个平时看不出来、出事才发现的假承诺。宁可起不来。
    #[error("进程笼: {0}")]
    Cage(String),
}
