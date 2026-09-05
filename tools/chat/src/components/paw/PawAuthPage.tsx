"use client";

import type { FormEventHandler } from "react";
import { PawCheckIcon } from "./PawIcons";

interface PawAuthPageProps {
  email: string;
  password: string;
  busy: boolean;
  error: string | null;
  onEmailChange: (value: string) => void;
  onPasswordChange: (value: string) => void;
  onSubmit: FormEventHandler<HTMLFormElement>;
}

export function PawAuthPage({
  email,
  password,
  busy,
  error,
  onEmailChange,
  onPasswordChange,
  onSubmit,
}: PawAuthPageProps) {
  return (
    <div className="paw-auth">
      <section className="paw-auth-card">
        <div className="paw-auth-kicker">sub2api</div>
        <h1 className="paw-auth-title">Chat</h1>
        <p className="paw-auth-copy">使用 sub2api 账号登录</p>

        <form className="paw-auth-form" onSubmit={onSubmit}>
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
            {busy ? "..." : "登录"}
          </button>
        </form>
      </section>
    </div>
  );
}
