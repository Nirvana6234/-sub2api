//! 桥：把 [`codex_host`] 的进程与 [`codex_adapter`] 的会话，接到前端。
//!
//! 桥本身**不认识 Tauri** —— 事件通过 [`EventSink`] 出去，Tauri 那一层只是实现了这个
//! trait 的一个薄壳。这样集成测试能直接驱动整座桥，不用绕 invoke 机制。
//!
//! # 多会话并发改造：一个引擎，多条 thread
//!
//! 这座桥曾经"一次只服务一个会话"：`running: Mutex<Option<Running>>` 是单槽位，
//! 起第二条会话之前必须先停掉第一条。那条限制**不是 codex 的限制，是这座桥自己的
//! 简化**——探针实测（`codex-host/scripts/probe-multi-thread.py`，真二进制）证明
//! 一个 codex 进程能真的**并发**跑两条 thread 的 turn：两次 `turn/start` 同时发出去，
//! 两边出站请求同一毫秒开始、同一毫秒结束，不是排队串行。
//!
//! 所以现在 `engine: Mutex<Option<SharedEngine>>` 装的是**一个共享的引擎**（进程 +
//! 转发层 + 一条 JSON-RPC 连接），懒起、之后复用；每次 [`AgentBridge::start`] 都是
//! 在这个共享引擎上**再开一条 thread**，不是再起一个进程。代价换回来的好处很直接：
//! 一个 218MB 的进程服务 N 个对话，而不是 N 个。
//!
//! 引擎级操作（[`AgentBridge::stop`]）和对话级操作（[`AgentBridge::end_thread`]）
//! 因此彻底分开：前者杀进程、抹凭据，**影响所有对话**；后者只归档一条 thread，
//! 引擎和其它对话原封不动。混着用的后果是"切一下审批模式，结果把邻座对话的引擎也拔了"。

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

use dto::{ApprovalKind, ApprovalRequest, SendParams, StartParams, StartedThread, UiDecision, UiEvent};

/// 事件出口。Tauri 那层实现它，测试自己实现一个收集器。
pub trait EventSink: Send + Sync + 'static {
    fn emit(&self, event: UiEvent);
}

