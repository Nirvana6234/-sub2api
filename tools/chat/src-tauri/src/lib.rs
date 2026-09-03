//! 共飞 AI 工作台的 Tauri 壳。
//!
//! **这里是整个 agent 链路上唯一认识 Tauri 的地方。** 往下三层都不认识：
//!
//! ```text
//! 前端（只认识 agent::dto 里的类型）
//!   ↕ invoke / emit          ← 就这一层
//! chat_lib::agent::AgentBridge（不认识 Tauri）
//!   ↕
//! codex-host（进程与笼子）  +  codex-adapter（协议与会话）
//!   ↕ JSONL over stdio
//! codex app-server
//! ```
//!
//! 命令函数本身都只有三行 —— 逻辑全在桥里，所以集成测试能直接驱动桥，
//! 不必去绕 Tauri 的 invoke 机制。

pub mod agent;

use std::sync::Arc;

use tauri::{AppHandle, Emitter, Manager};

use agent::dto::{StartParams, StartedThread, UiDecision};
use agent::{AgentBridge, BridgeError, EventSink};

/// 所有 agent 事件都从这一个通道出去。前端 `listen("agent://event")` 收。
pub const AGENT_EVENT: &str = "agent://event";

/// 把桥的事件转成 Tauri 事件。
struct TauriSink(AppHandle);

impl EventSink for TauriSink {
    fn emit(&self, event: agent::dto::UiEvent) {
        // 发不出去只可能是窗口已经没了 —— 那时也没人在听了。
        let _ = self.0.emit(AGENT_EVENT, event);
    }
}

#[tauri::command]
async fn agent_start(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    params: StartParams,
) -> Result<StartedThread, BridgeError> {
    bridge.start(params).await
}

#[tauri::command]
async fn agent_send(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    text: String,
) -> Result<(), BridgeError> {
    bridge.send(text).await
}

#[tauri::command]
async fn agent_interrupt(bridge: tauri::State<'_, Arc<AgentBridge>>) -> Result<(), BridgeError> {
    bridge.interrupt().await
}

#[tauri::command]
async fn agent_answer(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    request_id: String,
    decision: UiDecision,
) -> Result<(), BridgeError> {
    bridge.answer(request_id, decision).await
}

#[tauri::command]
async fn agent_stop(bridge: tauri::State<'_, Arc<AgentBridge>>) -> Result<(), BridgeError> {
    bridge.stop().await
}

/// 这台机器上这一次安装的身份 —— 前端拿它拼托管 key 的名字。
///
/// 命名规则照搬小白端：机器名让一个账号的几台机器各持各的租约，
/// 安装 ID 让重装之后不去续期一个说不清来历的旧租约。
#[tauri::command]
async fn agent_device_identity(
    app_dir: String,
) -> Result<codex_host::DeviceIdentity, BridgeError> {
    codex_host::DeviceIdentity::load_or_create(&app_dir)
        .map_err(|e| BridgeError::Host(e.to_string()))
}

/// 从一批 key 的**元数据**里挑出该用的那把，返回它的 id。
///
/// **传进来的不含密钥** —— 选择只需要 id / 名字 / 分组 / 过期时间。前端拿到 id
/// 再去自己那份列表里取值，密钥就少走一趟 IPC，也就少一处可能被日志带出去。
///
/// 规则本身（尤其「无过期时间排最后」「分组看数据不看名字」）在
/// `codex_host::keylease` 里，有测试盯着 —— 那几条写反了不会报错，只会安静地错。
#[tauri::command]
async fn agent_pick_key(
    keys: Vec<codex_host::KeyMeta>,
    identity: codex_host::DeviceIdentity,
    group_id: Option<i64>,
) -> Result<Option<i64>, BridgeError> {
    Ok(codex_host::pick_current(&keys, &identity, group_id))
}

/// 这次安装在某个分组下该用的 key 名字。
#[tauri::command]
async fn agent_key_name(
    identity: codex_host::DeviceIdentity,
    group_id: Option<i64>,
) -> Result<String, BridgeError> {
    Ok(codex_host::key_name(&identity, group_id))
}

/// 这把 key 该不该续期。
#[tauri::command]
async fn agent_key_needs_renewal(
    key: codex_host::KeyMeta,
    now_ms: i64,
    renew_when_days_left: i64,
) -> Result<bool, BridgeError> {
    Ok(codex_host::needs_renewal(&key, now_ms, renew_when_days_left))
}

#[tauri::command]
async fn agent_is_running(bridge: tauri::State<'_, Arc<AgentBridge>>) -> Result<bool, BridgeError> {
    Ok(bridge.is_running().await)
}

pub fn run() {
    tauri::Builder::default()
        .setup(|app| {
            let sink = Arc::new(TauriSink(app.handle().clone()));
            app.manage(Arc::new(AgentBridge::new(sink)));
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            agent_start,
            agent_send,
            agent_interrupt,
            agent_answer,
            agent_stop,
            agent_is_running,
            agent_device_identity,
            agent_pick_key,
            agent_key_name,
            agent_key_needs_renewal,
        ])
        .run(tauri::generate_context!())
        .expect("error while running Chat");
}
