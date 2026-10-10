import React, { useEffect, useState } from 'react';
import { AnimatePresence, motion } from 'motion/react';
import {
  LayoutDashboard,
  Users,
  Layers,
  Zap,
  Settings,
  Sparkles,
  ArrowLeft,
  Moon,
  Sun,
  Laptop,
  LogOut,
  Languages,
} from 'lucide-react';
import { useTheme } from '@shared/hooks/useTheme';
import { ErrorBoundary, IconButton, Button, useToast } from '@shared/ui';
import { useLanguage } from '@shared/i18n';
import { logout } from '@shared/services/auth';
import { appConfig } from '@shared/config/app-mode';
import { OverviewView } from './views/OverviewView';
import { UsersView } from './views/UsersView';
import { TasksView } from './views/TasksView';
import { ChannelsView } from './views/ChannelsView';
import { SettingsView } from './views/SettingsView';

type ConsoleTab = 'overview' | 'users' | 'tasks' | 'channels' | 'settings';

const CONSOLE_TABS: ConsoleTab[] = ['overview', 'users', 'tasks', 'channels', 'settings'];

/** 旧版 dashboard tab 的向后兼容映射 */
function parseTab(raw: string | null): ConsoleTab {
  if (raw && (CONSOLE_TABS as string[]).includes(raw)) return raw as ConsoleTab;
  if (raw === 'dashboard') return 'overview';
  return 'overview';
}

function readTabFromUrl(): ConsoleTab {
  if (typeof window === 'undefined') return 'overview';
  return parseTab(new URLSearchParams(window.location.search).get('tab'));
}

