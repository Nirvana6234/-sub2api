//! 桥：把 [`codex_host`] 的进程与 [`codex_adapter`] 的会话，接到前端。
//!
//! 桥本身**不认识 Tauri** —— 事件通过 [`EventSink`] 出去，Tauri 那一层只是实现了这个
//! trait 的一个薄壳。这样集成测试能直接驱动整座桥，不用绕 invoke 机制。

pub mod dto;

use std::collections::HashMap;
use std::sync::Arc;

use codex_adapter::protocol::{ServerRequest, ServerRequestPayload};
use codex_adapter::session::{Session, SessionEvent};
use codex_adapter::{
    ApprovalDecision, ApprovalPolicy, ErrorNotification, ReasoningKind, RequestId, SandboxMode,
    WorkspaceDir,
};
use codex_host::{Backoff, CodexHome, Engine, EngineConfig, LocalRelay, Supervisor};
use tokio::sync::Mutex;

use dto::{ApprovalKind, ApprovalRequest, StartParams, StartedThread, UiDecision, UiEvent};

/// 事件出口。Tauri 那层实现它，测试自己实现一个收集器。
pub trait EventSink: Send + Sync + 'static {
    fn emit(&self, event: UiEvent);
}

/// 桥这一层会出的错。所有变体都能序列化给前端。
#[derive(Debug, thiserror::Error, serde::Serialize)]
#[serde(tag = "kind", content = "message", rename_all = "camelCase")]
pub enum BridgeError {
    /// 还没起会话就来发消息 / 停止 / 答复。
    #[error("还没有正在运行的 agent 会话")]
    NotRunning,
    /// 已经有一个在跑了。
    #[error("已经有一个 agent 会话在运行")]
    AlreadyRunning,
    /// 参数不对（沙箱名、审批策略名、目录……）。
    #[error("参数不合法: {0}")]
    BadParams(String),
    /// 宿主侧（进程、笼子、CODEX_HOME）。
    #[error("宿主: {0}")]
    Host(String),
    /// 协议 / 会话侧。
    #[error("会话: {0}")]
    Session(String),
    /// 这条审批已经答过了，或者从来不存在。
    ///
    /// **刻意报错而不是静默成功**：界面重渲染导致的重复答复必须能看见，
    /// 否则会变成「同一条审批被答了两次」这种查不出来的问题。
    #[error("找不到待答复的审批 {0}（可能已经答过，或引擎重启了）")]
    NoSuchApproval(String),
}

/// 待答复的审批表。**键必须是完整的 [`RequestId`]**（`Num` 和 `Str` 是两种东西），
/// 答复之后立刻移除。
type Pending = Arc<Mutex<HashMap<RequestId, ServerRequest>>>;

struct Running {
    session: Arc<Session>,
    /// 只是持有：笼子在它身上，丢掉就等于杀掉 codex。
    _engine: Engine,
    pending: Pending,
    /// 会话结束时要把凭据从这里抹掉。
    home: CodexHome,
    /// 本地转发层。**只是持有** —— 丢掉它就等于关掉那个回环端口，
    /// codex 从此打不出去任何请求。
    relay: LocalRelay,
}

/// codex 二进制和程序数据目录在哪。
///
/// **这两条刻意不由前端传。** A9 把「凭据只落在自己程序目录下」做成了结构性保证
/// （`CodexHome::under_app_dir`：位置由程序推出来），可只要 `StartParams` 还带着
/// `appDir`，那个保证就离一次 `invoke` 只有一步之遥 —— 网页那侧能指到哪里，
/// 凭据就能落到哪里。`codexBinary` 同理：随包发之后，让渲染进程点名一个任意 exe
/// 没有任何好处，那正是「agent 跑渲染进程说什么就跑什么」的形状。
///
/// 于是两条都在 Rust 这侧定：数据目录来自 Tauri 的 `app_data_dir()`，
/// 二进制来自随包资源。
#[derive(Debug, Clone)]
pub struct AgentPaths {
    pub app_dir: std::path::PathBuf,
    pub codex_binary: std::path::PathBuf,
}

