"use client";

import { useMemo, useState } from "react";
import {
  PawCheckIcon,
  PawCloseIcon,
  PawCopyIcon,
  PawEditIcon,
  PawPlusIcon,
  PawTrashIcon,
} from "./PawIcons";
import { PawModal } from "./PawModal";
import type { PawPrompt } from "@/client/paw/types";

interface PawPromptModalProps {
  userPrompts: PawPrompt[];
  builtinPrompts: PawPrompt[];
  onAdd: (title: string, content: string) => string | null;
  onUpdate: (id: string, title: string, content: string) => boolean;
  onDelete: (id: string) => void;
  onNotice: (value: string) => void;
  onClose: () => void;
}

export function PawPromptModal({
  userPrompts,
  builtinPrompts,
  onAdd,
  onUpdate,
  onDelete,
  onNotice,
  onClose,
}: PawPromptModalProps) {
  const [query, setQuery] = useState("");
  const [editingId, setEditingId] = useState<string | null>(null);
  const [title, setTitle] = useState("");
  const [content, setContent] = useState("");
  const allPrompts = useMemo(
    () => [...userPrompts, ...builtinPrompts],
    [builtinPrompts, userPrompts],
  );
  const filteredPrompts = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    if (!normalized) return allPrompts;
    return allPrompts.filter((prompt) =>
      `${prompt.title}\n${prompt.content}`.toLowerCase().includes(normalized),
    );
  }, [allPrompts, query]);
  const editingPrompt = editingId
    ? allPrompts.find((prompt) => prompt.id === editingId)
    : null;

  function beginEdit(prompt?: PawPrompt) {
    setEditingId(prompt?.id ?? "new");
    setTitle(prompt?.title ?? "新提示词");
    setContent(prompt?.content ?? "");
  }

  function closeEditor() {
    setEditingId(null);
    setTitle("");
    setContent("");
  }

  function saveEditor() {
    if (!title.trim() || !content.trim()) {
      onNotice("请填写提示词标题和内容。");
      return;
    }
    if (editingId === "new") {
      if (!onAdd(title, content)) {
        onNotice("提示词保存失败。");
        return;
      }
      onNotice("提示词已添加。");
    } else if (editingId && onUpdate(editingId, title, content)) {
      onNotice("提示词已更新。");
    } else {
      onNotice("内置提示词不可编辑。");
      return;
    }
    closeEditor();
  }

  async function copyPrompt(prompt: PawPrompt) {
    try {
      await navigator.clipboard.writeText(prompt.content);
      onNotice("提示词已复制。");
    } catch {
      onNotice("复制失败，请手动选择文本。");
    }
  }

  return (
    <PawModal
      title="提示词管理"
      onClose={onClose}
      actions={
        editingId ? (
          <>
            <button type="button" className="paw-button" onClick={closeEditor}>
              <PawCloseIcon width={15} height={15} />
              取消
            </button>
            <button type="button" className="paw-button primary" onClick={saveEditor}>
              <PawCheckIcon width={15} height={15} />
              保存
            </button>
          </>
        ) : (
          <button type="button" className="paw-button primary" onClick={() => beginEdit()}>
            <PawPlusIcon width={15} height={15} />
            新建提示词
          </button>
        )
      }
    >
      {editingId ? (
        <div className="paw-prompt-editor">
          <label className="paw-field">
            <span className="paw-field-label">标题</span>
            <input
              value={title}
              autoFocus
              onChange={(event) => setTitle(event.currentTarget.value)}
              placeholder="例如：产品需求分析"
            />
          </label>
          <label className="paw-field">
            <span className="paw-field-label">提示词内容</span>
            <textarea
              value={content}
              rows={10}
              onChange={(event) => setContent(event.currentTarget.value)}
              placeholder="输入发送给模型的提示词内容"
            />
          </label>
          {editingPrompt && !editingPrompt.isUser ? (
            <p className="paw-settings-note">内置提示词仅可查看，不能修改。</p>
          ) : null}
        </div>
      ) : (
        <div className="paw-prompt-manager">
          <input
            className="paw-search-input"
            value={query}
            autoFocus
            onChange={(event) => setQuery(event.currentTarget.value)}
            placeholder="搜索提示词"
          />
          <div className="paw-prompt-list">
            {filteredPrompts.length === 0 ? (
              <div className="paw-search-empty">没有匹配的提示词。</div>
            ) : (
              filteredPrompts.map((prompt) => (
                <article className="paw-prompt-item" key={prompt.id}>
                  <div className="paw-prompt-item-copy">
                    <strong>{prompt.title}</strong>
                    <span>{prompt.content.replace(/\s+/g, " ")}</span>
                  </div>
                  <div className="paw-prompt-item-actions">
                    {prompt.isUser ? (
                      <>
                        <button
                          type="button"
                          className="paw-icon-button"
                          title="编辑提示词"
                          aria-label="编辑提示词"
                          onClick={() => beginEdit(prompt)}
                        >
                          <PawEditIcon width={14} height={14} />
                        </button>
                        <button
                          type="button"
                          className="paw-icon-button"
                          title="删除提示词"
                          aria-label="删除提示词"
                          onClick={() => {
                            onDelete(prompt.id);
                            onNotice("提示词已删除。");
                          }}
                        >
                          <PawTrashIcon width={14} height={14} />
                        </button>
                      </>
                    ) : null}
                    <button
                      type="button"
                      className="paw-icon-button"
                      title="复制提示词"
                      aria-label="复制提示词"
                      onClick={() => void copyPrompt(prompt)}
                    >
                      <PawCopyIcon width={14} height={14} />
                    </button>
                  </div>
                </article>
              ))
            )}
          </div>
        </div>
      )}
    </PawModal>
  );
}
