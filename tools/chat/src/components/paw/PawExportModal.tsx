"use client";

import { useMemo, useState } from "react";
import {
  PawArrowDownIcon,
  PawCheckIcon,
  PawCloseIcon,
  PawCopyIcon,
  PawDownloadIcon,
} from "./PawIcons";
import { PawModal } from "./PawModal";
import type { PawConversation, PawConversationMessage } from "@/client/paw/types";

type PawExportFormat = "markdown" | "text" | "json";

interface PawExportModalProps {
  conversation: PawConversation;
  onClose: () => void;
  onNotice: (value: string) => void;
}

function roleLabel(message: PawConversationMessage): string {
  return message.role === "user" ? "用户" : message.role === "assistant" ? "Chat" : "系统";
}

function messagePreview(message: PawConversationMessage): string {
  return (message.content || message.reasoningContent || "(空消息)")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 120);
}

function toMarkdown(messages: PawConversationMessage[], title: string): string {
  return [
    `# ${title}`,
    "",
    ...messages.flatMap((message) => [
      `## ${roleLabel(message)}`,
      "",
      message.reasoningContent
        ? `> 推理过程\n>\n> ${message.reasoningContent.replace(/\n/g, "\n> ")}\n`
        : "",
      message.content || "(空消息)",
      ...(message.images?.length
        ? ["", ...message.images.map((image) => `![生成的图片](${image})`)]
        : []),
      "",
    ]),
  ].join("\n");
}

function toText(messages: PawConversationMessage[], title: string): string {
  return [
    title,
    "",
    ...messages.flatMap((message) => [
      `[${roleLabel(message)}]`,
      message.reasoningContent ? `推理过程：${message.reasoningContent}` : "",
      message.content || "(空消息)",
      "",
    ]),
  ].join("\n");
}

function toJson(messages: PawConversationMessage[], title: string): string {
  return JSON.stringify(
    {
      title,
      messages: messages.map((message) => ({
        role: message.role,
        content: message.content,
        model: message.model,
        reasoningContent: message.reasoningContent,
        attachments: message.attachments?.map((attachment) => ({
          id: attachment.id,
          filename: attachment.filename,
          mime_type: attachment.mime_type,
          size: attachment.size,
        })),
        images: message.images,
        createdAt: message.createdAt,
        updatedAt: message.updatedAt,
      })),
    },
    null,
    2,
  );
}

