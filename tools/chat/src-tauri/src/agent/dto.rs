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
//! # 多会话并发改造：几乎每个事件都多了一个 `thread_id`
//!
//! 一个引擎进程现在能同时服务好几个对话（见 `AgentBridge` 模块文档），一条事件流
//! 里会交替出现属于不同对话的通知。**没有 `thread_id` 的事件前端没法归类** ——
//! 这不是可选的装饰字段，是这次改造能成立的前提。唯一的例外是 `EngineStopped`：
//! 它天生就是全局的（进程本身没了），前端要把它广播给**所有**还挂着的对话，
//! 不能只归到某一个。
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
    TurnStarted {
        thread_id: String,
        turn_id: Option<String>,
    },

    /// assistant 正文增量。
    ///
    /// 带 `turn_id` 是刻意的：会话的「此刻在跑哪个 turn」是**「此刻」**的状态，
    /// 会跑在事件消费者前面，**不能用来判断某个事件属于哪一轮**。归属信息只能在事件里。
    /// `thread_id` 同理，但归的是"哪个对话"这一层——多会话并发之后这个更要紧。
    #[serde(rename_all = "camelCase")]
    AgentText {
        thread_id: String,
        turn_id: Option<String>,
        item_id: String,
        delta: String,
    },

    #[serde(rename_all = "camelCase")]
    Reasoning {
        thread_id: String,
        turn_id: Option<String>,
        delta: String,
        /// `text` / `summaryText` / `summaryPart`
        kind: String,
    },

    #[serde(rename_all = "camelCase")]
    CommandOutput {
        thread_id: String,
        turn_id: Option<String>,
        item_id: Option<String>,
        chunk: String,
    },

    #[serde(rename_all = "camelCase")]
    TurnDiffUpdated {
        thread_id: String,
        turn_id: String,
        diff: String,
    },

    #[serde(rename_all = "camelCase")]
    FileChangePatchUpdated {
        thread_id: String,
        turn_id: String,
        item_id: String,
        changes: serde_json::Value,
    },

    #[serde(rename_all = "camelCase")]
    FileChangeOutputDelta {
        thread_id: String,
        turn_id: String,
        item_id: String,
        delta: String,
    },

    #[serde(rename_all = "camelCase")]
    PlanDelta {
        thread_id: String,
        turn_id: String,
        item_id: String,
        delta: String,
    },

    #[serde(rename_all = "camelCase")]
    TurnPlanUpdated {
        thread_id: String,
        turn_id: String,
        plan: serde_json::Value,
        explanation: Option<String>,
    },

    #[serde(rename_all = "camelCase")]
    TerminalInteraction {
        thread_id: String,
        turn_id: String,
        item_id: String,
        process_id: String,
        stdin: String,
    },

    #[serde(rename_all = "camelCase")]
    TurnModerationMetadata {
        thread_id: String,
        turn_id: String,
        metadata: serde_json::Value,
    },

    /// 一个 item 开始/结束。审批要显示「在批什么」全靠它。
    #[serde(rename_all = "camelCase")]
    Item {
        thread_id: String,
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
        thread_id: String,
        waiting_on_approval: bool,
        flags: Vec<String>,
    },

    /// 要人做决定了。`thread_id` 已经在 `ApprovalRequest` 里带着。
    #[serde(rename_all = "camelCase")]
    ApprovalRequested(ApprovalRequest),

    /// 某条审批已经被答复了（可能是另一端答的）—— 界面上该把它从队列里去掉。
    #[serde(rename_all = "camelCase")]
    ApprovalResolved { thread_id: String, request_id: String },

    /// 出错了，但 codex 会自己重试。**这不是终态**，界面该显示「正在重试」而不是「失败」。
    #[serde(rename_all = "camelCase")]
    Retrying {
        /// 不是所有错误都能确定属于哪条 thread（比如握手阶段的错误）。
        thread_id: Option<String>,
        message: String,
        http_status: Option<u16>,
        /// 401/403 时该引导重新登录，而不是让用户干等。
        auth_failure: bool,
    },

    /// 同一条 thread 连续重试太多次，**我们自己**放弃了、主动打断了这一轮。
    ///
    /// codex 的 `Retrying` 是它自己的重连节奏，理论上可以无限循环下去——真实撞见过
    /// 一次网关并发限流连续推了 4 条以上还没完（用户账号的并发配额被设成了 1）。
    /// 界面只显示「正在重试」的话，用户没有任何办法知道"到底还要等多久/是不是
    /// 该放弃了"。这是**终态**：这一轮已经被 `turn/interrupt` 主动打断，不会再有
    /// 这条 thread 的后续事件（除了 codex 对打断本身的确认）。
    #[serde(rename_all = "camelCase")]
    GaveUpRetrying {
        thread_id: String,
        attempts: usize,
        last_message: String,
    },

    /// codex 主动说给用户听的一句话（比如模型元数据缺失时的降级提示）。
    /// **必须显示**，但和 `Passthrough`/`DecodeError` 不同——这条是认识、
    /// 且认为用户该看到的，不该套诊断那种「协议可能漂了」的措辞。
    #[serde(rename_all = "camelCase")]
    Warning {
        thread_id: Option<String>,
        message: String,
    },

    /// 协议已知、但 payload 仍可能变化的通知。前端按 `method` 展示，
    /// 同时保留原始参数，避免把上游的新字段静默丢掉。
    #[serde(rename_all = "camelCase")]
    KnownNotification {
        method: String,
        thread_id: Option<String>,
        raw: serde_json::Value,
    },

    #[serde(rename_all = "camelCase")]
    Failed {
        thread_id: Option<String>,
        message: String,
        http_status: Option<u16>,
        auth_failure: bool,
    },

    #[serde(rename_all = "camelCase")]
    TurnCompleted {
        thread_id: String,
        turn_id: Option<String>,
        /// 原样透出：`completed` / `interrupted` / 以后上游可能新增的。
        status: String,
        /// `status == "completed"`。**别在前端自己判断**，上游会加新状态。
        success: bool,
        interrupted: bool,
    },

    /// 一条 thread 被主动归档了（`agent_end_thread` 的结果）——**只影响这一条**，
    /// 引擎和别的对话都还在跑。和 `EngineStopped`（全局、进程没了）是两回事。
    #[serde(rename_all = "camelCase")]
    ThreadEnded { thread_id: String },

    /// 引擎没了 —— 事件流断了。**这是全局事件**：所有挂着的对话都要收到，
    /// 前端不能只把它归给"当前显示的那个"。
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
    /// 这条审批属于哪个对话——多会话并发之后，界面必须靠它才能把审批
    /// 显示在正确的对话下面，而不是随手挂在"当前显示的那个"上。
    /// 尽力而为（见 `ServerRequest::thread_id`），极少数没挖出来的落 `None`。
    pub thread_id: Option<String>,
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

