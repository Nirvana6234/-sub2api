"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import {
  clearPawSession,
  loadPawSession,
  markPawSessionExpired,
  savePawSession,
} from "@/client/paw/auth";
import {
  fetchPawConfig,
  editPawImage,
  generatePawImage,
  loginPaw,
  savePawDefaults,
  sendPawChat,
  uploadPawFile,
} from "@/client/paw/api";
import { safeLocalStorage } from "@/utils/storage";
import type {
  PawAttachment,
  PawConfigData,
  PawConversation,
  PawConversationMessage,
  PawGroup,
  PawImageSize,
  PawModel,
  PawPrompt,
  PawSelectionState,
  PawSession,
  PawSubmitKey,
} from "@/client/paw/types";

const CONVERSATIONS_KEY = "paw-conversations:v2";
const ACTIVE_CONVERSATION_KEY = "paw-active-conversation:v2";
const SELECTION_KEY = "paw-selection:v2";
const MODE_KEY = "paw-mode:v1";
const IMAGE_SIZE_KEY = "paw-image-size:v1";
const PROMPTS_KEY = "paw-prompts:v1";
const SUBMIT_KEY = "paw-submit-key:v1";
const PAW_IMAGE_SIZES: PawImageSize[] = [
  "1024x1024",
  "1792x1024",
  "1024x1792",
  "768x1344",
  "864x1152",
  "1344x768",
  "1152x864",
  "1440x720",
  "720x1440",
];

export const PAW_BUILTIN_PROMPTS: PawPrompt[] = [
  {
    id: "builtin-summarize",
    title: "总结内容",
    content: "请总结下面的内容，并列出三个关键要点：\n",
    createdAt: 0,
  },
  {
    id: "builtin-polish",
    title: "润色文字",
    content: "请润色下面这段文字，让表达更自然、清晰：\n",
    createdAt: 0,
  },
  {
    id: "builtin-explain",
    title: "解释概念",
    content: "请用简单易懂的方式解释这个概念，并举一个例子：\n",
    createdAt: 0,
  },
  {
    id: "builtin-plan",
    title: "制定计划",
    content: "请帮我制定一个可执行的计划，包含步骤和注意事项：\n",
    createdAt: 0,
  },
  {
    id: "builtin-translate",
    title: "翻译文本",
    content: "请将下面的内容翻译成简体中文，并保留原文的格式：\n",
    createdAt: 0,
  },
  {
    id: "builtin-code-review",
    title: "代码审查",
    content: "请审查下面的代码，指出潜在问题、风险和改进建议：\n",
    createdAt: 0,
  },
];

function createId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

function normalizeConversation(input: Partial<PawConversation>): PawConversation {
  const now = Date.now();
  const messages = Array.isArray(input.messages)
    ? input.messages
        .filter((message): message is PawConversationMessage => Boolean(message))
        .map((message) => ({
          id:
            typeof message.id === "string" && message.id.trim()
              ? message.id
              : createId("message"),
          role:
            message.role === "system" ||
            message.role === "user" ||
            message.role === "assistant"
              ? message.role
              : "user",
          content: typeof message.content === "string" ? message.content : "",
          model: typeof message.model === "string" ? message.model : undefined,
          reasoningContent:
            typeof message.reasoningContent === "string"
              ? message.reasoningContent
              : undefined,
          attachments: Array.isArray(message.attachments)
            ? message.attachments.filter(Boolean).map((item) => ({ ...item }))
            : undefined,
          images: Array.isArray(message.images)
            ? message.images.filter(
                (item): item is string =>
                  typeof item === "string" && item.trim().length > 0,
              )
            : undefined,
          pinned: Boolean(message.pinned),
          error: Boolean(message.error),
          createdAt:
            typeof message.createdAt === "number" ? message.createdAt : now,
          updatedAt:
            typeof message.updatedAt === "number" ? message.updatedAt : now,
        }))
    : [];

  return {
    id:
      typeof input.id === "string" && input.id.trim()
        ? input.id
        : createId("conversation"),
    title:
      typeof input.title === "string" && input.title.trim()
        ? input.title.trim()
        : "新对话",
    draft: typeof input.draft === "string" ? input.draft : "",
    createdAt: typeof input.createdAt === "number" ? input.createdAt : now,
    updatedAt: typeof input.updatedAt === "number" ? input.updatedAt : now,
    contextStartIndex:
      typeof input.contextStartIndex === "number" &&
      Number.isFinite(input.contextStartIndex)
        ? Math.min(messages.length, Math.max(0, Math.floor(input.contextStartIndex)))
        : undefined,
    messages,
  };
}

function createConversation(): PawConversation {
  return normalizeConversation({});
}

function readJSON<T>(key: string, fallback: T): T {
  const storage = safeLocalStorage();
  const raw = storage.getItem(key);
  if (!raw) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}

function writeJSON(key: string, value: unknown): void {
  safeLocalStorage().setItem(key, JSON.stringify(value));
}

function loadConversations(): PawConversation[] {
  const stored = readJSON<unknown[]>(CONVERSATIONS_KEY, []);
  if (!Array.isArray(stored)) return [];
  const conversations = stored.map((item) =>
    normalizeConversation(item as Partial<PawConversation>),
  );
  return conversations.length > 0 ? conversations : [createConversation()];
}

function loadSelection(): PawSelectionState | null {
  const stored = readJSON<Partial<PawSelectionState> | null>(SELECTION_KEY, null);
  if (!stored) return null;
  return {
    groupId:
      typeof stored.groupId === "number" && Number.isFinite(stored.groupId)
        ? stored.groupId
        : null,
    modelId: typeof stored.modelId === "string" ? stored.modelId : "",
    reasoning: typeof stored.reasoning === "string" ? stored.reasoning : "",
  };
}

function normalizePrompt(input: Partial<PawPrompt>, isUser = true): PawPrompt | null {
  const title = typeof input.title === "string" ? input.title.trim() : "";
  const content = typeof input.content === "string" ? input.content : "";
  if (!title || !content.trim()) return null;
  return {
    id:
      typeof input.id === "string" && input.id.trim()
        ? input.id
        : createId("prompt"),
    title: title.slice(0, 80),
    content,
    createdAt:
      typeof input.createdAt === "number" && Number.isFinite(input.createdAt)
        ? input.createdAt
        : Date.now(),
    isUser,
  };
}