export function PawExportModal({
  conversation,
  onClose,
  onNotice,
}: PawExportModalProps) {
  const [step, setStep] = useState<"select" | "preview">("select");
  const [format, setFormat] = useState<PawExportFormat>("markdown");
  const [includeContext, setIncludeContext] = useState(true);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(
    () => new Set(conversation.messages.map((message) => message.id)),
  );
  const [copied, setCopied] = useState(false);

  const selectedMessages = useMemo(
    () =>
      conversation.messages.filter(
        (message, index) =>
          selectedIds.has(message.id) &&
          (includeContext || conversation.contextStartIndex == null || index >= conversation.contextStartIndex),
      ),
    [conversation.contextStartIndex, conversation.messages, includeContext, selectedIds],
  );
  const content = useMemo(() => {
    if (format === "markdown") return toMarkdown(selectedMessages, conversation.title);
    if (format === "text") return toText(selectedMessages, conversation.title);
    return toJson(selectedMessages, conversation.title);
  }, [conversation.title, format, selectedMessages]);
  const allSelected = conversation.messages.length > 0 && selectedIds.size === conversation.messages.length;

  function toggleMessage(messageId: string) {
    setSelectedIds((current) => {
      const next = new Set(current);
      if (next.has(messageId)) next.delete(messageId);
      else next.add(messageId);
      return next;
    });
  }

  async function copyContent() {
    try {
      await navigator.clipboard.writeText(content);
      setCopied(true);
      onNotice("导出内容已复制。");
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      onNotice("复制失败，请手动选择文本。");
    }
  }

  function downloadContent() {
    const extension = format === "json" ? "json" : format === "markdown" ? "md" : "txt";
    const mimeType =
      format === "json" ? "application/json" : format === "markdown" ? "text/markdown" : "text/plain";
    const blob = new Blob([content], { type: `${mimeType};charset=utf-8` });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${conversation.title || "新对话"}.${extension}`;
    anchor.click();
    URL.revokeObjectURL(url);
    onNotice("聊天记录已下载。");
  }

  return (
    <PawModal
      title="分享聊天记录"
      onClose={onClose}
      actions={
        step === "select" ? (
          <>
            <button type="button" className="paw-button" onClick={onClose}>
              <PawCloseIcon width={15} height={15} />
              取消
            </button>
            <button
              type="button"
              className="paw-button primary"
              disabled={selectedMessages.length === 0}
              onClick={() => setStep("preview")}
            >
              预览
              <PawArrowDownIcon width={15} height={15} />
            </button>
          </>
        ) : (
          <>
            <button type="button" className="paw-button" onClick={() => setStep("select")}>
              返回选择
            </button>
            <button type="button" className="paw-button" onClick={() => void copyContent()}>
              {copied ? (
                <PawCheckIcon width={15} height={15} />
              ) : (
                <PawCopyIcon width={15} height={15} />
              )}
              {copied ? "已复制" : "复制"}
            </button>
            <button type="button" className="paw-button primary" onClick={downloadContent}>
              <PawDownloadIcon width={15} height={15} />
              下载
            </button>
          </>
        )
      }
    >
      {step === "select" ? (
        <div className="paw-export-modal">
          <div className="paw-export-steps">
            <span className="active">1 选择消息</span>
            <span>2 预览导出</span>
          </div>
          <div className="paw-export-options">
            <label className="paw-export-format">
              <span>导出格式</span>
              <select
                value={format}
                onChange={(event) => setFormat(event.currentTarget.value as PawExportFormat)}
              >
                <option value="markdown">Markdown</option>
                <option value="text">纯文本</option>
                <option value="json">JSON</option>
              </select>
            </label>
            <label className="paw-export-check">
              <input
                type="checkbox"
                checked={includeContext}
                onChange={(event) => setIncludeContext(event.currentTarget.checked)}
              />
              <span>包含清除前的上下文消息</span>
            </label>
          </div>
          <div className="paw-export-selection-head">
            <strong>选择消息</strong>
            <button
              type="button"
              className="paw-link-button"
              onClick={() =>
                setSelectedIds(
                  allSelected
                    ? new Set()
                    : new Set(conversation.messages.map((message) => message.id)),
                )
              }
            >
              {allSelected ? "取消全选" : "全选"}
            </button>
          </div>
          <div className="paw-export-message-list">
            {conversation.messages.map((message, index) => {
              const hiddenByContext =
                !includeContext &&
                conversation.contextStartIndex != null &&
                index < conversation.contextStartIndex;
              return (
                <label
                  className={`paw-export-message ${hiddenByContext ? "muted" : ""}`}
                  key={message.id}
                >
                  <input
                    type="checkbox"
                    checked={selectedIds.has(message.id)}
                    disabled={hiddenByContext}
                    onChange={() => toggleMessage(message.id)}
                  />
                  <span className="paw-export-message-copy">
                    <strong>{roleLabel(message)}</strong>
                    <span>{messagePreview(message)}</span>
                  </span>
                </label>
              );
            })}
          </div>
          <div className="paw-export-meta">
            已选择 {selectedMessages.length} / {conversation.messages.length} 条消息
          </div>
        </div>
      ) : (
        <div className="paw-export-modal">
          <div className="paw-export-steps">
            <span>1 选择消息</span>
            <span className="active">2 预览导出</span>
          </div>
          <div className="paw-export-meta">
            {selectedMessages.length} 条消息 · {conversation.title}
          </div>
          <pre className="paw-export-preview">{content}</pre>
        </div>
      )}
    </PawModal>
  );
}