/// 一座桥。整个应用一个。
pub struct AgentBridge {
    running: Mutex<Option<Running>>,
    sink: Arc<dyn EventSink>,
    paths: AgentPaths,
    /// 当前账号会话。**前端是唯一持有者**，这里只是转交给转发层。
    ///
    /// 刻意不在 Rust 里实现刷新：刷新逻辑已经在前端 `api.ts` 里了，
    /// 复制一份到这边，两份迟早会对不上（而且对不上的表现是「偶尔要重新登录」）。
    session_token: Mutex<Option<String>>,
}

impl AgentBridge {
    pub fn new(sink: Arc<dyn EventSink>, paths: AgentPaths) -> Self {
        AgentBridge {
            running: Mutex::new(None),
            sink,
            paths,
            session_token: Mutex::new(None),
        }
    }

    /// 前端在登录/刷新之后把当前账号会话推下来。传 `None` 表示已登出。
    ///
    /// 会话正跑着也能推 —— 转发层下一条请求就用新的，不必重启 codex。
    pub async fn set_session_token(&self, jwt: Option<String>) {
        *self.session_token.lock().await = jwt.clone();
        if let Some(running) = self.running.lock().await.as_ref() {
            running.relay.set_session_token(jwt).await;
        }
    }

    /// 起引擎 + 起会话。返回 threadId。
    pub async fn start(&self, params: StartParams) -> Result<StartedThread, BridgeError> {
        let mut slot = self.running.lock().await;
        if slot.is_some() {
            return Err(BridgeError::AlreadyRunning);
        }

        let sandbox = parse_sandbox(&params.sandbox)?;
        let approval = parse_approval(&params.approval_policy)?;
        let cwd = WorkspaceDir::new(&params.cwd)
            .map_err(|e| BridgeError::BadParams(e.to_string()))?;
        let home = CodexHome::under_app_dir(&self.paths.app_dir)
            .map_err(|e| BridgeError::Host(e.to_string()))?;

        // 先把转发层起起来，再让 codex 指向它。**codex 只看得见这个回环地址**，
        // 中转站地址和账号会话都留在这一侧。
        let relay = LocalRelay::start(&params.relay_base_url, &params.client_user_agent)
            .await
            .map_err(|e| BridgeError::Host(e.to_string()))?;
        {
            let mut held = self.session_token.lock().await;
            if held.is_none() && !params.session_token.is_empty() {
                *held = Some(params.session_token.clone());
            }
            relay.set_session_token(held.clone()).await;
        }

        // 二进制是哪一种按文件名判断（官方 codex.exe 要带 app-server 子命令，
        // 精简的 codex-app-server.exe 不带）。判断错只会让进程起不来，不会跑错地方。
        let binary = self.paths.codex_binary.clone();
        let config = EngineConfig {
            kind: codex_host::CodexBinaryKind::infer(&binary),
            binary,
            home: home.clone(),
            base_url: relay.base_url(),
            model: params.model,
        };

        let sink = Arc::clone(&self.sink);
        let restarted = Supervisor::new(config, Backoff::default())
            .start(|failed| {
                // 起不来的每一次都要看得见。悄悄重试到最后才报错，
                // 中间那段时间用户只会觉得「卡住了」。
                sink.emit(UiEvent::Retrying {
                    message: format!("第 {} 次启动失败: {}", failed.attempt, failed.error),
                    http_status: None,
                    auth_failure: false,
                });
            })
            .await
            .map_err(|e| BridgeError::Host(e.to_string()))?;

        let mut engine = restarted.engine;
        let attempts = restarted.attempts;

        let (stdout, stdin) = engine.take_stdio().map_err(|e| BridgeError::Host(e.to_string()))?;
        let (session, events) = Session::connect(stdout, stdin);
        let session = Arc::new(session);

        // 交给 codex 的"API key"是转发层那个本地令牌。它照样会被 codex 写进
        // auth.json，但离开这台机器就一文不值 —— 真正的账号凭据从没进过它的地址空间。
        session
            .handshake("cofly-workbench", env!("CARGO_PKG_VERSION"), relay.token())
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))?;

        let thread_id = session
            .start_thread(cwd, sandbox, approval)
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))?;

        // 登记这条 thread 的分组。**必须在第一轮之前** —— 转发层认不出的 thread
        // 会被直接拒掉（宁可报错也不拿别的分组顶上）。
        relay.bind_thread(&thread_id, params.group_id).await;

        let pending: Pending = Arc::new(Mutex::new(HashMap::new()));
        spawn_pump(events, Arc::clone(&self.sink), Arc::clone(&pending));

        *slot = Some(Running {
            session,
            _engine: engine,
            pending,
            home,
            relay,
        });

        Ok(StartedThread { thread_id, attempts })
    }

    /// 发一轮提问。
    pub async fn send(&self, text: String) -> Result<(), BridgeError> {
        let slot = self.running.lock().await;
        let running = slot.as_ref().ok_or(BridgeError::NotRunning)?;
        running
            .session
            .send_turn(&text)
            .await
            .map(|_| ())
            .map_err(|e| BridgeError::Session(e.to_string()))
    }

    /// 停止当前这一轮。
    ///
    /// 没有正在跑的轮次时会**报错**，不静默成功 —— 「点了停止却还在跑」是最难查的一类问题。
    pub async fn interrupt(&self) -> Result<(), BridgeError> {
        let slot = self.running.lock().await;
        let running = slot.as_ref().ok_or(BridgeError::NotRunning)?;
        running
            .session
            .interrupt()
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))
    }

    /// 答复一条审批。
    ///
    /// 前端只给**语义**决定，线上说哪个词由这里照着那条请求决定 ——
    /// 三类审批的响应形状互不相同，旧方法连决定词汇都是另一套。
    /// 这个映射只存在于这一个地方。
    pub async fn answer(
        &self,
        request_id: String,
        decision: UiDecision,
    ) -> Result<(), BridgeError> {
        let slot = self.running.lock().await;
        let running = slot.as_ref().ok_or(BridgeError::NotRunning)?;

        let key = parse_request_id(&request_id);
        // 取出并**移除** —— 重复答复要能报错，不能悄悄答第二次。
        let request = running
            .pending
            .lock()
            .await
            .remove(&key)
            .ok_or_else(|| BridgeError::NoSuchApproval(request_id.clone()))?;

        let answer = build_answer(&request, decision).map_err(BridgeError::Session)?;
        running
            .session
            .answer(&answer)
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))
    }

    /// 有序停止：关 stdin 让 codex 自己收摊，超时再动手。
    pub async fn stop(&self) -> Result<(), BridgeError> {
        let mut slot = self.running.lock().await;
        let Some(running) = slot.take() else {
            return Err(BridgeError::NotRunning);
        };
        // 待答复的审批随引擎一起作废：那些 id 在新进程里不存在，
        // 界面上还挂着的话，用户点下去只会拿到 NoSuchApproval。
        running.pending.lock().await.clear();

        let shutdown = running
            ._engine
            .shutdown(std::time::Duration::from_secs(5))
            .await;

        // 会话结束 = 凭据不该再留在盘上。**先停进程再抹**，否则 codex 可能又写一遍。
        // 抹不掉要报错，但不能因此吞掉停止过程本身的失败。
        let wiped = running.home.wipe_credentials();

        shutdown.map_err(|e| BridgeError::Host(e.to_string()))?;
        wiped.map(|_| ()).map_err(|e| BridgeError::Host(e.to_string()))
    }

    pub async fn is_running(&self) -> bool {
        self.running.lock().await.is_some()
    }
}

