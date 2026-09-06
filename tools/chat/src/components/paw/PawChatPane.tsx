"use client";

import {
  Fragment,
  type ChangeEvent,
  type KeyboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  PawArrowDownIcon,
  PawBreakIcon,
  PawBrainIcon,
  PawCheckIcon,
  PawCloseIcon,
  PawCopyIcon,
  PawDownloadIcon,
  PawEditIcon,
  PawFolderIcon,
  PawKeyboardIcon,
  PawLayersIcon,
  PawMaximizeIcon,
  PawMenuIcon,
  PawMinimizeIcon,
  PawMoonIcon,
  PawPaperclipIcon,
  PawPromptIcon,
  PawRulerIcon,
  PawRefreshIcon,
  PawRobotIcon,
  PawSettingsIcon,
  PawShieldAlertIcon,
  PawStopIcon,
  PawSunIcon,
  PawTrashIcon,
  PawSendIcon,
  PawPinIcon,
  PawVolumeIcon,
  PawVolumeOffIcon,
} from "./PawIcons";
import { PawAnnouncementCenter } from "./PawAnnouncementCenter";
import { PawMarkdown } from "./PawMarkdown";
import type { AgentApprovalUiMode, ApprovalRequest } from "@/client/agent/session";
import type {
  PawAgentApprovalReview,
  PawAgentFileChange,
  PawAgentFileSearch,
  PawAgentNotification,
  PawAgentPanels,
  PawAttachment,
  PawConfigData,
  PawConversation,
  PawGroup,
  PawImageSize,
  PawModel,
  PawPrompt,
  PawSubmitKey,
} from "@/client/paw/types";

type PawTheme = "auto" | "light" | "dark";

interface PawChatPaneProps {
  config: PawConfigData | null;
  configBusy: boolean;
  configError: string | null;
  notice: string | null;
  selectionInvalid: boolean;
  fileBusy: boolean;
  selectedGroupId: number | null;
  selectedModelId: string;
  selectedReasoning: string;
  submitKey: PawSubmitKey;
  prompts: PawPrompt[];
  currentGroup: PawGroup | undefined;
  currentModel: PawModel | undefined;
  activeConversation: PawConversation | null;
  draft: string;
  attachments: PawAttachment[];
  sending: boolean;
  editingMessageId: string | null;
  canSend: boolean;
  imageSize: PawImageSize;
  imageSizes: PawImageSize[];
  theme: PawTheme;
  isFullscreen: boolean;
  onNoticeChange: (value: string | null) => void;
  onDraftChange: (value: string) => void;
  onChangeGroup: (value: number) => void;
  onChangeModel: (value: string) => void;
  onChangeReasoning: (value: string) => void;
  onChangeImageSize: (value: PawImageSize) => void;
  onRefreshConfig: () => void;
  onSaveDefaults: () => void;
  onFileChange: (event: ChangeEvent<HTMLInputElement>) => void;
  onPasteFiles: (files: File[]) => void;
  onSend: () => void;
  onStop: () => void;
  onRemoveAttachment: (id: string) => void;
  onOpenSidebar: () => void;
  onOpenSettings: () => void;
  onOpenShortcuts: () => void;
  onCompact: () => void;
  onToggleTheme: () => void;
  onToggleFullscreen: () => void;
  onNewConversation: () => void;
  onClearConversation: () => void;
  onRestoreContext: () => void;
  onExportConversation: () => void;
  onCopyMessage: (messageId: string) => void;
  onTogglePinMessage: (messageId: string) => void;
  onDeleteMessage: (messageId: string) => void;
  onEditMessage: (messageId: string) => void;
  onRetryMessage: (messageId: string) => void;
  onCancelEdit: () => void;
  onRenameConversation: (id: string, title: string) => void;
  getSelectionSummary: (
    config: PawConfigData | null,
    groupId: number | null,
    modelId: string,
    reasoning: string,
  ) => string;

  // ── agent（挂在当前对话上的工作目录能力）──────────────────────────
  // 只在桌面端出现（`agentDesktop`）。这不是一个独立模式：挂上工作目录之后，
  // 这个对话的发送就走 codex，界面还是这一个 PawChatPane。
  /** 是不是跑在桌面壳里；PWA 里恒为 false，agent 相关的 chip 都不渲染。 */
  agentDesktop: boolean;
  /** 当前对话挂没挂工作目录（选了目录就算挂，不管锁没锁）。 */
  agentArmed: boolean;
  agentCwd: string | null;
  /** `agentCwd` 锁没锁——发过第一条消息之后才锁，之前可以随便重选。 */
  agentCwdLocked: boolean;
  /** 审批模式：`review`（需要审核）/ `full`（完全控制，默认）。随时可切。 */
  agentApprovalMode: AgentApprovalUiMode;
  /** 正在起/发送这一轮；用它禁用输入框。 */
  agentBusy: boolean;
  agentCompacting: boolean;
  /** 有命令正在跑——命令输出要等跑完才落进正文，这段时间界面看着容易像卡住了。 */
  agentRunningTool: boolean;
  /** 正在重试——只存最新一条。`null` 表示没卡在重试上。 */
  agentRetrying: { message: string } | null;
  agentApprovals: ApprovalRequest[];
  agentWaitingOnApproval: boolean;
  agentError: string | null;
  /** 未挂目录时点这个 chip：直接弹出系统目录选择器。已挂目录时是 no-op。 */
  onPickAgentDirectory: () => void;
  onSetAgentApprovalMode: (mode: AgentApprovalUiMode) => void;
  onAnswerAgentApproval: (requestId: string, approve: boolean) => void;
}

function roleLabel(role: PawConversation["messages"][number]["role"]): string {
  if (role === "user") return "你";
  if (role === "assistant") return "";
  return "系统";
}

