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
//! 类型形状取自 `protocol/0.144.2/` 下由 `codex app-server generate-json-schema`
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
    /// 发一轮提问。可以按轮覆盖 model / effort / sandbox / cwd。
    TurnStart { thread_id: String, text: String },
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
/// 只有这三个取值 —— 0.144.2 里**没有** `on-failure`（老版本有过，别照旧文档写）。
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
            ClientRequest::TurnStart { thread_id, text } => serde_json::json!({
                "threadId": thread_id,
                "input": [{ "type": "text", "text": text, "text_elements": [] }],
            }),
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
    CommandApproval(CommandApprovalParams),
    FileChangeApproval(FileChangeApprovalParams),
    /// 权限申请。响应形状和另外两个**完全不同** —— 没有 `decision` 字段。
    PermissionsApproval(Value),
    /// 我们没投影的服务端请求。上层仍然要答复它（一般是拒绝），否则 turn 卡死。
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
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct FileChangeApprovalParams {
    pub thread_id: String,
    pub turn_id: String,
    pub item_id: String,
    #[serde(default)]
    pub reason: Option<String>,
    #[serde(default)]
    pub available_decisions: Vec<Value>,
}

/// 审批决定。命令执行与文件改动共用这套；权限申请是另一套形状。
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
    /// 拼成响应体。命令执行与文件改动都是 `{"decision": ...}`。
    pub fn to_response(self) -> Value {
        serde_json::json!({ "decision": self })
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
    ItemStarted { thread_id: String, item_type: String, item_id: Option<String> },
    ItemCompleted { thread_id: String, item_type: String, item_id: Option<String> },
    /// assistant 正文的增量。
    AgentMessageDelta { thread_id: String, item_id: String, delta: String },
    /// 思考过程的增量（正文 / 摘要 / 摘要分段）。
    ReasoningDelta { thread_id: String, item_id: Option<String>, delta: String, kind: ReasoningKind },
    /// 命令执行的输出增量。
    CommandOutputDelta { thread_id: String, item_id: Option<String>, chunk: String },
    /// 某个服务端请求已经被（可能是另一端）答复了 —— 用来做「一处响应、另一处队列消失」。
    ServerRequestResolved { thread_id: String, request_id: RequestId },
    /// 服务端推来的错误通知。
    ///
    /// **模型侧的失败几乎全走这里，而不是 JSON-RPC error 响应。** 实测：用一个错误的
    /// API key 发一轮，`turn/start` 正常返回成功，401 是以一串 `error` 通知的形式到达的。
    /// 所以只盯着请求响应做错误处理，会把上游错误整类漏掉。
    Error(ErrorNotification),
    Other,
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
                        if method == "item/started" {
                            NotificationPayload::ItemStarted { thread_id, item_type, item_id }
                        } else {
                            NotificationPayload::ItemCompleted { thread_id, item_type, item_id }
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
            _ => NotificationPayload::Other,
        };

        Notification { method, raw, payload }
    }
}

impl ServerRequest {
    pub fn project(id: RequestId, method: String, raw: Value) -> Self {
        let payload = match method.as_str() {
            "item/commandExecution/requestApproval" => {
                match serde_json::from_value::<CommandApprovalParams>(raw.clone()) {
                    Ok(p) => ServerRequestPayload::CommandApproval(p),
                    Err(_) => ServerRequestPayload::Other,
                }
            }
            "item/fileChange/requestApproval" => {
                match serde_json::from_value::<FileChangeApprovalParams>(raw.clone()) {
                    Ok(p) => ServerRequestPayload::FileChangeApproval(p),
                    Err(_) => ServerRequestPayload::Other,
                }
            }
            "item/permissions/requestApproval" => {
                ServerRequestPayload::PermissionsApproval(raw.clone())
            }
            _ => ServerRequestPayload::Other,
        };

        ServerRequest { id, method, raw, payload }
    }
}
