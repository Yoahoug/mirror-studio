import type { UnifiedTask } from '../types/common';
import { apiRequest } from './client';

export interface ImageTaskPayload {
  prompt: string;
  negative_prompt?: string;
  size_code?: string;
  style_code?: string;
  quality_code?: string;
  reference_upload_token?: string;
  image_model_id?: string;
}

export interface ImageModel {
  id: string;
  name: string;
  description?: string;
  supports_reference?: boolean;
}

export async function fetchImageModels(): Promise<ImageModel[]> {
  const res = await apiRequest<{ models: ImageModel[] }>('/api/generator/models');
  return res?.models || [];
}

export async function createImageTask(payload: ImageTaskPayload): Promise<{ task_no: string }> {
  return apiRequest('/api/generator/tasks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function fetchTaskDetail(taskNo: string, signal?: AbortSignal): Promise<UnifiedTask> {
  return apiRequest<UnifiedTask>(`/api/generator/tasks/${encodeURIComponent(taskNo)}`, {
    signal,
  });
}

export async function fetchHistoryTasks(page: number = 1, pageSize: number = 20): Promise<{
  items: UnifiedTask[];
  total: number;
  has_more: boolean;
}> {
  return apiRequest(`/api/generator/tasks?page=${page}&page_size=${pageSize}`);
}

export async function cancelTask(taskNo: string): Promise<void> {
  await apiRequest(`/api/generator/tasks/${encodeURIComponent(taskNo)}/cancel`, {
    method: 'POST',
  });
}

export async function deleteTask(taskNo: string): Promise<void> {
  await apiRequest(`/api/generator/tasks/${encodeURIComponent(taskNo)}`, {
    method: 'DELETE',
  });
}
