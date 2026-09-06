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

use agent::dto::{SendParams, StartParams, StartedThread, UiDecision};
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

/// 起一条新 thread（不是"起会话"了——引擎懒起、之后被所有对话共用）。
#[tauri::command]
async fn agent_start(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    params: StartParams,
) -> Result<StartedThread, BridgeError> {
    bridge.start(params).await
}

/// 发一轮提问。**必须点名 `threadId`**——多会话并发之后不再有"当前那一个"的概念，
/// `model`/`reasoning` 也每轮都传，见 `SendParams` 的文档。
#[tauri::command]
async fn agent_send(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    params: SendParams,
) -> Result<(), BridgeError> {
    bridge.send(params).await
}

/// 打断某条 thread 上正在跑的那一轮，不影响别的对话。
#[tauri::command]
async fn agent_interrupt(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    thread_id: String,
) -> Result<(), BridgeError> {
    bridge.interrupt(&thread_id).await
}

#[tauri::command]
async fn agent_answer(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    request_id: String,
    decision: UiDecision,
) -> Result<(), BridgeError> {
    bridge.answer(request_id, decision).await
}

/// 归档一条 thread——**只影响它自己**，引擎和别的对话都还在跑。
/// 和 `agent_stop`（杀整个引擎）是两回事，别用混。
#[tauri::command]
async fn agent_end_thread(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    thread_id: String,
) -> Result<(), BridgeError> {
    bridge.end_thread(&thread_id).await
}

#[tauri::command]
async fn agent_compact(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    thread_id: String,
) -> Result<(), BridgeError> {
    bridge.compact(&thread_id).await
}

/// 停掉**整个引擎**：杀进程、抹凭据，**影响所有还挂着的对话**。
/// 是给"整个应用要退出/登出 agent"这类场景用的，不是"结束当前这个对话"该调的东西。
#[tauri::command]
async fn agent_stop(bridge: tauri::State<'_, Arc<AgentBridge>>) -> Result<(), BridgeError> {
    bridge.stop().await
}

/// 把当前账号会话推给转发层。
///
/// 前端在登录成功、以及每次刷新拿到新 access token 之后调一次；登出时传 `null`。
/// **刷新逻辑只留在前端**（`api.ts` 里已经有了）——在 Rust 里再实现一份，
/// 两份迟早对不上，而对不上的表现是「偶尔莫名其妙要重新登录」。
///
/// 会话正跑着也能调：下一条请求就用新的，不必重启 codex。
#[tauri::command]
async fn agent_set_session_token(
    bridge: tauri::State<'_, Arc<AgentBridge>>,
    token: Option<String>,
) -> Result<(), BridgeError> {
    bridge.set_session_token(token).await;
    Ok(())
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

/// 随包的 codex 在资源里的位置。
#[cfg(windows)]
const BUNDLED_CODEX: &str = "codex/bin/codex-app-server.exe";
#[cfg(not(windows))]
const BUNDLED_CODEX: &str = "codex/bin/codex-app-server";

/// 定出 codex 二进制和程序数据目录 —— **两条都不经过前端**。
///
/// `COFLY_CODEX_BINARY` 这个环境变量是给开发和端到端用的。它能存在，是因为
/// **环境变量由启动这个进程的人设，网页设不了** —— 这和让渲染进程在
/// `invoke` 里点名一个路径是两回事。
///
/// 这里刻意**不因为找不到二进制就失败**：那会让整个应用起不来，而用户可能只是
/// 想用 Chat 那一半。真正起 agent 的时候 `Engine::spawn` 会带着路径报错，
/// 那才是该说这句话的时机。
fn resolve_agent_paths(app: &tauri::AppHandle) -> agent::AgentPaths {
    use tauri::Manager;

    let exe_dir = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(std::path::Path::to_path_buf))
        .unwrap_or_else(|| std::path::PathBuf::from("."));

    let app_dir = app
        .path()
        .app_data_dir()
        .unwrap_or_else(|_| exe_dir.join("cofly-workbench"));

    let codex_binary = std::env::var_os("COFLY_CODEX_BINARY")
        .map(std::path::PathBuf::from)
        .or_else(|| {
            app.path()
                .resolve(BUNDLED_CODEX, tauri::path::BaseDirectory::Resource)
                .ok()
        })
        .unwrap_or_else(|| exe_dir.join(BUNDLED_CODEX));

    agent::AgentPaths {
        app_dir,
        codex_binary,
    }
}

pub fn run() {
    tauri::Builder::default()
        // 选工作目录用。**只加了它一个** —— v2 里插件命令是要在 capabilities 里
        // 显式放行的，多放一个就多一个前端能碰的原生能力。
        .plugin(tauri_plugin_dialog::init())
        .setup(|app| {
            let sink = Arc::new(TauriSink(app.handle().clone()));
            let paths = resolve_agent_paths(app.handle());
            app.manage(Arc::new(AgentBridge::new(sink, paths)));

            // Windows 上一个有记录的 WebView2 坑：点标题栏最大化按钮（或双击标题栏）
            // 触发的 resize，WebView2 的内部 HWND 有时不会跟着重绘，要等下一次
            // 交互（拖动/移动窗口）才会醒过来——表现就是"窗口大了，但内容区还是
            // 老尺寸"。用同一个尺寸再 `set_size` 一次是社区里报出来能顶住的办法：
            // 不改变任何正常场景的行为（尺寸没变），只是给 WebView2 一次强制
            // 重新同步的机会。
            if let Some(window) = app.get_webview_window("main") {
                let window_for_resize = window.clone();
                window.on_window_event(move |event| {
                    if let tauri::WindowEvent::Resized(size) = event {
                        let _ = window_for_resize.set_size(*size);
                    }
                });
            }

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            agent_start,
            agent_send,
            agent_interrupt,
            agent_answer,
            agent_end_thread,
            agent_compact,
            agent_stop,
            agent_set_session_token,
            agent_is_running,
            agent_device_identity,
            agent_pick_key,
            agent_key_name,
            agent_key_needs_renewal,
        ])
        .run(tauri::generate_context!())
        .expect("error while running Chat");
}
