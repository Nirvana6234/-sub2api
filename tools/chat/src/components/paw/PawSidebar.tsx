"use client";

import { useEffect, useMemo, useState, type PointerEvent } from "react";
import {
  PawCloseIcon,
  PawDragIcon,
  PawEditIcon,
  PawCheckIcon,
  PawDownloadIcon,
  PawLogoutIcon,
  PawPlusIcon,
  PawPromptIcon,
  PawRefreshIcon,
  PawSearchIcon,
  PawSettingsIcon,
  PawTrashIcon,
} from "./PawIcons";
import { PawModal } from "./PawModal";
import type { PawConversation, PawConfigData, PawSession } from "@/client/paw/types";

interface PawSidebarProps {
  session: PawSession;
  config: PawConfigData | null;
  conversations: PawConversation[];
  activeConversationId: string;
  onNewConversation: () => void;
  onDeleteConversation: (id?: string) => void;
  onRenameConversation: (id: string, title: string) => void;
  onRefreshConfig: () => void;
  onOpenPrompts: () => void;
  onOpenSettings: () => void;
  onExportConversation: () => void;
  onLogout: () => void;
  onSelectConversation: (id: string) => void;
  onReorderConversations: (sourceId: string, targetId: string) => void;
  onCloseMobile: () => void;
  onDragStart: (event: PointerEvent<HTMLDivElement>) => void;
}

function formatConversationDate(value: number): string {
  return new Date(value).toLocaleDateString("zh-CN", {
    month: "numeric",
    day: "numeric",
  });
}

