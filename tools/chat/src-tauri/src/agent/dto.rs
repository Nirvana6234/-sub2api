//! 前端看得到的**唯一**一套类型。
//!
//! # 前端不认识 codex 协议
//!
//! 这不是洁癖，是从 A2/A3 实测里得来的教训：
//!
//! - 同一个「拒绝」，v2 方法说 `decline`、旧方法说 `denied`；
//! - 三类审批的**响应形状互不相同**，权限那类甚至没有「拒绝」这个值；
//! - 被打断的一轮走的是 `turn/completed`，靠 `status` 字符串才分得出来。
//!
//! 这些细节一旦漏到前端，就会以「某个按钮偶尔不生效」的形式回来找我们。所以
//! 前端只发**语义**（同意/拒绝/拒绝并中断），线上说哪个词由 Rust 侧照着那条请求决定。
//!
//! # 这是线格式，不是调试转储
//!
//! 每个变体都带显式 tag。`passthrough` 与 `decodeError` 是**诊断**，必须能到达界面，
//! 但**不能长得像正经 agent 事件** —— 否则 A6 会把一条上游新通知当成 agent 的输出画出来。

use serde::{Deserialize, Serialize};

/// 推给前端的事件。
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "type", rename_all = "camelCase")]
pub enum UiEvent {
    #[serde(rename_all = "camelCase")]
    ThreadStarted { thread_id: String },

    #[serde(rename_all = "camelCase")]
    TurnStarted { turn_id: Option<String> },

    /// assistant 正文增量。
    ///
    /// 带 `turn_id` 是刻意的：会话的 `current_turn()` 是**「此刻」**的状态，
    /// 会跑在事件消费者前面，**不能用来判断某个事件属于哪一轮**。归属信息只能在事件里。
    #[serde(rename_all = "camelCase")]
    AgentText {
        turn_id: Option<String>,
        item_id: String,
        delta: String,
    },

    #[serde(rename_all = "camelCase")]
    Reasoning {
        turn_id: Option<String>,
        delta: String,
        /// `text` / `summaryText` / `summaryPart`
        kind: String,
    },

    #[serde(rename_all = "camelCase")]
    CommandOutput {
        turn_id: Option<String>,
        item_id: Option<String>,
        chunk: String,
    },

    /// 一个 item 开始/结束。审批要显示「在批什么」全靠它。
    #[serde(rename_all = "camelCase")]
    Item {
        item_id: Option<String>,
        item_type: String,
        /// 结束时才有；拒绝之后是 `"declined"`。
        status: Option<String>,
        /// 原始 item（含 fileChange 的 `changes[]`：路径与 diff）。
        item: serde_json::Value,
    },

    /// 会话正卡在审批上（或不卡了）。两端靠它显示同一个状态。
    #[serde(rename_all = "camelCase")]
    Status {
        waiting_on_approval: bool,
        flags: Vec<String>,
    },

    /// 要人做决定了。
    #[serde(rename_all = "camelCase")]
    ApprovalRequested(ApprovalRequest),

    /// 某条审批已经被答复了（可能是另一端答的）—— 界面上该把它从队列里去掉。
    #[serde(rename_all = "camelCase")]
    ApprovalResolved { request_id: String },

    /// 出错了，但 codex 会自己重试。**这不是终态**，界面该显示「正在重试」而不是「失败」。
    #[serde(rename_all = "camelCase")]
    Retrying {
        message: String,
        http_status: Option<u16>,
        /// 401/403 时该引导重新登录，而不是让用户干等。
        auth_failure: bool,
    },

    #[serde(rename_all = "camelCase")]
    Failed {
        message: String,
        http_status: Option<u16>,
        auth_failure: bool,
    },

    #[serde(rename_all = "camelCase")]
    TurnCompleted {
        turn_id: Option<String>,
        /// 原样透出：`completed` / `interrupted` / 以后上游可能新增的。
        status: String,
        /// `status == "completed"`。**别在前端自己判断**，上游会加新状态。
        success: bool,
        interrupted: bool,
    },

    /// 引擎没了 —— 事件流断了。
    #[serde(rename_all = "camelCase")]
    EngineStopped { reason: String },

    /// **诊断**：我们没投影的上游通知，原样带着。界面不该把它当 agent 输出。
    #[serde(rename_all = "camelCase")]
    Passthrough {
        method: String,
        raw: serde_json::Value,
    },

    /// **诊断**：有一行解不开，基本等于上游协议漂了。
    #[serde(rename_all = "camelCase")]
    DecodeError { line: String, error: String },
}

/// 一条待决的审批，**连同它要批的东西一起**。
///
/// A3 实测：`item/fileChange/requestApproval` 的参数里只有几个 id，
/// **没有 diff、没有路径**。真正的内容在同 `itemId` 的 `item/started` 里。
/// 这个关联在 Rust 侧做完再发给前端 —— 让每个界面自己重建一遍那张表，
/// 迟早会有一个地方建错，然后用户在看不见内容的情况下点了同意。
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ApprovalRequest {
    /// 答复时要原样传回来。
    pub request_id: String,
    /// 上游方法名，仅供显示与排错；**前端不要按它拼响应**。
    pub method: String,
    /// 人话说明这是要干什么。
    pub kind: ApprovalKind,
    pub reason: Option<String>,
    /// 命令执行：真正要跑的整条命令，**必须原样展示**。
    pub command: Option<String>,
    pub cwd: Option<String>,
    /// 关联到的 item（文件改动的 `changes[]` 在这里）。
    pub item: Option<serde_json::Value>,
    /// 文件改动申请「放开某个根目录的写权限」时，那个根目录。
    /// 有值时这不是一次性放行，而是一次作用域授权，界面必须显示出来。
    pub grant_root: Option<String>,
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "camelCase")]
pub enum ApprovalKind {
    /// 要跑一条命令。
    Command,
    /// 要改文件。
    FileChange,
    /// 要更大的权限（网络、工作区外的读写）。
    Permissions,
    /// 我们没见过的服务端请求。**仍然必须答复**，否则整轮卡死。
    Unknown,
}

/// 用户做的决定 —— **只有语义，没有线上词汇**。
///
/// 线上到底说 `decline` 还是 `denied`、还是「授一个空权限档案」，由 Rust 侧
/// 照着那条请求自己决定。前端不需要、也不应该知道。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum UiDecision {
    Approve,
    /// 本会话内同类不再问。
    ApproveForSession,
    /// 拒绝，**这一轮继续**（agent 会自己说明做不了）。
    Decline,
    /// 拒绝，**并立即中断整轮**。
    Cancel,
}

/// 起会话要的参数。
///
/// `codex_binary` 与 `api_key` 都由调用方给：**前者归 A10（打包与版本工程）**，
/// **后者归 A9（凭据托管）**。A5 只负责把它们接上，不越界替它们做决定。
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct StartParams {
    pub codex_binary: String,
    /// 私有 `CODEX_HOME`。
    pub codex_home: String,
    pub base_url: String,
    pub model: String,
    pub api_key: String,
    /// agent 的工作目录。
    pub cwd: String,
    /// 沙箱地板：`read-only` / `workspace-write` / `danger-full-access`。
    pub sandbox: String,
    /// 审批策略：`untrusted` / `on-request` / `never`。
    pub approval_policy: String,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct StartedThread {
    pub thread_id: String,
    /// 起了几次才成功。>1 说明前面失败过，界面可以提一句。
    pub attempts: u32,
}
