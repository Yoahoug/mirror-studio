/**
 * 显式模式与特性契约模块 (Single Source of Truth)
 * 线上 Go 模板显式注入 data-mode="online"，本地开发者模式注入 data-mode="local"
 * 不再依赖“属性缺失”隐式判定
 */

import type { AppMode } from '../types/common';

export interface AppFeatures {
  /** 本地 ComfyUI 生图板块 */
  comfy: boolean;
  /** 本地 TTS 语音合成板块 */
  tts: boolean;
  /** 本地 AI 对话悬浮窗与填回描述 */
  chatWidget: boolean;
  /** 本地免密固定账号 */
  localAccounts: boolean;
}

export interface AppConfig {
  mode: AppMode;
  isLocal: boolean;
  isOnline: boolean;
  brandName: string;
  csrfToken: string;
  initialUser: string;
  isAuthenticated: boolean;
  features: AppFeatures;
}

function resolveAppConfig(): AppConfig {
  if (typeof document === 'undefined') {
    return {
      mode: 'online',
      isLocal: false,
      isOnline: true,
      brandName: 'Mirror Studio',
      csrfToken: '',
      initialUser: '',
      isAuthenticated: false,
      features: { comfy: false, tts: false, chatWidget: false, localAccounts: false },
    };
  }

  const rootEl =
    document.getElementById('workspace-root') ||
    document.getElementById('console-root');

  const rawMode = rootEl?.dataset.mode?.trim().toLowerCase();
  const isLocal = rawMode === 'local';
  const mode: AppMode = isLocal ? 'local' : 'online';

  const brandName = rootEl?.dataset.brand || 'Mirror Studio';
  const csrfToken = rootEl?.dataset.csrf || '';
  const initialUser = rootEl?.dataset.user || '';
  const isAuthenticated = rootEl?.dataset.authenticated === '1' || Boolean(initialUser);

  return {
    mode,
    isLocal,
    isOnline: !isLocal,
    brandName,
    csrfToken,
    initialUser,
    isAuthenticated,
    features: {
      comfy: isLocal,
      tts: isLocal,
      chatWidget: isLocal,
      localAccounts: isLocal,
    },
  };
}

export const appConfig = resolveAppConfig();
