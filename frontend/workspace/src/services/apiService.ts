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
  quality_code?: "auto" | "low" | "medium" | "high";
  generation_mode?: "text_to_image" | "image_to_image";
  reference_image_name?: string;
  reference_image_data_url?: string;
  reference_upload_token?: string;
  topic_id?: number;
  client_request_id?: string;
  wait_seconds?: number;
  /** 仅本地开发者模式：engine="comfy" 时任务走本机 ComfyUI（对应「Comfy 生图」板块）。 */
  engine?: "comfy";
  comfy_model?: string;
  seed?: number;
  steps?: number;
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

export interface TtsRefAudioUploadResult {
  reference_upload_token: string;
  reference_audio_name: string;
  reference_audio_attached: boolean;
}

export type UploadKind = "generation_reference_image" | "novel_epub" | "tts_ref_audio";

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
  | { kind: "novel_epub"; session: UploadSession; result: NovelBook }
  | { kind: "tts_ref_audio"; session: UploadSession; result: TtsRefAudioUploadResult };

interface UploadFileResumableOptions {
  clientUploadId?: string;
  signal?: AbortSignal;
  onSession?: (session: UploadSession) => void;
  onProgress?: (progress: UploadProgress) => void;
}

const UPLOAD_MAX_ATTEMPTS = 8;
const UPLOAD_REQUEST_TIMEOUT_MS = 90000;
const UPLOAD_RETRYABLE_STATUS_RE = /Request failed:\s*(408|409|425|429|500|502|503|504|520|522|523|524)/i;
const CSRF_FAILURE_RE = /csrf|CSRF|CSRF验证失败|CSRF 验证失败|403/i;

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

function setDomCsrfToken(token: string) {
  const el = document.getElementById("workspace-root");
  if (el && token) el.dataset.csrf = token;
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

class CsrfRequiredError extends Error {
  constructor(message = "CSRF 验证失败，请刷新后重试") {
    super(message);
    this.name = "CsrfRequiredError";
  }
}

function isCsrfFailure(status: number, contentType: string, bodyText: string) {
  return status === 403 && !contentType.includes("application/json") && CSRF_FAILURE_RE.test(bodyText);
}

export async function refreshCsrfToken(): Promise<string> {
  const res = await fetch("/api/auth/csrf", {
    method: "GET",
    credentials: "same-origin",
    headers: {
      "X-Requested-With": "XMLHttpRequest",
    },
  });
  const contentType = res.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    throw new Error("CSRF token refresh failed");
  }
  const body: ApiResponse<{ csrf_token: string }> = await res.json().catch(() => ({
    code: -1,
    message: "Invalid response",
    data: { csrf_token: "" },
  }));
  if (!res.ok || body.code !== 0 || !body.data?.csrf_token) {
    throw new Error(body.message || "CSRF token refresh failed");
  }
  setDomCsrfToken(body.data.csrf_token);
  return body.data.csrf_token;
}

