//! **计划里写的那条 A4 验收，原文照做**：
//! 「`turn` 进行中**强杀 Chat**，任务管理器里**没有残留 codex 进程**。」
//!
//! `cage.rs` 里那两条用的是假靶子，证明的是 Job Object 这个机制成立。
//! 这一条用**真 codex**，而且**确认它已经在产出**之后才动手 ——
//! 因为真 codex 会自己再生助手进程（命令执行器、沙箱助手），
//! 那些才是可能从笼子里逃出去的东西，假靶子测不到。
//!
//! `#[ignore]`：要真 codex 与真中转站，还费 token。普通 `cargo test` 会显示
//! `1 ignored`，不会伪装成跑过了。
//!
//! ```text
//! set CODEX_ADAPTER_BIN=C:\...\codex.exe
//! set CODEX_ADAPTER_BASE_URL=http://...
//! cargo test -p codex-host --test e2e_cage -- --ignored --nocapture
//! ```

#![cfg(windows)]

use std::path::PathBuf;
use std::process::Stdio;
use std::time::Duration;

use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::Command;

/// 等一个进程死掉 —— **按句柄等，不按 pid 查**。
///
/// 「还有没有这个 pid 的进程」这种查法会在 PID 被系统回收再分配后给出错误答案。
/// 先把句柄开好再动手杀，句柄就一直指着那一个进程，不会认错。
mod win {
    use windows_sys::Win32::Foundation::{CloseHandle, HANDLE, WAIT_OBJECT_0};
    use windows_sys::Win32::System::Threading::{OpenProcess, WaitForSingleObject};

    /// `SYNCHRONIZE`：只要能等它，不需要别的权限。
    const SYNCHRONIZE: u32 = 0x0010_0000;

    pub struct ProcessWatch(HANDLE);

    // 句柄可以跨线程用。
    unsafe impl Send for ProcessWatch {}

    impl ProcessWatch {
        /// 在杀之前就把句柄开好。开不出来说明进程已经没了。
        pub fn open(pid: u32) -> Option<Self> {
            // SAFETY: 只申请 SYNCHRONIZE，失败返回空句柄。
            let handle = unsafe { OpenProcess(SYNCHRONIZE, 0, pid) };
            if handle.is_null() {
                None
            } else {
                Some(ProcessWatch(handle))
            }
        }

        /// 在 `ms` 毫秒内死掉了吗。
        pub fn died_within(&self, ms: u32) -> bool {
            // SAFETY: 句柄由本类型持有，仍然有效。
            unsafe { WaitForSingleObject(self.0, ms) == WAIT_OBJECT_0 }
        }
    }

    impl Drop for ProcessWatch {
        fn drop(&mut self) {
            unsafe {
                CloseHandle(self.0);
            }
        }
    }
}

fn need(var: &str) -> String {
    std::env::var(var)
        .unwrap_or_else(|_| panic!("这条端到端验收需要环境变量 {var}（见本文件顶部说明）"))
}

#[tokio::test(flavor = "multi_thread")]
#[ignore = "需要真 codex 进程与中转站：cargo test -p codex-host --test e2e_cage -- --ignored"]
async fn hard_killing_the_host_mid_turn_leaves_no_codex_behind() {
    let home = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("cage-e2e-home");
    let _ = std::fs::remove_dir_all(&home);

    let mut probe = Command::new(env!("CARGO_BIN_EXE_cage-probe"))
        .arg("--codex")
        .env("CAGE_PROBE_BIN", need("CODEX_ADAPTER_BIN"))
        .env("CAGE_PROBE_BASE_URL", need("CODEX_ADAPTER_BASE_URL"))
        .env(
            "CAGE_PROBE_MODEL",
            std::env::var("CODEX_ADAPTER_MODEL").unwrap_or_else(|_| "gpt-5.5".to_owned()),
        )
        .env("CAGE_PROBE_HOME", &home)
        .stdout(Stdio::piped())
        .stdin(Stdio::null())
        .stderr(Stdio::inherit())
        .spawn()
        .expect("探针起不来");

    let stdout = probe.stdout.take().expect("拿不到 stdout");
    let mut reader = BufReader::new(stdout);
    let mut line = String::new();
    tokio::time::timeout(Duration::from_secs(180), reader.read_line(&mut line))
        .await
        .expect("180s 内没等到 READY（真 codex 起会话 + 跑一轮要时间）")
        .expect("读 READY 失败");

    let codex_pid: u32 = line
        .trim()
        .rsplit(' ')
        .next()
        .and_then(|s| s.parse().ok())
        .unwrap_or_else(|| panic!("READY 行里没有 codex pid: {line:?}"));
    println!("--- codex pid = {codex_pid}，此刻它正在产出");

    // **先开句柄，再动手。** 反过来的话，杀完再开就可能开到一个复用了同一 pid 的新进程。
    let watch = win::ProcessWatch::open(codex_pid)
        .expect("开不了 codex 进程句柄 —— 它在被杀之前就已经不在了？");

    // TerminateProcess：和用户在任务管理器里点「结束任务」同一条路，
    // 探针里的析构函数一个都不会跑。
    probe.kill().await.expect("杀不掉探针");

    assert!(
        watch.died_within(15_000),
        "强杀宿主 15s 后 codex（pid {codex_pid}）还活着 —— \
         『关掉 Chat 就够不着』这句话不成立"
    );
    println!("--- codex 已随宿主一起消失");
}
