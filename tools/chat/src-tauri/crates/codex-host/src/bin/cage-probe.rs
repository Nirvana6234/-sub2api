//! A4 验收用的靶子。**只给测试用。**
//!
//! # 它怎么证明「强杀宿主，孙进程跟着死」
//!
//! 三个角色：
//!
//! ```text
//! 测试进程 ──spawn──> cage-probe ──spawn──> cage-probe --sleep
//!            (stdout 管道)        (stdout 继承自上面那根管道)
//! ```
//!
//! 关键在于**孙进程继承了同一根 stdout 管道**。于是测试那头的读端要读到 EOF，
//! 必须等到**两个写端都关闭** —— 也就是两个进程都死了。
//!
//! 测试于是可以：强杀 `cage-probe`（`TerminateProcess`，不跑任何析构函数）→ 去读管道。
//! 读到 EOF 就说明孙进程也死了；读不到就说明它还活着，笼子没关住。
//!
//! **这样绕开了 PID 复用**：不需要「查查看还有没有这个 pid 的进程」——
//! 那种查法会在 PID 被系统回收再分配时给出错误答案。
//!
//! `--no-cage` 是反向对照：不建笼子，孙进程**应该**活下来。没有这个对照，
//! 主测试可能因为别的原因通过（比如孙进程压根没起来），而我们不会知道。

use std::io::Write;
use std::process::Stdio;
use std::time::Duration;

use codex_host::Cage;

#[tokio::main(flavor = "current_thread")]
async fn main() {
    let args: Vec<String> = std::env::args().collect();

    // 孙进程：什么都不做，只是活着并且抓着那根管道。
    if args.iter().any(|a| a == "--sleep") {
        loop {
            std::thread::sleep(Duration::from_secs(3600));
        }
    }

    // 真 codex 模式：起一个货真价实的 app-server，跑起一轮，然后挂着等强杀。
    // 这才是计划里写的验收 —— codex 会自己再生助手进程（命令执行器、沙箱助手），
    // 那些才是可能从笼子里逃出去的东西，靠假靶子测不到。
    if args.iter().any(|a| a == "--codex") {
        run_real_codex().await;
        return;
    }

    let use_cage = !args.iter().any(|a| a == "--no-cage");

    let cage = if use_cage {
        let cage = Cage::new().expect("建不出笼子");
        // 建完立刻核对：标志位设错字段是会静默成功的。
        if cfg!(windows) {
            assert!(cage.kills_on_close().expect("查不了笼子状态"), "笼子没带上「关闭即杀」");
        }
        Some(cage)
    } else {
        None
    };

    let me = std::env::current_exe().expect("找不到自己");
    let child = tokio::process::Command::new(me)
        .arg("--sleep")
        // 继承 stdout：孙进程和我们抓着同一根管道，这是整个测试的支点。
        .stdout(Stdio::inherit())
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .expect("孙进程起不来");

    if let Some(cage) = &cage {
        cage.adopt(&child).expect("孙进程入不了笼");
    }

    println!("READY {}", child.id().unwrap_or(0));
    std::io::stdout().flush().expect("flush");

    // 挂着，等测试来强杀。笼子必须在作用域里活着 —— 它一旦被销毁，
    // 孙进程立刻就死了，那就不是在测「强杀」而是在测「正常析构」。
    loop {
        std::thread::sleep(Duration::from_secs(3600));
    }
}

/// 起真 codex，发一轮，等它开始产出后报 READY，然后挂着。
///
/// 配置全从环境变量来（测试负责设）：
/// `CAGE_PROBE_BIN` / `CAGE_PROBE_BASE_URL` / `CAGE_PROBE_MODEL` / `CAGE_PROBE_HOME`，
/// API key 从 `CAGE_PROBE_API_KEY` 或用户的 `~/.codex/auth.json` 读。
async fn run_real_codex() {
    use codex_adapter::session::{Session, SessionEvent};
    use codex_adapter::{ApprovalPolicy, SandboxMode, WorkspaceDir};
    use codex_host::{CodexHome, EngineConfig};

    let need = |k: &str| std::env::var(k).unwrap_or_else(|_| panic!("缺环境变量 {k}"));

    let home = CodexHome::under_app_dir(need("CAGE_PROBE_HOME")).expect("CODEX_HOME 不合法");
    let workdir = std::path::Path::new(&need("CAGE_PROBE_HOME")).join("work");
    std::fs::create_dir_all(&workdir).expect("建工作目录");

    let api_key = std::env::var("CAGE_PROBE_API_KEY").unwrap_or_else(|_| {
        let auth = std::path::PathBuf::from(
            std::env::var("USERPROFILE").or_else(|_| std::env::var("HOME")).expect("找不到 home"),
        )
        .join(".codex")
        .join("auth.json");
        let text = std::fs::read_to_string(&auth).expect("读不到 auth.json");
        let json: serde_json::Value = serde_json::from_str(&text).expect("auth.json 不是 JSON");
        json.get("OPENAI_API_KEY").and_then(|v| v.as_str()).expect("没有 OPENAI_API_KEY").to_owned()
    });

    let binary: std::path::PathBuf = need("CAGE_PROBE_BIN").into();
    let config = EngineConfig {
        kind: codex_host::CodexBinaryKind::infer(&binary),
        binary,
        home,
        base_url: need("CAGE_PROBE_BASE_URL"),
        model: std::env::var("CAGE_PROBE_MODEL").unwrap_or_else(|_| "gpt-5.5".to_owned()),
    };

    let mut engine = codex_host::Engine::spawn(&config).expect("起不了 codex");
    let pid = engine.pid().expect("拿不到 codex pid");
    let (out, inp) = engine.take_stdio().expect("拿不到 stdio");
    let (session, mut events) = Session::connect(out, inp);

    session.handshake("cofly-cage-probe", "0.1.0", &api_key).await.expect("握手");
    session
        .start_thread(
            WorkspaceDir::new(&workdir).expect("工作目录"),
            SandboxMode::ReadOnly,
            ApprovalPolicy::Never,
        )
        .await
        .expect("起会话");
    session
        .send_turn("Count slowly from 1 to 500, one number per line, no other text.")
        .await
        .expect("发一轮");

    // 等到真的开始产出 —— 「turn 进行中」这个条件必须是真的，
    // 否则测的就是「空转的进程被杀掉」，那没意思。
    let deadline = tokio::time::Instant::now() + Duration::from_secs(120);
    loop {
        match tokio::time::timeout_at(deadline, events.recv()).await {
            Ok(Some(SessionEvent::AgentText { .. })) => break,
            Ok(Some(_)) => continue,
            Ok(None) => panic!("事件流提前结束"),
            Err(_) => panic!("120s 内没等到 assistant 输出"),
        }
    }

    println!("READY {pid}");
    std::io::stdout().flush().expect("flush");

    // 挂着等强杀。engine 必须留在作用域里 —— 笼子在它身上。
    loop {
        tokio::time::sleep(Duration::from_secs(3600)).await;
    }
}
