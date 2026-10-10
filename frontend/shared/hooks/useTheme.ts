import { useCallback, useEffect, useState } from 'react';
import type { ThemeMode } from '../types/common';

const THEME_STORAGE_KEY = 'mirror.theme';

export function useTheme() {
  const [themeMode, setThemeModeState] = useState<ThemeMode>(() => {
    if (typeof window === 'undefined') return 'system';
    return (localStorage.getItem(THEME_STORAGE_KEY) as ThemeMode) || 'system';
  });

  const applyTheme = useCallback((mode: ThemeMode) => {
    if (typeof document === 'undefined') return;

    let resolvedTheme: 'light' | 'dark' = 'light';
    if (mode === 'system') {
      const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
      resolvedTheme = prefersDark ? 'dark' : 'light';
    } else {
      resolvedTheme = mode;
    }

    document.documentElement.setAttribute('data-theme', resolvedTheme);
    document.documentElement.setAttribute('data-theme-mode', mode);
  }, []);

  const setThemeMode = useCallback((mode: ThemeMode) => {
    setThemeModeState(mode);
    localStorage.setItem(THEME_STORAGE_KEY, mode);
    applyTheme(mode);
  }, [applyTheme]);

  useEffect(() => {
    applyTheme(themeMode);

    const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
    const handleChange = () => {
      if (themeMode === 'system') {
        applyTheme('system');
      }
    };

    mediaQuery.addEventListener('change', handleChange);
    return () => mediaQuery.removeEventListener('change', handleChange);
  }, [themeMode, applyTheme]);

  return {
    themeMode,
    setThemeMode,
    isDark: typeof document !== 'undefined' && document.documentElement.getAttribute('data-theme') === 'dark',
  };
}
