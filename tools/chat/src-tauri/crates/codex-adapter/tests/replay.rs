//! 拿**真实录制的报文**回放整条收发链路，不起进程。
//!
//! 这些 fixture 是 `scripts/capture-fixtures.py` 对着真 codex 进程 + 真中转站录下来的。
//! 用录制报文而不是手写报文，是因为手写的只能测出我们对 schema 的理解，测不出线上到底发什么。

use std::collections::HashMap;

use codex_adapter::protocol::{NotificationPayload, ServerRequestPayload, WorkspaceDir};
use codex_adapter::transport::{new_pending, pump, Incoming};
use codex_adapter::{decode_line, AdapterError, ApprovalDecision, ClientRequest, RpcError};
use serde_json::Value;
use tokio::sync::mpsc;

/// 造一条只有方法名的服务端请求 —— 用来检查答复的形状，不需要真实参数。
fn fake_request(method: &str) -> codex_adapter::ServerRequest {
    codex_adapter::ServerRequest::project(
        codex_adapter::RequestId::Num(7),
        method.to_owned(),
        serde_json::json!({}),
    )
}

fn answer_frame(a: &codex_adapter::Answer) -> Value {
    a.to_frame()
}

fn answer_value(a: &codex_adapter::Answer) -> Value {
    a.to_frame().get("result").cloned().expect("这条答复不是 result 形状")
}

const FIXTURE: &str = include_str!("fixtures/decline-command-approval.jsonl");
const BAD_CREDENTIALS: &str = include_str!("fixtures/bad-credentials.jsonl");
const INVALID_CWD: &str = include_str!("fixtures/invalid-cwd.jsonl");
const FILE_CHANGE: &str = include_str!("fixtures/decline-file-change.jsonl");

/// 一条录制记录：`{t_ms, dir, msg}`，stderr 的那些没有 `msg`。
fn records_of(fixture: &str) -> Vec<(String, Value)> {
    fixture
        .lines()
        .filter(|l| !l.trim().is_empty())
        .filter_map(|l| {
            let rec: Value = serde_json::from_str(l).expect("fixture 行不是合法 JSON");
            let dir = rec
                .get("dir")
                .and_then(Value::as_str)
                .unwrap_or_else(|| panic!("fixture 行缺 dir: {l}"))
                .to_owned();
            // stderr 的记录没有 msg，那是正常的；其余缺 msg 就是 fixture 坏了。
            // 这里刻意不静默跳过 —— 一份悄悄变小的语料是这些测试唯一报不出来的故障。
            match rec.get("msg") {
                Some(msg) => Some((dir, msg.clone())),
                None => {
                    assert_eq!(dir, "stderr", "只有 stderr 记录可以没有 msg: {l}");
                    None
                }
            }
        })
        .collect()
}

fn records() -> Vec<(String, Value)> {
    records_of(FIXTURE)
}

fn server_lines(fixture: &str) -> String {
    records_of(fixture)
        .into_iter()
        .filter(|(dir, _)| dir == "server->client")
        .map(|(_, msg)| serde_json::to_string(&msg).unwrap())
        .collect::<Vec<_>>()
        .join("\n")
}

async fn drain_fixture() -> Vec<Incoming> {
    drain(FIXTURE).await
}

async fn drain(fixture: &str) -> Vec<Incoming> {
    let data = server_lines(fixture);
    let (tx, mut rx) = mpsc::channel(256);
    let pending = new_pending();

    let pump_task = tokio::spawn(async move { pump(data.as_bytes(), pending, tx).await });

    let mut got = Vec::new();
    while let Some(item) = rx.recv().await {
        got.push(item);
    }
    pump_task.await.unwrap().expect("回放不该以连接级错误结束");
    got
}

/// **这条本来会在第一次真实使用时才发现**：0.153.0 的 codex 每轮之后都会推
/// `thread/tokenUsage/updated`、`account/rateLimits/updated` 两条通知，而我们的 fixture 里
/// 明明就录到了 —— `replays_the_whole_transcript_without_decode_errors` 只检查
/// "解不解得开"，不检查"认不认识"，于是这两条方法名一直悠悠落在 `Other`
/// 里，用户第一次真实对话就在界面上看到一片"未识别的上游通知"。
///
/// 这条把"认不认识"也钉住：**已经录到的每一条通知都必须有一个明确的归宿** ——
/// 要么投影成具体的 `NotificationPayload` 变体，要么落进 `NotificationPayload::Ignored`
/// 那个白名单，不许再悠悠地变成 `Other`。
#[tokio::test]
async fn every_notification_we_have_ever_recorded_has_a_known_home() {
    for fixture in [FIXTURE, BAD_CREDENTIALS, INVALID_CWD, FILE_CHANGE] {
        for (dir, msg) in records_of(fixture) {
            if dir != "server->client" || msg.get("id").is_some() {
                continue;
            }
            let Some(method) = msg.get("method").and_then(Value::as_str) else {
                continue;
            };
            let note = codex_adapter::protocol::Notification::project(
                method.to_owned(),
                msg.get("params").cloned().unwrap_or(Value::Null),
            );
            assert!(
                !matches!(note.payload, NotificationPayload::Other),
                "{method} 落进了 Other——要么补一个投影，要么加进 Ignored 白名单"
            );
        }
    }
}

