import React, { useState, useEffect, Suspense, lazy, useRef } from "react";
import { AnimatePresence } from "motion/react";
import TopToolbar from "./components/TopToolbar";
import Sidebar from "./components/Sidebar";
import HistorySpace from "./components/HistorySpace";
import GenerationWorkbench, { type WorkbenchAssistantHandle } from "./components/GenerationWorkbench";
import NovelVisualizationSpace from "./components/NovelVisualizationSpace";
import ReleaseNotesModal, { type ReleaseInfo } from "./components/ReleaseNotesModal";
import SettingsModal, { type SettingsTabId } from "./components/SettingsModal";
import AuthModal from "./components/AuthModal";
import { fetchAccountSummary, fetchChatModels, type AccountSummaryResponse, type ChatModel, type UserInfo } from "./services/apiService";
import { ToastContainer, ToastType } from "./components/Toast";
import { translations } from "./i18n";
import { useLanguage } from "@shared/i18n";
import { appConfig } from "@shared/config/app-mode";
import { setOnAuthRequired } from "@shared/services/client";
import { Skeleton } from "@shared/ui";
import type { AssistantScene, AssistantWorkbenchState } from "@shared/types/assistant";

const ChatWidget = lazy(() => import("./components/ChatWidget"));
const ComfyWorkbench = lazy(() => import("./components/ComfyWorkbench"));
const TtsWorkbench = lazy(() => import("./components/TtsWorkbench"));

const RELEASE_DISMISS_PREFIX = "mirror.release.dismissed:";
const WORKSPACE_TABS = new Set(["create", "comfy", "tts", "novelviz", "history"]);

function readReleaseInfo(): ReleaseInfo | null {
  const el = document.getElementById("workspace-release-data");
  if (!el?.textContent) return null;
  try {
    const parsed = JSON.parse(el.textContent) as {
      version?: string;
      title?: string;
      notes?: string;
      published_at?: string;
    };
    const version = parsed.version?.trim() || "";
    if (!version) return null;
    return {
      version,
      title: parsed.title?.trim() || `${version} 更新`,
      notes: parsed.notes || "",
      publishedAt: parsed.published_at || "",
    };
  } catch {
    return null;
  }
}

