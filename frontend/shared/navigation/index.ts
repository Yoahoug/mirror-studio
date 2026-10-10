/**
 * mirror-studio 单一导航与 URL 状态管理层
 * 纯客户端路由（无需第三方大库），以 URL SearchParams 为唯一状态来源
 * 支持刷新保留位置、前进后退、深链接分享、结构化任务回跳与向后兼容
 */

import { useCallback, useEffect, useState } from 'react';
import { appConfig } from '../config/app-mode';

export type WorkspaceTab = 'create' | 'history';
export type CreateSubTab = 'image' | 'audio' | 'story';
export type ImageEngine = 'cloud' | 'comfy';
export type ModalType = 'settings' | 'release' | 'auth' | 'voice_library' | null;

export interface NavigationState {
  tab: WorkspaceTab;
  sub: CreateSubTab;
  engine: ImageEngine;
  taskNo: string | null;
  taskKind: 'image' | 'comfy' | 'tts' | 'novelviz' | null;
  authMode: 'login' | 'register' | null;
  modal: ModalType;
  next: string;
}

export function parseNavigationState(): NavigationState {
  if (typeof window === 'undefined') {
    return {
      tab: 'create',
      sub: 'image',
      engine: 'cloud',
      taskNo: null,
      taskKind: null,
      authMode: null,
      modal: null,
      next: '',
    };
  }

  const params = new URLSearchParams(window.location.search);

  // 1. 解析 Tab（兼顾旧版 activeTab: novelviz / comfy / tts / history / create）
  let rawTab = params.get('tab') || params.get('view') || '';
  let sub: CreateSubTab = 'image';
  let engine: ImageEngine = 'cloud';
  let tab: WorkspaceTab = 'create';

  if (rawTab === 'history') {
    tab = 'history';
  } else if (rawTab === 'novelviz') {
    tab = 'create';
    sub = 'story';
  } else if (rawTab === 'tts' && appConfig.features.tts) {
    tab = 'create';
    sub = 'audio';
  } else if (rawTab === 'comfy' && appConfig.features.comfy) {
    tab = 'create';
    sub = 'image';
    engine = 'comfy';
  } else {
    tab = 'create';
    const rawSub = params.get('sub');
    if (rawSub === 'audio' && appConfig.features.tts) sub = 'audio';
    else if (rawSub === 'story') sub = 'story';
    else sub = 'image';

    const rawEngine = params.get('engine');
    if (rawEngine === 'comfy' && appConfig.features.comfy) engine = 'comfy';
    else engine = 'cloud';
  }

  // 2. 解析选中的任务（兼容结构化 ?task= 与历史 comfy:/tts:/novelviz: 前缀）
  let taskNo = params.get('task');
  let taskKind: 'image' | 'comfy' | 'tts' | 'novelviz' | null = null;
  if (taskNo) {
    if (taskNo.startsWith('novelviz:')) {
      taskKind = 'novelviz';
      taskNo = taskNo.replace('novelviz:', '');
    } else if (taskNo.startsWith('comfy:')) {
      taskKind = 'comfy';
      taskNo = taskNo.replace('comfy:', '');
    } else if (taskNo.startsWith('tts:')) {
      taskKind = 'tts';
      taskNo = taskNo.replace('tts:', '');
    } else {
      const explicitKind = params.get('task_kind');
      taskKind = (explicitKind as any) || 'image';
    }
  }

  // 3. 解析鉴权与弹窗（兼容 ?auth=login|register）
  const authParam = params.get('auth');
  let authMode: 'login' | 'register' | null = null;
  if (authParam === 'login' || authParam === 'register') {
    authMode = authParam;
  }

  const modalParam = params.get('modal') as ModalType;
  let modal: ModalType = modalParam || null;
  if (authMode) {
    modal = 'auth';
  }

  const next = params.get('next') || '';

  return {
    tab,
    sub,
    engine,
    taskNo,
    taskKind,
    authMode,
    modal,
    next,
  };
}

export function useAppNavigation() {
  const [navState, setNavState] = useState<NavigationState>(parseNavigationState);

  const syncStateFromUrl = useCallback(() => {
    setNavState(parseNavigationState());
  }, []);

  useEffect(() => {
    window.addEventListener('popstate', syncStateFromUrl);
    return () => window.removeEventListener('popstate', syncStateFromUrl);
  }, [syncStateFromUrl]);

  const updateUrl = useCallback((updater: (params: URLSearchParams) => void, replace: boolean = false) => {
    const url = new URL(window.location.href);
    updater(url.searchParams);

    if (replace) {
      window.history.replaceState({}, '', url.toString());
    } else {
      window.history.pushState({}, '', url.toString());
    }
    setNavState(parseNavigationState());
  }, []);

  const navigateTo = useCallback(
    (tab: WorkspaceTab, sub: CreateSubTab = 'image', engine: ImageEngine = 'cloud') => {
      updateUrl((params) => {
        params.set('tab', tab);
        if (tab === 'create') {
          params.set('sub', sub);
          if (sub === 'image') {
            params.set('engine', engine);
          } else {
            params.delete('engine');
          }
        } else {
          params.delete('sub');
          params.delete('engine');
        }
      });
    },
    [updateUrl]
  );

  const openTaskDetail = useCallback(
    (kind: 'image' | 'comfy' | 'tts' | 'novelviz', taskNo: string) => {
      updateUrl((params) => {
        params.set('task', taskNo);
        params.set('task_kind', kind);
      });
    },
    [updateUrl]
  );

  const closeTaskDetail = useCallback(() => {
    updateUrl((params) => {
      params.delete('task');
      params.delete('task_kind');
    });
  }, [updateUrl]);

  const openAuth = useCallback(
    (mode: 'login' | 'register' = 'login', nextUrl?: string) => {
      if (appConfig.isLocal) {
        // 本地开发者模式无登录页概念，不执行鉴权弹窗
        return;
      }
      updateUrl((params) => {
        params.set('auth', mode);
        params.set('modal', 'auth');
        if (nextUrl) params.set('next', nextUrl);
      });
    },
    [updateUrl]
  );

  const closeAuth = useCallback(() => {
    updateUrl((params) => {
      params.delete('auth');
      if (params.get('modal') === 'auth') {
        params.delete('modal');
      }
      params.delete('next');
    }, true);
  }, [updateUrl]);

  const openModal = useCallback(
    (modal: ModalType) => {
      if (!modal) return;
      updateUrl((params) => {
        params.set('modal', modal);
      });
    },
    [updateUrl]
  );

  const closeModal = useCallback(() => {
    updateUrl((params) => {
      params.delete('modal');
      params.delete('auth');
    });
  }, [updateUrl]);

  const goToConsole = useCallback((next?: string) => {
    const target = next ? `/console?next=${encodeURIComponent(next)}` : '/console';
    window.location.href = target;
  }, []);

  const goToWorkspace = useCallback((next?: string) => {
    const target = next ? `/workspace?next=${encodeURIComponent(next)}` : '/workspace';
    window.location.href = target;
  }, []);

  return {
    ...navState,
    navigateTo,
    openTaskDetail,
    closeTaskDetail,
    openAuth,
    closeAuth,
    openModal,
    closeModal,
    goToConsole,
    goToWorkspace,
  };
}
