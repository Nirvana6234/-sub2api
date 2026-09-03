//! 共飞 AI 工作台 ←→ codex app-server 的适配层。
//!
//! # 这个 crate 是什么
//!
//! Chat 什么都有了，**就是没有 agent 能力**。codex 只补这一块。这个 crate 是我们自己写的
//! 薄封装：**协议怎么说话归我们，agent 怎么想归上游**。它不引 Tauri、不认识 UI，
//! 可以脱开壳单独跑测试。
//!
//! # 边界
//!
//! - 只说 [`protocol`] 里那十来个方法。上游 87 个 `ClientRequest` 里剩下的九成不碰。
//! - **不 spawn 进程。** 收发架在 `AsyncRead`/`AsyncWrite` 上，进程笼是宿主（A4）的事。
//!   这样单测能拿 `tests/fixtures/` 里录制的真实报文跑完整条链路。
//! - 运行时对未知字段/未知方法宽松，漂移交给对着真二进制导出的 schema 做的测试去抓。
//!
//! # 凭据
//!
//! 走 [`protocol::ClientRequest::LoginApiKey`]，**不是环境变量**。`CODEX_API_KEY` 对
//! app-server 无效（那个开关在上游被硬编码成 false，只对把 app-server 当库嵌入的用法有效）。
//! 凭据是一条 RPC 的好处：不会被子进程继承，也不出现在进程列表里。

pub mod error;
pub mod protocol;
pub mod session;
pub mod transport;

pub use error::{AdapterError, RpcError};
pub use protocol::{
    Answer, ApprovalDecision, ApprovalPolicy, ClientRequest, ErrorNotification, GrantScope,
    Notification, NotificationPayload, ReasoningKind, RequestId, SandboxMode, ServerMessage,
    ServerRequest, ServerRequestPayload, ThreadStatus, WorkspaceDir,
};
pub use session::{drive_turn, Session, SessionEvent, TurnOutcome, TurnStatus};
pub use transport::{decode_line, pump, Client, Incoming};

/// 这个 crate 的类型是照着哪个 codex 版本导出的 schema 写的。
///
/// bump codex 时：重跑 `scripts/capture-fixtures.py` 重录 fixture，改这里，跑 `cargo test`。
/// 测试挂了就是协议漂了 —— 类型检查抓不到 JSON-RPC 的语义漂移，只有报文能。
pub const PINNED_CODEX_VERSION: &str = "0.153.0";