export default function App() {
  const [activeTab, setActiveTabState] = useState("create");
  const [isCollapsed, setIsCollapsed] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [settingsInitialTab, setSettingsInitialTab] = useState<SettingsTabId>("profile");
  const [showAuth, setShowAuth] = useState(false);
  const [authMode, setAuthMode] = useState<"login" | "register">("login");
  const [selectedModel, setSelectedModel] = useState("");
  const [chatModels, setChatModels] = useState<ChatModel[]>([]);
  const [toasts, setToasts] = useState<{ id: string, message: string, type: ToastType }[]>([]);
  const [userInfo, setUserInfo] = useState<UserInfo | null>(null);
  const [selectedTaskNo, setSelectedTaskNo] = useState("");
  const [selectedComfyTaskNo, setSelectedComfyTaskNo] = useState("");
  const [selectedNovelTaskId, setSelectedNovelTaskId] = useState<number | null>(null);
  const [workbenchResetSignal, setWorkbenchResetSignal] = useState(0);
  const [novelvizResetSignal, setNovelvizResetSignal] = useState(0);
  const [releaseInfo] = useState<ReleaseInfo | null>(() => readReleaseInfo());
  const [showReleaseNotes, setShowReleaseNotes] = useState(false);
  // 悬浮窗对话「填回描述」的投递：只有当前在生图板块时才接得住。
  const [fillRequest, setFillRequest] = useState<{ id: number; text: string } | null>(null);
  // 工作台 ref：悬浮窗助手读「当前板块状态」、写「结构化回填」都走这里（create/comfy/tts 各持一个）。
  const generationRef = useRef<WorkbenchAssistantHandle>(null);
  const comfyRef = useRef<WorkbenchAssistantHandle>(null);
  const ttsRef = useRef<WorkbenchAssistantHandle>(null);

  // Read initial user data from DOM (injected by Django template)
  const rootEl = document.getElementById("workspace-root");
  const initialCaptcha = rootEl?.dataset.captcha || "";
  const initialCaptchaSvg = rootEl?.dataset.captchaSvg || "";
  const initialIsAuthenticated = rootEl?.dataset.authenticated === "1";
  const initialNextPath = new URLSearchParams(window.location.search).get("next") || "";
  // 「Comfy 生图」「TTS 语音合成」板块只在本地开发者模式（npm run local 的入口文件带 data-mode="local"）
  // 出现；线上 Go 模板没有这个标记，所以线上既看不到入口也不会发这些请求。
  const comfyEnabled = appConfig.features.comfy;
  const ttsEnabled = appConfig.features.tts;

  // 壳层语言跟随共享语言上下文（复核 P1-3）：与侧栏/顶栏同一开关同一持久化，
  // 词条暂仍取本地字典（工作台组件透传 t.history / t.settings，台账「明确不做」）。
  const { language } = useLanguage();
  const t = translations[language];

  const [isAuthenticated, setIsAuthenticated] = useState(initialIsAuthenticated);
  const [postAuthRedirect, setPostAuthRedirect] = useState(initialNextPath);
  const [accountSummary, setAccountSummary] = useState<AccountSummaryResponse | null>(null);
  const [selectedTtsTaskNo, setSelectedTtsTaskNo] = useState("");

  /**
   * URL 写入语义（回归复核 P1-1/P1-2 修复）：
   * - 用户交互（push=true）：pushState，产生可前进/后退的历史条目；
   * - 状态恢复（popstate / 首屏 URL 恢复，push=false）：replaceState 静默对齐，
   *   不制造重复历史条目，也不在恢复路径里再次入栈（后退陷阱）。
   */
  const applyTabUrl = (nextTab: string, taskNo: string | null, taskKind: string | null, push: boolean) => {
    const url = new URL(window.location.href);
    url.searchParams.set("tab", nextTab);
    if (taskNo && taskKind) {
      url.searchParams.set("task", taskNo);
      url.searchParams.set("task_kind", taskKind);
    } else {
      url.searchParams.delete("task");
      url.searchParams.delete("task_kind");
    }
    if (push) {
      window.history.pushState({}, "", url.toString());
    } else {
      window.history.replaceState({}, "", url.toString());
    }
  };

  const setActiveTab = (tab: string, options: { push?: boolean } = {}) => {
    if (tab === "comfy" && !comfyEnabled) return;
    if (tab === "tts" && !ttsEnabled) return;
    const nextTab = WORKSPACE_TABS.has(tab) ? tab : "create";
    setActiveTabState(nextTab);
    const push = options.push !== false;
    applyTabUrl(nextTab, null, null, push);
  };

  // 浏览器前进/后退：只从 URL 恢复状态（replaceState 语义，绝不再入栈）
  useEffect(() => {
    const handlePopState = () => {
      const params = new URLSearchParams(window.location.search);
      const raw = params.get("tab") || "create";
      const legacyTab = WORKSPACE_TABS.has(raw) ? raw : "create";
      setActiveTabState(legacyTab);
      const task = params.get("task") || "";
      const kind = params.get("task_kind") || "";
      if (task) {
        restoreHistoryTask(task, kind);
      } else {
        setSelectedTaskNo("");
        setSelectedComfyTaskNo("");
        setSelectedTtsTaskNo("");
      }
      syncDocumentTitle(legacyTab);
    };
    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 首屏从 URL 恢复（刷新 / 分享链接场景）：只 setState + replaceState 对齐
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const raw = params.get("tab") || "create";
    const initialTab = WORKSPACE_TABS.has(raw) ? raw : "create";
    const task = params.get("task") || "";
    const kind = params.get("task_kind") || "";
    if (task) {
      restoreHistoryTask(task, kind);
    } else if (initialTab !== "create") {
      setActiveTabState(initialTab);
    }
    applyTabUrl(initialTab, task || null, task ? kind || "image" : null, false);
    syncDocumentTitle(task || initialTab);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openNovelVisualization = () => {
    setSelectedNovelTaskId(null);
    setActiveTab("novelviz");
    setNovelvizResetSignal((value) => value + 1);
  };

  const syncDocumentTitle = (tabOrTask: string) => {
    const isTask = Boolean(tabOrTask) && !WORKSPACE_TABS.has(tabOrTask) && !tabOrTask.includes(":");
    document.title = `绘镜 · ${
      isTask ? t.header.history : tabOrTask === "history" ? t.header.history : tabOrTask === "novelviz" ? t.header.novelviz : tabOrTask === "tts" ? t.header.tts : tabOrTask === "comfy" ? t.header.comfy : t.header.creationCore
    }`;
  };

  /** 从 URL 的结构化参数恢复任务选中态：只写 state，不动 history（恢复路径专用） */
  const restoreHistoryTask = (taskNo: string, kind: string) => {
    if (kind === "comfy" && comfyEnabled) {
      setSelectedComfyTaskNo(taskNo);
      setSelectedTaskNo("");
      setSelectedTtsTaskNo("");
      return;
    }
    if (kind === "tts" && ttsEnabled) {
      setSelectedTtsTaskNo(taskNo);
      setSelectedTaskNo("");
      setSelectedComfyTaskNo("");
      return;
    }
    setSelectedComfyTaskNo("");
    setSelectedTtsTaskNo("");
    setSelectedTaskNo(taskNo);
  };

  const openHistoryTask = (taskNo: string) => {
    if (taskNo.startsWith("novelviz:")) {
      const taskId = Number(taskNo.replace("novelviz:", ""));
      if (Number.isFinite(taskId) && taskId > 0) {
        setSelectedNovelTaskId(taskId);
        setActiveTab("novelviz");
        return;
      }
    }
    if (taskNo.startsWith("comfy:")) {
      const comfyTaskNo = taskNo.slice("comfy:".length);
      if (comfyTaskNo && comfyEnabled) {
        setSelectedComfyTaskNo(comfyTaskNo);
        setSelectedTaskNo("");
        setActiveTab("comfy");
        applyTabUrl("comfy", comfyTaskNo, "comfy", true);
        return;
      }
    }
    if (taskNo.startsWith("tts:")) {
      const ttsTaskNo = taskNo.slice("tts:".length);
      if (ttsTaskNo && ttsEnabled) {
        setSelectedTtsTaskNo(ttsTaskNo);
        setSelectedTaskNo("");
        setActiveTab("tts");
        applyTabUrl("tts", ttsTaskNo, "tts", true);
        return;
      }
    }
    setSelectedComfyTaskNo("");
    setSelectedTtsTaskNo("");
    setSelectedTaskNo(taskNo);
    setActiveTab("create", { push: false });
    applyTabUrl("create", taskNo, "image", true);
  };

  const applyAccountSummary = (summary: AccountSummaryResponse) => {
    setAccountSummary(summary);
    setUserInfo({
      username: summary.user.username,
      nickname: summary.user.nickname,
      display_name: summary.user.nickname || summary.user.username,
      email: summary.user.email,
      avatar: summary.user.avatar,
      remark: summary.user.remark,
      role: "用户",
      daily_quota: summary.quota.daily_quota,
      quota_remaining: summary.quota.available_quota,
      quota_frozen: summary.quota.frozen_quota,
      quota_total: Math.max(summary.quota.daily_quota, summary.quota.available_quota + summary.quota.frozen_quota),
    });
  };

  // Fetch user info and chat model list from backend on mount.
  useEffect(() => {
    if (isAuthenticated) {
      fetchAccountSummary()
        .then(applyAccountSummary)
        .catch(() => {
          setIsAuthenticated(false);
          setAccountSummary(null);
          setUserInfo(null);
        });
    } else {
      setAccountSummary(null);
      setUserInfo(null);
    }
  }, [isAuthenticated]);

  useEffect(() => {
    if (!isAuthenticated) {
      setChatModels([]);
      setSelectedModel("");
      return;
    }
    fetchChatModels()
      .then((models) => {
        setChatModels(models);
        setSelectedModel((current) => current || models[0]?.channel_code || "");
      })
      .catch(() => {
        setChatModels([]);
        setSelectedModel("");
      });
  }, [isAuthenticated]);

  const addToast = (message: string, type: ToastType = "success") => {
    const id = Date.now().toString();
    setToasts(prev => [...prev, { id, message, type }]);
  };

  const removeToast = (id: string) => {
    setToasts(prev => prev.filter(t => t.id !== id));
  };

  const openAuth = (mode: "login" | "register" = "login") => {
    setAuthMode(mode);
    setShowAuth(true);
  };

  // 全局 401 兜底：任何走 shared client 的请求遇到会话失效，统一弹出登录框（台账 #8）。
  // 组件层已各自处理 AuthRequiredError 的场景不会重复触发（弹窗已开时 openAuth 幂等）。
  useEffect(() => {
    setOnAuthRequired(() => {
      if (appConfig.isOnline) {
        openAuth("login");
      }
    });
    return () => setOnAuthRequired(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openSettings = (tab: SettingsTabId = "profile") => {
    setSettingsInitialTab(tab);
    setShowSettings(true);
  };

  // 悬浮窗助手：按当前板块解析要联动的工作台（生图两板块共用 image 场景，TTS 单独）。
  const activeAssistantRef = () => {
    if (activeTab === "comfy") return comfyRef.current;
    if (activeTab === "tts") return ttsRef.current;
    return generationRef.current;
  };

  // 悬浮窗对话把提示词填进「创作描述」：不在生图板块时返回 false（悬浮窗改成复制到剪贴板）。
  const handleChatFillPrompt = (text: string) => {
    if (activeTab !== "create" && activeTab !== "comfy") return false;
    setFillRequest({ id: Date.now(), text });
    return true;
  };

  // 助手上下文：当前板块 + 输入快照（随每轮对话发给服务端拼 system prompt）。
  const handleAssistantContext = (): { scene: AssistantScene; mode?: string; snapshot: string } | null => {
    const handle = activeAssistantRef();
    if (!handle) return null;
    try {
      return handle.getAssistantState();
    } catch {
      return null;
    }
  };

  // 助手回复末尾的 <<<FILL>>> JSON：交给当前板块的工作台按字段回填。
  const handleAssistantFill = (fill: import("@shared/types/assistant").AssistantFillPayload) => {
    activeAssistantRef()?.applyAssistantFill(fill);
  };

  // 动态「已入框」：当前板块工作台实时比对表单与计划（用户改过表单 → 按钮复归可点）。
  const handleMatchesAssistantFill = (fill: import("@shared/types/assistant").AssistantFillPayload) => {
    try {
      return activeAssistantRef()?.matchesAssistantFill(fill) ?? false;
    } catch {
      return false;
    }
  };

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const requestedMode = params.get("auth");
    const requestedNext = params.get("next") || "";
    if (requestedNext) {
      setPostAuthRedirect(requestedNext);
    }
    if (!isAuthenticated && (requestedMode === "login" || requestedMode === "register")) {
      openAuth(requestedMode);
    }
    if (requestedMode || requestedNext) {
      window.history.replaceState({}, "", window.location.pathname);
    }
  }, []);

  useEffect(() => {
    if (!releaseInfo?.version) return;
    const dismissed = window.localStorage.getItem(`${RELEASE_DISMISS_PREFIX}${releaseInfo.version}`) === "1";
    setShowReleaseNotes(!dismissed);
  }, [releaseInfo]);

  const handleCloseReleaseNotes = (dismissForCurrentVersion: boolean) => {
    if (dismissForCurrentVersion && releaseInfo?.version) {
      window.localStorage.setItem(`${RELEASE_DISMISS_PREFIX}${releaseInfo.version}`, "1");
    }
    setShowReleaseNotes(false);
  };

  const requireAuth = (next: () => void, mode: "login" | "register" = "login") => {
    if (!isAuthenticated) {
      openAuth(mode);
      return;
    }
    next();
  };

  const handleAuthenticated = async (fallback: UserInfo) => {
    setIsAuthenticated(true);
    setShowAuth(false);
    try {
      const summary = await fetchAccountSummary();
      applyAccountSummary(summary);
    } catch {
      setAccountSummary(null);
      setUserInfo(fallback);
    }
    if (postAuthRedirect.startsWith("/") && !postAuthRedirect.startsWith("//")) {
      window.location.href = postAuthRedirect;
    }
  };

  return (
    <div className="relative flex min-h-screen overflow-x-hidden bg-brand-bg text-brand-primary transition-colors duration-300">
      <Sidebar
        activeTab={activeTab}
        setActiveTab={setActiveTab}
        isCollapsed={isCollapsed}
        setIsCollapsed={setIsCollapsed}
        onOpenNovelVisualization={openNovelVisualization}
        releaseVersion={releaseInfo?.version}
        showComfy={comfyEnabled}
        showTts={ttsEnabled}
      />
      <main className="relative z-0 flex flex-1 flex-col pb-24 md:pb-0">
        <header className="z-40 flex flex-col gap-3 border-b border-[var(--color-border-subtle)] bg-[var(--color-surface)]/85 px-4 py-3 backdrop-blur-md transition-all sm:px-6 md:h-16 md:flex-row md:items-center md:justify-between md:py-0">
          <div className="flex min-w-0 items-center gap-2">
            <h2 className="text-sm font-medium text-[var(--color-muted)] uppercase tracking-widest italic leading-none">
              {activeTab === 'create' ? t.header.creationCore : activeTab === 'comfy' ? t.header.comfy : activeTab === 'tts' ? t.header.tts : activeTab === 'history' ? t.header.history : activeTab === 'novelviz' ? t.header.novelviz : t.header.brand}
            </h2>
            <div className="w-1.5 h-1.5 rounded-full bg-[var(--color-success)] animate-pulse" />
          </div>
          
          <TopToolbar 
            onOpenSettings={() => requireAuth(() => openSettings("profile"), "login")} 
            onOpenSettingsTab={(tab) => requireAuth(() => openSettings(tab), "login")}
            selectedModel={selectedModel}
            onModelChange={setSelectedModel}
            chatModels={chatModels}
            addToast={addToast}
            userInfo={userInfo}
            accountSummary={accountSummary}
            releaseInfo={releaseInfo}
            isAuthenticated={isAuthenticated}
            onOpenAuth={openAuth}
            onOpenHistory={() => setActiveTab("history")}
            onOpenTask={openHistoryTask}
          />
        </header>

        <div
          className="flex-1 overflow-x-hidden overflow-y-auto scroll-smooth transition-colors duration-300"
        >
          <AnimatePresence initial={false} mode="wait">
            {activeTab === 'history' ? (
              <HistorySpace
                key="history"
                addToast={addToast}
                t={t.history}
                onOpenTask={openHistoryTask}
              />
            ) : activeTab === 'comfy' && appConfig.features.comfy ? (
              <Suspense fallback={<div className="p-8"><Skeleton className="h-64 w-full" /></div>}>
                <ComfyWorkbench
                  ref={comfyRef}
                  key="comfy-workbench"
                  isAuthenticated={isAuthenticated}
                  selectedModel={selectedModel}
                  chatModels={chatModels}
                  onOpenAuth={openAuth}
                  addToast={addToast}
                  selectedTaskNo={selectedComfyTaskNo}
                  resetSignal={workbenchResetSignal}
                  fillRequest={fillRequest}
                  onFillRequestConsumed={() => setFillRequest(null)}
                  onGenerationSubmitted={() => fetchAccountSummary().then(applyAccountSummary).catch(() => {})}
                />
              </Suspense>
            ) : activeTab === 'tts' && appConfig.features.tts ? (
              <Suspense fallback={<div className="p-8"><Skeleton className="h-64 w-full" /></div>}>
                <TtsWorkbench
                  ref={ttsRef}
                  key="tts-workbench"
                  isAuthenticated={isAuthenticated}
                  selectedModel={selectedModel}
                  chatModels={chatModels}
                  onOpenAuth={openAuth}
                  addToast={addToast}
                  selectedTaskNo={selectedTtsTaskNo}
                  resetSignal={workbenchResetSignal}
                  onGenerationSubmitted={() => fetchAccountSummary().then(applyAccountSummary).catch(() => {})}
                />
              </Suspense>
            ) : activeTab === 'novelviz' ? (
              <NovelVisualizationSpace
                key="novelviz"
                isAuthenticated={isAuthenticated}
                selectedModel={selectedModel}
                chatModels={chatModels}
                resetSignal={novelvizResetSignal}
                selectedTaskId={selectedNovelTaskId}
                onOpenAuth={openAuth}
                addToast={addToast}
              />
            ) : (
              <GenerationWorkbench
                ref={generationRef}
                key="generation-workbench"
                isAuthenticated={isAuthenticated}
                selectedModel={selectedModel}
                chatModels={chatModels}
                onOpenAuth={openAuth}
                addToast={addToast}
                selectedTaskNo={selectedTaskNo}
                resetSignal={workbenchResetSignal}
                fillRequest={fillRequest}
                onFillRequestConsumed={() => setFillRequest(null)}
                onGenerationSubmitted={() => fetchAccountSummary().then(applyAccountSummary).catch(() => {})}
              />
            )}
          </AnimatePresence>
        </div>
      </main>

      <AnimatePresence>
        {showSettings && (
          <SettingsModal
            onClose={() => setShowSettings(false)}
            initialTab={settingsInitialTab}
            t={t.settings}
          />
        )}
        {showAuth && (
          <AuthModal
            initialMode={authMode}
            initialCaptcha={initialCaptcha}
            initialCaptchaSvg={initialCaptchaSvg}
            onClose={() => setShowAuth(false)}
            onAuthenticated={handleAuthenticated}
            addToast={addToast}
          />
        )}
        {releaseInfo && (
          <ReleaseNotesModal
            release={releaseInfo}
            open={showReleaseNotes}
            onClose={handleCloseReleaseNotes}
          />
        )}
      </AnimatePresence>

      <ToastContainer toasts={toasts} onClose={removeToast} />

      {/* 悬浮窗对话：只在本地开发者模式（需要本机的对话中转站接口） */}
      {appConfig.features.chatWidget && (
        <Suspense fallback={null}>
          <ChatWidget
            addToast={addToast}
            onFillPrompt={handleChatFillPrompt}
            activeTab={activeTab}
            getAssistantContext={handleAssistantContext}
            onAssistantFill={handleAssistantFill}
            matchesAssistantFill={handleMatchesAssistantFill}
          />
        </Suspense>
      )}
    </div>
  );
}
