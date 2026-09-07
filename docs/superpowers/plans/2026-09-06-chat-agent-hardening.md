# Chat Desktop Agent Hardening Plan

> 目标：补齐 Chat 桌面端在长时间使用、多会话和 Agent 重启场景下的可靠性边界。

## P0：本轮实施

- [x] 持久化 `agentThreadId`，应用重启后通过 `thread/resume` 继续原 Agent 会话。
- [x] 为恢复增加失败降级：旧 thread 不存在或已失效时清理旧绑定并新建 thread。
- [x] Agent 回合结束后在运行时清理 reasoning、工具面板和 `agent-output`，只保留最终正文。
- [x] 构造下一轮请求时再次过滤 `agent-output`，避免旧数据污染模型上下文。
- [x] 移除带 threadId 通知错误 fallback 到当前对话的行为。
- [x] 无法解析归属的审批请求：唯一候选会话自动归属；多候选或无候选时自动拒绝并展示诊断。

## P1：下一步

- [ ] 统一附件发送成功、登出、切换对话时的 Blob URL 和 File 映射清理。
- [ ] 为历史附件增加可恢复的本地缓存或明确不可恢复状态。
- [ ] 将通知面板中的高频 Known 通知改为语义化视图，保留原始 payload 折叠查看。
- [ ] 增加 Chat 前端单元测试和桌面 smoke 测试。
- [ ] 增加真实通知 payload、字段缺失和多会话交错事件测试。

## 验收标准

1. 重启 Chat 后，已绑定工作目录且存在 `agentThreadId` 的对话可以继续原 Agent 上下文。
2. Agent 回合完成后，内存消息和 localStorage 投影都不再包含 reasoning、工具面板和命令输出。
3. 两个对话并发时，带 threadId 的通知不会显示到另一个对话。
4. 任意审批请求都不会因为前端无法解析 threadId 而无限等待。
5. `npm run typecheck`、`npm run build`、`cargo check` 和相关 Rust 测试通过。

## P1 implementation status (2026-09-06)

- [x] Attachment Blob cache backed by IndexedDB, with best-effort fallback when unavailable.
- [x] Restore historical attachment `File` mappings and image previews after reload/import.
- [x] Track Blob URL references and release them on composer replacement, message/conversation deletion, import, reset, and unmount.
- [x] Mark unavailable historical attachments in the conversation view.
- [x] Render agent notifications as semantic cards with collapsed raw payloads.
- [x] Run `npm run typecheck`, `npm run build`, and `cargo check --workspace`.