export function PawSidebar({
  session,
  config,
  conversations,
  activeConversationId,
  onNewConversation,
  onDeleteConversation,
  onRenameConversation,
  onRefreshConfig,
  onOpenPrompts,
  onOpenSettings,
  onExportConversation,
  onLogout,
  onSelectConversation,
  onReorderConversations,
  onCloseMobile,
  onDragStart,
}: PawSidebarProps) {
  const user = config?.user ?? session.user;
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [draggedConversationId, setDraggedConversationId] = useState<string | null>(null);
  const [searchOpen, setSearchOpen] = useState(false);
  const [searchQuery, setSearchQuery] = useState("");

  const searchResults = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) return conversations;
    return conversations.filter((conversation) => {
      const haystack = [
        conversation.title,
        ...conversation.messages.map((message) => message.content),
      ]
        .join("\n")
        .toLowerCase();
      return haystack.includes(query);
    });
  }, [conversations, searchQuery]);

  useEffect(() => {
    if (!renamingId) return;
    const conversation = conversations.find((item) => item.id === renamingId);
    setRenameValue(conversation?.title ?? "");
  }, [conversations, renamingId]);

  function beginRename(id: string, title: string) {
    setRenamingId(id);
    setRenameValue(title);
  }

  function commitRename() {
    if (!renamingId) return;
    onRenameConversation(renamingId, renameValue);
    setRenamingId(null);
  }

  return (
    <aside className="paw-sidebar">
      <div className="paw-sidebar-header">
        <div className="paw-sidebar-brand">
          <div>
            <h2 className="paw-brand-title">Chat</h2>
            <div className="paw-brand-subtitle">sub2api</div>
          </div>
          <button
            className="paw-icon-button paw-mobile-only"
            type="button"
            aria-label="关闭侧栏"
            onClick={onCloseMobile}
          >
            <PawCloseIcon width={16} height={16} />
          </button>
        </div>
        <div className="paw-sidebar-actions">
          <button
            className="paw-icon-button"
            type="button"
            onClick={() => {
              setSearchQuery("");
              setSearchOpen(true);
            }}
            title="搜索对话"
            aria-label="搜索对话"
          >
            <PawSearchIcon width={16} height={16} />
          </button>
          <button
            className="paw-icon-button"
            type="button"
            onClick={onOpenPrompts}
            title="提示词"
            aria-label="提示词"
          >
            <PawPromptIcon width={16} height={16} />
          </button>
          <button
            className="paw-icon-button"
            type="button"
            onClick={onRefreshConfig}
            title="刷新配置"
            aria-label="刷新配置"
          >
            <PawRefreshIcon width={16} height={16} />
          </button>
        </div>
      </div>

      <div className="paw-sidebar-list">
        {conversations.length === 0 ? (
          <div className="paw-empty-state sidebar-empty">
            <div>
              <h2>暂无对话</h2>
            </div>
          </div>
        ) : null}

        {conversations.map((conversation) => (
          <div
            key={conversation.id}
            className={`paw-conversation-item ${
              conversation.id === activeConversationId ? "active" : ""
            }`}
            draggable={renamingId !== conversation.id}
            onDragStart={() => setDraggedConversationId(conversation.id)}
            onDragEnd={() => setDraggedConversationId(null)}
            onDragOver={(event) => event.preventDefault()}
            onDrop={(event) => {
              event.preventDefault();
              if (draggedConversationId && draggedConversationId !== conversation.id) {
                onReorderConversations(draggedConversationId, conversation.id);
              }
              setDraggedConversationId(null);
            }}
          >
            <button
              type="button"
              className="paw-conversation-select"
              onClick={() => {
                onSelectConversation(conversation.id);
                onCloseMobile();
              }}
            >
              {renamingId === conversation.id ? (
                <input
                  className="paw-conversation-rename"
                  value={renameValue}
                  autoFocus
                  onChange={(event) => setRenameValue(event.currentTarget.value)}
                  onClick={(event) => event.stopPropagation()}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      commitRename();
                    } else if (event.key === "Escape") {
                      setRenamingId(null);
                    }
                  }}
                />
              ) : (
                <>
                  <span className="paw-conversation-title">{conversation.title}</span>
                  <span className="paw-conversation-info">
                    <span>{conversation.messages.length} 条消息</span>
                    <span>{formatConversationDate(conversation.updatedAt)}</span>
                  </span>
                </>
              )}
            </button>
            <div className="paw-conversation-actions">
              {renamingId === conversation.id ? (
                <button
                  type="button"
                  className="paw-icon-button"
                  title="重命名对话"
                  aria-label="重命名对话"
                  onClick={(event) => {
                    event.stopPropagation();
                    commitRename();
                  }}
                >
                  <PawCheckIcon width={14} height={14} />
                </button>
              ) : (
                <button
                  type="button"
                  className="paw-icon-button"
                  onClick={(event) => {
                    event.stopPropagation();
                    beginRename(conversation.id, conversation.title);
                  }}
                >
                  <PawEditIcon width={14} height={14} />
                </button>
              )}
              <button
                type="button"
                className="paw-icon-button"
                title="删除对话"
                aria-label="删除对话"
                onClick={(event) => {
                  event.stopPropagation();
                  onDeleteConversation(conversation.id);
                }}
              >
                <PawTrashIcon width={14} height={14} />
              </button>
            </div>
          </div>
        ))}
      </div>

      <div className="paw-sidebar-footer">
        <div className="paw-sidebar-footer-row">
          <button
            className="paw-icon-button"
            type="button"
            onClick={() => onDeleteConversation(activeConversationId)}
            disabled={conversations.length === 0}
            title="删除当前对话"
            aria-label="删除当前对话"
          >
            <PawTrashIcon width={16} height={16} />
          </button>
          <button
            className="paw-icon-button"
            type="button"
            onClick={onExportConversation}
            disabled={!activeConversationId}
            title="导出当前对话"
            aria-label="导出当前对话"
          >
            <PawDownloadIcon width={16} height={16} />
          </button>
          <button
            className="paw-icon-button"
            type="button"
            onClick={onOpenSettings}
            title="设置"
            aria-label="设置"
          >
            <PawSettingsIcon width={16} height={16} />
          </button>
          <button
            className="paw-icon-button"
            type="button"
            onClick={onLogout}
            title="退出登录"
            aria-label="退出登录"
          >
            <PawLogoutIcon width={16} height={16} />
          </button>
          <button className="paw-button primary paw-sidebar-new-button" type="button" onClick={onNewConversation}>
            <PawPlusIcon width={16} height={16} />
            新对话
          </button>
        </div>
        <div className="paw-sidebar-user paw-sidebar-user-footer">
          <div>{user?.name || "已登录"}</div>
          <div>{user?.email || session.user?.email || "本地会话"}</div>
        </div>
      </div>
      <div className="paw-sidebar-drag" onPointerDown={onDragStart}>
        <PawDragIcon width={16} height={16} />
      </div>
      {searchOpen ? (
        <PawModal title="搜索对话" onClose={() => setSearchOpen(false)}>
          <div className="paw-search-modal">
            <input
              className="paw-search-input"
              value={searchQuery}
              autoFocus
              placeholder="搜索标题或消息内容"
              onChange={(event) => setSearchQuery(event.currentTarget.value)}
            />
            <div className="paw-search-results">
              {searchResults.length === 0 ? (
                <div className="paw-search-empty">没有找到匹配的对话。</div>
              ) : (
                searchResults.map((conversation) => (
                  <button
                    type="button"
                    className="paw-search-result"
                    key={conversation.id}
                    onClick={() => {
                      onSelectConversation(conversation.id);
                      setSearchOpen(false);
                      onCloseMobile();
                    }}
                  >
                    <strong>{conversation.title}</strong>
                    <span>
                      {conversation.messages.at(-1)?.content ||
                        `${conversation.messages.length} 条消息`}
                    </span>
                    <small>{formatConversationDate(conversation.updatedAt)}</small>
                  </button>
                ))
              )}
            </div>
          </div>
        </PawModal>
      ) : null}
    </aside>
  );
}
