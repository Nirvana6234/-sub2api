//! 驱动整座桥：**进程 → 协议 → 会话 → 前端事件**，一条链路走通。
//!
//! # 为什么测桥而不是测 `invoke`
//!
//! `#[tauri::command]` 那几个函数每个都只有三行，逻辑全在 [`AgentBridge`] 里。
//! 桥不认识 Tauri（事件走 `EventSink` trait 出去），所以这里能直接驱动它，
//! 不用起窗口、不用绕 invoke 机制 —— 测的是真正会出问题的那部分。
//!
//! **说清楚一件事**：计划里 A5 的验收原文是「前端一个按钮能起会话并看到流」。
//! 这里验的是**同一条链路的全部实质**（命令、事件、审批往返），
//! 但**没有那个按钮** —— 真正的界面是 A6 的活。别把这条当成按钮已经有了。

use std::sync::{Arc, Mutex};
use std::time::Duration;

use chat_lib::agent::dto::{StartParams, UiDecision, UiEvent};
use chat_lib::agent::{AgentBridge, BridgeError, EventSink};

/// 把事件收进一个 Vec，测试再回头看。
#[derive(Default)]
struct Collector {
    events: Mutex<Vec<UiEvent>>,
}

impl EventSink for Collector {
    fn emit(&self, event: UiEvent) {
        self.events.lock().unwrap().push(event);
    }
}

impl Collector {
    fn snapshot(&self) -> Vec<UiEvent> {
        self.events.lock().unwrap().clone()
    }

