//! codex app-server 协议的**最小子集**。
//!
//! 上游 `ClientRequest` 有 87 个方法、`ServerNotification` 有 68 个、`ServerRequest` 有 10 个。
//! 我们只用其中十来个 —— 我们要的是 agent 循环，不是 codex 的全部功能面。
//!
//! # 两条规矩
//!
//! 1. **运行时宽松，测试时严格。** 这里所有类型都忽略未知字段，未知方法落到 `Other`。
//!    上游加字段不能让 app 崩 —— 我们的前提就是跟着上游版本走。漂移由
//!    `tests/` 里对着真二进制导出的 schema 做的对比来抓，不是靠运行时报错。
//! 2. **永远保留 `raw`。** 每条通知和服务端请求都带着原始 `params`，即使我们没投影它。
//!    本机链路要能显示上游的新事件，而不是等我们发版才认识它。
//!
//! 类型形状取自 `protocol/0.153.0/` 下由 `codex app-server generate-json-schema`
//! 导出的 schema，并用 `tests/fixtures/` 里真实录制的报文校对过。

use serde::{Deserialize, Serialize};
use serde_json::Value;

/// JSON-RPC 的请求 id：schema 里是 `string | int64`。
#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(untagged)]
pub enum RequestId {
    Num(i64),
    Str(String),
}

impl std::fmt::Display for RequestId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            RequestId::Num(n) => write!(f, "{n}"),
            RequestId::Str(s) => write!(f, "{s}"),
        }
    }
}

// ---------------------------------------------------------------------------
// 客户端 -> 服务端
// ---------------------------------------------------------------------------

/// 我们会发的方法。**只有这些** —— 别顺手加，每加一个都要想清楚宿主凭什么有这个能力。
///
/// 尤其注意：`fs/*` 与 `command/exec` 这类让 app-server 直接碰文件系统和执行命令的方法
/// **一个都不在这里**，远程链路永不开放，本机也不建议开。agent 自己的工具调用走沙箱
/// 加审批，那是另一条路。
#[derive(Debug, Clone)]
pub enum ClientRequest {
    /// 握手。必须第一个发，之后要跟一条 `initialized` 通知。
    Initialize { client_name: String, client_version: String },
    /// 送凭据。**必须走这条**，`CODEX_API_KEY` 环境变量对 app-server 无效
    /// （`enable_codex_api_key_env` 在 app-server 里被硬编码成 false）。
    /// 好处是凭据只是一条 RPC，不会被子进程继承、不出现在进程列表里。
    LoginApiKey { api_key: String },
    /// 起一个新会话。沙箱与审批策略是**按调用传**的，不依赖用户的 `~/.codex/config.toml`。
    ///
    /// `cwd` 只能由 [`WorkspaceDir::new`] 构造 —— 因为 codex 自己不校验目录，见那里的说明。
    ThreadStart { cwd: WorkspaceDir, sandbox: SandboxMode, approval_policy: ApprovalPolicy },
    ThreadResume { thread_id: String },
    ThreadList { limit: Option<u32> },
    ThreadArchive { thread_id: String },
    ThreadCompactStart { thread_id: String },
    /// 发一轮提问。
    ///
    /// `model`/`effort` **必须每轮都传**（不是只在起会话时传一次）——多会话并发改造
    /// 之后，一个 codex 进程要同时服务好几个对话，模型不再是起进程时 `-c model=...`
    /// 定死的进程级常量。schema 原文："Override the model/reasoning effort for this
    /// turn and subsequent turns"，`effort` 对应的是 `ReasoningEffort`，
    /// **要求非空字符串**（`minLength: 1`）——所以「标准」挡板要映射成 `None`，
    /// 不能发一个空串上去。
    TurnStart { thread_id: String, text: String, model: Option<String>, effort: Option<String> },
    /// 打断一轮。**必须带 `turn_id`** —— 只给 `thread_id` 是不够的，
    /// 所以会话层必须自己跟踪当前这一轮的 id。
    TurnInterrupt { thread_id: String, turn_id: String },
}

/// 一个**已经确认存在、且确实是目录**的工作目录。
///
/// 存在这个类型，是因为实测发现 **codex 不校验 `cwd`**：`thread/start` 传一个根本不存在的
/// 路径，它照样把会话建起来、照发 `thread/started`，全程零报错。于是「用户填了个不存在的
/// 目录」这件事会一路悄悄穿过去，直到 agent 开始干活才以别的面目炸出来。
///
/// 校验点放在这里而不是调用方，是因为**每个调用方都会忘**。类型带着不变量，忘不了。
/// A8 的目录白名单是在这之上再加一层策略，不是替代它。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WorkspaceDir(String);

impl WorkspaceDir {
    /// 校验并接受一个工作目录。
    ///
    /// # Errors
    /// 路径不存在、不是目录，或者不是合法 UTF-8 时返回 [`AdapterError::InvalidPath`]。
    pub fn new(path: impl AsRef<std::path::Path>) -> Result<Self, crate::error::AdapterError> {
        let path = path.as_ref();
        if !path.exists() {
            return Err(crate::error::AdapterError::InvalidPath(format!(
                "目录不存在: {}",
                path.display()
            )));
        }
        if !path.is_dir() {
            return Err(crate::error::AdapterError::InvalidPath(format!(
                "不是目录: {}",
                path.display()
            )));
        }
        match path.to_str() {
            Some(s) => Ok(WorkspaceDir(s.to_owned())),
            None => Err(crate::error::AdapterError::InvalidPath(format!(
                "路径不是合法 UTF-8: {}",
                path.display()
            ))),
        }
    }

    pub fn as_str(&self) -> &str {
        &self.0
    }
}

/// 沙箱地板。这是**不依赖用户点头**的那一层，审批是它之上的第二层。
///
/// 对应 schema 的 `SandboxMode`，是 `thread/start` 的 `sandbox` 字段用的编码。
///
/// **陷阱**：`turn/start` 的 `sandboxPolicy` 用的是**另一个类型** `SandboxPolicy`，
/// 同样的概念却是 camelCase（`readOnly` / `workspaceWrite` / `dangerFullAccess`）。
/// 别把这个类型直接塞到那个字段上 —— 值会被静默拒绝。我们目前只用 `thread/start`。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum SandboxMode {
    ReadOnly,
    WorkspaceWrite,
    /// 不要在产品里默认给这个。
    DangerFullAccess,
}

/// 审批策略，对应 schema 的 `AskForApproval`。
///
/// 只有这三个取值 —— 0.153.0 里**没有** `on-failure`（老版本有过，别照旧文档写）。
/// 编码是 kebab-case，写成 camelCase 会被服务端拒掉。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum ApprovalPolicy {
    /// 除白名单外一律问。
    Untrusted,
    /// agent 认为需要提权时才问。这是我们要的默认。
    OnRequest,
    /// 从不问 —— 等于关掉人工审批，只剩沙箱地板。
    Never,
}

