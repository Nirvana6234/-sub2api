"use client";

import { useCallback, type FormEventHandler } from "react";
import { PawCheckIcon } from "./PawIcons";
import { PawTurnstile } from "./PawTurnstile";
import { PawDownloadIcon } from "./PawIcons";
import { getClientConfig } from "@/config/client";
import type { PawPublicSettings } from "@/client/paw/types";

type PawAuthMode = "login" | "register";

interface PawAuthPageProps {
  mode: PawAuthMode;
  settings: PawPublicSettings | null;
  settingsBusy: boolean;
  email: string;
  password: string;
  registerPassword: string;
  registerConfirmPassword: string;
  verifyCode: string;
  invitationCode: string;
  promoCode: string;
  busy: boolean;
  verifyBusy: boolean;
  verifyCountdown: number;
  captchaResetKey: number;
  error: string | null;
  onEmailChange: (value: string) => void;
  onPasswordChange: (value: string) => void;
  onRegisterPasswordChange: (value: string) => void;
  onRegisterConfirmPasswordChange: (value: string) => void;
  onVerifyCodeChange: (value: string) => void;
  onInvitationCodeChange: (value: string) => void;
  onPromoCodeChange: (value: string) => void;
  onCaptchaTokenChange: (value: string) => void;
  onCaptchaError: () => void;
  onModeChange: (mode: PawAuthMode) => void;
  onSendVerifyCode: () => void;
  onLoginSubmit: FormEventHandler<HTMLFormElement>;
  onRegisterSubmit: FormEventHandler<HTMLFormElement>;
}

