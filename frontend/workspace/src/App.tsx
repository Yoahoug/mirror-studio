import { useState, useEffect } from "react";
import { AnimatePresence } from "motion/react";
import TopToolbar from "./components/TopToolbar";
import Sidebar from "./components/Sidebar";
import HistorySpace from "./components/HistorySpace";
import GenerationWorkbench from "./components/GenerationWorkbench";
import ComfyWorkbench from "./components/ComfyWorkbench";
import NovelVisualizationSpace from "./components/NovelVisualizationSpace";
import ReleaseNotesModal, { type ReleaseInfo } from "./components/ReleaseNotesModal";
import SettingsModal, { type SettingsTabId } from "./components/SettingsModal";
import AuthModal from "./components/AuthModal";
import { fetchAccountSummary, fetchChatModels, type AccountSummaryResponse, type ChatModel, type UserInfo } from "./services/apiService";
import { ToastContainer, ToastType } from "./components/Toast";
import { Language, translations } from "./i18n";

const RELEASE_DISMISS_PREFIX = "mirror.release.dismissed:";
const WORKSPACE_TABS = new Set(["create", "comfy", "novelviz", "history"]);

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
  const [language, setLanguage] = useState<Language>("zh");
  const [toasts, setToasts] = useState<{ id: string, message: string, type: ToastType }[]>([]);
  const [userInfo, setUserInfo] = useState<UserInfo | null>(null);
  const [selectedTaskNo, setSelectedTaskNo] = useState("");
  const [selectedComfyTaskNo, setSelectedComfyTaskNo] = useState("");
  const [selectedNovelTaskId, setSelectedNovelTaskId] = useState<number | null>(null);
  const [workbenchResetSignal, setWorkbenchResetSignal] = useState(0);
  const [novelvizResetSignal, setNovelvizResetSignal] = useState(0);
  const [releaseInfo] = useState<ReleaseInfo | null>(() => readReleaseInfo());
  const [showReleaseNotes, setShowReleaseNotes] = useState(false);

  // Read initial user data from DOM (injected by Django template)
  const rootEl = document.getElementById("workspace-root");
  const initialCaptcha = rootEl?.dataset.captcha || "";
  const initialCaptchaSvg = rootEl?.dataset.captchaSvg || "";
  const initialIsAuthenticated = rootEl?.dataset.authenticated === "1";
  const initialNextPath = new URLSearchParams(window.location.search).get("next") || "";
  // 「Comfy 生图」板块只在本地开发者模式（npm run local 的入口文件带 data-mode="local"）
  // 出现；线上 Go 模板没有这个标记，所以线上既看不到入口也不会发任何 Comfy 请求。
  const comfyEnabled = rootEl?.dataset.mode === "local";

  const t = translations[language];

  const [isAuthenticated, setIsAuthenticated] = useState(initialIsAuthenticated);
  const [postAuthRedirect, setPostAuthRedirect] = useState(initialNextPath);
  const [accountSummary, setAccountSummary] = useState<AccountSummaryResponse | null>(null);

  const setActiveTab = (tab: string) => {
    if (tab === "comfy" && !comfyEnabled) return;
    const nextTab = WORKSPACE_TABS.has(tab) ? tab : "create";
    setActiveTabState(nextTab);
  };

  const openNovelVisualization = () => {
    setSelectedNovelTaskId(null);
    setActiveTab("novelviz");
    setNovelvizResetSignal((value) => value + 1);
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
        return;
      }
    }
    setSelectedComfyTaskNo("");
    setSelectedTaskNo(taskNo);
    setActiveTab("create");
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

  const openSettings = (tab: SettingsTabId = "profile") => {
    setSettingsInitialTab(tab);
    setShowSettings(true);
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
        onNewConversation={() => {
          setSelectedTaskNo("");
          setSelectedComfyTaskNo("");
          setWorkbenchResetSignal((value) => value + 1);
          setActiveTab("create");
          addToast(t.toasts.newChat);
        }}
        onOpenNovelVisualization={openNovelVisualization}
        releaseVersion={releaseInfo?.version}
        showComfy={comfyEnabled}
        t={t.sidebar}
      />

      <main className="relative z-0 flex flex-1 flex-col pb-24 md:pb-0">
        <header className="z-40 flex flex-col gap-3 border-b border-gray-100 bg-white/85 px-4 py-3 backdrop-blur-md transition-all sm:px-6 md:h-16 md:flex-row md:items-center md:justify-between md:py-0">
          <div className="flex min-w-0 items-center gap-2">
            <h2 className="text-sm font-medium text-gray-500 uppercase tracking-widest italic leading-none">
              {activeTab === 'create' ? t.header.creationCore : activeTab === 'comfy' ? t.header.comfy : activeTab === 'history' ? t.header.history : activeTab === 'novelviz' ? t.header.novelviz : t.header.brand}
            </h2>
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
          </div>
          
          <TopToolbar 
            onOpenSettings={() => requireAuth(() => openSettings("profile"), "login")} 
            onOpenSettingsTab={(tab) => requireAuth(() => openSettings(tab), "login")}
            selectedModel={selectedModel}
            onModelChange={setSelectedModel}
            chatModels={chatModels}
            language={language}
            onLanguageChange={setLanguage}
            addToast={addToast}
            t={t}
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
          <AnimatePresence initial={false}>
            {activeTab === 'history' ? (
              <HistorySpace
                key="history"
                addToast={addToast}
                t={t.history}
                onOpenTask={openHistoryTask}
              />
            ) : activeTab === 'comfy' && comfyEnabled ? (
              <ComfyWorkbench
                key="comfy-workbench"
                isAuthenticated={isAuthenticated}
                selectedModel={selectedModel}
                chatModels={chatModels}
                onOpenAuth={openAuth}
                addToast={addToast}
                selectedTaskNo={selectedComfyTaskNo}
                resetSignal={workbenchResetSignal}
                onGenerationSubmitted={() => fetchAccountSummary().then(applyAccountSummary).catch(() => {})}
              />
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
                key="generation-workbench"
                isAuthenticated={isAuthenticated}
                selectedModel={selectedModel}
                chatModels={chatModels}
                onOpenAuth={openAuth}
                addToast={addToast}
                selectedTaskNo={selectedTaskNo}
                resetSignal={workbenchResetSignal}
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
    </div>
  );
}
