//! JSONL over stdio 的收发与请求/响应配对。
//!
//! **刻意不认识「进程」。** 收发面架在 `AsyncRead`/`AsyncWrite` 上，所以单测能拿录制的
//! 报文喂一个 `Cursor` 跑完整条链路，不需要真的起 codex。真进程的 `ChildStdin`/`ChildStdout`
//! 由宿主在 A4 里接进来 —— 那时这一层不用改。

use std::collections::HashMap;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;

use serde_json::Value;
use tokio::io::{AsyncBufReadExt, AsyncWrite, AsyncWriteExt, BufReader};
use tokio::sync::{mpsc, oneshot, Mutex};

use crate::error::{AdapterError, RpcError};
use crate::protocol::{ClientRequest, Notification, RequestId, ServerMessage, ServerRequest};

/// 解析一行 JSONL。
///
/// 判定顺序照 JSON-RPC：有 `method` 且有 `id` 是服务端请求，只有 `method` 是通知，
/// 有 `id` 且有 `result`/`error` 是对我们请求的响应。
pub fn decode_line(line: &str) -> Result<ServerMessage, AdapterError> {
    let value: Value = serde_json::from_str(line)
        .map_err(|e| AdapterError::Protocol(format!("不是合法 JSON: {e}: {}", truncate(line, 200))))?;

    let id = value
        .get("id")
        .and_then(|v| serde_json::from_value::<RequestId>(v.clone()).ok());
    let method = value.get("method").and_then(Value::as_str).map(str::to_owned);
    let params = value.get("params").cloned().unwrap_or(Value::Null);

    match (method, id) {
        (Some(method), Some(id)) => Ok(ServerMessage::Request(ServerRequest::project(id, method, params))),
        (Some(method), None) => Ok(ServerMessage::Notification(Notification::project(method, params))),
        (None, id) => {
            if let Some(err) = value.get("error") {
                let error: RpcError = serde_json::from_value(err.clone()).map_err(|e| {
                    AdapterError::Protocol(format!("error 对象解不开: {e}: {}", truncate(line, 200)))
                })?;
                Ok(ServerMessage::Error { id, error })
            } else if let Some(id) = id {
                Ok(ServerMessage::Response {
                    id,
                    result: value.get("result").cloned().unwrap_or(Value::Null),
                })
            } else {
                Err(AdapterError::Protocol(format!(
                    "既没有 method 也没有 id: {}",
                    truncate(line, 200)
                )))
            }
        }
    }
}

/// 截断到 `n` 字节，但不切在 UTF-8 字符中间（报文里有中文，切一半会 panic）。
fn truncate(s: &str, n: usize) -> String {
    if s.len() <= n {
        return s.to_owned();
    }
    let mut end = n;
    while end > 0 && !s.is_char_boundary(end) {
        end -= 1;
    }
    format!("{}…", &s[..end])
}

/// 收到的、需要上层处理的东西。
#[derive(Debug)]
pub enum Incoming {
    /// 服务端问我们（审批等）。**不答复 turn 会卡住。**
    Request(ServerRequest),
    Notification(Notification),
    /// 有一行解不开。
    ///
    /// 单独一个变体，而不是伪造一条通知：伪造的方法名会和上游真实方法名共用命名空间，
    /// 消费方分不出「codex 这么说的」还是「我们自己编的」。
    ///
    /// 收到它基本等于**上游版本漂移了**。不该拖垮连接，但也**绝不能静默吞掉**。
    DecodeError { line: String, error: String },
}

/// 待响应表：请求 id → 等着它的那个 future。读写两半共享。
pub type Pending = Arc<Mutex<HashMap<RequestId, oneshot::Sender<Result<Value, AdapterError>>>>>;

/// 新建一张空的待响应表。只有 [`Client::new`] 和测试需要它。
pub fn new_pending() -> Pending {
    Arc::new(Mutex::new(HashMap::new()))
}

/// 发送端：持有写半边和待响应表。可跨任务共享（`Arc<Client<W>>`）。
pub struct Client<W> {
    writer: Mutex<W>,
    pending: Pending,
    next_id: AtomicI64,
}

impl<W: AsyncWrite + Unpin + Send> Client<W> {
    pub fn new(writer: W) -> (Arc<Self>, Pending) {
        let pending = new_pending();
        let client = Arc::new(Client {
            writer: Mutex::new(writer),
            pending: Arc::clone(&pending),
            next_id: AtomicI64::new(0),
        });
        (client, pending)
    }