impl ClientRequest {
    pub fn method(&self) -> &'static str {
        match self {
            ClientRequest::Initialize { .. } => "initialize",
            ClientRequest::LoginApiKey { .. } => "account/login/start",
            ClientRequest::ThreadStart { .. } => "thread/start",
            ClientRequest::ThreadResume { .. } => "thread/resume",
            ClientRequest::ThreadList { .. } => "thread/list",
            ClientRequest::ThreadArchive { .. } => "thread/archive",
            ClientRequest::ThreadCompactStart { .. } => "thread/compact/start",
            ClientRequest::TurnStart { .. } => "turn/start",
            ClientRequest::TurnInterrupt { .. } => "turn/interrupt",
        }
    }

    pub fn params(&self) -> Value {
        match self {
            ClientRequest::Initialize { client_name, client_version } => serde_json::json!({
                "clientInfo": { "name": client_name, "title": null, "version": client_version },
                "capabilities": null,
            }),
            ClientRequest::LoginApiKey { api_key } => serde_json::json!({
                "type": "apiKey",
                "apiKey": api_key,
            }),
            ClientRequest::ThreadStart { cwd, sandbox, approval_policy } => serde_json::json!({
                "cwd": cwd.as_str(),
                "sandbox": sandbox,
                "approvalPolicy": approval_policy,
            }),
            ClientRequest::ThreadResume { thread_id } => serde_json::json!({ "threadId": thread_id }),
            ClientRequest::ThreadList { limit } => match limit {
                Some(n) => serde_json::json!({ "limit": n }),
                None => serde_json::json!({}),
            },
            ClientRequest::ThreadArchive { thread_id } => serde_json::json!({ "threadId": thread_id }),
            ClientRequest::ThreadCompactStart { thread_id } => {
                serde_json::json!({ "threadId": thread_id })
            }
            ClientRequest::TurnStart { thread_id, text, model, effort } => {
                let mut params = serde_json::json!({
                    "threadId": thread_id,
                    "input": [{ "type": "text", "text": text, "text_elements": [] }],
                });
                if let Some(model) = model {
                    params["model"] = serde_json::json!(model);
                }
                if let Some(effort) = effort {
                    params["effort"] = serde_json::json!(effort);
                }
                params
            }
            ClientRequest::TurnInterrupt { thread_id, turn_id } => serde_json::json!({
                "threadId": thread_id,
                "turnId": turn_id,
            }),
        }
    }
}

// ---------------------------------------------------------------------------
// 服务端 -> 客户端
// ---------------------------------------------------------------------------

/// 一行 JSONL 解出来的东西。
#[derive(Debug, Clone)]
pub enum ServerMessage {
    /// 对我们某个请求的成功响应。
    Response { id: RequestId, result: Value },
    /// 对我们某个请求的失败响应。`id` 可能为空（服务端连请求都没解析出来）。
    Error { id: Option<RequestId>, error: crate::error::RpcError },
    /// 服务端反过来问我们 —— **必须回**，不回这一轮就卡住。
    Request(ServerRequest),
    /// 单向通知。
    Notification(Notification),
}

/// 服务端发起的请求。目前我们只认三类审批，其余落到 `Other`。
///
/// **`Other` 也必须回**：不认识的服务端请求如果不答复，turn 会永远等下去。
/// 处置见 [`ServerRequestPayload::Other`]。
#[derive(Debug, Clone)]
pub struct ServerRequest {
    pub id: RequestId,
    pub method: String,
    /// 原始参数，永远保留。
    pub raw: Value,
    pub payload: ServerRequestPayload,
}

#[derive(Debug, Clone)]
pub enum ServerRequestPayload {
    CommandApproval(Box<CommandApprovalParams>),
    FileChangeApproval(Box<FileChangeApprovalParams>),
    /// 权限申请。响应形状和另外两个**完全不同** —— 没有 `decision` 字段，
    /// 而且**没有「拒绝」这个值**：拒绝就是授一个空档案。
    PermissionsApproval(Box<PermissionsApprovalParams>),
    /// 我们没投影的服务端请求（含 `execCommandApproval` / `applyPatchApproval`
    /// 这两个旧方法 —— 没观测到它们在 v2 流程里出现，所以只留 `raw`）。
    ///
    /// **仍然必须答复**，否则 turn 卡死。[`ServerRequest::deny`] 对它们也给得出答案。
    Other,
}

/// `item/commandExecution/requestApproval` 的参数。
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct CommandApprovalParams {
    pub thread_id: String,
    pub turn_id: String,
    pub item_id: String,
    /// 给用户看的理由。
    #[serde(default)]
    pub reason: Option<String>,
    /// 真正要执行的整条命令。**UI 必须原样展示这个**，不要只显示摘要。
    pub command: String,
    pub cwd: String,
    /// 服务端建议这次给用户显示哪几个选项。
    ///
    /// 注意这**不是合法值全集**：schema 里 `decline` / `cancel` 永远合法，
    /// 但不一定出现在这个列表里。列表是 UI 提示，不是校验依据。
    #[serde(default)]
    pub available_decisions: Vec<Value>,
}

/// `item/fileChange/requestApproval` 的参数。
///
/// 注意它**没有** `availableDecisions`（命令执行那个才有）。之前照抄过来一个带
/// `#[serde(default)]` 的空 Vec，看着像数据、实际永远填不上 —— UI 读到零个选项
/// 会得出错误结论。已删掉。
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileChangeApprovalParams {
    pub thread_id: String,
    pub turn_id: String,
    pub item_id: String,
    #[serde(default)]
    pub reason: Option<String>,
    /// agent 在申请「允许写这个根目录之下的东西」。
    ///
    /// 有值时这**不是一次性的是/否**，而是一次作用域授权 —— UI 必须把这个路径
    /// 显示出来，否则用户在不知情的情况下放开了一整棵目录树。
    #[serde(default)]
    pub grant_root: Option<String>,
}

/// `item/permissions/requestApproval` 的参数。
///
/// # 未经实测
///
/// 这个类型是**照 schema 写的，没有录制报文佐证**。试过让 agent 申请网络与工作区外的
/// 读权限，它转而去试 shell 命令，走的是 `item/commandExecution/requestApproval` ——
/// 没能把这一类逼出来。所以这里的字段名、可空性、以及 `deny()` 给的空档案答复
/// **都还没被真实报文验证过**。哪天真触发了，先录一份 fixture 再回来改这里。
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PermissionsApprovalParams {
    pub thread_id: String,
    pub turn_id: String,
    pub item_id: String,
    pub cwd: String,
    #[serde(default)]
    pub reason: Option<String>,
    /// agent 想要的权限档案（文件系统 / 网络）。
    ///
    /// 我们**不解释**它的内部结构 —— 那是上游拥有、会增删的一大坨。原样带给 UI 展示，
    /// 由人来判断。
    pub permissions: Value,
}

