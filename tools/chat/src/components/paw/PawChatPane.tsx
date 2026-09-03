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
import { PawMarkdown } from "./PawMarkdown";
import type { ApprovalRequest } from "@/client/agent/session";
import type {
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
  /** 是不是跑在桌面壳里；PWA 里恒为 false，两个 chip 都不渲染。 */
  agentDesktop: boolean;
  /** 当前对话挂没挂工作目录。 */
  agentArmed: boolean;
  agentCwd: string | null;
  /** 正在起会话/结束会话；轮次是否在跑用外面的 `sending`（已经把两条路合并过）。 */
  agentBusy: boolean;
  agentApprovals: ApprovalRequest[];
  agentWaitingOnApproval: boolean;
  agentError: string | null;
  /** 未挂目录时点这个 chip：直接弹出系统目录选择器。 */
  onPickAgentDirectory: () => void;
  /** 已挂目录时选"更换工作目录"。 */
  onChangeAgentDirectory: () => void;
  /** 已挂目录时选"结束 agent 会话"：停线程、抹凭据、解除这个对话的绑定。 */
  onEndAgentSession: () => void;
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

function avatarLabel(role: PawConversation["messages"][number]["role"]): string {
  if (role === "assistant") return "G";
  if (role === "user") return "你";
  return "系";
}

function ActionButton({
  label,
  icon,
  onClick,
  disabled = false,
  active = false,
}: {
  label: string;
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
      title={label}
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

/** 目录的最后一段，给 chip 当标签用——完整路径塞进一个小按钮里没法读。 */
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
  agentBusy,
  agentApprovals,
  agentWaitingOnApproval,
  agentError,
  onPickAgentDirectory,
  onChangeAgentDirectory,
  onEndAgentSession,
  onAnswerAgentApproval,
}: PawChatPaneProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [nearBottom, setNearBottom] = useState(true);
  const [promptMenuOpen, setPromptMenuOpen] = useState(false);
  const [promptIndex, setPromptIndex] = useState(0);
  const [selectorOpen, setSelectorOpen] = useState<
    "group" | "model" | "reasoning" | "size" | "agentDir" | null
  >(null);
  const [approvalMenuOpen, setApprovalMenuOpen] = useState(false);
  const [previewImage, setPreviewImage] = useState<string | null>(null);
  const [speakingMessageId, setSpeakingMessageId] = useState<string | null>(null);
  const [editingTitle, setEditingTitle] = useState(false);
  const [titleDraft, setTitleDraft] = useState("");
  const messages = activeConversation?.messages ?? [];
  const summary = useMemo(
    () => getSelectionSummary(config, selectedGroupId, selectedModelId, selectedReasoning),
    [config, getSelectionSummary, selectedGroupId, selectedModelId, selectedReasoning],
  );
  const promptQuery = draft.startsWith("/") ? draft.slice(1).trim().toLowerCase() : "";
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
    if (selectorOpen === "agentDir") {
      return [
        { title: "更换工作目录…", subtitle: agentCwd ?? undefined, value: "change" },
        { title: "结束 agent 会话", subtitle: "停止线程、抹掉本地凭据", value: "end" },
      ];
    }
    return [];
  }, [
    config?.groups,
    currentGroup?.models,
    currentModel?.reasoning.values,
    imageSizes,
    selectorOpen,
    agentCwd,
  ]);

  const selectorTitle =
    selectorOpen === "group"
      ? "选择分组与倍率"
      : selectorOpen === "model"
        ? "选择模型"
        : selectorOpen === "reasoning"
          ? "选择推理强度"
          : selectorOpen === "agentDir"
            ? "工作目录"
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
  }, [draft]);

  useEffect(() => {
    setPromptIndex(0);
  }, [promptQuery, promptItems.length]);

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
      setPromptMenuOpen(false);
      return;
    }
    if (promptMenuOpen && promptItems.length > 0) {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        setPromptIndex((current) =>
          event.key === "ArrowDown"
            ? (current + 1) % promptItems.length
            : (current - 1 + promptItems.length) % promptItems.length,
        );
        return;
      }
      if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
        const prompt = promptItems[promptIndex];
        if (prompt) {
          event.preventDefault();
          onDraftChange(prompt.content);
          setPromptMenuOpen(false);
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
          {agentArmed && agentWaitingOnApproval ? (
            <div className="paw-banner warn">agent 正在等待你的批准——见工具条上的「待批准」。</div>
          ) : null}
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
                  if (message.role === "user") onEditMessage(message.id);
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
                        onClick={() => onEditMessage(message.id)}
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
          {promptMenuOpen && promptItems.length ? (
            <div className="paw-prompt-hints">
              {promptItems.map((prompt, index) => (
                <button
                  type="button"
                  className={`paw-prompt-hint ${promptIndex === index ? "active" : ""}`}
                  key={prompt.title}
                  onClick={() => {
                    onDraftChange(prompt.content);
                    setPromptMenuOpen(false);
                    inputRef.current?.focus();
                  }}
                >
                  <strong>{prompt.title}</strong>
                  <span>{prompt.content.replace(/\n/g, " ")}</span>
                </button>
              ))}
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
                {/* agent：不是切换进去的模式，是给这个对话挂一个工作目录。
                    只在桌面端出现——PWA 里本机没有 codex。 */}
                {agentDesktop ? (
                  <ActionButton
                    label={agentArmed && agentCwd ? shortDirName(agentCwd) : "工作目录"}
                    icon={<PawFolderIcon width={16} height={16} />}
                    active={agentArmed}
                    disabled={agentBusy}
                    onClick={() => {
                      if (agentArmed) setSelectorOpen("agentDir");
                      else onPickAgentDirectory();
                    }}
                  />
                ) : null}
                {agentDesktop && agentApprovals.length > 0 ? (
                  <span className="paw-agent-approval-anchor">
                    <ActionButton
                      label={`待批准 ${agentApprovals.length}`}
                      icon={<PawShieldAlertIcon width={16} height={16} />}
                      active={approvalMenuOpen}
                      onClick={() => setApprovalMenuOpen((open) => !open)}
                    />
                    {approvalMenuOpen ? (
                      <div className="paw-agent-approval-menu" role="dialog" aria-label="待批准的操作">
                        {agentApprovals.map((request) => (
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
                      </div>
                    ) : null}
                  </span>
                ) : null}
              </div>
            <div className="paw-input-actions-end">
              <ActionButton
                label="提示词"
                icon={<PawPromptIcon width={16} height={16} />}
                onClick={() => {
                  setPromptMenuOpen((open) => !open);
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
              <span className="paw-composer-hint">{summary}</span>
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
            } else if (selectorOpen === "agentDir") {
              if (value === "change") onChangeAgentDirectory();
              else onEndAgentSession();
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