#[tokio::test]
async fn replays_the_whole_transcript_without_decode_errors() {
    let incoming = drain_fixture().await;

    // 任何一行解不开都会变成 DecodeError —— 那等于协议漂了。
    let decode_errors: Vec<_> = incoming
        .iter()
        .filter_map(|i| match i {
            Incoming::DecodeError { line, error } => Some(format!("{error}: {line}")),
            _ => None,
        })
        .collect();
    assert!(decode_errors.is_empty(), "有行解不开: {decode_errors:?}");
    assert!(incoming.len() > 20, "回放出来的消息太少: {}", incoming.len());
}

#[tokio::test]
async fn projects_the_events_the_ui_actually_needs() {
    let incoming = drain_fixture().await;

    let mut thread_id = None;
    let mut agent_text = String::new();
    let mut saw_waiting_on_approval = false;
    let mut turn_status = None;
    let mut item_types = Vec::new();

    for item in &incoming {
        let Incoming::Notification(note) = item else { continue };
        match &note.payload {
            NotificationPayload::ThreadStarted { thread_id: id } => thread_id = Some(id.clone()),
            NotificationPayload::AgentMessageDelta { delta, .. } => agent_text.push_str(delta),
            NotificationPayload::ThreadStatusChanged { status, .. } => {
                if status.is_waiting_on_approval() {
                    saw_waiting_on_approval = true;
                }
            }
            NotificationPayload::TurnCompleted { status, .. } => turn_status = Some(status.clone()),
            NotificationPayload::ItemStarted { item_type, .. } => item_types.push(item_type.clone()),
            _ => {}
        }
    }

    assert!(thread_id.is_some(), "没投影出 threadId");
    assert!(!agent_text.is_empty(), "assistant 正文一个字都没拼出来");
    assert_eq!(turn_status.as_deref(), Some("completed"), "turn 没有正常收束");

    // 这条是 V-17 的核心：拒绝审批期间，会话状态必须能让两端都看出「正卡在审批上」。
    assert!(saw_waiting_on_approval, "没看到 waitingOnApproval 状态");

    for expected in ["userMessage", "agentMessage", "commandExecution", "reasoning"] {
        assert!(item_types.iter().any(|t| t == expected), "没见到 item 类型 {expected}");
    }
}

#[tokio::test]
async fn surfaces_the_command_approval_with_the_full_command() {
    let incoming = drain_fixture().await;

    let approvals: Vec<_> = incoming
        .iter()
        .filter_map(|i| match i {
            Incoming::Request(req) => Some(req),
            _ => None,
        })
        .collect();
    assert_eq!(approvals.len(), 1, "应当正好一次审批请求");

    let req = approvals[0];
    assert_eq!(req.method, "item/commandExecution/requestApproval");

    let ServerRequestPayload::CommandApproval(params) = &req.payload else {
        panic!("审批请求没被投影成 CommandApproval: {:?}", req.payload);
    };

    // UI 必须能原样展示真正要跑的那条命令，不能只给摘要。
    assert!(params.command.contains("SHOULD_NOT_EXIST.txt"), "命令原文丢了: {}", params.command);
    assert!(!params.cwd.is_empty());
    assert!(params.reason.is_some(), "少了给用户看的理由");

    // availableDecisions 是 UI 提示，不是合法值全集：这次录到的里面就没有 decline，
    // 但 schema 里 decline 一直合法，我们发过去也确实生效了。
    assert!(!params.available_decisions.is_empty());
}

