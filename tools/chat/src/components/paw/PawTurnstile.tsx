"use client";

import { useEffect, useRef } from "react";

interface TurnstileApi {
  render: (
    container: HTMLElement,
    options: {
      sitekey: string;
      callback: (token: string) => void;
      "expired-callback"?: () => void;
      "error-callback"?: () => void;
      theme?: "light" | "dark" | "auto";
      size?: "normal" | "compact" | "flexible";
    },
  ) => string;
  remove: (widgetId: string) => void;
}

declare global {
  interface Window {
    turnstile?: TurnstileApi;
    onPawTurnstileLoad?: () => void;
  }
}

let turnstileLoadPromise: Promise<void> | null = null;

function loadTurnstile(): Promise<void> {
  if (typeof window === "undefined") {
    return Promise.reject(new Error("安全验证只能在浏览器中加载"));
  }
  if (window.turnstile) {
    return Promise.resolve();
  }
  if (turnstileLoadPromise) {
    return turnstileLoadPromise;
  }

  turnstileLoadPromise = new Promise<void>((resolve, reject) => {
    const existingScript = document.querySelector<HTMLScriptElement>(
      'script[src*="challenges.cloudflare.com/turnstile"]',
    );
    if (existingScript) {
      window.onPawTurnstileLoad = () => resolve();
      existingScript.addEventListener("error", () => reject(new Error("安全验证加载失败")), {
        once: true,
      });
      return;
    }

    const script = document.createElement("script");
    script.src =
      "https://challenges.cloudflare.com/turnstile/v0/api.js?onload=onPawTurnstileLoad";
    script.async = true;
    script.defer = true;
    window.onPawTurnstileLoad = () => resolve();
    script.onerror = () => reject(new Error("安全验证加载失败"));
    document.head.appendChild(script);
  });

  return turnstileLoadPromise;
}

interface PawTurnstileProps {
  siteKey: string;
  onToken: (token: string) => void;
  onExpired: () => void;
  onError: () => void;
}

export function PawTurnstile({
  siteKey,
  onToken,
  onExpired,
  onError,
}: PawTurnstileProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const widgetIdRef = useRef<string | null>(null);

  useEffect(() => {
    let disposed = false;

    void loadTurnstile()
      .then(() => {
        if (disposed || !window.turnstile || !containerRef.current || !siteKey) {
          return;
        }

        widgetIdRef.current = window.turnstile.render(containerRef.current, {
          sitekey: siteKey,
          callback: onToken,
          "expired-callback": onExpired,
          "error-callback": onError,
          theme: "auto",
          size: "flexible",
        });
      })
      .catch(() => {
        if (!disposed) {
          onError();
        }
      });

    return () => {
      disposed = true;
      if (window.turnstile && widgetIdRef.current) {
        window.turnstile.remove(widgetIdRef.current);
      }
      widgetIdRef.current = null;
    };
  }, [onError, onExpired, onToken, siteKey]);

  return <div className="paw-turnstile" ref={containerRef} />;
}
