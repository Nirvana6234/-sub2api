//! **A4 的验收**：强杀宿主之后，笼子里的进程不能活下来。
//!
//! 验的是「用户在任务管理器里点结束任务」那条路 —— `TerminateProcess`，
//! **不给任何析构函数跑的机会**。`kill_on_drop` 在这条路上完全不起作用，
//! 所以这里测的确实是笼子本身。
//!
//! 判定方式见 `src/bin/cage-probe.rs` 的说明：靠共享 stdout 管道的 EOF，
//! 不靠查 PID（PID 会被复用，查出来的答案不可信）。

use std::process::Stdio;
use std::time::Duration;

use tokio::io::{AsyncBufReadExt, AsyncReadExt, BufReader};
use tokio::process::Command;

/// 起一个探针，等它报 READY，返回（进程、stdout 读端、孙进程 PID）。
async fn start_probe(
    extra: &[&str],
) -> (tokio::process::Child, tokio::process::ChildStdout, u32) {
    let mut child = Command::new(env!("CARGO_BIN_EXE_cage-probe"))
        .args(extra)
        .stdout(Stdio::piped())
        .stdin(Stdio::null())
        .stderr(Stdio::inherit())
        .spawn()
        .expect("探针起不来");

    let mut stdout = child.stdout.take().expect("拿不到 stdout");

    // 读第一行 READY。用 BufReader 会把后面的字节也吞进它自己的缓冲里，
    // 所以读完之后要把 BufReader 拆掉、把剩余缓冲还回来 —— 这里的做法是
    // 只读到换行为止，且确认缓冲区已空。
    let mut reader = BufReader::new(&mut stdout);
    let mut line = String::new();
    tokio::time::timeout(Duration::from_secs(30), reader.read_line(&mut line))
        .await
        .expect("30s 内没等到探针 READY")
        .expect("读 READY 失败");
    assert!(line.starts_with("READY"), "探针的第一行不对: {line:?}");
    let grandchild: u32 = line
        .trim()
        .rsplit(' ')
        .next()
        .and_then(|s| s.parse().ok())
        .unwrap_or_else(|| panic!("READY 行里没有孙进程 PID: {line:?}"));
    assert!(
        reader.buffer().is_empty(),
        "BufReader 预读了多余字节，后面的 EOF 判定会不准"
    );
    drop(reader);

    (child, stdout, grandchild)
}

/// 管道要多久才读到 EOF。EOF 意味着**所有**写端都关了 —— 探针和它的孙进程都死了。
async fn wait_for_eof(mut stdout: tokio::process::ChildStdout, within: Duration) -> bool {
    let mut buf = Vec::new();
    tokio::time::timeout(within, stdout.read_to_end(&mut buf))
        .await
        .is_ok()
}

/// **验收本体。**
#[tokio::test(flavor = "multi_thread")]
async fn hard_killing_the_host_takes_the_caged_process_with_it() {
    let (mut probe, stdout, _grandchild) = start_probe(&[]).await;

    // TerminateProcess —— 和用户在任务管理器里点「结束任务」是同一条路。
    // 探针里的 Cage 析构函数**不会**执行；能杀掉孙进程的只有内核关句柄这一下。
    probe.kill().await.expect("杀不掉探针");

    assert!(
        wait_for_eof(stdout, Duration::from_secs(15)).await,
        "强杀宿主 15s 后管道仍未 EOF —— 笼子里的进程活下来了。\
         『关掉 Chat 就够不着』这句话不成立。"
    );
}

/// 反向对照：**不建笼子**时，孙进程应该活下来。
///
/// 没有这一条，上面那条测试可能因为完全无关的原因通过（比如孙进程根本没起来），
/// 而我们不会知道 —— 一个永远绿的测试等于没有测试。
#[tokio::test(flavor = "multi_thread")]
async fn without_the_cage_the_child_survives_which_proves_the_test_can_fail() {
    let (mut probe, stdout, grandchild) = start_probe(&["--no-cage"]).await;
    probe.kill().await.expect("杀不掉探针");

    let eof = wait_for_eof(stdout, Duration::from_secs(5)).await;

    // 把孤儿收拾掉，别留在机器上。**按 PID 杀，不按镜像名** ——
    // 按名字杀会连带干掉并发跑的那条验收测试里的探针，制造假绿。
    kill_pid(grandchild);

    assert!(
        !eof,
        "没建笼子却也 EOF 了 —— 说明孙进程是被别的东西杀掉的，\
         上面那条验收测试证明不了笼子在起作用"
    );
}

/// 收掉反向对照故意留下的那个孤儿。按 PID，不按名字。
fn kill_pid(pid: u32) {
    #[cfg(windows)]
    let mut cmd = {
        let mut c = std::process::Command::new("taskkill");
        c.args(["/F", "/PID", &pid.to_string()]);
        c
    };
    #[cfg(not(windows))]
    let mut cmd = {
        let mut c = std::process::Command::new("kill");
        c.args(["-9", &pid.to_string()]);
        c
    };
    let _ = cmd.stdout(Stdio::null()).stderr(Stdio::null()).status();
}

/// 笼子建出来之后必须**真的**带着「关闭即杀」。
///
/// `SetInformationJobObject` 把标志设在错误的结构体字段上会**静默成功** ——
/// 笼子看起来建好了，测试也过了，直到线上强杀时才发现根本没关住。所以回头查一次。
#[test]
fn the_cage_actually_carries_the_kill_on_close_flag() {
    let cage = codex_host::Cage::new().expect("建不出笼子");
    let kills = cage.kills_on_close().expect("查不了笼子状态");

    if cfg!(windows) {
        assert!(kills, "笼子没带上「关闭即杀」标志");
    } else {
        // 非 Windows 上没有等价物，就该如实回 false，别假装有保证。
        assert!(!kills, "非 Windows 上不该声称有笼子保证");
    }
}
