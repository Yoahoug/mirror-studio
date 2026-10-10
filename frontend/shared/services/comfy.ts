import type { UnifiedTask } from '../types/common';
import { apiRequest } from './client';

export interface ComfyStatus {
  running: boolean;
  base_url: string;
  has_start_cmd: boolean;
  start_cmd_hint?: string;
  models: { id: string; name: string; description?: string }[];
  devices?: { id: number; name: string; vram_free_mb: number; vram_total_mb: number }[];
  stats?: any;
}

export interface ComfyTaskPayload {
  prompt: string;
  negative_prompt?: string;
  model: string;
  aspect_ratio?: string;
  reference_upload_token?: string;
  free_memory_after?: boolean;
}

export async function fetchComfyStatus(): Promise<ComfyStatus> {
  return apiRequest<ComfyStatus>('/api/comfy/status');
}

export async function startComfy(): Promise<{ message: string }> {
  return apiRequest('/api/comfy/start', { method: 'POST' });
}

export async function releaseComfyMemory(): Promise<{ message: string }> {
  return apiRequest('/api/comfy/release', { method: 'POST' });
}

export async function fetchComfyLog(limit: number = 30): Promise<{
  available: boolean;
  lines: string[];
  issues?: string[];
}> {
  return apiRequest(`/api/comfy/log?limit=${limit}`);
}

export async function createComfyTask(payload: ComfyTaskPayload): Promise<{ task_no: string }> {
  return apiRequest('/api/comfy/tasks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function fetchComfyTask(taskNo: string, signal?: AbortSignal): Promise<UnifiedTask> {
  return apiRequest<UnifiedTask>(`/api/comfy/tasks/${encodeURIComponent(taskNo)}`, {
    signal,
  });
}
