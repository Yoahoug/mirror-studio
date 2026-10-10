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
}

export interface AdminUser {
  id: number;
  username: string;
  email: string;
  role: string;
  status: string;
  is_staff: boolean;
  date_joined: string | null;
  quota: {
    daily_quota: number;
    available_quota: number;
    frozen_quota: number;
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

export interface ImageChannel {
  id: number;
  name: string;
  type: string;
  status: string;
  priority: number;
  base_url?: string;
  model_name?: string;
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

export async function updateUserQuota(
  userId: number,
  payload: { daily_quota?: number; available_quota?: number; remark?: string }
): Promise<any> {
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

export async function fetchConsoleChannels(): Promise<ImageChannel[]> {
  const res = await apiRequest<ImageChannel[] | { items: ImageChannel[] }>('/console/api/image-channels/');
  return Array.isArray(res) ? res : (res as any)?.items || [];
}

export async function fetchConsoleSettings(): Promise<Record<string, any>> {
  return apiRequest('/console/api/config/');
}

export async function updateConsoleSetting(key: string, value: string): Promise<any> {
  return apiRequest(`/console/api/config/${encodeURIComponent(key)}/`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ value }),
  });
}
