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
import { useLanguage } from "@shared/i18n";
import { IconButton } from "@shared/ui";
import { appConfig } from "@shared/config/app-mode";
import { localAvatar } from "@shared/services/avatar";

interface TopToolbarProps {
  onOpenSettings: () => void;
  onOpenSettingsTab?: (tab: "profile" | "security" | "quota" | "support") => void;
  selectedModel?: string;
  onModelChange?: (model: string) => void;
  chatModels?: ChatModel[];
  addToast?: (msg: string, type?: "success" | "error" | "info") => void;
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
  addToast,
  userInfo,
  accountSummary,
  releaseInfo,
  isAuthenticated = true,
  onOpenAuth,
  onOpenHistory,
  onOpenTask,
}: TopToolbarProps) {
  const { t } = useLanguage();
  const [showNotifications, setShowNotifications] = useState(false);
  const [showUser, setShowUser] = useState(false);
  const [showModelTray, setShowModelTray] = useState(false);
  const [readIds, setReadIds] = useState<string[]>(() => readStoredNotificationIds());

  const currentModel = chatModels.find((model) => model.channel_code === selectedModel) || chatModels[0];
  const modelReady = Boolean(currentModel);
  // 本地开发者模式下展示对话中转站配置引导（线上永远用不到）
  const isLocalMode = appConfig.isLocal;
  const displayName = userInfo?.display_name || userInfo?.nickname || userInfo?.username || "guest";
  const avatarUrl = userInfo?.avatar?.trim() || localAvatar(userInfo?.username || "admin");
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

  const pillClass =
    "flex items-center gap-2 h-9 bg-[var(--color-surface)] border border-[var(--color-border)] px-4 rounded-full shadow-sm hover:shadow-[var(--shadow-floating)] hover:border-[var(--color-border-hover)] transition-all cursor-pointer";

  if (!isAuthenticated) {
    return (
      <div className="flex w-full flex-wrap items-center justify-end gap-2 md:w-auto md:flex-nowrap md:gap-3">
        <div className="hidden items-center gap-2 rounded-full border border-[var(--color-border)] bg-[var(--color-surface)] px-4 shadow-sm sm:flex sm:h-9">
          <Sparkles className="w-3.5 h-3.5 text-[var(--color-warning)]" />
          <span className="text-xs font-semibold text-[var(--color-muted)]">{t.workspace.nav.login}</span>
        </div>
        <motion.button
          onClick={() => onOpenAuth?.("login")}
          whileHover={{ scale: 1.02, y: -1 }}
          whileTap={{ scale: 0.98 }}
          className="flex h-9 items-center gap-2 rounded-full border border-[var(--color-border)] bg-[var(--color-surface)] px-4 text-xs font-bold text-[var(--color-ink-secondary)] shadow-sm transition-all hover:border-[var(--color-border-hover)] hover:text-[var(--color-ink)]"
        >
          <LogIn className="w-3.5 h-3.5" />
          {t.workspace.nav.login}
        </motion.button>
        <motion.button
          onClick={() => onOpenAuth?.("register")}
          whileHover={{ scale: 1.02, y: -1 }}
          whileTap={{ scale: 0.98 }}
          className="flex h-9 items-center gap-2 rounded-full bg-[var(--color-ink)] px-4 text-xs font-bold text-[var(--color-surface)] shadow-sm transition-all hover:opacity-90"
        >
          <UserPlus className="w-3.5 h-3.5" />
          {t.workspace.auth.registerTitle}
        </motion.button>
      </div>
    );
  }

  return (
    <>
      <div className="flex w-full flex-wrap items-center justify-end gap-2 md:w-auto md:flex-nowrap md:gap-3">
        {/* 额度胶囊 */}
        <div className={`${pillClass} order-2 min-w-[132px] flex-1 justify-center px-3 sm:order-none sm:flex-none sm:justify-start`}>
          <Zap className="w-3.5 h-3.5 text-[var(--color-warning)] fill-[var(--color-warning)]" />
          <span className="text-xs font-semibold text-[var(--color-ink-secondary)] tabular-nums">
            {t.common.quota} {userInfo?.quota_remaining ?? "--"}
          </span>
          <div className="mx-1 h-3 w-[1px] bg-[var(--color-border)]" />
          <span className="text-xs text-[var(--color-submuted)] tabular-nums">
            {t.common.frozen} {userInfo?.quota_frozen ?? 0}
          </span>
        </div>

        {/* 对话模型选择器 */}
        <div className="relative order-1 min-w-0 flex-1 sm:order-none sm:flex-none">
          <motion.button
            onClick={() => {
              setShowModelTray(!showModelTray);
              setShowNotifications(false);
              setShowUser(false);
            }}
            whileHover={{ scale: 1.02, y: -1 }}
            whileTap={{ scale: 0.98 }}
            aria-label={currentModel?.name || "LLM"}
            className={`${pillClass} group w-full justify-between px-3 sm:w-auto ${modelReady ? "" : "border-[var(--color-warning)]/30 bg-[var(--color-warning-light)]"}`}
          >
            <div className={`flex h-5 w-5 items-center justify-center rounded-md ${modelReady ? "bg-[var(--color-info-light)] text-[var(--color-info)]" : "bg-[var(--color-surface)] text-[var(--color-warning)]"}`}>
              <Cpu className="h-3 w-3" />
            </div>
            <span className={`min-w-0 flex-1 truncate text-left text-xs font-bold group-hover:text-[var(--color-ink)] ${modelReady ? "text-[var(--color-ink-secondary)]" : "text-[var(--color-warning-text)]"}`}>
              {currentModel?.name || "LLM"}
            </span>
            <ChevronDown className={`h-3 w-3 text-[var(--color-submuted)] transition-transform ${showModelTray ? "rotate-180" : ""}`} />
          </motion.button>

          <AnimatePresence>
            {showModelTray && (
              <>
                <div className="fixed inset-0 z-40" onClick={() => setShowModelTray(false)} />
                <motion.div
                  initial={{ opacity: 0, y: 10, scale: 0.95 }}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0, y: 10, scale: 0.95 }}
                  transition={{ duration: 0.16, ease: [0.2, 0, 0, 1] }}
                  className="absolute left-0 top-full z-50 mt-3 w-[min(18rem,calc(100vw-1.5rem))] rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface)] p-2 shadow-[var(--shadow-overlay)] sm:left-auto sm:right-0"
                  role="menu"
                >
                  <div className="mb-1 px-3 py-2">
                    <p className="text-[10px] font-black uppercase tracking-widest text-[var(--color-submuted)]">
                      {t.workspace.chat.configTitle}
                    </p>
                  </div>
                  <div className="space-y-1">
                    {!chatModels.length && (
                      <div className="rounded-xl border border-[var(--color-warning)]/20 bg-[var(--color-warning-light)] p-3 text-left">
                        <div className="flex items-center gap-2">
                          <MessageSquare className="h-4 w-4 text-[var(--color-warning)]" />
                          <p className="text-sm font-bold text-[var(--color-ink)]">LLM</p>
                        </div>
                        <p className="mt-2 text-xs leading-5 text-[var(--color-muted)]">
                          {t.workspace.chat.placeholder}
                        </p>
                      </div>
                    )}
                    {chatModels.map((model) => (
                      <button
                        key={model.channel_code}
                        onClick={() => {
                          onModelChange?.(model.channel_code);
                          setShowModelTray(false);
                          addToast?.(`${model.name}`, "success");
                        }}
                        className={`group flex w-full items-center gap-3 rounded-xl p-3 transition-all ${selectedModel === model.channel_code ? "bg-[var(--color-surface-hover)]" : "hover:bg-[var(--color-surface-hover)]/60"}`}
                      >
                        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[var(--color-info-light)] text-[var(--color-info)] transition-transform group-hover:scale-110">
                          <Cpu className="h-5 w-5" />
                        </div>
                        <div className="text-left">
                          <p className={`text-sm font-bold ${selectedModel === model.channel_code ? "text-[var(--color-ink)]" : "text-[var(--color-ink-secondary)] group-hover:text-[var(--color-ink)]"}`}>
                            {model.name}
                          </p>
                          <p className="line-clamp-1 text-[10px] text-[var(--color-submuted)]">{model.provider_name || "LLM"} · {model.model_name}</p>
                        </div>
                        {selectedModel === model.channel_code && <div className="ml-auto h-1.5 w-1.5 rounded-full bg-[var(--color-ink)]" />}
                      </button>
                    ))}
                    {/* 本地开发者模式：对话渠道在工作台里配置（悬浮窗对话 → 设置） */}
                    {isLocalMode && chatModels.length > 0 && !chatModels[0].configured && (
                      <div className="mx-1 mt-1 rounded-xl border border-[var(--color-warning)]/20 bg-[var(--color-warning-light)] px-3 py-2 text-[11px] leading-5 text-[var(--color-warning-text)]">
                        {t.workspace.chat.configTitle}
                      </div>
                    )}
                  </div>
                </motion.div>
              </>
            )}
          </AnimatePresence>
        </div>

        {/* 通知与设置 */}
        <div className="relative order-3 flex items-center gap-2 sm:order-none">
          <IconButton
            aria-label="通知"
            size="sm"
            onClick={toggleNotifications}
            variant={showNotifications ? "primary" : "secondary"}
            className="relative rounded-full"
          >
            <Bell className="h-4 w-4 stroke-[1.8]" />
            {notificationCount > 0 && (
              <span className="absolute right-1.5 top-1.5 h-2 w-2 rounded-full bg-[var(--color-danger)]" />
            )}
          </IconButton>

          <IconButton
            aria-label="设置"
            size="sm"
            onClick={onOpenSettings}
            variant="secondary"
            className="rounded-full"
          >
            <Settings className="h-4 w-4 stroke-[1.8]" />
          </IconButton>

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

        {/* 用户面板入口 */}
        <div className="relative order-4 border-l border-[var(--color-border-subtle)] pl-2 sm:order-none sm:pl-4">
          <motion.button
            type="button"
            onClick={toggleUser}
            whileHover={{ scale: 1.02, y: -1 }}
            whileTap={{ scale: 0.98 }}
            aria-label={displayName}
            className={`flex h-11 cursor-pointer items-center gap-2 rounded-full border px-2 pl-3 shadow-sm transition-all sm:gap-3 sm:pl-4 ${showUser ? "border-[var(--color-ink)] bg-[var(--color-ink)] text-[var(--color-surface)]" : "border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-ink-secondary)] hover:border-[var(--color-border-hover)]"}`}
          >
            <div className="hidden text-right lg:block">
              <p className="text-xs font-black leading-none tabular-nums">{displayName}</p>
              <p className={`mt-1 text-[10px] uppercase tracking-tight ${showUser ? "text-[var(--color-surface)]/50" : "text-[var(--color-submuted)]"}`}>{userInfo?.role || "user"}</p>
            </div>
            <div className="flex h-8 w-8 items-center justify-center overflow-hidden rounded-full border border-[var(--color-border)] bg-[var(--color-surface-sunken)]">
              <img src={avatarUrl} alt={`${displayName} avatar`} className="h-full w-full object-cover" />
            </div>
            <ChevronDown className={`h-3 w-3 transition-transform ${showUser ? "rotate-180 text-[var(--color-surface)]/70" : "text-[var(--color-submuted)]"}`} />
          </motion.button>

          <AnimatePresence>
            {showUser && (
              <>
                <div className="fixed inset-0 z-40" onClick={closePanels} />
                <UserPanel
                  onClose={closePanels}
                  onOpenSettingsTab={onOpenSettingsTab}
                  userInfo={userInfo}
                />
              </>
            )}
          </AnimatePresence>
        </div>
      </div>
    </>
  );
}