function loadPrompts(): PawPrompt[] {
  const stored = readJSON<unknown[]>(PROMPTS_KEY, []);
  if (!Array.isArray(stored)) return [];
  return stored
    .map((item) => normalizePrompt((item ?? {}) as Partial<PawPrompt>))
    .filter((item): item is PawPrompt => Boolean(item));
}

function saveSelection(selection: PawSelectionState): void {
  writeJSON(SELECTION_KEY, selection);
}

function imageSourcesFromResponse(
  data: Array<{ url?: string; b64_json?: string }>,
): string[] {
  return data
    .map((item) => {
      if (typeof item.url === "string" && item.url.trim()) {
        return item.url.trim();
      }
      if (typeof item.b64_json === "string" && item.b64_json.trim()) {
        return `data:image/png;base64,${item.b64_json.trim()}`;
      }
      return "";
    })
    .filter(Boolean);
}

function createUserMessage(
  content: string,
  attachments: PawAttachment[],
): PawConversationMessage {
  const now = Date.now();
  return {
    id: createId("user"),
    role: "user",
    content,
    attachments: attachments.length ? attachments.map((item) => ({ ...item })) : undefined,
    createdAt: now,
    updatedAt: now,
  };
}

function createAssistantMessage(model?: string): PawConversationMessage {
  const now = Date.now();
  return {
    id: createId("assistant"),
    role: "assistant",
    content: "",
    model,
    createdAt: now,
    updatedAt: now,
  };
}

function getDefaultGroupId(config: PawConfigData | null): number | null {
  return config?.defaults.group_id || null;
}

function findGroup(
  config: PawConfigData | null,
  groupId: number | null,
): PawGroup | undefined {
  if (!config || groupId == null) return undefined;
  return config.groups.find((group) => group.id === groupId);
}

function findModel(
  group: PawGroup | undefined,
  modelId: string,
): PawModel | undefined {
  if (!group || !modelId) return undefined;
  return group.models.find((model) => model.id === modelId);
}

function getDefaultModelId(group: PawGroup | undefined, fallback: string): string {
  if (!group) return fallback;
  return group.models.find((model) => model.id === fallback)?.id ?? group.models[0]?.id ?? "";
}

function getDefaultReasoning(model: PawModel | undefined, fallback: string): string {
  if (!model || !model.reasoning.supported) return "";
  return (
    model.reasoning.values.find((value) => value === fallback) ??
    model.reasoning.default ??
    model.reasoning.values[0] ??
    ""
  );
}

function hasConfiguredDefaults(config: PawConfigData): boolean {
  return (
    config.defaults.group_id > 0 ||
    Boolean(config.defaults.model_id.trim()) ||
    Boolean(config.defaults.reasoning.trim())
  );
}

function getPawImageSizes(model: PawModel | undefined): PawImageSize[] {
  if (!model?.image_generation) return [];
  const id = model.id.toLowerCase();
  if (id.includes("dall-e") || id.includes("dalle") || id.includes("gpt-image")) {
    return ["1024x1024", "1792x1024", "1024x1792"];
  }
  if (id.includes("cogview")) {
    return [
      "1024x1024",
      "768x1344",
      "864x1152",
      "1344x768",
      "1152x864",
      "1440x720",
      "720x1440",
    ];
  }
  return ["1024x1024"];
}

function isSelectionValid(
  config: PawConfigData | null,
  groupId: number | null,
  modelId: string,
  reasoning: string,
): boolean {
  const group = findGroup(config, groupId);
  const model = findModel(group, modelId);
  if (!group || !model) return false;
  if (!model.reasoning.supported) {
    return reasoning === "";
  }
  if (!reasoning) {
    return Boolean(model.reasoning.default || model.reasoning.values[0]);
  }
  return model.reasoning.values.includes(reasoning);
}

function selectionSummary(
  config: PawConfigData | null,
  groupId: number | null,
  modelId: string,
  reasoning: string,
): string {
  const group = findGroup(config, groupId);
  const model = findModel(group, modelId);
  if (!group || !model) return "未选择可用模型";
  const reasoningLabel = model.reasoning.supported
    ? reasoning || model.reasoning.default || model.reasoning.values[0] || "标准"
    : "不支持推理";
  return `${group.name} / ${model.name} / ${reasoningLabel}`;
}

function getContextStartIndex(conversation: PawConversation): number {
  return Math.min(
    conversation.messages.length,
    Math.max(0, conversation.contextStartIndex ?? 0),
  );
}

function cleanSelectionLabel(value: string): string {
  const normalized = value.replace(/\s+/g, " ").trim();
  return normalized || "新对话";
}

