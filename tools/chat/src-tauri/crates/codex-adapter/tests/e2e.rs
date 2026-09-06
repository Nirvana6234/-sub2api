//! 对**真 codex 进程**跑通一轮 —— A2 的验收就在这里。
//!
//! # 为什么全部 `#[ignore]`
//!
//! 这些测试要一个真的 codex 二进制和一个真的中转站，还会消耗 token。挂 `#[ignore]`
//! 之后，普通 `cargo test` 会在结果里明明白白写着 `N ignored` ——
//! **不会出现「一个真进程测试都没跑，但全绿」这种最坏情况**。
//!
//! 要真跑：
//!
//! ```text
//! set CODEX_ADAPTER_BIN=C:\...\codex.exe
//! set CODEX_ADAPTER_BASE_URL=http://...
//! cargo test -p codex-adapter --test e2e -- --ignored --nocapture
//! ```
//!
//! 环境变量缺了就直接 panic 说清楚缺哪个 —— 既然是显式点名要跑的，
//! 静默跳过就是骗人。
//!
//! API key 默认从 `~/.codex/auth.json` 读，也可以用 `CODEX_ADAPTER_API_KEY` 覆盖。
//! 全程用**私有 `CODEX_HOME`**，用户自己的 `~/.codex` 不读不写。

use std::path::PathBuf;
use std::process::Stdio;
use std::time::Duration;

use codex_adapter::session::{drive_turn, Session, SessionEvent, TurnStatus};
use codex_adapter::{ApprovalPolicy, SandboxMode, WorkspaceDir};
use tokio::process::{Child, Command};

struct Env {
    bin: String,
    base_url: String,
    model: String,
    api_key: String,
}

fn need(var: &str) -> String {
    std::env::var(var).unwrap_or_else(|_| {
        panic!("端到端测试需要环境变量 {var}（见本文件顶部的说明）")
    })
}

fn env() -> Env {
    let api_key = std::env::var("CODEX_ADAPTER_API_KEY").ok().unwrap_or_else(|| {
        let auth = PathBuf::from(std::env::var("USERPROFILE").or_else(|_| std::env::var("HOME")).expect("找不到 home"))
            .join(".codex")
            .join("auth.json");
        let text = std::fs::read_to_string(&auth)
            .unwrap_or_else(|e| panic!("读不到 {}: {e}；或者用 CODEX_ADAPTER_API_KEY 指定", auth.display()));
        let json: serde_json::Value = serde_json::from_str(&text).expect("auth.json 不是合法 JSON");
        json.get("OPENAI_API_KEY")
            .and_then(|v| v.as_str())
            .expect("auth.json 里没有 OPENAI_API_KEY")
            .to_owned()
    });

    Env {
        bin: need("CODEX_ADAPTER_BIN"),
        base_url: need("CODEX_ADAPTER_BASE_URL"),
        model: std::env::var("CODEX_ADAPTER_MODEL").unwrap_or_else(|_| "gpt-5.5".to_owned()),
        api_key,
    }
}

/// 起一个 app-server，配上私有 CODEX_HOME 和一个干净的工作目录。
///
/// `CODEX_HOME` 放在 `target/` 下而不是系统 temp —— codex 在 temp 下会拒绝建
/// PATH 别名并告警。
fn spawn(env: &Env, tag: &str) -> (Child, WorkspaceDir) {
    let root = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join(format!("e2e-{tag}"));
    let codex_home = root.join("home");
    let workdir = root.join("work");
    std::fs::create_dir_all(&codex_home).expect("建 CODEX_HOME");
    std::fs::create_dir_all(&workdir).expect("建工作目录");
    std::fs::write(workdir.join("README.md"), b"e2e workspace\n").expect("写探针文件");

    let mut cmd = Command::new(&env.bin);
    cmd.arg("app-server")
        .args(["-c", "model_provider=\"custom\""])
        .args(["-c", "model_providers.custom.name=\"custom\""])
        .args(["-c", "model_providers.custom.wire_api=\"responses\""])
        .args(["-c", "model_providers.custom.requires_openai_auth=true"])
        .args(["-c", &format!("model_providers.custom.base_url=\"{}\"", env.base_url)])
        .args(["-c", &format!("model=\"{}\"", env.model)])
        .env("CODEX_HOME", &codex_home)
        .env_remove("OPENAI_API_KEY")
        .env_remove("CODEX_API_KEY")
        .current_dir(&workdir)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .kill_on_drop(true);

    let child = cmd.spawn().unwrap_or_else(|e| panic!("起不来 {}: {e}", env.bin));
    let cwd = WorkspaceDir::new(&workdir).expect("工作目录刚建的，应当合法");
    (child, cwd)
}

