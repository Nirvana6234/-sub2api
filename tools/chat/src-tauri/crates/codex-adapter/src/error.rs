//! 错误分类。
//!
//! 分类的目的只有一个：**让上层知道该怎么办**。进程没了要重启，协议不认要报升级，
//! 鉴权失败要引导重新登录，目录非法要让用户重选，模型侧错误要原样告诉用户。
//! 把它们混成一个 `anyhow::Error` 就等于把这四种处置全丢了。

use std::io;

use serde::{Deserialize, Serialize};

/// JSON-RPC 的错误对象（`JSONRPCErrorError`）。
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct RpcError {
    pub code: i64,
    pub message: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<serde_json::Value>,
}

/// 上层需要区别对待的几类失败。
#[derive(Debug, thiserror::Error)]
pub enum AdapterError {
    /// stdio 断了 —— app-server 进程已经不在，或者管道被关。宿主该走重启退避。
    #[error("codex app-server 连接已断开: {0}")]
    ProcessGone(String),

    /// 收到了不是 JSON、或者不符合 JSON-RPC 形状的东西。
    ///
    /// 这一类**几乎总是版本漂移**：上游改了报文而我们的投影没跟上。不要静默吞掉。
    #[error("协议不认识: {0}")]
    Protocol(String),

    /// 鉴权失败 —— 中转站拒绝了我们的凭据。要重新走登录授权。
    #[error("鉴权失败: {0}")]
    Auth(RpcError),

    /// 工作目录非法（不存在、不是目录、或不在我们的白名单里）。
    ///
    /// **这一类只会由我们自己产生。** 实测：`thread/start` 传一个根本不存在的 `cwd`，
    /// codex **照样成功建会话**，不做任何校验。所以目录合法性必须在宿主侧起会话之前
    /// 自己验（A8 的目录白名单），指望上游报错就等于没验。
    #[error("工作目录非法: {0}")]
    InvalidPath(String),

    /// 模型 / 上游服务返回的错误，原样透出给用户。
    #[error("上游错误: {0}")]
    Upstream(RpcError),

    /// 本地 IO 失败。
    #[error("IO 错误: {0}")]
    Io(#[from] io::Error),

    /// 请求发出去了，但连接在收到响应前就没了。
    #[error("请求 {0} 未收到响应")]
    Canceled(String),
}

impl std::fmt::Display for RpcError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "[{}] {}", self.code, self.message)
    }
}

impl AdapterError {
    /// 把一个 JSON-RPC 错误**响应**归类。
    ///
    /// # 先说清楚这个函数管不着什么
    ///
    /// **模型侧的失败基本不走这里。** 实测（0.144.2）：拿一个无效 API key 发一轮，
    /// `turn/start` 会**正常返回成功**，401 是以一串 `error` **通知**送达的，带着
    /// `codexErrorInfo.responseStreamDisconnected.httpStatusCode = 401` 和 `willRetry`。
    /// 那条路见 [`crate::protocol::ErrorNotification`]，别指望这个函数。
    ///
    /// 所以这里只处理**请求响应**里的错误：主要是我们和上游对不上（方法不存在、参数不对），
    /// 以及少数握手期失败。分类靠标准 JSON-RPC 码，够用且不会因为上游改文案而失灵。
    ///
    /// 文本匹配只留了鉴权一条，因为握手期的鉴权失败确实只有文案可依。**不要往这里加更多
    /// 文本规则** —— 加之前先录一条 fixture 证明那条路真的会走到这儿。
    pub fn classify(err: RpcError) -> Self {
        // 标准 JSON-RPC 码：方法不存在 / 参数不对 —— 一定是我们和上游对不上了。
        if matches!(err.code, -32601 | -32602) {
            return AdapterError::Protocol(format!("{err}"));
        }

        let text = err.message.to_ascii_lowercase();
        if text.contains("401")
            || text.contains("unauthorized")
            || text.contains("invalid api key")
            || text.contains("not logged in")
        {
            return AdapterError::Auth(err);
        }

        AdapterError::Upstream(err)
    }

    /// 这个错误是否意味着连接已经不可用（而不是单次请求失败）。
    pub fn is_fatal(&self) -> bool {
        matches!(self, AdapterError::ProcessGone(_) | AdapterError::Io(_))
    }
}