function formatTime(value: number): string {
  return new Date(value).toLocaleString("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function stringifyAgentValue(value: unknown): string {
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
}

function fileChangeEntries(change: PawAgentFileChange): Array<{
  path: string;
  kind: string;
  diff: string;
}> {
  const values = Array.isArray(change.changes) ? change.changes : [change.changes];
  return values
    .filter((value) => value !== undefined && value !== null)
    .map((value) => {
      if (!value || typeof value !== "object") {
        return { path: "文件变更", kind: "update", diff: String(value) };
      }
      const entry = value as Record<string, unknown>;
      const kindValue = entry.kind;
      const kind =
        typeof kindValue === "string"
          ? kindValue
          : kindValue && typeof kindValue === "object" && "type" in kindValue
            ? String((kindValue as Record<string, unknown>).type)
            : "update";
      return {
        path: typeof entry.path === "string" ? entry.path : "文件变更",
        kind,
        diff: typeof entry.diff === "string" ? entry.diff : stringifyAgentValue(value),
      };
    });
}

function agentNotificationLabel(method: string): string {
  switch (method) {
    case "thread/closed":
      return "会话已关闭";
    case "thread/deleted":
      return "会话已删除";
    case "thread/unarchived":
      return "会话已取消归档";
    case "thread/compacted":
      return "上下文已压缩";
    case "thread/reverted":
      return "会话已回退";
    case "thread/settings/updated":
      return "会话设置已更新";
    case "thread/queue/changed":
      return "会话队列已更新";
    case "thread/name/updated":
      return "会话名称已更新";
    case "thread/project/updated":
      return "会话项目已更新";
    case "mcpServer/oauthLogin/completed":
      return "MCP OAuth 登录";
    case "windowsSandbox/setupCompleted":
      return "Windows 沙箱";
    default:
      return method;
  }
}

function agentFileSearchEntries(search: PawAgentFileSearch): Array<{
  path: string;
  fileName: string;
  matchType: string;
}> {
  return search.files.map((file) => {
    const entry = file && typeof file === "object" ? (file as Record<string, unknown>) : {};
    return {
      path: typeof entry.path === "string" ? entry.path : "未知路径",
      fileName: typeof entry.file_name === "string" ? entry.file_name : "",
      matchType:
        typeof entry.match_type === "string"
          ? entry.match_type
          : typeof entry.matchType === "string"
            ? entry.matchType
            : "match",
    };
  });
}

function approvalReviewSummary(review: PawAgentApprovalReview): string {
  const raw =
    review.raw && typeof review.raw === "object"
      ? (review.raw as Record<string, unknown>)
      : {};
  const data =
    raw.review && typeof raw.review === "object"
      ? (raw.review as Record<string, unknown>)
      : {};
  const status = typeof data.status === "string" ? data.status : "reviewing";
  const risk = typeof data.riskLevel === "string" ? `，风险：${data.riskLevel}` : "";
  return `${status}${risk}`;
}

function AgentPanels({ panels }: { panels?: PawAgentPanels }) {
  const [planOpen, setPlanOpen] = useState(true);
  if (!panels) return null;
  const fileChanges = Object.values(panels.fileChanges ?? {});
  const notifications = panels.notifications ?? [];
  const fileSearches = Object.values(panels.fileSearches ?? {});
  const approvalReviews = Object.values(panels.approvalReviews ?? {});
  const hasContent =
    Boolean(panels.plan) ||
    Boolean(panels.diff) ||
    fileChanges.length > 0 ||
    Boolean(panels.terminalInteractions?.length) ||
    Boolean(panels.moderationMetadata?.length) ||
    notifications.length > 0 ||
    fileSearches.length > 0 ||
    approvalReviews.length > 0;
  if (!hasContent) return null;

  return (
    <div className="paw-agent-panels">
      {panels.plan ? (
        <details
          className="paw-agent-panel"
          open={planOpen}
          onToggle={(event) => setPlanOpen(event.currentTarget.open)}
        >
          <summary>
            <span>执行计划</span>
            <span className="paw-agent-panel-meta">
              {panels.plan.steps.length > 0
                ? `${panels.plan.steps.length} 步`
                : panels.plan.delta
                  ? "生成中"
                  : "待更新"}
            </span>
          </summary>
          <div className="paw-agent-panel-body">
            {panels.plan.explanation ? (
              <p className="paw-agent-plan-explanation">{panels.plan.explanation}</p>
            ) : null}
            {panels.plan.steps.length > 0 ? (
              <ol className="paw-agent-plan-list">
                {panels.plan.steps.map((step, index) => {
                  const entry =
                    step && typeof step === "object"
                      ? (step as Record<string, unknown>)
                      : null;
                  const status = String(entry?.status ?? "pending");
                  const label = String(entry?.step ?? step ?? "");
                  return (
                    <li key={`${index}-${label}`} data-status={status}>
                      <span className="paw-agent-plan-status">
                        {status === "completed" ? "✓" : status === "inProgress" ? "•" : "○"}
                      </span>
                      <span>{label}</span>
                    </li>
                  );
                })}
              </ol>
            ) : null}
            {panels.plan.delta ? (
              <pre className="paw-agent-panel-pre">{panels.plan.delta}</pre>
            ) : null}
          </div>
        </details>
      ) : null}

      {panels.diff ? (
        <details className="paw-agent-panel">
          <summary>
            <span>最新 diff</span>
            <span className="paw-agent-panel-meta">
              {panels.diff.split("\n").length} 行
            </span>
          </summary>
          <pre className="paw-agent-panel-pre paw-agent-diff">{panels.diff}</pre>
        </details>
      ) : null}

      {fileChanges.length > 0 ? (
        <details className="paw-agent-panel">
          <summary>
            <span>文件变更</span>
            <span className="paw-agent-panel-meta">{fileChanges.length} 项</span>
          </summary>
          <div className="paw-agent-panel-body paw-agent-file-changes">
            {fileChanges.map((change) => (
              <div className="paw-agent-file-change" key={change.itemId}>
                {fileChangeEntries(change).map((entry, index) => (
                  <div className="paw-agent-file-change-entry" key={`${entry.path}-${index}`}>
                    <div className="paw-agent-file-change-head">
                      <code>{entry.path}</code>
                      <span>{entry.kind}</span>
                    </div>
                    <pre className="paw-agent-panel-pre paw-agent-diff">{entry.diff}</pre>
                  </div>
                ))}
                {change.output ? (
                  <pre className="paw-agent-panel-pre paw-agent-output-text">
                    {change.output}
                  </pre>
                ) : null}
              </div>
            ))}
          </div>
        </details>
      ) : null}

      {panels.terminalInteractions?.length ? (
        <details className="paw-agent-panel">
          <summary>
            <span>终端交互</span>
            <span className="paw-agent-panel-meta">
              {panels.terminalInteractions.length} 条
            </span>
          </summary>
          <div className="paw-agent-panel-body paw-agent-terminal-list">
            {panels.terminalInteractions.map((interaction, index) => (
              <div
                className="paw-agent-terminal-item"
                key={`${interaction.itemId}-${index}`}
              >
                <div className="paw-agent-file-change-head">
                  <code>进程 {interaction.processId}</code>
                  <span>{formatTime(interaction.createdAt)}</span>
                </div>
                <pre className="paw-agent-panel-pre">{interaction.stdin}</pre>
              </div>
            ))}
          </div>
        </details>
      ) : null}

      {panels.moderationMetadata?.length ? (
        <details className="paw-agent-panel">
          <summary>
            <span>内容审核元数据</span>
            <span className="paw-agent-panel-meta">
              {panels.moderationMetadata.length} 条
            </span>
          </summary>
          <div className="paw-agent-panel-body">
            {panels.moderationMetadata.map((metadata, index) => (
              <pre className="paw-agent-panel-pre" key={index}>
                {stringifyAgentValue(metadata)}
              </pre>
            ))}
          </div>
        </details>
      ) : null}

      {notifications.length > 0 ? (
        <details className="paw-agent-panel">
          <summary>
            <span>会话通知</span>
            <span className="paw-agent-panel-meta">{notifications.length} 条</span>
          </summary>
          <div className="paw-agent-panel-body paw-agent-notification-list">
            {notifications.map((notification: PawAgentNotification, index) => (
              <div className="paw-agent-notification-item" key={`${notification.method}-${index}`}>
                <div className="paw-agent-file-change-head">
                  <span>{agentNotificationLabel(notification.method)}</span>
                  <code>{notification.method}</code>
                </div>
                <p className="paw-agent-notification-message">{notification.message}</p>
                <pre className="paw-agent-panel-pre">{stringifyAgentValue(notification.raw)}</pre>
              </div>
            ))}
          </div>
        </details>
      ) : null}

      {fileSearches.length > 0 ? (
        <details className="paw-agent-panel">
          <summary>
            <span>文件搜索</span>
            <span className="paw-agent-panel-meta">{fileSearches.length} 个会话</span>
          </summary>
          <div className="paw-agent-panel-body paw-agent-file-searches">
            {fileSearches.map((search: PawAgentFileSearch) => {
              const entries = agentFileSearchEntries(search);
              return (
                <div className="paw-agent-file-search" key={search.sessionId}>
                  <div className="paw-agent-file-change-head">
                    <span>{search.query ? `查询：${search.query}` : "文件搜索"}</span>
                    <span>{search.completed ? "已完成" : "搜索中"}</span>
                  </div>
                  {entries.length > 0 ? (
                    <div className="paw-agent-file-search-results">
                      {entries.map((entry, index) => (
                        <div className="paw-agent-file-search-result" key={`${entry.path}-${index}`}>
                          <code>{entry.path}</code>
                          <span>{entry.fileName || entry.matchType}</span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <p className="paw-agent-notification-message">没有匹配文件</p>
                  )}
                </div>
              );
            })}
          </div>
        </details>
      ) : null}

      {approvalReviews.length > 0 ? (
        <details className="paw-agent-panel">
          <summary>
            <span>自动审批复核</span>
            <span className="paw-agent-panel-meta">{approvalReviews.length} 条</span>
          </summary>
          <div className="paw-agent-panel-body paw-agent-review-list">
            {approvalReviews.map((review: PawAgentApprovalReview) => (
              <div className="paw-agent-review-item" key={review.reviewId}>
                <div className="paw-agent-file-change-head">
                  <span>{review.method === "autoApprovalReview/strictReviewRequired" ? "需要严格审核" : "自动审核"}</span>
                  <span>{approvalReviewSummary(review)}</span>
                </div>
                <pre className="paw-agent-panel-pre">{stringifyAgentValue(review.raw)}</pre>
              </div>
            ))}
          </div>
        </details>
      ) : null}
    </div>
  );
}

function avatarLabel(role: PawConversation["messages"][number]["role"]): string {
  if (role === "assistant") return "G";
  if (role === "user") return "你";
  return "系";
}

function ActionButton({
  label,
  title,
  icon,
  onClick,
  disabled = false,
  active = false,
}: {
  label: string;
  /** 悬停提示；不给就用 `label`。用于放不下的完整信息（比如工作目录全路径）。 */
  title?: string;
  icon: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  active?: boolean;
}) {
  const iconRef = useRef<HTMLSpanElement>(null);
  const labelRef = useRef<HTMLSpanElement>(null);
  const [fullWidth, setFullWidth] = useState(96);

  useEffect(() => {
    const iconWidth = iconRef.current?.getBoundingClientRect().width ?? 16;
    const labelWidth = labelRef.current?.scrollWidth ?? 0;
    setFullWidth(Math.ceil(iconWidth + labelWidth + 18));
  }, [label]);

  return (
    <button
      type="button"
      className={`paw-chat-action ${active ? "active" : ""}`}
      onClick={onClick}
      disabled={disabled}
      title={title ?? label}
      aria-label={label}
      aria-pressed={active}
      style={
        {
          "--paw-action-full-width": `${fullWidth}px`,
        } as React.CSSProperties
      }
    >
      <span className="paw-chat-action-icon" ref={iconRef}>
        {icon}
      </span>
      <span className="paw-chat-action-label" ref={labelRef}>
        {label}
      </span>
    </button>
  );
}

type PawSelectorItem = {
  title: string;
  subtitle?: string;
  description?: string;
  rateLabel?: string;
  rateOriginalLabel?: string;
  rateKind?: "group" | "personal";
  rateDescription?: string;
  peakLabel?: string;
  value: string;
};

/** 待批准队列一次最多摊开几条——agent 一口气甩出一串审批请求时，全铺开会把
 * composer 顶到看不见，也会让人不知道从哪条点起。见 `approvalBatch`。 */
const APPROVAL_BATCH_SIZE = 3;

/** 状态行的计时——60 秒以内直接看秒数，再长换成 分:秒，免得三位数秒数比"卡住了
 * 多久"这个问题本身还难读。 */
function formatElapsed(totalSeconds: number): string {
  if (totalSeconds < 60) return `${totalSeconds}s`;
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

/** 目录的最后一段，给按钮当标签用——完整路径塞进这条窄窄的 footer 里没法读。 */
function shortDirName(path: string): string {
  const trimmed = path.replace(/[\\/]+$/, "");
  const segment = trimmed.split(/[\\/]/).pop();
  return segment || path;
}

function formatGroupMultiplier(value: number | undefined): string | undefined {
  return typeof value === "number" && Number.isFinite(value)
    ? `${value.toFixed(3)}x`
    : undefined;
}

function PawSelector({
  title,
  explanation,
  items,
  selectedValue,
  onSelect,
  onClose,
}: {
  title: string;
  explanation?: string;
  items: PawSelectorItem[];
  selectedValue?: string;
  onSelect: (value: string) => void;
  onClose: () => void;
}) {
  useEffect(() => {
    const handleKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [onClose]);

  return (
    <div
      className="paw-selector-overlay"
      role="presentation"
      onMouseDown={(event) => {
        if (event.currentTarget === event.target) onClose();
      }}
    >
      <section
        className="paw-selector"
        role="dialog"
        aria-modal="true"
        aria-label={title}
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="paw-selector-head">
          <h2>{title}</h2>
          <span>{items.length} 个选项</span>
        </header>
        {explanation ? (
          <div className="paw-selector-explanation">{explanation}</div>
        ) : null}
        <div className="paw-selector-list">
          {items.length === 0 ? (
            <div className="paw-selector-empty">暂无可用选项</div>
          ) : (
            items.map((item) => (
              <button
                className={`paw-selector-item ${
                  selectedValue === item.value ? "selected" : ""
                }`}
                key={item.value}
                type="button"
                onClick={() => {
                  onSelect(item.value);
                  onClose();
                }}
              >
                <span className="paw-selector-item-avatar">
                  {item.title.slice(0, 1).toUpperCase()}
                </span>
                <span className="paw-selector-item-copy">
                  <span className="paw-selector-item-heading">
                    <strong>{item.title}</strong>
                    {item.rateLabel ? (
                      <span className="paw-selector-item-rate-summary">
                        {item.rateKind === "personal" ? "专属倍率：" : "分组倍率："}
                        {item.rateOriginalLabel ? `${item.rateOriginalLabel} → ` : ""}
                        {item.rateLabel}
                      </span>
                    ) : null}
                  </span>
                  {item.subtitle ? <small>{item.subtitle}</small> : null}
                  {item.description ? (
                    <small className="paw-selector-item-description">
                      {item.description}
                    </small>
                  ) : null}
                  {item.rateDescription ? (
                    <small className="paw-selector-item-rate-description">
                      {item.rateDescription}
                    </small>
                  ) : null}
                  {selectedValue === item.value ? (
                    <small className="paw-selector-item-current">当前使用中</small>
                  ) : null}
                </span>
                {item.peakLabel ? (
                  <span className="paw-selector-item-rate">
                    <span className="paw-selector-peak-pill">{item.peakLabel}</span>
                  </span>
                ) : null}
                {selectedValue === item.value ? (
                  <span className="paw-selector-check" aria-label="已选择">
                    <PawCheckIcon width={16} height={16} />
                  </span>
                ) : null}
              </button>
            ))
          )}
        </div>
      </section>
    </div>
  );
}

export function PawChatPane({
  config,
  configBusy,
  configError,
  notice,
  selectionInvalid,
  fileBusy,
  selectedGroupId,
  selectedModelId,
  selectedReasoning,
  submitKey,
  prompts,
  currentGroup,
  currentModel,
  activeConversation,
  draft,
  attachments,
  sending,
  editingMessageId,
  canSend,
  imageSize,
  imageSizes,
  theme,
  isFullscreen,
  onNoticeChange,
  onDraftChange,
  onChangeGroup,
  onChangeModel,
  onChangeReasoning,
  onChangeImageSize,
  onRefreshConfig,
  onSaveDefaults,
  onFileChange,
  onPasteFiles,
  onSend,
  onStop,
  onRemoveAttachment,
  onOpenSidebar,
  onOpenSettings,
  onOpenShortcuts,
  onCompact,
  onToggleTheme,
  onToggleFullscreen,
  onNewConversation,
  onClearConversation,
  onRestoreContext,
  onExportConversation,
  onCopyMessage,
  onTogglePinMessage,
  onDeleteMessage,
  onEditMessage,
  onRetryMessage,
  onCancelEdit,
  onRenameConversation,
  getSelectionSummary,
  agentDesktop,
  agentArmed,
  agentCwd,
  agentCwdLocked,
  agentApprovalMode,
  agentBusy,
  agentCompacting,
  agentRunningTool,
  agentRetrying,
  agentApprovals,
  agentWaitingOnApproval,
  agentError,
  onPickAgentDirectory,
  onSetAgentApprovalMode,
  onAnswerAgentApproval,
}: PawChatPaneProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [nearBottom, setNearBottom] = useState(true);
  const [promptMenuOpen, setPromptMenuOpen] = useState(false);
  const [commandMenuLevel, setCommandMenuLevel] = useState<"root" | "prompts">("root");
  const [promptIndex, setPromptIndex] = useState(0);
  const [selectorOpen, setSelectorOpen] = useState<
    "group" | "model" | "reasoning" | "size" | null
  >(null);
  const [previewImage, setPreviewImage] = useState<string | null>(null);
  /**
   * 当前摊开显示的这一批待批准请求的 id（最多 `APPROVAL_BATCH_SIZE` 条）。
   *
   * 不是简单地"总取前 3 条"——那样答完第 1 条，原来的第 4 条会立刻补位，
   * 队列一直在动，容易看错点错。这里是**批次制**：这一批里只要还有没答完的，
   * 就不看后面新来的；这一批全部清空了，才从剩下的里面切下一批 3 条。
   */
  const [approvalBatch, setApprovalBatch] = useState<string[]>([]);
  useEffect(() => {
    setApprovalBatch((current) => {
      const stillPending = current.filter((id) =>
        agentApprovals.some((request) => request.requestId === id),
      );
      if (stillPending.length > 0) return stillPending;
      if (agentApprovals.length === 0) return [];
      return agentApprovals.slice(0, APPROVAL_BATCH_SIZE).map((request) => request.requestId);
    });
  }, [agentApprovals]);
  /**
   * "codex cli 正在处理…"配的计时——从这一轮开始数到现在过了几秒，让"卡住了
   * 吗"这个问题至少有个数可看。按对话 id 记起点，不用单个全局变量：切到别的
   * 对话再切回来，计时不会被错误地清零重算（这个面板只显示当前对话，`sending`
   * 会随着切换对话而true/false跳变，但同一个对话里那一轮其实一直在跑）。
   */
  const turnStartRef = useRef<Map<string, number>>(new Map());
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  useEffect(() => {
    const conversationId = activeConversation?.id ?? null;
    if (!conversationId || !sending) {
      setElapsedSeconds(0);
      return;
    }
    if (!turnStartRef.current.has(conversationId)) {
      turnStartRef.current.set(conversationId, Date.now());
    }
    const tick = () => {
      const startedAt = turnStartRef.current.get(conversationId);
      setElapsedSeconds(startedAt ? Math.max(0, Math.floor((Date.now() - startedAt) / 1000)) : 0);
    };
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
  }, [sending, activeConversation?.id]);
  useEffect(() => {
    // 这一轮真的结束了（不是切走又切回来），把起点也清掉，免得下一轮复用
    // 上一轮的计时起点。
    if (!sending && activeConversation?.id) {
      turnStartRef.current.delete(activeConversation.id);
    }
  }, [sending, activeConversation?.id]);
  const [speakingMessageId, setSpeakingMessageId] = useState<string | null>(null);
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const messages = activeConversation?.messages ?? [];
  const summary = useMemo(
    () => getSelectionSummary(config, selectedGroupId, selectedModelId, selectedReasoning),
    [config, getSelectionSummary, selectedGroupId, selectedModelId, selectedReasoning],
  );
  const commandQuery = draft.startsWith("/") ? draft.slice(1).trim().toLowerCase() : "";
  const promptQuery =
    commandMenuLevel === "prompts"
      ? commandQuery.replace(/^prompt\b/, "").trim()
      : "";
  const skillItems = useMemo(
    () => [
      {
        id: "compact",
        title: "鍘嬬缉涓婁笅鏂�",
        subtitle: agentDesktop
          ? "鎴戜笅闈㈢殑 agent thread"
          : "浠呮闈㈢ agent 鍙敤",
      },
      {
        id: "prompt",
        title: "鎻愮ず璇�",
        subtitle: `${prompts.length} 涓湰浜烘彁绀鸿瘝`,
      },
    ],
    [agentDesktop, prompts.length],
  );
  const filteredSkillItems = useMemo(
    () =>
      skillItems.filter(
        (item) =>
          !commandQuery ||
          item.id.includes(commandQuery) ||
          item.title.toLowerCase().includes(commandQuery),
      ),
    [commandQuery, skillItems],
  );
  const promptItems = useMemo(
    () =>
      prompts.filter(
        (prompt) =>
          !promptQuery ||
          prompt.title.toLowerCase().includes(promptQuery) ||
          prompt.content.toLowerCase().includes(promptQuery),
      ),
    [promptQuery, prompts],
  );
  const selectorItems = useMemo((): PawSelectorItem[] => {
    if (selectorOpen === "group") {
      return (config?.groups ?? []).map((group) => {
        const effectiveMultiplier =
          group.user_rate_multiplier ?? group.rate_multiplier;
        const hasPersonalMultiplier = group.user_rate_multiplier != null;
        const rateLabel =
          group.subscription_type === "subscription"
            ? "订阅"
            : formatGroupMultiplier(effectiveMultiplier);
        const rateOriginalLabel =
          group.user_rate_multiplier != null &&
          group.rate_multiplier != null &&
          group.user_rate_multiplier !== group.rate_multiplier
            ? formatGroupMultiplier(group.rate_multiplier)
            : undefined;
        const rateDescription =
          group.subscription_type === "subscription"
            ? "订阅额度按服务端订阅规则计算"
            : effectiveMultiplier == null
              ? "倍率由服务端计费规则决定"
              : `每 $1 Token 额度扣除 ￥${effectiveMultiplier.toFixed(3)} 账户余额${
                  hasPersonalMultiplier
                    ? rateOriginalLabel
                      ? `（当前为专属倍率，分组默认倍率 ${rateOriginalLabel}）`
                      : "（当前为专属倍率）"
                    : "（未设置专属倍率，使用分组默认倍率）"
                }`;
        const peakLabel =
          group.peak_rate_enabled && group.peak_start && group.peak_end
            ? `高峰时段 ${group.peak_start}-${group.peak_end} 按 ×${(
                group.peak_rate_multiplier ?? 1
              ).toFixed(2)} 计费（服务端时区）`
            : undefined;
        return {
          title: group.name,
          subtitle: `${group.models.length} 个模型`,
          description: group.description || undefined,
          rateLabel,
          rateOriginalLabel,
          rateKind: hasPersonalMultiplier ? "personal" : "group",
          rateDescription,
          peakLabel,
          value: String(group.id),
        };
      });
    }
    if (selectorOpen === "model") {
      return (currentGroup?.models ?? []).map((model) => ({
        title: model.name,
        subtitle: [
          model.owned_by,
          model.reasoning.supported ? "支持推理" : "",
          model.vision ? "支持图片" : "",
        ]
          .filter(Boolean)
          .join(" · "),
        value: model.id,
      }));
    }
    if (selectorOpen === "reasoning") {
      return [
        { title: "标准", subtitle: "使用模型默认推理设置", value: "" },
        ...(currentModel?.reasoning.values ?? []).map((value) => ({
          title: value,
          subtitle: "推理强度",
          value,
        })),
      ];
    }
    if (selectorOpen === "size") {
      return imageSizes.map((size) => ({
        title: size,
        subtitle: "生成图片尺寸",
        value: size,
      }));
    }
    return [];
  }, [config?.groups, currentGroup?.models, currentModel?.reasoning.values, imageSizes, selectorOpen]);

  const selectorTitle =
    selectorOpen === "group"
      ? "选择分组与倍率"
      : selectorOpen === "model"
        ? "选择模型"
        : selectorOpen === "reasoning"
          ? "选择推理强度"
          : "选择图片尺寸";
  const selectorExplanation =
    selectorOpen === "group"
      ? "倍率决定 Token 额度如何扣除账户余额。例如 0.500x 表示每 $1 Token 额度扣除 ￥0.500 账户余额；订阅分组按服务端订阅规则计算，高峰时段按服务端时区的高峰倍率计费。"
      : undefined;

  const selectorSelectedValue =
    selectorOpen === "group"
      ? (selectedGroupId == null ? undefined : String(selectedGroupId))
      : selectorOpen === "model"
        ? selectedModelId
        : selectorOpen === "reasoning"
          ? selectedReasoning
          : imageSize;

  useEffect(() => {
    const dom = scrollRef.current;
    if (!dom) return;
    requestAnimationFrame(() => {
      if (dom.scrollHeight - dom.scrollTop - dom.clientHeight < 180 || messages.length < 2) {
        dom.scrollTop = dom.scrollHeight;
        setNearBottom(true);
      }
    });
  }, [messages, sending]);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.style.height = "0px";
    input.style.height = `${Math.min(Math.max(input.scrollHeight, 52), 240)}px`;
  }, [draft, editingMessageId]);

  useEffect(() => {
    setPromptMenuOpen(draft.startsWith("/"));
    if (!draft.startsWith("/")) {
      setCommandMenuLevel("root");
      setPromptIndex(0);
    } else if (/^prompt(?:\s|$)/i.test(commandQuery)) {
      setCommandMenuLevel("prompts");
      setPromptIndex(0);
    } else {
      setCommandMenuLevel("root");
      setPromptIndex(0);
    }
  }, [draft]);

  useEffect(() => {
    setPromptIndex(0);
  }, [commandMenuLevel, commandQuery, promptItems.length, filteredSkillItems.length]);

  useEffect(() => {
    if (!editingTitle) return;
    setTitleDraft(activeConversation?.title ?? "");
  }, [activeConversation?.title, editingTitle]);

  useEffect(() => {
    if (!previewImage) return;
    const handleKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") setPreviewImage(null);
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [previewImage]);

  useEffect(() => {
    return () => {
      window.speechSynthesis?.cancel();
    };
  }, []);

  function toggleSpeech(message: PawConversation["messages"][number]) {
    if (!("speechSynthesis" in window)) {
      onNoticeChange("当前浏览器不支持朗读。");
      return;
    }
    if (speakingMessageId === message.id) {
      window.speechSynthesis.cancel();
      setSpeakingMessageId(null);
      return;
    }
    window.speechSynthesis.cancel();
    const text = `${message.reasoningContent ?? ""}\n${message.content}`
      .replace(/```[\s\S]*?```/g, "代码块")
      .replace(/[`*_>#~-]/g, "")
      .replace(/\s+/g, " ")
      .trim();
    if (!text) return;
    const utterance = new SpeechSynthesisUtterance(text);
    utterance.lang = "zh-CN";
    utterance.onend = () => setSpeakingMessageId(null);
    utterance.onerror = () => {
      setSpeakingMessageId(null);
      onNoticeChange("朗读失败，请稍后重试。");
    };
    setSpeakingMessageId(message.id);
    window.speechSynthesis.speak(utterance);
  }

  function commitTitle() {
    if (!activeConversation) return;
    onRenameConversation(activeConversation.id, titleDraft);
    setEditingTitle(false);
  }

  function handleInputKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Escape" && promptMenuOpen) {
      event.preventDefault();
      if (commandMenuLevel === "prompts") {
        setCommandMenuLevel("root");
        onDraftChange("/");
      } else {
        setPromptMenuOpen(false);
      }
      return;
    }
    if (promptMenuOpen) {
      const items = commandMenuLevel === "root" ? filteredSkillItems : promptItems;
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        setPromptIndex((current) =>
          event.key === "ArrowDown"
            ? (current + 1) % Math.max(1, items.length)
            : (current - 1 + Math.max(1, items.length)) % Math.max(1, items.length),
        );
        return;
      }
      if (
        commandMenuLevel === "root" &&
        (event.key === "ArrowRight" || event.key === "Tab")
      ) {
        const selected = filteredSkillItems[promptIndex];
        if (selected?.id === "prompt") {
          event.preventDefault();
          setCommandMenuLevel("prompts");
          setPromptIndex(0);
          onDraftChange("/prompt ");
        }
        return;
      }
      if (commandMenuLevel === "prompts" && event.key === "ArrowLeft") {
        event.preventDefault();
        setCommandMenuLevel("root");
        setPromptIndex(0);
        onDraftChange("/");
        return;
      }
      if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
        if (commandMenuLevel === "root") {
          event.preventDefault();
          const selected = filteredSkillItems[promptIndex];
          if (selected?.id === "compact") {
            onDraftChange("");
            setPromptMenuOpen(false);
            onCompact();
          } else if (selected?.id === "prompt") {
            setCommandMenuLevel("prompts");
            setPromptIndex(0);
            onDraftChange("/prompt ");
          }
          return;
        }
        const prompt = promptItems[promptIndex];
        if (prompt) {
          event.preventDefault();
          onDraftChange(prompt.content);
          setPromptMenuOpen(false);
          setCommandMenuLevel("root");
          return;
        }
      }
    }
    const submitModifierMatches =
      submitKey === "enter"
        ? !event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey
        : submitKey === "shift-enter"
          ? event.shiftKey
          : submitKey === "ctrl-enter"
            ? event.ctrlKey
            : event.altKey;
    if (
      event.key === "Enter" &&
      submitModifierMatches &&
      !event.nativeEvent.isComposing
    ) {
      event.preventDefault();
      if (sending) onStop();
      else onSend();
    }
  }

  function handleInputPaste(event: React.ClipboardEvent<HTMLTextAreaElement>) {
    const files = Array.from(event.clipboardData.files);
    if (files.length > 0) {
      event.preventDefault();
      onPasteFiles(files);
    }
  }

  return (
    <main className="paw-chat">
      <header className="paw-window-header">
        <div className="paw-window-leading">
          <button
            className="paw-icon-button paw-mobile-only"
            type="button"
            aria-label="打开侧栏"
            title="打开侧栏"
            onClick={onOpenSidebar}
          >
            <PawMenuIcon width={17} height={17} />
          </button>
          <div className="paw-window-title">
            {editingTitle ? (
              <input
                className="paw-title-input"
                value={titleDraft}
                autoFocus
                onChange={(event) => setTitleDraft(event.currentTarget.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    commitTitle();
                  } else if (event.key === "Escape") {
                    setEditingTitle(false);
                  }
                }}
                onBlur={commitTitle}
              />
            ) : (
              <button
                type="button"
                className="paw-title-button"
                onClick={() => {
                  setEditingTitle(true);
                  setTitleDraft(activeConversation?.title ?? "");
                }}
                title="重命名对话"
              >
                {activeConversation?.title || "新对话"}
              </button>
            )}
            <div className="paw-window-subtitle">
              {messages.length} 条消息 · {summary}
            </div>
          </div>
        </div>
        <div className="paw-window-actions">
          <PawAnnouncementCenter />
          <button
            type="button"
            className="paw-icon-button"
            onClick={onRefreshConfig}
            title="刷新配置"
            aria-label="刷新配置"
            disabled={configBusy}
          >
            <PawRefreshIcon width={16} height={16} />
          </button>
          <button
            type="button"
            className="paw-icon-button"
            onClick={() => {
              setEditingTitle(true);
              setTitleDraft(activeConversation?.title ?? "");
            }}
            title="重命名对话"
            aria-label="重命名对话"
            disabled={!activeConversation}
          >
            <PawEditIcon width={16} height={16} />
          </button>
          <button
            type="button"
            className="paw-icon-button"
            onClick={onExportConversation}
            title="导出对话"
            aria-label="导出对话"
            disabled={!activeConversation}
          >
            <PawDownloadIcon width={16} height={16} />
          </button>
          <button
            type="button"
            className="paw-icon-button"
            onClick={onToggleFullscreen}
            title={isFullscreen ? "退出全屏" : "全屏"}
            aria-label={isFullscreen ? "退出全屏" : "全屏"}
          >
            {isFullscreen ? (
              <PawMinimizeIcon width={16} height={16} />
            ) : (
              <PawMaximizeIcon width={16} height={16} />
            )}
          </button>
        </div>
      </header>

      <div className="paw-chat-main">
        <div
          className="paw-message-list"
          ref={scrollRef}
          onScroll={(event) => {
            const dom = event.currentTarget;
            setNearBottom(dom.scrollHeight - dom.scrollTop - dom.clientHeight < 80);
          }}
        >
          {notice ? (
            <button type="button" className="paw-banner" onClick={() => onNoticeChange(null)}>
              {notice}
            </button>
          ) : null}
          {configError ? <div className="paw-banner warn">{configError}</div> : null}
          {selectionInvalid ? (
            <div className="paw-banner warn">当前选择已失效，请重新选择分组或模型。</div>
          ) : null}
          {agentError ? <div className="paw-banner warn">{agentError}</div> : null}
          {/* 待批准的操作、以及"agent 现在到底在干嘛"，都挪到了输入框上方
              （paw-agent-approval-panel / paw-agent-status）——这里是消息列表，
              一旦对话变长、视图停在底部，放在列表顶端的提示根本滚不到看得见的
              地方，等于提示了个寂寞。 */}
          {configBusy && !config ? <div className="paw-banner">正在加载分组和模型...</div> : null}

          {messages.length === 0 ? (
            <div className="paw-empty-state">
              <div className="paw-empty-logo">P</div>
              <h2>开始新的对话</h2>
              <p>选择分组和模型，然后输入你的问题。</p>
            </div>
          ) : null}

          {messages.map((message, index) => (
            <Fragment key={message.id}>
              {activeConversation?.contextStartIndex != null &&
              activeConversation.contextStartIndex > 0 &&
              index === activeConversation.contextStartIndex ? (
                <button
                  type="button"
                  className="paw-context-divider"
                  onClick={onRestoreContext}
                >
                  <span>已清除前文上下文</span>
                  <strong>点击恢复</strong>
                </button>
              ) : null}
              <article
                className={`paw-message-shell ${message.role}`}
                onDoubleClick={() => {
                  if (message.role !== "user") return;
                  // agent 对话是 append-only 的 codex thread，没有"从这条截断
                  // 重新生成"这回事——`onEditMessage` 那套（回填草稿、进入编辑态、
                  // 承诺"发送后会重新生成后续内容"）在这里兑现不了，而且真正
                  // 发送时走的是 agent.send，根本不知道有编辑态这回事，于是
                  // "正在编辑这条消息"那条横幅发完也不会消失。直接定位到输入框。
                  if (agentArmed) {
                    inputRef.current?.focus();
                    return;
                  }
                  onEditMessage(message.id);
                }}
              >
              <div className={`paw-message-avatar ${message.role}`}>{avatarLabel(message.role)}</div>
              <div className="paw-message">
                <div className="paw-message-header">
                  <div className="paw-message-identity">
                    {roleLabel(message.role) ? (
                      <span className="paw-message-role">{roleLabel(message.role)}</span>
                    ) : null}
                    {message.role === "assistant" && (message.model || currentModel) ? (
                      <span className="paw-message-model">
                        {message.model || currentModel?.name}
                      </span>
                    ) : null}
                    <span className="paw-message-date">{formatTime(message.updatedAt)}</span>
                  </div>
                  <div className="paw-message-actions">
                    <ActionButton
                      label="复制"
                      icon={<PawCopyIcon width={15} height={15} />}
                      onClick={() => onCopyMessage(message.id)}
                    />
                    {message.role === "user" ? (
                      <ActionButton
                        label="编辑"
                        icon={<PawEditIcon width={15} height={15} />}
                        onClick={() =>
                          agentArmed ? inputRef.current?.focus() : onEditMessage(message.id)
                        }
                      />
                    ) : null}
                    {message.role === "assistant" ? (
                      <ActionButton
                        label="重试"
                        icon={<PawRefreshIcon width={15} height={15} />}
                        onClick={() => onRetryMessage(message.id)}
                      />
                    ) : null}
                    {message.role === "assistant" ? (
                      <ActionButton
                        label={message.pinned ? "取消置顶" : "置顶"}
                        icon={<PawPinIcon width={15} height={15} />}
                        active={Boolean(message.pinned)}
                        onClick={() => onTogglePinMessage(message.id)}
                      />
                    ) : null}
                    {message.role === "assistant" ? (
                      <ActionButton
                        label={speakingMessageId === message.id ? "停止朗读" : "朗读"}
                        icon={
                          speakingMessageId === message.id ? (
                            <PawVolumeOffIcon width={15} height={15} />
                          ) : (
                            <PawVolumeIcon width={15} height={15} />
                          )
                        }
                        active={speakingMessageId === message.id}
                        onClick={() => toggleSpeech(message)}
                      />
                    ) : null}
                    <ActionButton
                      label="删除"
                      icon={<PawTrashIcon width={15} height={15} />}
                      onClick={() => onDeleteMessage(message.id)}
                    />
                  </div>
                </div>
                <div className={`paw-message-body ${message.role}`}>
                  {message.attachments?.length ? (
                    <div className="paw-message-attachments">
                      {message.attachments.map((attachment) => (
                        <span className="paw-message-attachment" key={attachment.id}>
                          {attachment.previewUrl ? (
                            <img src={attachment.previewUrl} alt="" />
                          ) : (
                            <PawPaperclipIcon width={13} height={13} />
                          )}
                          <span>{attachment.filename}</span>
                        </span>
                      ))}
                    </div>
                  ) : null}
                  {message.reasoningContent ? (
                    <details className="paw-reasoning" open={sending && message.role === "assistant"}>
                      <summary>推理过程</summary>
                      <div>{message.reasoningContent}</div>
                    </details>
                  ) : null}
                  <PawMarkdown
                    content={message.content}
                    loading={message.role === "assistant" && sending && !message.content}
                  />
                  {message.role === "assistant" ? (
                    <AgentPanels panels={message.agentPanels} />
                  ) : null}
                  {message.images?.length ? (
                    <div className="paw-message-images">
                      {message.images.map((image, index) => (
                        <a
                          href={image}
                          target="_blank"
                          rel="noreferrer"
                          key={`${message.id}-image-${index}`}
                          onClick={(event) => {
                            event.preventDefault();
                            setPreviewImage(image);
                          }}
                        >
                          <img src={image} alt={`生成的图片 ${index + 1}`} />
                        </a>
                      ))}
                    </div>
                  ) : null}
                  {message.error ? <div className="paw-message-error">生成失败，请重试。</div> : null}
                </div>
              </div>
              </article>
            </Fragment>
          ))}
          {!nearBottom && messages.length > 0 ? (
            <button
              type="button"
              className="paw-scroll-bottom"
              onClick={() => {
                const dom = scrollRef.current;
                if (!dom) return;
                dom.scrollTo({ top: dom.scrollHeight, behavior: "smooth" });
                setNearBottom(true);
              }}
            >
              <PawArrowDownIcon width={15} height={15} />
              回到底部
            </button>
          ) : null}
        </div>

        <div className="paw-input-panel">
          {promptMenuOpen &&
          (commandMenuLevel === "root"
            ? filteredSkillItems.length > 0
            : promptItems.length > 0) ? (
            <div className="paw-prompt-hints">
              {commandMenuLevel === "root"
                ? filteredSkillItems.map((item, index) => (
                    <button
                      type="button"
                      className={`paw-prompt-hint ${promptIndex === index ? "active" : ""}`}
                      key={item.id}
                      onClick={() => {
                        if (item.id === "compact") {
                          onDraftChange("");
                          setPromptMenuOpen(false);
                          onCompact();
                        } else {
                          setCommandMenuLevel("prompts");
                          setPromptIndex(0);
                          onDraftChange("/prompt ");
                        }
                        inputRef.current?.focus();
                      }}
                    >
                      <strong>{item.title}</strong>
                      <span>{item.subtitle}</span>
                    </button>
                  ))
                : promptItems.map((prompt, index) => (
                <button
                  type="button"
                  className={`paw-prompt-hint ${promptIndex === index ? "active" : ""}`}
                  key={prompt.title}
                  onClick={() => {
                    onDraftChange(prompt.content);
                    setPromptMenuOpen(false);
                    setCommandMenuLevel("root");
                    inputRef.current?.focus();
                  }}
                >
                  <strong>{prompt.title}</strong>
                  <span>{prompt.content.replace(/\n/g, " ")}</span>
                </button>
                ))}
            </div>
          ) : null}

          {/* 这一轮还在跑，但暂时没有新文字流进来——审批刚同意、正在等模型看
              命令结果、或者命令本身在跑，用户看到的都是"什么都没发生"。
              不点破的话，唯一的线索是发送按钮变成了"停止"，太容易被忽略。
              没有待批准项时才显示，避免和下面的审批面板同时喊两件事。 */}
          {agentDesktop &&
          agentArmed &&
          (sending || agentCompacting) &&
          agentApprovals.length === 0 ? (
            <div className={`paw-agent-status ${agentRetrying ? "warn" : ""}`}>
              <span className="paw-agent-status-dot" />
              {/* 重试状态优先显示——它比"正在处理"更要紧，且只存最新一条，
                  不会随着上游连续推好几条"Reconnecting... N/5"而刷屏
                  （之前是直接把每条都塞进消息列表，真撞见过连续 4 条以上）。 */}
              <span>
                {agentCompacting
                  ? "codex cli 姝ｅ湪鍘嬪畬涓婁笅鏂�"
                  : agentRetrying
                  ? `codex cli 正在重试：${agentRetrying.message}`
                  : agentRunningTool
                    ? "codex cli 正在执行命令…"
                    : "codex cli 正在处理…"}
              </span>
              <span className="paw-agent-status-timer">{formatElapsed(elapsedSeconds)}</span>
            </div>
          ) : null}

            <div className="paw-input-actions">
              <div className="paw-selection-actions">
                <ActionButton
                  label={currentGroup?.name || "选择分组"}
                  icon={<PawLayersIcon width={16} height={16} />}
                  onClick={() => setSelectorOpen("group")}
                  active={selectorOpen === "group"}
                  disabled={!config?.groups.length}
                />
                <ActionButton
                  label={currentModel?.name || "模型"}
                  icon={<PawRobotIcon width={16} height={16} />}
                  onClick={() => setSelectorOpen("model")}
                  active={selectorOpen === "model"}
                  disabled={!currentGroup}
                />
                <ActionButton
                  label={
                    currentModel?.reasoning.supported
                      ? selectedReasoning || "标准"
                      : "推理"
                  }
                  icon={<PawBrainIcon width={16} height={16} />}
                  onClick={() => setSelectorOpen("reasoning")}
                  active={selectorOpen === "reasoning"}
                  disabled={!currentModel?.reasoning.supported}
                />
                {currentModel?.image_generation && imageSizes.length > 0 ? (
                  <ActionButton
                    label={imageSize}
                    icon={<PawRulerIcon width={16} height={16} />}
                    onClick={() => setSelectorOpen("size")}
                    active={selectorOpen === "size"}
                  />
                ) : null}
                {/* 工作目录 / 审批模式挪到了 composer footer 那一行（发送按钮正对面）——
                    这里只留分组/模型/推理/图片尺寸这几个"每条消息都可能不一样"的
                    选择。待批准的操作不再放这里当一个要点开的 chip——见下面
                    paw-agent-approval-panel，它会直接弹在输入框上方。 */}
              </div>
            <div className="paw-input-actions-end">
              <ActionButton
                label="提示词"
                icon={<PawPromptIcon width={16} height={16} />}
                onClick={() => {
                  setPromptMenuOpen((open) => !open);
                  setCommandMenuLevel("root");
                  if (!draft.startsWith("/")) onDraftChange("/");
                  inputRef.current?.focus();
                }}
              />
              <label
                className="paw-chat-action"
                title={currentModel?.image_generation ? "上传图片" : "上传附件"}
                aria-disabled={
                  fileBusy ||
                  (currentModel?.image_generation
                    ? !currentModel?.vision && !currentModel?.file_input
                    : !currentModel?.file_input && !currentModel?.vision)
                }
                style={{ "--paw-action-full-width": "78px" } as React.CSSProperties}
              >
                <span className="paw-chat-action-icon">
                  <PawPaperclipIcon width={16} height={16} />
                </span>
                <span className="paw-chat-action-label">
                  {fileBusy ? "上传中" : currentModel?.image_generation ? "图片" : "附件"}
                </span>
                <input
                  type="file"
                  accept={currentModel?.image_generation ? "image/*" : undefined}
                  multiple
                  hidden
                  onChange={onFileChange}
                  disabled={
                    fileBusy ||
                    (currentModel?.image_generation
                      ? !currentModel?.vision && !currentModel?.file_input
                      : !currentModel?.file_input && !currentModel?.vision)
                  }
                />
              </label>
              <ActionButton
                label="保存默认"
                icon={<PawSettingsIcon width={16} height={16} />}
                onClick={onSaveDefaults}
                disabled={selectionInvalid || selectedGroupId == null || !selectedModelId}
              />
              <ActionButton
                label={theme === "dark" ? "浅色" : "深色"}
                icon={theme === "dark" ? <PawSunIcon width={16} height={16} /> : <PawMoonIcon width={16} height={16} />}
                onClick={onToggleTheme}
              />
              <ActionButton
                label="快捷键"
                icon={<PawKeyboardIcon width={16} height={16} />}
                onClick={onOpenShortcuts}
              />
              <ActionButton
                label="清除上下文"
                icon={<PawBreakIcon width={16} height={16} />}
                onClick={onClearConversation}
                disabled={!activeConversation?.messages.length}
              />
            </div>
          </div>

          {editingMessageId ? (
            <div className="paw-edit-banner">
              <span>正在编辑这条消息</span>
              <button type="button" className="paw-button" onClick={onCancelEdit}>
                取消
              </button>
            </div>
          ) : null}

          {/* 待批准的操作直接弹出来，不用点哪个按钮才看得到——审批是会挡住继续
              对话的事，藏在一个要点开的 chip 后面等于让用户自己去发现"卡住了"。
              位置紧贴在输入框上方、同宽，是发送前最后必经的地方。 */}
          {agentDesktop && agentApprovals.length > 0 ? (
            <div className="paw-agent-approval-panel" role="dialog" aria-label="待批准的操作">
              <div className="paw-agent-approval-panel-header">
                <PawShieldAlertIcon width={16} height={16} />
                <span>
                  agent 请求执行 {agentApprovals.length} 项操作，需要你确认后才能继续
                </span>
                {/* 逐条点太慢——批量按钮答的是这个对话**当前全部**待批准项，
                    不止摊开显示的那 3 条，排队里的一起处理掉。 */}
                <span className="paw-agent-approval-bulk">
                  <button
                    type="button"
                    className="paw-button danger"
                    onClick={() =>
                      agentApprovals.forEach((request) =>
                        onAnswerAgentApproval(request.requestId, false),
                      )
                    }
                  >
                    全部拒绝
                  </button>
                  <button
                    type="button"
                    className="paw-button primary"
                    onClick={() =>
                      agentApprovals.forEach((request) =>
                        onAnswerAgentApproval(request.requestId, true),
                      )
                    }
                  >
                    全部同意
                  </button>
                </span>
              </div>
              {/* 一次最多摊开 APPROVAL_BATCH_SIZE 条，防止刷屏——见 approvalBatch
                  的注释。这一批答完，effect 会自动切下一批，不需要在这里手动推进。 */}
              {agentApprovals
                .filter((request) => approvalBatch.includes(request.requestId))
                .map((request) => (
                <div key={request.requestId} className="paw-agent-approval-item">
                  <p>{request.reason ?? "agent 请求执行一个操作"}</p>
                  {request.command ? <pre>{request.command}</pre> : null}
                  {request.grantRoot ? (
                    <p className="paw-agent-approval-warn">
                      这不是一次性放行：它要的是「{request.grantRoot}」这个目录的长期写权限。
                    </p>
                  ) : null}
                  <div className="paw-agent-approval-actions">
                    <button
                      type="button"
                      className="paw-button"
                      onClick={() => onAnswerAgentApproval(request.requestId, false)}
                    >
                      拒绝
                    </button>
                    <button
                      type="button"
                      className="paw-button primary"
                      onClick={() => onAnswerAgentApproval(request.requestId, true)}
                    >
                      同意
                    </button>
                  </div>
                </div>
              ))}
              {agentApprovals.length > approvalBatch.length ? (
                <p className="paw-agent-approval-queued">
                  还有 {agentApprovals.length - approvalBatch.length} 项排队，处理完这些后自动出现
                </p>
              ) : null}
            </div>
          ) : null}

          <div className="paw-composer-box">
            <textarea
              ref={inputRef}
              value={draft}
              placeholder="输入消息，按 Enter 发送，Shift + Enter 换行"
              onChange={(event) => onDraftChange(event.currentTarget.value)}
              onKeyDown={handleInputKeyDown}
              onPaste={handleInputPaste}
              onFocus={() => {
                if (draft.startsWith("/")) setPromptMenuOpen(true);
                const dom = scrollRef.current;
                if (dom) dom.scrollTop = dom.scrollHeight;
              }}
              onClick={() => {
                const dom = scrollRef.current;
                if (dom) dom.scrollTop = dom.scrollHeight;
              }}
              disabled={sending}
              aria-label="消息输入框"
            />

            {attachments.length ? (
              <div className="paw-attachment-list">
                {attachments.map((attachment) => (
                  <span key={attachment.id} className="paw-attachment-chip">
                    <PawPaperclipIcon width={13} height={13} />
                    {attachment.filename}
                    <button
                      type="button"
                      onClick={() => onRemoveAttachment(attachment.id)}
                      title={`移除 ${attachment.filename}`}
                      aria-label={`移除 ${attachment.filename}`}
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            ) : null}

            <div className="paw-composer-footer">
              <span className="paw-composer-hint-group">
                <span className="paw-composer-hint">{summary}</span>
                {/* 工作目录 / 审批模式收在发送按钮前面，不摆在工具条上——是用户看
                    这一眼、按这一下之间必经的地方。审批用原生下拉框——两个选项，
                    不需要自己拼一套弹层。 */}
                {agentDesktop ? (
                  <button
                    type="button"
                    className={`paw-composer-dir-button ${agentArmed ? "active" : ""}`}
                    onClick={() => {
                      if (!agentCwdLocked) onPickAgentDirectory();
                    }}
                    disabled={agentCwdLocked}
                    aria-label={
                      agentCwdLocked
                        ? `工作目录：${agentCwd ?? ""}（已锁定）`
                        : agentArmed
                          ? `工作目录：${agentCwd ?? ""}（点击重选）`
                          : "选择 agent 的工作目录"
                    }
                    title={
                      agentCwdLocked
                        ? (agentCwd ?? undefined)
                        : agentArmed
                          ? `${agentCwd ?? ""}（点击重选，发消息前都能改）`
                          : "选择 agent 的工作目录"
                    }
                  >
                    <PawFolderIcon width={14} height={14} />
                    <span>{agentArmed && agentCwd ? shortDirName(agentCwd) : "选择工作目录"}</span>
                  </button>
                ) : null}
                {agentDesktop ? (
                  <select
                    className="paw-composer-mode-select"
                    aria-label="审批模式"
                    value={agentApprovalMode}
                    onChange={(event) =>
                      onSetAgentApprovalMode(event.currentTarget.value as AgentApprovalUiMode)
                    }
                  >
                    <option value="full">完全控制</option>
                    <option value="review">需要审核</option>
                  </select>
                ) : null}
              </span>
              <button
                className="paw-button primary paw-chat-send"
                type="button"
                onClick={sending ? onStop : onSend}
                disabled={!canSend && !sending}
              >
                {sending ? <PawStopIcon width={16} height={16} /> : <PawSendIcon width={16} height={16} />}
                {sending ? "停止" : "发送"}
              </button>
            </div>
          </div>
        </div>
      </div>
      {selectorOpen ? (
        <PawSelector
          title={selectorTitle}
          explanation={selectorExplanation}
          items={selectorItems}
          selectedValue={selectorSelectedValue}
          onSelect={(value) => {
            if (selectorOpen === "group") {
              onChangeGroup(Number(value));
            } else if (selectorOpen === "model") {
              onChangeModel(value);
            } else if (selectorOpen === "reasoning") {
              onChangeReasoning(value);
            } else {
              onChangeImageSize(value as PawImageSize);
            }
          }}
          onClose={() => setSelectorOpen(null)}
        />
      ) : null}
      {previewImage ? (
        <div
          className="paw-image-preview-overlay"
          role="presentation"
          onMouseDown={(event) => {
            if (event.currentTarget === event.target) setPreviewImage(null);
          }}
        >
          <section
            className="paw-image-preview"
            role="dialog"
            aria-modal="true"
            aria-label="图片预览"
            onMouseDown={(event) => event.stopPropagation()}
          >
            <button
              type="button"
              className="paw-icon-button paw-image-preview-close"
              onClick={() => setPreviewImage(null)}
              aria-label="关闭图片预览"
              title="关闭图片预览"
            >
              <PawCloseIcon width={18} height={18} />
            </button>
            <img src={previewImage} alt="图片预览" />
          </section>
        </div>
      ) : null}
    </main>
  );
}
