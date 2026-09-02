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
    assert_eq!(session.current_turn(), announced, "turn/started 到了但会话没记住 turnId");
    assert!(session.current_thread().is_some(), "threadId 没记住");
}

/// 一轮收束后 turnId 必须清掉，否则「停止」会打向一个已经结束的轮次。
#[tokio::test]
async fn clears_the_turn_id_once_the_turn_completes() {
    let (session, mut events) = Session::connect(server_stream(APPROVAL_RUN), tokio::io::sink());
    while events.recv().await.is_some() {}

    assert!(session.current_thread().is_some(), "threadId 不该被清掉");
    assert_eq!(session.current_turn(), None, "turn/completed 后 turnId 没清掉");
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
    let err = session.interrupt().await.unwrap_err();
    assert!(
        err.to_string().contains("没有会话"),
        "报错该说清楚为什么打断不了: {err}"
    );
}