export function PawAuthPage({
  mode,
  settings,
  settingsBusy,
  email,
  password,
  registerPassword,
  registerConfirmPassword,
  verifyCode,
  invitationCode,
  promoCode,
  busy,
  verifyBusy,
  verifyCountdown,
  captchaResetKey,
  error,
  onEmailChange,
  onPasswordChange,
  onRegisterPasswordChange,
  onRegisterConfirmPasswordChange,
  onVerifyCodeChange,
  onInvitationCodeChange,
  onPromoCodeChange,
  onCaptchaTokenChange,
  onCaptchaError,
  onModeChange,
  onSendVerifyCode,
  onLoginSubmit,
  onRegisterSubmit,
}: PawAuthPageProps) {
  const registrationEnabled = settings?.registration_enabled === true;
  const installPath = `${getClientConfig().mountPath}/install` || "/install";
  const turnstileEnabled =
    settings?.turnstile_enabled === true && Boolean(settings.turnstile_site_key);
  const unsupportedCaptcha =
    !turnstileEnabled &&
    (settings?.tencent_captcha_enabled === true || settings?.aliyun_captcha_enabled === true);
  const emailVerifyEnabled = settings?.email_verify_enabled === true;

  const handleRegisterMode = useCallback(() => {
    onModeChange("register");
  }, [onModeChange]);

  const handleLoginMode = useCallback(() => {
    onModeChange("login");
  }, [onModeChange]);

  return (
    <div className="paw-auth">
      <section className="paw-auth-card">
        <div className="paw-auth-kicker">sub2api</div>
        <h1 className="paw-auth-title">共飞AI工作台</h1>
        <p className="paw-auth-copy">
          {mode === "login" ? "使用 sub2api 账号登录" : "注册 sub2api 账号后开始对话"}
        </p>

        {mode === "login" ? (
          <form className="paw-auth-form" onSubmit={onLoginSubmit}>
            <label className="paw-field">
              <span className="paw-field-label">邮箱</span>
              <input
                type="email"
                value={email}
                onChange={(event) => onEmailChange(event.currentTarget.value)}
                placeholder="请输入邮箱"
                autoComplete="email"
                required
              />
            </label>
            <label className="paw-field">
              <span className="paw-field-label">密码</span>
              <input
                type="password"
                value={password}
                onChange={(event) => onPasswordChange(event.currentTarget.value)}
                placeholder="请输入密码"
                autoComplete="current-password"
                required
              />
            </label>
            {error ? <div className="paw-banner warn">{error}</div> : null}
            <button className="paw-button primary" type="submit" disabled={busy}>
              <PawCheckIcon width={16} height={16} />
              {busy ? "登录中..." : "登录"}
            </button>
            {registrationEnabled ? (
              <button className="paw-auth-switch" type="button" onClick={handleRegisterMode}>
                没有账号？立即注册
              </button>
            ) : null}
            {settingsBusy ? <p className="paw-auth-hint">正在检查注册设置...</p> : null}
            <a className="paw-auth-install-link" href={installPath}>
              <PawDownloadIcon width={15} height={15} />
              安装到手机桌面
            </a>
          </form>
        ) : (
          <form className="paw-auth-form" onSubmit={onRegisterSubmit}>
            <label className="paw-field">
              <span className="paw-field-label">邮箱</span>
              <input
                type="email"
                value={email}
                onChange={(event) => onEmailChange(event.currentTarget.value)}
                placeholder="请输入邮箱"
                autoComplete="email"
                required
                disabled={busy || settingsBusy}
              />
            </label>
            <label className="paw-field">
              <span className="paw-field-label">密码</span>
              <input
                type="password"
                value={registerPassword}
                onChange={(event) => onRegisterPasswordChange(event.currentTarget.value)}
                placeholder="至少 6 位密码"
                autoComplete="new-password"
                required
                disabled={busy}
              />
            </label>
            <label className="paw-field">
              <span className="paw-field-label">确认密码</span>
              <input
                type="password"
                value={registerConfirmPassword}
                onChange={(event) => onRegisterConfirmPasswordChange(event.currentTarget.value)}
                placeholder="再次输入密码"
                autoComplete="new-password"
                required
                disabled={busy}
              />
            </label>

            {emailVerifyEnabled ? (
              <div className="paw-field">
                <span className="paw-field-label">邮箱验证码</span>
                <div className="paw-auth-inline">
                  <input
                    type="text"
                    value={verifyCode}
                    onChange={(event) => onVerifyCodeChange(event.currentTarget.value)}
                    placeholder="请输入验证码"
                    inputMode="numeric"
                    maxLength={6}
                    autoComplete="one-time-code"
                    required
                    disabled={busy}
                  />
                  <button
                    className="paw-button"
                    type="button"
                    onClick={onSendVerifyCode}
                    disabled={busy || verifyBusy || verifyCountdown > 0}
                  >
                    {verifyBusy
                      ? "发送中..."
                      : verifyCountdown > 0
                        ? `${verifyCountdown}s 后重试`
                        : "发送验证码"}
                  </button>
                </div>
              </div>
            ) : null}

            {settings?.invitation_code_enabled ? (
              <label className="paw-field">
                <span className="paw-field-label">邀请码</span>
                <input
                  type="text"
                  value={invitationCode}
                  onChange={(event) => onInvitationCodeChange(event.currentTarget.value)}
                  placeholder="请输入邀请码"
                  autoComplete="off"
                  required
                  disabled={busy}
                />
              </label>
            ) : null}

            {settings?.promo_code_enabled ? (
              <label className="paw-field">
                <span className="paw-field-label">
                  优惠码 <small>（可选）</small>
                </span>
                <input
                  type="text"
                  value={promoCode}
                  onChange={(event) => onPromoCodeChange(event.currentTarget.value)}
                  placeholder="请输入优惠码"
                  autoComplete="off"
                  disabled={busy}
                />
              </label>
            ) : null}

            {turnstileEnabled ? (
              <PawTurnstile
                key={captchaResetKey}
                siteKey={settings?.turnstile_site_key ?? ""}
                onToken={onCaptchaTokenChange}
                onExpired={onCaptchaError}
                onError={onCaptchaError}
              />
            ) : null}

            {unsupportedCaptcha ? (
              <div className="paw-banner warn">
                当前服务端启用了腾讯或阿里云安全验证，Chat 暂不支持该验证方式，请使用后台网页完成注册。
              </div>
            ) : null}
            {error ? <div className="paw-banner warn">{error}</div> : null}
            <button
              className="paw-button primary"
              type="submit"
              disabled={busy || settingsBusy || !registrationEnabled || unsupportedCaptcha}
            >
              <PawCheckIcon width={16} height={16} />
              {busy ? "注册中..." : "注册"}
            </button>
            <button className="paw-auth-switch" type="button" onClick={handleLoginMode}>
              已有账号？返回登录
            </button>
            <a className="paw-auth-install-link" href={installPath}>
              <PawDownloadIcon width={15} height={15} />
              安装到手机桌面
            </a>
          </form>
        )}
      </section>
    </div>
  );
}