/// 桥这一层会出的错。所有变体都能序列化给前端。
#[derive(Debug, thiserror::Error, serde::Serialize)]
#[serde(tag = "kind", content = "message", rename_all = "camelCase")]
pub enum BridgeError {
    /// 还没起引擎就来发消息 / 停止 / 答复 / 打断。
    #[error("还没有正在运行的 agent 引擎")]
    NotRunning,
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
/// 答复之后立刻移除。**整个引擎共享一张表**——`RequestId` 本身在一条 JSON-RPC
/// 连接上就是唯一的，不需要再按 thread 分表；`ServerRequest::thread_id()` 已经
/// 让答复路径按需回查得到归属。
type Pending = Arc<Mutex<HashMap<RequestId, ServerRequest>>>;

/// 一个正在跑的共享引擎：进程 + 会话 + 转发层。**整个应用同一时刻只有一份**，
/// 被 [`AgentBridge`] 的所有对话共用。
struct SharedEngine {
    session: Arc<Session>,
    /// 只是持有：笼子在它身上，丢掉就等于杀掉 codex。
    _engine: Engine,
    pending: Pending,
    /// 引擎停止时要把凭据从这里抹掉——**这是全局操作**，不是某条 thread 的事。
    home: CodexHome,
    /// 本地转发层。**只是持有** —— 丢掉它就等于关掉那个回环端口，
    /// codex 从此打不出去任何请求。
    relay: LocalRelay,
}

struct Spawned {
    engine: SharedEngine,
    attempts: u32,
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
    engine: Mutex<Option<SharedEngine>>,
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
            engine: Mutex::new(None),
            sink,
            paths,
            session_token: Mutex::new(None),
        }
    }

    /// 前端在登录/刷新之后把当前账号会话推下来。传 `None` 表示已登出。
    ///
    /// 引擎正跑着也能推 —— 转发层下一条请求就用新的，不必重启 codex。
    pub async fn set_session_token(&self, jwt: Option<String>) {
        *self.session_token.lock().await = jwt.clone();
        if let Some(engine) = self.engine.lock().await.as_ref() {
            engine.relay.set_session_token(jwt).await;
        }
    }

    /// 起一条新 thread。**引擎懒起、之后复用**：第一次调用会真的 spawn 一个
    /// codex 进程；后面的调用只是在同一个进程上再开一条 thread，不再排队等旧的先停。
    pub async fn start(&self, params: StartParams) -> Result<StartedThread, BridgeError> {
        // 参数校验必须在拿锁、在可能 spawn 进程**之前**——不管引擎是不是已经在跑，
        // 参数不对就该当场失败，不能先起了进程再发现白起。
        let sandbox = parse_sandbox(&params.sandbox)?;
        let approval = parse_approval(&params.approval_policy)?;
        let cwd = WorkspaceDir::new(&params.cwd).map_err(|e| BridgeError::BadParams(e.to_string()))?;

        let mut slot = self.engine.lock().await;
        let attempts = if slot.is_some() {
            1
        } else {
            let spawned = self.spawn_engine(&params).await?;
            *slot = Some(spawned.engine);
            spawned.attempts
        };
        let engine = slot.as_ref().expect("刚确保过引擎存在");

        let thread_id = engine
            .session
            .start_thread(cwd, sandbox, approval)
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))?;

        // 登记这条 thread 的分组。**必须在第一轮之前** —— 转发层认不出的 thread
        // 会被直接拒掉（宁可报错也不拿别的分组顶上）。
        engine.relay.bind_thread(&thread_id, params.group_id).await;

        Ok(StartedThread { thread_id, attempts })
    }

    /// 真正 spawn 一个 codex 进程 + 起转发层 + 握手。**只在引擎还不存在时调用一次**。
    ///
    /// `params.session_token`/`relay_base_url`/`client_user_agent` 只在这里起作用——
    /// 复用已有引擎的后续 `start()` 调用会忽略这几个字段（引擎只有一份，
    /// 这几样东西是它自己的，不是按 thread 变的）。`model` 同理，只当命令行的
    /// 默认值用，真正每轮用的模型走 [`SendParams::model`]。
    async fn spawn_engine(&self, params: &StartParams) -> Result<Spawned, BridgeError> {
        let home = CodexHome::under_app_dir(&self.paths.app_dir)
            .map_err(|e| BridgeError::Host(e.to_string()))?;
        home.prepare_powershell_utf8_profile()
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
            model: params.model.clone(),
        };

        let sink = Arc::clone(&self.sink);
        let restarted = Supervisor::new(config, Backoff::default())
            .start(|failed| {
                // 起不来的每一次都要看得见。悄悄重试到最后才报错，
                // 中间那段时间用户只会觉得「卡住了」。这个阶段还没有 thread，
                // 报不出归属，`thread_id: None` 如实反映这一点。
                sink.emit(UiEvent::Retrying {
                    thread_id: None,
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

        let pending: Pending = Arc::new(Mutex::new(HashMap::new()));
        spawn_pump(events, Arc::clone(&self.sink), Arc::clone(&pending), Arc::clone(&session));

        Ok(Spawned {
            engine: SharedEngine { session, _engine: engine, pending, home, relay },
            attempts,
        })
    }

    /// 发一轮提问。`model`/`reasoning` 按这一轮传，覆盖引擎起来时的命令行默认值——
    /// 共享一个进程之后，模型不再是进程级的常量。
    pub async fn send(&self, params: SendParams) -> Result<(), BridgeError> {
        let slot = self.engine.lock().await;
        let engine = slot.as_ref().ok_or(BridgeError::NotRunning)?;
        // 空字符串＝"标准/不覆盖"，翻成协议层的 None——上游的 effort 字段
        // 要求非空字符串，传空串会被拒。
        let reasoning = (!params.reasoning.trim().is_empty()).then_some(params.reasoning.as_str());
        engine
            .session
            .send_turn(&params.thread_id, &params.text, Some(params.model.as_str()), reasoning)
            .await
            .map(|_| ())
            .map_err(|e| BridgeError::Session(e.to_string()))
    }

    /// 打断某条 thread 上正在跑的那一轮。**不影响别的对话**。
    pub async fn interrupt(&self, thread_id: &str) -> Result<(), BridgeError> {
        let slot = self.engine.lock().await;
        let engine = slot.as_ref().ok_or(BridgeError::NotRunning)?;
        engine.session.interrupt(thread_id).await.map_err(|e| BridgeError::Session(e.to_string()))
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
        let slot = self.engine.lock().await;
        let engine = slot.as_ref().ok_or(BridgeError::NotRunning)?;

        let key = parse_request_id(&request_id);
        // 取出并**移除** —— 重复答复要能报错，不能悄悄答第二次。
        let request = engine
            .pending
            .lock()
            .await
            .remove(&key)
            .ok_or_else(|| BridgeError::NoSuchApproval(request_id.clone()))?;

        let answer = build_answer(&request, decision).map_err(BridgeError::Session)?;
        engine
            .session
            .answer(&answer)
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))
    }

    /// 归档一条 thread——**只影响它自己**，引擎和其它对话都还在跑。
    ///
    /// 用于"切审批模式""放弃/删除这个对话"这类需要结束一条 thread、但不该把
    /// 全体对话一起打断的场景。和 [`AgentBridge::stop`]（杀整个进程）是两回事，
    /// 别把两个混用——混用的后果是切一下审批模式，结果把邻座对话的引擎也拔了。
    pub async fn end_thread(&self, thread_id: &str) -> Result<(), BridgeError> {
        let slot = self.engine.lock().await;
        let engine = slot.as_ref().ok_or(BridgeError::NotRunning)?;
        engine
            .session
            .archive_thread(thread_id)
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))?;
        // 这条 thread 名下还没答复的审批，随它一起作废——那些 id 不会再有人问了，
        // 界面上还挂着的话，用户点下去只会拿到一个和自己毫不相干的错误。
        engine.pending.lock().await.retain(|_, req| req.thread_id() != Some(thread_id));
        self.sink.emit(UiEvent::ThreadEnded { thread_id: thread_id.to_owned() });
        Ok(())
    }

    /// Compact one live thread without stopping the shared engine.
    pub async fn compact(&self, thread_id: &str) -> Result<(), BridgeError> {
        let slot = self.engine.lock().await;
        let engine = slot.as_ref().ok_or(BridgeError::NotRunning)?;
        engine
            .session
            .compact_thread(thread_id)
            .await
            .map_err(|e| BridgeError::Session(e.to_string()))
    }

    /// 有序停止**整个引擎**：关 stdin 让 codex 自己收摊，超时再动手，然后抹掉凭据。
    ///
    /// **影响所有还挂着的对话**——多会话并发之后这是重家伙，不是"结束当前这个对话"
    /// 该调的东西（那是 [`AgentBridge::end_thread`]）。这个方法是给"整个应用要
    /// 退出/登出 agent"这类场景用的。
    pub async fn stop(&self) -> Result<(), BridgeError> {
        let mut slot = self.engine.lock().await;
        let Some(engine) = slot.take() else {
            return Err(BridgeError::NotRunning);
        };
        // 待答复的审批随引擎一起作废：那些 id 在新进程里不存在，
        // 界面上还挂着的话，用户点下去只会拿到 NoSuchApproval。
        engine.pending.lock().await.clear();

        let shutdown = engine._engine.shutdown(std::time::Duration::from_secs(5)).await;

        // 引擎结束 = 凭据不该再留在盘上。**先停进程再抹**，否则 codex 可能又写一遍。
        // 抹不掉要报错，但不能因此吞掉停止过程本身的失败。
        let wiped = engine.home.wipe_credentials();

        shutdown.map_err(|e| BridgeError::Host(e.to_string()))?;
        wiped.map(|_| ()).map_err(|e| BridgeError::Host(e.to_string()))
    }

    pub async fn is_running(&self) -> bool {
        self.engine.lock().await.is_some()
    }
}