async function apiRequest<T>(
  url: string,
  options: RequestInit = {},
  csrfRetry = true,
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
    const bodyText = await res.text().catch(() => "");
    if (csrfRetry && isCsrfFailure(res.status, contentType, bodyText)) {
      await refreshCsrfToken();
      return apiRequest<T>(url, options, false);
    }
    if (isCsrfFailure(res.status, contentType, bodyText)) {
      throw new CsrfRequiredError();
    }
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

// ── 对话中转站（仅本地开发者模式：工作台悬浮窗里配置；线上 Go 服务没有这些接口）──

export type ChatReasoningEffort = "off" | "low" | "medium" | "high";

export interface ChatProviderConfig {
  base_url: string;
  api_path: string;
  model: string;
  reasoning_effort: ChatReasoningEffort;
  temperature: number;
  custom_instruction: string;
  configured: boolean;
  source: "workbench" | "env" | "none";
  has_key: boolean;
  key_hint: string;
  efforts: ChatReasoningEffort[];
}

export interface ChatProviderPatch {
  base_url?: string;
  model?: string;
  api_key?: string;
  clear_api_key?: boolean;
  reasoning_effort?: ChatReasoningEffort;
  temperature?: number;
  custom_instruction?: string;
}

export async function fetchChatConfig(): Promise<ChatProviderConfig> {
  return apiRequest<ChatProviderConfig>("/api/chat/config");
}

/** 保存中转站配置；api_key 留空表示沿用已保存的密钥。 */
export async function saveChatConfig(patch: ChatProviderPatch): Promise<ChatProviderConfig> {
  return apiRequest<ChatProviderConfig>("/api/chat/config", {
    method: "PUT",
    body: JSON.stringify(patch),
  });
}

/** 让服务端代问上游 /v1/models：测试连通 + 取模型清单（可带未保存的地址/密钥先试）。 */
export async function fetchChatRelayModels(
  input: { base_url?: string; api_key?: string } = {}
): Promise<string[]> {
  const res = await apiRequest<{ models: string[] }>("/api/chat/models", {
    method: "POST",
    body: JSON.stringify(input),
  });
  return res.models;
}

/**
 * 悬浮窗对话的流式接口：SSE 逐片回调，思考过程在 delta.reasoning_content。
 * 服务端会注入生图提示词工程师的 system prompt，这里只发 user/assistant 历史。
 */
export async function streamChat(
  messages: { role: "user" | "assistant"; content: string }[],
  handlers: { onDelta?: (delta: { content?: string; reasoning?: string }) => void },
  signal?: AbortSignal
): Promise<void> {
  const res = await fetch("/api/chat/stream", {
    method: "POST",
    credentials: "same-origin",
    headers: getBaseHeaders(),
    body: JSON.stringify({ messages }),
    signal,
  });
  if (!res.ok) {
    let message = `HTTP ${res.status}`;
    try {
      const payload = await res.json();
      message = payload?.message || payload?.error || message;
    } catch {
      /* 保留 HTTP 状态文案 */
    }
    throw new Error(message);
  }
  const reader = res.body?.getReader();
  if (!reader) throw new Error("响应没有内容流");
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() || "";
    for (const line of lines) {
      const trimmed = line.trim();
      if (!trimmed.startsWith("data:")) continue;
      const payload = trimmed.slice(5).trim();
      if (!payload || payload === "[DONE]") continue;
      let parsed: any;
      try {
        parsed = JSON.parse(payload);
      } catch {
        continue;
      }
      if (typeof parsed?.error === "string" && parsed.error) throw new Error(parsed.error);
      const delta = parsed?.choices?.[0]?.delta;
      if (!delta) continue;
      const reasoning = typeof delta.reasoning_content === "string" ? delta.reasoning_content : "";
      const content = typeof delta.content === "string" ? delta.content : "";
      if (reasoning || content) handlers.onDelta?.({ reasoning, content });
    }
  }
}

// ── Generation Tasks ──

export interface GenerationTask {
  task_no: string;
  task_kind?: "generation" | "novelviz" | "tts";
  novel_task_id?: number;
  status: string;
  progress_percent: number;
  generation_mode: "text_to_image" | "image_to_image" | "novel_visualization";
  prompt: string;
  /** 仅 TTS 历史条目（task_kind="tts"）携带：合成产物音频列表。 */
  outputs?: Array<{ id: string; name: string; kind: string; sr: number | null; duration_s: number | null; preview_url: string; download_url: string }>;
  negative_prompt?: string;
  style_code?: string;
  size_code?: string;
  quality_code?: "auto" | "low" | "medium" | "high";
  /** 仅本地开发者模式：engine="comfy" 表示任务由本机 ComfyUI 执行。 */
  engine?: string;
  comfy_model?: string;
  /** 仅本地开发者模式：出图渠道名；placeholder=true 表示零配置渠道出的渐变占位图。 */
  channel_name?: string;
  placeholder?: boolean;
  steps?: number | null;
  seed?: number | null;
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
}, signal?: AbortSignal): Promise<UploadSession> {
  return apiRequest<UploadSession>("/api/uploads/sessions", {
    method: "POST",
    body: JSON.stringify(payload),
    signal,
  });
}

