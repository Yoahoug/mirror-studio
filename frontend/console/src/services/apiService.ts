export interface Pagination {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
  has_next: boolean;
  has_previous: boolean;
}

export interface PagedResponse<T> {
  items: T[];
  pagination: Pagination;
}

export interface UserInfo {
  username: string;
  email: string;
  role: string;
  daily_quota: number;
  quota_remaining: number;
  quota_frozen: number;
  quota_total: number;
}

export interface DashboardData {
  today: {
    new_users: number;
    tasks: number;
    failed_tasks: number;
    succeeded_tasks: number;
  };
  totals: {
    users: number;
    images: number;
    tasks: number;
    active_channels: number;
  };
  latest_tasks: AdminTask[];
  latest_users: AdminUser[];
  recent_failures: FailureLog[];
}

export interface UserQuotaInfo {
  daily_quota: number;
  available_quota: number;
  frozen_quota: number;
  total_generated_count: number;
  last_reset_date: string | null;
}

export interface AdminUser {
  id: number;
  username: string;
  email: string;
  mobile: string;
  role: string;
  status: string;
  is_staff: boolean;
  date_joined: string | null;
  last_login: string | null;
  last_login_ip: string | null;
  quota: UserQuotaInfo | null;
}

export interface AdminUserDetail extends AdminUser {
  profile: {
    nickname: string;
    avatar: string;
    register_ip: string | null;
    register_source: string;
    remark: string;
  } | null;
}

export interface AdminTask {
  id: number;
  task_no: string;
  user_id: number;
  username: string;
  status: string;
  prompt: string;
  style_code: string;
  size_code: string;
  channel_name: string;
  quota_cost: number;
  error_code: string;
  error_message: string;
  provider_request_id: string;
  provider_response_json: Record<string, any>;
  created_at: string | null;
  finished_at: string | null;
}

export interface FailureLog {
  id: number;
  event_type: string;
  source: string;
  success: boolean;
  remark: string;
  task_no: string;
  error_code: string;
  error_message: string;
  provider_request_id: string;
  payload_json: Record<string, any>;
  created_at: string | null;
}

export interface SiteConfigItem {
  id: number;
  key: string;
  value: string;
  is_secret: boolean;
  description: string;
  category: string;
  updated_at: string | null;
}

export interface ConfigCategory {
  code: string;
  label: string;
}

export interface ConfigListResponse {
  items: SiteConfigItem[];
  categories: ConfigCategory[];
}

export interface ImageChannel {
  id: number;
  name: string;
  channel_code: string;
  channel_type: "image" | "llm";
  provider_name: string;
  base_url: string;
  api_path: string;
  edit_api_path: string;
  model_name: string;
  timeout_seconds: number;
  max_retries: number;
  is_active: boolean;
  priority: number;
  created_at: string | null;
  updated_at: string | null;
}

export interface ImageChannelPayload {
  name: string;
  channel_code?: string;
  channel_type: "image" | "llm";
  provider_name: string;
  base_url: string;
  api_path: string;
  edit_api_path: string;
  model_name: string;
  timeout_seconds: number;
  max_retries: number;
  is_active: boolean;
  priority: number;
  api_key?: string;
}

export interface QueryParams {
  page?: number;
  per_page?: number;
  keyword?: string;
  status?: string;
  role?: string;
}

interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

export class ConsoleApiError extends Error {
  status: number;
  code?: number;

  constructor(message: string, status: number, code?: number) {
    super(message);
    this.name = "ConsoleApiError";
    this.status = status;
    this.code = code;
  }
}

function getCsrfToken(): string {
  const match = document.cookie.match(/csrftoken=([^;]+)/);
  if (match?.[1]) return decodeURIComponent(match[1]);
  const el = document.getElementById("console-root");
  return el?.dataset.csrf || "";
}

function getBaseHeaders(): Record<string, string> {
  return {
    "Content-Type": "application/json",
    "X-CSRFToken": getCsrfToken(),
  };
}

function buildQuery(params: QueryParams = {}): string {
  const search = new URLSearchParams();
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null && String(value).trim() !== "") {
      search.set(key, String(value));
    }
  });
  const query = search.toString();
  return query ? `?${query}` : "";
}

