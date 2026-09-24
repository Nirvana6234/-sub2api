//! 会话层的回放测试：拿录制报文喂进去，不起进程。

use std::io::Cursor;

use codex_adapter::session::{drive_turn, Session, SessionEvent, TurnStatus};
use serde_json::Value;

const APPROVAL_RUN: &str = include_str!("fixtures/decline-command-approval.jsonl");
const BAD_CREDENTIALS: &str = include_str!("fixtures/bad-credentials.jsonl");

/// 把录制里服务端发来的那些行还原成一条流。
fn server_stream(fixture: &str) -> Cursor<Vec<u8>> {
    let mut out = String::new();
    for line in fixture.lines().filter(|l| !l.trim().is_empty()) {
        let rec: Value = serde_json::from_str(line).expect("fixture 行不是合法 JSON");
        if rec.get("dir").and_then(Value::as_str) != Some("server->client") {
            continue;
        }
        if let Some(msg) = rec.get("msg") {
            out.push_str(&serde_json::to_string(msg).unwrap());
            out.push('\n');
        }
    }
    Cursor::new(out.into_bytes())
}

/// 从录制里挖出 `thread/started` 通知里的那个 threadId——多会话并发改造之后，
/// `Session` 的方法都要显式传 thread_id，测试不能再假装"只有一条会话"。
fn recorded_thread_id(fixture: &str) -> String {
    for line in fixture.lines().filter(|l| !l.trim().is_empty()) {
        let rec: Value = serde_json::from_str(line).expect("fixture 行不是合法 JSON");
        let Some(msg) = rec.get("msg") else { continue };
        if msg.get("method").and_then(Value::as_str) == Some("thread/started") {
            if let Some(id) = msg
                .get("params")
                .and_then(|p| p.get("thread"))
                .and_then(|t| t.get("id"))
                .and_then(Value::as_str)
            {
                return id.to_owned();
            }
        }
    }
    panic!("录制里没有 thread/started，挖不出 threadId");
}

#[tokio::test]
async fn drives_a_recorded_turn_to_completion_and_collects_the_text() {
    let (session, mut events) = Session::connect(server_stream(APPROVAL_RUN), tokio::io::sink());

    let mut saw_approval = false;
    let mut saw_waiting = false;
    let outcome = drive_turn(&session, &mut events, 5, |e| match e {
        SessionEvent::ApprovalRequested(_) => saw_approval = true,
        SessionEvent::StatusChanged { status, .. } if status.is_waiting_on_approval() => {
            saw_waiting = true;
        }
        _ => {}
    })
    .await;

    // A2 的验收就是这两条：assistant 正文 + turn/completed。
    assert!(!outcome.text.is_empty(), "没拼出 assistant 正文");
    assert_eq!(outcome.status, TurnStatus::Completed("completed".to_owned()));
    assert!(saw_approval, "审批请求没透出给调用方");
    assert!(saw_waiting, "waitingOnApproval 状态没透出给调用方");
    assert!(outcome.events > 20, "事件太少: {}", outcome.events);
}

/// `turn/started` 一到，会话就必须记住 turnId —— 用户这时点停止得打得中。
///
/// 用截断到 `turn/started` 为止的流来验：跑完整段流是验不到的，因为翻译任务会一路
/// 跑到 `turn/completed` 再把 turnId 清掉（`current_turn()` 是「此刻」而不是
/// 「消费者读到哪」的状态，这是有意的）。
#[tokio::test]
async fn records_the_turn_id_as_soon_as_the_notification_lands() {
    let full = server_stream(APPROVAL_RUN).into_inner();
    let text = String::from_utf8(full).unwrap();
    let prefix: String = text
        .lines()
        .take_while(|l| !l.contains("\"turn/completed\""))
        .map(|l| format!("{l}\n"))
        .collect();
    assert!(prefix.contains("turn/started"), "截断点选错了");

    let (session, mut events) = Session::connect(Cursor::new(prefix.into_bytes()), tokio::io::sink());

    let mut announced = None;
    while let Some(event) = events.recv().await {
        if let SessionEvent::TurnStarted { turn_id, .. } = &event {
            announced = turn_id.clone();
        }
    }

    assert!(announced.is_some(), "录制里应当有带 turnId 的 turn/started");
    let thread_id = recorded_thread_id(APPROVAL_RUN);
    assert_eq!(
        session.current_turn(&thread_id),
        announced,
        "turn/started 到了但会话没记住 turnId"
    );
    assert!(session.known_thread(&thread_id), "threadId 没记住");
}

/// 一轮收束后 turnId 必须清掉，否则「停止」会打向一个已经结束的轮次。
#[tokio::test]
async fn clears_the_turn_id_once_the_turn_completes() {
    let (session, mut events) = Session::connect(server_stream(APPROVAL_RUN), tokio::io::sink());
    while events.recv().await.is_some() {}

    let thread_id = recorded_thread_id(APPROVAL_RUN);
    assert!(session.known_thread(&thread_id), "threadId 不该被清掉");
    assert_eq!(session.current_turn(&thread_id), None, "turn/completed 后 turnId 没清掉");
}

