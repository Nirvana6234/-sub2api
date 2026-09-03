/**
 * agent 的沙箱与审批策略默认值。
 *
 * 这两个是**设置**，不是每次"挂工作目录"都要重新选的东西——用户已经纠正过一次：
 * 设置相关的东西该在设置弹窗里，不该占聊天工具条的位置。这里只管存取，
 * 界面在 `PawSettingsModal.tsx` 里。
 */
import type { AgentApprovalPolicy, AgentSandbox } from "./session";

const KEY = "cofly-agent-settings:v1";

export interface AgentSettings {
  sandbox: AgentSandbox;
  approvalPolicy: AgentApprovalPolicy;
}

const DEFAULTS: AgentSettings = {
  sandbox: "workspace-write",
  approvalPolicy: "on-request",
};

export function loadAgentSettings(): AgentSettings {
  try {
    const raw = window.localStorage.getItem(KEY);
    if (!raw) return DEFAULTS;
    const parsed = JSON.parse(raw) as Partial<AgentSettings>;
    return {
      sandbox: parsed.sandbox ?? DEFAULTS.sandbox,
      approvalPolicy: parsed.approvalPolicy ?? DEFAULTS.approvalPolicy,
    };
  } catch {
    return DEFAULTS;
  }
}

export function saveAgentSettings(settings: AgentSettings): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(settings));
  } catch {
    /* 存不下就用默认值，不影响其余功能 */
  }
}
