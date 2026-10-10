import { useMemo, useState } from "react";
import { motion, AnimatePresence } from "motion/react";
import {
  Zap,
  Bell,
  Settings,
  ChevronDown,
  Cpu,
  LogIn,
  MessageSquare,
  Sparkles,
  UserPlus,
} from "lucide-react";
import NotificationPanel, { readStoredNotificationIds } from "./NotificationPanel";
import UserPanel from "./UserPanel";
import { type ReleaseInfo } from "./ReleaseNotesModal";
import { type AccountSummaryResponse, type ChatModel, type UserInfo } from "../services/apiService";
import { type Language } from "../i18n";
import { appConfig } from "@shared/config/app-mode";

interface TopToolbarProps {
  onOpenSettings: () => void;
  onOpenSettingsTab?: (tab: "profile" | "security" | "quota" | "support") => void;
  selectedModel?: string;
  onModelChange?: (model: string) => void;
  chatModels?: ChatModel[];
  language?: Language;
  onLanguageChange?: (lang: Language) => void;
  addToast?: (msg: string, type?: "success" | "error" | "info") => void;
  t: any;
  userInfo?: UserInfo | null;
  accountSummary?: AccountSummaryResponse | null;
  releaseInfo?: ReleaseInfo | null;
  isAuthenticated?: boolean;
  onOpenAuth?: (mode: "login" | "register") => void;
  onOpenHistory?: () => void;
  onOpenTask?: (taskNo: string) => void;
}

