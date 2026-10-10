import { motion } from "motion/react";
import { localAvatar } from "@shared/services/avatar";
import { LogOut, ShieldCheck, Mail, Globe, Sparkles } from "lucide-react";
import { useLanguage } from "@shared/i18n";
import { logout, type UserInfo } from "../services/apiService";

export default function UserPanel({
  onClose,
  onOpenSettingsTab,
  userInfo,
}: {
  onClose: () => void,
  onOpenSettingsTab?: (tab: "profile" | "security" | "quota" | "support") => void,
  userInfo?: UserInfo | null,
}) {
  const { t, language, setLanguage } = useLanguage();
  const username = userInfo?.username || "user";
  const displayName = userInfo?.display_name || userInfo?.nickname || username;
  const avatarUrl = userInfo?.avatar?.trim() || localAvatar(username);
  const profileRemark = userInfo?.remark?.trim() || "";
  const quotaRemaining = Math.max(userInfo?.quota_remaining ?? 0, 0);
  const quotaFrozen = Math.max(userInfo?.quota_frozen ?? 0, 0);
  const quotaTotal = Math.max(userInfo?.quota_total ?? 0, 0);
  const quotaCapacity = Math.max(quotaTotal, quotaRemaining + quotaFrozen);
  const quotaPercent = quotaCapacity > 0 ? Math.min(Math.round((quotaRemaining / quotaCapacity) * 100), 100) : 0;

  return (
    <motion.div
      initial={{ opacity: 0, y: 10, scale: 0.95 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 10, scale: 0.95 }}
      transition={{ duration: 0.16, ease: [0.2, 0, 0, 1] }}
      className="fixed inset-x-3 top-[5.25rem] z-50 max-h-[calc(100vh-6.5rem)] overflow-y-auto rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface)] shadow-[var(--shadow-overlay)] sm:absolute sm:inset-x-auto sm:right-0 sm:top-16 sm:max-h-none sm:w-80 sm:overflow-visible sm:mr-0"
      onClick={(event) => event.stopPropagation()}
    >
      <div className="space-y-4 p-6 pb-5 text-center sm:p-8 sm:pb-6">
        <div className="relative inline-block mx-auto">
          <div className="w-24 h-24 rounded-[2rem] border-2 border-[var(--color-surface)] shadow-[var(--shadow-floating)] flex items-center justify-center overflow-hidden bg-[var(--color-surface-sunken)]">
            <img src={avatarUrl} alt={`${displayName} avatar`} className="h-full w-full object-cover" />
          </div>
          <div className="absolute -bottom-1 -right-1 w-7 h-7 bg-[var(--color-ink)] rounded-xl flex items-center justify-center shadow-sm">
            <Sparkles className="w-3.5 h-3.5 text-[var(--color-surface)]" />
          </div>
        </div>

        <div>
          <h3 className="text-xl font-bold tracking-tight text-[var(--color-ink)] leading-none">{displayName}</h3>
          <div className="mt-2 flex items-center justify-center gap-1.5">
            <div className="w-1.5 h-1.5 rounded-full bg-[var(--color-success)]" />
            <span className="text-[10px] font-bold text-[var(--color-submuted)] uppercase tracking-widest leading-none">
              {userInfo?.role || "user"}
            </span>
          </div>
          {displayName !== username && (
            <p className="mt-3 text-xs font-semibold text-[var(--color-submuted)] tabular-nums">@{username}</p>
          )}
          {profileRemark && (
            <p className="mx-auto mt-3 max-w-[15rem] text-sm leading-6 text-[var(--color-muted)]">{profileRemark}</p>
          )}
        </div>
      </div>

      <div className="space-y-1 px-3 pb-3">
        <div className="mx-1 mb-4 space-y-3 rounded-2xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-4 sm:mx-3">
          <div className="flex items-center justify-between text-[10px] font-black text-[var(--color-muted)] uppercase tracking-widest">
            <span>{t.common.quota}</span>
            <span className="text-[var(--color-ink)] tabular-nums">{quotaRemaining} / {quotaCapacity}</span>
          </div>
          <div className="h-1.5 w-full bg-[var(--color-border-subtle)] rounded-full overflow-hidden">
            <motion.div
              initial={{ width: 0 }}
              animate={{ width: `${quotaPercent}%` }}
              transition={{ type: 'spring', stiffness: 200, damping: 25 }}
              className="h-full bg-[var(--color-ink)] rounded-full"
            />
          </div>
        </div>

        {[
          { icon: ShieldCheck, label: t.workspace.nav.privacy, tab: "security" as const },
          { icon: Mail, label: t.workspace.nav.support, tab: "support" as const },
        ].map((item, i) => (
          <button
            key={i}
            type="button"
            onClick={() => {
              onClose();
              onOpenSettingsTab?.(item.tab);
            }}
            className="group mx-1 flex w-full items-center gap-3 rounded-xl px-5 py-3 text-sm text-[var(--color-muted)] transition-all hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)] sm:mx-3 sm:px-6"
          >
            <item.icon className="w-4 h-4 transition-transform group-hover:scale-110" />
            <span className="font-medium">{item.label}</span>
          </button>
        ))}

        <button
          onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')}
          className="group mx-1 flex w-full items-center justify-between rounded-xl px-5 py-3 text-sm text-[var(--color-muted)] transition-all hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)] sm:mx-3 sm:px-6"
        >
          <div className="flex items-center gap-3">
            <Globe className="w-4 h-4 transition-transform group-hover:scale-110" />
            <span className="font-medium">{t.common.language}</span>
          </div>
          <span className="text-[10px] font-black uppercase tracking-widest text-[var(--color-submuted)]">{language === 'zh' ? 'EN' : '中文'}</span>
        </button>

        <div className="mx-4 my-2 h-[1px] bg-[var(--color-border-subtle)] sm:mx-6" />

        <button
          onClick={() => logout()}
          className="group mx-1 flex w-full items-center gap-3 rounded-xl px-5 py-3 text-sm text-[var(--color-danger)] transition-all hover:bg-[var(--color-danger-light)] sm:mx-3 sm:px-6"
        >
          <LogOut className="w-4 h-4 transition-transform group-hover:-translate-x-1" />
          <span className="font-bold">{t.workspace.nav.logout}</span>
        </button>
      </div>
    </motion.div>
  );
}
