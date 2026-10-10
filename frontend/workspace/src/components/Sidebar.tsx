import { motion, AnimatePresence } from "motion/react";
import { 
  LayoutGrid, 
  History, 
  Film,
  Cpu,
  Sparkles,
  PanelLeftClose,
  PanelLeftOpen,
  MessageSquarePlus,
  Info
} from "lucide-react";

interface SidebarProps {
  activeTab: string;
  setActiveTab: (tab: string) => void;
  isCollapsed: boolean;
  setIsCollapsed: (collapsed: boolean) => void;
  onNewConversation?: () => void;
  onOpenNovelVisualization?: () => void;
  releaseVersion?: string;
  /** 仅本地开发者模式（data-mode="local"）为 true：显示「Comfy 生图」板块入口。 */
  showComfy?: boolean;
  t: any;
}

export default function Sidebar({ activeTab, setActiveTab, isCollapsed, setIsCollapsed, onNewConversation, onOpenNovelVisualization, releaseVersion, showComfy = false, t }: SidebarProps) {
  const menuItems = [
    { id: "create", label: t.create, icon: LayoutGrid },
    ...(showComfy ? [{ id: "comfy", label: t.comfy || "Comfy 生图", icon: Cpu }] : []),
    { id: "novelviz", label: t.novelviz || "小说可视化", icon: Film },
    { id: "new", label: t.newChat, icon: MessageSquarePlus, isAction: true },
    { id: "history", label: t.history, icon: History },
  ];

  const handleItemClick = (item: any) => {
    if (item.isAction && item.id === 'new') {
      onNewConversation?.();
      return;
    }
    if (item.id === "novelviz") {
      onOpenNovelVisualization?.();
      return;
    }
    setActiveTab(item.id);
  };

  return (
    <>
      <motion.div
        initial={false}
        animate={{
          width: isCollapsed ? 76 : 248,
        }}
        transition={{ type: "spring", stiffness: 300, damping: 30 }}
        className="relative z-50 hidden min-h-screen self-stretch overflow-hidden border-r border-gray-100 bg-white p-3 md:sticky md:top-0 md:flex md:h-screen md:flex-col"
      >
      <div className={`flex items-center ${isCollapsed ? 'justify-center' : 'justify-between'} px-2 py-3 mb-5 h-12`}>
        <div className="flex items-center gap-3 overflow-hidden">
          <div className="w-8 h-8 rounded-lg bg-black flex items-center justify-center shrink-0 shadow-lg shadow-black/10">
            <Sparkles className="text-white w-5 h-5" />
          </div>
          <motion.span 
            initial={false}
            animate={{ 
              opacity: isCollapsed ? 0 : 1,
              width: isCollapsed ? 0 : "auto",
              marginLeft: isCollapsed ? -8 : 0
            }}
            transition={{ duration: 0.2 }}
            className="font-bold text-xl tracking-tight text-black whitespace-nowrap overflow-hidden"
          >
            绘镜
          </motion.span>
        </div>
        {!isCollapsed && (
          <motion.button 
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            onClick={() => setIsCollapsed(true)}
            className="p-1.5 hover:bg-gray-50 rounded-lg text-gray-400 hover:text-black transition-colors shrink-0"
          >
            <PanelLeftClose className="w-5 h-5" />
          </motion.button>
        )}
      </div>

      {isCollapsed && (
        <motion.div 
          initial={{ opacity: 0, y: -10 }}
          animate={{ opacity: 1, y: 0 }}
          className="flex justify-center mb-6"
        >
          <button 
            onClick={() => setIsCollapsed(false)}
            className="p-2 hover:bg-gray-50 rounded-xl text-gray-400 hover:text-black transition-colors border border-gray-50 shadow-sm"
          >
            <PanelLeftOpen className="w-5 h-5" />
          </button>
        </motion.div>
      )}

      <nav className="flex-1 space-y-1.5">
        {menuItems.map((item) => (
          <button
            key={item.id}
            onClick={() => handleItemClick(item)}
            title={isCollapsed ? item.label : ""}
            className={`w-full flex items-center ${isCollapsed ? 'justify-center' : 'gap-3'} px-3 py-2.5 rounded-2xl transition-colors duration-200 relative group ${
              activeTab === item.id 
                ? "text-black font-semibold" 
                : "text-gray-400 hover:text-gray-900"
            }`}
          >
            {activeTab === item.id && (
              <motion.div
                layoutId="active-nav"
                className="absolute inset-0 bg-gray-50/80 rounded-2xl -z-10 shadow-sm border border-gray-100/50"
                transition={{ type: "spring", bounce: 0.2, duration: 0.6 }}
              />
            )}
            <item.icon className={`w-5 h-5 transition-transform duration-200 group-hover:scale-110 shrink-0 ${activeTab === item.id ? "text-black" : "text-gray-400 group-hover:text-gray-600"}`} />
            <motion.span 
              initial={false}
              animate={{ 
                opacity: isCollapsed ? 0 : 1,
                width: isCollapsed ? 0 : "auto",
                display: isCollapsed ? "none" : "block"
              }}
              transition={{ duration: 0.2 }}
              className="text-sm z-10 whitespace-nowrap overflow-hidden"
            >
              {item.label}
            </motion.span>
          </button>
        ))}
      </nav>

      <div className="mt-auto pt-3 border-t border-gray-100">
        <div className={`rounded-2xl border border-gray-100/50 bg-gray-50/50 ${isCollapsed ? 'flex justify-center p-2' : 'p-3.5'}`}>
          {isCollapsed ? (
            <Info className="w-5 h-5 text-gray-400" />
          ) : (
            <div className="space-y-2.5">
              <div className="flex items-center gap-2">
                <div className="p-1.5 bg-white rounded-lg border border-gray-100 shadow-sm">
                  <Info className="w-3.5 h-3.5 text-gray-400" />
                </div>
                <span className="text-[11px] font-bold text-gray-400 uppercase tracking-widest">{t.about}</span>
              </div>
              <div>
                <p className="text-[13px] font-bold text-gray-900">{t.engine}</p>
                <div className="flex items-center gap-2 mt-1">
                  <span className="px-1.5 py-0.5 bg-emerald-50 text-emerald-600 text-[10px] font-black rounded uppercase tracking-tighter">{releaseVersion || t.version}</span>
                  <span className="w-1 h-1 rounded-full bg-gray-300" />
                  <span className="text-[10px] font-medium text-gray-400 italic">{t.stable}</span>
                </div>
              </div>
              <p className="text-[10px] text-gray-400 leading-5">
                {t.tagline}
              </p>
            </div>
          )}
        </div>
      </div>
      </motion.div>

      <div className="fixed inset-x-0 bottom-0 z-[70] border-t border-gray-200/80 bg-white/96 px-3 pb-[max(env(safe-area-inset-bottom),0.75rem)] pt-2 shadow-[0_-12px_30px_rgba(15,23,42,0.08)] backdrop-blur md:hidden">
        <div className={`grid gap-2 ${menuItems.length > 4 ? "grid-cols-5" : "grid-cols-4"}`}>
          {menuItems.map((item) => {
            const active = !item.isAction && activeTab === item.id;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => handleItemClick(item)}
                className={`flex min-h-[58px] flex-col items-center justify-center rounded-2xl px-2 py-2 text-[11px] font-bold transition ${
                  active
                    ? "bg-black text-white shadow-lg shadow-black/10"
                    : item.isAction
                      ? "bg-[#6d5dfc] text-white shadow-lg shadow-[#6d5dfc]/20"
                      : "bg-gray-50 text-gray-500"
                }`}
              >
                <item.icon className="mb-1 h-[18px] w-[18px]" />
                <span className="leading-none">{item.label}</span>
              </button>
            );
          })}
        </div>
      </div>
    </>
  );
}