#[tokio::test]
async fn resolved_broadcast_carries_the_request_id() {
    let incoming = drain_fixture().await;

    let resolved: Vec<_> = incoming
        .iter()
        .filter_map(|i| match i {
            Incoming::Notification(n) => match &n.payload {
                NotificationPayload::ServerRequestResolved { request_id, .. } => Some(request_id.clone()),
                _ => None,
            },
            _ => None,
        })
        .collect();

    // 「一处响应、另一处队列消失」靠的就是它 —— PC 与手机不必自己发明一套机制。
    assert_eq!(resolved.len(), 1, "应当广播一次 serverRequest/resolved");

    let approval_id = incoming
        .iter()
        .find_map(|i| match i {
            Incoming::Request(req) => Some(req.id.clone()),
            _ => None,
        })
        .expect("没有审批请求");
    assert_eq!(resolved[0], approval_id, "resolved 的 requestId 对不上审批请求的 id");
}

#[tokio::test]
async fn unknown_notifications_survive_with_their_raw_payload() {
    // **不能再从 fixture 里现捞一个 `Other` 的例子了**：这条测试原来就是这么写的
    // （旧注释原话点名 tokenUsage/rateLimits/remoteControl 当例子），而那几个
    // 后来全被证明是我们该认识、只是没认的方法——`every_notification_we_have_ever_recorded_has_a_known_home`
    // 那道闸把它们转正之后，这份录制里已经一个 `Other` 都不剩，这条测试的前提
    // 本身就是错的。改成直接造一个**保证不存在**的方法名，测的是"没投影的
    // 东西会不会被丢掉"这个机制本身，不依赖某份录制恰好还没被我们追上。
    let note = codex_adapter::protocol::Notification::project(
        "definitely/not/a/real/method".to_owned(),
        serde_json::json!({ "some": "payload" }),
    );
    assert!(matches!(note.payload, NotificationPayload::Other));
    // 没投影不等于丢掉——原始 params 必须还在，本机链路要能显示上游新事件。
    assert_eq!(note.raw, serde_json::json!({ "some": "payload" }));
}

/// 我们拼的请求参数，必须和当初真的发出去、并且被服务端接受了的那些字节一致。
#[tokio::test]
async fn client_request_params_match_what_was_actually_sent() {
    let sent: HashMap<String, Value> = records()
        .into_iter()
        .filter(|(dir, _)| dir == "client->server")
        .filter_map(|(_, msg)| {
            let method = msg.get("method")?.as_str()?.to_owned();
            Some((method, msg.get("params").cloned().unwrap_or(Value::Null)))
        })
        .collect();

    let recorded_thread_id = sent
        .get("turn/start")
        .and_then(|p| p.get("threadId"))
        .and_then(Value::as_str)
        .expect("录制里没有 turn/start 的 threadId")
        .to_owned();
    let recorded_text = sent
        .get("turn/start")
        .and_then(|p| p.get("input"))
        .and_then(|v| v.get(0))
        .and_then(|v| v.get("text"))
        .and_then(Value::as_str)
        .expect("录制里没有 turn/start 的 text")
        .to_owned();
    let recorded_cwd = sent
        .get("thread/start")
        .and_then(|p| p.get("cwd"))
        .and_then(Value::as_str)
        .expect("录制里没有 thread/start 的 cwd")
        .to_owned();

    let cases: Vec<(&str, ClientRequest)> = vec![
        (
            "account/login/start",
            // 录制时被脱敏成了这个字面量，比对的是形状不是密钥。
            ClientRequest::LoginApiKey { api_key: "<redacted>".to_owned() },
        ),
        (
            "thread/start",
            ClientRequest::ThreadStart {
                // WorkspaceDir 会真的去看这个目录在不在（codex 自己不看，所以我们看）。
                // 录制用的临时目录早没了，这里把它建回来，好让比对的是真实发出去的那串字节。
                cwd: {
                    std::fs::create_dir_all(&recorded_cwd).expect("重建录制用的工作目录");
                    WorkspaceDir::new(&recorded_cwd).expect("重建后应当合法")
                },
                sandbox: codex_adapter::SandboxMode::ReadOnly,
                approval_policy: codex_adapter::ApprovalPolicy::OnRequest,
            },
        ),
        (
            "turn/start",
            // 录制时还没有 model/effort 覆盖这回事，所以两个都传 None——
            // 这样比对的仍然是「和当初真实发出去的字节一致」。
            ClientRequest::TurnStart {
                thread_id: recorded_thread_id,
                text: recorded_text,
                model: None,
                effort: None,
            },
        ),
    ];

    for (method, req) in cases {
        assert_eq!(req.method(), method);
        let recorded = sent.get(method).unwrap_or_else(|| panic!("录制里没有 {method}"));
        assert_eq!(&req.params(), recorded, "{method} 的参数和真实发出去的不一致");
    }
}

