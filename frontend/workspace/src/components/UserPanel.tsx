import { motion } from "motion/react";
import { LogOut, ShieldCheck, Mail, Globe, Sparkles } from "lucide-react";
import { Language } from "../i18n";
import { logout, type UserInfo } from "../services/apiService";

export default function UserPanel({ 
  onClose, 
  onOpenSettingsTab,
  userInfo,
  language, 
  onLanguageChange,
  t 
}: { 
  onClose: () => void, 
  onOpenSettingsTab?: (tab: "profile" | "security" | "quota" | "support") => void,
  userInfo?: UserInfo | null,
  language: Language,
  onLanguageChange: (lang: Language) => void,
  t: any
}) {
  const username = userInfo?.username || "user";
  const displayName = userInfo?.display_name || userInfo?.nickname || username;
  const fallbackAvatarUrl = `https://api.dicebear.com/7.x/avataaars/svg?seed=${encodeURIComponent(username || "admin")}`;
  const avatarUrl = userInfo?.avatar?.trim() || fallbackAvatarUrl;
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
      className="fixed inset-x-3 top-[5.25rem] z-50 max-h-[calc(100vh-6.5rem)] overflow-y-auto rounded-[2rem] border border-gray-100 bg-white shadow-2xl neumorph-subtle sm:absolute sm:inset-x-auto sm:right-0 sm:top-16 sm:max-h-none sm:w-80 sm:overflow-visible sm:mr-0"
      onClick={(event) => event.stopPropagation()}
    >
      <div className="space-y-4 p-6 pb-5 text-center sm:p-8 sm:pb-6">
        <div className="relative inline-block mx-auto">
          <div className="w-24 h-24 rounded-[2.5rem] bg-gradient-to-tr from-gray-50 to-gray-200 border-2 border-white shadow-xl flex items-center justify-center overflow-hidden">
            <img src={avatarUrl} alt={`${displayName}头像`} className="h-full w-full object-cover" />
          </div>
          <div className="absolute -bottom-1 -right-1 w-7 h-7 bg-black rounded-xl flex items-center justify-center shadow-lg">
            <Sparkles className="w-3.5 h-3.5 text-white" />
          </div>
        </div>

        <div>
          <h3 className="text-xl font-bold tracking-tight text-gray-900 leading-none">{displayName}</h3>
          <div className="mt-2 flex items-center justify-center gap-1.5">
            <div className="w-1.5 h-1.5 rounded-full bg-emerald-400" />
            <span className="text-[10px] font-bold text-gray-400 uppercase tracking-widest italic leading-none">{t.creator}</span>
          </div>
          {displayName !== username && (
            <p className="mt-3 text-xs font-semibold text-gray-400">@{username}</p>
          )}
          {profileRemark && (
            <p className="mx-auto mt-3 max-w-[15rem] text-sm leading-6 text-gray-500">{profileRemark}</p>
          )}
        </div>
      </div>

      <div className="space-y-1 px-3 pb-3">
        <div className="mx-1 mb-4 space-y-3 rounded-2xl border border-gray-100 bg-gray-50/50 p-4 sm:mx-3">
          <div className="flex items-center justify-between text-[10px] font-black text-gray-400 uppercase tracking-widest italic">
            <span>{t.quota}</span>
            <span className="text-black">{quotaRemaining} / {quotaCapacity}</span>
          </div>
          <div className="h-1.5 w-full bg-gray-100 rounded-full overflow-hidden shadow-inner">
            <motion.div 
              initial={{ width: 0 }}
              animate={{ width: `${quotaPercent}%` }}
              className="h-full bg-black rounded-full" 
            />
          </div>
        </div>

        {[
          { icon: ShieldCheck, label: t.privacy, color: "text-gray-400", tab: "security" as const },
          { icon: Mail, label: t.support, color: "text-gray-400", tab: "support" as const },
        ].map((item, i) => (
          <button
            key={i}
            type="button"
            onClick={() => {
              onClose();
              onOpenSettingsTab?.(item.tab);
            }}
            className="group mx-1 flex w-full items-center gap-3 rounded-xl px-5 py-3 text-sm text-gray-500 transition-all hover:bg-gray-50 hover:text-black sm:mx-3 sm:px-6"
          >
            <item.icon className={`w-4 h-4 transition-transform group-hover:scale-110 ${item.color}`} />
            <span className="font-medium">{item.label}</span>
          </button>
        ))}

        <button 
          onClick={() => onLanguageChange(language === 'zh' ? 'en' : 'zh')}
          className="group mx-1 flex w-full items-center justify-between rounded-xl px-5 py-3 text-sm text-gray-500 transition-all hover:bg-gray-50 hover:text-black sm:mx-3 sm:px-6"
        >
          <div className="flex items-center gap-3">
            <Globe className="w-4 h-4 text-gray-400 transition-transform group-hover:scale-110" />
            <span className="font-medium">{t.language}</span>
          </div>
          <span className="text-[10px] font-black uppercase tracking-widest text-black/40">{language === 'zh' ? 'EN' : '中文'}</span>
        </button>

        <div className="mx-4 my-2 h-[1px] bg-gray-50 sm:mx-6" />
        
        <button
          onClick={() => logout()}
          className="group mx-1 flex w-full items-center gap-3 rounded-xl px-5 py-3 text-sm text-red-400 transition-all hover:bg-red-50 hover:text-red-500 sm:mx-3 sm:px-6"
        >
          <LogOut className="w-4 h-4 transition-transform group-hover:-translate-x-1" />
          <span className="font-bold">{t.logout}</span>
        </button>
      </div>
    </motion.div>
  );
}