export async function fetchUploadSession(uploadId: string): Promise<UploadSession> {
  return apiRequest<UploadSession>(`/api/uploads/sessions/${uploadId}`);
}

async function parseApiResponse<T>(res: Response, csrfRetry: () => Promise<T>): Promise<T> {
  const redirectedUrl = res.redirected ? new URL(res.url, window.location.origin) : null;
  const redirectedPath = redirectedUrl?.pathname || "";
  const redirectedAuth = redirectedUrl?.searchParams.get("auth") || "";
  if (res.status === 401 || redirectedPath === "/workspace" || redirectedAuth === "login") {
    throw new AuthRequiredError();
  }

  const contentType = res.headers.get("content-type") || "";
  if (!contentType.includes("application/json")) {
    const bodyText = await res.text().catch(() => "");
    if (isCsrfFailure(res.status, contentType, bodyText)) {
      return csrfRetry();
    }
    throw new Error(res.ok ? "Invalid response" : `Request failed: ${res.status}`);
  }

  const body: ApiResponse<T> = await res.json().catch(() => ({ code: -1, message: "Invalid response", data: null as any }));
  if (!res.ok || body.code !== 0) {
    if (res.status === 401 || body.code === 401001) {
      throw new AuthRequiredError(body.message || "请先登录或注册");
    }
    throw new Error(body.message || `Request failed: ${res.status}`);
  }
  return body.data;
}

function uploadSessionChunkWithProgress(
  uploadId: string,
  index: number,
  chunk: Blob,
  options: {
    signal?: AbortSignal;
    onUploadProgress?: (loaded: number) => void;
    csrfRetry?: boolean;
  } = {},
): Promise<UploadSession> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    let settled = false;
    let abortHandler: (() => void) | null = null;
    const cleanup = () => {
      if (abortHandler) options.signal?.removeEventListener("abort", abortHandler);
    };
    const fail = (error: unknown) => {
      if (settled) return;
      settled = true;
      cleanup();
      reject(error);
    };

    xhr.open("PUT", `/api/uploads/sessions/${uploadId}/chunks/${index}`);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.setRequestHeader("X-CSRFToken", getCsrfToken());
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        options.onUploadProgress?.(Math.min(event.loaded, chunk.size));
      }
    };
    xhr.onerror = () => fail(new Error("Failed to fetch"));
    xhr.ontimeout = () => fail(new Error("上传等待时间较长，请保持页面打开"));
    xhr.onabort = () => fail(new DOMException("Upload canceled", "AbortError"));
    xhr.onload = () => {
      void parseApiResponse<UploadSession>(
        {
          ok: xhr.status >= 200 && xhr.status < 300,
          status: xhr.status,
          redirected: Boolean(xhr.responseURL && xhr.responseURL !== new URL(`/api/uploads/sessions/${uploadId}/chunks/${index}`, window.location.origin).href),
          url: xhr.responseURL || "",
          headers: new Headers({ "content-type": xhr.getResponseHeader("content-type") || "" }),
          json: async () => JSON.parse(xhr.responseText || "{}"),
          text: async () => xhr.responseText || "",
        } as Response,
        async () => {
          if (options.csrfRetry === false) throw new CsrfRequiredError();
          await refreshCsrfToken();
          return uploadSessionChunkWithProgress(uploadId, index, chunk, { ...options, csrfRetry: false });
        },
      )
        .then((data) => {
          if (settled) return;
          settled = true;
          cleanup();
          resolve(data);
        })
        .catch(fail);
    };
    abortHandler = () => xhr.abort();
    if (options.signal?.aborted) {
      xhr.abort();
      return;
    }
    options.signal?.addEventListener("abort", abortHandler, { once: true });
    xhr.send(chunk);
  });
}