export function usePawClient() {
  const [hydrated, setHydrated] = useState(false);
  const [session, setSession] = useState<PawSession | null>(null);
  const [loginEmail, setLoginEmail] = useState("");
  const [loginPassword, setLoginPassword] = useState("");
  const [loginBusy, setLoginBusy] = useState(false);
  const [loginError, setLoginError] = useState<string | null>(null);
  const [config, setConfig] = useState<PawConfigData | null>(null);
  const [configBusy, setConfigBusy] = useState(false);
  const [configError, setConfigError] = useState<string | null>(null);
  const [selectedGroupId, setSelectedGroupId] = useState<number | null>(null);
  const [selectedModelId, setSelectedModelId] = useState("");
  const [selectedReasoning, setSelectedReasoning] = useState("");
  const [submitKey, setSubmitKey] = useState<PawSubmitKey>("enter");
  const [imageMode, setImageMode] = useState(false);
  const [imageSize, setImageSize] = useState<PawImageSize>("1024x1024");
  const [conversations, setConversations] = useState<PawConversation[]>([]);
  const [prompts, setPrompts] = useState<PawPrompt[]>([]);
  const [activeConversationId, setActiveConversationId] = useState("");
  const [draft, setDraftState] = useState("");
  const [attachments, setAttachments] = useState<PawAttachment[]>([]);
  const [notice, setNotice] = useState<string | null>(null);
  const [selectionInvalid, setSelectionInvalid] = useState(false);
  const [fileBusy, setFileBusy] = useState(false);
  const [sending, setSending] = useState(false);
  const [editingMessageId, setEditingMessageId] = useState<string | null>(null);
  const sendAbortRef = useRef<AbortController | null>(null);
  const draftBackupRef = useRef("");
  const attachmentsBackupRef = useRef<PawAttachment[]>([]);
  const attachmentFilesRef = useRef<Map<string, File>>(new Map());
  const selectionInitializedRef = useRef(false);

  const activeConversation = useMemo(
    () =>
      conversations.find((conversation) => conversation.id === activeConversationId) ??
      conversations[0] ??
      null,
    [activeConversationId, conversations],
  );

  const currentGroup = findGroup(config, selectedGroupId);
  const currentModel = findModel(currentGroup, selectedModelId);
  const canSend = Boolean(
    session &&
      config &&
      currentGroup &&
      currentModel &&
      isSelectionValid(config, selectedGroupId, selectedModelId, selectedReasoning) &&
      !sending,
  );

  const updateConversation = useCallback(
    (conversationId: string, updater: (conversation: PawConversation) => PawConversation) => {
      setConversations((current) =>
        current.map((conversation) =>
          conversation.id === conversationId ? updater(conversation) : conversation,
        ),
      );
    },
    [],
  );

  const syncDraft = useCallback(
    (value: string) => {
      setDraftState(value);
      if (!activeConversationId) return;
      updateConversation(activeConversationId, (conversation) => ({
        ...conversation,
        draft: value,
        updatedAt: Date.now(),
      }));
    },
    [activeConversationId, updateConversation],
  );

  const removeAttachment = useCallback((id: string) => {
    setAttachments((current) => {
      const removed = current.find((attachment) => attachment.id === id);
      if (removed?.previewUrl?.startsWith("blob:")) {
        URL.revokeObjectURL(removed.previewUrl);
      }
      attachmentFilesRef.current.delete(id);
      return current.filter((attachment) => attachment.id !== id);
    });
  }, []);

  const clearEditState = useCallback((restoreDraft = false) => {
    setEditingMessageId(null);
    if (restoreDraft) {
      setDraftState(draftBackupRef.current);
      setAttachments(attachmentsBackupRef.current.map((item) => ({ ...item })));
      if (activeConversationId) {
        updateConversation(activeConversationId, (conversation) => ({
          ...conversation,
          draft: draftBackupRef.current,
          updatedAt: Date.now(),
        }));
      }
    }
    draftBackupRef.current = "";
    attachmentsBackupRef.current = [];
  }, [activeConversationId, updateConversation]);

  const deleteMessage = useCallback((messageId: string) => {
    if (!activeConversationId) return;
    updateConversation(activeConversationId, (conversation) => ({
      ...conversation,
      messages: conversation.messages.filter((message) => message.id !== messageId),
      contextStartIndex:
        conversation.contextStartIndex == null
          ? undefined
          : Math.min(
              conversation.contextStartIndex,
              Math.max(0, conversation.messages.length - 1),
            ),
      updatedAt: Date.now(),
    }));
  }, [activeConversationId, updateConversation]);

  const togglePinMessage = useCallback((messageId: string) => {
    if (!activeConversationId) return;
    updateConversation(activeConversationId, (conversation) => ({
      ...conversation,
      messages: conversation.messages.map((message) =>
        message.id === messageId
          ? { ...message, pinned: !message.pinned, updatedAt: Date.now() }
          : message,
      ),
      updatedAt: Date.now(),
    }));
    setNotice("消息置顶状态已更新。");
  }, [activeConversationId, updateConversation]);

  const copyMessage = useCallback((messageId: string) => {
    if (!activeConversationId) return;
    const conversation = conversations.find((item) => item.id === activeConversationId);
    const message = conversation?.messages.find((item) => item.id === messageId);
    const text = [
      message?.reasoningContent?.trim(),
      message?.content?.trim(),
    ].filter(Boolean).join("\n\n");
    if (!text) return;
    const clipboard = navigator.clipboard;
    if (!clipboard) {
      setNotice("复制失败，请手动选择文本。");
      return;
    }
    void clipboard.writeText(text).then(
      () => setNotice("已复制消息内容。"),
      () => setNotice("复制失败，请手动选择文本。"),
    );
  }, [activeConversationId, conversations]);

  const getRequestMessages = useCallback(
    (
      conversation: PawConversation,
      endIndex = conversation.messages.length,
    ): Array<Pick<PawConversationMessage, "role" | "content">> => {
      const contextStart = getContextStartIndex(conversation);
      return conversation.messages
        .slice(0, endIndex)
        .filter((message, index) => index >= contextStart || message.pinned)
        .map((message) => ({ role: message.role, content: message.content }));
    },
    [],
  );

  async function dispatchConversationSend(options: {
    conversation: PawConversation;
    requestMessages: Array<Pick<PawConversationMessage, "role" | "content">>;
    nextMessages: PawConversationMessage[];
    requestAttachments: PawAttachment[];
    assistantMessage: PawConversationMessage;
    title: string;
    restoreDraft: string;
    restoreAttachments: PawAttachment[];
    editMessageId?: string | null;
  }): Promise<void> {
    const abortController = new AbortController();
    sendAbortRef.current = abortController;
    setSending(true);
    setDraftState("");
    setAttachments([]);
    setNotice(null);

    const nextConversation = normalizeConversation({
      id: options.conversation.id,
      title: options.title,
      draft: "",
      createdAt: options.conversation.createdAt,
      updatedAt: Date.now(),
      contextStartIndex: options.conversation.contextStartIndex,
      messages: options.nextMessages,
    });

    setConversations((current) => {
      const exists = current.some((item) => item.id === options.conversation.id);
      if (exists) {
        return current.map((item) =>
          item.id === options.conversation.id ? nextConversation : item,
        );
      }
      return [nextConversation, ...current];
    });
    setActiveConversationId(options.conversation.id);

    try {
      const response = await sendPawChat(
        {
          group_id: currentGroup!.id,
          model_id: currentModel!.id,
          reasoning: selectedReasoning,
          messages: options.requestMessages.map((item) => ({
            role: item.role,
            content: item.content,
          })),
          stream: true,
          attachments: options.requestAttachments.map((item) => ({ id: item.id })),
        },
        {
          signal: abortController.signal,
          onDelta: ({ contentDelta, reasoningDelta }) => {
            updateConversation(options.conversation.id, (item) => ({
              ...item,
              messages: item.messages.map((message) =>
                message.id === options.assistantMessage.id
                  ? {
                      ...message,
                      content: `${message.content}${contentDelta}`,
                      reasoningContent: `${message.reasoningContent ?? ""}${reasoningDelta}`,
                      updatedAt: Date.now(),
                    }
                  : message,
              ),
              updatedAt: Date.now(),
            }));
          },
        },
      );

      updateConversation(options.conversation.id, (item) => ({
        ...item,
        messages: item.messages.map((message) =>
          message.id === options.assistantMessage.id
            ? {
                ...message,
                content: response.content || message.content,
                reasoningContent: response.reasoningContent || message.reasoningContent,
                updatedAt: Date.now(),
              }
            : message,
        ),
        updatedAt: Date.now(),
      }));

      if (options.editMessageId) {
        clearEditState(false);
        setNotice("消息已重新生成。");
      }
    } catch (error) {
      const isAbort = error instanceof DOMException && error.name === "AbortError";
      const message = isAbort
        ? "已停止生成。"
        : error instanceof Error
          ? error.message
          : "发送失败";

      if (!isAbort && /(CONFIG|MODEL|GROUP|REASONING|QUOTA)/i.test(message)) {
        setSelectionInvalid(true);
        setNotice("当前配置不可用，请重新选择分组或模型。");
        await refreshConfig();
      } else {
        setNotice(message);
      }

      updateConversation(options.conversation.id, (item) => ({
        ...item,
        messages: item.messages.map((itemMessage) =>
          itemMessage.id === options.assistantMessage.id
            ? {
                ...itemMessage,
                content: itemMessage.content || message,
                error: !isAbort,
                updatedAt: Date.now(),
              }
            : itemMessage,
        ),
        updatedAt: Date.now(),
      }));

      setDraftState(options.restoreDraft);
      setAttachments(options.restoreAttachments.map((item) => ({ ...item })));
      if (options.editMessageId) {
        setEditingMessageId(options.editMessageId);
      }
    } finally {
      if (sendAbortRef.current === abortController) {
        sendAbortRef.current = null;
      }
      setSending(false);
    }
  }

  async function dispatchImageGeneration(options: {
    conversation: PawConversation;
    prompt: string;
    attachments: PawAttachment[];
    title: string;
  }): Promise<void> {
    const assistantMessage = createAssistantMessage(currentModel?.name);
    const userMessage = createUserMessage(options.prompt, options.attachments);
    const nextConversation = normalizeConversation({
      id: options.conversation.id,
      title: options.title,
      draft: "",
      createdAt: options.conversation.createdAt,
      updatedAt: Date.now(),
      contextStartIndex: options.conversation.contextStartIndex,
      messages: [...options.conversation.messages, userMessage, assistantMessage],
    });

    setSending(true);
    setDraftState("");
    setAttachments([]);
    setNotice(null);
    setConversations((current) => {
      const exists = current.some((item) => item.id === options.conversation.id);
      return exists
        ? current.map((item) =>
            item.id === options.conversation.id ? nextConversation : item,
          )
        : [nextConversation, ...current];
    });
    setActiveConversationId(options.conversation.id);

    try {
      const response = await generatePawImage({
        group_id: currentGroup!.id,
        model_id: currentModel!.id,
        prompt: options.prompt,
        size: getPawImageSizes(currentModel).includes(imageSize)
          ? imageSize
          : "1024x1024",
        n: 1,
        stream: false,
      });
      const images = imageSourcesFromResponse(response.data);
      if (!images.length) {
        throw new Error("图片生成成功，但没有返回图片。");
      }
      updateConversation(options.conversation.id, (item) => ({
        ...item,
        messages: item.messages.map((message) =>
          message.id === assistantMessage.id
            ? {
                ...message,
                content: "已生成图片。",
                images,
                updatedAt: Date.now(),
              }
            : message,
        ),
        updatedAt: Date.now(),
      }));
    } catch (error) {
      const message = error instanceof Error ? error.message : "图片生成失败";
      if (/(CONFIG|MODEL|GROUP|REASONING|QUOTA)/i.test(message)) {
        setSelectionInvalid(true);
        setNotice("当前图像配置不可用，请重新选择分组或模型。");
        await refreshConfig();
      } else {
        setNotice(message);
      }
      updateConversation(options.conversation.id, (item) => ({
        ...item,
        messages: item.messages.map((itemMessage) =>
          itemMessage.id === assistantMessage.id
            ? {
                ...itemMessage,
                content: message,
                error: true,
                updatedAt: Date.now(),
              }
            : itemMessage,
        ),
        updatedAt: Date.now(),
      }));
      setDraftState(options.prompt);
      setAttachments(options.attachments.map((item) => ({ ...item })));
    } finally {
      setSending(false);
    }
  }

  async function dispatchImageEdit(options: {
    conversation: PawConversation;
    prompt: string;
    attachments: PawAttachment[];
    title: string;
  }): Promise<void> {
    const sourceFiles = options.attachments
      .filter((attachment) => attachment.mime_type.startsWith("image/"))
      .map((attachment) => attachmentFilesRef.current.get(attachment.id))
      .filter((file): file is File => Boolean(file));
    if (sourceFiles.length === 0) {
      setNotice("图片附件已失效，请重新上传图片后再编辑。");
      return;
    }

    const assistantMessage = createAssistantMessage(currentModel?.name);
    const userMessage = createUserMessage(options.prompt, options.attachments);
    const nextConversation = normalizeConversation({
      id: options.conversation.id,
      title: options.title,
      draft: "",
      createdAt: options.conversation.createdAt,
      updatedAt: Date.now(),
      contextStartIndex: options.conversation.contextStartIndex,
      messages: [...options.conversation.messages, userMessage, assistantMessage],
    });

    setSending(true);
    setDraftState("");
    setAttachments([]);
    setNotice(null);
    setConversations((current) => {
      const exists = current.some((item) => item.id === options.conversation.id);
      return exists
        ? current.map((item) =>
            item.id === options.conversation.id ? nextConversation : item,
          )
        : [nextConversation, ...current];
    });
    setActiveConversationId(options.conversation.id);

    try {
      const body = new FormData();
      body.set("group_id", String(currentGroup!.id));
      body.set("model", currentModel!.id);
      body.set("prompt", options.prompt);
      body.set(
        "size",
        getPawImageSizes(currentModel).includes(imageSize) ? imageSize : "1024x1024",
      );
      body.set("n", "1");
      sourceFiles.forEach((file) => body.append("image", file, file.name));
      const response = await editPawImage(body);
      const images = imageSourcesFromResponse(response.data);
      if (!images.length) {
        throw new Error("图片编辑成功，但没有返回图片。");
      }
      updateConversation(options.conversation.id, (item) => ({
        ...item,
        messages: item.messages.map((message) =>
          message.id === assistantMessage.id
            ? {
                ...message,
                content: "已完成图片编辑。",
                images,
                updatedAt: Date.now(),
              }
            : message,
        ),
        updatedAt: Date.now(),
      }));
    } catch (error) {
      const message = error instanceof Error ? error.message : "图片编辑失败";
      if (/(CONFIG|MODEL|GROUP|REASONING|QUOTA)/i.test(message)) {
        setSelectionInvalid(true);
        setNotice("当前图像配置不可用，请重新选择分组或模型。");
        await refreshConfig();
      } else {
        setNotice(message);
      }
      updateConversation(options.conversation.id, (item) => ({
        ...item,
        messages: item.messages.map((itemMessage) =>
          itemMessage.id === assistantMessage.id
            ? {
                ...itemMessage,
                content: message,
                error: true,
                updatedAt: Date.now(),
              }
            : itemMessage,
        ),
        updatedAt: Date.now(),
      }));
      setDraftState(options.prompt);
      setAttachments(options.attachments.map((item) => ({ ...item })));
    } finally {
      setSending(false);
    }
  }

  const beginEditMessage = useCallback((messageId: string) => {
    const conversation = conversations.find((item) => item.id === activeConversationId);
    const message = conversation?.messages.find((item) => item.id === messageId);
    if (!conversation || !message || message.role !== "user") return;
    draftBackupRef.current = draft;
    attachmentsBackupRef.current = attachments.map((item) => ({ ...item }));
    setEditingMessageId(messageId);
    syncDraft(message.content);
    setAttachments(message.attachments ? message.attachments.map((item) => ({ ...item })) : []);
    setNotice("正在编辑这条消息，发送后会重新生成后续内容。");
  }, [activeConversationId, attachments, conversations, draft, syncDraft]);

  const retryMessage = useCallback((messageId: string) => {
    const conversation = conversations.find((item) => item.id === activeConversationId);
    if (!conversation) return;
    const index = conversation.messages.findIndex((item) => item.id === messageId);
    if (index < 0) return;
    const target = conversation.messages[index];
    if (target.role !== "assistant") return;
    const requestMessages = getRequestMessages(conversation, index);
    const nextAssistantMessage = createAssistantMessage(currentModel?.name);
    void dispatchConversationSend({
      conversation,
      requestMessages,
      nextMessages: [
        ...conversation.messages.slice(0, index),
        nextAssistantMessage,
      ],
      requestAttachments: [],
      assistantMessage: nextAssistantMessage,
      title: conversation.title,
      restoreDraft: draft,
      restoreAttachments: attachments,
    });
  }, [
    activeConversationId,
    attachments,
    conversations,
    currentModel?.name,
    draft,
    getRequestMessages,
  ]);

  useEffect(() => {
    setHydrated(true);
    setSession(loadPawSession());
    const initialConversations = loadConversations();
    setConversations(initialConversations);

    const storedSelection = loadSelection();
    if (storedSelection) {
      setSelectedGroupId(storedSelection.groupId);
      setSelectedModelId(storedSelection.modelId);
      setSelectedReasoning(storedSelection.reasoning);
      selectionInitializedRef.current = true;
    }
    const storedSubmitKey = safeLocalStorage().getItem(SUBMIT_KEY) as PawSubmitKey | null;
    if (
      storedSubmitKey === "enter" ||
      storedSubmitKey === "shift-enter" ||
      storedSubmitKey === "ctrl-enter" ||
      storedSubmitKey === "alt-enter"
    ) {
      setSubmitKey(storedSubmitKey);
    }
    setPrompts(loadPrompts());
    setImageMode(safeLocalStorage().getItem(MODE_KEY) === "image");
    const storedImageSize = safeLocalStorage().getItem(IMAGE_SIZE_KEY) as PawImageSize | null;
    if (storedImageSize && PAW_IMAGE_SIZES.includes(storedImageSize)) {
      setImageSize(storedImageSize);
    }

    const storedActiveId = safeLocalStorage().getItem(ACTIVE_CONVERSATION_KEY) ?? "";
    const initialActiveId =
      initialConversations.find((conversation) => conversation.id === storedActiveId)?.id ??
      initialConversations[0]?.id ??
      "";
    setActiveConversationId(initialActiveId);
  }, []);

  useEffect(() => {
    if (!hydrated) return;
    writeJSON(CONVERSATIONS_KEY, conversations);
  }, [conversations, hydrated]);

  useEffect(() => {
    if (!hydrated) return;
    if (activeConversationId) {
      safeLocalStorage().setItem(ACTIVE_CONVERSATION_KEY, activeConversationId);
    } else {
      safeLocalStorage().removeItem(ACTIVE_CONVERSATION_KEY);
    }
  }, [activeConversationId, hydrated]);

  useEffect(() => {
    if (!hydrated) return;
    saveSelection({
      groupId: selectedGroupId,
      modelId: selectedModelId,
      reasoning: selectedReasoning,
    });
  }, [hydrated, selectedGroupId, selectedModelId, selectedReasoning]);

  useEffect(() => {
    if (!hydrated) return;
    safeLocalStorage().setItem(MODE_KEY, imageMode ? "image" : "chat");
  }, [hydrated, imageMode]);

  useEffect(() => {
    if (!hydrated) return;
    safeLocalStorage().setItem(IMAGE_SIZE_KEY, imageSize);
  }, [hydrated, imageSize]);

  useEffect(() => {
    if (!hydrated) return;
    writeJSON(PROMPTS_KEY, prompts);
  }, [hydrated, prompts]);

  useEffect(() => {
    if (!hydrated) return;
    safeLocalStorage().setItem(SUBMIT_KEY, submitKey);
  }, [hydrated, submitKey]);

  const refreshConfig = useCallback(async () => {
    if (!session) return;
    setConfigBusy(true);
    setConfigError(null);
    try {
      const response = await fetchPawConfig();
      setConfig(response.data);
    } catch (error) {
      setConfigError(
        error instanceof Error ? error.message : "配置加载失败，请重新登录或稍后再试。",
      );
    } finally {
      setConfigBusy(false);
    }
  }, [session]);

  useEffect(() => {
    if (!session) {
      setConfig(null);
      return;
    }
    void refreshConfig();
  }, [refreshConfig, session?.accessToken]);

  useEffect(() => {
    if (!config) return;

    if (!selectionInitializedRef.current) {
      const configured = hasConfiguredDefaults(config);
      const nextGroup = configured
        ? findGroup(config, getDefaultGroupId(config))
        : config.groups[0];
      const nextGroupId = nextGroup?.id ?? null;
      const nextModelId = configured
        ? config.defaults.model_id
        : nextGroup?.models[0]?.id ?? "";
      const nextModel = findModel(nextGroup, nextModelId);
      const nextReasoning = configured
        ? config.defaults.reasoning
        : getDefaultReasoning(nextModel, "");
      const valid = isSelectionValid(config, nextGroupId, nextModelId, nextReasoning);
      setSelectedGroupId(valid ? nextGroupId : null);
      setSelectedModelId(valid ? nextModelId : "");
      setSelectedReasoning(valid ? nextReasoning : "");
      setSelectionInvalid(!valid);
      if (!valid) {
        setNotice("当前默认选择已失效，请重新选择分组或模型。");
      }
      selectionInitializedRef.current = true;
      return;
    }

    const valid = isSelectionValid(
      config,
      selectedGroupId,
      selectedModelId,
      selectedReasoning,
    );
    setSelectionInvalid(!valid);
    if (!valid) {
      setSelectedGroupId(null);
      setSelectedModelId("");
      setSelectedReasoning("");
      setNotice("当前选择已失效，请重新选择分组或模型。");
    }
  }, [config, selectedGroupId, selectedModelId, selectedReasoning]);

  const addConversation = useCallback(() => {
    clearEditState(false);
    const conversation = createConversation();
    setConversations((current) => [conversation, ...current]);
    setActiveConversationId(conversation.id);
    setDraftState("");
    setAttachments([]);
    setNotice(null);
  }, []);

  const selectConversation = useCallback(
    (conversationId: string) => {
      const conversation = conversations.find((item) => item.id === conversationId);
      if (!conversation) return;
      clearEditState(false);
      setActiveConversationId(conversationId);
      setDraftState(conversation.draft);
      setAttachments([]);
    },
    [clearEditState, conversations],
  );

  const deleteConversation = useCallback(
    (conversationId?: string) => {
      const targetId = conversationId ?? activeConversationId;
      if (!targetId) return;
      setConversations((current) => {
        const next = current.filter((conversation) => conversation.id !== targetId);
        const fallback = next[0] ?? null;
        if (targetId === activeConversationId) {
          setActiveConversationId(fallback?.id ?? "");
          setDraftState(fallback?.draft ?? "");
          setAttachments([]);
        }
        return next;
      });
      clearEditState(false);
    },
    [activeConversationId, clearEditState],
  );

  const reorderConversations = useCallback((sourceId: string, targetId: string) => {
    setConversations((current) => {
      const sourceIndex = current.findIndex((conversation) => conversation.id === sourceId);
      const targetIndex = current.findIndex((conversation) => conversation.id === targetId);
      if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) {
        return current;
      }
      const next = current.slice();
      const [moved] = next.splice(sourceIndex, 1);
      if (!moved) return current;
      next.splice(targetIndex, 0, moved);
      return next;
    });
  }, []);

  const clearConversationMessages = useCallback((conversationId?: string) => {
    const targetId = conversationId ?? activeConversationId;
    if (!targetId) return;
    updateConversation(targetId, (conversation) => ({
      ...conversation,
      contextStartIndex: conversation.messages.length,
      updatedAt: Date.now(),
    }));
    if (targetId === activeConversationId) {
      clearEditState(false);
    }
  }, [activeConversationId, clearEditState, updateConversation]);

  const restoreConversationContext = useCallback((conversationId?: string) => {
    const targetId = conversationId ?? activeConversationId;
    if (!targetId) return;
    updateConversation(targetId, (conversation) => ({
      ...conversation,
      contextStartIndex: undefined,
      updatedAt: Date.now(),
    }));
  }, [activeConversationId, updateConversation]);

  const renameConversation = useCallback((conversationId: string, title: string) => {
    const nextTitle = cleanSelectionLabel(title).slice(0, 48);
    updateConversation(conversationId, (conversation) => ({
      ...conversation,
      title: nextTitle,
      updatedAt: Date.now(),
    }));
  }, [updateConversation]);

  const addPrompt = useCallback((title: string, content: string): string | null => {
    const prompt = normalizePrompt({ title, content });
    if (!prompt) return null;
    setPrompts((current) => [prompt, ...current]);
    return prompt.id;
  }, []);

  const updatePrompt = useCallback(
    (promptId: string, title: string, content: string): boolean => {
      const nextPrompt = normalizePrompt({ id: promptId, title, content });
      if (!nextPrompt) return false;
      setPrompts((current) =>
        current.map((prompt) =>
          prompt.id === promptId ? { ...nextPrompt, isUser: true } : prompt,
        ),
      );
      return true;
    },
    [],
  );

  const deletePrompt = useCallback((promptId: string) => {
    setPrompts((current) => current.filter((prompt) => prompt.id !== promptId));
  }, []);

  const exportLocalData = useCallback(() => {
    const payload = {
      type: "paw-local-data",
      version: 1,
      exportedAt: new Date().toISOString(),
      conversations,
      activeConversationId,
      selection: {
        groupId: selectedGroupId,
        modelId: selectedModelId,
        reasoning: selectedReasoning,
      },
      submitKey,
      imageMode,
      imageSize,
      prompts,
    };
    const blob = new Blob([JSON.stringify(payload, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `paw-data-${new Date().toISOString().slice(0, 10)}.json`;
    anchor.click();
    URL.revokeObjectURL(url);
    setNotice("Chat 本地数据已导出。");
  }, [
    activeConversationId,
    conversations,
    imageMode,
    imageSize,
    prompts,
    selectedGroupId,
    selectedModelId,
    selectedReasoning,
    submitKey,
  ]);

  const importLocalData = useCallback(
    async (file: File) => {
      try {
        const parsed = JSON.parse(await file.text()) as Record<string, unknown>;
        if (parsed.type !== "paw-local-data" || !Array.isArray(parsed.conversations)) {
          throw new Error("这不是有效的 Chat 数据文件。");
        }
        const importedConversations = parsed.conversations
          .map((item) => normalizeConversation((item ?? {}) as Partial<PawConversation>))
          .filter((item): item is PawConversation => Boolean(item));
        const nextConversations =
          importedConversations.length > 0 ? importedConversations : [createConversation()];
        const requestedActiveId =
          typeof parsed.activeConversationId === "string" ? parsed.activeConversationId : "";
        const nextActiveId =
          nextConversations.find((conversation) => conversation.id === requestedActiveId)?.id ??
          nextConversations[0]?.id ??
          "";
        const selection = (parsed.selection ?? {}) as Partial<PawSelectionState>;
        const nextGroupId =
          typeof selection.groupId === "number" && Number.isFinite(selection.groupId)
            ? selection.groupId
            : null;
        const nextModelId = typeof selection.modelId === "string" ? selection.modelId : "";
        const nextReasoning =
          typeof selection.reasoning === "string" ? selection.reasoning : "";
        const nextSubmitKey: PawSubmitKey =
          parsed.submitKey === "shift-enter" ||
          parsed.submitKey === "ctrl-enter" ||
          parsed.submitKey === "alt-enter"
            ? parsed.submitKey
            : "enter";
        const importedPrompts = Array.isArray(parsed.prompts)
          ? parsed.prompts
              .map((item) => normalizePrompt((item ?? {}) as Partial<PawPrompt>))
              .filter((item): item is PawPrompt => Boolean(item))
          : [];

        setConversations(nextConversations);
        setActiveConversationId(nextActiveId);
        setDraftState(nextConversations.find((item) => item.id === nextActiveId)?.draft ?? "");
        setAttachments([]);
        setSelectedGroupId(nextGroupId);
        setSelectedModelId(nextModelId);
        setSelectedReasoning(nextReasoning);
        setSubmitKey(nextSubmitKey);
        setImageMode(parsed.imageMode === true);
        if (typeof parsed.imageSize === "string" && PAW_IMAGE_SIZES.includes(parsed.imageSize as PawImageSize)) {
          setImageSize(parsed.imageSize as PawImageSize);
        }
        setPrompts(importedPrompts);
        setSelectionInvalid(false);
        setNotice("Chat 本地数据已导入。");
      } catch (error) {
        setNotice(error instanceof Error ? error.message : "本地数据导入失败。");
      }
    },
    [],
  );

  const resetLocalData = useCallback(() => {
    const conversation = createConversation();
    setConversations([conversation]);
    setActiveConversationId(conversation.id);
    setDraftState("");
    setAttachments([]);
    setPrompts([]);
    setSelectedGroupId(null);
    setSelectedModelId("");
    setSelectedReasoning("");
    setSubmitKey("enter");
    setImageMode(false);
    setImageSize("1024x1024");
    setSelectionInvalid(false);
    setNotice("Chat 本地数据已清空。");
  }, []);

  const updateSelection = useCallback(
    (groupId: number) => {
      const nextGroup = findGroup(config, groupId);
      const nextModelId = getDefaultModelId(nextGroup, config?.defaults.model_id ?? "");
      const nextModel = findModel(nextGroup, nextModelId);
      const nextReasoning = getDefaultReasoning(nextModel, config?.defaults.reasoning ?? "");
      setSelectedGroupId(groupId);
      setSelectedModelId(nextModelId);
      setSelectedReasoning(nextReasoning);
      if (!nextModel?.image_generation) {
        setImageMode(false);
      }
      setImageSize((current) =>
        getPawImageSizes(nextModel).includes(current) ? current : "1024x1024",
      );
      setSelectionInvalid(!isSelectionValid(config, groupId, nextModelId, nextReasoning));
      setNotice(null);
    },
    [config],
  );

  const updateModel = useCallback(
    (modelId: string) => {
      const nextModel = findModel(currentGroup, modelId);
      const nextReasoning = getDefaultReasoning(nextModel, selectedReasoning);
      setSelectedModelId(modelId);
      setSelectedReasoning(nextReasoning);
      if (!nextModel?.image_generation) {
        setImageMode(false);
      }
      setImageSize((current) =>
        getPawImageSizes(nextModel).includes(current) ? current : "1024x1024",
      );
      setSelectionInvalid(
        !isSelectionValid(config, selectedGroupId, modelId, nextReasoning),
      );
      setNotice(null);
    },
    [config, currentGroup, selectedGroupId, selectedReasoning],
  );

  const toggleImageMode = useCallback(() => {
    if (!currentModel?.image_generation) {
      setNotice("当前模型不支持图片生成，请重新选择模型。");
      return;
    }
    setImageMode((current) => !current);
    setNotice(null);
  }, [currentModel]);

  const updateReasoning = useCallback(
    (reasoning: string) => {
      setSelectedReasoning(reasoning);
      setSelectionInvalid(
        !isSelectionValid(config, selectedGroupId, selectedModelId, reasoning),
      );
      setNotice(null);
    },
    [config, selectedGroupId, selectedModelId],
  );

  const handleLogin = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      setLoginBusy(true);
      setLoginError(null);
      try {
        const nextSession = await loginPaw(loginEmail, loginPassword);
        setSession(nextSession);
        setLoginPassword("");
        setNotice("登录成功。");
      } catch (error) {
        setLoginError(error instanceof Error ? error.message : "登录失败");
      } finally {
        setLoginBusy(false);
      }
    },
    [loginEmail, loginPassword],
  );

  const handleLogout = useCallback(() => {
    sendAbortRef.current?.abort();
    clearEditState(false);
    clearPawSession();
    markPawSessionExpired();
    setSession(null);
    setConfig(null);
    setConfigError(null);
    setLoginPassword("");
    setNotice(null);
    setSelectionInvalid(false);
    setSending(false);
    setAttachments([]);
    setActiveConversationId("");
    setDraftState("");
  }, [clearEditState]);

  const uploadFiles = useCallback(async (files: File[]) => {
    if (!files.length) return;
      setFileBusy(true);
      try {
        const uploaded: PawAttachment[] = [];
        for (const file of files) {
          const response = await uploadPawFile(file);
          uploaded.push({
            ...response.data,
            previewUrl: file.type.startsWith("image/")
              ? URL.createObjectURL(file)
              : undefined,
          });
          attachmentFilesRef.current.set(response.data.id, file);
        }
        setAttachments((current) => [...current, ...uploaded]);
        setNotice(`${uploaded.length} 个附件已上传。`);
      } catch (error) {
        setNotice(error instanceof Error ? error.message : "附件上传失败");
      } finally {
        setFileBusy(false);
      }
  }, []);

  const handleFileChange = useCallback(
    async (event: React.ChangeEvent<HTMLInputElement>) => {
      const files = Array.from(event.target.files ?? []);
      event.target.value = "";
      await uploadFiles(files);
    },
    [uploadFiles],
  );

  const handlePasteFiles = useCallback(
    async (files: File[]) => {
      const accepted = files.filter((file) => {
        if (file.type.startsWith("image/")) {
          return Boolean(currentModel?.vision || currentModel?.file_input);
        }
        return Boolean(currentModel?.file_input);
      });
      if (!accepted.length) {
        setNotice("当前模型不支持粘贴的文件类型。");
        return;
      }
      await uploadFiles(accepted.slice(0, 3));
    },
    [currentModel?.file_input, currentModel?.vision, uploadFiles],
  );

  const handleSaveDefaults = useCallback(async () => {
    if (!config || selectedGroupId == null || !selectedModelId) return;
    if (!isSelectionValid(config, selectedGroupId, selectedModelId, selectedReasoning)) {
      setSelectionInvalid(true);
      setNotice("当前选择不可用，请先重新选择分组或模型。");
      return;
    }
    await savePawDefaults({
      group_id: selectedGroupId,
      model_id: selectedModelId,
      reasoning: selectedReasoning,
    });
    setNotice("默认选择已保存。");
  }, [config, selectedGroupId, selectedModelId, selectedReasoning]);

  const handleStop = useCallback(() => {
    sendAbortRef.current?.abort();
  }, []);

  const handleSend = useCallback(async () => {
    const conversation = activeConversation ?? createConversation();
    if (!config || !currentGroup || !currentModel) {
      setNotice("请先选择可用的分组和模型。");
      return;
    }
    if (!isSelectionValid(config, selectedGroupId, selectedModelId, selectedReasoning)) {
      setSelectionInvalid(true);
      setNotice("当前分组或模型已失效，请重新选择。");
      return;
    }

    const text = draft.trim();
    if (!text && attachments.length === 0) {
      setNotice(imageMode ? "请输入图片描述后再生成。" : "先输入内容再发送。");
      return;
    }

    const submittedDraft = draft;
    const submittedAttachments = attachments.map((item) => ({ ...item }));

    if (imageMode) {
      if (!currentModel.image_generation) {
        setSelectionInvalid(true);
        setNotice("当前模型不支持图片生成，请重新选择模型。");
        return;
      }
      if (editingMessageId) {
        setNotice("图片生成消息暂不支持编辑，请新建一条图片请求。");
        return;
      }
      const imageAttachments = submittedAttachments.filter((attachment) =>
        attachment.mime_type.startsWith("image/"),
      );
      const imageRequest = {
        conversation,
        prompt: text,
        attachments: submittedAttachments,
        title:
          conversation.title === "新对话" && text
            ? cleanSelectionLabel(text.slice(0, 32))
            : conversation.title,
      };
      if (imageAttachments.length > 0) {
        await dispatchImageEdit(imageRequest);
      } else {
        await dispatchImageGeneration(imageRequest);
      }
      return;
    }

    if (editingMessageId) {
      const index = conversation.messages.findIndex((item) => item.id === editingMessageId);
      const original = conversation.messages[index];
      if (index < 0 || !original || original.role !== "user") {
        setNotice("当前编辑目标已失效，请重新选择消息。");
        return;
      }
      const prefix = conversation.messages.slice(0, index);
      const editedMessage: PawConversationMessage = {
        ...original,
        content: text,
        attachments: submittedAttachments.length ? submittedAttachments : original.attachments,
        error: false,
        updatedAt: Date.now(),
      };
      const assistantMessage = createAssistantMessage(currentModel?.name);
      const title = prefix.length === 0 && text ? cleanSelectionLabel(text.slice(0, 32)) : conversation.title;
      await dispatchConversationSend({
        conversation,
      requestMessages: [
        ...getRequestMessages(conversation, index),
        { role: "user", content: text },
      ],
        nextMessages: [...prefix, editedMessage, assistantMessage],
        requestAttachments: submittedAttachments,
        assistantMessage,
        title,
        restoreDraft: text,
        restoreAttachments: submittedAttachments,
        editMessageId: editingMessageId,
      });
      return;
    }

    const userMessage = createUserMessage(text, submittedAttachments);
    const assistantMessage = createAssistantMessage(currentModel?.name);
    const nextMessages = [...conversation.messages, userMessage, assistantMessage];
    const title =
      conversation.title === "新对话" && text
        ? cleanSelectionLabel(text.slice(0, 32))
        : conversation.title;

    await dispatchConversationSend({
      conversation,
      requestMessages: [
        ...getRequestMessages(conversation),
        { role: "user", content: text },
      ],
      nextMessages,
      requestAttachments: submittedAttachments,
      assistantMessage,
      title,
      restoreDraft: submittedDraft,
      restoreAttachments: submittedAttachments,
    });
  }, [
    activeConversation,
    attachments,
    config,
    editingMessageId,
    currentGroup,
    currentModel,
    draft,
    selectedGroupId,
    selectedModelId,
    selectedReasoning,
    imageMode,
    imageSize,
    dispatchConversationSend,
    dispatchImageGeneration,
    dispatchImageEdit,
    getRequestMessages,
  ]);

  return {
    hydrated,
    session,
    loginEmail,
    setLoginEmail,
    loginPassword,
    setLoginPassword,
    loginBusy,
    loginError,
    config,
    configBusy,
    configError,
    conversations,
    activeConversation,
    activeConversationId,
    draft,
    setDraft: syncDraft,
    attachments,
    setAttachments,
    removeAttachment,
    notice,
    setNotice,
    selectionInvalid,
    fileBusy,
    sending,
    editingMessageId,
    selectedGroupId,
    selectedModelId,
    selectedReasoning,
    submitKey,
    setSubmitKey,
    imageMode,
    imageSize,
    imageSizes: getPawImageSizes(currentModel),
    setImageSize,
    toggleImageMode,
    currentGroup,
    currentModel,
    canSend,
    addConversation,
    selectConversation,
    deleteConversation,
    reorderConversations,
    clearConversationMessages,
    restoreConversationContext,
    renameConversation,
    prompts,
    builtinPrompts: PAW_BUILTIN_PROMPTS,
    addPrompt,
    updatePrompt,
    deletePrompt,
    exportLocalData,
    importLocalData,
    resetLocalData,
    updateSelection,
    updateModel,
    updateReasoning,
    refreshConfig,
    handleLogin,
    handleLogout,
    handleFileChange,
    handlePasteFiles,
    handleSaveDefaults,
    handleSend,
    handleStop,
    copyMessage,
    togglePinMessage,
    deleteMessage,
    beginEditMessage,
    retryMessage,
    clearEditState,
    getSelectionSummary: selectionSummary,
  };
}
