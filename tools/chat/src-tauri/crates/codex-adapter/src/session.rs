//! 会话面：起会话 → 发提问 → 收流 → 停止 → 续跑。
//!
//! 这一层把协议报文翻成**上层真正需要的事件**，同时替上层记住它总会忘的事：每条
//! thread 当前的 `turnId`。这件事尤其重要 —— `turn/interrupt` 必须带 `turnId`，
//! 而它会从两条路各送来一次（`turn/start` 的响应、`turn/started` 通知），且**通知可能
//! 先到**。谁先到就用谁，否则用户早早点停止会打不中。
//!
//! # 一个 `Session` 现在能同时驱动多条 thread
//!
//! 多会话并发改造（见开发计划）之前，这里只跟踪"当前唯一一条" thread/turn。
//! 探针实测（`codex-host/scripts/probe-multi-thread.py`，真二进制）证明一个 codex
//! 进程能真的**并发**跑两条 thread 的 turn——两次 `turn/start` 同时发出去，两边的
//! 出站 HTTP 请求同一毫秒开始、同一毫秒结束，增量事件交替到达，不是排队串行。
//! 所以状态从单值换成按 `thread_id` 分桶的表，`send_turn`/`interrupt` 都显式要
//! `thread_id` 参数——**不再有"当前那条 thread"这个隐含概念**，那正是旧设计里
//! 只能服务一个会话的根源。
//!
//! 和 [`crate::transport`] 一样，**这里不 spawn 进程**。给它一对读写半边就行，
//! 真进程由宿主（A4）提供，测试可以喂内存流或自己起一个 codex。

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use serde_json::Value;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::sync::mpsc;

use crate::error::AdapterError;
use crate::protocol::{
    Answer, ApprovalPolicy, ClientRequest, ErrorNotification, NotificationPayload, ReasoningKind,
    RequestId, SandboxMode, ServerRequest, ThreadStatus, WorkspaceDir,
};
use crate::transport::{pump, Client, Incoming};

