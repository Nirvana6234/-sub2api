"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import QRCode from "qrcode";
import {
  cancelPawPaymentOrder,
  createPawPaymentOrder,
  fetchPawPaymentCheckoutInfo,
  fetchPawPaymentOrder,
} from "@/client/paw/api";
import type {
  PawPaymentCheckoutInfo,
  PawPaymentMethodLimit,
  PawPaymentOrder,
  PawPaymentOrderCreateResult,
} from "@/client/paw/types";
import { PawCheckIcon, PawCloseIcon, PawDownloadIcon, PawRefreshIcon } from "./PawIcons";
import { PawModal } from "./PawModal";

const PRESET_AMOUNTS = [10, 20, 50, 100, 200, 500, 1000, 2000, 5000];
const POLL_INTERVAL_MS = 3000;

interface PawPaymentModalProps {
  balance?: number;
  onCompleted: () => void;
  onClose: () => void;
}

function paymentMethodLabel(type: string, method?: PawPaymentMethodLimit): string {
  const knownLabels: Record<string, string> = {
    alipay: "支付宝",
    wxpay: "微信支付",
    stripe: "银行卡 / Stripe",
    easypay: "在线支付",
    airwallex: "Airwallex",
  };
  return knownLabels[type.toLowerCase()] || method?.display_name || type;
}

function currencySymbol(currency?: string): string {
  const normalized = (currency || "CNY").toUpperCase();
  if (normalized === "CNY" || normalized === "RMB") return "¥";
  if (normalized === "EUR") return "€";
  if (normalized === "GBP") return "£";
  return "$";
}

function formatMoney(value: number, currency?: string): string {
  return `${currencySymbol(currency)}${value.toFixed(2)}`;
}

function normalizeStatus(status: string | undefined): string {
  return String(status || "").trim().toUpperCase();
}

function isSuccessStatus(status: string | undefined): boolean {
  return ["PAID", "RECHARGING", "COMPLETED"].includes(normalizeStatus(status));
}

function isTerminalStatus(status: string | undefined): boolean {
  return [
    "PAID",
    "RECHARGING",
    "COMPLETED",
    "EXPIRED",
    "CANCELLED",
    "FAILED",
  ].includes(normalizeStatus(status));
}

function secondsUntil(value?: string): number {
  if (!value) return 0;
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return 0;
  return Math.max(0, Math.ceil((timestamp - Date.now()) / 1000));
}

function countdownLabel(seconds: number): string {
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return `${String(minutes).padStart(2, "0")}:${String(remainder).padStart(2, "0")}`;
}

function validAmount(
  value: number,
  checkout: PawPaymentCheckoutInfo | null,
  method: PawPaymentMethodLimit | undefined,
): boolean {
  if (!checkout || checkout.balance_disabled || !method || !method.available || value <= 0) {
    return false;
  }
  if (checkout.global_min > 0 && value < checkout.global_min) return false;
  if (checkout.global_max > 0 && value > checkout.global_max) return false;
  if (method.single_min > 0 && value < method.single_min) return false;
  if (method.single_max > 0 && value > method.single_max) return false;
  return Number.isFinite(value) && Math.round(value * 100) === value * 100;
}