function mergeAbortSignals(signals: Array<AbortSignal | undefined>): AbortSignal | undefined {
  const activeSignals = signals.filter(Boolean) as AbortSignal[];
  if (!activeSignals.length) return undefined;
  const controller = new AbortController();
  const abort = () => {
    if (!controller.signal.aborted) controller.abort();
  };
  activeSignals.forEach((signal) => {
    if (signal.aborted) {
      abort();
      return;
    }
    signal.addEventListener("abort", abort, { once: true });
  });
  return controller.signal;
}

async function withRequestTimeout<T>(
  request: (signal?: AbortSignal) => Promise<T>,
  signal?: AbortSignal,
  timeoutMs = UPLOAD_REQUEST_TIMEOUT_MS,
): Promise<T> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await request(mergeAbortSignals([signal, controller.signal]));
  } catch (error) {
    if (controller.signal.aborted && !signal?.aborted) {
      throw new Error("上传等待时间较长，请保持页面打开");
    }
    throw error;
  } finally {
    window.clearTimeout(timeout);
  }
}

export async function uploadSessionChunk(uploadId: string, index: number, chunk: Blob, signal?: AbortSignal): Promise<UploadSession> {
  return apiRequest<UploadSession>(`/api/uploads/sessions/${uploadId}/chunks/${index}`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/octet-stream",
    },
    body: chunk,
    signal,
  });
}

export async function completeUploadSession(uploadId: string, signal?: AbortSignal): Promise<UploadSession> {
  return apiRequest<UploadSession>(`/api/uploads/sessions/${uploadId}/complete`, {
    method: "POST",
    signal,
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

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function shouldRetryUploadError(error: unknown): boolean {
  if (isAbortError(error) || error instanceof AuthRequiredError || error instanceof CsrfRequiredError) return false;
  const message = error instanceof Error ? error.message : String(error || "");
  return (
    !message ||
    message === "Failed to fetch" ||
    message === "Load failed" ||
    message === "NetworkError when attempting to fetch resource." ||
    message === "Invalid response" ||
    message.includes("上传等待时间较长") ||
    UPLOAD_RETRYABLE_STATUS_RE.test(message)
  );
}

async function waitForNetwork(signal?: AbortSignal): Promise<void> {
  if (typeof navigator === "undefined" || navigator.onLine !== false) return;
  await new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("Upload canceled", "AbortError"));
      return;
    }
    const cleanup = () => {
      window.removeEventListener("online", handleOnline);
      signal?.removeEventListener("abort", handleAbort);
    };
    const handleOnline = () => {
      cleanup();
      resolve();
    };
    const handleAbort = () => {
      cleanup();
      reject(new DOMException("Upload canceled", "AbortError"));
    };
    window.addEventListener("online", handleOnline, { once: true });
    signal?.addEventListener("abort", handleAbort, { once: true });
  });
}

async function waitBeforeRetry(attempt: number, signal?: AbortSignal): Promise<void> {
  await waitForNetwork(signal);
  const jitter = Math.floor(Math.random() * 350);
  const delay = Math.min(900 * 2 ** Math.max(attempt - 1, 0), 12000) + jitter;
  await new Promise<void>((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("Upload canceled", "AbortError"));
      return;
    }
    const timer = window.setTimeout(resolve, delay);
    signal?.addEventListener(
      "abort",
      () => {
        window.clearTimeout(timer);
        reject(new DOMException("Upload canceled", "AbortError"));
      },
      { once: true },
    );
  });
}