/// 把会话事件翻成前端事件，顺便维护两张表：待答复审批（在 [`SharedEngine::pending`]
/// 里，不在这里）、`(threadId, itemId) → item`。
///
/// **这个任务和引擎同寿命，不是和某条 thread 同寿命**——一个引擎现在要服务好几条
/// 并发的 thread，事件会交替到达，所以这里的表都按 `thread_id` 分桶，
/// 不能再假设"当前只有一条 thread 在跑"。
/// 同一条 thread 连续重试超过这个数就主动放弃——数字对齐 codex 自己在
/// "Reconnecting... N/5" 里用的 5，不是巧合：codex 那个计数器是它自己的重连节奏，
/// 我们看不见、也拦不住；但如果连续 5 次都没能往前推进一步，跟着它无限等下去
/// 没有意义，我们自己的判断准绳定在它自己都认为"该数一数"的同一个点上。
const MAX_CONSECUTIVE_RETRIES: usize = 5;

fn spawn_pump(
    mut events: tokio::sync::mpsc::Receiver<SessionEvent>,
    sink: Arc<dyn EventSink>,
    pending: Pending,
    session: Arc<Session>,
) {
    tokio::spawn(async move {
        // (threadId, itemId) → item：审批请求只给 id，内容要靠这张表补上。
        // 按 thread 分桶是因为 itemId 只在它所属的 thread 内部保证唯一，
        // 两条并发的 thread 各自的 id 序列互不相干。
        let mut items: HashMap<(String, String), serde_json::Value> = HashMap::new();
        // 每条 thread 此刻在跑哪个 turn——只用来给正文事件补 turn_id 方便前端，
        // 真相来源是 `Session` 自己那张表，这里只是个跟着事件流走的镜像。
        let mut current_turn: HashMap<String, Option<String>> = HashMap::new();
        // 每条 thread 连续收到几次 Retrying——真实撞见过一次网关并发限流连续推了
        // 4 条以上还没完（账号并发配额被设成了 1）。`Session::drive_turn` 那套
        // 一次性驱动单轮的护栏在这里够不到：`spawn_pump` 和引擎同寿命、要同时
        // 服务好几条并发 thread，不是"驱动一轮然后返回"的形状，所以这里另外
        // 按 thread 记一份，逻辑跟 `drive_turn` 一致（有进展就清零，超限就放弃）。
        let mut retry_counts: HashMap<String, usize> = HashMap::new();

        while let Some(event) = events.recv().await {
            match event {
                SessionEvent::ThreadStarted { thread_id } => {
                    sink.emit(UiEvent::ThreadStarted { thread_id });
                }
                SessionEvent::TurnStarted { thread_id, turn_id } => {
                    current_turn.insert(thread_id.clone(), turn_id.clone());
                    retry_counts.remove(&thread_id);
                    sink.emit(UiEvent::TurnStarted { thread_id, turn_id });
                }
                SessionEvent::AgentText { thread_id, item_id, delta } => {
                    retry_counts.remove(&thread_id); // 有进展就把重试计数清零。
                    let turn_id = current_turn.get(&thread_id).cloned().flatten();
                    sink.emit(UiEvent::AgentText { thread_id, turn_id, item_id, delta });
                }
                SessionEvent::Reasoning { thread_id, delta, kind } => {
                    let turn_id = current_turn.get(&thread_id).cloned().flatten();
                    sink.emit(UiEvent::Reasoning {
                        thread_id,
                        turn_id,
                        delta,
                        kind: match kind {
                            ReasoningKind::Text => "text",
                            ReasoningKind::SummaryText => "summaryText",
                            ReasoningKind::SummaryPart => "summaryPart",
                        }
                        .to_owned(),
                    });
                }
                SessionEvent::CommandOutput { thread_id, item_id, chunk } => {
                    let turn_id = current_turn.get(&thread_id).cloned().flatten();
                    sink.emit(UiEvent::CommandOutput { thread_id, turn_id, item_id, chunk });
                }
                SessionEvent::TurnDiffUpdated { thread_id, turn_id, diff } => {
                    sink.emit(UiEvent::TurnDiffUpdated { thread_id, turn_id, diff });
                }
                SessionEvent::FileChangePatchUpdated {
                    thread_id,
                    turn_id,
                    item_id,
                    changes,
                } => {
                    sink.emit(UiEvent::FileChangePatchUpdated {
                        thread_id,
                        turn_id,
                        item_id,
                        changes,
                    });
                }
                SessionEvent::FileChangeOutputDelta {
                    thread_id,
                    turn_id,
                    item_id,
                    delta,
                } => {
                    sink.emit(UiEvent::FileChangeOutputDelta {
                        thread_id,
                        turn_id,
                        item_id,
                        delta,
                    });
                }
                SessionEvent::PlanDelta {
                    thread_id,
                    turn_id,
                    item_id,
                    delta,
                } => {
                    sink.emit(UiEvent::PlanDelta {
                        thread_id,
                        turn_id,
                        item_id,
                        delta,
                    });
                }
                SessionEvent::TurnPlanUpdated {
                    thread_id,
                    turn_id,
                    plan,
                    explanation,
                } => {
                    sink.emit(UiEvent::TurnPlanUpdated {
                        thread_id,
                        turn_id,
                        plan,
                        explanation,
                    });
                }
                SessionEvent::TerminalInteraction {
                    thread_id,
                    turn_id,
                    item_id,
                    process_id,
                    stdin,
                } => {
                    sink.emit(UiEvent::TerminalInteraction {
                        thread_id,
                        turn_id,
                        item_id,
                        process_id,
                        stdin,
                    });
                }
                SessionEvent::TurnModerationMetadata {
                    thread_id,
                    turn_id,
                    metadata,
                } => {
                    sink.emit(UiEvent::TurnModerationMetadata {
                        thread_id,
                        turn_id,
                        metadata,
                    });
                }
                SessionEvent::ItemStarted { thread_id, item_id, item_type, item } => {
                    if let Some(id) = &item_id {
                        items.insert((thread_id.clone(), id.clone()), item.clone());
                    }
                    sink.emit(UiEvent::Item {
                        thread_id,
                        item_id,
                        item_type,
                        status: None,
                        item,
                    });
                }
                SessionEvent::ItemCompleted { thread_id, item_id, item_type, item_status, item } => {
                    if let Some(id) = &item_id {
                        items.insert((thread_id.clone(), id.clone()), item.clone());
                    }
                    sink.emit(UiEvent::Item {
                        thread_id,
                        item_id,
                        item_type,
                        status: item_status,
                        item,
                    });
                }
                SessionEvent::StatusChanged { thread_id, status } => {
                    sink.emit(UiEvent::Status {
                        thread_id,
                        waiting_on_approval: status.is_waiting_on_approval(),
                        flags: status.active_flags.clone(),
                    });
                }
                SessionEvent::ApprovalRequested(request) => {
                    let ui = to_ui_approval(&request, &items);
                    pending.lock().await.insert(request.id.clone(), *request);
                    sink.emit(UiEvent::ApprovalRequested(ui));
                }
                SessionEvent::ApprovalResolved { thread_id, request_id } => {
                    // 另一端答了 —— 本地队列里那条也该消失。
                    pending.lock().await.remove(&request_id);
                    sink.emit(UiEvent::ApprovalResolved {
                        thread_id,
                        request_id: request_id.to_string(),
                    });
                }
                SessionEvent::Warning { thread_id, message } => {
                    sink.emit(UiEvent::Warning { thread_id, message });
                }
                SessionEvent::KnownNotification { method, thread_id, raw } => {
                    sink.emit(UiEvent::KnownNotification { method, thread_id, raw });
                }
                SessionEvent::Retrying(err) => {
                    match &err.thread_id {
                        Some(thread_id) => {
                            let attempts = retry_counts
                                .entry(thread_id.clone())
                                .and_modify(|n| *n += 1)
                                .or_insert(1);
                            if *attempts >= MAX_CONSECUTIVE_RETRIES {
                                let thread_id = thread_id.clone();
                                let attempts = *attempts;
                                retry_counts.remove(&thread_id);
                                // 打断失败也照样报「放弃」——那一轮反正已经没救了，
                                // 用户等的是一个说法，不是我们打断成不成功。最常见的
                                // 失败原因是打断指令到的时候轮次自己已经先收场了，
                                // 那是好消息（不需要我们出手），不是需要拦下的错误。
                                let _ = session.interrupt(&thread_id).await;
                                sink.emit(UiEvent::GaveUpRetrying {
                                    thread_id,
                                    attempts,
                                    last_message: err.message.clone(),
                                });
                            } else {
                                sink.emit(error_event(&err, true));
                            }
                        }
                        // 握手阶段等还没有 thread 的重试——没有归属可数，原样透出。
                        None => sink.emit(error_event(&err, true)),
                    }
                }
                SessionEvent::Failed(err) => {
                    if let Some(thread_id) = &err.thread_id {
                        retry_counts.remove(thread_id);
                    }
                    sink.emit(error_event(&err, false));
                }
                SessionEvent::TurnCompleted { thread_id, turn_id, status } => {
                    current_turn.insert(thread_id.clone(), None);
                    retry_counts.remove(&thread_id);
                    sink.emit(UiEvent::TurnCompleted {
                        thread_id,
                        turn_id,
                        success: status == "completed",
                        interrupted: status == "interrupted",
                        status,
                    });
                }
                SessionEvent::Passthrough { method, raw } => {
                    sink.emit(UiEvent::Passthrough { method, raw });
                }
                // 认识、但用不上的记账通知（token 用量、ChatGPT 套餐限额）——
                // 我们的配额和计费全在 sub2api 后端那一侧，故意不转发给界面。
                // 见 codex_adapter::protocol::NotificationPayload::Ignored。
                SessionEvent::Ignored { .. } => {}
                SessionEvent::DecodeError { line, error } => {
                    sink.emit(UiEvent::DecodeError { line, error });
                }
            }
        }

        // 流断了 = 引擎没了。这是**全局**事件——所有还挂着的对话都要收到，
        // 让界面知道，别停在一个永远转圈的状态上。
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
            thread_id: err.thread_id.clone(),
            message,
            http_status: err.http_status,
            auth_failure: err.is_auth_failure(),
        }
    } else {
        UiEvent::Failed {
            thread_id: err.thread_id.clone(),
            message,
            http_status: err.http_status,
            auth_failure: err.is_auth_failure(),
        }
    }
}

/// 把一条服务端请求翻成界面能显示的东西 —— **把 item 补上**。
fn to_ui_approval(
    request: &ServerRequest,
    items: &HashMap<(String, String), serde_json::Value>,
) -> ApprovalRequest {
    let thread_id = request.thread_id().map(str::to_owned);
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

    let item = match (&thread_id, &item_id) {
        (Some(tid), Some(iid)) => items.get(&(tid.clone(), iid.clone())).cloned(),
        _ => None,
    };

    ApprovalRequest {
        thread_id,
        request_id: request.id.to_string(),
        method: request.method.clone(),
        kind,
        reason,
        command,
        cwd,
        item,
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