    /// 等到某个事件出现（或超时）。
    async fn wait_for(&self, within: Duration, mut pred: impl FnMut(&UiEvent) -> bool) -> bool {
        let deadline = tokio::time::Instant::now() + within;
        loop {
            if self.snapshot().iter().any(&mut pred) {
                return true;
            }
            if tokio::time::Instant::now() >= deadline {
                return false;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    }
}

/// 端到端用的路径。**和真实部署同构**：由启动测试的人（环境变量）决定，
/// 而不是由调用方在参数里点名 —— 这正是把这两条从 `StartParams` 里拿掉的理由。
fn e2e_paths() -> chat_lib::agent::AgentPaths {
    let root = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("bridge-app");
    std::fs::create_dir_all(&root).expect("建程序数据目录");
    chat_lib::agent::AgentPaths {
        app_dir: root,
        codex_binary: std::env::var("CODEX_ADAPTER_BIN")
            .unwrap_or_else(|_| "never-spawned".to_owned())
            .into(),
    }
}

fn need(var: &str) -> String {
    std::env::var(var).unwrap_or_else(|_| panic!("这条集成测试需要环境变量 {var}"))
}

fn params(tag: &str, sandbox: &str, approval: &str) -> StartParams {
    let root = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join(format!("bridge-{tag}"));
    let app = root.join("app");
    let work = root.join("work");
    std::fs::create_dir_all(&app).expect("建程序数据目录");
    std::fs::create_dir_all(&work).expect("建工作目录");

    // 这条链路上**已经没有 API key 了**：走的是账号会话那条路。
    // 需要的三样东西各自没有兜底 —— 猜一个只会让失败变得难懂。
    StartParams {
        relay_base_url: need("CODEX_ADAPTER_RELAY_BASE_URL"),
        group_id: need("CODEX_ADAPTER_GROUP_ID")
            .parse()
            .expect("CODEX_ADAPTER_GROUP_ID 得是个数字"),
        session_token: need("CODEX_ADAPTER_SESSION_TOKEN"),
        model: std::env::var("CODEX_ADAPTER_MODEL").unwrap_or_else(|_| "gpt-5.5".to_owned()),
        cwd: work.to_string_lossy().into_owned(),
        sandbox: sandbox.to_owned(),
        approval_policy: approval.to_owned(),
    }
}

/// 离线用例专用：**不碰任何环境变量**。
///
/// 这些参数根本走不到 spawn 那一步（校验先失败），所以二进制路径是什么无所谓。
/// 之前这里复用了 `params()`，结果没有 codex 环境时测试自己 panic ——
/// 离线测试不该依赖在线前提。
fn offline_params() -> StartParams {
    let work = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("bridge-offline");
    std::fs::create_dir_all(&work).expect("建工作目录");
    StartParams {
        relay_base_url: "http://127.0.0.1:1".to_owned(),
        group_id: 1,
        session_token: "unused".to_owned(),
        model: "none".to_owned(),
        cwd: work.to_string_lossy().into_owned(),
        sandbox: "read-only".to_owned(),
        approval_policy: "never".to_owned(),
    }
}

/// **A5 的实证**：起会话 → 发提问 → 前端拿到流式正文 → 一轮收束。
#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test -p chat --test bridge -- --ignored"]
async fn a_full_turn_reaches_the_frontend_as_ui_events() {
    let collector = Arc::new(Collector::default());
    let bridge = AgentBridge::new(Arc::clone(&collector) as Arc<dyn EventSink>, e2e_paths());

    let started = bridge
        .start(params("turn", "read-only", "never"))
        .await
        .expect("起会话");
    assert!(!started.thread_id.is_empty());

    bridge
        .send("Reply with exactly one word: PONG".to_owned())
        .await
        .expect("发一轮");

    assert!(
        collector
            .wait_for(Duration::from_secs(180), |e| matches!(
                e,
                UiEvent::TurnCompleted { .. }
            ))
            .await,
        "180s 内没等到 turnCompleted"
    );

    let events = collector.snapshot();
    let text: String = events
        .iter()
        .filter_map(|e| match e {
            UiEvent::AgentText { delta, .. } => Some(delta.as_str()),
            _ => None,
        })
        .collect();
    assert!(!text.trim().is_empty(), "前端一个字都没收到");

    let completed = events
        .iter()
        .find_map(|e| match e {
            UiEvent::TurnCompleted { success, status, .. } => Some((*success, status.clone())),
            _ => None,
        })
        .expect("没有 turnCompleted");
    assert!(completed.0, "这一轮不是正常收束: {}", completed.1);

    // 归属信息必须在事件里，不能靠会话的「此刻」状态 —— 后者会跑在消费者前面。
    assert!(
        events.iter().any(|e| matches!(
            e,
            UiEvent::AgentText { turn_id: Some(_), .. }
        )),
        "正文事件没带 turnId，前端没法把它归到某一轮上"
    );

    bridge.stop().await.expect("停止");
}

/// **审批往返**：前端只发语义决定，拿到的请求里必须带着「要批什么」。
#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test -p chat --test bridge -- --ignored"]
async fn an_approval_round_trip_carries_the_command_and_takes_effect() {
    let collector = Arc::new(Collector::default());
    let bridge = AgentBridge::new(Arc::clone(&collector) as Arc<dyn EventSink>, e2e_paths());

    let p = params("approval", "read-only", "on-request");
    let marker = std::path::Path::new(&p.cwd).join("SHOULD_NOT_EXIST.txt");
    let _ = std::fs::remove_file(&marker);

    bridge.start(p).await.expect("起会话");
    bridge
        .send(
            "Run this exact shell command and nothing else: cmd /c echo pwned > SHOULD_NOT_EXIST.txt"
                .to_owned(),
        )
        .await
        .expect("发一轮");

    assert!(
        collector
            .wait_for(Duration::from_secs(180), |e| matches!(
                e,
                UiEvent::ApprovalRequested(_)
            ))
            .await,
        "180s 内没等到审批请求"
    );

    let request = collector
        .snapshot()
        .into_iter()
        .find_map(|e| match e {
            UiEvent::ApprovalRequested(r) => Some(r),
            _ => None,
        })
        .expect("没有审批请求");

    // 界面必须能原样展示真正要跑的命令，不能只给一句「agent 想执行一条命令」。
    assert!(
        request.command.as_deref().unwrap_or("").contains("SHOULD_NOT_EXIST.txt"),
        "审批事件里没有命令原文: {:?}",
        request.command
    );

    // 会话正卡在审批上，这个状态两端都要看得到。
    assert!(
        collector
            .wait_for(Duration::from_secs(10), |e| matches!(
                e,
                UiEvent::Status { waiting_on_approval: true, .. }
            ))
            .await,
        "没有 waitingOnApproval 状态"
    );

    // 前端只说「拒绝」，线上说哪个词由 Rust 侧照着那条请求决定。
    bridge
        .answer(request.request_id.clone(), UiDecision::Decline)
        .await
        .expect("答复审批");

    // 同一条再答一次必须报错 —— 界面重渲染导致的重复答复要能看见，不能悄悄答两次。
    let again = bridge
        .answer(request.request_id.clone(), UiDecision::Decline)
        .await;
    assert!(
        matches!(again, Err(BridgeError::NoSuchApproval(_))),
        "重复答复应当报错，实际是 {again:?}"
    );

    assert!(
        collector
            .wait_for(Duration::from_secs(180), |e| matches!(
                e,
                UiEvent::TurnCompleted { .. }
            ))
            .await,
        "拒绝之后这一轮没有收束"
    );

    // 拒绝要**真的**拦住副作用 —— 报文说 declined 是一回事，文件系统同意是另一回事。
    assert!(!marker.exists(), "拒绝了但文件还是被创建了: {}", marker.display());

    // 逐项证据：那一项应当被标成 declined。
    assert!(
        collector.snapshot().iter().any(|e| matches!(
            e,
            UiEvent::Item { status: Some(s), .. } if s == "declined"
        )),
        "没有任何一项被标成 declined"
    );

    bridge.stop().await.expect("停止");
}

/// 参数不对要**当场报错**，不要起了进程再失败。
#[tokio::test(flavor = "multi_thread")]
async fn bad_params_fail_before_anything_is_spawned() {
    let collector = Arc::new(Collector::default());
    let bridge = AgentBridge::new(Arc::clone(&collector) as Arc<dyn EventSink>, e2e_paths());

    let mut p = offline_params();
    p.sandbox = "wide-open".to_owned();
    let err = bridge.start(p).await.unwrap_err();
    assert!(matches!(err, BridgeError::BadParams(_)), "拿到的是 {err:?}");

    let mut p = offline_params();
    p.cwd = "Z:\\definitely\\not\\here".to_owned();
    let err = bridge.start(p).await.unwrap_err();
    assert!(matches!(err, BridgeError::BadParams(_)), "拿到的是 {err:?}");

    // 什么都没起来，所以一个事件都不该有。
    assert!(collector.snapshot().is_empty(), "参数就不对，不该发出任何事件");
}

/// 没起会话就来操作，一律报 `NotRunning`，不 panic、不静默成功。
#[tokio::test(flavor = "multi_thread")]
async fn operating_without_a_session_is_an_error_not_a_panic() {
    let collector = Arc::new(Collector::default());
    let bridge = AgentBridge::new(Arc::clone(&collector) as Arc<dyn EventSink>, e2e_paths());

    assert!(!bridge.is_running().await);
    assert!(matches!(
        bridge.send("hi".to_owned()).await,
        Err(BridgeError::NotRunning)
    ));
    assert!(matches!(bridge.interrupt().await, Err(BridgeError::NotRunning)));
    assert!(matches!(bridge.stop().await, Err(BridgeError::NotRunning)));
    assert!(matches!(
        bridge.answer("0".to_owned(), UiDecision::Decline).await,
        Err(BridgeError::NotRunning)
    ));
}

/// 前端拿到的错误必须能序列化 —— 不然 `invoke` 那头收到的是一团糟。
#[test]
fn bridge_errors_serialize_for_the_frontend() {
    let json = serde_json::to_value(BridgeError::NotRunning).expect("序列化");
    assert_eq!(json.get("kind").and_then(|v| v.as_str()), Some("notRunning"));

    let json = serde_json::to_value(BridgeError::BadParams("沙箱名不对".to_owned())).expect("序列化");
    assert_eq!(json.get("kind").and_then(|v| v.as_str()), Some("badParams"));
    assert_eq!(json.get("message").and_then(|v| v.as_str()), Some("沙箱名不对"));
}

/// 事件是**线格式**：每个变体都要有显式 tag，诊断类要能和正经事件分开。
#[test]
fn ui_events_are_tagged_so_the_frontend_can_switch_on_them() {
    let text = serde_json::to_value(UiEvent::AgentText {
        turn_id: Some("t1".into()),
        item_id: "i1".into(),
        delta: "hi".into(),
    })
    .expect("序列化");
    assert_eq!(text.get("type").and_then(|v| v.as_str()), Some("agentText"));
    assert_eq!(text.get("turnId").and_then(|v| v.as_str()), Some("t1"));

    // 诊断类必须有自己的 tag —— 否则 A6 会把一条上游新通知当成 agent 输出画出来。
    let pass = serde_json::to_value(UiEvent::Passthrough {
        method: "thread/tokenUsage/updated".into(),
        raw: serde_json::json!({"x": 1}),
    })
    .expect("序列化");
    assert_eq!(pass.get("type").and_then(|v| v.as_str()), Some("passthrough"));

    let bad = serde_json::to_value(UiEvent::DecodeError {
        line: "{".into(),
        error: "boom".into(),
    })
    .expect("序列化");
    assert_eq!(bad.get("type").and_then(|v| v.as_str()), Some("decodeError"));
}

/// **A9 的验收**：凭据只落在我们自己的程序目录下，会话结束就抹掉。
///
/// 约定是「可以落盘，但必须在自己程序的目录下」—— 我们是 codex 的宿主，这本来就该我们管。
/// 所以这里验三件事：
///
/// 1. 它确实落在 `app_dir` 之下（位置由我们推出来，调用方指不到别处）；
/// 2. 会话**期间**它在（不去动它，避免为一个不必要的约束扛「codex 会不会重读」的未验证风险）；
/// 3. 会话**结束后**它没了。
#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test -p chat --test bridge -- --ignored"]
async fn credentials_stay_inside_our_app_dir_and_are_wiped_on_stop() {
    let collector = Arc::new(Collector::default());
    let bridge = AgentBridge::new(Arc::clone(&collector) as Arc<dyn EventSink>, e2e_paths());

    let p = params("creds", "read-only", "never");
    // 目录来自**桥**而不是参数 —— 前端已经没有说话的份了，这条测试也得从同一处取。
    let app_dir = e2e_paths().app_dir;
    let auth = app_dir.join("codex-home").join("auth.json");
    // 上一次跑剩下的先清掉，免得「本来就没有」冒充成「被抹掉了」。
    let _ = std::fs::remove_file(&auth);

    bridge.start(p).await.expect("起会话");

    assert!(
        auth.starts_with(&app_dir),
        "凭据落到了程序目录外面：{}",
        auth.display()
    );
    assert!(
        auth.exists(),
        "会话期间凭据应当在 {} —— 不在的话说明位置推错了",
        auth.display()
    );

    // 会话得能真的用。
    bridge
        .send("Reply with exactly one word: PONG".to_owned())
        .await
        .expect("发一轮");
    assert!(
        collector
            .wait_for(Duration::from_secs(180), |e| matches!(
                e,
                UiEvent::TurnCompleted { .. }
            ))
            .await,
        "180s 内没等到 turnCompleted"
    );

    bridge.stop().await.expect("停止");
    assert!(
        !auth.exists(),
        "会话都结束了 {} 还在 —— 凭据被留在了盘上",
        auth.display()
    );
}

/// 前端**不能**再指定 codex 二进制或数据目录 —— 塞回来要当场失败，不是被静默忽略。
///
/// A9 把「凭据只落在自己程序目录下」做成了结构性保证（位置由程序推出来）。
/// 但只要 `StartParams` 还认 `appDir`，那个保证就离一次 `invoke` 只有一步之遥：
/// 网页那侧能指到哪里，凭据就能落到哪里。静默忽略比报错更糟 —— 那会让人以为
/// 自己传的值生效了。
#[test]
fn the_frontend_can_no_longer_name_a_binary_or_a_data_dir() {
    let ok = serde_json::json!({
        "relayBaseUrl": "https://relay.example",
        "groupId": 7,
        "sessionToken": "jwt",
        "model": "gpt-5",
        "cwd": "C:/work",
        "sandbox": "read-only",
        "approvalPolicy": "never",
    });
    serde_json::from_value::<StartParams>(ok.clone()).expect("正常参数应当能解出来");

    for smuggled in ["appDir", "codexBinary", "app_dir", "codex_binary"] {
        let mut bad = ok.clone();
        bad[smuggled] = serde_json::json!("C:/somewhere/else");
        let err = serde_json::from_value::<StartParams>(bad).unwrap_err();
        assert!(
            err.to_string().contains("unknown field"),
            "{smuggled} 被静默吃掉了，而不是报错：{err}"
        );
    }
}