/// 把会话事件翻成前端事件，顺便维护两张表：待答复审批、itemId → item。
fn spawn_pump(
    mut events: tokio::sync::mpsc::Receiver<SessionEvent>,
    sink: Arc<dyn EventSink>,
    pending: Pending,
) {
    tokio::spawn(async move {
        // itemId → item：审批请求只给 id，内容要靠这张表补上。
        let mut items: HashMap<String, serde_json::Value> = HashMap::new();
        let mut current_turn: Option<String> = None;

        while let Some(event) = events.recv().await {
            match event {
                SessionEvent::ThreadStarted { thread_id } => {
                    sink.emit(UiEvent::ThreadStarted { thread_id });
                }
                SessionEvent::TurnStarted { turn_id, .. } => {
                    current_turn = turn_id.clone();
                    sink.emit(UiEvent::TurnStarted { turn_id });
                }
                SessionEvent::AgentText { item_id, delta } => {
                    sink.emit(UiEvent::AgentText {
                        turn_id: current_turn.clone(),
                        item_id,
                        delta,
                    });
                }
                SessionEvent::Reasoning { delta, kind } => {
                    sink.emit(UiEvent::Reasoning {
                        turn_id: current_turn.clone(),
                        delta,
                        kind: match kind {
                            ReasoningKind::Text => "text",
                            ReasoningKind::SummaryText => "summaryText",
                            ReasoningKind::SummaryPart => "summaryPart",
                        }
                        .to_owned(),
                    });
                }
                SessionEvent::CommandOutput { item_id, chunk } => {
                    sink.emit(UiEvent::CommandOutput {
                        turn_id: current_turn.clone(),
                        item_id,
                        chunk,
                    });
                }
                SessionEvent::ItemStarted { item_id, item_type, item } => {
                    if let Some(id) = &item_id {
                        items.insert(id.clone(), item.clone());
                    }
                    sink.emit(UiEvent::Item {
                        item_id,
                        item_type,
                        status: None,
                        item,
                    });
                }
                SessionEvent::ItemCompleted { item_id, item_type, item_status, item } => {
                    if let Some(id) = &item_id {
                        items.insert(id.clone(), item.clone());
                    }
                    sink.emit(UiEvent::Item {
                        item_id,
                        item_type,
                        status: item_status,
                        item,
                    });
                }
                SessionEvent::StatusChanged { status, .. } => {
                    sink.emit(UiEvent::Status {
                        waiting_on_approval: status.is_waiting_on_approval(),
                        flags: status.active_flags.clone(),
                    });
                }
                SessionEvent::ApprovalRequested(request) => {
                    let ui = to_ui_approval(&request, &items);
                    pending.lock().await.insert(request.id.clone(), *request);
                    sink.emit(UiEvent::ApprovalRequested(ui));
                }
                SessionEvent::ApprovalResolved { request_id } => {
                    // 另一端答了 —— 本地队列里那条也该消失。
                    pending.lock().await.remove(&request_id);
                    sink.emit(UiEvent::ApprovalResolved {
                        request_id: request_id.to_string(),
                    });
                }
                SessionEvent::Retrying(err) => sink.emit(error_event(&err, true)),
                SessionEvent::Failed(err) => sink.emit(error_event(&err, false)),
                SessionEvent::TurnCompleted { turn_id, status } => {
                    current_turn = None;
                    sink.emit(UiEvent::TurnCompleted {
                        turn_id,
                        success: status == "completed",
                        interrupted: status == "interrupted",
                        status,
                    });
                }
                SessionEvent::Passthrough { method, raw } => {
                    sink.emit(UiEvent::Passthrough { method, raw });
                }
                SessionEvent::DecodeError { line, error } => {
                    sink.emit(UiEvent::DecodeError { line, error });
                }
            }
        }

        // 流断了 = 引擎没了。让界面知道，别停在一个永远转圈的状态上。
        pending.lock().await.clear();
        sink.emit(UiEvent::EngineStopped {
            reason: "事件流已结束".to_owned(),
        });
    });
}

