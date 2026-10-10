import type { UnifiedTask } from '../types/common';
import { apiRequest } from './client';

export interface TtsEngineSpec {
  id: string;
  label: string;
  capabilities: string[];
  model?: string;
  output_sr?: number | null;
  ref_audio?: { min_s?: number; max_s?: number; hard?: boolean; recommended?: string } | null;
  speakers?: string[] | null;
  init_params?: Record<string, any>;
  request_params?: Record<string, any>;
  running?: boolean;
}

export interface TtsVoice {
  name: string;
  source: string;
  created_at: string;
  instruct?: string;
  ref_text?: string;
  language?: string;
  audio_url?: string;
}

export interface TtsStatus {
  enabled: boolean;
  cli: { python: string; script: string };
  engines: TtsEngineSpec[];
  instance: { engine: string; pid: number; started_at: string } | null;
  gpu: { name: string; vram_total_mb: number; vram_free_mb: number } | null;
  voices: TtsVoice[];
  gptsovits_voices?: string[];
  error?: string;
}

export interface TtsTaskPayload {
  mode: 'design' | 'say' | 'clone' | 'preset';
  text: string;
  engine?: string;
  voice?: string;
  instruct?: string;
  speaker?: string;
  save_as?: string;
  ref_token?: string;
  ref_text?: string;
  language?: string;
  params?: Record<string, any>;
}

export async function fetchTtsStatus(): Promise<TtsStatus> {
  return apiRequest<TtsStatus>('/api/tts/status');
}

export async function fetchTtsVoices(): Promise<{ voices: TtsVoice[]; gptsovits_voices?: string[] }> {
  return apiRequest('/api/tts/voices');
}

export async function createTtsTask(payload: TtsTaskPayload): Promise<{ task_no: string }> {
  return apiRequest('/api/tts/tasks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function fetchTtsTask(taskNo: string, signal?: AbortSignal): Promise<UnifiedTask> {
  return apiRequest<UnifiedTask>(`/api/tts/tasks/${encodeURIComponent(taskNo)}`, {
    signal,
  });
}

export async function cancelTtsTask(taskNo: string): Promise<void> {
  await apiRequest(`/api/tts/tasks/${encodeURIComponent(taskNo)}/cancel`, { method: 'POST' });
}

export async function releaseTtsMemory(): Promise<{ message: string }> {
  return apiRequest('/api/tts/release', { method: 'POST' });
}

export async function startTtsEngine(engine: string): Promise<{ message: string }> {
  return apiRequest('/api/tts/start', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ engine }),
  });
}

export async function stopTts(): Promise<{ message: string }> {
  return apiRequest('/api/tts/stop', { method: 'POST' });
}

export async function fetchTtsLog(engine: string, lines: number = 30): Promise<{ lines: string[] }> {
  return apiRequest(`/api/tts/log?engine=${encodeURIComponent(engine)}&lines=${lines}`);
}
