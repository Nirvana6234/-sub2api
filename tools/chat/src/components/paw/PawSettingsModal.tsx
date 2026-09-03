"use client";

import type { AgentApprovalPolicy, AgentSandbox } from "@/client/agent/session";
import { loadAgentSettings, saveAgentSettings } from "@/client/agent/settings";
import type {
  PawConfigData,
  PawSession,
  PawSubmitKey,
} from "@/client/paw/types";
import { useRef, useState } from "react";
import { PawDownloadIcon, PawEditIcon, PawUploadIcon } from "./PawIcons";
import { PawModal } from "./PawModal";

type PawTheme = "auto" | "light" | "dark";

interface PawSettingsModalProps {
  config: PawConfigData | null;
  session: PawSession;
  theme: PawTheme;
  submitKey: PawSubmitKey;
  promptCount: number;
  currentSelection: string;
  defaultSelection: string;
  selectionInvalid: boolean;
  /** 只在桌面端为 true——这一节是 agent 的设置，PWA 里没有 agent。 */
  agentDesktop: boolean;
  onThemeChange: (theme: PawTheme) => void;
  onSubmitKeyChange: (submitKey: PawSubmitKey) => void;
  onSaveDefaults: () => void;
  onOpenPrompts: () => void;
  onExportLocalData: () => void;
  onImportLocalData: (file: File) => void;
  onResetLocalData: () => void;
  onClose: () => void;
}