/// 上层要消费的事件。比协议通知高一层，但**不认识的东西一律保留原样**。
#[derive(Debug)]
pub enum SessionEvent {
    ThreadStarted {
        thread_id: String,
    },
    TurnStarted {
        thread_id: String,
        turn_id: Option<String>,
    },
    /// assistant 正文增量。**带 `thread_id`**——共享一个 codex 进程时，两条并发的
    /// thread 会把各自的增量交替送进同一条事件流，不靠这个字段没法认领。
    AgentText {
        thread_id: String,
        item_id: String,
        delta: String,
    },
    /// 思考过程增量。
    Reasoning {
        thread_id: String,
        delta: String,
        kind: ReasoningKind,
    },
    /// 命令执行的输出增量。
    CommandOutput {
        thread_id: String,
        item_id: Option<String>,
        chunk: String,
    },
    TurnDiffUpdated {
        thread_id: String,
        turn_id: String,
        diff: String,
    },
    FileChangePatchUpdated {
        thread_id: String,
        turn_id: String,
        item_id: String,
        changes: Value,
    },
    FileChangeOutputDelta {
        thread_id: String,
        turn_id: String,
        item_id: String,
        delta: String,
    },
    PlanDelta {
        thread_id: String,
        turn_id: String,
        item_id: String,
        delta: String,
    },
    TurnPlanUpdated {
        thread_id: String,
        turn_id: String,
        plan: Value,
        explanation: Option<String>,
    },
    TerminalInteraction {
        thread_id: String,
        turn_id: String,
        item_id: String,
        process_id: String,
        stdin: String,
    },
    TurnModerationMetadata {
        thread_id: String,
        turn_id: String,
        metadata: Value,
    },
    /// 会话状态变化，最要紧的是 `waiting_on_approval`。
    StatusChanged {
        thread_id: String,
        status: ThreadStatus,
    },
    /// 服务端在等我们答复。**不答这一轮就卡死**，见 [`Session::respond`]。
    ApprovalRequested(Box<ServerRequest>),
    /// 某个服务端请求已被答复（可能是另一端答的）。用来做「一处响应、另一处队列消失」。
    ApprovalResolved {
        thread_id: String,
        request_id: RequestId,
    },
    /// 出错了但 codex 会自己重试 —— **这不是终态**，UI 该显示「正在重试」。
    /// `ErrorNotification` 自己带 `thread_id`，不必在这里再重复一份。
    Retrying(Box<ErrorNotification>),
    /// 出错且不再重试。
    Failed(Box<ErrorNotification>),
    TurnCompleted {
        thread_id: String,
        turn_id: Option<String>,
        status: String,
    },
    /// 一个 item 开始了。
    ///
    /// **审批 UI 必须留着这些。** `item/fileChange/requestApproval` 只给 id，
    /// 真正「改了什么」（路径 + diff）在这里的 `item.changes[]` 里 ——
    /// 审批请求是个指针，UI 得按 `item_id` 回查。
    ItemStarted {
        thread_id: String,
        item_id: Option<String>,
        item_type: String,
        item: Value,
    },
    /// 一个 item 结束了。`item_status` 在拒绝之后会是 `"declined"`，
    /// 是「拒绝确实生效」的逐项证据。
    ItemCompleted {
        thread_id: String,
        item_id: Option<String>,
        item_type: String,
        item_status: Option<String>,
        item: Value,
    },
    /// 我们没投影的上游通知。**原样带着**，本机链路要能显示上游新事件。
    Passthrough {
        method: String,
        raw: Value,
    },
    /// codex 主动说给用户听的一句话（见 `NotificationPayload::Warning`）。
    /// **必须**转给界面——不是我们没见过的协议，是 codex 认为用户该知道。
    /// `thread_id` 不是每次都有（有些提醒是账号级的，不属于任何一条 thread）。
    Warning {
        thread_id: Option<String>,
        message: String,
    },
    /// 已知的上游通知，保留原始 payload 交给 UI 按方法归类。
    KnownNotification {
        method: String,
        thread_id: Option<String>,
        raw: Value,
    },
    /// 认识、但故意不往上传的方法（见 `NotificationPayload::Ignored`）。
    /// **不产生任何 UI 事件**——这条和 `Passthrough` 的区别就在这里：
    /// `Passthrough` 是"看不懂，你们看看要不要处理"，这条是"看懂了，不用管"。
    Ignored {
        #[allow(dead_code)]
        method: String,
    },
    /// 有一行解不开 —— 基本等于版本漂移。
    DecodeError {
        line: String,
        error: String,
    },
}

#[derive(Debug, Default)]
struct SessionState {
    /// 每条活着的 thread → 它此刻正在跑的 turnId（`None` = 没有正在跑的轮次）。
    /// key 存在即代表这条 thread 是这个 Session 认识的。
    turns: HashMap<String, Option<String>>,
}

type BoxedWriter = Box<dyn AsyncWrite + Unpin + Send>;

/// 一条已经连上的 app-server 会话。
pub struct Session {
    client: Arc<Client<BoxedWriter>>,
    state: Arc<Mutex<SessionState>>,
}

impl Session {
    /// 接上一对读写半边，开始泵事件。
    ///
    /// 返回的接收端就是事件流；**丢掉它会让泵停下来**。
    pub fn connect<R, W>(reader: R, writer: W) -> (Self, mpsc::Receiver<SessionEvent>)
    where
        R: AsyncRead + Unpin + Send + 'static,
        W: AsyncWrite + Unpin + Send + 'static,
    {
        let (client, pending) = Client::new(Box::new(writer) as BoxedWriter);
        let state = Arc::new(Mutex::new(SessionState::default()));

        let (raw_tx, mut raw_rx) = mpsc::channel::<Incoming>(256);
        let (event_tx, event_rx) = mpsc::channel::<SessionEvent>(256);

        tokio::spawn(async move { pump(reader, pending, raw_tx).await });

        let translator_state = Arc::clone(&state);
        tokio::spawn(async move {
            while let Some(incoming) = raw_rx.recv().await {
                let event = translate(incoming, &translator_state);
                if event_tx.send(event).await.is_err() {
                    break; // 上层不收了
                }
            }
        });

        (Session { client, state }, event_rx)
    }

