"use client";

import { useEffect, useState } from "react";
import { getClientConfig } from "@/config/client";
import { PawCheckIcon, PawDownloadIcon } from "./PawIcons";

type InstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{
    outcome: "accepted" | "dismissed";
    platform: string;
  }>;
};

type InstallPlatform = "android" | "ios" | "windows" | "macos" | "linux" | "other";

function detectPlatform(): InstallPlatform {
  const userAgent = navigator.userAgent.toLowerCase();
  const platform = navigator.platform.toLowerCase();

  if (/iphone|ipad|ipod/.test(userAgent) || (platform === "macintel" && navigator.maxTouchPoints > 1)) {
    return "ios";
  }
  if (/android/.test(userAgent)) return "android";
  if (/windows/.test(userAgent)) return "windows";
  if (/macintosh|mac os x/.test(userAgent)) return "macos";
  if (/linux/.test(userAgent)) return "linux";
  return "other";
}

export function PawInstallPage() {
  const [platform, setPlatform] = useState<InstallPlatform>("other");
  const [deferredPrompt, setDeferredPrompt] = useState<InstallPromptEvent | null>(null);
  const [installed, setInstalled] = useState(false);
  const [installNotice, setInstallNotice] = useState<string | null>(null);
  const [installBusy, setInstallBusy] = useState(false);

  useEffect(() => {
    setPlatform(detectPlatform());

    const standalone =
      window.matchMedia("(display-mode: standalone)").matches ||
      Boolean((navigator as Navigator & { standalone?: boolean }).standalone);
    setInstalled(standalone);

    const handleBeforeInstallPrompt = (event: Event) => {
      event.preventDefault();
      setDeferredPrompt(event as InstallPromptEvent);
    };
    const handleAppInstalled = () => {
      setInstalled(true);
      setDeferredPrompt(null);
      setInstallNotice("已安装到桌面。");
    };

    window.addEventListener("beforeinstallprompt", handleBeforeInstallPrompt);
    window.addEventListener("appinstalled", handleAppInstalled);
    return () => {
      window.removeEventListener("beforeinstallprompt", handleBeforeInstallPrompt);
      window.removeEventListener("appinstalled", handleAppInstalled);
    };
  }, []);

  const mountPath = getClientConfig().mountPath;
  const chatPath = mountPath || "/";
  const isAndroid = platform === "android";
  const isIOS = platform === "ios";

  async function handleInstall() {
    setInstallNotice(null);
    setInstallBusy(true);

    if (isIOS) {
      try {
        if (typeof navigator.share === "function") {
          await navigator.share({
            title: "共飞AI工作台",
            text: "添加共飞AI工作台到主屏幕",
            url: new URL(chatPath, window.location.origin).toString(),
          });
          setInstallNotice("请在系统菜单中选择“添加到主屏幕”，再点击右上角“添加”。");
        } else {
          setInstallNotice("请点击 Safari 的分享按钮，选择“添加到主屏幕”，再点击右上角“添加”。");
        }
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setInstallNotice("请点击 Safari 的分享按钮，选择“添加到主屏幕”，再点击右上角“添加”。");
        }
      } finally {
        setInstallBusy(false);
      }
      return;
    }

    if (!deferredPrompt) {
      setInstallNotice(
        isAndroid
          ? "当前浏览器暂未提供一键安装，请打开浏览器菜单，选择“添加到主屏幕”或“安装应用”。"
          : "请使用浏览器地址栏的安装图标或菜单完成安装。",
      );
      setInstallBusy(false);
      return;
    }

    try {
      await deferredPrompt.prompt();
      const choice = await deferredPrompt.userChoice;
      setDeferredPrompt(null);
      if (choice.outcome === "accepted") {
        setInstalled(true);
        setInstallNotice("正在安装到桌面。");
      }
    } finally {
      setInstallBusy(false);
    }
  }

  return (
    <main className="paw-install-page">
      <section className="paw-install-card">
        <div className="paw-install-brand">
          <div className="paw-install-logo">G</div>
          <div>
            <div className="paw-install-kicker">共飞平台</div>
            <h1>共飞AI工作台</h1>
          </div>
        </div>

        <p className="paw-install-copy">
          将 Chat 安装到设备桌面，之后可以像独立应用一样快速打开。
        </p>

        {installed ? (
          <div className="paw-install-status success">
            <PawCheckIcon width={18} height={18} />
            已安装到桌面
          </div>
        ) : null}

        {isAndroid && !installed ? (
          <div className="paw-install-action">
            <button
              className="paw-button primary"
              type="button"
              onClick={() => void handleInstall()}
              disabled={installBusy}
            >
              <PawDownloadIcon width={17} height={17} />
              {installBusy ? "正在打开安装..." : "安装到桌面"}
            </button>
            <span>适用于 Android 手机的 Chrome 等支持 PWA 的浏览器。</span>
          </div>
        ) : null}

        {isIOS && !installed ? (
          <div className="paw-install-action">
            <button
              className="paw-button primary"
              type="button"
              onClick={() => void handleInstall()}
              disabled={installBusy}
            >
              <PawDownloadIcon width={17} height={17} />
              {installBusy ? "正在打开安装菜单..." : "添加到主屏幕"}
            </button>
            <span>点击后会打开 iOS 系统菜单，请选择“添加到主屏幕”。</span>
          </div>
        ) : null}

        {isIOS && !installed ? (
          <div className="paw-install-instructions compact">
            <h2>只需最后一步</h2>
            <p>在系统菜单中点击“添加到主屏幕”，再点击右上角“添加”即可。</p>
          </div>
        ) : null}

        {!isAndroid && !isIOS && !installed ? (
          <div className="paw-install-instructions">
            <h2>通过浏览器安装</h2>
            <p>
              在地址栏右侧查找安装图标，或打开浏览器菜单，选择“安装共飞AI工作台”。
            </p>
            <span>
              {platform === "windows"
                ? "当前平台：Windows"
                : platform === "macos"
                  ? "当前平台：macOS"
                  : platform === "linux"
                    ? "当前平台：Linux"
                    : "当前平台：桌面浏览器"}
            </span>
          </div>
        ) : null}

        {installNotice ? <div className="paw-install-notice">{installNotice}</div> : null}

        <a className="paw-install-back" href={chatPath}>
          返回 Chat
        </a>
      </section>
    </main>
  );
}
