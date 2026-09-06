"use client";

import { useEffect, useMemo, useState } from "react";
import {
  fetchPawUsageDashboardSnapshot,
  fetchPawUsageDashboardStats,
  fetchPawUsageLogs,
} from "@/client/paw/api";
import type {
  PawConfigData,
  PawGroup,
  PawSession,
  PawUsageDashboardSnapshot,
  PawUsageDashboardStats,
  PawUsageLog,
  PawUsageTrendPoint,
} from "@/client/paw/types";
import { PawCloseIcon, PawLogoutIcon, PawWalletIcon } from "./PawIcons";
import { PawModal } from "./PawModal";

interface PawProfileModalProps {
  config: PawConfigData | null;
  session: PawSession;
  currentGroup?: PawGroup;
  fullPage?: boolean;
  onOpenPayment: () => void;
  onLogout: () => void;
  onClose: () => void;
}

function formatMoney(value: number | undefined): string {
  return typeof value === "number" && Number.isFinite(value)
    ? `¥${value.toFixed(4)}`
    : "--";
}

function formatNumber(value: number | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "--";
  return value.toLocaleString("zh-CN");
}

function formatTokens(value: number): string {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)}K`;
  return value.toLocaleString("zh-CN");
}

function formatGroupRate(group?: PawGroup): string {
  if (!group) return "未选择";
  if (group.subscription_type === "subscription") return "订阅";
  const rate = group.user_rate_multiplier ?? group.rate_multiplier;
  return typeof rate === "number" && Number.isFinite(rate)
    ? `${rate.toFixed(3)}x`
    : "未提供";
}

function formatDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function normalizedTrend(snapshot: PawUsageDashboardSnapshot | null): PawUsageTrendPoint[] {
  return (snapshot?.trend ?? []).map((point) => {
    const inputTokens = point.input_tokens ?? 0;
    const outputTokens = point.output_tokens ?? 0;
    const cacheCreationTokens = point.cache_creation_tokens ?? 0;
    const cacheReadTokens = point.cache_read_tokens ?? 0;
    return {
      ...point,
      input_tokens: inputTokens,
      output_tokens: outputTokens,
      cache_creation_tokens: cacheCreationTokens,
      cache_read_tokens: cacheReadTokens,
      total_tokens:
        typeof point.total_tokens === "number"
          ? point.total_tokens
          : inputTokens + outputTokens + cacheCreationTokens + cacheReadTokens,
    };
  });
}

function TokenTrendChart({ trend }: { trend: PawUsageTrendPoint[] }) {
  const points = useMemo(() => {
    const values = trend.map((item) => item.total_tokens);
    const max = Math.max(...values, 1);
    return values.map((value, index) => {
      const x = trend.length <= 1 ? 320 : (index / (trend.length - 1)) * 600 + 20;
      const y = 164 - (value / max) * 132;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    });
  }, [trend]);

  if (trend.length === 0) {
    return <div className="paw-account-chart-empty">暂无 Token 使用数据</div>;
  }

  const first = trend[0]?.date ?? "";
  const last = trend.at(-1)?.date ?? "";
  const latest = trend.at(-1)?.total_tokens ?? 0;

  return (
    <svg viewBox="0 0 640 190" role="img" aria-label="Token 使用趋势图">
      {[32, 76, 120, 164].map((y) => (
        <line
          key={y}
          className="paw-account-chart-grid"
          x1="20"
          x2="620"
          y1={y}
          y2={y}
        />
      ))}
      <polyline className="paw-account-chart-line" points={points.join(" ")} />
      <text className="paw-account-chart-label" x="20" y="184">
        {first}
      </text>
      <text className="paw-account-chart-label" x="620" y="184" textAnchor="end">
        {last}
      </text>
      <text className="paw-account-chart-label" x="620" y="22" textAnchor="end">
        {formatTokens(latest)} Token
      </text>
    </svg>
  );
}

export function PawProfileModal({
  config,
  session,
  currentGroup,
  fullPage = false,
  onOpenPayment,
  onLogout,
  onClose,
}: PawProfileModalProps) {
  const user = config?.user ?? session.user;
  const [stats, setStats] = useState<PawUsageDashboardStats | null>(null);
  const [snapshot, setSnapshot] = useState<PawUsageDashboardSnapshot | null>(null);
  const [logs, setLogs] = useState<PawUsageLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");

  useEffect(() => {
    let active = true;
    setLoading(true);
    setLoadError("");
    void Promise.allSettled([
      fetchPawUsageDashboardStats(),
      fetchPawUsageDashboardSnapshot(30),
      fetchPawUsageLogs(8),
    ]).then((results) => {
      if (!active) return;
      const [statsResult, snapshotResult, logsResult] = results;
      if (statsResult.status === "fulfilled") setStats(statsResult.value);
      if (snapshotResult.status === "fulfilled") setSnapshot(snapshotResult.value);
      if (logsResult.status === "fulfilled") setLogs(logsResult.value.items);
      if (results.some((result) => result.status === "rejected")) {
        setLoadError("部分用量数据暂时无法加载");
      }
      setLoading(false);
    });
    return () => {
      active = false;
    };
  }, []);

  const trend = normalizedTrend(snapshot);
  const groupNameById = useMemo(
    () => new Map((config?.groups ?? []).map((group) => [group.id, group.name])),
    [config?.groups],
  );

  const content = (
    <div className="paw-account-details">
      <div className="paw-profile-identity">
        <div className="paw-profile-avatar">G</div>
        <div>
          <strong>{user?.name || "已登录账户"}</strong>
          <span>{user?.email || "共飞平台账户"}</span>
        </div>
      </div>

      <div className="paw-account-summary-grid">
        <div className="paw-account-summary-card">
          <span>账户余额</span>
          <strong>{formatMoney(user?.balance)}</strong>
        </div>
        <div className="paw-account-summary-card">
          <span>冻结余额</span>
          <strong>{formatMoney(user?.frozen_balance)}</strong>
        </div>
        <div className="paw-account-summary-card">
          <span>当前分组倍率</span>
          <strong>{formatGroupRate(currentGroup)}</strong>
        </div>
      </div>

      {loading ? (
        <div className="paw-account-loading">正在加载 Token 使用数据...</div>
      ) : (
        <>
          <div className="paw-account-summary-grid">
            <div className="paw-account-summary-card">
              <span>累计请求</span>
              <strong>{formatNumber(stats?.total_requests)}</strong>
            </div>
            <div className="paw-account-summary-card">
              <span>累计 Token</span>
              <strong>{formatNumber(stats?.total_tokens)}</strong>
            </div>
            <div className="paw-account-summary-card">
              <span>今日 Token</span>
              <strong>{formatNumber(stats?.today_tokens)}</strong>
            </div>
          </div>

          <section className="paw-account-section">
            <div className="paw-account-section-head">
              <h3>Token 使用趋势</h3>
              <span>近 30 天</span>
            </div>
            <div className="paw-account-chart">
              <TokenTrendChart trend={trend} />
            </div>
          </section>

          <section className="paw-account-section">
            <div className="paw-account-section-head">
              <h3>Token 使用记录</h3>
              <span>{logs.length > 0 ? `最近 ${logs.length} 条` : "暂无记录"}</span>
            </div>
            {logs.length > 0 ? (
              <div className="paw-account-table">
                <div className="paw-account-table-row paw-account-table-header">
                  <span>时间</span>
                  <span>模型 / 分组</span>
                  <span>Token</span>
                  <span>费用</span>
                </div>
                {logs.map((log) => (
                  <div className="paw-account-table-row" key={log.id}>
                    <span>{formatDate(log.created_at)}</span>
                    <strong title={log.model}>
                      {log.model}
                      {log.group_id && groupNameById.get(log.group_id)
                        ? ` / ${groupNameById.get(log.group_id)}`
                        : ""}
                    </strong>
                    <span>
                      {formatTokens(
                        log.input_tokens +
                          log.output_tokens +
                          log.cache_creation_tokens +
                          log.cache_read_tokens,
                      )}
                    </span>
                    <span>{formatMoney(log.actual_cost)}</span>
                  </div>
                ))}
              </div>
            ) : (
              <div className="paw-account-chart-empty">暂无 Token 使用记录</div>
            )}
          </section>
        </>
      )}

      {loadError ? <div className="paw-account-error">{loadError}</div> : null}

      <div className="paw-profile-actions">
        <button type="button" className="paw-button" onClick={onOpenPayment}>
          <PawWalletIcon width={15} height={15} />
          账户充值
        </button>
        <button type="button" className="paw-button danger" onClick={onLogout}>
          <PawLogoutIcon width={15} height={15} />
          退出账号
        </button>
      </div>
    </div>
  );

  if (fullPage) {
    return (
      <main className="paw-account-page">
        <header className="paw-account-page-head">
          <div>
            <h1>账户详情</h1>
            <p>查看账户余额、Token 使用情况和消费记录</p>
          </div>
          <button
            type="button"
            className="paw-button"
            onClick={onClose}
            title="返回对话"
          >
            <PawCloseIcon width={15} height={15} />
            返回对话
          </button>
        </header>
        <div className="paw-account-page-scroll">{content}</div>
      </main>
    );
  }

  return (
    <PawModal title="账户详情" onClose={onClose}>
      {content}
    </PawModal>
  );
}