async function retryUploadRequest<T>(
  request: () => Promise<T>,
  signal?: AbortSignal,
  maxAttempts = UPLOAD_MAX_ATTEMPTS,
): Promise<T> {
  let lastError: unknown = null;
  for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
    if (signal?.aborted) throw new DOMException("Upload canceled", "AbortError");
    try {
      return await request();
    } catch (error) {
      lastError = error;
      if (attempt >= maxAttempts || !shouldRetryUploadError(error)) break;
      await waitBeforeRetry(attempt, signal);
    }
  }
  throw lastError instanceof Error ? lastError : new Error("上传暂未完成，请稍后重试或重新选择文件");
}

async function uploadChunkWithRetry(
  uploadId: string,
  index: number,
  chunk: Blob,
  signal?: AbortSignal,
  onUploadProgress?: (loaded: number) => void,
): Promise<UploadSession> {
  return retryUploadRequest(
    () => withRequestTimeout(
      (requestSignal) => uploadSessionChunkWithProgress(uploadId, index, chunk, {
        signal: requestSignal,
        onUploadProgress,
      }),
      signal,
    ),
    signal,
  );
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
export function uploadFileResumable(
  kind: "tts_ref_audio",
  file: File,
  options?: UploadFileResumableOptions,
): Promise<{ kind: "tts_ref_audio"; session: UploadSession; result: TtsRefAudioUploadResult }>;
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
  await retryUploadRequest(() => withRequestTimeout(() => refreshCsrfToken(), options.signal, 15000), options.signal, 3);
  let session = await retryUploadRequest(
    () => withRequestTimeout(
      (requestSignal) => createOrResumeUploadSession({
        kind,
        filename: file.name || (kind === "novel_epub" ? "novel.epub" : "reference-image.png"),
        content_type: file.type || "application/octet-stream",
        total_size: file.size,
        client_upload_id: options.clientUploadId || buildClientUploadId(kind, file),
      }, requestSignal),
      options.signal,
      20000,
    ),
    options.signal,
  );
  options.onSession?.(session);
  const uploaded = new Set(session.uploaded_chunks || []);
  const report = (status: UploadProgress["status"], currentChunkBytes = 0) => {
    const loaded = Math.min(uploaded.size * session.chunk_size + currentChunkBytes, total);
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
      try {
        session = await uploadChunkWithRetry(
          session.upload_id,
          index,
          chunk,
          options.signal,
          (loaded) => report("uploading", loaded),
        );
      } catch (error) {
        if (!shouldRetryUploadError(error)) throw error;
        session = await retryUploadRequest(() => fetchUploadSession(session.upload_id), options.signal, 3);
        (session.uploaded_chunks || []).forEach((item) => uploaded.add(item));
        if (uploaded.has(index)) {
          report("uploading");
          continue;
        }
        throw error;
      }
      options.onSession?.(session);
      (session.uploaded_chunks || []).forEach((item) => uploaded.add(item));
      report("uploading");
    }
    report("completing");
    session = await retryUploadRequest(
      () => withRequestTimeout((requestSignal) => completeUploadSession(session.upload_id, requestSignal), options.signal, 60000),
      options.signal,
    );
    options.onSession?.(session);
  }

  const result = (session.result || {}) as any;
  report("completed");
  if (kind === "generation_reference_image") {
    return { kind, session, result: result as ReferenceUploadResult };
  }
  if (kind === "tts_ref_audio") {
    return { kind, session, result: result as TtsRefAudioUploadResult };
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

// ── Comfy Local Generation（仅本地开发者模式：线上 Go 服务没有这些接口）──

export interface ComfyModelSpec {
  id: string;
  label: string;
  note: string;
  unet: string;
  steps_mode: "fixed" | "selectable";
  default_steps: number | null;
  max_steps: number | null;
}

export interface ComfyStats {
  comfyui_version: string;
  ram_total: number;
  ram_free: number;
  devices: Array<{ name: string; vram_total: number; vram_free: number }>;
}

export interface ComfyStatus {
  base_url: string;
  running: boolean;
  error: string;
  stats: ComfyStats | null;
  start_command_configured: boolean;
  timeout_ms: number;
  free_memory: boolean;
  models: ComfyModelSpec[];
}

export interface ComfyStartResult {
  started: boolean;
  already_running: boolean;
  status: ComfyStatus;
  /** 启动后立刻从 ComfyUI 日志里抽到的异常摘要（没有则为空字符串）。 */
  comfy_issues?: string;
}

export interface ComfyIssue {
  level: "error" | "warning";
  message: string;
  at: string;
}

export interface ComfyLogReport {
  available: boolean;
  total_lines: number;
  errors: number;
  warnings: number;
  issues: ComfyIssue[];
  error?: string;
}

export async function fetchComfyStatus(): Promise<ComfyStatus> {
  return apiRequest<ComfyStatus>("/api/comfy/status");
}

/** 读取 ComfyUI 服务日志里最近的 ERROR / WARNING（自定义节点、Manager 联网失败等）。 */
export async function fetchComfyLog(limit = 8): Promise<ComfyLogReport> {
  return apiRequest<ComfyLogReport>(`/api/comfy/log?limit=${encodeURIComponent(String(limit))}`);
}

export async function startComfy(): Promise<ComfyStartResult> {
  return apiRequest<ComfyStartResult>("/api/comfy/start", { method: "POST" });
}

export async function interruptComfy(): Promise<{ interrupted: boolean }> {
  return apiRequest<{ interrupted: boolean }>("/api/comfy/interrupt", { method: "POST" });
}

/** 让 ComfyUI 卸载模型、归还内存（出图后模型会一直驻留）。 */
export async function freeComfyMemory(): Promise<{ freed: boolean; status: ComfyStatus }> {
  return apiRequest<{ freed: boolean; status: ComfyStatus }>("/api/comfy/free", { method: "POST" });
}

// ── TTS（仅本地开发者模式：调 E:\AI\TTS CLI 契约，线上 Go 服务没有这些接口）──

export interface TtsEngineSpec {
  id: string;
  label: string;
  capabilities: string[];
  model: string;
  output_sr: number | Record<string, number> | null;
  ref_audio: { min_s?: number | null; max_s?: number | null; hard?: boolean; recommended?: string; note?: string } | null;
  speakers: string[] | null;
  community_voices: string[] | null;
  load_seconds_estimate: [number, number] | null;
  init_params: Record<string, any>;
  request_params: Record<string, any>;
  notes: string[];
  python_ok?: boolean | null;
  running?: boolean;
  note?: string;
}

export interface TtsVoice {
  name: string;
  source: string;
  created_at: string;
  instruct: string;
  ref_text: string;
  language: string;
  protocol: string;
  has_voice_pt: boolean;
  audio_url: string;
}

export interface TtsGpuInfo {
  name: string;
  vram_total_mb: number;
  vram_used_mb: number;
}

export interface TtsInstance {
  engine: string;
  pid: number | null;
  started_at: string;
  load_s: number | null;
  idle_timeout_s: number | null;
}

export interface TtsStatus {
  enabled: boolean;
  cli: { python: string; script: string };
  config: { timeout_ms: number; idle_timeout_seconds: number };
  contract_version: string;
  engines: TtsEngineSpec[];
  instance: TtsInstance | null;
  gpu: TtsGpuInfo | null;
  voices: TtsVoice[];
  gptsovits_voices: string[];
  paths: Record<string, string> | null;
  error: string;
}

export interface TtsOutput {
  id: string;
  name: string;
  kind: string;
  sr: number | null;
  duration_s: number | null;
  size: number | null;
  audio_url: string;
  preview_url: string;
  download_url: string;
}

export interface TtsTask {
  task_no: string;
  task_kind: "tts";
  status: "queued" | "running" | "succeeded" | "failed" | "canceled";
  progress_percent: number;
  mode: "design" | "say" | "clone" | "preset";
  engine: string;
  engine_label: string;
  text: string;
  instruct: string;
  voice: string;
  speaker: string;
  language: string;
  takes: number;
  save_as: string;
  ref_filename: string;
  ref_text: string;
  params: Record<string, any>;
  outputs: TtsOutput[];
  saved_voice: string;
  warnings: string[];
  timing: { handle_s?: number } | Record<string, number>;
  load_s: number | null;
  error_code: string;
  error_message: string;
  submitted_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface TtsSubmitPayload {
  mode: "design" | "say" | "clone" | "preset";
  text: string;
  /** clone：qwen-base / gptsovits / indextts / megatts3；preset：qwen-preset / gptsovits */
  engine?: string;
  /** preset 别名，后端与 engine 等价 */
  speaker_engine?: string;
  instruct?: string;
  voice?: string;
  speaker?: string;
  language?: string;
  takes?: number;
  save_as?: string;
  ref_token?: string;
  ref_text?: string;
  ref_lang?: string;
  params?: Record<string, string | number | boolean | number[]>;
  switch?: boolean;
}

export async function fetchTtsStatus(): Promise<TtsStatus> {
  return apiRequest<TtsStatus>("/api/tts/status");
}

export async function startTtsEngine(payload: { engine: string; force?: boolean; switch?: boolean; keep_resident?: boolean }): Promise<{ engine: string; slot: TtsInstance | null }> {
  return apiRequest<{ engine: string; slot: TtsInstance | null }>("/api/tts/start", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export async function stopTts(): Promise<{ stopped: boolean }> {
  return apiRequest<{ stopped: boolean }>("/api/tts/stop", { method: "POST" });
}

export async function releaseTtsMemory(): Promise<{ released: boolean; stopped?: boolean; reason: string; holder?: { owner: string; label: string } | null }> {
  return apiRequest<{ released: boolean; stopped?: boolean; reason: string; holder?: { owner: string; label: string } | null }>("/api/tts/release", { method: "POST" });
}

export async function releaseComfyMemory(): Promise<{ released: boolean; running?: boolean; reason: string; holder?: { owner: string; label: string } | null }> {
  return apiRequest<{ released: boolean; running?: boolean; reason: string; holder?: { owner: string; label: string } | null }>("/api/comfy/release", { method: "POST" });
}

export async function fetchGpuStatus(): Promise<{ owner: string; label: string; held_for_ms: number; waiting: number } | null> {
  return apiRequest<{ owner: string; label: string; held_for_ms: number; waiting: number } | null>("/api/gpu/status");
}

export async function fetchTtsLog(engine: string, lines = 60): Promise<{ engine: string; text: string }> {
  const search = new URLSearchParams({ engine, lines: String(lines) });
  return apiRequest<{ engine: string; text: string }>(`/api/tts/log?${search.toString()}`);
}

export async function fetchTtsVoices(): Promise<{ voices: TtsVoice[]; gptsovits_voices: string[]; enabled: boolean }> {
  return apiRequest<{ voices: TtsVoice[]; gptsovits_voices: string[]; enabled: boolean }>("/api/tts/voices");
}

export async function createTtsTask(payload: TtsSubmitPayload): Promise<TtsTask> {
  return apiRequest<TtsTask>("/api/tts/tasks", { method: "POST", body: JSON.stringify(payload) });
}

export async function fetchTtsTaskDetail(taskNo: string): Promise<TtsTask> {
  return apiRequest<TtsTask>(`/api/tts/tasks/${encodeURIComponent(taskNo)}`);
}

export async function cancelTtsTask(taskNo: string): Promise<TtsTask> {
  return apiRequest<TtsTask>(`/api/tts/tasks/${encodeURIComponent(taskNo)}/cancel`, { method: "POST" });
}

export async function deleteTtsTask(taskNo: string): Promise<void> {
  await apiRequest<any>(`/api/tts/tasks/${encodeURIComponent(taskNo)}`, { method: "DELETE" });
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