/// 上游 401 走的是通知，不是响应 —— 这条测试盯的就是这个事实。
///
/// 只盯着请求响应做错误处理会把整类上游错误漏掉：录制显示 `turn/start` **返回成功**，
/// 401 随后以 `error` 通知到达。
#[tokio::test]
async fn upstream_auth_failure_arrives_as_a_notification_not_a_response() {
    let incoming = drain(BAD_CREDENTIALS).await;

    let errors: Vec<_> = incoming
        .iter()
        .filter_map(|i| match i {
            Incoming::Notification(n) => match &n.payload {
                NotificationPayload::Error(e) => Some(e),
                _ => None,
            },
            _ => None,
        })
        .collect();

    assert!(!errors.is_empty(), "没投影出任何 error 通知");

    let first = errors[0];
    // 文案在 params.error.message，不在 params.message —— 读错层级就会拿到整个 JSON。
    assert!(!first.message.is_empty());
    assert!(!first.message.starts_with('{'), "message 读错层级了: {}", first.message);

    // 结构化状态码才是可靠信号，不要去匹配文案。
    assert_eq!(first.http_status, Some(401), "没挖出 httpStatusCode");
    assert!(first.is_auth_failure());
    assert!(first.thread_id.is_some());
    assert!(
        first.details.as_deref().unwrap_or("").contains("INVALID_API_KEY"),
        "additionalDetails 丢了真正的原因"
    );

    // codex 自己会重试，所以这几条**不是终态** —— UI 该显示「正在重试」而不是「登录失效」。
    assert!(first.will_retry, "willRetry 丢了，会把重连中误报成致命错误");
}

/// codex **不校验 cwd**：传一个不存在的目录，会话照样建起来。
///
/// 所以目录合法性只能我们自己在起会话前验（A8）。这条测试是那个结论的锚。
#[tokio::test]
async fn codex_does_not_validate_cwd_so_the_host_must() {
    let sent_cwd = records_of(INVALID_CWD)
        .into_iter()
        .find(|(dir, msg)| dir == "client->server" && msg.get("method") == Some(&Value::String("thread/start".into())))
        .and_then(|(_, msg)| msg.get("params")?.get("cwd")?.as_str().map(str::to_owned))
        .expect("录制里没有 thread/start");
    assert!(sent_cwd.contains("no"), "这条 fixture 本来就该发一个不存在的目录");

    let incoming = drain(INVALID_CWD).await;

    let started = incoming.iter().any(|i| matches!(
        i,
        Incoming::Notification(n) if matches!(n.payload, NotificationPayload::ThreadStarted { .. })
    ));
    let errored = incoming.iter().any(|i| matches!(
        i,
        Incoming::Notification(n) if matches!(n.payload, NotificationPayload::Error(_))
    ));

    assert!(started, "会话居然没起来 —— 上游行为变了，A8 的前提要重新评估");
    assert!(!errored, "上游开始校验 cwd 了？那是好事，但要回来改 A8 的假设");
}

