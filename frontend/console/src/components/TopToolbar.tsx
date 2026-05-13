import { useState } from "react";
import { motion, AnimatePresence } from "motion/react";
import { 
  Zap, 
  Bell, 
  Settings, 
  Moon, 
  Sun,
  ChevronDown,
  CreditCard,
  Cpu,
  Layers,
  Sparkles
} from "lucide-react";
import RechargeModal from "./RechargeModal";
import NotificationPanel from "./NotificationPanel";
import UserPanel from "./UserPanel";
import SettingsModal from "./SettingsModal";
import { type UserInfo } from "../services/apiService";

export default function TopToolbar({ 
  onOpenRecharge, 
  onOpenSettings,
  onOpenInvite,
  selectedModel,
  onModelChange,
  language,
  onLanguageChange,
  addToast,
  t,
  userInfo
}: { 
  onOpenRecharge: () => void, 
  onOpenSettings: () => void,
  onOpenInvite?: () => void,
  selectedModel?: string,
  onModelChange?: (model: string) => void,
  language?: string,
  onLanguageChange?: (lang: any) => void,
  addToast?: (msg: string, type?: "success" | "error" | "info") => void,
  t: any,
  userInfo?: UserInfo | null
}) {
  const [showNotifications, setShowNotifications] = useState(false);
  const [showUser, setShowUser] = useState(false);
  const [showModelTray, setShowModelTray] = useState(false);

  const models = [
    { id: "llm-primary", name: "主力 LLM", desc: language === 'zh' ? "后台对接方配置" : "Console managed provider", icon: Sparkles, color: "text-blue-500 bg-blue-50" },
    { id: "llm-backup", name: "备用 LLM", desc: language === 'zh' ? "仅用于对话辅助" : "Assistant-only model", icon: Cpu, color: "text-emerald-500 bg-emerald-50" },
  ];

  const currentModel = models.find(m => m.id === selectedModel) || models[0];

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

  return (
    <>
      <div className="flex items-center gap-3">
        {/* Status Badges Pill */}
        <div className={pillClass}>
          <Zap className="w-3.5 h-3.5 text-amber-500 fill-amber-500" />
          <span className="text-xs font-semibold text-gray-700">{t.toolbar.remaining} {userInfo?.quota_remaining ?? '--'}</span>
          <div className="w-[1px] h-3 bg-gray-200 mx-1" />
          <span className="text-xs text-gray-400">{t.toolbar.frozen} {userInfo?.quota_frozen ?? 0}</span>
        </div>

        {/* Model Switcher Action */}
        <div className="relative">
          <motion.button
            onClick={() => {
              setShowModelTray(!showModelTray);
              setShowNotifications(false);
              setShowUser(false);
            }}
            whileHover={{ scale: 1.02, y: -1 }}
            whileTap={{ scale: 0.98 }}
            className={`${pillClass} group`}
          >
            <div className={`w-5 h-5 rounded-md flex items-center justify-center ${currentModel.color}`}>
              <currentModel.icon className="w-3 h-3" />
            </div>
            <span className="text-xs font-bold text-gray-700 group-hover:text-black">{currentModel.name}</span>
            <ChevronDown className={`w-3 h-3 text-gray-300 transition-transform ${showModelTray ? 'rotate-180' : ''}`} />
          </motion.button>

          <AnimatePresence>
            {showModelTray && (
              <>
                <div className="fixed inset-0 z-40" onClick={() => setShowModelTray(false)} />
                <motion.div
                  initial={{ opacity: 0, y: 10, scale: 0.95 }}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0, y: 10, scale: 0.95 }}
                  className="absolute top-full mt-3 right-0 w-64 bg-white rounded-[1.5rem] shadow-2xl border border-gray-100 p-2 z-50 neumorph-subtle"
                >
                  <div className="px-3 py-2 mb-1">
                    <p className="text-[10px] font-black text-gray-300 uppercase tracking-widest italic">{language === 'zh' ? '选择创作引擎' : 'Select Engine'}</p>
                  </div>
                  <div className="space-y-1">
                    {models.map((m) => (
                      <button
                        key={m.id}
                        onClick={() => {
                          onModelChange?.(m.id);
                          setShowModelTray(false);
                          addToast?.(`${t.toasts.modelSwitched} ${m.name}`, "success");
                        }}
                        className={`w-full flex items-center gap-3 p-3 rounded-xl transition-all group ${
                          selectedModel === m.id ? 'bg-gray-50' : 'hover:bg-gray-50/50'
                        }`}
                      >
                        <div className={`w-10 h-10 rounded-2xl flex items-center justify-center shrink-0 transition-transform group-hover:scale-110 ${m.color}`}>
                          <m.icon className="w-5 h-5" />
                        </div>
                        <div className="text-left">
                          <p className={`text-sm font-bold ${selectedModel === m.id ? 'text-black' : 'text-gray-600 group-hover:text-black'}`}>
                            {m.name}
                          </p>
                          <p className="text-[10px] text-gray-400 line-clamp-1">{m.desc}</p>
                        </div>
                        {selectedModel === m.id && (
                          <div className="ml-auto w-1.5 h-1.5 rounded-full bg-black" />
                        )}
                      </button>
                    ))}
                  </div>
                </motion.div>
              </>
            )}
          </AnimatePresence>
        </div>

        {/* Quick Recharge */}
        <motion.button
          onClick={onOpenRecharge}
          whileHover={{ scale: 1.02, y: -1 }}
          whileTap={{ scale: 0.98 }}
          className={`${pillClass} group`}
        >
          <CreditCard className="w-3.5 h-3.5 text-gray-400 group-hover:text-black transition-colors" />
          <span className="text-xs font-semibold text-gray-600 group-hover:text-black">{t.toolbar.quickRecharge}</span>
        </motion.button>

        {/* Action Icons */}
        <div className="flex items-center gap-2 relative">
          <motion.button
            onClick={toggleNotifications}
            whileHover={{ scale: 1.1 }}
            whileTap={{ scale: 0.9 }}
            className={`w-9 h-9 rounded-full border border-gray-100 flex items-center justify-center transition-all shadow-sm ${showNotifications ? 'bg-black text-white' : 'bg-white text-gray-400 hover:text-black'}`}
          >
            <Bell className={`w-4 h-4 stroke-[1.8] ${showNotifications ? 'text-white' : ''}`} />
          </motion.button>
          
          <motion.button
            onClick={onOpenSettings}
            whileHover={{ scale: 1.1 }}
            whileTap={{ scale: 0.9 }}
            className="w-9 h-9 rounded-full bg-white border border-gray-100 flex items-center justify-center text-gray-400 hover:text-black transition-all shadow-sm"
          >
            <Settings className="w-4 h-4 stroke-[1.8]" />
          </motion.button>

          {showNotifications && (
            <div className="fixed inset-0 z-40" onClick={closePanels} />
          )}
          <AnimatePresence>
            {showNotifications && (
              <NotificationPanel onClose={closePanels} />
            )}
          </AnimatePresence>
        </div>

        <div className="flex items-center gap-3 pl-4 border-l border-gray-100 relative">
          <div className="text-right hidden md:block">
            <p className="text-xs font-bold text-gray-900 leading-none">{userInfo?.username || 'guest'}</p>
            <p className="text-[10px] text-gray-400 mt-1 uppercase tracking-tighter">{userInfo?.role || 'user'}</p>
          </div>
          <motion.div 
            onClick={toggleUser}
            whileHover={{ scale: 1.05 }}
            className="relative cursor-pointer group"
          >
            <div className="w-9 h-9 rounded-full bg-gradient-to-tr from-gray-100 to-gray-50 border border-gray-200 shadow-inner flex items-center justify-center overflow-hidden">
              <div className={`w-full h-full bg-[url('https://api.dicebear.com/7.x/avataaars/svg?seed=${userInfo?.username || 'guest'}')] bg-cover`} />
            </div>
            <div className={`absolute -bottom-1 -right-1 w-4 h-4 rounded-full flex items-center justify-center shadow-sm border border-gray-100 transition-colors ${showUser ? 'bg-black text-white' : 'bg-white text-gray-400'}`}>
              <ChevronDown className="w-2.5 h-2.5" />
            </div>
          </motion.div>

          <AnimatePresence>
            {showUser && (
              <>
                <div className="fixed inset-0 z-40" onClick={closePanels} />
                <UserPanel 
                  onClose={closePanels} 
                  onOpenInvite={() => {
                    closePanels();
                    onOpenInvite?.();
                  }}
                  language={language as any}
                  onLanguageChange={onLanguageChange as any}
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

