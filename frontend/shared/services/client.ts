/**
 * 统一网络通信层 (Base HTTP Client)
 * 集中管理：
 * 1. 信封解包与业务状态码判定
 * 2. CSRF Token 注入与失效重试
 * 3. 401 鉴权策略集中化（感知 local / online 模式）
 * 4. 规范 /downloads 尾斜杠兼容性
 */

import { appConfig } from '../config/app-mode';

export class ApiError extends Error {
  public code: number;
  public data: any;

  constructor(message: string, code: number = -1, data: any = null) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.data = data;
  }
}

export class AuthRequiredError extends ApiError {
  constructor(message: string = '请先登录账号') {
    super(message, 401);
    this.name = 'AuthRequiredError';
  }
}

let authRequiredListener: (() => void) | null = null;

export function setOnAuthRequired(listener: () => void) {
  authRequiredListener = listener;
}

export function getCsrfToken(): string {
  if (typeof document === 'undefined') return '';
  const match = document.cookie.match(/(?:^|;\s*)csrftoken=([^;]+)/);
  if (match) return decodeURIComponent(match[1]);
  return appConfig.csrfToken || '';
}

/** 统一规范媒体下载 URL：保证带上尾斜杠以兼容 Go ServeMux 与 Node 本地 API */
export function formatDownloadUrl(token: string, options: { preview?: boolean; download?: boolean } = {}): string {
  const query = new URLSearchParams();
  if (options.preview) query.set('preview', '1');
  if (options.download) query.set('download', '1');
  const qs = query.toString();
  return `/downloads/${encodeURIComponent(token)}/${qs ? `?${qs}` : ''}`;
}

export interface RequestOptions extends RequestInit {
  csrfRetry?: boolean;
}

export async function apiRequest<T = any>(endpoint: string, options: RequestOptions = {}): Promise<T> {
  const headers = new Headers(options.headers || {});
  headers.set('Accept', 'application/json');

  const method = (options.method || 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') {
    const csrf = getCsrfToken();
    if (csrf && !headers.has('X-CSRFToken')) {
      headers.set('X-CSRFToken', csrf);
    }
  }

  const response = await fetch(endpoint, {
    credentials: 'same-origin',
    ...options,
    headers,
  });

  // 401 处理：集中感知模式
  if (response.status === 401) {
    if (appConfig.isOnline) {
      if (authRequiredListener) {
        authRequiredListener();
      }
    }
    throw new AuthRequiredError('会话已失效或尚未登录');
  }

  const contentType = response.headers.get('content-type') || '';
  if (!contentType.includes('application/json')) {
    if (!response.ok) {
      throw new ApiError(`请求失败 (${response.status})`, response.status);
    }
    return null as T;
  }

  const payload = await response.json();

  // 后端返回标准信封 { code: 0, message: "ok", data: ... }
  if (typeof payload.code === 'number' && payload.code !== 0) {
    if (payload.code === 401) {
      if (appConfig.isOnline && authRequiredListener) {
        authRequiredListener();
      }
      throw new AuthRequiredError(payload.message || '请先登录');
    }
    throw new ApiError(payload.message || '操作失败', payload.code, payload.data);
  }

  return (payload.data !== undefined ? payload.data : payload) as T;
}
