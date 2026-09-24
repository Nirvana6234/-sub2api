//! 起不来就退避重试；起来之后死了要让调用方**知道**。

use std::time::Duration;

use crate::engine::{Engine, EngineConfig};
use crate::HostError;

/// 退避策略。
///
/// 刻意不加随机抖动：宿主只有一个 codex，不存在惊群，而确定性让它可测。
#[derive(Debug, Clone, Copy)]
pub struct Backoff {
    pub base: Duration,
    pub max: Duration,
    pub max_attempts: u32,
}

impl Default for Backoff {
    fn default() -> Self {
        Backoff {
            base: Duration::from_millis(500),
            max: Duration::from_secs(30),
            max_attempts: 5,
        }
    }
}

impl Backoff {
    /// 第 `attempt` 次失败之后等多久（`attempt` 从 1 开始）。
    pub fn delay(&self, attempt: u32) -> Duration {
        if attempt == 0 {
            return Duration::ZERO;
        }
        // 2^(attempt-1) * base，封顶 max。用 checked 避免次数大了以后溢出。
        let factor = 1u32.checked_shl(attempt - 1).unwrap_or(u32::MAX);
        self.base.checked_mul(factor).unwrap_or(self.max).min(self.max)
    }
}

/// 一次失败的尝试，交给调用方去记日志或提示用户。
#[derive(Debug, Clone)]
pub struct FailedAttempt {
    pub attempt: u32,
    pub error: String,
    pub retry_in: Duration,
}

/// 重启成功的结果。
///
/// `#[must_use]` 是故意的：**重启不是透明的**。新起来的 app-server 是个空进程，
/// 之前那些 thread 的内存状态**全没了**。调用方必须要么 `thread/resume` 把会话续起来，
/// 要么如实告诉用户「引擎重启了」。悄悄接上去装作无事发生，会让用户以为上下文还在。
#[must_use = "引擎重启后旧 thread 在新进程里不存在：要么 thread/resume，要么告诉用户"]
pub struct Restarted {
    pub engine: Engine,
    /// 一共试了几次才起来。
    pub attempts: u32,
}

/// 负责把引擎拉起来，并在拉不起来时退避重试。
pub struct Supervisor {
    config: EngineConfig,
    backoff: Backoff,
}

impl Supervisor {
    pub fn new(config: EngineConfig, backoff: Backoff) -> Self {
        Supervisor { config, backoff }
    }

    /// 把引擎拉起来。失败就按退避重试，直到成功或用完次数。
    ///
    /// 每次失败都会调一次 `on_failure` —— **失败要能被看见**，
    /// 悄悄重试到最后才报错，中间那段时间用户只会觉得「卡住了」。
    ///
    /// # Errors
    /// 用完 `max_attempts` 仍然起不来时，返回最后一次的错误。
    pub async fn start(
        &self,
        mut on_failure: impl FnMut(&FailedAttempt),
    ) -> Result<Restarted, HostError> {
        let mut last: Option<HostError> = None;

        for attempt in 1..=self.backoff.max_attempts {
            match Engine::spawn(&self.config) {
                Ok(engine) => return Ok(Restarted { engine, attempts: attempt }),
                Err(e) => {
                    let retry_in = if attempt < self.backoff.max_attempts {
                        self.backoff.delay(attempt)
                    } else {
                        Duration::ZERO
                    };
                    on_failure(&FailedAttempt {
                        attempt,
                        error: e.to_string(),
                        retry_in,
                    });
                    last = Some(e);
                    if attempt < self.backoff.max_attempts {
                        tokio::time::sleep(retry_in).await;
                    }
                }
            }
        }

        Err(last.unwrap_or_else(|| HostError::Spawn("max_attempts 是 0".to_owned())))
    }
}