async function apiRequest<T>(url: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(url, {
    credentials: "same-origin",
    headers: {
      ...getBaseHeaders(),
      ...((options.headers as Record<string, string>) || {}),
    },
    ...options,
  });

  const redirectedUrl = response.redirected ? new URL(response.url, window.location.origin) : null;
  const redirectedPath = redirectedUrl?.pathname || "";
  const redirectedAuth = redirectedUrl?.searchParams.get("auth") || "";
  if (response.status === 401 || redirectedPath === "/workspace" || redirectedAuth === "login") {
    window.location.href = `/workspace?auth=login&next=${encodeURIComponent("/console")}`;
    throw new ConsoleApiError("未登录", 401);
  }

  const contentType = response.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    throw new ConsoleApiError(response.ok ? "服务返回格式异常" : `请求失败：${response.status}`, response.status);
  }

  const body: ApiResponse<T> = await response.json().catch(() => ({
    code: -1,
    message: "服务返回格式异常",
    data: null as T,
  }));

  if (!response.ok || body.code !== 0) {
    throw new ConsoleApiError(body.message || `请求失败：${response.status}`, response.status, body.code);
  }
  return body.data;
}

export function logout(): Promise<void> {
  return fetch("/api/auth/logout", {
    method: "POST",
    credentials: "same-origin",
    headers: getBaseHeaders(),
  }).then(() => {
    window.location.href = `/workspace?auth=login&next=${encodeURIComponent("/console")}`;
  });
}

export function fetchDashboard(): Promise<DashboardData> {
  return apiRequest<DashboardData>("/console/api/dashboard/");
}

export function fetchUsers(params: QueryParams = {}): Promise<PagedResponse<AdminUser>> {
  return apiRequest<PagedResponse<AdminUser>>(`/console/api/users/${buildQuery(params)}`);
}

export function fetchUserDetail(id: number): Promise<AdminUserDetail> {
  return apiRequest<AdminUserDetail>(`/console/api/users/${id}/`);
}

export function updateUserQuota(
  id: number,
  payload: { daily_quota?: number; available_quota?: number; remark?: string },
): Promise<UserQuotaInfo> {
  return apiRequest<UserQuotaInfo>(`/console/api/users/${id}/quota/`, {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function fetchTasks(params: QueryParams = {}): Promise<PagedResponse<AdminTask>> {
  return apiRequest<PagedResponse<AdminTask>>(`/console/api/tasks/${buildQuery(params)}`);
}

export function fetchConfig(category?: string): Promise<ConfigListResponse> {
  const query = category ? `?category=${encodeURIComponent(category)}` : "";
  return apiRequest<ConfigListResponse>(`/console/api/config/${query}`);
}

export function updateConfig(
  key: string,
  payload: { value: string; is_secret?: boolean; description?: string; category?: string },
): Promise<{ key: string; updated_at: string }> {
  return apiRequest<{ key: string; updated_at: string }>(`/console/api/config/${encodeURIComponent(key)}/`, {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

export function batchUpdateConfig(
  items: Array<{ key: string; value: string; is_secret?: boolean; description?: string; category?: string }>,
): Promise<{ updated: number }> {
  return apiRequest<{ updated: number }>("/console/api/config/batch/", {
    method: "PUT",
    body: JSON.stringify({ items }),
  });
}

export function fetchImageChannels(): Promise<ImageChannel[]> {
  return apiRequest<ImageChannel[]>("/console/api/image-channels/");
}

export function createImageChannel(payload: ImageChannelPayload): Promise<ImageChannel> {
  return apiRequest<ImageChannel>("/console/api/image-channels/", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function updateImageChannel(id: number, payload: ImageChannelPayload): Promise<ImageChannel> {
  return apiRequest<ImageChannel>(`/console/api/image-channels/${id}/`, {
    method: "PUT",
    body: JSON.stringify(payload),
  });
}

export function deleteImageChannel(id: number): Promise<{ deleted: boolean }> {
  return apiRequest<{ deleted: boolean }>(`/console/api/image-channels/${id}/`, {
    method: "DELETE",
  });
}