    /// 握手 + 送凭据。
    ///
    /// 凭据走协议而不是环境变量 —— `CODEX_API_KEY` 对 app-server 无效，
    /// 而且走 RPC 的凭据不会被子进程继承、不出现在进程列表里。
    pub async fn handshake(
        &self,
        client_name: &str,
        client_version: &str,
        api_key: &str,
    ) -> Result<(), AdapterError> {
        self.client
            .request(ClientRequest::Initialize {
                client_name: client_name.to_owned(),
                client_version: client_version.to_owned(),
            })
            .await?;
        self.client.notify("initialized", serde_json::json!({})).await?;
        self.client
            .request(ClientRequest::LoginApiKey { api_key: api_key.to_owned() })
            .await?;
        Ok(())
    }

    /// 起一个新会话，返回 threadId。
    pub async fn start_thread(
        &self,
        cwd: WorkspaceDir,
        sandbox: SandboxMode,
        approval_policy: ApprovalPolicy,
    ) -> Result<String, AdapterError> {
        let result = self
            .client
            .request(ClientRequest::ThreadStart { cwd, sandbox, approval_policy })
            .await?;
        let thread_id = thread_id_of(&result).ok_or_else(|| {
            AdapterError::Protocol(format!("thread/start 响应里没有 thread id: {result}"))
        })?;
        self.state.lock().unwrap().turns.entry(thread_id.clone()).or_insert(None);
        Ok(thread_id)
    }

    /// 续跑一个旧会话。
    pub async fn resume_thread(&self, thread_id: &str) -> Result<String, AdapterError> {
        let result = self
            .client
            .request(ClientRequest::ThreadResume { thread_id: thread_id.to_owned() })
            .await?;
        let resumed = thread_id_of(&result).unwrap_or_else(|| thread_id.to_owned());
        self.state.lock().unwrap().turns.entry(resumed.clone()).or_insert(None);
        Ok(resumed)
    }

    /// 归档一条 thread：**只影响它自己**，Session 上其它并发的 thread 不受影响。
    ///
    /// 多会话并发改造之后，"结束/放弃某个对话"不再等于"关掉整个引擎"——引擎是共享的，
    /// 关了就把还在用别的对话的人一起断了。这是对应的细粒度操作。
    pub async fn archive_thread(&self, thread_id: &str) -> Result<(), AdapterError> {
        self.client
            .request(ClientRequest::ThreadArchive { thread_id: thread_id.to_owned() })
            .await?;
        self.state.lock().unwrap().turns.remove(thread_id);
        Ok(())
    }

    /// Ask codex to compact the context of one thread.
    pub async fn compact_thread(&self, thread_id: &str) -> Result<(), AdapterError> {
        self.client
            .request(ClientRequest::ThreadCompactStart { thread_id: thread_id.to_owned() })
            .await?;
        Ok(())
    }

    /// 发一轮提问。返回 turnId（响应里带的那个）。
    ///
    /// 注意 `turn/started` 通知**可能比这个响应先到**，两条路都会写同一个 turnId，
    /// 谁先到算谁的 —— 见 [`Session::current_turn`]。
    ///
    /// `model`/`effort` **必须每轮都传**：共享一个进程之后，模型不再是进程级的常量，
    /// 由调用方（每条对话记着自己选了什么）在这里按轮覆盖。
    pub async fn send_turn(
        &self,
        thread_id: &str,
        text: &str,
        model: Option<&str>,
        effort: Option<&str>,
    ) -> Result<Option<String>, AdapterError> {
        let result = self
            .client
            .request(ClientRequest::TurnStart {
                thread_id: thread_id.to_owned(),
                text: text.to_owned(),
                model: model.map(str::to_owned),
                effort: effort.map(str::to_owned),
            })
            .await?;

        let turn_id = result.get("turn").and_then(|t| t.get("id")).and_then(Value::as_str);
        if let Some(id) = turn_id {
            let mut state = self.state.lock().unwrap();
            // 通知先到的话这里就是同一个值，覆盖无害。
            state.turns.insert(thread_id.to_owned(), Some(id.to_owned()));
        }
        Ok(turn_id.map(str::to_owned))
    }

