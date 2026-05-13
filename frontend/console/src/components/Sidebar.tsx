import { motion, AnimatePresence } from "motion/react";
import { 
  LayoutGrid, 
  History, 
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
  t: any;
}

export default function Sidebar({ activeTab, setActiveTab, isCollapsed, setIsCollapsed, onNewConversation, t }: SidebarProps) {
  const menuItems = [
    { id: "create", label: t.create, icon: LayoutGrid },
    { id: "new", label: t.newChat, icon: MessageSquarePlus, isAction: true },
    { id: "history", label: t.history, icon: History },
  ];

  const handleItemClick = (item: any) => {
    if (item.isAction && item.id === 'new') {
      onNewConversation?.();
      return;
    }
    setActiveTab(item.id);
  };

  return (
    <motion.div 
      initial={false}
      animate={{ 
        width: isCollapsed ? 80 : 260,
      }}
      transition={{ type: "spring", stiffness: 300, damping: 30 }}
      className="h-full border-r border-gray-100 bg-white flex flex-col p-4 z-50 relative overflow-hidden"
    >
      <div className={`flex items-center ${isCollapsed ? 'justify-center' : 'justify-between'} px-2 py-4 mb-8 h-12`}>
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

      <nav className="flex-1 space-y-2">
        {menuItems.map((item) => (
          <button
            key={item.id}
            onClick={() => handleItemClick(item)}
            title={isCollapsed ? item.label : ""}
            className={`w-full flex items-center ${isCollapsed ? 'justify-center' : 'gap-3'} px-3 py-3 rounded-2xl transition-colors duration-200 relative group ${
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

      <div className="pt-4 border-t border-gray-100">
        <div className={`p-4 rounded-2xl bg-gray-50/50 border border-gray-100/50 ${isCollapsed ? 'flex justify-center p-2' : ''}`}>
          {isCollapsed ? (
            <Info className="w-5 h-5 text-gray-400" />
          ) : (
            <div className="space-y-3">
              <div className="flex items-center gap-2">
                <div className="p-1.5 bg-white rounded-lg border border-gray-100 shadow-sm">
                  <Info className="w-3.5 h-3.5 text-gray-400" />
                </div>
                <span className="text-[11px] font-bold text-gray-400 uppercase tracking-widest">{t.about}</span>
              </div>
              <div>
                <p className="text-[13px] font-bold text-gray-900">{t.engine}</p>
                <div className="flex items-center gap-2 mt-1">
                  <span className="px-1.5 py-0.5 bg-emerald-50 text-emerald-600 text-[10px] font-black rounded uppercase tracking-tighter">{t.version}</span>
                  <span className="w-1 h-1 rounded-full bg-gray-300" />
                  <span className="text-[10px] font-medium text-gray-400 italic">{t.stable}</span>
                </div>
              </div>
              <p className="text-[11px] text-gray-400 leading-relaxed">
                {t.tagline}
              </p>
            </div>
          )}
        </div>
      </div>
    </motion.div>
  );
}