#[test]
fn decode_line_reports_garbage_as_protocol_drift() {
    let err = decode_line("this is not json").unwrap_err();
    assert!(matches!(err, AdapterError::Protocol(_)), "拿到的是 {err:?}");

    let err = decode_line(r#"{"jsonrpc":"2.0"}"#).unwrap_err();
    assert!(matches!(err, AdapterError::Protocol(_)), "拿到的是 {err:?}");
}

#[test]
fn error_classification_separates_the_cases_that_need_different_handling() {
    let auth = AdapterError::classify(RpcError {
        code: -32603,
        message: "request failed: 401 Unauthorized".to_owned(),
        data: None,
    });
    assert!(matches!(auth, AdapterError::Auth(_)), "拿到的是 {auth:?}");

    let drift = AdapterError::classify(RpcError {
        code: -32601,
        message: "Method not found".to_owned(),
        data: None,
    });
    assert!(matches!(drift, AdapterError::Protocol(_)), "拿到的是 {drift:?}");

    let upstream = AdapterError::classify(RpcError {
        code: -32603,
        message: "model returned 500".to_owned(),
        data: None,
    });
    assert!(matches!(upstream, AdapterError::Upstream(_)), "拿到的是 {upstream:?}");
    assert!(!upstream.is_fatal(), "单次上游错误不该被当成连接已死");
}

/// codex 收下不存在的目录不吭声，所以这个不变量必须由我们守住。
#[test]
fn workspace_dir_refuses_what_codex_would_have_accepted() {
    let missing = std::env::temp_dir().join("cofly-no-such-dir-ever");
    let err = WorkspaceDir::new(&missing).unwrap_err();
    assert!(matches!(err, AdapterError::InvalidPath(_)), "拿到的是 {err:?}");

    // 文件不是目录。
    let file = std::env::temp_dir().join("cofly-workspace-dir-probe.txt");
    std::fs::write(&file, b"x").expect("写探针文件");
    let err = WorkspaceDir::new(&file).unwrap_err();
    assert!(matches!(err, AdapterError::InvalidPath(_)), "拿到的是 {err:?}");
    let _ = std::fs::remove_file(&file);

    // 真目录放行。
    let ok = WorkspaceDir::new(std::env::temp_dir()).expect("临时目录应当合法");
    assert!(!ok.as_str().is_empty());
}

/// 同一个「拒绝」，对不同方法要说不同的词。
///
/// v2 的 `item/*/requestApproval` 说 `decline`，旧的 `execCommandApproval` /
/// `applyPatchApproval` 说 `denied`。发错词会被拒 —— 所以别自己拼 `{"decision":…}`。
#[test]
fn the_same_decision_serializes_differently_per_method() {
    let v2 = fake_request("item/commandExecution/requestApproval");
    let legacy = fake_request("execCommandApproval");

    assert_eq!(answer_value(&v2.approve(ApprovalDecision::Decline).unwrap()),
               serde_json::json!({ "decision": "decline" }));
    assert_eq!(answer_value(&legacy.approve(ApprovalDecision::Decline).unwrap()),
               serde_json::json!({ "decision": "denied" }));

    // decline = 拒绝但这一轮继续；cancel = 拒绝并立即中断整轮。UI 要给两个按钮。
    assert_eq!(answer_value(&v2.approve(ApprovalDecision::Cancel).unwrap()),
               serde_json::json!({ "decision": "cancel" }));
    assert_eq!(answer_value(&legacy.approve(ApprovalDecision::Cancel).unwrap()),
               serde_json::json!({ "decision": "abort" }));
    assert_eq!(answer_value(&v2.approve(ApprovalDecision::AcceptForSession).unwrap()),
               serde_json::json!({ "decision": "acceptForSession" }));
    assert_eq!(answer_value(&legacy.approve(ApprovalDecision::AcceptForSession).unwrap()),
               serde_json::json!({ "decision": "approved_for_session" }));
}

/// `deny()` 必须对**每一种**服务端请求都给得出答案 —— 漏一个，那一轮就永远卡着。
#[test]
fn deny_is_total_and_shaped_per_method() {
    let cases: Vec<(&str, serde_json::Value)> = vec![
        ("item/commandExecution/requestApproval", serde_json::json!({ "decision": "decline" })),
        ("item/fileChange/requestApproval", serde_json::json!({ "decision": "decline" })),
        // 旧方法是另一套词汇。
        ("execCommandApproval", serde_json::json!({ "decision": "denied" })),
        ("applyPatchApproval", serde_json::json!({ "decision": "denied" })),
        // 权限这一类没有「拒绝」这个值：拒绝 = 授一个空档案。
        ("item/permissions/requestApproval", serde_json::json!({ "permissions": {}, "scope": "turn" })),
        ("item/tool/call", serde_json::json!({ "success": false, "contentItems": [] })),
        ("item/tool/requestUserInput", serde_json::json!({ "answers": [] })),
        ("mcpServer/elicitation/request", serde_json::json!({ "action": "decline" })),
    ];

    for (method, expected) in cases {
        let req = fake_request(method);
        assert_eq!(answer_value(&req.deny()), expected, "{method} 的拒绝形状不对");
    }

    // 这两个要的是我们造不出来的凭证。老实回错误，不要编一个假 token 送上去。
    for method in ["attestation/generate", "account/chatgptAuthTokens/refresh", "some/unknown/method"] {
        let req = fake_request(method);
        let frame = answer_frame(&req.deny());
        assert!(frame.get("error").is_some(), "{method} 应当回 JSON-RPC 错误，实际是 {frame}");
        assert!(frame.get("result").is_none(), "{method} 不该编出一个假 result");
    }
}

/// 拿甲类请求的响应去答乙类请求，在类型上就该走不通。
#[test]
fn you_cannot_answer_one_approval_with_anothers_shape() {
    let permissions = fake_request("item/permissions/requestApproval");
    let err = permissions.approve(ApprovalDecision::Decline).unwrap_err();
    assert!(err.to_string().contains("不是是/否型"), "{err}");

    let command = fake_request("item/commandExecution/requestApproval");
    let err = command
        .grant_permissions(serde_json::json!({}), codex_adapter::GrantScope::Turn)
        .unwrap_err();
    assert!(err.to_string().contains("不是权限申请"), "{err}");
}