    /// 打断指定 thread 上正在跑的那一轮。
    ///
    /// **没有进行中的轮次时直接报错，不静默成功** —— 悄悄什么都不做会让「点了停止却
    /// 一直在跑」变成一个查不出来的 bug。**不认识这条 thread** 时也报错，而不是
    /// 误把它当"没有轮次"——两种失败原因不一样，报错文案要分得开。
    pub async fn interrupt(&self, thread_id: &str) -> Result<(), AdapterError> {
        let turn_id = {
            let state = self.state.lock().unwrap();
            match state.turns.get(thread_id) {
                Some(turn_id) => turn_id.clone(),
                None => return Err(AdapterError::Protocol("没有会话可打断".to_owned())),
            }
        };
        let turn_id = turn_id.ok_or_else(|| {
            AdapterError::Protocol("没有进行中的轮次可打断（turn/interrupt 必须带 turnId）".to_owned())
        })?;

        self.client
            .request(ClientRequest::TurnInterrupt {
                thread_id: thread_id.to_owned(),
                turn_id,
            })
            .await?;
        self.state.lock().unwrap().turns.insert(thread_id.to_owned(), None);
        Ok(())
    }

    /// 答复一个服务端请求。
    ///
    /// **每个 [`SessionEvent::ApprovalRequested`] 都必须走到这里**，包括我们不认识的
    /// 那些 —— 不答复 turn 会一直等下去。
    ///
    /// [`Answer`] 只能从那条请求本身生成（[`ServerRequest::approve`] /
    /// [`ServerRequest::deny`] / [`ServerRequest::grant_permissions`]），
    /// 所以「拿 A 类请求的响应去答 B 类请求」在类型上就写不出来。这不是洁癖：
    /// 三类审批的响应形状**互不相同**，旧的 `execCommandApproval` 连决定词汇都是另一套。
    pub async fn answer(&self, answer: &Answer) -> Result<(), AdapterError> {
        self.client.answer(answer).await
    }

    /// 这条 thread 是不是这个 Session 认识的（`start_thread`/`resume_thread` 登记过、
    /// 还没被 `archive_thread` 摘掉）。
    pub fn known_thread(&self, thread_id: &str) -> bool {
        self.state.lock().unwrap().turns.contains_key(thread_id)
    }

    /// 这条 thread 此刻的轮次 id。由 `turn/start` 的响应和 `turn/started` 通知**竞相**填写。
    ///
    /// # 这是「此刻」的状态，不是「你读到哪了」的状态
    ///
    /// 翻译任务会跑在事件消费者前面。所以当上层刚从事件流里取到这条 thread 的
    /// `TurnStarted` 时，这里可能**已经**因为 `turn/completed` 而变回 `None` 了。
    ///
    /// 对 [`Session::interrupt`] 而言这正是想要的语义 —— 要打断的是此刻真在跑的那一轮，
    /// 而不是界面刚渲染到的那一轮。但**别拿它去判断某个事件属于哪一轮**：
    /// 那种场景要用事件自己带的 `thread_id`/`turn_id`。
    pub fn current_turn(&self, thread_id: &str) -> Option<String> {
        self.state.lock().unwrap().turns.get(thread_id).cloned().flatten()
    }
}

fn thread_id_of(result: &Value) -> Option<String> {
    result
        .get("thread")
        .and_then(|t| t.get("id"))
        .and_then(Value::as_str)
        .or_else(|| result.get("threadId").and_then(Value::as_str))
        .map(str::to_owned)
}

