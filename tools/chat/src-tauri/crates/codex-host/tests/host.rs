//! `CODEX_HOME` 校验、退避算术、以及「起不来时会不会安静地卡住」。

use std::time::Duration;

use codex_host::{Backoff, CodexHome, DeviceIdentity, EngineConfig, HostError, Supervisor};

#[test]
fn codex_home_refuses_the_system_temp_dir() {
    // 实测：codex 在 temp 下会拒绝创建 helper 并告警，之后仍然能跑 ——
    // 这种「能用但已降级」最难查，所以直接挡在门口。
    let bad = std::env::temp_dir().join("cofly-app-dir-should-be-rejected");
    let err = CodexHome::under_app_dir(&bad).unwrap_err();
    assert!(matches!(err, HostError::BadCodexHome(_)), "拿到的是 {err:?}");
    assert!(err.to_string().contains("temp"), "报错该说清楚为什么: {err}");
    assert!(!bad.exists(), "被拒的路径不该被建出来");
}

/// 凭据只会落在**程序数据目录之下**，位置由我们推出来，调用方指不到别处。
#[test]
fn codex_home_lives_under_the_app_dir_and_nowhere_else() {
    let app = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("app-dir");
    let _ = std::fs::remove_dir_all(&app);

    let home = CodexHome::under_app_dir(&app).expect("程序数据目录下应当可用");
    assert!(home.as_path().starts_with(&app), "CODEX_HOME 跑到程序目录外面去了");
    assert!(home.as_path().is_dir(), "应当被建出来");
    assert!(
        home.credentials_file().starts_with(&app),
        "凭据文件跑到程序目录外面去了"
    );

    // 再来一次：已存在也要能通过，并且落在同一个位置。
    let again = CodexHome::under_app_dir(&app).expect("已存在也应当可用");
    assert_eq!(again, home, "同一个程序目录必须推出同一个 CODEX_HOME");
}

#[test]
fn codex_home_refuses_when_a_file_squats_on_the_path() {
    let app = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("app-dir-squatted");
    std::fs::create_dir_all(&app).expect("建程序目录");
    let squatter = app.join("codex-home");
    let _ = std::fs::remove_dir_all(&squatter);
    std::fs::write(&squatter, b"x").expect("写文件占位");

    let err = CodexHome::under_app_dir(&app).unwrap_err();
    assert!(matches!(err, HostError::BadCodexHome(_)), "拿到的是 {err:?}");
    let _ = std::fs::remove_file(&squatter);
}