fn connect(child: &mut Child) -> (Session, tokio::sync::mpsc::Receiver<SessionEvent>) {
    let stdout = child.stdout.take().expect("拿不到 stdout");
    let stdin = child.stdin.take().expect("拿不到 stdin");
    Session::connect(stdout, stdin)
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test --test e2e -- --ignored"]
async fn runs_a_real_turn_and_gets_assistant_text() {
    let env = env();
    let (mut child, cwd) = spawn(&env, "turn");
    let (session, mut events) = connect(&mut child);

    session
        .handshake("cofly-workbench", "0.1.0", &env.api_key)
        .await
        .expect("握手 + 登录");

    let thread_id = session
        .start_thread(cwd, SandboxMode::ReadOnly, ApprovalPolicy::Never)
        .await
        .expect("起会话");
    assert!(!thread_id.is_empty());

    session
        .send_turn(&thread_id, "Reply with exactly one word: PONG", None, None)
        .await
        .expect("发一轮");

    let outcome = tokio::time::timeout(
        Duration::from_secs(180),
        drive_turn(&session, &mut events, 5, |e| {
            if let SessionEvent::AgentText { delta, .. } = e {
                print!("{delta}");
            }
        }),
    )
    .await
    .expect("整轮超时（180s）");

    println!("\n--- outcome: {:?} / {} 个事件", outcome.status, outcome.events);
    assert_eq!(
        outcome.status,
        TurnStatus::Completed("completed".to_owned()),
        "turn 没有正常收束"
    );
    assert!(!outcome.text.trim().is_empty(), "没拿到 assistant 正文");

    let _ = child.kill().await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test --test e2e -- --ignored"]
async fn resumes_a_thread_and_keeps_talking() {
    let env = env();
    let (mut child, cwd) = spawn(&env, "resume");
    let (session, mut events) = connect(&mut child);

    session
        .handshake("cofly-workbench", "0.1.0", &env.api_key)
        .await
        .expect("握手 + 登录");
    let thread_id = session
        .start_thread(cwd, SandboxMode::ReadOnly, ApprovalPolicy::Never)
        .await
        .expect("起会话");

    session
        .send_turn(&thread_id, "Remember the number 41. Reply OK.", None, None)
        .await
        .expect("第一轮");
    let first = tokio::time::timeout(
        Duration::from_secs(180),
        drive_turn(&session, &mut events, 5, |_| {}),
    )
    .await
    .expect("第一轮超时");
    assert_eq!(first.status, TurnStatus::Completed("completed".to_owned()));

    // 同一条连接上把会话续起来，再问一轮 —— 上下文得还在。
    let resumed = session.resume_thread(&thread_id).await.expect("续跑会话");
    assert_eq!(resumed, thread_id);

    session
        .send_turn(
            &resumed,
            "What number did I ask you to remember? Reply with just the number.",
            None,
            None,
        )
        .await
        .expect("第二轮");
    let second = tokio::time::timeout(
        Duration::from_secs(180),
        drive_turn(&session, &mut events, 5, |_| {}),
    )
    .await
    .expect("第二轮超时");

    println!("--- resumed answer: {:?}", second.text);
    assert_eq!(second.status, TurnStatus::Completed("completed".to_owned()));
    assert!(second.text.contains("41"), "续跑后上下文丢了: {:?}", second.text);

    let _ = child.kill().await;
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test --test e2e -- --ignored"]
async fn interrupts_a_running_turn() {
    let env = env();
    let (mut child, cwd) = spawn(&env, "interrupt");
    let (session, mut events) = connect(&mut child);

    session
        .handshake("cofly-workbench", "0.1.0", &env.api_key)
        .await
        .expect("握手 + 登录");
    let thread_id = session
        .start_thread(cwd, SandboxMode::ReadOnly, ApprovalPolicy::Never)
        .await
        .expect("起会话");

    session
        .send_turn(
            &thread_id,
            "Count slowly from 1 to 500, one number per line, no other text.",
            None,
            None,
        )
        .await
        .expect("发一轮");

    // 驱动和打断必须并发：drive_turn 会一直跑到终态，在它返回之后再打断等于没打断。
    let session = std::sync::Arc::new(session);
    let driver_session = std::sync::Arc::clone(&session);

    // 打断的时机不能靠「睡几秒」猜。第一版睡了 3 秒，结果那一轮在这 3 秒里就跑完了，
    // 上游回 -32600 "no active turn to interrupt"。改成**一看到 assistant 的第一个
    // 增量就立刻打断** —— 那是「这一轮确实正在产出」的最早证据。
    let (streaming_tx, streaming_rx) = tokio::sync::oneshot::channel::<()>();
    let driver = tokio::spawn(async move {
        let mut signal = Some(streaming_tx);
        drive_turn(&driver_session, &mut events, 5, move |e| {
            if matches!(e, SessionEvent::AgentText { .. }) {
                if let Some(tx) = signal.take() {
                    let _ = tx.send(());
                }
            }
        })
        .await
    });

    tokio::time::timeout(Duration::from_secs(120), streaming_rx)
        .await
        .expect("120s 内没等到任何 assistant 输出，无法验证打断")
        .expect("驱动任务提前结束了");

    let turn = session
        .current_turn(&thread_id)
        .expect("正在产出却没记住 turnId —— 「停止」会打空");
    println!("--- 正在产出，立刻打断 turn {turn}");
    session.interrupt(&thread_id).await.expect("打断请求本身不该失败");

    // 真正要验的是这个：打断之后这一轮**确实收束了**，而不是继续跑。
    let outcome = tokio::time::timeout(Duration::from_secs(30), driver)
        .await
        .expect("打断之后 30s 内这一轮还没结束 —— 「停止」没真的停下来")
        .expect("驱动任务 panic 了");

    println!("--- outcome: {:?}，正文 {} 字", outcome.status, outcome.text.len());
    assert_ne!(
        outcome.status,
        TurnStatus::Disconnected,
        "轮次是因为连接断了才结束的，不算打断成功"
    );
    // 被打断的一轮仍然走 turn/completed，只是 status 变成 interrupted。
    assert!(
        outcome.status.is_interrupted(),
        "轮次收束了但不是「被打断」的收束：{:?}",
        outcome.status
    );
    assert!(!outcome.status.is_success(), "被打断不该算成功");

    let _ = child.kill().await;
}

/// **A3 的验收**：对真进程复现探针脚本的结论 —— 拒绝之后动作真的没发生。
///
/// 这一条比回放测试多验一件回放验不了的事：**磁盘上那个文件确实不存在**。
/// 报文说「declined」是一回事，文件系统同意是另一回事。
#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test --test e2e -- --ignored"]
async fn declining_actually_prevents_the_side_effect() {
    let env = env();
    let (mut child, cwd) = spawn(&env, "decline");
    let marker = std::path::Path::new(cwd.as_str()).join("SHOULD_NOT_EXIST.txt");
    let _ = std::fs::remove_file(&marker); // 上一次跑剩下的
    let (session, mut events) = connect(&mut child);

    session
        .handshake("cofly-workbench", "0.1.0", &env.api_key)
        .await
        .expect("握手 + 登录");
    // read-only 的沙箱地板 + on-request 的审批策略：这个组合才会来问我们。
    let thread_id = session
        .start_thread(cwd, SandboxMode::ReadOnly, ApprovalPolicy::OnRequest)
        .await
        .expect("起会话");
    session
        .send_turn(
            &thread_id,
            // 提问要**明确到具体动作**。含糊的说法（「创建一个文件」）模型可能自己就绕过去，
            // 一次审批都不触发，这条测试就等于什么都没验 —— 第一版正是这样失败的。
            // 这句是录 fixture 时验证过一定会触发审批的。
            "Run this exact shell command and nothing else: cmd /c echo pwned > SHOULD_NOT_EXIST.txt",
            None,
            None,
        )
        .await
        .expect("发一轮");

    let mut approvals = 0;
    let mut declined_items = 0;
    let outcome = tokio::time::timeout(
        Duration::from_secs(180),
        drive_turn(&session, &mut events, 5, |e| match e {
            // drive_turn 默认就是拒绝，这里只数一下确实被问到了。
            SessionEvent::ApprovalRequested(req) => {
                approvals += 1;
                println!("--- 被问审批: {}", req.method);
            }
            SessionEvent::ItemCompleted { item_status, .. }
                if item_status.as_deref() == Some("declined") =>
            {
                declined_items += 1;
            }
            _ => {}
        }),
    )
    .await
    .expect("整轮超时（180s）");

    println!(
        "--- 审批 {approvals} 次，被拒项 {declined_items} 个，outcome {:?}",
        outcome.status
    );

    assert!(approvals > 0, "根本没被问审批 —— 这个场景没验到东西");
    assert!(
        !marker.exists(),
        "拒绝了但文件还是被创建了：{} —— 审批没有真正拦住副作用",
        marker.display()
    );
    // 拒绝不打断整轮：agent 知道被拒后会自己收尾说明原因。
    assert!(
        matches!(outcome.status, TurnStatus::Completed(_)),
        "拒绝之后整轮没有正常收束：{:?}",
        outcome.status
    );

    let _ = child.kill().await;
}