export function PawPaymentModal({
  balance,
  onCompleted,
  onClose,
}: PawPaymentModalProps) {
  const [checkout, setCheckout] = useState<PawPaymentCheckoutInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [amountText, setAmountText] = useState("");
  const [selectedMethod, setSelectedMethod] = useState("");
  const [createdOrder, setCreatedOrder] = useState<PawPaymentOrderCreateResult | null>(null);
  const [order, setOrder] = useState<PawPaymentOrder | null>(null);
  const [secondsRemaining, setSecondsRemaining] = useState(0);
  const [qrError, setQrError] = useState<string | null>(null);
  const qrCanvasRef = useRef<HTMLCanvasElement>(null);
  const completedRaisedRef = useRef(false);

  const methods = useMemo(
    () =>
      Object.entries(checkout?.methods || {}).filter(([, method]) => method.available),
    [checkout],
  );
  const selectedMethodLimit = checkout?.methods[selectedMethod];
  const amount = Number(amountText);
  const amountIsValid = validAmount(amount, checkout, selectedMethodLimit);
  const feeRate = checkout?.recharge_fee_rate || 0;
  const fee = amountIsValid ? Math.round(amount * feeRate) / 100 : 0;
  const total = amountIsValid ? amount + fee : 0;
  const multiplier =
    checkout?.balance_recharge_multiplier && checkout.balance_recharge_multiplier > 0
      ? checkout.balance_recharge_multiplier
      : 1;
  const creditedAmount = amountIsValid ? Math.round(amount * multiplier * 100) / 100 : 0;
  const orderCurrency = createdOrder?.currency || selectedMethodLimit?.currency || "CNY";
  const orderStatus = normalizeStatus(order?.status);
  const hasActiveOrder = Boolean(createdOrder && !isTerminalStatus(orderStatus));

  const loadCheckout = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const nextCheckout = await fetchPawPaymentCheckoutInfo();
      setCheckout(nextCheckout);
      const nextMethods = Object.entries(nextCheckout.methods).filter(
        ([, method]) => method.available,
      );
      const nextMethod = nextMethods[0]?.[0] || "";
      setSelectedMethod(nextMethod);
      const firstMethod = nextMethods[0]?.[1];
      const preset = PRESET_AMOUNTS.find((candidate) =>
        validAmount(candidate, nextCheckout, firstMethod),
      );
      const fallback =
        firstMethod?.single_min ||
        nextCheckout.global_min ||
        PRESET_AMOUNTS[0];
      setAmountText(String(preset || fallback));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : "支付配置加载失败");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadCheckout();
  }, [loadCheckout]);

  useEffect(() => {
    if (!selectedMethod || !checkout) return;
    const method = checkout.methods[selectedMethod];
    const current = Number(amountText);
    if (validAmount(current, checkout, method)) return;
    const preset = PRESET_AMOUNTS.find((candidate) =>
      validAmount(candidate, checkout, method),
    );
    const fallback = method?.single_min || checkout.global_min || PRESET_AMOUNTS[0];
    setAmountText(String(preset || fallback));
  }, [amountText, checkout, selectedMethod]);

  useEffect(() => {
    if (!createdOrder?.qr_code || !qrCanvasRef.current) {
      setQrError(null);
      return;
    }
    setQrError(null);
    void QRCode.toCanvas(qrCanvasRef.current, createdOrder.qr_code, {
      width: 228,
      margin: 2,
      errorCorrectionLevel: "M",
    }).catch(() => {
      setQrError("二维码生成失败，请使用支付链接");
    });
  }, [createdOrder?.qr_code]);

  useEffect(() => {
    if (!createdOrder) return;
    const timer = window.setInterval(() => {
      setSecondsRemaining(secondsUntil(createdOrder.expires_at));
    }, 1000);
    setSecondsRemaining(secondsUntil(createdOrder.expires_at));
    return () => window.clearInterval(timer);
  }, [createdOrder]);

  useEffect(() => {
    if (!createdOrder || !order || isTerminalStatus(order.status)) return;
    let cancelled = false;
    const timer = window.setInterval(() => {
      void fetchPawPaymentOrder(createdOrder.order_id)
        .then((nextOrder) => {
          if (!cancelled) setOrder(nextOrder);
        })
        .catch((pollError) => {
          if (!cancelled) {
            setError(pollError instanceof Error ? pollError.message : "订单状态查询失败");
          }
        });
    }, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [createdOrder, order]);

  useEffect(() => {
    if (!order || !isSuccessStatus(order.status) || completedRaisedRef.current) return;
    completedRaisedRef.current = true;
    onCompleted();
  }, [onCompleted, order]);

  async function handleCreateOrder() {
    if (!checkout || !selectedMethodLimit || !amountIsValid || submitting || hasActiveOrder) {
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const result = await createPawPaymentOrder({
        amount: Math.round(amount * 100) / 100,
        payment_type: selectedMethod,
        order_type: "balance",
        is_mobile: window.matchMedia("(max-width: 760px)").matches,
        payment_source: "hosted_redirect",
      });
      setCreatedOrder(result);
      setOrder({
        id: result.order_id,
        amount: result.amount,
        pay_amount: result.pay_amount,
        fee_rate: result.fee_rate,
        currency: result.currency,
        payment_type: result.payment_type || selectedMethod,
        out_trade_no: result.out_trade_no || "",
        status: "PENDING",
        order_type: "balance",
        expires_at: result.expires_at,
      });
      setSecondsRemaining(secondsUntil(result.expires_at));
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "创建支付订单失败");
    } finally {
      setSubmitting(false);
    }
  }

  async function handleCancelOrder() {
    if (!createdOrder || cancelling || isTerminalStatus(order?.status)) return;
    setCancelling(true);
    setError(null);
    try {
      await cancelPawPaymentOrder(createdOrder.order_id);
      setOrder((current) => (current ? { ...current, status: "CANCELLED" } : current));
    } catch (cancelError) {
      setError(cancelError instanceof Error ? cancelError.message : "取消订单失败");
    } finally {
      setCancelling(false);
    }
  }

  function openPayUrl() {
    if (!createdOrder?.pay_url) return;
    window.open(createdOrder.pay_url, "_blank", "noopener,noreferrer");
  }

  function downloadQrCode() {
    const canvas = qrCanvasRef.current;
    if (!canvas) return;
    const link = document.createElement("a");
    link.href = canvas.toDataURL("image/png");
    link.download = `gongfei-payment-${createdOrder?.order_id || "qrcode"}.png`;
    link.click();
  }

  const statusMessage =
    orderStatus === "COMPLETED" || orderStatus === "RECHARGING" || orderStatus === "PAID"
      ? "充值成功，账户余额已更新。"
      : orderStatus === "EXPIRED"
        ? "订单已过期，请重新发起充值。"
        : orderStatus === "CANCELLED"
          ? "订单已取消。"
          : orderStatus === "FAILED"
            ? "支付失败，请重新发起充值。"
            : "请完成支付，页面会自动查询订单状态。";

  return (
    <PawModal title="账户充值" onClose={onClose}>
      {loading ? (
        <div className="paw-payment-loading">
          <PawRefreshIcon width={18} height={18} />
          正在读取支付配置
        </div>
      ) : createdOrder ? (
        <div className="paw-payment-flow">
          <div className="paw-payment-status">
            <strong>{statusMessage}</strong>
            <span>订单 #{createdOrder.order_id}</span>
          </div>
          <div className="paw-payment-order-summary">
            <span>实际支付</span>
            <strong>{formatMoney(createdOrder.pay_amount, orderCurrency)}</strong>
          </div>
          {createdOrder.qr_code ? (
            <div className="paw-payment-qr-section">
              <div className="paw-payment-qr-frame">
                <canvas ref={qrCanvasRef} aria-label="支付二维码" />
              </div>
              {qrError ? <p className="paw-payment-error">{qrError}</p> : null}
              <p>使用手机扫码完成支付</p>
              <button type="button" className="paw-button" onClick={downloadQrCode}>
                <PawDownloadIcon width={15} height={15} />
                保存二维码
              </button>
            </div>
          ) : null}
          {createdOrder.pay_url ? (
            <button type="button" className="paw-button primary paw-payment-pay-link" onClick={openPayUrl}>
              打开支付页面
            </button>
          ) : null}
          <div className="paw-payment-countdown">
            <span>订单有效期</span>
            <strong>{secondsRemaining ? countdownLabel(secondsRemaining) : "已结束"}</strong>
          </div>
          {isSuccessStatus(order?.status) ? (
            <div className="paw-payment-success">
              <PawCheckIcon width={18} height={18} />
              充值已到账
            </div>
          ) : null}
          {!isTerminalStatus(order?.status) ? (
            <button
              type="button"
              className="paw-button danger paw-payment-cancel"
              onClick={() => void handleCancelOrder()}
              disabled={cancelling}
            >
              {cancelling ? "正在取消" : "取消订单"}
            </button>
          ) : null}
          {isTerminalStatus(order?.status) && !isSuccessStatus(order?.status) ? (
            <button type="button" className="paw-button primary" onClick={() => {
              setCreatedOrder(null);
              setOrder(null);
              setError(null);
            }}>
              重新充值
            </button>
          ) : null}
        </div>
      ) : (
        <div className="paw-payment-form">
          <div className="paw-payment-balance">
            <span>当前余额</span>
            <strong>{typeof balance === "number" ? formatMoney(balance, "CNY") : "暂不可用"}</strong>
          </div>
          {checkout?.balance_disabled ? (
            <div className="paw-payment-empty">服务端暂未开启余额充值。</div>
          ) : methods.length === 0 ? (
            <div className="paw-payment-empty">服务端未配置可用支付方式。</div>
          ) : (
            <>
              <div className="paw-payment-field">
                <label htmlFor="paw-payment-amount">充值金额</label>
                <input
                  id="paw-payment-amount"
                  className="paw-payment-input"
                  inputMode="decimal"
                  value={amountText}
                  onChange={(event) => setAmountText(event.currentTarget.value)}
                  placeholder="请输入充值金额"
                />
                <div className="paw-payment-presets">
                  {PRESET_AMOUNTS.map((preset) => (
                    <button
                      type="button"
                      className={Number(amountText) === preset ? "active" : ""}
                      key={preset}
                      onClick={() => setAmountText(String(preset))}
                    >
                      {preset}
                    </button>
                  ))}
                </div>
                {!amountIsValid && amountText ? (
                  <p className="paw-payment-error">
                    请输入符合服务端限制的金额
                    {checkout?.global_min ? `，最低 ${checkout.global_min}` : ""}
                    {checkout?.global_max ? `，最高 ${checkout.global_max}` : ""}
                  </p>
                ) : null}
              </div>
              <div className="paw-payment-field">
                <span className="paw-payment-label">支付方式</span>
                <div className="paw-payment-methods">
                  {methods.map(([type, method]) => (
                    <button
                      type="button"
                      key={type}
                      className={`paw-payment-method ${selectedMethod === type ? "active" : ""}`}
                      onClick={() => setSelectedMethod(type)}
                    >
                      <span>{paymentMethodLabel(type, method)}</span>
                      {selectedMethod === type ? <PawCheckIcon width={15} height={15} /> : null}
                    </button>
                  ))}
                </div>
              </div>
              {amountIsValid ? (
                <div className="paw-payment-breakdown">
                  <div><span>充值金额</span><strong>{formatMoney(amount, selectedMethodLimit?.currency)}</strong></div>
                  {feeRate > 0 ? <div><span>服务费 ({feeRate}%)</span><strong>{formatMoney(fee, selectedMethodLimit?.currency)}</strong></div> : null}
                  <div><span>实际支付</span><strong>{formatMoney(total, selectedMethodLimit?.currency)}</strong></div>
                  {multiplier !== 1 ? <div><span>到账余额</span><strong>{formatMoney(creditedAmount, "CNY")}</strong></div> : null}
                </div>
              ) : null}
              {checkout?.help_text ? <p className="paw-payment-help">{checkout.help_text}</p> : null}
              <button
                type="button"
                className="paw-button primary paw-payment-submit"
                onClick={() => void handleCreateOrder()}
                disabled={!amountIsValid || submitting}
              >
                {submitting ? "正在创建订单" : "创建支付订单"}
              </button>
            </>
          )}
          {error ? <p className="paw-payment-error">{error}</p> : null}
        </div>
      )}
      {error && createdOrder ? <p className="paw-payment-error">{error}</p> : null}
      {createdOrder && isSuccessStatus(order?.status) ? (
        <div className="paw-payment-modal-actions">
          <button type="button" className="paw-button primary" onClick={onClose}>
            完成
          </button>
        </div>
      ) : (
        <div className="paw-payment-modal-actions">
          <button type="button" className="paw-button" onClick={onClose}>
            <PawCloseIcon width={15} height={15} />
            关闭
          </button>
        </div>
      )}
    </PawModal>
  );
}