fn translate(incoming: Incoming, state: &Arc<Mutex<SessionState>>) -> SessionEvent {
    match incoming {
        Incoming::DecodeError { line, error } => SessionEvent::DecodeError { line, error },
        Incoming::Request(req) => SessionEvent::ApprovalRequested(Box::new(req)),
        Incoming::Notification(note) => match note.payload {
            NotificationPayload::ThreadStarted { thread_id } => {
                state.lock().unwrap().turns.entry(thread_id.clone()).or_insert(None);
                SessionEvent::ThreadStarted { thread_id }
            }
            NotificationPayload::TurnStarted { thread_id, turn_id } => {
                if let Some(id) = &turn_id {
                    // 可能比 turn/start 的响应先到 —— 谁先到算谁的。**只写这一条
                    // thread 自己的槽位**，不能碰其它并发 thread 的状态。
                    state.lock().unwrap().turns.insert(thread_id.clone(), Some(id.clone()));
                }
                SessionEvent::TurnStarted { thread_id, turn_id }
            }
            NotificationPayload::TurnCompleted { thread_id, turn_id, status } => {
                state.lock().unwrap().turns.insert(thread_id.clone(), None);
                SessionEvent::TurnCompleted { thread_id, turn_id, status }
            }
            NotificationPayload::AgentMessageDelta { thread_id, item_id, delta } => {
                SessionEvent::AgentText { thread_id, item_id, delta }
            }
            NotificationPayload::ReasoningDelta { thread_id, delta, kind, .. } => {
                SessionEvent::Reasoning { thread_id, delta, kind }
            }
            NotificationPayload::CommandOutputDelta { thread_id, item_id, chunk } => {
                SessionEvent::CommandOutput { thread_id, item_id, chunk }
            }
            NotificationPayload::TurnDiffUpdated { thread_id, turn_id, diff } => {
                SessionEvent::TurnDiffUpdated { thread_id, turn_id, diff }
            }
            NotificationPayload::FileChangePatchUpdated {
                thread_id,
                turn_id,
                item_id,
                changes,
            } => SessionEvent::FileChangePatchUpdated {
                thread_id,
                turn_id,
                item_id,
                changes,
            },
            NotificationPayload::FileChangeOutputDelta {
                thread_id,
                turn_id,
                item_id,
                delta,
            } => SessionEvent::FileChangeOutputDelta {
                thread_id,
                turn_id,
                item_id,
                delta,
            },
            NotificationPayload::PlanDelta {
                thread_id,
                turn_id,
                item_id,
                delta,
            } => SessionEvent::PlanDelta {
                thread_id,
                turn_id,
                item_id,
                delta,
            },
            NotificationPayload::TurnPlanUpdated {
                thread_id,
                turn_id,
                plan,
                explanation,
            } => SessionEvent::TurnPlanUpdated {
                thread_id,
                turn_id,
                plan,
                explanation,
            },
            NotificationPayload::TerminalInteraction {
                thread_id,
                turn_id,
                item_id,
                process_id,
                stdin,
            } => SessionEvent::TerminalInteraction {
                thread_id,
                turn_id,
                item_id,
                process_id,
                stdin,
            },
            NotificationPayload::TurnModerationMetadata {
                thread_id,
                turn_id,
                metadata,
            } => SessionEvent::TurnModerationMetadata {
                thread_id,
                turn_id,
                metadata,
            },
            NotificationPayload::ThreadStatusChanged { thread_id, status } => {
                SessionEvent::StatusChanged { thread_id, status }
            }
            NotificationPayload::ServerRequestResolved { thread_id, request_id } => {
                SessionEvent::ApprovalResolved { thread_id, request_id }
            }
            NotificationPayload::Error(err) => {
                if err.will_retry {
                    SessionEvent::Retrying(Box::new(err))
                } else {
                    SessionEvent::Failed(Box::new(err))
                }
            }
            NotificationPayload::ItemStarted { thread_id, item_type, item_id, item } => {
                SessionEvent::ItemStarted { thread_id, item_id, item_type, item }
            }
            NotificationPayload::ItemCompleted {
                thread_id, item_type, item_id, item_status, item,
            } => SessionEvent::ItemCompleted { thread_id, item_id, item_type, item_status, item },
            NotificationPayload::Warning { thread_id, message } => {
                SessionEvent::Warning { thread_id, message }
            }
            NotificationPayload::Known { method, thread_id, raw } => {
                SessionEvent::KnownNotification { method, thread_id, raw }
            }
            NotificationPayload::Ignored { method } => SessionEvent::Ignored { method },
            NotificationPayload::Other => {
                SessionEvent::Passthrough { method: note.method, raw: note.raw }
            }
        },
    }
}

