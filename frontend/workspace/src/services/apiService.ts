/**
 * API Service Layer – connects ArtMirror React frontend to Django backend.
 * All requests go through the Django server (same origin).
 *
 * Backend response format: { code: 0, message: "ok", data: ... }
 */

// ── Types ──

export interface UserInfo {
  username: string;
  nickname: string;
  display_name: string;
  email: string;
  avatar: string;
  remark: string;
  role: string;
  daily_quota: number;
  quota_remaining: number;
  quota_frozen: number;
  quota_total: number;
}

export interface AccountSettings {
  user: {
    username: string;
    nickname: string;
    email: string;
    avatar: string;
    remark: string;
    role: string;
    status: string;
    date_joined: string | null;
    last_login_at: string | null;
    last_login_ip: string;
    register_source: string;
  };
  quota: {
    daily_quota: number;
    available_quota: number;
    frozen_quota: number;
    total_generated_count: number;
    last_reset_date: string | null;
  };
}

export interface ChatModel {
  id: number;
  name: string;
  channel_code: string;
  provider_name: string;
  model_name: string;
  configured: boolean;
}

export interface ChatMessage {
  role: "user" | "model";
  content: string;
  image?: string;
}

export interface GenerationPayload {
  prompt: string;
  negative_prompt?: string;
  style_code?: string;
  size_code?: string;
  generation_mode?: "text_to_image" | "image_to_image";
  reference_image_name?: string;
  reference_image_data_url?: string;
  reference_upload_token?: string;
  topic_id?: number;
  client_request_id?: string;
  wait_seconds?: number;
}

export interface ReferenceUploadPayload {
  reference_image_name?: string;
  reference_image_data_url: string;
}

export interface ReferenceUploadResult {
  reference_upload_token: string;
  reference_image_name: string;
  reference_image_attached: boolean;
}

export type UploadKind = "generation_reference_image" | "novel_epub";

export interface UploadSession {
  upload_id: string;
  kind: UploadKind;
  filename: string;
  content_type: string;
  total_size: number;
  chunk_size: number;
  total_chunks: number;
  uploaded_chunks: number[];
  status: "uploading" | "completed" | "canceled" | "expired";
  expires_at?: string | null;
  result?: Record<string, any>;
}

export interface UploadProgress {
  loaded: number;
  total: number;
  percent: number;
  uploadedChunks: number;
  totalChunks: number;
  status: "preparing" | "uploading" | "completing" | "completed";
}

export type CompletedUploadResult =
  | { kind: "generation_reference_image"; session: UploadSession; result: ReferenceUploadResult }
  | { kind: "novel_epub"; session: UploadSession; result: NovelBook };

interface UploadFileResumableOptions {
  clientUploadId?: string;
  signal?: AbortSignal;
  onSession?: (session: UploadSession) => void;
  onProgress?: (progress: UploadProgress) => void;
}

// ── Helpers ──

function getCsrfToken(): string {
  const match = document.cookie.match(/csrftoken=([^;]+)/);
  if (match?.[1]) return decodeURIComponent(match[1]);
  const el = document.getElementById("workspace-root");
  return el?.dataset.csrf || "";
}

function getBaseHeaders(): Record<string, string> {
  return {
    "Content-Type": "application/json",
    "X-CSRFToken": getCsrfToken(),
  };
}

interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

export class AuthRequiredError extends Error {
  constructor(message = "请先登录或注册") {
    super(message);
    this.name = "AuthRequiredError";
  }
}

async function apiRequest<T>(
  url: string,
  options: RequestInit = {}
): Promise<T> {
  const headers = {
    ...getBaseHeaders(),
    ...((options.headers as Record<string, string>) || {}),
  };
  const res = await fetch(url, {
    credentials: "same-origin",
    ...options,
    headers,
  });

  const redirectedUrl = res.redirected ? new URL(res.url, window.location.origin) : null;
  const redirectedPath = redirectedUrl?.pathname || "";
  const redirectedAuth = redirectedUrl?.searchParams.get("auth") || "";
  if (res.status === 401 || redirectedPath === "/workspace" || redirectedAuth === "login") {
    throw new AuthRequiredError();
  }

  const contentType = res.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    throw new Error(res.ok ? "Invalid response" : `Request failed: ${res.status}`);
  }

  const body: ApiResponse<T> = await res.json().catch(() => ({ code: -1, message: "Invalid response", data: null as any }));

  if (!res.ok || body.code !== 0) {
    throw new Error(body.message || `Request failed: ${res.status}`);
  }

  return body.data;
}