fn error_event(err: &ErrorNotification, retrying: bool) -> UiEvent {
    let message = match &err.details {
        Some(d) => format!("{}（{d}）", err.message),
        None => err.message.clone(),
    };
    if retrying {
        UiEvent::Retrying {
            message,
            http_status: err.http_status,
            auth_failure: err.is_auth_failure(),
        }
    } else {
        UiEvent::Failed {
            message,
            http_status: err.http_status,
            auth_failure: err.is_auth_failure(),
        }
    }
}

/// 把一条服务端请求翻成界面能显示的东西 —— **把 item 补上**。
fn to_ui_approval(
    request: &ServerRequest,
    items: &HashMap<String, serde_json::Value>,
) -> ApprovalRequest {
    let (kind, reason, command, cwd, item_id, grant_root) = match &request.payload {
        ServerRequestPayload::CommandApproval(p) => (
            ApprovalKind::Command,
            p.reason.clone(),
            Some(p.command.clone()),
            Some(p.cwd.clone()),
            Some(p.item_id.clone()),
            None,
        ),
        ServerRequestPayload::FileChangeApproval(p) => (
            ApprovalKind::FileChange,
            p.reason.clone(),
            None,
            None,
            Some(p.item_id.clone()),
            p.grant_root.clone(),
        ),
        ServerRequestPayload::PermissionsApproval(p) => (
            ApprovalKind::Permissions,
            p.reason.clone(),
            None,
            Some(p.cwd.clone()),
            Some(p.item_id.clone()),
            None,
        ),
        ServerRequestPayload::Other => (ApprovalKind::Unknown, None, None, None, None, None),
    };

    ApprovalRequest {
        request_id: request.id.to_string(),
        method: request.method.clone(),
        kind,
        reason,
        command,
        cwd,
        item: item_id.and_then(|id| items.get(&id).cloned()),
        grant_root,
    }
}