/// 只等 `turn/completed` 的代码会在鉴权失败时永远挂着 —— 这条盯的就是那个护栏。
#[tokio::test]
async fn gives_up_instead_of_hanging_when_the_upstream_keeps_retrying() {
    let (session, mut events) = Session::connect(server_stream(BAD_CREDENTIALS), tokio::io::sink());

    let mut retries_observed = 0;
    let outcome = drive_turn(&session, &mut events, 2, |e| {
        if matches!(e, SessionEvent::Retrying(_)) {
            retries_observed += 1;
        }
    })
    .await;

    match outcome.status {
        TurnStatus::GaveUpRetrying { attempts, ref last } => {
            assert_eq!(attempts, 2);
            assert!(last.contains("Reconnecting"), "护栏没带上最后一条错误: {last}");
        }
        other => panic!("应当因重试过多而放弃，实际是 {other:?}"),
    }

    // 重试通知必须**透出给调用方**，而不是在等待期间被吞掉 ——
    // 用户要看到「正在重试」，不是对着一个不动的界面猜。
    assert!(retries_observed >= 2, "重试事件没透出: {retries_observed}");
}

/// 没有进行中的轮次时，停止必须报错而不是静默成功。
#[tokio::test]
async fn interrupt_without_an_active_turn_fails_loudly() {
    let (session, _events) = Session::connect(Cursor::new(Vec::new()), tokio::io::sink());
    let err = session.interrupt("thread-nobody-knows").await.unwrap_err();
    assert!(
        err.to_string().contains("没有会话"),
        "报错该说清楚为什么打断不了: {err}"
    );
}

// ---------------------------------------------------------------------------
// 多会话并发改造：两条 thread 交替送进同一个 Session，状态不能串
// ---------------------------------------------------------------------------

/// 手写两条 thread 交替到达的通知行，而不是拿两份录制拼——这样能精确控制
/// 到达顺序，把最容易漏的那个时刻钉死：**A 完成的时候，B 的状态不能被碰**。
///
/// 报文形状取自真实录制（见 `tests/fixtures/*.jsonl`），不是编的。
fn line(method: &str, params: serde_json::Value) -> String {
    serde_json::to_string(&serde_json::json!({
        "jsonrpc": "2.0",
        "method": method,
        "params": params,
    }))
    .unwrap()
}

#[tokio::test]
async fn concurrent_threads_on_one_session_do_not_clobber_each_others_turn_state() {
    const THREAD_A: &str = "thread-aaa";
    const THREAD_B: &str = "thread-bbb";

    // 顺序是刻意的：A 的 turn/started 先到、B 的后到，然后**只完成 A**。
    // 如果状态还是旧版那种单值（不是按 thread_id 分桶的表），"完成 A" 那一步会把
    // B 的 turnId 也一起清掉——下面的断言就是专门盯这个的。
    let script = [
        line("thread/started", serde_json::json!({ "thread": { "id": THREAD_A } })),
        line(
            "turn/started",
            serde_json::json!({ "threadId": THREAD_A, "turn": { "id": "turn-a1" } }),
        ),
        line("thread/started", serde_json::json!({ "thread": { "id": THREAD_B } })),
        line(
            "turn/started",
            serde_json::json!({ "threadId": THREAD_B, "turn": { "id": "turn-b1" } }),
        ),
        line(
            "item/agentMessage/delta",
            serde_json::json!({ "threadId": THREAD_A, "itemId": "item-a", "delta": "hello-a" }),
        ),
        line(
            "item/agentMessage/delta",
            serde_json::json!({ "threadId": THREAD_B, "itemId": "item-b", "delta": "hello-b" }),
        ),
        line(
            "turn/completed",
            serde_json::json!({ "threadId": THREAD_A, "turn": { "id": "turn-a1", "status": "completed" } }),
        ),
    ]
    .join("\n")
        + "\n";

    let (session, mut events) = Session::connect(Cursor::new(script.into_bytes()), tokio::io::sink());

    let mut agent_text: std::collections::HashMap<String, String> = std::collections::HashMap::new();
    let mut turn_completed = 0;
    while turn_completed < 1 {
        let Some(event) = events.recv().await else { break };
        match event {
            SessionEvent::AgentText { thread_id, delta, .. } => {
                agent_text.entry(thread_id).or_default().push_str(&delta);
            }
            SessionEvent::TurnCompleted { .. } => turn_completed += 1,
            _ => {}
        }
    }

    // 两条 thread 各自的正文没有被对方污染——这是"按 thread_id 路由"最直接的证据。
    assert_eq!(agent_text.get(THREAD_A).map(String::as_str), Some("hello-a"));
    assert_eq!(agent_text.get(THREAD_B).map(String::as_str), Some("hello-b"));

    // **这是这条测试真正要抓的断言**：A 完成了（清成 None），**B 完全没被碰**。
    // 旧的单值实现在"完成 A"那一步会把唯一那个槽位清空，B 的 turnId 会跟着消失——
    // 从单值 turnId 换成按 thread_id 分桶的表，保的就是这条性质。
    assert_eq!(session.current_turn(THREAD_A), None, "A 应当已经清空");
    assert_eq!(
        session.current_turn(THREAD_B).as_deref(),
        Some("turn-b1"),
        "B 的 turnId 不该被 A 的 turn/completed 碰到——如果这里变成 None，\
         说明状态又退回单值了"
    );
    assert!(session.known_thread(THREAD_B), "B 这条 thread 本身也不该被摘掉");
}
