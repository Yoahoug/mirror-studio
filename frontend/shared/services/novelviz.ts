import { apiRequest, getCsrfToken } from './client';

export interface NovelSceneImage {
  id: string;
  scene_id: string;
  title: string;
  prompt: string;
  status: string;
  preview_url: string;
  download_url: string;
}

export interface NovelTaskDetail {
  id: number;
  status: string;
  book_title: string;
  scene_mode: string;
  scenes_count: number;
  images: NovelSceneImage[];
  error_message?: string;
  created_at: string;
}

export async function uploadNovelEpub(file: File): Promise<{
  book_source: string;
  book_filename: string;
  uploaded_book_id: string;
  display_name: string;
}> {
  const formData = new FormData();
  formData.append('file', file, file.name);

  const csrf = getCsrfToken();
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (csrf) headers['X-CSRFToken'] = csrf;

  const res = await fetch('/api/novelviz/books/upload', {
    method: 'POST',
    headers,
    body: formData,
    credentials: 'same-origin',
  });

  const payload = await res.json();
  if (payload.code !== 0) {
    throw new Error(payload.message || '上传电子书失败');
  }
  return {
    book_source: payload.data.source,
    book_filename: payload.data.filename,
    uploaded_book_id: payload.data.uploaded_book_id,
    display_name: payload.data.display_name,
  };
}

export async function createNovelTask(payload: {
  book_source: string;
  book_filename: string;
  uploaded_book_id: string;
  scene_mode?: string;
  target_plot?: string;
}): Promise<{ id: number }> {
  return apiRequest('/api/novelviz/tasks', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function fetchNovelTask(taskId: number): Promise<NovelTaskDetail> {
  return apiRequest<NovelTaskDetail>(`/api/novelviz/tasks/${taskId}`);
}

export async function fetchNovelTasks(): Promise<{ items: NovelTaskDetail[] }> {
  return apiRequest('/api/novelviz/tasks');
}