export function PawSettingsModal({
  config,
  session,
  theme,
  submitKey,
  promptCount,
  currentSelection,
  defaultSelection,
  selectionInvalid,
  agentDesktop,
  onThemeChange,
  onSubmitKeyChange,
  onSaveDefaults,
  onOpenPrompts,
  onExportLocalData,
  onImportLocalData,
  onResetLocalData,
  onClose,
}: PawSettingsModalProps) {
  const user = config?.user ?? session.user;
  const importRef = useRef<HTMLInputElement>(null);
  // 沙箱/审批策略只在"挂新工作目录时"读一次，不需要接进 usePawClient 的状态机——
  // 存取都在这两个 setter 里做完，读它的一侧（useAgentSession）会在下次挂目录时
  // 重新 load。
  const [agentSettings, setAgentSettings] = useState(() => loadAgentSettings());

  function updateAgentSettings(patch: Partial<typeof agentSettings>) {
    const next = { ...agentSettings, ...patch };
    setAgentSettings(next);
    saveAgentSettings(next);
  }

  return (
    <PawModal title="设置" onClose={onClose}>
      <div className="paw-settings-list">
        <div className="paw-settings-item">
          <div>
            <strong>外观</strong>
            <p>选择 Chat 的颜色主题。</p>
          </div>
          <select
            value={theme}
            onChange={(event) => onThemeChange(event.currentTarget.value as PawTheme)}
            aria-label="颜色主题"
          >
            <option value="auto">跟随系统</option>
            <option value="light">浅色</option>
            <option value="dark">深色</option>
          </select>
        </div>
        <div className="paw-settings-item">
          <div>
            <strong>发送快捷键</strong>
            <p>选择按下 Enter 时是否立即发送消息。</p>
          </div>
          <select
            value={submitKey}
            onChange={(event) =>
              onSubmitKeyChange(event.currentTarget.value as PawSubmitKey)
            }
            aria-label="发送快捷键"
          >
            <option value="enter">Enter 发送</option>
            <option value="shift-enter">Shift + Enter 发送</option>
            <option value="ctrl-enter">Ctrl + Enter 发送</option>
            <option value="alt-enter">Alt + Enter 发送</option>
          </select>
        </div>
        <div className="paw-settings-item paw-settings-item-stack">
          <div>
            <strong>默认选择</strong>
            <p>新对话会优先使用默认的分组、模型和推理强度。</p>
          </div>
          <div className="paw-settings-defaults">
            <div className="paw-settings-default-row">
              <span>当前默认</span>
              <strong>{defaultSelection}</strong>
            </div>
            <div className="paw-settings-default-row">
              <span>本次选择</span>
              <strong>{currentSelection}</strong>
            </div>
            <button
              type="button"
              className="paw-button"
              onClick={onSaveDefaults}
              disabled={selectionInvalid || currentSelection === "未选择可用模型"}
            >
              保存当前选择
            </button>
          </div>
        </div>
        <div className="paw-settings-item">
          <div>
            <strong>提示词</strong>
            <p>管理聊天输入框中的快捷提示词，共 {promptCount} 条自定义提示词。</p>
          </div>
          <button type="button" className="paw-button" onClick={onOpenPrompts}>
            <PawEditIcon width={15} height={15} />
            管理
          </button>
        </div>
        {agentDesktop ? (
          <>
            <div className="paw-settings-item">
              <div>
                <strong>agent 沙箱</strong>
                <p>
                  只约束<strong>不经审批就跑</strong>的命令——一旦你点同意，那条命令就
                  带着本程序的全部权限运行，这里的选择挡不住它。
                </p>
              </div>
              <select
                value={agentSettings.sandbox}
                onChange={(event) =>
                  updateAgentSettings({ sandbox: event.currentTarget.value as AgentSandbox })
                }
                aria-label="agent 沙箱"
              >
                <option value="read-only">只读</option>
                <option value="workspace-write">可写工作目录</option>
                <option value="danger-full-access">不设限</option>
              </select>
            </div>
            <div className="paw-settings-item">
              <div>
                <strong>agent 审批</strong>
                <p>新挂工作目录时用这个策略；已经挂上的对话不受这里的修改影响。</p>
              </div>
              <select
                value={agentSettings.approvalPolicy}
                onChange={(event) =>
                  updateAgentSettings({
                    approvalPolicy: event.currentTarget.value as AgentApprovalPolicy,
                  })
                }
                aria-label="agent 审批策略"
              >
                <option value="on-request">按需询问</option>
                <option value="untrusted">只放行可信命令</option>
                <option value="never">从不询问</option>
              </select>
            </div>
          </>
        ) : null}
        <div className="paw-settings-item paw-settings-item-stack">
          <div>
            <strong>本地数据</strong>
            <p>导出或导入对话、提示词和 Chat 的本地设置。</p>
          </div>
          <div className="paw-settings-actions">
            <button type="button" className="paw-button" onClick={onExportLocalData}>
              <PawDownloadIcon width={15} height={15} />
              导出
            </button>
            <button
              type="button"
              className="paw-button"
              onClick={() => importRef.current?.click()}
            >
              <PawUploadIcon width={15} height={15} />
              导入
            </button>
            <input
              ref={importRef}
              type="file"
              accept="application/json,.json"
              hidden
              onChange={(event) => {
                const file = event.currentTarget.files?.[0];
                event.currentTarget.value = "";
                if (file) onImportLocalData(file);
              }}
            />
            <button type="button" className="paw-button danger" onClick={onResetLocalData}>
              清空
            </button>
          </div>
        </div>
        <div className="paw-settings-item">
          <div>
            <strong>登录账号</strong>
            <p>{user?.email || "当前账号"}</p>
          </div>
          <span className="paw-settings-value">sub2api</span>
        </div>
        <div className="paw-settings-item">
          <div>
            <strong>连接方式</strong>
            <p>Chat 只使用当前 sub2api 账号的授权会话。</p>
          </div>
          <span className="paw-settings-value">JWT</span>
        </div>
        <div className="paw-settings-item paw-settings-item-stack">
          <div>
            <strong>关于 Chat</strong>
            <p>面向 sub2api 的中文 AI 对话客户端，聊天记录保存在当前设备。</p>
          </div>
          <div className="paw-settings-about">
            <span>版本</span>
            <strong>0.1.0</strong>
            <span>服务端路由</span>
            <strong>/api/v1/paw</strong>
          </div>
        </div>
      </div>
    </PawModal>
  );
}

export function PawShortcutsModal({ onClose }: { onClose: () => void }) {
  const shortcuts = [
    ["新建对话", "Ctrl / Cmd + Shift + O"],
    ["切换对话", "Alt / Ctrl + ↑ ↓"],
    ["聚焦输入框", "Shift + Esc"],
    ["复制最后一条回复", "Ctrl / Cmd + Shift + C"],
    ["复制最近代码", "Ctrl / Cmd + Shift + ;"],
    ["打开快捷键", "Ctrl / Cmd + /"],
    ["清空当前对话", "Ctrl / Cmd + Shift + Backspace"],
  ];

  return (
    <PawModal title="快捷键" onClose={onClose}>
      <div className="paw-shortcuts-list">
        {shortcuts.map(([label, keys]) => (
          <div className="paw-shortcut-row" key={label}>
            <span>{label}</span>
            <kbd>{keys}</kbd>
          </div>
        ))}
      </div>
    </PawModal>
  );
}
