/**
 * mirror-studio 统一类型定义
 */

export type AppMode = 'online' | 'local';

export type ThemeMode = 'light' | 'dark' | 'system';

export type Language = 'zh' | 'en';

export type TaskStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled';

export interface TaskOutput {
  id: string;
  name: string;
  kind?: string;
  sr?: number | null;
  duration_s?: number | null;
  size?: number | null;
  audio_url?: string;
  preview_url: string;
  download_url: string;
  image_url?: string;
}

export interface UnifiedTask {
  task_no: string;
  task_kind: 'image' | 'comfy' | 'tts' | 'novelviz';
  status: TaskStatus;
  progress_percent: number;
  prompt: string;
  negative_prompt?: string;
  style_code?: string;
  size_code?: string;
  quality_code?: string;
  engine?: string;
  engine_label?: string;
  mode?: string;
  reference_image_name?: string;
  reference_image_attached?: boolean;
  quota_cost?: number;
  error_code?: string;
  error_message?: string;
  outputs?: TaskOutput[];
  images?: TaskOutput[];
  submitted_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface UserSummary {
  username: string;
  email?: string;
  isAuthenticated: boolean;
  isStaff?: boolean;
  availableQuota?: number;
  dailyQuota?: number;
}