/// 会话结束要把凭据抹掉，而且抹之前先覆盖内容。
#[test]
fn wiping_credentials_removes_the_file() {
    let app = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("app-dir-wipe");
    let _ = std::fs::remove_dir_all(&app);
    let home = CodexHome::under_app_dir(&app).expect("建 CODEX_HOME");

    // 没有文件时是 no-op，不该报错。
    assert!(!home.wipe_credentials().expect("空目录抹除"), "本来就没有文件");

    std::fs::write(home.credentials_file(), br#"{"OPENAI_API_KEY":"sk-fake"}"#).expect("造一个凭据");
    assert!(home.wipe_credentials().expect("抹除"), "应当报告确实抹掉了一个");
    assert!(!home.credentials_file().exists(), "文件还在");
}

#[test]
fn backoff_doubles_and_then_stops_growing() {
    let b = Backoff {
        base: Duration::from_millis(100),
        max: Duration::from_millis(800),
        max_attempts: 10,
    };
    assert_eq!(b.delay(0), Duration::ZERO);
    assert_eq!(b.delay(1), Duration::from_millis(100));
    assert_eq!(b.delay(2), Duration::from_millis(200));
    assert_eq!(b.delay(4), Duration::from_millis(800));
    // 封顶之后不再增长，也不会因为移位溢出而 panic。
    assert_eq!(b.delay(5), Duration::from_millis(800));
    assert_eq!(b.delay(40), Duration::from_millis(800));
    assert_eq!(b.delay(u32::MAX), Duration::from_millis(800));
}

/// 起不来的时候，**每一次失败都要能被看见**。
///
/// 悄悄重试到最后才报错，中间那段时间用户只会觉得「卡住了」——
/// 那是最难排查的一种故障表现。
#[tokio::test(flavor = "multi_thread")]
async fn failing_to_start_reports_every_attempt_and_then_gives_up() {
    let home = CodexHome::under_app_dir(
        std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("app-dir-supervisor"),
    )
    .expect("建 CODEX_HOME");

    let config = EngineConfig {
        binary: "definitely-not-a-real-codex-binary".into(),
        home,
        base_url: "http://127.0.0.1:1".to_owned(),
        model: "none".to_owned(),
    };
    let backoff = Backoff {
        base: Duration::from_millis(1),
        max: Duration::from_millis(2),
        max_attempts: 3,
    };

    let mut seen = Vec::new();
    let result = Supervisor::new(config, backoff)
        .start(|f| seen.push((f.attempt, f.error.clone())))
        .await;

    let err = result.err().expect("这个二进制不存在，不该起得来");
    assert!(matches!(err, HostError::Spawn(_)), "拿到的是 {err:?}");

    assert_eq!(seen.len(), 3, "三次尝试应当报三次，实际 {seen:?}");
    assert_eq!(seen.iter().map(|(a, _)| *a).collect::<Vec<_>>(), vec![1, 2, 3]);
    for (_, msg) in &seen {
        assert!(msg.contains("起不来"), "失败原因该说人话: {msg}");
    }
}

/// `Restarted` 带着 `#[must_use]`，因为**重启不是透明的**。
///
/// 这条测试用编译期的办法说明意图：拿到 `Restarted` 就必须处理它。
/// 新起来的 app-server 是个空进程，之前那些 thread 的内存状态全没了 ——
/// 悄悄接上去装作无事发生，会让用户以为上下文还在。
#[test]
fn restarted_is_must_use_so_callers_cannot_ignore_lost_state() {
    // 这里只做一件事：把这个约定写下来，并让它出现在测试列表里，
    // 免得以后有人顺手把 #[must_use] 删掉。
    let src = include_str!("../src/supervisor.rs");
    assert!(
        src.contains("#[must_use") && src.contains("pub struct Restarted"),
        "Restarted 上的 #[must_use] 不见了 —— 重启会变回「透明」的，那是错的"
    );
}

/// 安装 ID 必须**跨进程稳定**（同一次安装每次都读到同一个），
/// 又必须**跨安装不同**（重装换新的）。
///
/// 这两条一起决定了托管 key 的名字对不对：稳定性保证我们能认领上次那把、
/// 不重复建；差异性保证重装之后不去续期一个说不清来历的旧租约。
#[test]
fn install_id_is_stable_within_an_install_and_fresh_across_installs() {
    let app = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("identity");
    let _ = std::fs::remove_dir_all(&app);

    let first = DeviceIdentity::load_or_create(&app).expect("生成身份");
    assert!(!first.install_id.is_empty(), "安装 ID 不该是空的");
    assert!(!first.machine_name.is_empty(), "机器名不该是空的");

    let second = DeviceIdentity::load_or_create(&app).expect("再读一次");
    assert_eq!(first, second, "同一次安装每次都该读到同一个身份");

    // 模拟卸载重装：程序数据目录没了。
    std::fs::remove_dir_all(&app).expect("清掉程序数据目录");
    let reinstalled = DeviceIdentity::load_or_create(&app).expect("重装后生成");
    assert_ne!(
        first.install_id, reinstalled.install_id,
        "重装之后安装 ID 应当是新的，否则会去续期旧安装留下的租约"
    );
    assert_eq!(
        first.machine_name, reinstalled.machine_name,
        "机器名不该跟着重装变"
    );
}
