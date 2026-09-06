import type {
  PawConversation,
  PawConversationMessage,
} from "./types";

export type ConversationCompressionMode = "completed" | "aggressive";

export interface ConversationCompressionResult {
  conversations: PawConversation[];
  removedConversationCount: number;
  mode: ConversationCompressionMode;
  saved: boolean;
}

/**
 * `useAgentSession.ts` 的 `flushCommandBuffer`（约第 408-414 行）把命令输出拼成
 * 这个固定格式落进 `content`（``` agent-output 围栏，第一行是 `$ 命令` 摘要）——
 * `agentPanels` 管不到它，清理必须单独在 `content` 里把这段抠掉，只留模型自己
 * 说的话。**格式契约在那边，改了那边的拼法这里要跟着改**，两边没有共享类型钉着。
 */
const AGENT_OUTPUT_BLOCK = /\n{0,2}```agent-output\n[\s\S]*?\n```\n?/g;

function stripCommandOutput(content: string): string {
  return content.replace(AGENT_OUTPUT_BLOCK, "\n\n").replace(/\n{3,}/g, "\n\n").trim();
}

/**
 * `agentTurn` 只对**新建**的消息可靠——2026-09-06 之前落盘的对话历史里，agent
 * 消息一律没有这个字段（`createAssistantMessage()` 那时还不认识它），如果只认
 * `agentTurn`，已经存在 `paw-conversations:v2` 里的那些超大 agent 记录会被永远
 * 判定成"看不出来是不是 agent"而跳过清理——这条功能原本要修的正是这些。
 * `agentPanels` 非空、或 `content` 里带 agent-output 围栏，任何一个成立都是
 * "这是 agent 消息"的可靠证据（`sendPawChat`/图片生成两条路径都不会产生这两样
 * 东西），补上这两条兜底旧数据。
 */
function looksLikeAgentMessage(message: PawConversationMessage): boolean {
  return (
    message.agentTurn === true ||
    message.agentPanels != null ||
    message.content.includes("```agent-output")
  );
}

function durableMessage(
  message: PawConversationMessage,
  mode: ConversationCompressionMode,
): PawConversationMessage {
  if (message.role !== "assistant") return { ...message };

  // "completed" 只清 agent 轮次的中间数据。普通 Paw 对话/图片消息完成后也带
  // `turnStatus: "complete"`，但它们的 `reasoningContent` 是产品正文，不能被这条
  // 基线默认写入路径顺手清掉——那不是用户要的，是误伤。"aggressive" 是配额撑不住
  // 时的最后手段，不受这条限制，情愿牺牲普通消息的 reasoning 也不去删整条对话历史。
  const isAgentMessage = looksLikeAgentMessage(message);
  const shouldStrip =
    mode === "aggressive" || (mode === "completed" && isAgentMessage && message.turnStatus !== "active");
  if (!shouldStrip) return { ...message };

  return {
    ...message,
    // 只有真的认出是 agent 消息才把标记钉死成 true——不这样做的话，只靠
    // `agentPanels`/围栏认出来的 legacy 消息，清理完这两样证据也跟着消失了，下次
    // 写入就"看不出"是 agent 消息。但绝不能无条件钉：`aggressive` 模式下普通对话
    // 消息也会被剥离（配额撑不住的最后手段），把它们也标成 agentTurn=true 会让
    // 它们此后每次都被"completed"档误伤——这正是加这个字段要防的问题。
    agentTurn: isAgentMessage ? true : message.agentTurn,
    content: stripCommandOutput(message.content),
    reasoningContent: undefined,
    agentPanels: undefined,
  };
}

export function projectConversationsForStorage(
  conversations: PawConversation[],
  mode: ConversationCompressionMode = "completed",
): PawConversation[] {
  return conversations.map((conversation) => ({
    ...conversation,
    messages: conversation.messages.map((message) => durableMessage(message, mode)),
  }));
}

function hasPinnedMessage(conversation: PawConversation): boolean {
  return conversation.messages.some((message) => message.pinned);
}

export function buildQuotaFallbacks(
  conversations: PawConversation[],
  activeConversationId: string,
): ConversationCompressionResult[] {
  const results: ConversationCompressionResult[] = [
    {
      conversations: projectConversationsForStorage(conversations, "completed"),
      removedConversationCount: 0,
      mode: "completed",
      saved: false,
    },
    {
      conversations: projectConversationsForStorage(conversations, "aggressive"),
      removedConversationCount: 0,
      mode: "aggressive",
      saved: false,
    },
  ];

  const removable = conversations
    .filter(
      (conversation) =>
        conversation.id !== activeConversationId && !hasPinnedMessage(conversation),
    )
    .sort((a, b) => a.updatedAt - b.updatedAt);

  for (let count = 1; count <= removable.length; count += 1) {
    const removedIds = new Set(removable.slice(0, count).map((conversation) => conversation.id));
    results.push({
      conversations: projectConversationsForStorage(
        conversations.filter((conversation) => !removedIds.has(conversation.id)),
        "aggressive",
      ),
      removedConversationCount: count,
      mode: "aggressive",
      saved: false,
    });
  }

  const active = conversations.find((conversation) => conversation.id === activeConversationId);
  const pinned = conversations.filter(
    (conversation) =>
      conversation.id !== activeConversationId && hasPinnedMessage(conversation),
  );
  if (active && pinned.length > 0) {
    results.push({
      conversations: projectConversationsForStorage([active, ...pinned], "aggressive"),
      removedConversationCount: Math.max(0, conversations.length - pinned.length - 1),
      mode: "aggressive",
      saved: false,
    });
  }
  if (active) {
    results.push({
      conversations: projectConversationsForStorage([active], "aggressive"),
      removedConversationCount: Math.max(0, conversations.length - 1),
      mode: "aggressive",
      saved: false,
    });
  }

  return results;
}

function tryWrite(
  storage: { setItem: (key: string, value: string) => boolean },
  key: string,
  candidate: ConversationCompressionResult,
): boolean {
  try {
    return storage.setItem(key, JSON.stringify(candidate.conversations));
  } catch {
    // The storage adapter normally catches this; keep the helper safe for tests and
    // browser implementations that throw directly.
    return false;
  }
}

export function persistConversationsWithCompression(
  storage: { setItem: (key: string, value: string) => boolean },
  key: string,
  conversations: PawConversation[],
  activeConversationId: string,
): ConversationCompressionResult {
  // 基线就是"回合完成后仅保留最终结果"——不是等 localStorage 写满才退化。之前
  // 第一个候选是完全不剥离的原样数据，正常使用（存储没满）下永远走不到剥离那几个
  // 候选，reasoning/命令输出/plan/diff/通知原始数据因此从不清理。见 2026-09-06
  // 的复查。
  const baseline: ConversationCompressionResult = {
    conversations: projectConversationsForStorage(conversations, "completed"),
    removedConversationCount: 0,
    mode: "completed",
    saved: false,
  };
  if (tryWrite(storage, key, baseline)) {
    return { ...baseline, saved: true };
  }

  // 基线都存不下，才值得为更狠的退化付代价——`buildQuotaFallbacks` 会给每一条
  // 可删的历史对话都算一份候选，日常场景（基线已经够）不该白付这个成本。
  for (const candidate of buildQuotaFallbacks(conversations, activeConversationId)) {
    if (tryWrite(storage, key, candidate)) {
      return { ...candidate, saved: true };
    }
  }

  return { ...baseline, saved: false };
}
