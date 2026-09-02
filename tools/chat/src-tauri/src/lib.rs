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
        ])
        .run(tauri::generate_context!())
        .expect("error while running Chat");
}
