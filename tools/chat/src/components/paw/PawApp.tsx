"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent,
} from "react";

import { useAgentSession } from "../../client/agent/useAgentSession";
import { getPawServiceBaseUrl } from "../../client/paw/config";
import { PawAuthPage } from "./PawAuthPage";
import { PawChatPane } from "./PawChatPane";
import { PawSidebar } from "./PawSidebar";
import { PawSettingsModal, PawShortcutsModal } from "./PawSettingsModal";
import { PawModal } from "./PawModal";
import { PawCheckIcon, PawCloseIcon } from "./PawIcons";
import { PawExportModal } from "./PawExportModal";
import { PawPromptModal } from "./PawPromptModal";
import { PawPaymentModal } from "./PawPaymentModal";
import { PawProfileModal } from "./PawProfileModal";
import { usePawClient } from "./usePawClient";

const SIDEBAR_WIDTH_KEY = "paw-sidebar-width:v1";
const SIDEBAR_VIEWPORT_WIDTH_KEY = "paw-sidebar-viewport-width:v1";
const THEME_KEY = "paw-theme:v1";
const MIN_SIDEBAR_WIDTH = 260;
const DEFAULT_SIDEBAR_WIDTH = 316;
const MAX_SIDEBAR_WIDTH = 440;
type PawTheme = "auto" | "light" | "dark";

function clampSidebarWidth(width: number): number {
  return Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, width));
}

function getResponsiveSidebarWidth(viewportWidth: number): number {
  return clampSidebarWidth(Math.round(viewportWidth * 0.24));
}