/// 授权的作用范围。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum GrantScope {
    /// 只在这一轮有效。
    Turn,
    /// 整个会话都有效。
    Session,
}

/// 用户做出的审批决定。**这是给人看的语义，不是线上的字面量。**
///
/// 线上的字面量有**两套**，取决于是哪个方法在问：
///
/// | | `item/*/requestApproval`（v2） | `execCommandApproval` / `applyPatchApproval`（旧） |
/// |---|---|---|
/// | Accept | `accept` | `approved` |
/// | AcceptForSession | `acceptForSession` | `approved_for_session` |
/// | Decline | `decline` | `denied` |
/// | Cancel | `cancel` | `abort` |
///
/// 拿一套的词去答另一套的请求会被拒。所以**别自己拼 `{"decision": ...}`** ——
/// 用 [`ServerRequest::approve`] / [`ServerRequest::deny`]，让请求自己决定该说哪个词。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum ApprovalDecision {
    Accept,
    /// 本会话内同类不再问。
    AcceptForSession,
    /// 拒绝，**agent 继续这一轮**（会自己说明为什么做不了）。
    Decline,
    /// 拒绝，**并立即中断整轮**。
    Cancel,
}

impl ApprovalDecision {
    /// v2 的 `item/*/requestApproval` 用的词。
    fn v2_word(self) -> &'static str {
        match self {
            ApprovalDecision::Accept => "accept",
            ApprovalDecision::AcceptForSession => "acceptForSession",
            ApprovalDecision::Decline => "decline",
            ApprovalDecision::Cancel => "cancel",
        }
    }

    /// 旧的 `execCommandApproval` / `applyPatchApproval` 用的词。
    fn legacy_word(self) -> &'static str {
        match self {
            ApprovalDecision::Accept => "approved",
            ApprovalDecision::AcceptForSession => "approved_for_session",
            ApprovalDecision::Decline => "denied",
            ApprovalDecision::Cancel => "abort",
        }
    }
}

/// 一条**已经配好对**的答复：它记着自己要答的是哪个请求，形状也已经按那个请求定死。
///
/// 只能从 [`ServerRequest`] 上拿到，所以拿 A 类请求的响应去答 B 类请求这件事
/// 根本无法表达 —— 和 [`WorkspaceDir`] 是同一个手法。
#[derive(Debug, Clone)]
pub struct Answer {
    pub(crate) id: RequestId,
    pub(crate) body: AnswerBody,
}

#[derive(Debug, Clone)]
pub(crate) enum AnswerBody {
    Result(Value),
    /// 有些服务端请求我们**根本答不上来**（要一个证书 token、要 ChatGPT 的
    /// access token）。那种情况必须回 JSON-RPC 错误，而不是编一个假的 result ——
    /// 编出来的假值会一路流到上游去，比直接说"我不行"糟得多。
    Error { code: i64, message: String },
}

impl Answer {
    pub fn request_id(&self) -> &RequestId {
        &self.id
    }

    /// 拼成真正要发出去的 JSON-RPC 帧。
    ///
    /// 公开出来是为了让测试能检查「到底发了什么」，而不是只能相信注释。
    pub fn to_frame(&self) -> Value {
        match &self.body {
            AnswerBody::Result(value) => serde_json::json!({
                "jsonrpc": "2.0",
                "id": self.id,
                "result": value,
            }),
            AnswerBody::Error { code, message } => serde_json::json!({
                "jsonrpc": "2.0",
                "id": self.id,
                "error": { "code": code, "message": message },
            }),
        }
    }
}

/// 单向通知：方法名 + 原始参数 + 我们的投影。
#[derive(Debug, Clone)]
pub struct Notification {
    pub method: String,
    /// 原始参数，永远保留 —— 上游新事件不必等我们发版就能显示。
    pub raw: Value,
    pub payload: NotificationPayload,
}

