//! `CODEX_HOME` 校验、退避算术、以及「起不来时会不会安静地卡住」。

use std::time::Duration;

use codex_host::{Backoff, CodexHome, EngineConfig, HostError, Supervisor};

#[test]
fn codex_home_refuses_the_system_temp_dir() {
    // 实测：codex 在 temp 下会拒绝创建 helper 并告警，之后仍然能跑 ——
    // 这种「能用但已降级」最难查，所以直接挡在门口。
    let bad = std::env::temp_dir().join("cofly-codex-home-should-be-rejected");
    let err = CodexHome::new(&bad).unwrap_err();
    assert!(matches!(err, HostError::BadCodexHome(_)), "拿到的是 {err:?}");
    assert!(err.to_string().contains("temp"), "报错该说清楚为什么: {err}");
    assert!(!bad.exists(), "被拒的路径不该被建出来");
}

#[test]
fn codex_home_creates_the_directory_when_missing() {
    let good = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("home-ok");
    let _ = std::fs::remove_dir_all(&good);

    let home = CodexHome::new(&good).expect("target 下的目录应当可用");
    assert!(good.is_dir(), "应当被建出来");
    assert_eq!(home.as_path(), good.as_path());

    // 再来一次：已存在也要能通过。
    CodexHome::new(&good).expect("已存在的目录应当可用");
}

#[test]
fn codex_home_refuses_a_file() {
    let path = std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("home-is-a-file");
    std::fs::write(&path, b"x").expect("写文件");
    let err = CodexHome::new(&path).unwrap_err();
    assert!(matches!(err, HostError::BadCodexHome(_)), "拿到的是 {err:?}");
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
    let home = CodexHome::new(
        std::path::PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("home-supervisor"),
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