// ---------------------------------------------------------------------------
// 把一轮开到终点
// ---------------------------------------------------------------------------

/// 一轮跑完之后的结果。
#[derive(Debug)]
pub struct TurnOutcome {
    /// 拼起来的 assistant 正文。
    pub text: String,
    pub status: TurnStatus,
    /// 这一轮一共经过了多少个事件（诊断用）。
    pub events: usize,
}

#[derive(Debug, PartialEq)]
pub enum TurnStatus {
    /// 上游以 `turn/completed` 收束，里面带的 `status` 原样放在这里。
    ///
    /// **别把它当成「成功」的同义词。** 实测：打断一轮之后，上游同样发
    /// `turn/completed`，只是 `status` 是 `"interrupted"` —— 不是 `turn/failed`。
    /// 所以「跑完了」和「被停掉了」要靠这个字符串分辨，用 [`TurnStatus::is_success`]。
    /// 保留原始字符串而不是穷举枚举，是因为上游还会加新状态。
    Completed(String),
    /// 收到了不再重试的错误。
    Failed(String),
    /// 事件流断了（进程没了）。
    Disconnected,
    /// 连续重试太多次仍无进展，我们主动放弃。
    ///
    /// **这是我们自己的护栏，不是上游的终态。** 实测用无效凭据发一轮时，codex 会一直
    /// `Reconnecting... n/5` 地重试，而 `turn/completed` **不会来** —— 只等
    /// `turn/completed` 的代码会永远挂在那里。上游最终会不会自己收束、以什么形式收束，
    /// 我们没观测到，所以这里按次数兜底。
    GaveUpRetrying { attempts: usize, last: String },
}

impl TurnStatus {
    /// 这一轮是不是正常跑完的（不是被打断、不是失败）。
    pub fn is_success(&self) -> bool {
        matches!(self, TurnStatus::Completed(s) if s == "completed")
    }

    /// 是不是被打断的。
    pub fn is_interrupted(&self) -> bool {
        matches!(self, TurnStatus::Completed(s) if s == "interrupted")
    }
}

/// 驱动一轮直到终态，把 assistant 正文拼出来。
///
/// 审批默认**拒绝**（`decline`：拒绝但让 agent 继续这一轮）。A2 不做审批 UI，
/// 但**必须答复** —— 不答复 turn 会一直等。真正的审批交互是 A3 的活。
///
/// 每个事件都会先交给 `observe` 再被处理，所以调用方能看到重试通知，
/// 而不是在等待期间被吞掉。
pub async fn drive_turn(
    session: &Session,
    events: &mut mpsc::Receiver<SessionEvent>,
    max_retry_errors: usize,
    mut observe: impl FnMut(&SessionEvent),
) -> TurnOutcome {
    let mut text = String::new();
    let mut count = 0usize;
    let mut retries = 0usize;

    while let Some(event) = events.recv().await {
        count += 1;
        observe(&event);

        match event {
            SessionEvent::AgentText { delta, .. } => {
                text.push_str(&delta);
                retries = 0; // 有进展就把重试计数清零
            }
            SessionEvent::ApprovalRequested(req) => {
                // 不答复就卡死，所以默认拒绝。`deny()` 是全的：每一类请求（包括我们
                // 不认识的）都有一个它自己形状的「不」，不会拿甲的响应去答乙。
                let _ = session.answer(&req.deny()).await;
            }
            SessionEvent::Retrying(err) => {
                retries += 1;
                if retries >= max_retry_errors {
                    return TurnOutcome {
                        text,
                        status: TurnStatus::GaveUpRetrying {
                            attempts: retries,
                            last: err.message.clone(),
                        },
                        events: count,
                    };
                }
            }
            SessionEvent::Failed(err) => {
                return TurnOutcome {
                    text,
                    status: TurnStatus::Failed(err.message.clone()),
                    events: count,
                };
            }
            SessionEvent::TurnCompleted { status, .. } => {
                return TurnOutcome { text, status: TurnStatus::Completed(status), events: count };
            }
            _ => {}
        }
    }

    TurnOutcome { text, status: TurnStatus::Disconnected, events: count }
}