// ── User / Auth ──

export interface RecentTaskSummary {
  task_no: string;
  status: string;
  created_at: string | null;
}

export interface AccountSummaryResponse {
  user: {
    username: string;
    nickname: string;
    email: string;
    avatar: string;
    remark: string;
  };
  quota: {
    daily_quota: number;
    available_quota: number;
    frozen_quota: number;
    total_generated_count: number;
    last_reset_date: string | null;
  };
  recent_tasks: RecentTaskSummary[];
}

export async function fetchAccountSummary(): Promise<AccountSummaryResponse> {
  return apiRequest<AccountSummaryResponse>("/api/account/summary");
}

export async function fetchUserInfo(): Promise<UserInfo> {
  const data = await fetchAccountSummary();
  return {
    username: data.user.username,
    nickname: data.user.nickname,
    display_name: data.user.nickname || data.user.username,
    email: data.user.email,
    avatar: data.user.avatar,
    remark: data.user.remark,
    role: "用户",
    daily_quota: data.quota.daily_quota,
    quota_remaining: data.quota.available_quota,
    quota_frozen: data.quota.frozen_quota,
    quota_total: Math.max(data.quota.daily_quota, data.quota.available_quota + data.quota.frozen_quota),
  };
}

export async function fetchAccountSettings(): Promise<AccountSettings> {
  return apiRequest<AccountSettings>("/api/account/settings");
}

export async function updateAccountSettings(payload: {
  nickname: string;
  email: string;
  avatar: string;
  remark: string;
}): Promise<AccountSettings> {
  return apiRequest<AccountSettings>("/api/account/settings", {
    method: "PATCH",
    body: JSON.stringify(payload),
  });
}

