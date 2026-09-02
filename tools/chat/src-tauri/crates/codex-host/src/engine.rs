//! 起一个 codex app-server，并把它关进笼子。

use std::path::PathBuf;
use std::process::Stdio;

use tokio::process::{Child, Command};

use crate::cage::Cage;
use crate::home::CodexHome;
use crate::HostError;

/// 起 app-server 需要的全部东西。
#[derive(Debug, Clone)]
pub struct EngineConfig {
    /// codex 可执行文件。
    pub binary: PathBuf,
    /// 私有 `CODEX_HOME`。
    pub home: CodexHome,
    /// 中转站地址（我们自己的服务端）。
    pub base_url: String,
    /// 模型名。
    pub model: String,
}

impl EngineConfig {
    /// 拼出 `-c` 覆盖参数。
    ///
    /// 全部按调用传，**不碰用户的 `~/.codex/config.toml`** —— 那是别人的配置，
    /// 我们既不该读也不该改。
    fn config_overrides(&self) -> Vec<String> {
        [
            "model_provider=\"custom\"".to_owned(),
            "model_providers.custom.name=\"custom\"".to_owned(),
            "model_providers.custom.wire_api=\"responses\"".to_owned(),
            "model_providers.custom.requires_openai_auth=true".to_owned(),
            format!("model_providers.custom.base_url=\"{}\"", self.base_url),
            format!("model=\"{}\"", self.model),
        ]
        .into()
    }
}

/// 一个跑着的 app-server：进程 + 它的 stdio + 关着它的笼子。
///
/// **笼子和这个结构体同生共死**：`Engine` 被丢弃（或宿主进程被强杀）时，
/// 笼子关闭，里面的 codex 进程被杀。
pub struct Engine {
    child: Child,
    /// 只是持有着 —— 它的价值全在「被销毁的那一刻」。
    _cage: Cage,
}

impl Engine {
    /// 起一个 app-server 并立刻入笼。
    ///
    /// # Errors
    /// 二进制起不来、笼子建不出、或者笼子建出来了却没真的带上「关闭即杀」标志时返回。
    pub fn spawn(config: &EngineConfig) -> Result<Self, HostError> {
        let cage = Cage::new().map_err(|e| HostError::Cage(format!("建不出进程笼: {e}")))?;

        // 建完就回头核对一次：这个标志设错字段是会静默成功的。
        // 宁可现在起不来，也不要线上强杀时才发现根本没关住。
        if cfg!(windows)
            && !cage
                .kills_on_close()
                .map_err(|e| HostError::Cage(format!("查不了进程笼状态: {e}")))?
        {
            return Err(HostError::Cage(
                "进程笼没有带上「关闭即杀」标志 —— 强杀 Chat 会留下孤儿 codex 进程".to_owned(),
            ));
        }

        let mut cmd = Command::new(&config.binary);
        cmd.arg("app-server");
        for over in config.config_overrides() {
            cmd.arg("-c").arg(over);
        }
        cmd.env("CODEX_HOME", config.home.as_path())
            // 这两个环境变量对 app-server 本来就无效（凭据走协议），
            // 显式清掉是为了不让它们被子进程继承出去。
            .env_remove("OPENAI_API_KEY")
            .env_remove("CODEX_API_KEY")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            // 管正常退出与 panic 展开那一半；强杀那一半归笼子。两个都要。
            .kill_on_drop(true);

        let mut child = cmd
            .spawn()
            .map_err(|e| HostError::Spawn(format!("{} 起不来: {e}", config.binary.display())))?;

        if let Err(e) = cage.adopt(&child) {
            // 入不了笼就不能让它活着 —— 否则就是个不受管的孤儿。
            let _ = child.start_kill();
            return Err(HostError::Cage(format!("进程入笼失败: {e}")));
        }

        Ok(Engine { child, _cage: cage })
    }

    /// 取走 stdio 的两半，交给 [`codex_adapter::Session::connect`]。
    ///
    /// 只能取一次。
    ///
    /// # Errors
    /// 已经被取走过时返回。
    pub fn take_stdio(
        &mut self,
    ) -> Result<(tokio::process::ChildStdout, tokio::process::ChildStdin), HostError> {
        let out = self
            .child
            .stdout
            .take()
            .ok_or_else(|| HostError::Spawn("stdout 已经被取走了".to_owned()))?;
        let inp = self
            .child
            .stdin
            .take()
            .ok_or_else(|| HostError::Spawn("stdin 已经被取走了".to_owned()))?;
        Ok((out, inp))
    }

    /// 取走 stderr —— codex 的告警只会走这里，别丢掉。
    pub fn take_stderr(&mut self) -> Option<tokio::process::ChildStderr> {
        self.child.stderr.take()
    }

    /// 进程还活着吗。
    ///
    /// 这就是我们全部的「健康探测」，而且是刻意的：协议里**没有便宜的心跳原语**。
    /// 拿 `thread/list` 当心跳要花一次真 RPC，而且它会因为跟健康无关的原因失败 ——
    /// 那种探测比不探测更坏。
    ///
    /// 「能不能干活」由 `initialize` 成功与否回答，那是会话层的事。
    pub fn is_alive(&mut self) -> bool {
        matches!(self.child.try_wait(), Ok(None))
    }

    pub fn pid(&self) -> Option<u32> {
        self.child.id()
    }

    /// 有序停止：先关 stdin 让它自己收摊，超时了再动手。
    pub async fn shutdown(mut self, grace: std::time::Duration) -> Result<(), HostError> {
        // 关掉 stdin = 给 app-server 一个 EOF，它会自己收束。
        drop(self.child.stdin.take());

        match tokio::time::timeout(grace, self.child.wait()).await {
            Ok(Ok(_)) => Ok(()),
            Ok(Err(e)) => Err(HostError::Spawn(format!("等进程退出失败: {e}"))),
            Err(_) => {
                let _ = self.child.kill().await;
                Ok(())
            }
        }
    }
}
