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
  PawImageIcon,
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
  PawStopIcon,
  PawSunIcon,
  PawTrashIcon,
  PawSendIcon,
  PawPinIcon,
  PawVolumeIcon,
  PawVolumeOffIcon,
} from "./PawIcons";
import { PawMarkdown } from "./PawMarkdown";
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
  imageMode: boolean;
  imageSize: PawImageSize;
  imageSizes: PawImageSize[];
  theme: PawTheme;
  isFullscreen: boolean;
  onNoticeChange: (value: string | null) => void;
  onDraftChange: (value: string) => void;
  onChangeGroup: (value: number) => void;
  onChangeModel: (value: string) => void;
  onChangeReasoning: (value: string) => void;
  onToggleImageMode: () => void;
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
}

function roleLabel(role: PawConversation["messages"][number]["role"]): string {
  if (role === "user") return "你";
  if (role === "assistant") return "Paw";
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
  if (role === "assistant") return "P";
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
  value: string;
};

function PawSelector({
  title,
  items,
  selectedValue,
  onSelect,
  onClose,
}: {
  title: string;
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
                  <strong>{item.title}</strong>
                  {item.subtitle ? <small>{item.subtitle}</small> : null}
                </span>
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
  imageMode,
  imageSize,
  imageSizes,
  theme,
  isFullscreen,
  onNoticeChange,
  onDraftChange,
  onChangeGroup,
  onChangeModel,
  onChangeReasoning,
  onToggleImageMode,
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
}: PawChatPaneProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const [nearBottom, setNearBottom] = useState(true);
  const [promptMenuOpen, setPromptMenuOpen] = useState(false);
  const [promptIndex, setPromptIndex] = useState(0);
  const [selectorOpen, setSelectorOpen] = useState<
    "group" | "model" | "reasoning" | "size" | null
  >(null);
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
      return (config?.groups ?? []).map((group) => ({
        title: group.name,
        subtitle: group.description || `${group.models.length} 个模型`,
        value: String(group.id),
      }));
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
      ? "选择分组"
      : selectorOpen === "model"
        ? "选择模型"
        : selectorOpen === "reasoning"
          ? "选择推理强度"
          : "选择图片尺寸";

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
                    <span className="paw-message-role">{roleLabel(message.role)}</span>
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
                {imageMode && imageSizes.length > 0 ? (
                  <ActionButton
                    label={imageSize}
                    icon={<PawRulerIcon width={16} height={16} />}
                    onClick={() => setSelectorOpen("size")}
                    active={selectorOpen === "size"}
                  />
                ) : null}
              </div>
            <div className="paw-input-actions-end">
              <ActionButton
                label={imageMode ? "聊天模式" : "图片模式"}
                icon={<PawImageIcon width={16} height={16} />}
                onClick={onToggleImageMode}
                active={imageMode}
              />
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
                title={imageMode ? "上传图片" : "上传附件"}
                aria-disabled={
                  fileBusy ||
                  (imageMode
                    ? !currentModel?.vision && !currentModel?.file_input
                    : !currentModel?.file_input && !currentModel?.vision)
                }
                style={{ "--paw-action-full-width": "78px" } as React.CSSProperties}
              >
                <span className="paw-chat-action-icon">
                  <PawPaperclipIcon width={16} height={16} />
                </span>
                <span className="paw-chat-action-label">
                  {fileBusy ? "上传中" : imageMode ? "图片" : "附件"}
                </span>
                <input
                  type="file"
                  accept={imageMode ? "image/*" : undefined}
                  multiple
                  hidden
                  onChange={onFileChange}
                  disabled={
                    fileBusy ||
                    (imageMode
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