export function PawApp() {
  const paw = usePawClient();
  const [sidebarWidth, setSidebarWidth] = useState(DEFAULT_SIDEBAR_WIDTH);
  const sidebarViewportWidthRef = useRef<number | null>(null);
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false);
  const [theme, setTheme] = useState<PawTheme>("auto");
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [promptsOpen, setPromptsOpen] = useState(false);
  const [paymentOpen, setPaymentOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const [isFullscreen, setIsFullscreen] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const [confirmState, setConfirmState] = useState<{
    title: string;
    message: string;
    onConfirm: () => void;
  } | null>(null);

  // agent 不是一个切换进去的"模式"——是给当前这个对话挂一个工作目录。
  // 挂上之后发送就走 codex，界面还是同一个 PawChatPane，只是消息列表里
  // 多了工具调用产生的正文。desktop-only（`agent.desktop` 自己在 effect 里判断）。
  const agent = useAgentSession({
    activeConversationId: paw.activeConversationId,
    activeConversation: paw.activeConversation,
    ensureActiveConversationId: paw.ensureActiveConversationId,
    groupId: paw.selectedGroupId,
    modelId: paw.selectedModelId,
    reasoning: paw.selectedReasoning,
    relayBaseUrl: getPawServiceBaseUrl(),
    sessionToken: paw.session?.accessToken ?? null,
    setAgentBinding: paw.setAgentBinding,
    lockAgentCwd: paw.lockAgentCwd,
    setAgentApprovalMode: paw.setAgentApprovalMode,
    beginTurn: paw.beginAgentTurn,
    appendDelta: paw.appendAgentDelta,
    updateAgentPanel: paw.updateAgentPanel,
    finishTurn: paw.finishAgentTurn,
    appendNotice: paw.appendAgentNotice,
  });

  useEffect(() => {
    try {
      const stored = window.localStorage.getItem(SIDEBAR_WIDTH_KEY);
      const storedViewport = Number(
        window.localStorage.getItem(SIDEBAR_VIEWPORT_WIDTH_KEY),
      );
      const parsed = stored ? Number(stored) : DEFAULT_SIDEBAR_WIDTH;
      const viewportWidth = window.innerWidth;
      sidebarViewportWidthRef.current = viewportWidth;

      if (Number.isFinite(parsed) && Number.isFinite(storedViewport) && storedViewport > 0) {
        setSidebarWidth(
          clampSidebarWidth((parsed * viewportWidth) / storedViewport),
        );
      } else if (stored) {
        // Older versions did not record the viewport size, so use the
        // responsive default instead of keeping a stale fixed width.
        setSidebarWidth(getResponsiveSidebarWidth(viewportWidth));
      } else {
        setSidebarWidth(getResponsiveSidebarWidth(viewportWidth));
      }
    } catch {
      sidebarViewportWidthRef.current = window.innerWidth;
      setSidebarWidth(getResponsiveSidebarWidth(window.innerWidth));
    }
  }, []);

  useEffect(() => {
    const handleViewportResize = () => {
      const viewportWidth = window.innerWidth;
      const previousViewportWidth = sidebarViewportWidthRef.current;
      sidebarViewportWidthRef.current = viewportWidth;

      if (
        previousViewportWidth === null ||
        previousViewportWidth <= 980 ||
        viewportWidth <= 980
      ) {
        return;
      }

      setSidebarWidth((currentWidth) =>
        clampSidebarWidth(
          (currentWidth * viewportWidth) / previousViewportWidth,
        ),
      );
    };

    window.addEventListener("resize", handleViewportResize);
    return () => window.removeEventListener("resize", handleViewportResize);
  }, []);

  useEffect(() => {
    try {
      const stored = window.localStorage.getItem(THEME_KEY) as PawTheme | null;
      if (stored === "auto" || stored === "light" || stored === "dark") {
        setTheme(stored);
      }
    } catch {
      setTheme("auto");
    }
  }, []);

  useEffect(() => {
    document.documentElement.classList.toggle("paw-light", theme === "light");
    document.documentElement.classList.toggle("paw-dark", theme === "dark");
    try {
      window.localStorage.setItem(THEME_KEY, theme);
    } catch {
      // localStorage is optional in embedded shells.
    }
  }, [theme]);

  useEffect(() => {
    const handleFullscreenChange = () => {
      setIsFullscreen(Boolean(document.fullscreenElement));
    };
    document.addEventListener("fullscreenchange", handleFullscreenChange);
    return () => document.removeEventListener("fullscreenchange", handleFullscreenChange);
  }, []);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const modifier = event.ctrlKey || event.metaKey;
      if (modifier && event.shiftKey && event.key.toLowerCase() === "o") {
        event.preventDefault();
        paw.addConversation();
      } else if (event.shiftKey && event.key === "Escape") {
        event.preventDefault();
        document.querySelector<HTMLTextAreaElement>(".paw-composer-box textarea")?.focus();
      } else if (modifier && event.shiftKey && event.key.toLowerCase() === "c") {
        const lastAssistant = paw.activeConversation?.messages
          .slice()
          .reverse()
          .find((message) => message.role === "assistant" && message.content.trim());
        if (lastAssistant) {
          event.preventDefault();
          paw.copyMessage(lastAssistant.id);
        }
      } else if (
        (event.altKey || modifier) &&
        !event.shiftKey &&
        (event.key === "ArrowUp" || event.key === "ArrowDown")
      ) {
        const currentIndex = paw.conversations.findIndex(
          (conversation) => conversation.id === paw.activeConversationId,
        );
        if (currentIndex >= 0 && paw.conversations.length > 1) {
          event.preventDefault();
          const direction = event.key === "ArrowUp" ? -1 : 1;
          const nextIndex =
            (currentIndex + direction + paw.conversations.length) %
            paw.conversations.length;
          const nextConversation = paw.conversations[nextIndex];
          if (nextConversation) paw.selectConversation(nextConversation.id);
        }
      } else if (
        modifier &&
        event.shiftKey &&
        event.code === "Semicolon"
      ) {
        const copyButtons = document.querySelectorAll<HTMLButtonElement>(
          ".paw-markdown-code-copy",
        );
        const lastCopyButton = copyButtons[copyButtons.length - 1];
        if (lastCopyButton) {
          event.preventDefault();
          lastCopyButton.click();
        }
      } else if (modifier && event.key === "/") {
        event.preventDefault();
        setShortcutsOpen(true);
      } else if (modifier && event.shiftKey && event.key === "Backspace") {
        event.preventDefault();
        paw.clearConversationMessages();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [paw]);

  useEffect(() => {
    try {
      window.localStorage.setItem(SIDEBAR_WIDTH_KEY, String(sidebarWidth));
      window.localStorage.setItem(
        SIDEBAR_VIEWPORT_WIDTH_KEY,
        String(window.innerWidth),
      );
    } catch {
      // localStorage is optional in embedded shells.
    }
  }, [sidebarWidth]);

  const handleSidebarDragStart = useCallback(
    (event: PointerEvent<HTMLDivElement>) => {
      if (window.matchMedia("(max-width: 980px)").matches) return;
      event.preventDefault();
      const startX = event.clientX;
      const startWidth = sidebarWidth;

      const handleMove = (moveEvent: globalThis.PointerEvent) => {
        setSidebarWidth(clampSidebarWidth(startWidth + moveEvent.clientX - startX));
      };

      const handleEnd = () => {
        window.removeEventListener("pointermove", handleMove);
        window.removeEventListener("pointerup", handleEnd);
      };

      window.addEventListener("pointermove", handleMove);
      window.addEventListener("pointerup", handleEnd);
    },
    [sidebarWidth],
  );

  if (!paw.hydrated) {
    return (
      <div className="paw-auth">
        <section className="paw-auth-card">
          <div className="paw-auth-kicker">sub2api</div>
          <h1 className="paw-auth-title">共飞AI工作台</h1>
          <p className="paw-auth-copy">正在准备本地工作区...</p>
        </section>
      </div>
    );
  }

  if (!paw.session) {
    return (
      <PawAuthPage
        mode={paw.authMode}
        settings={paw.authSettings}
        settingsBusy={paw.authSettingsBusy}
        email={paw.loginEmail}
        password={paw.loginPassword}
        registerPassword={paw.registerPassword}
        registerConfirmPassword={paw.registerConfirmPassword}
        verifyCode={paw.registerVerifyCode}
        invitationCode={paw.registerInvitationCode}
        promoCode={paw.registerPromoCode}
        busy={paw.loginBusy}
        verifyBusy={paw.verifyCodeBusy}
        verifyCountdown={paw.verifyCodeCountdown}
        captchaResetKey={paw.captchaResetKey}
        error={paw.loginError}
        onEmailChange={paw.setLoginEmail}
        onPasswordChange={paw.setLoginPassword}
        onRegisterPasswordChange={paw.setRegisterPassword}
        onRegisterConfirmPasswordChange={paw.setRegisterConfirmPassword}
        onVerifyCodeChange={paw.setRegisterVerifyCode}
        onInvitationCodeChange={paw.setRegisterInvitationCode}
        onPromoCodeChange={paw.setRegisterPromoCode}
        onCaptchaTokenChange={paw.handleCaptchaTokenChange}
        onCaptchaError={paw.handleCaptchaError}
        onModeChange={paw.handleAuthModeChange}
        onSendVerifyCode={paw.handleSendVerifyCode}
        onLoginSubmit={paw.handleLogin}
        onRegisterSubmit={paw.handleRegister}
      />
    );
  }

  function exportConversation() {
    const conversation = paw.activeConversation;
    if (!conversation) return;
    setExportOpen(true);
  }

  function cycleTheme() {
    setTheme((current) => (current === "auto" ? "light" : current === "light" ? "dark" : "auto"));
  }

  function toggleFullscreen() {
    if (document.fullscreenElement) {
      void document.exitFullscreen();
      return;
    }
    if (document.documentElement.requestFullscreen) {
      void document.documentElement.requestFullscreen();
    } else {
      setIsFullscreen((current) => !current);
    }
  }

  function requestConfirm(
    title: string,
    message: string,
    onConfirm: () => void,
  ) {
    setConfirmState({ title, message, onConfirm });
  }

  return (
    <div
      className={`paw-shell ${mobileSidebarOpen ? "paw-sidebar-open" : ""} ${
        isFullscreen ? "paw-fullscreen" : ""
      }`}
      style={{ "--sidebar-width": `${sidebarWidth}px` } as CSSProperties}
    >
      <button
        className="paw-sidebar-backdrop"
        type="button"
        aria-label="关闭侧栏"
        onClick={() => setMobileSidebarOpen(false)}
      />

      <PawSidebar
        session={paw.session}
        config={paw.config}
        conversations={paw.conversations}
        activeConversationId={paw.activeConversationId}
        onNewConversation={paw.addConversation}
        onDeleteConversation={(id) =>
          requestConfirm(
            "删除对话",
            "确定要删除当前对话吗？删除后无法恢复。",
            () => {
              // 这个对话名下如果还挂着活 thread，先归档掉——否则它变成孤儿，
              // 还占着引擎里的一个 thread，但已经没有任何界面引用它了。
              if (id) void agent.discardConversation(id);
              paw.deleteConversation(id);
              paw.setNotice("对话已删除。");
            },
          )
        }
        onRenameConversation={paw.renameConversation}
        onOpenPrompts={() => setPromptsOpen(true)}
        onOpenSettings={() => setSettingsOpen(true)}
        onOpenPayment={() => setPaymentOpen(true)}
        onOpenProfile={() => {
          setMobileSidebarOpen(false);
          setProfileOpen(true);
        }}
        onSelectConversation={paw.selectConversation}
        onReorderConversations={paw.reorderConversations}
        onCloseMobile={() => setMobileSidebarOpen(false)}
        onDragStart={handleSidebarDragStart}
      />

      {profileOpen ? (
        <PawProfileModal
          config={paw.config}
          session={paw.session}
          currentGroup={paw.currentGroup}
          fullPage
          onOpenPayment={() => {
            setProfileOpen(false);
            setPaymentOpen(true);
          }}
          onLogout={() => {
            setProfileOpen(false);
            paw.handleLogout();
          }}
          onClose={() => setProfileOpen(false)}
        />
      ) : (
      <PawChatPane
        agentDesktop={agent.desktop}
        agentArmed={agent.armed}
        agentCwd={agent.cwd}
        agentCwdLocked={agent.cwdLocked}
        agentRunningTool={agent.runningTool}
        agentRetrying={agent.retrying}
        agentCompacting={agent.compacting}
        agentApprovalMode={agent.approvalMode}
        agentBusy={agent.busy}
        agentApprovals={agent.approvals}
        agentWaitingOnApproval={agent.waitingOnApproval}
        agentError={agent.error}
        onPickAgentDirectory={() => void agent.pickDirectory()}
        onSetAgentApprovalMode={(mode) => void agent.setApprovalMode(mode)}
        onAnswerAgentApproval={(id, approve) => void agent.answer(id, approve)}
        config={paw.config}
        configBusy={paw.configBusy}
        configError={paw.configError}
        notice={paw.notice}
        selectionInvalid={paw.selectionInvalid}
        fileBusy={paw.fileBusy}
        selectedGroupId={paw.selectedGroupId}
        selectedModelId={paw.selectedModelId}
        selectedReasoning={paw.selectedReasoning}
        submitKey={paw.submitKey}
        prompts={[...paw.prompts, ...paw.builtinPrompts]}
        imageSize={paw.imageSize}
        imageSizes={paw.imageSizes}
        currentGroup={paw.currentGroup}
        currentModel={paw.currentModel}
        activeConversation={paw.activeConversation}
        draft={paw.draft}
        attachments={paw.attachments}
        sending={agent.armed ? agent.sending : paw.sending}
        editingMessageId={paw.editingMessageId}
        canSend={agent.armed ? !agent.busy && !agent.sending : paw.canSend}
        theme={theme}
        isFullscreen={isFullscreen}
        onNoticeChange={paw.setNotice}
        onDraftChange={paw.setDraft}
        onChangeGroup={paw.updateSelection}
        onChangeModel={paw.updateModel}
        onChangeReasoning={paw.updateReasoning}
        onChangeImageSize={paw.setImageSize}
        onRefreshConfig={() => {
          void paw.refreshConfig();
        }}
        onSaveDefaults={() => {
          void paw.handleSaveDefaults();
        }}
        onFileChange={paw.handleFileChange}
        onPasteFiles={paw.handlePasteFiles}
        onSend={() => {
          if (agent.armed) {
            const text = paw.draft.trim();
            if (!text) {
              paw.setNotice("先输入内容再发送。");
              return;
            }
            // 双保险：agent 会话本不该能进入编辑态（见 PawChatPane 里的判断），
            // 但万一某个入口漏判了，发送时兜底清掉，别让"正在编辑这条消息"
            // 那条横幅在 agent.send 完全不知道编辑态存在的情况下永远挂着。
            if (paw.editingMessageId) paw.clearEditState(false);
            paw.setDraft("");
            void agent.send(text);
            return;
          }
          void paw.handleSend();
        }}
        onStop={() => {
          if (agent.armed) {
            void agent.interruptTurn();
            return;
          }
          paw.handleStop();
        }}
        onRemoveAttachment={paw.removeAttachment}
        onOpenSidebar={() => setMobileSidebarOpen(true)}
        onOpenSettings={() => setSettingsOpen(true)}
        onOpenShortcuts={() => setShortcutsOpen(true)}
        onCompact={() => {
          if (agent.armed) {
            void agent.compact();
          } else {
            paw.setNotice("涓婁笅鏂囧帇缂╀粎鍦ㄦ寕杞� agent 鐨勬闈㈢瀵硅瘽涓彲鐢ㄣ€?");
          }
        }}
        onToggleTheme={cycleTheme}
        onToggleFullscreen={toggleFullscreen}
        onNewConversation={paw.addConversation}
        onClearConversation={() =>
          requestConfirm(
            "清除上下文",
            "清除后，之前的消息仍会保留在页面中，但不会继续发送给模型。",
            () => {
              paw.clearConversationMessages();
              paw.setNotice("已清除上下文。");
            },
          )
        }
        onRestoreContext={() => {
          paw.restoreConversationContext();
          paw.setNotice("已恢复全部上下文。");
        }}
        onExportConversation={exportConversation}
        onCopyMessage={paw.copyMessage}
        onTogglePinMessage={paw.togglePinMessage}
        onDeleteMessage={paw.deleteMessage}
        onEditMessage={paw.beginEditMessage}
        onRetryMessage={paw.retryMessage}
        onCancelEdit={() => paw.clearEditState(true)}
        onRenameConversation={paw.renameConversation}
        getSelectionSummary={paw.getSelectionSummary}
      />
      )}
      {settingsOpen ? (
        <PawSettingsModal
          config={paw.config}
          session={paw.session}
          theme={theme}
          submitKey={paw.submitKey}
          promptCount={paw.prompts.length}
          currentSelection={paw.getSelectionSummary(
            paw.config,
            paw.selectedGroupId,
            paw.selectedModelId,
            paw.selectedReasoning,
          )}
          defaultSelection={paw.getSelectionSummary(
            paw.config,
            paw.config?.defaults.group_id ?? null,
            paw.config?.defaults.model_id ?? "",
            paw.config?.defaults.reasoning ?? "",
          )}
          selectionInvalid={paw.selectionInvalid}
          onThemeChange={setTheme}
          onSubmitKeyChange={paw.setSubmitKey}
          onSaveDefaults={() => {
            void paw.handleSaveDefaults();
          }}
          onOpenPrompts={() => {
            setSettingsOpen(false);
            setPromptsOpen(true);
          }}
          onExportLocalData={paw.exportLocalData}
          onImportLocalData={(file) => {
            void paw.importLocalData(file);
          }}
          onResetLocalData={() =>
            requestConfirm(
              "清空本地数据",
              "确定要清空所有本地对话、提示词和 Chat 设置吗？此操作无法恢复。",
              paw.resetLocalData,
            )
          }
          onClose={() => setSettingsOpen(false)}
        />
      ) : null}
      {paymentOpen ? (
        <PawPaymentModal
          balance={paw.config?.user.balance}
          onCompleted={() => {
            void paw.refreshConfig();
            paw.setNotice("充值成功，账户余额已刷新。");
          }}
          onClose={() => setPaymentOpen(false)}
        />
      ) : null}
      {promptsOpen ? (
        <PawPromptModal
          userPrompts={paw.prompts}
          builtinPrompts={paw.builtinPrompts}
          onAdd={paw.addPrompt}
          onUpdate={paw.updatePrompt}
          onDelete={paw.deletePrompt}
          onNotice={paw.setNotice}
          onClose={() => setPromptsOpen(false)}
        />
      ) : null}
      {exportOpen && paw.activeConversation ? (
        <PawExportModal
          conversation={paw.activeConversation}
          onClose={() => setExportOpen(false)}
          onNotice={paw.setNotice}
        />
      ) : null}
      {shortcutsOpen ? <PawShortcutsModal onClose={() => setShortcutsOpen(false)} /> : null}
      {confirmState ? (
        <PawModal
          title={confirmState.title}
          onClose={() => setConfirmState(null)}
          actions={
            <>
              <button
                type="button"
                className="paw-button"
                onClick={() => setConfirmState(null)}
              >
                <PawCloseIcon width={15} height={15} />
                取消
              </button>
              <button
                type="button"
                className="paw-button primary"
                onClick={() => {
                  const action = confirmState.onConfirm;
                  setConfirmState(null);
                  action();
                }}
              >
                <PawCheckIcon width={15} height={15} />
                确认
              </button>
            </>
          }
        >
          <p className="paw-confirm-message">{confirmState.message}</p>
        </PawModal>
      ) : null}
    </div>
  );
}