/// 我们投影出来的通知。**没投影的不是错误**，落到 `Other` 并保留 `raw`。
#[derive(Debug, Clone)]
pub enum NotificationPayload {
    ThreadStarted { thread_id: String },
    /// `activeFlags` 里出现 `waitingOnApproval` 就说明这个会话正卡在审批上。
    /// PC 端与手机端都靠它显示同一个状态，不必自己发明机制。
    ThreadStatusChanged { thread_id: String, status: ThreadStatus },
    /// 一轮开始。**`turn_id` 要留住** —— `turn/interrupt` 必须带它。
    TurnStarted { thread_id: String, turn_id: Option<String> },
    TurnCompleted { thread_id: String, turn_id: Option<String>, status: String },
    /// 一个 item 开始了。
    ///
    /// **审批 UI 必须留着这些。** 实测：`item/fileChange/requestApproval` 请求里
    /// **只有几个 id**，没有 diff、没有路径、`reason` 都是 null —— 真正「改了什么」
    /// 在这条通知的 `item.changes[]` 里（`{path, kind, diff}`）。
    /// 也就是说**审批请求是个指针，不是载荷**：UI 得按 `item_id` 回查。
    ItemStarted { thread_id: String, item_type: String, item_id: Option<String>, item: Value },
    /// 一个 item 结束了。`item_status` 是**这一项**的结果，
    /// 拒绝审批之后它会是 `"declined"` —— 这是拒绝确实生效的逐项证据。
    ItemCompleted {
        thread_id: String,
        item_type: String,
        item_id: Option<String>,
        item_status: Option<String>,
        item: Value,
    },
    /// assistant 正文的增量。
    AgentMessageDelta { thread_id: String, item_id: String, delta: String },
    /// 思考过程的增量（正文 / 摘要 / 摘要分段）。
    ReasoningDelta { thread_id: String, item_id: Option<String>, delta: String, kind: ReasoningKind },
    /// 命令执行的输出增量。
    CommandOutputDelta { thread_id: String, item_id: Option<String>, chunk: String },
    /// 当前 turn 聚合后的最新 unified diff。
    TurnDiffUpdated { thread_id: String, turn_id: String, diff: String },
    /// 文件变更 item 的增量 diff。
    FileChangePatchUpdated {
        thread_id: String,
        turn_id: String,
        item_id: String,
        changes: Value,
    },
    /// 旧版 apply_patch 文本输出，仍然保留，避免上游偶发发送时静默丢失。
    FileChangeOutputDelta {
        thread_id: String,
        turn_id: String,
        item_id: String,
        delta: String,
    },
    /// 计划 item 的流式增量。
    PlanDelta {
        thread_id: String,
        turn_id: String,
        item_id: String,
        delta: String,
    },
    /// turn 当前完整计划快照。
    TurnPlanUpdated {
        thread_id: String,
        turn_id: String,
        plan: Value,
        explanation: Option<String>,
    },
    /// 交互式命令向 stdin 写入的内容。
    TerminalInteraction {
        thread_id: String,
        turn_id: String,
        item_id: String,
        process_id: String,
        stdin: String,
    },
    /// 内容审核元数据。元数据形状由上游决定，因此保留原始 Value。
    TurnModerationMetadata {
        thread_id: String,
        turn_id: String,
        metadata: Value,
    },
    /// 某个服务端请求已经被（可能是另一端）答复了 —— 用来做「一处响应、另一处队列消失」。
    ServerRequestResolved { thread_id: String, request_id: RequestId },
    /// 服务端推来的错误通知。
    ///
    /// **模型侧的失败几乎全走这里，而不是 JSON-RPC error 响应。** 实测：用一个错误的
    /// API key 发一轮，`turn/start` 正常返回成功，401 是以一串 `error` 通知的形式到达的。
    /// 所以只盯着请求响应做错误处理，会把上游错误整类漏掉。
    Error(ErrorNotification),
    /// codex 自己判断"用户该看到"的一句话提醒（schema 原文："Concise warning
    /// message for the user"）。**不是每一条都值得显示**——见 `is_known_noise_warning`
    /// 那条特别筛掉的例外。
    Warning { thread_id: Option<String>, message: String },
    /// 已知但暂时不为其建立强类型结构的通知。
    ///
    /// 这些方法属于当前协议，不能再落进 `Other`，但其中一部分 payload
    /// 仍在变化。保留原始参数，让上层按方法归类展示，同时避免丢字段。
    Known {
        method: String,
        thread_id: Option<String>,
        raw: Value,
    },
    /// **认识，但故意不投影成 UI 事件**的方法——和 `Other` 不是一回事。
    ///
    /// `Other` 意味着"这是什么我们不知道"，该被 UI 当成协议漂移的诊断显示出来；
    /// `Ignored` 意味着"知道这是什么，就是这台工作台用不上"。目前这些：
    ///
    /// - `thread/tokenUsage/updated`、`account/rateLimits/updated`——codex 自己的
    ///   ChatGPT 订阅套餐记账（`PlanType::free/plus/pro/team/...`、积分余额），
    ///   而我们走自定义 provider + 账号会话，配额和计费全在 sub2api 后端那一侧。
    /// - `remoteControl/status/changed`——codex 自己那个 `--remote-control` 功能
    ///   的连接状态（我们从不传这个参数，永远是 `disabled`）。我们的远程操作
    ///   方案是另一套，跟这个报的是两码事。
    /// - `account/login/completed`——**实测过它不携带我们需要的信息**：录到的两份
    ///   fixture 里，一份用的是真本地令牌、一份是故意错的（`bad-credentials`），
    ///   两边这条通知都回 `success: true, error: null`。也就是说它只确认"codex
    ///   收下了 `account/login/start` 这个调用形状、把凭据写进了 auth.json"，
    ///   不确认凭据是否真的能用——真能不能用要等第一次实际调用，那时候失败会走
    ///   `error` 通知（见 `NotificationPayload::Error`），我们已经在处理。
    /// - `account/updated`——账号级的 authMode/planType 广播。实测录到的值是
    ///   `{"authMode":"apikey","planType":null}`：authMode 是我们自己设的，
    ///   planType 用不上 ChatGPT 订阅套餐，两个字段都不携带新信息。
    /// - `thread/archived`——我们自己发 `thread/archive` 之后 codex 回的确认广播
    ///   （多会话并发改造：`end_thread`/切审批模式都会归档一条 thread）。
    ///   是我们自己动作的回声，不是需要界面关心的新信息。
    /// - 文件变更、计划、终端交互和审核元数据已经投影为独立事件，不能放回这里。
    ///
    /// 第一次真实使用就在界面上炸出一片黄色的"未识别的上游通知"——协议识别是对的
    /// （这些方法确实存在），错的是把「用不上」当成了「没见过」。
    Ignored { method: String },
    Other,
}

/// "warning" 方法里**内容匹配**筛掉的那一条：模型元数据缺失的降级提示。
///
/// 单独用一个函数而不是塞进上面 `Ignored` 那批按方法名匹配的白名单，是因为这条
/// 的方法名（`"warning"`）和别的、**该显示**的提醒（比如别的降级场景）共用同一个
/// 方法——不能按方法名整体拉黑，只能按内容筛这一条。
///
/// 实测撞到的原文（模型名会变，前后缀不变）：
/// "Model metadata for `gpt-5.6` not found. Defaulting to fallback metadata;
/// this can degrade performance and cause issues."
///
/// **筛掉它是因为它对共飞用户结构性地不可操作**：共飞自己的模型名（`gpt-5.6`
/// 这类，来自 `/api/v1/paw/config`）永远不会出现在 codex 自带的 OpenAI 型号表里
/// ——这不是配置漏了，是共飞每一个模型、每一轮都会撞见。原来"warning 必须显示"
/// 的设计初衷是"codex 主动想说给用户听的话"，但这条对着共飞用户念一百遍也没用，
/// 跟上面 `Ignored` 那几条"认识但用不上"是同一件事，只是判断依据是内容不是方法名。
///
/// 副作用（`context_window` 可能用的是通用兜底值，不是这个模型真实的窗口大小）
/// 没有被这条筛选处理——真要修，走 `-c model_context_window=<N>` 覆盖，是另一件事。
fn is_known_noise_warning(message: &str) -> bool {
    message.starts_with("Model metadata for") && message.contains("Defaulting to fallback metadata")
}

/// `error` 通知的投影。
#[derive(Debug, Clone, PartialEq)]
pub struct ErrorNotification {
    /// 给人看的一句话，例如 `Reconnecting... 1/5`。**它本身往往不是终态描述。**
    pub message: String,
    /// 上游 HTTP 状态码，从 `codexErrorInfo` 里挖出来的。
    ///
    /// 这是**结构化**信号，比匹配 message 文本可靠得多 —— 401 就是 401，
    /// 不会因为上游改了文案就失灵。
    pub http_status: Option<u16>,
    /// codex 还会自己重试。
    ///
    /// **为 true 时不要当致命错误弹给用户**：这只是「第 1/5 次重连」，
    /// 用户看到的应该是「正在重试」，不是「登录失效了」。
    pub will_retry: bool,
    pub thread_id: Option<String>,
    pub turn_id: Option<String>,
    /// 上游给的详细信息，通常带着真正的原因（如 `INVALID_API_KEY`）。
    pub details: Option<String>,
}

impl ErrorNotification {
    /// 这个错误是不是鉴权问题（该引导用户重新登录，而不是重试）。
    pub fn is_auth_failure(&self) -> bool {
        matches!(self.http_status, Some(401) | Some(403))
    }
}

