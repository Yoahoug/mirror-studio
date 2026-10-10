/**
 * 共享鉴权服务：只保留两个 SPA 共用的登出。
 *
 * 这里曾经还有 login / register / fetchCaptcha / fetchAccountSummary 的副本：
 * 它们与 workspace/src/services/apiService.ts 重复、返回结构还不一样，其中
 * fetchAccountSummary 打的是不存在的 /api/auth/summary（真接口是
 * /api/account/summary）。这些副本无人引用，已随本次清理删除；要加接口就加到
 * 各自的 apiService，不要再在这里造第二份契约。
 */
import { appConfig } from '../config/app-mode';
import { apiRequest } from './client';

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