/// 语义决定 → 那条请求自己形状的答复。
fn build_answer(
    request: &ServerRequest,
    decision: UiDecision,
) -> Result<codex_adapter::Answer, String> {
    match request.method.as_str() {
        // 权限申请没有「拒绝」这个值：拒绝＝授空档案，同意＝把它要的原样授出去。
        "item/permissions/requestApproval" => match decision {
            UiDecision::Approve | UiDecision::ApproveForSession => {
                let profile = request
                    .raw
                    .get("permissions")
                    .cloned()
                    .unwrap_or(serde_json::Value::Object(Default::default()));
                let scope = if matches!(decision, UiDecision::ApproveForSession) {
                    codex_adapter::GrantScope::Session
                } else {
                    codex_adapter::GrantScope::Turn
                };
                request.grant_permissions(profile, scope).map_err(|e| e.to_string())
            }
            UiDecision::Decline | UiDecision::Cancel => Ok(request.deny()),
        },
        _ => match decision {
            UiDecision::Approve => request.approve(ApprovalDecision::Accept),
            UiDecision::ApproveForSession => request.approve(ApprovalDecision::AcceptForSession),
            UiDecision::Decline => request.approve(ApprovalDecision::Decline),
            UiDecision::Cancel => request.approve(ApprovalDecision::Cancel),
        }
        // 不是是/否型的请求（工具调用之类）：同意没有意义，一律给它自己形状的「不」。
        .or_else(|_| Ok(request.deny()))
        .map_err(|e: codex_adapter::AdapterError| e.to_string()),
    }
}

/// 前端传回来的 id 是字符串。原来是数字的要还原成数字 ——
/// `RequestId::Num(0)` 和 `RequestId::Str("0")` 在表里是**两个键**。
fn parse_request_id(raw: &str) -> RequestId {
    match raw.parse::<i64>() {
        Ok(n) => RequestId::Num(n),
        Err(_) => RequestId::Str(raw.to_owned()),
    }
}

fn parse_sandbox(value: &str) -> Result<SandboxMode, BridgeError> {
    match value {
        "read-only" => Ok(SandboxMode::ReadOnly),
        "workspace-write" => Ok(SandboxMode::WorkspaceWrite),
        "danger-full-access" => Ok(SandboxMode::DangerFullAccess),
        other => Err(BridgeError::BadParams(format!("不认识的沙箱模式: {other}"))),
    }
}

fn parse_approval(value: &str) -> Result<ApprovalPolicy, BridgeError> {
    match value {
        "untrusted" => Ok(ApprovalPolicy::Untrusted),
        "on-request" => Ok(ApprovalPolicy::OnRequest),
        "never" => Ok(ApprovalPolicy::Never),
        other => Err(BridgeError::BadParams(format!("不认识的审批策略: {other}"))),
    }
}