export default function App() {
  const { themeMode, setThemeMode } = useTheme();
  const { t, language, setLanguage } = useLanguage();
  const { addToast } = useToast();
  const [tab, setTab] = useState<ConsoleTab>(readTabFromUrl);

  // URL query 是唯一状态来源：前进/后退与刷新都能恢复
  useEffect(() => {
    const handlePopState = () => setTab(readTabFromUrl());
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, []);

  const changeTab = (nextTab: ConsoleTab) => {
    setTab(nextTab);
    const url = new URL(window.location.href);
    url.searchParams.set('tab', nextTab);
    window.history.pushState({}, '', url.toString());
    document.title = `${t.console.nav[nextTab]} · ${t.common.appName}`;
  };

  useEffect(() => {
    document.title = `${t.console.nav[tab]} · ${t.common.appName}`;
  }, [t, tab]);

  const cycleTheme = () => {
    if (themeMode === 'light') setThemeMode('dark');
    else if (themeMode === 'dark') setThemeMode('system');
    else setThemeMode('light');
  };

  const themeIcon =
    themeMode === 'light' ? (
      <Sun className="h-4 w-4 text-[var(--color-warning)]" />
    ) : themeMode === 'dark' ? (
      <Moon className="h-4 w-4 text-[var(--color-info)]" />
    ) : (
      <Laptop className="h-4 w-4 text-[var(--color-muted)]" />
    );

  const themeLabel =
    themeMode === 'light'
      ? t.common.themeLight
      : themeMode === 'dark'
        ? t.common.themeDark
        : t.common.themeSystem;

  const handleLogout = async () => {
    await logout();
    if (appConfig.isLocal) {
      // 本地模式没有登录页：登出仅提示，绝不跳 /workspace?auth=login 死路径
      addToast(t.console.brand.logout, 'info');
    }
  };

  const navItems: Array<{ id: ConsoleTab; label: string; icon: typeof Layers }> = [
    { id: 'overview', label: t.console.nav.overview, icon: LayoutDashboard },
    { id: 'users', label: t.console.nav.users, icon: Users },
    { id: 'tasks', label: t.console.nav.tasks, icon: Layers },
    { id: 'channels', label: t.console.nav.channels, icon: Zap },
    { id: 'settings', label: t.console.nav.settings, icon: Settings },
  ];

  return (
    <div className="flex min-h-screen flex-col bg-[var(--color-bg)] text-[var(--color-ink)] transition-colors duration-200">
      {/* 顶部常驻导航栏 (Quiet Minimal Light Chrome) */}
      <header className="glass-chrome sticky top-0 z-50 border-b border-[var(--color-border)] px-4 sm:px-6">
        <div className="mx-auto flex h-16 max-w-7xl items-center justify-between gap-4">
          {/* 品牌区与返回创作台 */}
          <a
            href="/workspace"
            className="group flex select-none items-center gap-2.5"
            title={t.console.brand.backToStudio}
          >
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-[var(--color-ink)] text-[var(--color-surface)] shadow-sm shadow-black/15">
              <Sparkles className="h-4 w-4" />
            </div>
            <div className="flex flex-col leading-none">
              <span className="flex items-center gap-1.5 text-sm font-extrabold tracking-tight text-[var(--color-ink)]">
                {appConfig.brandName}
                <span className="rounded-md bg-[var(--color-accent-light)] px-1.5 py-0.5 text-[10px] font-black uppercase tracking-wider text-[var(--color-accent)]">
                  Console
                </span>
              </span>
              <span className="mt-0.5 text-[10px] font-semibold tracking-wider text-[var(--color-muted)]">
                {t.console.brand.console}
              </span>
            </div>
          </a>

          {/* 导航标签切换 */}
          <nav className="hidden items-center gap-1 rounded-2xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-1 md:flex">
            {navItems.map((item) => {
              const active = tab === item.id;
              const Icon = item.icon;
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => changeTab(item.id)}
                  className={`relative z-10 flex items-center gap-1.5 rounded-xl px-3.5 py-1.5 text-xs font-bold transition-colors duration-150 ${
                    active
                      ? 'text-[var(--color-ink)]'
                      : 'text-[var(--color-muted)] hover:text-[var(--color-ink)]'
                  }`}
                >
                  {active && (
                    <motion.div
                      layoutId="console-tab-indicator"
                      transition={{ type: 'spring', stiffness: 350, damping: 30 }}
                      className="absolute inset-0 -z-10 rounded-xl bg-[var(--color-surface)] shadow-sm"
                    />
                  )}
                  <Icon className="h-3.5 w-3.5" />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </nav>

          {/* 右侧工具区 */}
          <div className="flex items-center gap-2">
            <IconButton
              aria-label={`${t.console.brand.switchTheme} (${themeLabel})`}
              onClick={cycleTheme}
              size="sm"
            >
              {themeIcon}
            </IconButton>
            <IconButton
              aria-label={t.console.brand.language}
              onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')}
              size="sm"
            >
              <span className="flex items-center gap-1 text-[11px] font-bold text-[var(--color-muted)]">
                <Languages className="h-4 w-4" />
                {language === 'zh' ? 'EN' : '中'}
              </span>
            </IconButton>

            <a
              href="/workspace"
              className="hidden items-center gap-1 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] px-3 py-1.5 text-xs font-bold text-[var(--color-ink)] transition-colors hover:bg-[var(--color-surface-hover)] sm:inline-flex"
            >
              <ArrowLeft className="h-3.5 w-3.5" />
              {t.common.appName}
            </a>

            <Button
              variant="ghost"
              size="sm"
              icon={<LogOut className="h-3.5 w-3.5" />}
              onClick={handleLogout}
              className="text-[var(--color-muted)] hover:text-[var(--color-danger)]"
            >
              {t.console.brand.logout}
            </Button>
          </div>
        </div>

        {/* 移动端横向滑动导航 */}
        <div className="scrollbar-none flex gap-1 overflow-x-auto border-t border-[var(--color-border-subtle)] py-2 md:hidden">
          {navItems.map((item) => {
            const active = tab === item.id;
            const Icon = item.icon;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => changeTab(item.id)}
                className={`flex items-center gap-1.5 whitespace-nowrap rounded-xl px-3 py-1.5 text-xs font-bold transition-colors ${
                  active
                    ? 'bg-[var(--color-ink)] text-[var(--color-surface)]'
                    : 'bg-[var(--color-surface-sunken)] text-[var(--color-muted)]'
                }`}
              >
                <Icon className="h-3.5 w-3.5" />
                <span>{item.label}</span>
              </button>
            );
          })}
        </div>
      </header>

      {/* 视图内容区 */}
      <main className="mx-auto w-full max-w-7xl flex-1 p-4 sm:p-6 lg:p-8">
        <ErrorBoundary>
          <AnimatePresence mode="wait">
            <motion.div
              key={tab}
              initial={{ opacity: 0, y: 6 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -6 }}
              transition={{ duration: 0.18, ease: [0.2, 0, 0, 1] }}
            >
              {tab === 'overview' && <OverviewView onNavigate={changeTab} />}
              {tab === 'users' && <UsersView />}
              {tab === 'tasks' && <TasksView />}
              {tab === 'channels' && <ChannelsView />}
              {tab === 'settings' && <SettingsView />}
            </motion.div>
          </AnimatePresence>
        </ErrorBoundary>
      </main>
    </div>
  );
}