    /// 发一个请求并等响应。
    pub async fn request(&self, req: ClientRequest) -> Result<Value, AdapterError> {
        let id = RequestId::Num(self.next_id.fetch_add(1, Ordering::Relaxed));
        let (tx, rx) = oneshot::channel();
        self.pending.lock().await.insert(id.clone(), tx);

        let frame = serde_json::json!({
            "jsonrpc": "2.0",
            "id": id,
            "method": req.method(),
            "params": req.params(),
        });

        if let Err(e) = self.write_frame(&frame).await {
            self.pending.lock().await.remove(&id);
            return Err(e);
        }

        rx.await.map_err(|_| AdapterError::Canceled(id.to_string()))?
    }

    /// 发一条单向通知（目前只有 `initialized`）。
    pub async fn notify(&self, method: &str, params: Value) -> Result<(), AdapterError> {
        self.write_frame(&serde_json::json!({
            "jsonrpc": "2.0",
            "method": method,
            "params": params,
        }))
        .await
    }

    /// 答复服务端请求。**每个 [`Incoming::Request`] 都必须走到这里**，
    /// 包括我们不认识的那些 —— 否则 turn 会一直等。
    pub async fn respond(&self, id: &RequestId, result: Value) -> Result<(), AdapterError> {
        self.write_frame(&serde_json::json!({
            "jsonrpc": "2.0",
            "id": id,
            "result": result,
        }))
        .await
    }

    async fn write_frame(&self, frame: &Value) -> Result<(), AdapterError> {
        let mut line = serde_json::to_vec(frame)
            .map_err(|e| AdapterError::Protocol(format!("请求序列化失败: {e}")))?;
        line.push(b'\n');
        let mut w = self.writer.lock().await;
        w.write_all(&line)
            .await
            .map_err(|e| AdapterError::ProcessGone(e.to_string()))?;
        w.flush()
            .await
            .map_err(|e| AdapterError::ProcessGone(e.to_string()))
    }
}

/// 读半边：把每行分派到待响应表或 `incoming`。
///
/// 返回时说明流已经结束（进程退出 / 管道关闭）。**所有仍在等的请求都会被叫醒**，
/// 拿到 [`AdapterError::ProcessGone`] —— 悬着的 future 比崩溃更难查。
pub async fn pump<R>(
    reader: R,
    pending: Pending,
    incoming: mpsc::Sender<Incoming>,
) -> Result<(), AdapterError>
where
    R: tokio::io::AsyncRead + Unpin,
{
    let mut lines = BufReader::new(reader).lines();
    let mut result = Ok(());

    loop {
        let line = match lines.next_line().await {
            Ok(Some(line)) => line,
            Ok(None) => break,
            Err(e) => {
                result = Err(AdapterError::ProcessGone(e.to_string()));
                break;
            }
        };
        if line.trim().is_empty() {
            continue;
        }

        match decode_line(&line) {
            Ok(ServerMessage::Response { id, result: value }) => {
                if let Some(tx) = pending.lock().await.remove(&id) {
                    let _ = tx.send(Ok(value));
                }
                // 没人等的响应直接丢掉：说明请求方已经放弃，不是错误。
            }
            Ok(ServerMessage::Error { id, error }) => {
                let classified = AdapterError::classify(error);
                match id {
                    Some(id) => {
                        if let Some(tx) = pending.lock().await.remove(&id) {
                            let _ = tx.send(Err(classified));
                        }
                    }
                    // 没有 id 的错误是连接级的，向上抛。
                    None => {
                        result = Err(classified);
                        break;
                    }
                }
            }
            Ok(ServerMessage::Request(req)) => {
                if incoming.send(Incoming::Request(req)).await.is_err() {
                    break; // 上层不收了
                }
            }
            Ok(ServerMessage::Notification(note)) => {
                if incoming.send(Incoming::Notification(note)).await.is_err() {
                    break;
                }
            }
            Err(e) => {
                // 单行解不开不该拖垮整条连接，但要原样抛给上层去记日志。
                let event = Incoming::DecodeError {
                    line: truncate(&line, 2000),
                    error: e.to_string(),
                };
                if incoming.send(event).await.is_err() {
                    break;
                }
            }
        }
    }

    // 流结束：叫醒所有还在等的请求。
    let mut guard = pending.lock().await;
    for (id, tx) in guard.drain() {
        let _ = tx.send(Err(AdapterError::ProcessGone(format!("流已结束，请求 {id} 无响应"))));
    }

    result
}
