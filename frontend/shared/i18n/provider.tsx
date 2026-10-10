/**
 * 语言状态 Provider：localStorage 持久化 + 类型安全的 t() 消费入口。
 * 两个 SPA 共用；SSR 页面静态渲染不经过这里。
 */

import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { Language } from '../types/common';
import { getDictionary, type TranslationDictionary } from './index';

interface LanguageContextValue {
  language: Language;
  setLanguage: (lang: Language) => void;
  t: TranslationDictionary;
}

const LANGUAGE_STORAGE_KEY = 'mirror.language';

const LanguageContext = createContext<LanguageContextValue | null>(null);

function readInitialLanguage(): Language {
  if (typeof window === 'undefined') return 'zh';
  const saved = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
  return saved === 'en' ? 'en' : 'zh';
}

export function LanguageProvider({ children }: { children: React.ReactNode }) {
  const [language, setLanguageState] = useState<Language>(readInitialLanguage);

  const setLanguage = useCallback((lang: Language) => {
    setLanguageState(lang);
    try {
      window.localStorage.setItem(LANGUAGE_STORAGE_KEY, lang);
    } catch {
      // localStorage 不可用（隐私模式等）时仅本次会话生效
    }
  }, []);

  useEffect(() => {
    document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en';
  }, [language]);

  const value = useMemo<LanguageContextValue>(
    () => ({ language, setLanguage, t: getDictionary(language) }),
    [language, setLanguage]
  );

  return <LanguageContext.Provider value={value}>{children}</LanguageContext.Provider>;
}

export function useLanguage(): LanguageContextValue {
  const ctx = useContext(LanguageContext);
  if (!ctx) {
    throw new Error('useLanguage must be used within a LanguageProvider');
  }
  return ctx;
}