export default function TopToolbar({
  onOpenSettings,
  onOpenSettingsTab,
  selectedModel,
  onModelChange,
  chatModels = [],
  language = "zh",
  onLanguageChange,
  addToast,
  t,
  userInfo,
  accountSummary,
  releaseInfo,
  isAuthenticated = true,
  onOpenAuth,
  onOpenHistory,
  onOpenTask,
}: TopToolbarProps) {
  const [showNotifications, setShowNotifications] = useState(false);
  const [showUser, setShowUser] = useState(false);
  const [showModelTray, setShowModelTray] = useState(false);
  const [readIds, setReadIds] = useState<string[]>(() => readStoredNotificationIds());

  const currentModel = chatModels.find((model) => model.channel_code === selectedModel) || chatModels[0];
  const modelReady = Boolean(currentModel);
  // 本地开发者模式下展示对话中转站与显存释放
  const isLocalMode = appConfig.isLocal;
  const displayName = userInfo?.display_name || userInfo?.nickname || userInfo?.username || "guest";
  const fallbackAvatarUrl = `https://api.dicebear.com/7.x/avataaars/svg?seed=${encodeURIComponent(userInfo?.username || "admin")}`;
  const avatarUrl = userInfo?.avatar?.trim() || fallbackAvatarUrl;
  const notificationIds = useMemo(() => {
    const ids: string[] = [];
    if (releaseInfo?.version) {
      ids.push(`release:${releaseInfo.version}`);
    }
    if (accountSummary?.quota) {
      ids.push("quota:summary");
    }
    for (const task of accountSummary?.recent_tasks || []) {
      ids.push(`task:${task.task_no}`);
    }
    return ids;
  }, [accountSummary, releaseInfo]);
  const notificationCount = notificationIds.filter((id) => !readIds.includes(id)).length;

  const toggleNotifications = () => {
    setShowNotifications(!showNotifications);
    setShowUser(false);
  };

  const toggleUser = () => {
    setShowUser(!showUser);
    setShowNotifications(false);
  };

  const closePanels = () => {
    setShowNotifications(false);
    setShowUser(false);
  };

  const pillClass = "flex items-center gap-2 h-9 bg-white border border-gray-100 px-4 rounded-full shadow-sm hover:shadow-md hover:border-gray-200 transition-all cursor-pointer";

  if (!isAuthenticated) {
    return (
      <div className="flex w-full flex-wrap items-center justify-end gap-2 md:w-auto md:flex-nowrap md:gap-3">
        <div className="hidden items-center gap-2 rounded-full border border-gray-100 bg-white px-4 shadow-sm sm:flex sm:h-9">
          <Sparkles className="w-3.5 h-3.5 text-amber-400" />
          <span className="text-xs font-semibold text-gray-500">未登录预览</span>
        </div>
        <motion.button
          onClick={() => onOpenAuth?.("login")}
          whileHover={{ scale: 1.02, y: -1 }}
          whileTap={{ scale: 0.98 }}
          className="flex h-9 items-center gap-2 rounded-full border border-gray-100 bg-white px-4 text-xs font-bold text-gray-700 shadow-sm transition-all hover:border-gray-200 hover:text-black hover:shadow-md"
        >
          <LogIn className="w-3.5 h-3.5" />
          登录
        </motion.button>
        <motion.button
          onClick={() => onOpenAuth?.("register")}
          whileHover={{ scale: 1.02, y: -1 }}
          whileTap={{ scale: 0.98 }}
          className="flex h-9 items-center gap-2 rounded-full bg-black px-4 text-xs font-bold text-white shadow-lg shadow-black/10 transition-all hover:shadow-xl hover:shadow-black/15"
        >
          <UserPlus className="w-3.5 h-3.5" />
          注册
        </motion.button>
      </div>
    );
  }

  return (
    <>
      <div className="flex w-full flex-wrap items-center justify-end gap-2 md:w-auto md:flex-nowrap md:gap-3">
        <div className={`${pillClass} order-2 min-w-[132px] flex-1 justify-center px-3 sm:order-none sm:flex-none sm:justify-start`}>
          <Zap className="w-3.5 h-3.5 text-amber-500 fill-amber-500" />
          <span className="text-xs font-semibold text-gray-700">{t.toolbar.remaining} {userInfo?.quota_remaining ?? "--"}</span>
          <div className="mx-1 h-3 w-[1px] bg-gray-200" />
          <span className="text-xs text-gray-400">{t.toolbar.frozen} {userInfo?.quota_frozen ?? 0}</span>
        </div>

        <div className="relative order-1 min-w-0 flex-1 sm:order-none sm:flex-none">
          <motion.button
            onClick={() => {
              setShowModelTray(!showModelTray);
              setShowNotifications(false);
              setShowUser(false);
            }}
            whileHover={{ scale: 1.02, y: -1 }}
            whileTap={{ scale: 0.98 }}
            className={`${pillClass} group w-full justify-between px-3 sm:w-auto ${modelReady ? "" : "border-amber-100 bg-amber-50/60"}`}
          >
            <div className={`flex h-5 w-5 items-center justify-center rounded-md ${modelReady ? "bg-blue-50 text-blue-500" : "bg-white text-amber-600"}`}>
              <Cpu className="h-3 w-3" />
            </div>
            <span className={`min-w-0 flex-1 truncate text-left text-xs font-bold group-hover:text-black ${modelReady ? "text-gray-700" : "text-amber-700"}`}>
              {currentModel?.name || "LLM 未配置"}
            </span>
            <ChevronDown className={`h-3 w-3 text-gray-300 transition-transform ${showModelTray ? "rotate-180" : ""}`} />
          </motion.button>

          <AnimatePresence>
            {showModelTray && (
              <>
                <div className="fixed inset-0 z-40" onClick={() => setShowModelTray(false)} />
                <motion.div
                  initial={{ opacity: 0, y: 10, scale: 0.95 }}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0, y: 10, scale: 0.95 }}
                  className="absolute left-0 top-full z-50 mt-3 w-[min(18rem,calc(100vw-1.5rem))] rounded-[1.5rem] border border-gray-100 bg-white p-2 shadow-2xl neumorph-subtle sm:left-auto sm:right-0"
                >
                  <div className="mb-1 px-3 py-2">
                    <p className="text-[10px] font-black uppercase tracking-widest italic text-gray-300">
                      {language === "zh" ? "选择对话模型" : "Select Chat Model"}
                    </p>
                  </div>
                  <div className="space-y-1">
                    {!chatModels.length && (
                      <div className="rounded-2xl border border-amber-100 bg-white p-3 text-left shadow-sm">
                        <div className="flex items-center gap-2">
                          <MessageSquare className="h-4 w-4 text-amber-500" />
                          <p className="text-sm font-black text-gray-900">LLM 辅助未启用</p>
                        </div>
                        <div>
                          <p className="mt-2 text-xs leading-5 text-gray-500">不影响直接生图；需要 AI 优化提示词时，在后台“对接方配置”新增 LLM 模型即可。</p>
                        </div>
                      </div>
                    )}
                    {chatModels.map((model) => (
                      <button
                        key={model.channel_code}
                        onClick={() => {
                          onModelChange?.(model.channel_code);
                          setShowModelTray(false);
                          addToast?.(`${t.toasts.modelSwitched} ${model.name}`, "success");
                        }}
                        className={`group flex w-full items-center gap-3 rounded-xl p-3 transition-all ${selectedModel === model.channel_code ? "bg-gray-50" : "hover:bg-gray-50/50"}`}
                      >
                        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl bg-blue-50 text-blue-500 transition-transform group-hover:scale-110">
                          <Cpu className="h-5 w-5" />
                        </div>
                        <div className="text-left">
                          <p className={`text-sm font-bold ${selectedModel === model.channel_code ? "text-black" : "text-gray-600 group-hover:text-black"}`}>
                            {model.name}
                          </p>
                          <p className="line-clamp-1 text-[10px] text-gray-400">{model.provider_name || "LLM"} · {model.model_name}</p>
                        </div>
                        {selectedModel === model.channel_code && <div className="ml-auto h-1.5 w-1.5 rounded-full bg-black" />}
                      </button>
                    ))}
                    {/* 本地开发者模式：对话渠道在工作台里配置（悬浮窗对话 → 设置） */}
                    {isLocalMode && chatModels.length > 0 && !chatModels[0].configured && (
                      <div className="mx-1 mt-1 rounded-2xl border border-amber-100 bg-amber-50/70 px-3 py-2 text-[11px] leading-5 text-amber-700">
                        当前是本地模拟回复。点工作台右下角的悬浮窗 →「设置」，填接口地址（线上中转站或本机 LM Studio）、模型与密钥后即可用真实模型写提示词。
                      </div>
                    )}
                  </div>
                </motion.div>
              </>
            )}
          </AnimatePresence>
        </div>

        <div className="relative order-3 flex items-center gap-2 sm:order-none">
          <motion.button
            onClick={toggleNotifications}
            whileHover={{ scale: 1.1 }}
            whileTap={{ scale: 0.9 }}
            className={`relative flex h-9 w-9 items-center justify-center rounded-full border border-gray-100 shadow-sm transition-all ${showNotifications ? "bg-black text-white" : "bg-white text-gray-400 hover:text-black"}`}
          >
            <Bell className={`h-4 w-4 stroke-[1.8] ${showNotifications ? "text-white" : ""}`} />
            {notificationCount > 0 && (
              <span className="absolute right-1.5 top-1.5 h-2 w-2 rounded-full bg-rose-500" />
            )}
          </motion.button>

          <motion.button
            onClick={onOpenSettings}
            whileHover={{ scale: 1.1 }}
            whileTap={{ scale: 0.9 }}
            className="flex h-9 w-9 items-center justify-center rounded-full border border-gray-100 bg-white text-gray-400 shadow-sm transition-all hover:text-black"
          >
            <Settings className="h-4 w-4 stroke-[1.8]" />
          </motion.button>

          {showNotifications && (
            <div className="fixed inset-0 z-40" onClick={closePanels} />
          )}
          <AnimatePresence>
            {showNotifications && (
              <NotificationPanel
                onClose={closePanels}
                accountSummary={accountSummary}
                releaseInfo={releaseInfo}
                readIds={readIds}
                onReadIdsChange={setReadIds}
                onOpenHistory={() => {
                  closePanels();
                  onOpenHistory?.();
                }}
                onOpenTask={(taskNo) => {
                  closePanels();
                  onOpenTask?.(taskNo);
                }}
              />
            )}
          </AnimatePresence>
        </div>

        <div className="relative order-4 border-l border-gray-100 pl-2 sm:order-none sm:pl-4">
          <motion.button
            type="button"
            onClick={toggleUser}
            whileHover={{ scale: 1.02, y: -1 }}
            whileTap={{ scale: 0.98 }}
            className={`flex h-11 cursor-pointer items-center gap-2 rounded-full border px-2 pl-3 shadow-sm transition-all sm:gap-3 sm:pl-4 ${showUser ? "border-black bg-black text-white" : "border-gray-100 bg-white text-gray-700 hover:border-gray-200 hover:shadow-md"}`}
          >
            <div className="hidden text-right lg:block">
              <p className="text-xs font-black leading-none">{displayName}</p>
              <p className={`mt-1 text-[10px] uppercase tracking-tight ${showUser ? "text-white/50" : "text-gray-400"}`}>{userInfo?.role || "user"}</p>
            </div>
            <div className="flex h-8 w-8 items-center justify-center overflow-hidden rounded-full border border-gray-200 bg-gray-50 shadow-inner">
              <img src={avatarUrl} alt={`${displayName}头像`} className="h-full w-full object-cover" />
            </div>
            <ChevronDown className={`h-3 w-3 transition-transform ${showUser ? "rotate-180 text-white/70" : "text-gray-300"}`} />
          </motion.button>

          <AnimatePresence>
            {showUser && (
              <>
                <div className="fixed inset-0 z-40" onClick={closePanels} />
                <UserPanel
                  onClose={closePanels}
                  onOpenSettingsTab={onOpenSettingsTab}
                  userInfo={userInfo}
                  language={language}
                  onLanguageChange={onLanguageChange || (() => {})}
                  t={t.userPanel}
                />
              </>
            )}
          </AnimatePresence>
        </div>
      </div>
    </>
  );
}
