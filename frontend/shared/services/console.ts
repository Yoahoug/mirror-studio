import { apiRequest } from './client';

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
  role: string;
  status: string;
  is_staff: boolean;
  date_joined: string | null;
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
  style_code?: string;
  size_code?: string;
  channel_name?: string;
  quota_cost?: number;
  error_code?: string;
  error_message?: string;
  created_at: string | null;
  finished_at: string | null;
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

export interface ConfigListResponse {
  items: SiteConfigItem[];
  categories: Array<{ code: string; label: string }>;
}

export interface ImageChannel {
  id: number;
  name: string;
  channel_code: string;
  channel_type: 'image' | 'llm';
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
  channel_type: 'image' | 'llm';
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

export async function fetchConsoleDashboard(): Promise<DashboardData> {
  return apiRequest<DashboardData>('/console/api/dashboard/');
}

export async function fetchConsoleUsers(params: {
  page?: number;
  per_page?: number;
  keyword?: string;
  status?: string;
} = {}): Promise<PagedResponse<AdminUser>> {
  const query = new URLSearchParams();
  if (params.page) query.set('page', String(params.page));
  if (params.per_page) query.set('per_page', String(params.per_page));
  if (params.keyword) query.set('keyword', params.keyword);
  if (params.status) query.set('status', params.status);
  const qs = query.toString();
  return apiRequest<PagedResponse<AdminUser>>(`/console/api/users/${qs ? `?${qs}` : ''}`);
}

export async function fetchConsoleUserDetail(userId: number): Promise<AdminUserDetail> {
  return apiRequest<AdminUserDetail>(`/console/api/users/${userId}/`);
}

export async function updateUserQuota(
  userId: number,
  payload: { daily_quota?: number; available_quota?: number; remark?: string }
): Promise<UserQuotaInfo> {
  return apiRequest(`/console/api/users/${userId}/quota/`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function fetchConsoleTasks(params: {
  page?: number;
  per_page?: number;
  keyword?: string;
  status?: string;
} = {}): Promise<PagedResponse<AdminTask>> {
  const query = new URLSearchParams();
  if (params.page) query.set('page', String(params.page));
  if (params.per_page) query.set('per_page', String(params.per_page));
  if (params.keyword) query.set('keyword', params.keyword);
  if (params.status) query.set('status', params.status);
  const qs = query.toString();
  return apiRequest<PagedResponse<AdminTask>>(`/console/api/tasks/${qs ? `?${qs}` : ''}`);
}

export async function fetchConsoleConfig(category = ''): Promise<ConfigListResponse> {
  const qs = category ? `?category=${encodeURIComponent(category)}` : '';
  return apiRequest<ConfigListResponse>(`/console/api/config/${qs}`);
}

export async function updateConsoleSetting(
  key: string,
  payload: { value: string; is_secret?: boolean; description?: string; category?: string }
): Promise<{ key: string; updated_at: string }> {
  return apiRequest(`/console/api/config/${encodeURIComponent(key)}/`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function batchUpdateConsoleConfig(
  items: Array<{ key: string; value: string; is_secret?: boolean; description?: string; category?: string }>
): Promise<{ updated: number }> {
  return apiRequest('/console/api/config/batch/', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ items }),
  });
}

export async function fetchConsoleChannels(): Promise<ImageChannel[]> {
  const res = await apiRequest<ImageChannel[] | { items: ImageChannel[] }>('/console/api/image-channels/');
  return Array.isArray(res) ? res : (res as any)?.items || [];
}

export async function createConsoleChannel(payload: ImageChannelPayload): Promise<ImageChannel> {
  return apiRequest('/console/api/image-channels/', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function updateConsoleChannel(id: number, payload: ImageChannelPayload): Promise<ImageChannel> {
  return apiRequest(`/console/api/image-channels/${id}/`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function deleteConsoleChannel(id: number): Promise<{ deleted: boolean }> {
  return apiRequest(`/console/api/image-channels/${id}/`, { method: 'DELETE' });
}