/// 递归找出第一个 `httpStatusCode`。
///
/// `codexErrorInfo` 是个变体很多的联合体，且由上游拥有、会增删。我们只要状态码，
/// 所以不去镜像它的形状 —— 那样每次上游加一个变体我们都得跟着改。
fn find_http_status(v: &Value) -> Option<u16> {
    match v {
        Value::Object(map) => {
            if let Some(code) = map.get("httpStatusCode").and_then(Value::as_u64) {
                return u16::try_from(code).ok();
            }
            map.values().find_map(find_http_status)
        }
        Value::Array(items) => items.iter().find_map(find_http_status),
        _ => None,
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ReasoningKind {
    Text,
    SummaryText,
    SummaryPart,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ThreadStatus {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(default)]
    pub active_flags: Vec<String>,
}

impl ThreadStatus {
    pub fn is_waiting_on_approval(&self) -> bool {
        self.active_flags.iter().any(|f| f == "waitingOnApproval")
    }
}

// ---------------------------------------------------------------------------
// 投影
// ---------------------------------------------------------------------------

fn str_field(v: &Value, key: &str) -> Option<String> {
    v.get(key).and_then(Value::as_str).map(str::to_owned)
}

impl Notification {
    /// 把方法名 + 原始参数投影成我们的类型。**不认识的方法不是错误。**
    pub fn project(method: String, raw: Value) -> Self {
        let payload = match method.as_str() {
            "thread/started" => raw
                .get("thread")
                .and_then(|t| str_field(t, "id"))
                .map(|thread_id| NotificationPayload::ThreadStarted { thread_id })
                .unwrap_or(NotificationPayload::Other),
            "thread/status/changed" => {
                match (str_field(&raw, "threadId"), raw.get("status")) {
                    (Some(thread_id), Some(status)) => {
                        match serde_json::from_value::<ThreadStatus>(status.clone()) {
                            Ok(status) => NotificationPayload::ThreadStatusChanged { thread_id, status },
                            Err(_) => NotificationPayload::Other,
                        }
                    }
                    _ => NotificationPayload::Other,
                }
            }
            "turn/started" => match str_field(&raw, "threadId") {
                Some(thread_id) => NotificationPayload::TurnStarted {
                    thread_id,
                    turn_id: raw.get("turn").and_then(|t| str_field(t, "id")),
                },
                None => NotificationPayload::Other,
            },
            "turn/completed" => match str_field(&raw, "threadId") {
                Some(thread_id) => NotificationPayload::TurnCompleted {
                    thread_id,
                    turn_id: raw.get("turn").and_then(|t| str_field(t, "id")),
                    status: raw
                        .get("turn")
                        .and_then(|t| str_field(t, "status"))
                        .unwrap_or_else(|| "completed".to_owned()),
                },
                None => NotificationPayload::Other,
            },
            "item/started" | "item/completed" => {
                let item = raw.get("item");
                match (str_field(&raw, "threadId"), item.and_then(|i| str_field(i, "type"))) {
                    (Some(thread_id), Some(item_type)) => {
                        let item_id = item.and_then(|i| str_field(i, "id"));
                        // item 整个留着：审批 UI 要靠它显示「到底在批什么」。
                        let item = item.cloned().unwrap_or(Value::Null);
                        if method == "item/started" {
                            NotificationPayload::ItemStarted { thread_id, item_type, item_id, item }
                        } else {
                            NotificationPayload::ItemCompleted {
                                thread_id,
                                item_type,
                                item_id,
                                item_status: str_field(&item, "status"),
                                item,
                            }
                        }
                    }
                    _ => NotificationPayload::Other,
                }
            }
            "item/agentMessage/delta" => {
                match (
                    str_field(&raw, "threadId"),
                    str_field(&raw, "itemId"),
                    str_field(&raw, "delta"),
                ) {
                    (Some(thread_id), Some(item_id), Some(delta)) => {
                        NotificationPayload::AgentMessageDelta { thread_id, item_id, delta }
                    }
                    _ => NotificationPayload::Other,
                }
            }
            "item/reasoning/textDelta"
            | "item/reasoning/summaryTextDelta"
            | "item/reasoning/summaryPartAdded" => {
                let kind = match method.as_str() {
                    "item/reasoning/textDelta" => ReasoningKind::Text,
                    "item/reasoning/summaryTextDelta" => ReasoningKind::SummaryText,
                    _ => ReasoningKind::SummaryPart,
                };
                match str_field(&raw, "threadId") {
                    Some(thread_id) => NotificationPayload::ReasoningDelta {
                        thread_id,
                        item_id: str_field(&raw, "itemId"),
                        delta: str_field(&raw, "delta").unwrap_or_default(),
                        kind,
                    },
                    None => NotificationPayload::Other,
                }
            }
            "item/commandExecution/outputDelta" => match str_field(&raw, "threadId") {
                Some(thread_id) => NotificationPayload::CommandOutputDelta {
                    thread_id,
                    item_id: str_field(&raw, "itemId"),
                    chunk: str_field(&raw, "chunk")
                        .or_else(|| str_field(&raw, "delta"))
                        .unwrap_or_default(),
                },
                None => NotificationPayload::Other,
            },
            "turn/diff/updated" => match (
                str_field(&raw, "threadId"),
                str_field(&raw, "turnId"),
                str_field(&raw, "diff"),
            ) {
                (Some(thread_id), Some(turn_id), Some(diff)) => {
                    NotificationPayload::TurnDiffUpdated { thread_id, turn_id, diff }
                }
                _ => NotificationPayload::Other,
            },
            "item/fileChange/patchUpdated" => match (
                str_field(&raw, "threadId"),
                str_field(&raw, "turnId"),
                str_field(&raw, "itemId"),
                raw.get("changes"),
            ) {
                (Some(thread_id), Some(turn_id), Some(item_id), Some(changes)) => {
                    NotificationPayload::FileChangePatchUpdated {
                        thread_id,
                        turn_id,
                        item_id,
                        changes: changes.clone(),
                    }
                }
                _ => NotificationPayload::Other,
            },
            "item/fileChange/outputDelta" => match (
                str_field(&raw, "threadId"),
                str_field(&raw, "turnId"),
                str_field(&raw, "itemId"),
                str_field(&raw, "delta"),
            ) {
                (Some(thread_id), Some(turn_id), Some(item_id), Some(delta)) => {
                    NotificationPayload::FileChangeOutputDelta {
                        thread_id,
                        turn_id,
                        item_id,
                        delta,
                    }
                }
                _ => NotificationPayload::Other,
            },
            "item/plan/delta" => match (
                str_field(&raw, "threadId"),
                str_field(&raw, "turnId"),
                str_field(&raw, "itemId"),
                str_field(&raw, "delta"),
            ) {
                (Some(thread_id), Some(turn_id), Some(item_id), Some(delta)) => {
                    NotificationPayload::PlanDelta {
                        thread_id,
                        turn_id,
                        item_id,
                        delta,
                    }
                }
                _ => NotificationPayload::Other,
            },
            "turn/plan/updated" => match (
                str_field(&raw, "threadId"),
                str_field(&raw, "turnId"),
                raw.get("plan"),
            ) {
                (Some(thread_id), Some(turn_id), Some(plan)) => {
                    NotificationPayload::TurnPlanUpdated {
                        thread_id,
                        turn_id,
                        plan: plan.clone(),
                        explanation: str_field(&raw, "explanation"),
                    }
                }
                _ => NotificationPayload::Other,
            },
            "item/commandExecution/terminalInteraction" => match (
                str_field(&raw, "threadId"),
                str_field(&raw, "turnId"),
                str_field(&raw, "itemId"),
                str_field(&raw, "processId"),
                str_field(&raw, "stdin"),
            ) {
                (Some(thread_id), Some(turn_id), Some(item_id), Some(process_id), Some(stdin)) => {
                    NotificationPayload::TerminalInteraction {
                        thread_id,
                        turn_id,
                        item_id,
                        process_id,
                        stdin,
                    }
                }
                _ => NotificationPayload::Other,
            },
            "turn/moderationMetadata" => match (
                str_field(&raw, "threadId"),
                str_field(&raw, "turnId"),
                raw.get("metadata"),
            ) {
                (Some(thread_id), Some(turn_id), Some(metadata)) => {
                    NotificationPayload::TurnModerationMetadata {
                        thread_id,
                        turn_id,
                        metadata: metadata.clone(),
                    }
                }
                _ => NotificationPayload::Other,
            },
            "serverRequest/resolved" => {
                match (
                    str_field(&raw, "threadId"),
                    raw.get("requestId").and_then(|v| {
                        serde_json::from_value::<RequestId>(v.clone()).ok()
                    }),
                ) {
                    (Some(thread_id), Some(request_id)) => {
                        NotificationPayload::ServerRequestResolved { thread_id, request_id }
                    }
                    _ => NotificationPayload::Other,
                }
            }
            "error" => {
                // 注意层级：文案在 `params.error.message`，不在 `params.message`。
                let err = raw.get("error");
                NotificationPayload::Error(ErrorNotification {
                    message: err
                        .and_then(|e| str_field(e, "message"))
                        .or_else(|| str_field(&raw, "message"))
                        .unwrap_or_else(|| raw.to_string()),
                    http_status: err.and_then(find_http_status),
                    will_retry: raw.get("willRetry").and_then(Value::as_bool).unwrap_or(false),
                    thread_id: str_field(&raw, "threadId"),
                    turn_id: str_field(&raw, "turnId"),
                    details: err.and_then(|e| str_field(e, "additionalDetails")),
                })
            }
            "warning"
            | "configWarning"
            | "deprecationNotice"
            | "guardianWarning"
            | "windows/worldWritableWarning" => {
                let message = warning_message(&method, &raw);
                if is_known_noise_warning(&message) {
                    NotificationPayload::Ignored { method: method.clone() }
                } else {
                    NotificationPayload::Warning { thread_id: str_field(&raw, "threadId"), message }
                }
            }
            "thread/closed"
            | "thread/deleted"
            | "thread/unarchived"
            | "thread/compacted"
            | "thread/reverted"
            | "thread/settings/updated"
            | "thread/queue/changed"
            | "thread/name/updated"
            | "thread/project/updated"
            | "fuzzyFileSearch/sessionUpdated"
            | "fuzzyFileSearch/sessionCompleted"
            | "autoApprovalReview/strictReviewRequired"
            | "item/autoApprovalReview/started"
            | "item/autoApprovalReview/completed"
            | "mcpServer/oauthLogin/completed"
            | "windowsSandbox/setupCompleted" => NotificationPayload::Known {
                method: method.clone(),
                thread_id: str_field(&raw, "threadId"),
                raw: raw.clone(),
            },
            "thread/tokenUsage/updated"
            | "account/rateLimits/updated"
            | "remoteControl/status/changed"
            | "account/login/completed"
            | "account/updated"
            | "thread/archived"
            | "thread/realtime/started"
            | "thread/realtime/itemAdded"
            | "thread/realtime/item/started"
            | "thread/realtime/item/transcript/delta"
            | "thread/realtime/item/completed"
            | "thread/realtime/transcript/delta"
            | "thread/realtime/transcript/done"
            | "thread/realtime/outputAudio/delta"
            | "thread/realtime/sdp"
            | "thread/realtime/error"
            | "thread/realtime/closed"
            | "fs/changed"
            | "process/exited"
            | "process/outputDelta"
            | "command/exec/outputDelta"
            | "item/mcpToolCall/progress"
            | "mcpServer/startupStatus/updated"
            | "mcpServer/event/stream/notification"
            | "hook/started"
            | "hook/completed"
            | "skills/changed"
            | "app/list/updated"
            | "externalAgentConfig/import/progress"
            | "externalAgentConfig/import/completed"
            | "model/rerouted"
            | "model/safetyBuffering/updated"
            | "model/verification"
            | "modelProvider/authRecoveryCompleted"
            | "modelProvider/authRecoveryStarted"
            | "project/changed"
            | "thread/environment/connected"
            | "thread/environment/disconnected"
            | "thread/goal/cleared"
            | "thread/goal/updated" => {
                NotificationPayload::Ignored { method: method.clone() }
            }
            _ => NotificationPayload::Other,
        };

        Notification { method, raw, payload }
    }
}

fn warning_message(method: &str, raw: &Value) -> String {
    match method {
        "configWarning" => {
            let summary = str_field(raw, "summary").unwrap_or_else(|| "配置存在警告".to_owned());
            let details = str_field(raw, "details");
            let path = str_field(raw, "path");
            match (path, details) {
                (Some(path), Some(details)) => format!("{summary} ({path}): {details}"),
                (Some(path), None) => format!("{summary} ({path})"),
                (None, Some(details)) => format!("{summary}: {details}"),
                (None, None) => summary,
            }
        }
        "deprecationNotice" => {
            let summary = str_field(raw, "summary").unwrap_or_else(|| "上游功能已弃用".to_owned());
            str_field(raw, "details")
                .map(|details| format!("{summary}: {details}"))
                .unwrap_or(summary)
        }
        "guardianWarning" => str_field(raw, "message").unwrap_or_else(|| "安全守护提示".to_owned()),
        "windows/worldWritableWarning" => {
            let failed = raw.get("failedScan").and_then(Value::as_bool).unwrap_or(false);
            let count = raw.get("extraCount").and_then(Value::as_u64).unwrap_or(0);
            if failed {
                "Windows 工作目录权限扫描失败，请检查工作目录安全性".to_owned()
            } else if count > 0 {
                format!("Windows 工作目录发现 {count} 个可被其他用户写入的路径")
            } else {
                "Windows 工作目录权限检查发现异常".to_owned()
            }
        }
        _ => str_field(raw, "message").unwrap_or_default(),
    }
}

impl ServerRequest {
    /// 这条服务端请求属于哪条 thread——**尽力而为**，不是所有请求都带得出来。
    ///
    /// 三类已知审批的参数结构里都有 `thread_id`，直接取；`Other`（我们不认识的
    /// 服务端请求，比如 `attestation/generate`）就从 `raw` 里摸一下同名字段，
    /// 摸不到就是 `None`——多会话并发改造之后，这是把一条审批请求路由回正确
    /// 对话的唯一依据，宁可摸不到也不要编一个。
    pub fn thread_id(&self) -> Option<&str> {
        match &self.payload {
            ServerRequestPayload::CommandApproval(p) => Some(&p.thread_id),
            ServerRequestPayload::FileChangeApproval(p) => Some(&p.thread_id),
            ServerRequestPayload::PermissionsApproval(p) => Some(&p.thread_id),
            ServerRequestPayload::Other => self.raw.get("threadId").and_then(Value::as_str),
        }
    }

    pub fn project(id: RequestId, method: String, raw: Value) -> Self {
        let payload = match method.as_str() {
            "item/commandExecution/requestApproval" => {
                match serde_json::from_value::<CommandApprovalParams>(raw.clone()) {
                    Ok(p) => ServerRequestPayload::CommandApproval(Box::new(p)),
                    Err(_) => ServerRequestPayload::Other,
                }
            }
            "item/fileChange/requestApproval" => {
                match serde_json::from_value::<FileChangeApprovalParams>(raw.clone()) {
                    Ok(p) => ServerRequestPayload::FileChangeApproval(Box::new(p)),
                    Err(_) => ServerRequestPayload::Other,
                }
            }
            "item/permissions/requestApproval" => {
                match serde_json::from_value::<PermissionsApprovalParams>(raw.clone()) {
                    Ok(p) => ServerRequestPayload::PermissionsApproval(Box::new(p)),
                    Err(_) => ServerRequestPayload::Other,
                }
            }
            _ => ServerRequestPayload::Other,
        };

        ServerRequest { id, method, raw, payload }
    }

    /// 批准。
    ///
    /// # Errors
    /// 这个请求不是「是/否」型的（比如权限申请、工具调用）时返回错误 ——
    /// 那些得用各自专门的方法。
    pub fn approve(&self, decision: ApprovalDecision) -> Result<Answer, crate::error::AdapterError> {
        let word = match self.method.as_str() {
            "item/commandExecution/requestApproval" | "item/fileChange/requestApproval" => {
                decision.v2_word()
            }
            "execCommandApproval" | "applyPatchApproval" => decision.legacy_word(),
            other => {
                return Err(crate::error::AdapterError::Protocol(format!(
                    "{other} 不是是/否型的审批请求，不能用 approve() 答复"
                )))
            }
        };
        Ok(Answer {
            id: self.id.clone(),
            body: AnswerBody::Result(serde_json::json!({ "decision": word })),
        })
    }

    /// 授予权限（只对 `item/permissions/requestApproval`）。
    ///
    /// # Errors
    /// 用在别的请求上时返回错误。
    pub fn grant_permissions(
        &self,
        profile: Value,
        scope: GrantScope,
    ) -> Result<Answer, crate::error::AdapterError> {
        if self.method != "item/permissions/requestApproval" {
            return Err(crate::error::AdapterError::Protocol(format!(
                "{} 不是权限申请，不能用 grant_permissions() 答复",
                self.method
            )));
        }
        Ok(Answer {
            id: self.id.clone(),
            body: AnswerBody::Result(serde_json::json!({
                "permissions": profile,
                "scope": scope,
            })),
        })
    }

    /// 拒绝 —— 对**任何**服务端请求都给得出一个它自己形状的否定答复。
    ///
    /// 这个函数是全的（total），这一点很要紧：**任何一个服务端请求不答复，整轮就卡死**。
    /// 所以哪怕是我们完全不认识的方法，也必须有个说得出口的「不」。
    ///
    /// 各类的「不」长得完全不一样：
    ///
    /// | 方法 | 拒绝的形状 |
    /// |---|---|
    /// | `item/commandExecution/requestApproval`、`item/fileChange/requestApproval` | `{"decision":"decline"}` |
    /// | `execCommandApproval`、`applyPatchApproval`（旧） | `{"decision":"denied"}` —— **另一套词汇** |
    /// | `item/permissions/requestApproval` | `{"permissions":{},"scope":"turn"}` —— 这一类**没有「拒绝」这个值**，拒绝就是授一个空档案 |
    /// | `item/tool/call` | `{"success":false,"contentItems":[]}` |
    /// | `item/tool/requestUserInput` | `{"answers":[]}` |
    /// | `mcpServer/elicitation/request` | `{"action":"decline"}` |
    /// | `attestation/generate`、`account/chatgptAuthTokens/refresh` | **JSON-RPC 错误** —— 这两个要的是我们造不出来的凭证，只能老实说不行 |
    /// | 其它没见过的 | JSON-RPC 错误 |
    pub fn deny(&self) -> Answer {
        let body = match self.method.as_str() {
            "item/commandExecution/requestApproval" | "item/fileChange/requestApproval" => {
                AnswerBody::Result(serde_json::json!({ "decision": "decline" }))
            }
            "execCommandApproval" | "applyPatchApproval" => {
                AnswerBody::Result(serde_json::json!({ "decision": "denied" }))
            }
            "item/permissions/requestApproval" => AnswerBody::Result(serde_json::json!({
                "permissions": {},
                "scope": "turn",
            })),
            "item/tool/call" => {
                AnswerBody::Result(serde_json::json!({ "success": false, "contentItems": [] }))
            }
            "item/tool/requestUserInput" => AnswerBody::Result(serde_json::json!({ "answers": [] })),
            "mcpServer/elicitation/request" => {
                AnswerBody::Result(serde_json::json!({ "action": "decline" }))
            }
            other => AnswerBody::Error {
                // -32601 Method not found：我们这一侧没有实现这个方法。
                code: -32601,
                message: format!("共飞工作台不提供 {other}"),
            },
        };
        Answer { id: self.id.clone(), body }
    }
}

#[cfg(test)]
mod ignored_notification_tests {
    use super::*;

    /// 钉住具体是哪两个方法——这是实测撞出来的白名单，不是猜的。
    /// 撞见经过：0.153.0 每轮之后都推这两条，真实使用第一次就在界面上炸出一片
    /// "未识别的上游通知"；而 fixture 里其实早就录到了，只是没人拿它们比对过
    /// `Other`。见 `tests/replay.rs` 里 `every_notification_we_have_ever_recorded_has_a_known_home`。
    #[test]
    fn accounting_notifications_are_recognized_and_ignored_not_passthrough() {
        for method in [
            "thread/tokenUsage/updated",
            "account/rateLimits/updated",
            "remoteControl/status/changed",
            "account/login/completed",
            "account/updated",
            "thread/archived",
        ] {
            let note = Notification::project(method.to_owned(), serde_json::json!({}));
            assert!(
                matches!(&note.payload, NotificationPayload::Ignored { method: m } if m == method),
                "{method} 没有落进 Ignored：{:?}",
                note.payload
            );
        }
    }

    /// 模型元数据缺失的降级提示——共飞的模型名结构性地不会出现在 codex 自带的
    /// OpenAI 型号表里，这条会在**每一轮**都触发，对用户不可操作，筛掉。
    #[test]
    fn the_fallback_model_metadata_warning_is_filtered_out() {
        let note = Notification::project(
            "warning".to_owned(),
            serde_json::json!({
                "threadId": "t-1",
                "message": "Model metadata for `gpt-5.6` not found. Defaulting to fallback metadata; this can degrade performance and cause issues.",
            }),
        );
        assert!(
            matches!(&note.payload, NotificationPayload::Ignored { method } if method == "warning"),
            "没被筛掉：{:?}",
            note.payload
        );
    }

    /// 反过来验一次：别的 warning 内容必须照样显示——筛选是按内容，
    /// 不是把整个 "warning" 方法拉黑了。没有这条，上面那个测试可能因为
    /// 校验逻辑本身写错（比如误判了整个方法）而永远绿。
    #[test]
    fn other_warnings_still_surface() {
        let note = Notification::project(
            "warning".to_owned(),
            serde_json::json!({
                "threadId": "t-1",
                "message": "Something else codex wants the user to know.",
            }),
        );
        assert!(
            matches!(
                &note.payload,
                NotificationPayload::Warning { message, .. }
                    if message == "Something else codex wants the user to know."
            ),
            "被误筛了：{:?}",
            note.payload
        );
    }

    #[test]
    fn user_facing_server_notifications_are_projected() {
        let cases = [
            (
                "turn/diff/updated",
                serde_json::json!({
                    "threadId": "t-1",
                    "turnId": "turn-1",
                    "diff": "diff --git a/a.txt b/a.txt",
                }),
            ),
            (
                "item/fileChange/patchUpdated",
                serde_json::json!({
                    "threadId": "t-1",
                    "turnId": "turn-1",
                    "itemId": "item-1",
                    "changes": [],
                }),
            ),
            (
                "item/plan/delta",
                serde_json::json!({
                    "threadId": "t-1",
                    "turnId": "turn-1",
                    "itemId": "item-1",
                    "delta": "Inspect the repository",
                }),
            ),
            (
                "turn/plan/updated",
                serde_json::json!({
                    "threadId": "t-1",
                    "turnId": "turn-1",
                    "plan": [],
                }),
            ),
            (
                "item/commandExecution/terminalInteraction",
                serde_json::json!({
                    "threadId": "t-1",
                    "turnId": "turn-1",
                    "itemId": "item-1",
                    "processId": "process-1",
                    "stdin": "y\n",
                }),
            ),
            (
                "turn/moderationMetadata",
                serde_json::json!({
                    "threadId": "t-1",
                    "turnId": "turn-1",
                    "metadata": {"reviewed": true},
                }),
            ),
        ];

        for (method, raw) in cases {
            assert!(
                !matches!(
                    Notification::project(method.to_owned(), raw).payload,
                    NotificationPayload::Other
                ),
                "{method} should have a user-facing projection",
            );
        }
    }

    #[test]
    fn codex_warnings_are_normalized_to_warning_events() {
        let cases = [
            (
                "configWarning",
                serde_json::json!({"summary": "Invalid config", "path": "config.toml"}),
                "Invalid config (config.toml)",
            ),
            (
                "deprecationNotice",
                serde_json::json!({"summary": "Old option", "details": "Use the new option"}),
                "Old option: Use the new option",
            ),
            (
                "guardianWarning",
                serde_json::json!({"threadId": "t-1", "message": "Review this action"}),
                "Review this action",
            ),
        ];

        for (method, raw, expected) in cases {
            assert!(matches!(
                Notification::project(method.to_owned(), raw).payload,
                NotificationPayload::Warning { message, .. } if message == expected
            ));
        }
    }

    #[test]
    fn unsupported_server_notifications_are_explicitly_ignored() {
        let methods = [
            "thread/realtime/started",
            "thread/realtime/itemAdded",
            "thread/realtime/item/started",
            "thread/realtime/item/transcript/delta",
            "thread/realtime/item/completed",
            "thread/realtime/transcript/delta",
            "thread/realtime/transcript/done",
            "thread/realtime/outputAudio/delta",
            "thread/realtime/sdp",
            "thread/realtime/error",
            "thread/realtime/closed",
            "fs/changed",
            "process/exited",
            "process/outputDelta",
            "command/exec/outputDelta",
            "item/mcpToolCall/progress",
            "mcpServer/startupStatus/updated",
            "mcpServer/event/stream/notification",
            "hook/started",
            "hook/completed",
            "skills/changed",
            "app/list/updated",
            "externalAgentConfig/import/progress",
            "externalAgentConfig/import/completed",
            "model/rerouted",
            "model/safetyBuffering/updated",
            "model/verification",
            "modelProvider/authRecoveryCompleted",
            "modelProvider/authRecoveryStarted",
            "project/changed",
            "thread/environment/connected",
            "thread/environment/disconnected",
            "thread/goal/cleared",
            "thread/goal/updated",
        ];

        for method in methods {
            let note = Notification::project(method.to_owned(), serde_json::json!({}));
            assert!(
                matches!(note.payload, NotificationPayload::Ignored { method: actual } if actual == method),
                "{method} should be explicitly ignored",
            );
        }
    }

    #[test]
    fn known_notifications_are_not_reported_as_protocol_drift() {
        let methods = [
            "thread/closed",
            "thread/deleted",
            "thread/unarchived",
            "thread/compacted",
            "thread/reverted",
            "thread/settings/updated",
            "thread/queue/changed",
            "thread/name/updated",
            "thread/project/updated",
            "fuzzyFileSearch/sessionUpdated",
            "fuzzyFileSearch/sessionCompleted",
            "autoApprovalReview/strictReviewRequired",
            "item/autoApprovalReview/started",
            "item/autoApprovalReview/completed",
            "mcpServer/oauthLogin/completed",
            "windowsSandbox/setupCompleted",
        ];

        for method in methods {
            let note = Notification::project(
                method.to_owned(),
                serde_json::json!({ "threadId": "thread-1" }),
            );
            assert!(
                matches!(
                    note.payload,
                    NotificationPayload::Known { method: actual, .. } if actual == method
                ),
                "{method} should have a known notification projection",
            );
        }
    }
}
