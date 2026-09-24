"use client";

// The 「电脑 · 同步会话」 group at the top of the sidebar: the conversations paired
// computers share, listed like this app's own conversations, collapsible.

import { useCallback, useEffect, useState } from "react";

import { listDevices, listSessions } from "../../client/remote/api";
import type { RemoteDevice, RemoteSessionSummary } from "../../client/remote/protocol";
import type { StoredPairing } from "../../client/remote/store";
import { PawRefreshIcon, PawSettingsIcon } from "./PawIcons";

const OPEN_KEY = "paw-remote-sidebar-open:v1";

const STATUS_TEXT: Record<string, string> = {
  active: "运行中",
  idle: "空闲",
  notLoaded: "未打开",
  systemError: "出错",
  unknown: "状态未知",
  missing: "已不存在",
};

interface ComputerEntry {
  pairing: StoredPairing;
  device: RemoteDevice | null;
  sessions: RemoteSessionSummary[];
  error: string | null;
}

export interface PawRemoteSidebarProps {
  /** Which shared conversation is open in the main pane, as `${deviceId}|${threadId}`. */
  activeKey: string | null;
  /** Bumped by the parent to make the list reload, e.g. after a conversation was revoked. */
  reloadToken: number;
  onOpenSession: (pairing: StoredPairing, threadId: string, title: string) => void;
  onManage: () => void;
}

export function PawRemoteSidebar({ activeKey, reloadToken, onOpenSession, onManage }: PawRemoteSidebarProps) {
  const [open, setOpen] = useState(false);
  const [computers, setComputers] = useState<ComputerEntry[] | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    try {
      setOpen(window.localStorage.getItem(OPEN_KEY) === "1");
    } catch {
      // Stays collapsed.
    }
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const devices = await listDevices();
      const entries = await Promise.all(
        devices
          .filter((d) => d.pairing.status === "active")
          .map(async ({ pairing, device }): Promise<ComputerEntry> => {
            if (!device?.online) return { pairing, device, sessions: [], error: null };
            try {
              return { pairing, device, sessions: (await listSessions(pairing)).sessions, error: null };
            } catch (err) {
              return { pairing, device, sessions: [], error: err instanceof Error ? err.message : "读取失败" };
            }
          }),
      );
      setComputers(entries);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (open) void load();
  }, [open, load, reloadToken]);

  const toggle = () => {
    setOpen((value) => {
      try {
        window.localStorage.setItem(OPEN_KEY, value ? "0" : "1");
      } catch {
        // Not remembered; harmless.
      }
      return !value;
    });
  };

  return (
    <div className="paw-remote-sidebar">
      <div className="paw-remote-sidebar-head">
        <button type="button" className="paw-remote-sidebar-toggle" onClick={toggle} aria-expanded={open}>
          <span className={`paw-remote-sidebar-caret ${open ? "open" : ""}`}>›</span>
          电脑 · 同步会话
        </button>
        {open ? (
          <button type="button" className="paw-icon-button" title="刷新" aria-label="刷新" disabled={loading} onClick={() => void load()}>
            <PawRefreshIcon width={14} height={14} />
          </button>
        ) : null}
        <button type="button" className="paw-icon-button" title="配对与管理" aria-label="配对与管理" onClick={onManage}>
          <PawSettingsIcon width={14} height={14} />
        </button>
      </div>

      {open ? (
        <div className="paw-remote-sidebar-body">
          {computers !== null && computers.length === 0 ? (
            <button type="button" className="paw-remote-sidebar-empty" onClick={onManage}>
              还没有配对的电脑，去配对 ›
            </button>
          ) : null}
          {(computers ?? []).map(({ pairing, device, sessions, error }) => (
            <div key={pairing.deviceId} className="paw-remote-sidebar-computer">
              <div className="paw-remote-sidebar-computer-name">
                <span className={`paw-remote-dot ${device?.online ? "on" : ""}`} />
                {pairing.deviceName}
                {!device?.online ? <small>不在线</small> : null}
              </div>
              {error ? <div className="paw-remote-sidebar-note">{error}</div> : null}
              {device?.online && !error && sessions.length === 0 ? (
                <div className="paw-remote-sidebar-note">电脑上还没有勾选要同步的会话</div>
              ) : null}
              {sessions.map((session) => {
                const key = `${pairing.deviceId}|${session.threadId}`;
                return (
                  <div key={key} className={`paw-conversation-item ${key === activeKey ? "active" : ""}`}>
                    <button
                      type="button"
                      className="paw-conversation-select"
                      onClick={() => onOpenSession(pairing, session.threadId, session.title)}
                    >
                      <span className="paw-conversation-title">{session.title}</span>
                      <span className="paw-conversation-info">
                        <span>{STATUS_TEXT[session.status] ?? session.status}</span>
                        <span className={session.permission === "full_access" ? "paw-remote-danger" : ""}>
                          {session.permission === "full_access" ? "完全访问" : session.permission === "auto" ? "自动" : "沙箱"}
                        </span>
                      </span>
                    </button>
                  </div>
                );
              })}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}
