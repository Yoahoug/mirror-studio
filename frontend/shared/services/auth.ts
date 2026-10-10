import { appConfig } from '../config/app-mode';
import { apiRequest } from './client';

export interface AccountSummary {
  user_id: number;
  username: string;
  email: string;
  email_verified: boolean;
  available_quota: number;
  daily_quota: number;
  frozen_quota: number;
  is_staff: boolean;
}

export interface CaptchaData {
  captcha_code: string;
  captcha_svg: string;
}

export async function fetchAccountSummary(): Promise<AccountSummary> {
  return apiRequest<AccountSummary>('/api/auth/summary');
}

export async function fetchCaptcha(): Promise<CaptchaData> {
  return apiRequest<CaptchaData>('/api/auth/captcha');
}

export async function login(payload: { username: string; password: string; captcha_code?: string }): Promise<void> {
  await apiRequest('/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function register(payload: {
  username: string;
  password: string;
  password_confirm?: string;
  email?: string;
  captcha_code?: string;
}): Promise<void> {
  await apiRequest('/api/auth/register', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function logout(): Promise<{ logged_out: boolean }> {
  const app = typeof window === 'undefined' ? '' : window.location.pathname;

  try {
    const res = await apiRequest<{ logged_out: boolean; local_mode?: boolean }>('/api/auth/logout', {
      method: 'POST',
    });

    if (appConfig.isOnline) {
      // 整页跳转是必要例外：登出后必须重新注入 SSR 外壳（新会话 cookie、新 data-*）。
      // 从 console 登出时带 next 回跳创作台登录页；从 workspace 登出原地重置。
      window.location.href =
        app.startsWith('/console') || app.startsWith('/admin')
          ? '/workspace?auth=login&next=%2Fconsole'
          : '/workspace?auth=login';
    }
    return res || { logged_out: true };
  } catch (err) {
    if (appConfig.isOnline) {
      window.location.href =
        app.startsWith('/console') || app.startsWith('/admin')
          ? '/workspace?auth=login&next=%2Fconsole'
          : '/workspace?auth=login';
    }
    return { logged_out: false };
  }
}
