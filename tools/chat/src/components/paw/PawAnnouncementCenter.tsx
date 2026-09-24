"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  fetchPawAnnouncements,
  markPawAnnouncementRead,
} from "@/client/paw/api";
import type { PawAnnouncement } from "@/client/paw/types";
import { PawBellIcon, PawCheckIcon, PawCloseIcon } from "./PawIcons";
import { PawMarkdown } from "./PawMarkdown";

const FETCH_THROTTLE_MS = 20 * 60 * 1000;

function formatAnnouncementDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
    year: "numeric",
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function formatRelativeTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const elapsed = Date.now() - date.getTime();
  if (elapsed < 60 * 1000) return "刚刚";
  if (elapsed < 60 * 60 * 1000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
  if (elapsed < 24 * 60 * 60 * 1000) return `${Math.floor(elapsed / 3_600_000)} 小时前`;
  if (elapsed < 7 * 24 * 60 * 60 * 1000) return `${Math.floor(elapsed / 86_400_000)} 天前`;
  return formatAnnouncementDate(value);
}

export function PawAnnouncementCenter() {
  const [announcements, setAnnouncements] = useState<PawAnnouncement[]>([]);
  const [loading, setLoading] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [selectedAnnouncement, setSelectedAnnouncement] =
    useState<PawAnnouncement | null>(null);
  const [popupQueue, setPopupQueue] = useState<PawAnnouncement[]>([]);
  const [currentPopup, setCurrentPopup] = useState<PawAnnouncement | null>(null);
  const lastFetchAtRef = useRef(0);
  const shownPopupIdsRef = useRef(new Set<number>());

  const unreadCount = useMemo(
    () => announcements.filter((item) => !item.read_at).length,
    [announcements],
  );

  const fetchAnnouncements = useCallback(async (force = false) => {
    const now = Date.now();
    if (!force && now - lastFetchAtRef.current < FETCH_THROTTLE_MS) return;
    lastFetchAtRef.current = now;
    setLoading(true);

    try {
      const nextAnnouncements = await fetchPawAnnouncements();
      setAnnouncements(nextAnnouncements.slice(0, 20));
      const newPopups = nextAnnouncements.filter(
        (item) =>
          item.notify_mode === "popup" &&
          !item.read_at &&
          !shownPopupIdsRef.current.has(item.id),
      );
      if (newPopups.length > 0) {
        newPopups.forEach((item) => shownPopupIdsRef.current.add(item.id));
        setPopupQueue((current) => {
          const existingIds = new Set(current.map((item) => item.id));
          return [
            ...current,
            ...newPopups.filter(
              (item) => item.id !== currentPopup?.id && !existingIds.has(item.id),
            ),
          ];
        });
      }
    } catch (error) {
      lastFetchAtRef.current = 0;
      console.error("公告加载失败:", error);
    } finally {
      setLoading(false);
    }
  }, [currentPopup?.id]);

  useEffect(() => {
    void fetchAnnouncements();
    const timer = window.setInterval(() => {
      void fetchAnnouncements();
    }, FETCH_THROTTLE_MS);
    return () => window.clearInterval(timer);
  }, [fetchAnnouncements]);

  useEffect(() => {
    if (currentPopup || popupQueue.length === 0) return;
    const [next, ...rest] = popupQueue;
    if (!next) return;
    setCurrentPopup(next);
    setPopupQueue(rest);
  }, [currentPopup, popupQueue]);

  useEffect(() => {
    const hasOverlay = listOpen || detailOpen || Boolean(currentPopup);
    document.body.style.overflow = hasOverlay ? "hidden" : "";
    return () => {
      document.body.style.overflow = "";
    };
  }, [currentPopup, detailOpen, listOpen]);

  const markAsRead = useCallback(async (id: number) => {
    try {
      await markPawAnnouncementRead(id);
      const readAt = new Date().toISOString();
      setAnnouncements((current) =>
        current.map((item) => (item.id === id ? { ...item, read_at: readAt } : item)),
      );
      setSelectedAnnouncement((current) =>
        current?.id === id ? { ...current, read_at: readAt } : current,
      );
    } catch (error) {
      console.error("公告已读状态更新失败:", error);
    }
  }, []);

  function openDetail(announcement: PawAnnouncement) {
    setSelectedAnnouncement(announcement);
    setListOpen(false);
    setDetailOpen(true);
    if (!announcement.read_at) void markAsRead(announcement.id);
  }

  async function markAllAsRead() {
    const unread = announcements.filter((item) => !item.read_at);
    await Promise.all(unread.map((item) => markAsRead(item.id)));
  }

  function closeDetail() {
    setDetailOpen(false);
    setSelectedAnnouncement(null);
  }

  function handleEscape(event: KeyboardEvent) {
    if (event.key !== "Escape") return;
    if (currentPopup) {
      setCurrentPopup(null);
    } else if (detailOpen) {
      closeDetail();
    } else if (listOpen) {
      setListOpen(false);
    }
  }

  useEffect(() => {
    document.addEventListener("keydown", handleEscape);
    return () => document.removeEventListener("keydown", handleEscape);
  });

  return (
    <>
      <button
        type="button"
        className={`paw-icon-button paw-announcement-trigger ${
          unreadCount > 0 ? "has-unread" : ""
        }`}
        title="公告"
        aria-label={`公告${unreadCount > 0 ? `，${unreadCount} 条未读` : ""}`}
        onClick={() => {
          setListOpen(true);
          void fetchAnnouncements();
        }}
      >
        <PawBellIcon width={16} height={16} />
        {unreadCount > 0 ? (
          <span className="paw-announcement-dot" aria-hidden="true" />
        ) : null}
      </button>

      {typeof document !== "undefined"
        ? createPortal(
            <>
              {listOpen ? (
                <div
                  className="paw-announcement-backdrop"
                  role="presentation"
                  onMouseDown={() => setListOpen(false)}
                >
                  <section
                    className="paw-announcement-modal"
                    role="dialog"
                    aria-modal="true"
                    aria-label="公告"
                    onMouseDown={(event) => event.stopPropagation()}
                  >
                    <header className="paw-announcement-head">
                      <div>
                        <div className="paw-announcement-title-row">
                          <PawBellIcon width={18} height={18} />
                          <h2>公告</h2>
                        </div>
                        <p>
                          {unreadCount > 0
                            ? `${unreadCount} 条未读公告`
                            : "暂无未读公告"}
                        </p>
                      </div>
                      <div className="paw-announcement-head-actions">
                        {unreadCount > 0 ? (
                          <button
                            type="button"
                            className="paw-button primary"
                            onClick={() => void markAllAsRead()}
                            disabled={loading}
                          >
                            <PawCheckIcon width={15} height={15} />
                            全部已读
                          </button>
                        ) : null}
                        <button
                          type="button"
                          className="paw-icon-button"
                          aria-label="关闭公告"
                          onClick={() => setListOpen(false)}
                        >
                          <PawCloseIcon width={16} height={16} />
                        </button>
                      </div>
                    </header>

                    <div className="paw-announcement-list">
                      {loading && announcements.length === 0 ? (
                        <div className="paw-announcement-empty">正在加载公告...</div>
                      ) : announcements.length === 0 ? (
                        <div className="paw-announcement-empty">
                          <strong>暂无公告</strong>
                          <span>暂时没有需要查看的系统公告。</span>
                        </div>
                      ) : (
                        announcements.map((announcement) => (
                          <button
                            type="button"
                            className={`paw-announcement-item ${
                              announcement.read_at ? "" : "unread"
                            }`}
                            key={announcement.id}
                            onClick={() => openDetail(announcement)}
                          >
                            <span className="paw-announcement-item-icon">
                              {announcement.read_at ? (
                                <PawCheckIcon width={17} height={17} />
                              ) : (
                                <PawBellIcon width={17} height={17} />
                              )}
                            </span>
                            <span className="paw-announcement-item-copy">
                              <strong>{announcement.title}</strong>
                              <span>{formatRelativeTime(announcement.created_at)}</span>
                            </span>
                            {!announcement.read_at ? (
                              <span className="paw-announcement-unread-label">
                                未读
                              </span>
                            ) : null}
                          </button>
                        ))
                      )}
                    </div>
                  </section>
                </div>
              ) : null}

              {detailOpen && selectedAnnouncement ? (
                <div
                  className="paw-announcement-backdrop"
                  role="presentation"
                  onMouseDown={closeDetail}
                >
                  <section
                    className="paw-announcement-modal detail"
                    role="dialog"
                    aria-modal="true"
                    aria-label={selectedAnnouncement.title}
                    onMouseDown={(event) => event.stopPropagation()}
                  >
                    <header className="paw-announcement-head">
                      <div className="paw-announcement-detail-heading">
                        <span className="paw-announcement-kicker">公告</span>
                        <h2>{selectedAnnouncement.title}</h2>
                        <span>
                          {formatAnnouncementDate(selectedAnnouncement.created_at)}
                        </span>
                      </div>
                      <button
                        type="button"
                        className="paw-icon-button"
                        aria-label="关闭公告详情"
                        onClick={closeDetail}
                      >
                        <PawCloseIcon width={16} height={16} />
                      </button>
                    </header>
                    <div className="paw-announcement-detail-body">
                      <PawMarkdown content={selectedAnnouncement.content} />
                    </div>
                    <footer className="paw-announcement-detail-actions">
                      <button
                        type="button"
                        className="paw-button"
                        onClick={() => {
                          closeDetail();
                          setListOpen(true);
                        }}
                      >
                        返回公告列表
                      </button>
                      <button
                        type="button"
                        className="paw-button primary"
                        onClick={closeDetail}
                      >
                        <PawCheckIcon width={15} height={15} />
                        知道了
                      </button>
                    </footer>
                  </section>
                </div>
              ) : null}

              {currentPopup ? (
                <div
                  className="paw-announcement-backdrop popup"
                  role="presentation"
                  onMouseDown={(event) => {
                    if (event.currentTarget === event.target) setCurrentPopup(null);
                  }}
                >
                  <section
                    className="paw-announcement-modal popup"
                    role="dialog"
                    aria-modal="true"
                    aria-label={currentPopup.title}
                    onMouseDown={(event) => event.stopPropagation()}
                  >
                    <header className="paw-announcement-head popup">
                      <div className="paw-announcement-detail-heading">
                        <span className="paw-announcement-kicker">重要公告</span>
                        <h2>{currentPopup.title}</h2>
                        <span>
                          {formatAnnouncementDate(currentPopup.created_at)}
                        </span>
                      </div>
                    </header>
                    <div className="paw-announcement-detail-body">
                      <PawMarkdown content={currentPopup.content} />
                    </div>
                    <footer className="paw-announcement-detail-actions">
                      <button
                        type="button"
                        className="paw-button primary"
                        onClick={() => {
                          const id = currentPopup.id;
                          setCurrentPopup(null);
                          void markAsRead(id);
                        }}
                      >
                        <PawCheckIcon width={15} height={15} />
                        知道了
                      </button>
                    </footer>
                  </section>
                </div>
              ) : null}
            </>,
            document.body,
          )
        : null}
    </>
  );
}
