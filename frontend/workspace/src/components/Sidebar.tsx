import { motion } from "motion/react";
import {
  LayoutGrid,
  Film,
  Cpu,
  AudioLines,
  Sparkles,
  PanelLeftClose,
  PanelLeftOpen,
  Info,
  Library
} from "lucide-react";
import { useLanguage } from "@shared/i18n";

interface SidebarProps {
  activeTab: string;
  setActiveTab: (tab: string) => void;
  isCollapsed: boolean;
  setIsCollapsed: (collapsed: boolean) => void;
  onOpenNovelVisualization?: () => void;
  releaseVersion?: string;
  /** 仅本地开发者模式（data-mode="local"）为 true：显示「Comfy 生图」板块入口。 */
  showComfy?: boolean;
  /** 仅本地开发者模式（data-mode="local"）为 true：显示「TTS 语音合成」板块入口。 */
  showTts?: boolean;
}

export default function Sidebar({
  activeTab,
  setActiveTab,
  isCollapsed,
  setIsCollapsed,
  onOpenNovelVisualization,
  releaseVersion,
  showComfy = false,
  showTts = false,
}: SidebarProps) {
  const { t } = useLanguage();
  const ws = t.workspace;

  // 菜单顺序（本地开发者模式）：云生图排最后，避免误触点进「占位图」渠道；
  // 资产（画廊与历史）固定压轴。线上模式没有 comfy/tts，顺序自然收敛为 创作→绘卷→资产。
  const menuItems = [
    ...(showComfy ? [{ id: "comfy", label: ws.image.comfyEngine, icon: Cpu }] : []),
    ...(showTts ? [{ id: "tts", label: ws.audio.title, icon: AudioLines }] : []),
    { id: "novelviz", label: ws.subnav.story, icon: Film },
    { id: "history", label: ws.nav.assets, icon: Library },
    { id: "create", label: ws.nav.create, icon: LayoutGrid },
  ].map((item) => ({ ...item, isAction: false }));

  const handleItemClick = (id: string) => {
    if (id === "novelviz") {
      onOpenNovelVisualization?.();
      return;
    }
    setActiveTab(id);
  };

  return (
    <>
      <motion.div
        initial={false}
        animate={{
          width: isCollapsed ? 76 : 248,
        }}
        transition={{ type: "spring", stiffness: 300, damping: 30 }}
        className="relative z-50 hidden min-h-screen self-stretch overflow-hidden border-r border-[var(--color-border)] bg-[var(--color-surface)] p-3 md:sticky md:top-0 md:flex md:h-screen md:flex-col"
      >
      <div className={`flex items-center ${isCollapsed ? 'justify-center' : 'justify-between'} px-2 py-3 mb-5 h-12`}>
        <div className="flex items-center gap-3 overflow-hidden">
          <div className="w-8 h-8 rounded-lg bg-[var(--color-ink)] flex items-center justify-center shrink-0 shadow-sm">
            <Sparkles className="text-[var(--color-surface)] w-5 h-5" />
          </div>
          <motion.span
            initial={false}
            animate={{
              opacity: isCollapsed ? 0 : 1,
              width: isCollapsed ? 0 : "auto",
              marginLeft: isCollapsed ? -8 : 0
            }}
            transition={{ duration: 0.2 }}
            className="font-bold text-xl tracking-tight text-[var(--color-ink)] whitespace-nowrap overflow-hidden"
          >
            绘镜
          </motion.span>
        </div>
        {!isCollapsed && (
          <motion.button
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            onClick={() => setIsCollapsed(true)}
            aria-label="收起侧栏"
            className="p-1.5 hover:bg-[var(--color-surface-hover)] rounded-lg text-[var(--color-muted)] hover:text-[var(--color-ink)] transition-colors shrink-0"
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
            aria-label="展开侧栏"
            className="p-2 hover:bg-[var(--color-surface-hover)] rounded-xl text-[var(--color-muted)] hover:text-[var(--color-ink)] transition-colors border border-[var(--color-border-subtle)] shadow-sm"
          >
            <PanelLeftOpen className="w-5 h-5" />
          </button>
        </motion.div>
      )}

      <nav className="flex-1 space-y-1.5">
        {menuItems.map((item) => (
          <button
            key={item.id}
            onClick={() => handleItemClick(item.id)}
            title={isCollapsed ? item.label : ""}
            aria-label={item.label}
            className={`w-full flex items-center ${isCollapsed ? 'justify-center' : 'gap-3'} px-3 py-2.5 rounded-2xl transition-colors duration-200 relative group ${
              activeTab === item.id
                ? "text-[var(--color-ink)] font-semibold"
                : "text-[var(--color-muted)] hover:text-[var(--color-ink)]"
            }`}
          >
            {activeTab === item.id && (
              <motion.div
                layoutId="active-nav"
                className="absolute inset-0 bg-[var(--color-surface-sunken)] rounded-2xl -z-10 shadow-sm border border-[var(--color-border-subtle)]"
                transition={{ type: "spring", stiffness: 300, damping: 30 }}
              />
            )}
            <item.icon className={`w-5 h-5 transition-transform duration-200 group-hover:scale-110 shrink-0 ${activeTab === item.id ? "text-[var(--color-ink)]" : "text-[var(--color-muted)] group-hover:text-[var(--color-ink-secondary)]"}`} />
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

      <div className="mt-auto pt-3 border-t border-[var(--color-border-subtle)]">
        <div className={`rounded-2xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] ${isCollapsed ? 'flex justify-center p-2' : 'p-3.5'}`}>
          {isCollapsed ? (
            <Info className="w-5 h-5 text-[var(--color-muted)]" />
          ) : (
            <div className="space-y-2.5">
              <div className="flex items-center gap-2">
                <div className="p-1.5 bg-[var(--color-surface)] rounded-lg border border-[var(--color-border-subtle)] shadow-sm">
                  <Info className="w-3.5 h-3.5 text-[var(--color-muted)]" />
                </div>
                <span className="text-[11px] font-bold text-[var(--color-muted)] uppercase tracking-widest">{ws.nav.releaseNotes}</span>
              </div>
              <div>
                <p className="text-[13px] font-bold text-[var(--color-ink)]">{ws.subnav.image}</p>
                <div className="flex items-center gap-2 mt-1">
                  <span className="px-1.5 py-0.5 bg-[var(--color-success-light)] text-[var(--color-success-text)] text-[10px] font-black rounded uppercase tracking-tighter tabular-nums">{releaseVersion || t.common.version}</span>
                  <span className="w-1 h-1 rounded-full bg-[var(--color-border)]" />
                  <span className="text-[10px] font-medium text-[var(--color-muted)] italic">2026 Stable</span>
                </div>
              </div>
              <p className="text-[10px] text-[var(--color-submuted)] leading-5">
                © 2026 Yoahoug. All Rights Reserved.
              </p>
            </div>
          )}
        </div>
      </div>
      </motion.div>

      {/* 移动端底部导航：横滑胶囊布局，杜绝 375px 下 grid 挤压 */}
      <div className="fixed inset-x-0 bottom-0 z-[70] border-t border-[var(--color-border)] glass-chrome px-3 pb-[max(env(safe-area-inset-bottom),0.75rem)] pt-2 md:hidden">
        <div className="flex gap-2 overflow-x-auto scrollbar-none pb-0.5">
          {menuItems.map((item) => {
            const active = activeTab === item.id;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => handleItemClick(item.id)}
                aria-label={item.label}
                className={`flex h-12 shrink-0 items-center gap-1.5 rounded-2xl px-3.5 text-xs font-bold transition-colors ${
                  active
                    ? "bg-[var(--color-ink)] text-[var(--color-surface)] shadow-sm"
                    : "bg-[var(--color-surface-sunken)] text-[var(--color-muted)]"
                }`}
              >
                <item.icon className="h-4 w-4" />
                <span className="whitespace-nowrap leading-none">{item.label}</span>
              </button>
            );
          })}
        </div>
      </div>
    </>
  );
}