/// 起一条新 thread 要的参数。
///
/// **这里刻意没有 `codexBinary` 和 `appDir`。**
///
/// 两条都由 Rust 侧定（见 `AgentPaths`）。A9 把「凭据只落在自己程序目录下」做成了
/// 结构性保证，可只要这个结构体还带着 `appDir`，那个保证就离一次 `invoke` 只有一步；
/// `codexBinary` 同理 —— 随包发之后，让渲染进程点名一个任意 exe 没有任何好处。
///
/// `deny_unknown_fields` 是这条约定的**执法者**：谁要是又从前端把这两个字段塞回来，
/// 会当场反序列化失败，而不是被静默忽略然后让人以为它生效了。
///
/// # 多会话并发改造之后，这不再是"起会话"，是"起一条 thread"
///
/// 引擎（进程 + 转发层 + 握手）第一次调用时才懒起，之后**复用同一个**——
/// 这个结构体每次调用都在描述"要在这个共享引擎上再开一条 thread"，`model`
/// 只在引擎第一次起来时当命令行的默认值用（每一轮真正用的模型走 [`SendParams`]，
/// 不依赖这里）。
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct StartParams {
    /// **中转站根地址**（例如 `https://relay.example.com`），不是给 codex 的。
    ///
    /// codex 永远看不到它：它拿到的是本地转发层那个回环地址。见 `codex_host::LocalRelay`。
    pub relay_base_url: String,
    /// 这条 thread 用哪个分组。
    ///
    /// 分组在这里是**每条 thread 一个**，不再绑在某把 key 上 —— 转发层按 `thread-id`
    /// 把它翻成后端要的 `X-Paw-Group-Id`。想换分组就开一条新 thread，
    /// 不需要（也不允许）去改任何一把 key。
    pub group_id: i64,
    /// 当前账号会话（JWT）。**前端是它唯一的持有者**；刷新之后用
    /// `agent_set_session_token` 推下来，不必重启引擎。
    pub session_token: String,
    /// **登录那次浏览器请求的 `User-Agent`**（`navigator.userAgent`）。
    ///
    /// 不是随手加的字段：后端的账号会话绑着一个「IP + UA」指纹
    /// （`enforceSessionBinding`），指纹在登录时随 `session_token` 一起签发。
    /// 转发层是用 `reqwest` 发请求的，默认 UA 是 `reqwest/x.y.z`，和登录时
    /// 浏览器发的不一样 —— 同一个 token、两种指纹，后端会判成会话被搬到了
    /// 别的网络环境，直接 401 `SESSION_BINDING_MISMATCH` 并撤销整个 token
    /// family。这里必须原样传浏览器自己的 UA，转发层才能在转发时冒充回去。
    pub client_user_agent: String,
    /// 引擎第一次起来时的命令行默认模型。**不是这条 thread 真正会用的模型** ——
    /// 那个每轮都从 [`SendParams::model`] 传，覆盖这里的默认值。
    pub model: String,
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
    /// 引擎起了几次才成功。>1 说明前面失败过，界面可以提一句。
    /// 复用已有引擎时恒为 1。
    pub attempts: u32,
}

/// 发一轮提问要的参数。
///
/// `model`/`reasoning` **必须每轮都传**——多会话并发之后，模型不再是引擎起来时
/// 定死的常量，一个进程要同时服务好几个用了不同模型的对话。
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SendParams {
    pub thread_id: String,
    pub text: String,
    pub model: String,
    /// 推理强度。**空字符串表示"标准/不覆盖"**，翻成协议层的 `None`——
    /// 上游的 `effort` 字段要求非空字符串，传空串会被拒。
    pub reasoning: String,
}