export async function updateAccountPassword(payload: {
  current_password: string;
  new_password: string;
  confirm_password: string;
}): Promise<void> {
  await apiRequest("/api/account/password", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function fetchLoginCaptcha(): Promise<{ captcha_code: string; captcha_svg: string }> {
  return apiRequest<{ captcha_code: string; captcha_svg: string }>("/api/auth/captcha");
}

export async function loginAccount(payload: {
  username: string;
  password: string;
  captcha_code: string;
}): Promise<{ username: string }> {
  return apiRequest<{ username: string }>("/api/auth/login", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function sendRegisterEmailCode(email: string, username: string): Promise<{
  expire_seconds: number;
  resend_seconds: number;
}> {
  return apiRequest<{
    expire_seconds: number;
    resend_seconds: number;
  }>("/api/auth/register/email-code", {
    method: "POST",
    body: JSON.stringify({ email, username }),
  });
}

export async function checkRegisterAvailability(payload: {
  username?: string;
  email?: string;
}): Promise<{
  username?: { available: boolean; message: string };
  email?: { available: boolean; message: string };
}> {
  return apiRequest<{
    username?: { available: boolean; message: string };
    email?: { available: boolean; message: string };
  }>("/api/auth/register/check", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function registerAccount(payload: {
  username: string;
  email: string;
  email_verification_code: string;
  password: string;
  confirm_password: string;
}): Promise<{ username: string }> {
  return apiRequest<{ username: string }>("/api/auth/register", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function logout(): Promise<void> {
  await fetch("/api/auth/logout", {
    method: "POST",
    credentials: "same-origin",
    headers: getBaseHeaders(),
  });
  window.location.href = "/workspace?auth=login";
}

// ── AI Chat (proxied through Django backend) ──

export async function chatWithAI(
  prompt: string,
  history: { role: "user" | "model"; parts: { text: string }[] }[] = [],
  channelCode: string = ""
): Promise<string> {
  try {
    const res = await apiRequest<{ reply: string }>("/api/chat", {
      method: "POST",
      body: JSON.stringify({
        prompt,
        history,
        channel_code: channelCode,
      }),
    });
    return res.reply;
  } catch (error: any) {
    if (error instanceof AuthRequiredError) {
      throw error;
    }
    throw new Error(error.message || "AI 服务暂时不可用");
  }
}

export async function fetchChatModels(): Promise<ChatModel[]> {
  return apiRequest<ChatModel[]>("/api/chat/models");
}

// ── Generation Tasks ──

export interface GenerationTask {
  task_no: string;
  task_kind?: "generation" | "novelviz";
  novel_task_id?: number;
  status: string;
  progress_percent: number;
  generation_mode: "text_to_image" | "image_to_image" | "novel_visualization";
  prompt: string;
  negative_prompt?: string;
  style_code?: string;
  size_code?: string;
  reference_image_name?: string;
  reference_image_attached?: boolean;
  reference_image_data_url?: string;
  quota_cost?: number;
  error_message?: string;
  scene_mode?: "auto" | "targeted";
  target_plot?: string;
  submitted_at?: string;
  started_at?: string | null;
  finished_at?: string | null;
  images: Array<{
    id: number;
    status: string;
    width: number;
    height: number;
    expires_at?: string | null;
    preview_url?: string;
    download_url?: string;
    image_url?: string;
    scene_number?: number;
    title?: string;
    chapter?: string;
    story_significance?: string;
    excerpt?: string;
  }>;
}

export interface PaginationMeta {
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
  has_next: boolean;
  has_previous: boolean;
}

export interface PaginatedTasks {
  items: GenerationTask[];
  pagination: PaginationMeta;
}

export async function createGenerationTask(payload: GenerationPayload): Promise<GenerationTask> {
  return apiRequest<GenerationTask>("/api/generate/tasks", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function uploadGenerationReferenceImage(payload: ReferenceUploadPayload): Promise<ReferenceUploadResult> {
  return apiRequest<ReferenceUploadResult>("/api/generate/reference-images", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function createOrResumeUploadSession(payload: {
  kind: UploadKind;
  filename: string;
  content_type: string;
  total_size: number;
  client_upload_id: string;
}): Promise<UploadSession> {
  return apiRequest<UploadSession>("/api/uploads/sessions", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function fetchUploadSession(uploadId: string): Promise<UploadSession> {
  return apiRequest<UploadSession>(`/api/uploads/sessions/${uploadId}`);
}

export async function uploadSessionChunk(uploadId: string, index: number, chunk: Blob): Promise<UploadSession> {
  return apiRequest<UploadSession>(`/api/uploads/sessions/${uploadId}/chunks/${index}`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/octet-stream",
    },
    body: chunk,
  });
}

export async function completeUploadSession(uploadId: string): Promise<UploadSession> {
  return apiRequest<UploadSession>(`/api/uploads/sessions/${uploadId}/complete`, {
    method: "POST",
  });
}

export async function cancelUploadSession(uploadId: string): Promise<UploadSession> {
  return apiRequest<UploadSession>(`/api/uploads/sessions/${uploadId}`, {
    method: "DELETE",
  });
}

function buildClientUploadId(kind: UploadKind, file: File): string {
  return [
    kind,
    file.name,
    file.size,
    file.type || "application/octet-stream",
    file.lastModified || 0,
  ].join(":");
}

async function waitBeforeRetry(attempt: number): Promise<void> {
  await new Promise((resolve) => window.setTimeout(resolve, Math.min(800 * attempt, 3200)));
}

async function uploadChunkWithRetry(uploadId: string, index: number, chunk: Blob, signal?: AbortSignal): Promise<UploadSession> {
  let lastError: unknown = null;
  for (let attempt = 1; attempt <= 5; attempt += 1) {
    if (signal?.aborted) throw new DOMException("Upload canceled", "AbortError");
    try {
      return await uploadSessionChunk(uploadId, index, chunk);
    } catch (error) {
      lastError = error;
      if (attempt >= 5) break;
      await waitBeforeRetry(attempt);
    }
  }
  throw lastError instanceof Error ? lastError : new Error("分片上传失败");
}

export function uploadFileResumable(
  kind: "generation_reference_image",
  file: File,
  options?: UploadFileResumableOptions,
): Promise<{ kind: "generation_reference_image"; session: UploadSession; result: ReferenceUploadResult }>;
export function uploadFileResumable(
  kind: "novel_epub",
  file: File,
  options?: UploadFileResumableOptions,
): Promise<{ kind: "novel_epub"; session: UploadSession; result: NovelBook }>;
export async function uploadFileResumable(
  kind: UploadKind,
  file: File,
  options: UploadFileResumableOptions = {},
): Promise<CompletedUploadResult> {
  const total = file.size;
  options.onProgress?.({
    loaded: 0,
    total,
    percent: 0,
    uploadedChunks: 0,
    totalChunks: 0,
    status: "preparing",
  });
  let session = await createOrResumeUploadSession({
    kind,
    filename: file.name || (kind === "novel_epub" ? "novel.epub" : "reference-image.png"),
    content_type: file.type || "application/octet-stream",
    total_size: file.size,
    client_upload_id: options.clientUploadId || buildClientUploadId(kind, file),
  });
  options.onSession?.(session);
  const uploaded = new Set(session.uploaded_chunks || []);
  const report = (status: UploadProgress["status"]) => {
    const loaded = Math.min(uploaded.size * session.chunk_size, total);
    options.onProgress?.({
      loaded,
      total,
      percent: total ? Math.min(100, Math.round((loaded / total) * 100)) : 0,
      uploadedChunks: uploaded.size,
      totalChunks: session.total_chunks,
      status,
    });
  };
  report(session.status === "completed" ? "completed" : "uploading");

  if (session.status !== "completed") {
    for (let index = 0; index < session.total_chunks; index += 1) {
      if (uploaded.has(index)) continue;
      if (options.signal?.aborted) throw new DOMException("Upload canceled", "AbortError");
      const start = index * session.chunk_size;
      const chunk = file.slice(start, Math.min(start + session.chunk_size, file.size));
      session = await uploadChunkWithRetry(session.upload_id, index, chunk, options.signal);
      options.onSession?.(session);
      (session.uploaded_chunks || []).forEach((item) => uploaded.add(item));
      report("uploading");
    }
    report("completing");
    session = await completeUploadSession(session.upload_id);
    options.onSession?.(session);
  }

  const result = (session.result || {}) as any;
  report("completed");
  if (kind === "generation_reference_image") {
    return { kind, session, result: result as ReferenceUploadResult };
  }
  return { kind, session, result: result as NovelBook };
}

export async function fetchTasks(params: {
  topicId?: string;
  page?: number;
  pageSize?: number;
  query?: string;
} = {}): Promise<PaginatedTasks> {
  const search = new URLSearchParams();
  if (params.topicId) search.set("topic_id", params.topicId);
  if (params.page) search.set("page", String(params.page));
  if (params.pageSize) search.set("page_size", String(params.pageSize));
  if (params.query?.trim()) search.set("q", params.query.trim());
  const queryString = search.toString();
  return apiRequest<PaginatedTasks>(`/api/workspace/history${queryString ? `?${queryString}` : ""}`);
}

export async function fetchTaskDetail(taskNo: string): Promise<GenerationTask> {
  return apiRequest<GenerationTask>(`/api/generate/tasks/${taskNo}`);
}

export async function fetchImageDownloadUrl(imageId: number): Promise<{ url: string; preview_url?: string }> {
  return apiRequest<{ url: string; preview_url?: string }>(`/api/images/${imageId}/download-url`, { method: "POST" });
}

export async function cancelTask(taskNo: string): Promise<void> {
  await apiRequest<any>(`/api/generate/tasks/${taskNo}/cancel`, { method: "POST" });
}

export async function deleteGenerationTask(taskNo: string): Promise<void> {
  await apiRequest<any>(`/api/generate/tasks/${taskNo}`, { method: "DELETE" });
}

export async function deleteNovelTask(taskId: number): Promise<void> {
  await apiRequest<any>(`/api/novelviz/tasks/${taskId}`, { method: "DELETE" });
}

// ── Novel Scene Visualization ──

export interface NovelBook {
  id: string;
  source: "builtin" | "uploaded";
  filename: string;
  display_name: string;
  size: number;
  uploaded_book_id?: number | null;
  created_at?: string | null;
}

export interface NovelSceneImage {
  id: number;
  scene_number: number;
  title: string;
  chapter: string;
  story_significance: string;
  excerpt: string;
  visual_prompt: string;
  status: "pending" | "running" | "succeeded" | "failed";
  error_message: string;
  image_url: string;
  preview_url?: string;
  download_url?: string;
  attempts: number;
  created_at?: string | null;
  finished_at?: string | null;
}

export interface NovelVisualizationTask {
  id: number;
  book_filename: string;
  book_source: "builtin" | "uploaded";
  uploaded_book_id?: number | null;
  scene_mode: "auto" | "targeted";
  target_plot: string;
  status: "queued" | "running" | "succeeded" | "failed" | "canceled";
  progress_percent: number;
  error_message: string;
  scenes: Array<Record<string, any>>;
  images: NovelSceneImage[];
  logs: Array<{
    id: number;
    type: string;
    payload: Record<string, any>;
    created_at: string;
  }>;
  created_at?: string | null;
  started_at?: string | null;
  finished_at?: string | null;
}

export async function fetchNovelBooks(): Promise<NovelBook[]> {
  return apiRequest<NovelBook[]>("/api/novelviz/books");
}

export async function uploadNovelBook(file: File): Promise<NovelBook> {
  const formData = new FormData();
  formData.append("file", file);
  const res = await fetch("/api/novelviz/books/upload", {
    method: "POST",
    credentials: "same-origin",
    headers: {
      "X-CSRFToken": getCsrfToken(),
    },
    body: formData,
  });

  const redirectedUrl = res.redirected ? new URL(res.url, window.location.origin) : null;
  const redirectedPath = redirectedUrl?.pathname || "";
  const redirectedAuth = redirectedUrl?.searchParams.get("auth") || "";
  if (res.status === 401 || redirectedPath === "/workspace" || redirectedAuth === "login") {
    throw new AuthRequiredError();
  }

  const contentType = res.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    throw new Error(res.ok ? "Invalid response" : `Request failed: ${res.status}`);
  }

  const body: ApiResponse<NovelBook> = await res.json().catch(() => ({ code: -1, message: "Invalid response", data: null as any }));
  if (!res.ok || body.code !== 0) {
    throw new Error(body.message || `Request failed: ${res.status}`);
  }
  return body.data;
}

export async function fetchNovelTasks(): Promise<NovelVisualizationTask[]> {
  return apiRequest<NovelVisualizationTask[]>("/api/novelviz/tasks");
}

export async function createNovelTask(
  book: NovelBook,
  llmChannelCode = "",
  options: { sceneMode?: "auto" | "targeted"; targetPlot?: string } = {},
): Promise<NovelVisualizationTask> {
  return apiRequest<NovelVisualizationTask>("/api/novelviz/tasks", {
    method: "POST",
    body: JSON.stringify({
      book_source: book.source,
      book_filename: book.filename,
      uploaded_book_id: book.uploaded_book_id,
      llm_channel_code: llmChannelCode,
      scene_mode: options.sceneMode || "auto",
      target_plot: options.targetPlot || "",
    }),
  });
}

export async function fetchNovelTaskDetail(taskId: number): Promise<NovelVisualizationTask> {
  return apiRequest<NovelVisualizationTask>(`/api/novelviz/tasks/${taskId}`);
}

export async function retryNovelTask(taskId: number): Promise<NovelVisualizationTask> {
  return apiRequest<NovelVisualizationTask>(`/api/novelviz/tasks/${taskId}/retry`, {
    method: "POST",
  });
}
