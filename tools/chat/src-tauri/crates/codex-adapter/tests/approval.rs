//! 审批面：三类 `requestApproval` 的请求与响应。
//!
//! A3 的验收就是把探针脚本的结论变成常驻测试 —— **拒绝之后动作真的没发生**。
//! 这里用录制报文回放，不起进程；真进程那一半在 `e2e.rs`。

use std::io::Cursor;

use codex_adapter::protocol::ServerRequestPayload;
use codex_adapter::session::{drive_turn, Session, SessionEvent, TurnStatus};
use serde_json::Value;

const COMMAND_RUN: &str = include_str!("fixtures/decline-command-approval.jsonl");
const FILE_CHANGE_RUN: &str = include_str!("fixtures/decline-file-change.jsonl");

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

/// 回放一遍，边走边拒绝审批，把完整事件序列收下来。
///
/// 自己写循环而不是用 `drive_turn`，是因为后者在终态就返回了，拿不到完整序列 ——
/// 而审批要验的恰恰是「请求之后发生了什么」。
async fn collect(fixture: &str) -> Vec<SessionEvent> {
    let (session, mut events) = Session::connect(server_stream(fixture), tokio::io::sink());
    let mut seq = Vec::new();
    while let Some(event) = events.recv().await {
        if let SessionEvent::ApprovalRequested(req) = &event {
            // 不答复就卡死。
            session.answer(&req.deny()).await.expect("答复写不出去");
        }
        seq.push(event);
    }
    seq
}

/// **A3 的核心验收**：拒绝之后，那一项的结果是 `declined`，而且整轮正常收束。
///
/// 这是把 2026-09-02 探针脚本的结论（decline 后命令未执行、文件未生成）
/// 固化成常驻测试 —— 报文里逐项的 `status` 就是那个结论的证据。
#[tokio::test]
async fn declining_marks_the_item_declined_and_the_turn_still_completes() {
    for (name, fixture) in [("命令执行", COMMAND_RUN), ("文件改动", FILE_CHANGE_RUN)] {
        let (session, mut events) = Session::connect(server_stream(fixture), tokio::io::sink());

        let mut declined_items = Vec::new();
        let mut approvals = 0;
        let outcome = drive_turn(&session, &mut events, 5, |e| match e {
            SessionEvent::ApprovalRequested(_) => approvals += 1,
            SessionEvent::ItemCompleted { item_status, item_type, .. }
                if item_status.as_deref() == Some("declined") =>
            {
                declined_items.push(item_type.clone());
            }
            _ => {}
        })
        .await;

        assert!(approvals > 0, "{name}：这份录制里应当有审批请求");
        assert!(
            !declined_items.is_empty(),
            "{name}：拒绝之后没有任何一项被标成 declined —— 拒绝可能没生效"
        );
        // 拒绝不打断整轮：agent 知道被拒之后会自己收尾说明原因。
        assert!(
            matches!(outcome.status, TurnStatus::Completed(_)),
            "{name}：拒绝之后整轮没有正常收束：{:?}",
            outcome.status
        );
    }
}

/// 文件改动的审批请求**只是个指针** —— UI 必须靠 itemId 回查才知道在批什么。
#[tokio::test]
async fn file_change_approval_needs_the_item_to_be_understandable() {
    let seq = collect(FILE_CHANGE_RUN).await;

    // 先找那条审批请求。
    let approval = seq
        .iter()
        .find_map(|e| match e {
            SessionEvent::ApprovalRequested(req) => Some(req),
            _ => None,
        })
        .expect("这份录制里应当有文件改动审批");
    assert_eq!(approval.method, "item/fileChange/requestApproval");

    let ServerRequestPayload::FileChangeApproval(params) = &approval.payload else {
        panic!("没被投影成 FileChangeApproval: {:?}", approval.payload);
    };

    // 请求本身给不出任何「改了什么」—— 这正是必须回查 item 的原因。
    assert!(params.reason.is_none(), "这次录到的 reason 是 null");
    assert!(params.grant_root.is_none(), "这次录到的 grantRoot 是 null");
    assert!(!params.item_id.is_empty());

    // 回查：同一个 itemId 的 item/started 里带着路径和 diff。
    let item = seq
        .iter()
        .find_map(|e| match e {
            SessionEvent::ItemStarted { item_id, item, .. }
                if item_id.as_deref() == Some(params.item_id.as_str()) =>
            {
                Some(item)
            }
            _ => None,
        })
        .expect("按 itemId 回查不到对应的 item —— 审批 UI 会没东西可显示");

    let changes = item
        .get("changes")
        .and_then(Value::as_array)
        .expect("fileChange 的 item 应当带 changes");
    let first = &changes[0];
    assert!(first.get("path").and_then(Value::as_str).is_some(), "改动少了路径");
    assert!(
        first.get("diff").and_then(Value::as_str).is_some_and(|d| !d.is_empty()),
        "改动少了 diff —— 用户会在看不见内容的情况下点同意"
    );
}

/// `waitingOnApproval` 与 `serverRequest/resolved` 是两端保持一致的现成原语。
#[tokio::test]
async fn waiting_and_resolved_are_broadcast_so_both_ends_agree() {
    let seq = collect(COMMAND_RUN).await;

    let waiting = seq.iter().any(|e| matches!(
        e,
        SessionEvent::StatusChanged { status, .. } if status.is_waiting_on_approval()
    ));
    assert!(waiting, "没有 waitingOnApproval —— 另一端不知道这里卡在审批上");

    let approval_id = seq
        .iter()
        .find_map(|e| match e {
            SessionEvent::ApprovalRequested(req) => Some(req.id.clone()),
            _ => None,
        })
        .expect("没有审批请求");

    let resolved = seq
        .iter()
        .find_map(|e| match e {
            SessionEvent::ApprovalResolved { request_id } => Some(request_id.clone()),
            _ => None,
        })
        .expect("没有 serverRequest/resolved —— 一处响应后另一处的队列不会消失");

    assert_eq!(resolved, approval_id, "resolved 的 id 对不上审批请求");
}
