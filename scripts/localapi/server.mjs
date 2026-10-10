// 本地开发 API 服务：只做工作台与管理台需要的那部分接口，
// 无数据库、无登录、无注册，图片全部落在 local-data/media。
//
// 约定：前端通过 Vite 开发服务器代理到这里（默认 127.0.0.1:8000），
// 所以这里的 URL 形状、字段名与线上 Go 服务保持一致。
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import {pathToFileURL} from 'node:url';

import * as chatApi from './chat.mjs';
import * as comfyApi from './comfy.mjs';
import * as consoleApi from './console.mjs';
import {
  DATA_DIR,
  HOST,
  PORT,
  PROVIDER,
  STORE_FILE,
  providerConfigured,
} from './config.mjs';
import * as gpuApi from './gpu.mjs';
import * as novelApi from './novel.mjs';
import * as ttsApi from './tts.mjs';
import {downloadURL, localUser, mediaByToken, persist, quotaView, store} from './store.mjs';
import * as taskApi from './tasks.mjs';
import * as uploadApi from './uploads.mjs';

const {ValidationError} = taskApi;

// ── HTTP 基础工具 ──

function sendJSON(res, status, payload) {
  const body = Buffer.from(JSON.stringify(payload), 'utf8');
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': body.length,
    'Cache-Control': 'no-store',
  });
  res.end(body);
}

function ok(res, data) {
  sendJSON(res, 200, {code: 0, message: 'ok', data});
}

function fail(res, status, message, code = status) {
  sendJSON(res, status, {code, message, data: null});
}

async function readBody(req) {
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  return Buffer.concat(chunks);
}

async function readJSON(req) {
  const body = await readBody(req);
  if (!body.length) return {};
  try {
    return JSON.parse(body.toString('utf8'));
  } catch {
    throw new ValidationError('请求 JSON 格式错误');
  }
}

/** 极简 multipart 解析：只取指定字段的第一个文件部分。 */
function readMultipartFile(body, boundary, fieldName) {
  const delimiter = Buffer.from(`--${boundary}`);
  let offset = body.indexOf(delimiter);
  while (offset >= 0) {
    const next = body.indexOf(delimiter, offset + delimiter.length);
    if (next < 0) break;
    const part = body.subarray(offset + delimiter.length, next);
    const headerEnd = part.indexOf('\r\n\r\n');
    if (headerEnd > 0) {
      const headers = part.subarray(0, headerEnd).toString('utf8');
      const content = part.subarray(headerEnd + 4, part.length - 2);
      const nameMatch = /name="([^"]*)"/i.exec(headers);
      const filenameMatch = /filename="([^"]*)"/i.exec(headers);
      if (nameMatch?.[1] === fieldName && filenameMatch) {
        const mimeMatch = /content-type:\s*([^\r\n]+)/i.exec(headers);
        return {filename: filenameMatch[1], contentType: mimeMatch?.[1] || 'application/octet-stream', content};
      }
    }
    offset = next;
  }
  return null;
}

function parseCookies(req) {
  const header = req.headers.cookie || '';
  const cookies = {};
  for (const part of header.split(';')) {
    const separator = part.indexOf('=');
    if (separator < 0) continue;
    cookies[part.slice(0, separator).trim()] = part.slice(separator + 1).trim();
  }
  return cookies;
}

// ── 路由 ──

const routes = [];

function route(method, pattern, handler) {
  routes.push({method, segments: pattern.split('/').filter(Boolean), handler, pattern});
}

function match(routeEntry, method, pathname) {
  if (routeEntry.method !== method) return null;
  const parts = pathname.split('/').filter(Boolean);
  if (parts.length !== routeEntry.segments.length) return null;
  const params = {};
  for (let index = 0; index < parts.length; index += 1) {
    const segment = routeEntry.segments[index];
    if (segment.startsWith('{') && segment.endsWith('}')) {
      params[segment.slice(1, -1)] = decodeURIComponent(parts[index]);
      continue;
    }
    if (segment !== parts[index]) return null;
  }
  return params;
}

// ── 工作台：账号 ──

function accountSummary() {
  const user = localUser();
  return {
    user: {
      username: user.username,
      nickname: user.profile?.nickname || user.username,
      email: user.email,
      avatar: user.profile?.avatar || '',
      remark: user.profile?.remark || '',
    },
    quota: quotaView(),
    recent_tasks: store.tasks.slice(0, 5).map((task) => ({
      task_no: task.task_no,
      status: task.status,
      created_at: task.submitted_at,
    })),
  };
}

route('GET', '/api/account/summary', async (_req, res) => ok(res, accountSummary()));

route('GET', '/api/account/settings', async (_req, res) => {
  const user = localUser();
  return ok(res, {
    user: {
      username: user.username,
      nickname: user.profile?.nickname || '',
      email: user.email,
      avatar: user.profile?.avatar || '',
      remark: user.profile?.remark || '',
      role: user.role,
      status: user.status,
      date_joined: user.date_joined,
      last_login_at: user.last_login,
      last_login_ip: user.last_login_ip || '',
      register_source: user.profile?.register_source || 'local-mode',
    },
    quota: quotaView(),
  });
});

route('PATCH', '/api/account/settings', async (req, res) => {
  const payload = await readJSON(req);
  const user = localUser();
  user.profile = {
    ...user.profile,
    nickname: payload.nickname === undefined ? user.profile?.nickname || '' : String(payload.nickname),
    avatar: payload.avatar === undefined ? user.profile?.avatar || '' : String(payload.avatar),
    remark: payload.remark === undefined ? user.profile?.remark || '' : String(payload.remark),
  };
  if (payload.email !== undefined) user.email = String(payload.email);
  persist();
  return ok(res, {
    user: {
      username: user.username,
      nickname: user.profile.nickname,
      email: user.email,
      avatar: user.profile.avatar,
      remark: user.profile.remark,
      role: user.role,
      status: user.status,
      date_joined: user.date_joined,
      last_login_at: user.last_login,
      last_login_ip: user.last_login_ip || '',
      register_source: user.profile.register_source,
    },
    quota: quotaView(),
  });
});

route('POST', '/api/account/password', async (_req, res) =>
  fail(res, 400, '本地模式没有密码，账号由服务端固定提供', 400),
);

// ── 工作台：登录相关（本地模式自动登录，这里只保证前端不报错） ──

route('GET', '/api/auth/csrf', async (_req, res) => {
  const token = crypto.randomBytes(16).toString('hex');
  res.setHeader('Set-Cookie', `csrftoken=${token}; Path=/; SameSite=Lax`);
  return ok(res, {csrf_token: token});
});

route('GET', '/api/auth/captcha', async (_req, res) => {
  const code = 'LOCAL';
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="168" height="56"><rect width="168" height="56" fill="#eef2ff"/><text x="50%" y="62%" font-size="22" text-anchor="middle" fill="#1e293b" font-family="monospace">${code}</text></svg>`;
  return ok(res, {captcha_code: code, captcha_svg: `data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}`});
});

route('POST', '/api/auth/login', async (_req, res) =>
  fail(res, 400, '本地模式已自动登录，无需登录', 400),
);

route('POST', '/api/auth/register', async (_req, res) =>
  fail(res, 400, '本地模式没有注册流程，固定使用 local 账号', 400),
);

route('POST', '/api/auth/register/email-code', async (_req, res) =>
  fail(res, 400, '本地模式不发送邮件验证码', 400),
);

route('POST', '/api/auth/register/check', async (_req, res) =>
  ok(res, {username: {available: true, message: ''}, email: {available: true, message: ''}}),
);

route('POST', '/api/auth/logout', async (_req, res) => {
  // 本地模式的账号是服务端提供的固定身份，登出只回一个成功，页面仍处于可用状态。
  return ok(res, {logged_out: false, local_mode: true});
});

// ── 工作台：对话 ──
//
// 对话渠道（中转站/本机 LM Studio）由工作台的悬浮窗对话 → 设置里配置，
// 存 local-data/store.json 的 chat 段；密钥只在本机，这里只回掩码。

route('GET', '/api/chat/config', async (_req, res) => ok(res, chatApi.chatConfigView()));

route('PUT', '/api/chat/config', async (req, res) => {
  const payload = await readJSON(req);
  return ok(res, chatApi.saveChatConfig(payload));
});

// 代理上游 /v1/models：设置面板的「获取模型列表 / 测试连通」。
route('POST', '/api/chat/models', async (req, res) => {
  const payload = await readJSON(req);
  const models = await chatApi.listRelayModels({
    baseUrl: payload.base_url,
    apiKey: payload.api_key,
  });
  return ok(res, {models});
});

route('GET', '/api/chat/models', async (_req, res) => {
  const config = chatApi.chatConfigView();
  const models = store.channels
    .filter((channel) => channel.channel_type === 'llm')
    .map((channel) => ({
      id: channel.id,
      name: config.configured ? `${config.model} · 中转站` : '本地模拟对话（未配置）',
      channel_code: channel.channel_code,
      provider_name: config.configured ? 'Relay' : channel.provider_name,
      model_name: config.configured ? config.model : channel.model_name,
      configured: config.configured,
    }));
  return ok(res, models);
});

route('POST', '/api/chat', async (req, res) => {
  const payload = await readJSON(req);
  const prompt = String(payload.prompt || '').trim();
  if (!prompt) return fail(res, 400, '请输入对话内容', 400);
  try {
    const reply = await chatApi.chatReply({prompt, history: Array.isArray(payload.history) ? payload.history : []});
    return ok(res, {reply});
  } catch (error) {
    return fail(res, 502, `对话渠道调用失败：${error.message}`, 502);
  }
});

// 流式对话（悬浮窗对话用）：上游 SSE 原样透传，思考过程在 delta.reasoning_content。
route('POST', '/api/chat/stream', async (req, res) => {
  const payload = await readJSON(req);
  const config = chatApi.resolveChatConfig();
  const messages = chatApi.buildChatMessages(payload.messages, config.custom_instruction);
  const controller = new AbortController();
  req.on('close', () => controller.abort());
  const upstream = await chatApi.openChatStream({messages, signal: controller.signal});
  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-transform',
    Connection: 'keep-alive',
  });
  try {
    const reader = upstream.body.getReader();
    for (;;) {
      const {done, value} = await reader.read();
      if (done) break;
      res.write(Buffer.from(value));
    }
  } catch (error) {
    if (!res.writableEnded) {
      res.write(`data: ${JSON.stringify({error: String(error?.message || error)})}\n\n`);
    }
  } finally {
    if (!res.writableEnded) res.end();
  }
});

// ── 工作台：生图任务 ──

route('POST', '/api/generate/tasks', async (req, res) => {
  const payload = await readJSON(req);
  const task = taskApi.createTask(payload);
  return ok(res, taskApi.serializeTask(task));
});

route('GET', '/api/generate/tasks/{taskNo}', async (_req, res, params) => {
  const task = taskApi.findTask(params.taskNo);
  if (!task) return fail(res, 404, '任务不存在', 404);
  return ok(res, taskApi.serializeTask(task));
});

route('DELETE', '/api/generate/tasks/{taskNo}', async (_req, res, params) => {
  if (!taskApi.deleteTask(params.taskNo)) return fail(res, 404, '任务不存在', 404);
  return ok(res, {deleted: true});
});

route('POST', '/api/generate/tasks/{taskNo}/cancel', async (_req, res, params) => {
  const task = taskApi.cancelTask(params.taskNo);
  if (!task) return fail(res, 404, '任务不存在', 404);
  return ok(res, taskApi.serializeTask(task));
});

route('POST', '/api/generate/reference-images', async (req, res) => {
  const payload = await readJSON(req);
  const reference = taskApi.registerReference({
    dataURL: String(payload.reference_image_data_url || ''),
    filename: String(payload.reference_image_name || 'reference-image.png'),
  });
  if (!reference) return fail(res, 400, '请提供参考图数据', 400);
  return ok(res, {
    reference_upload_token: reference.token,
    reference_image_name: reference.name,
    reference_image_attached: true,
  });
});

// ── Comfy 本地生图（仅本地模式：线上 Go 服务没有这三条接口，前端据此不显示板块）──

route('GET', '/api/comfy/status', async (_req, res) => ok(res, await comfyApi.comfyStatus()));

route('GET', '/api/comfy/log', async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const limit = Math.max(1, Math.min(20, Number.parseInt(url.searchParams.get('limit') || '8', 10) || 8));
  return ok(res, await comfyApi.readComfyLog({limit}));
});

route('POST', '/api/comfy/start', async (_req, res) => ok(res, await comfyApi.startComfy()));

route('POST', '/api/comfy/interrupt', async (_req, res) => ok(res, await comfyApi.interruptComfy()));

// 释放内存：让 ComfyUI 卸载模型（出图后模型会一直驻留，占内存/显存）
route('POST', '/api/comfy/free', async (_req, res) => ok(res, await comfyApi.freeComfyMemory()));

// ── TTS 语音合成（仅本地模式：调 E:\AI\TTS CLI 契约，线上 Go 服务没有这些接口）──

route('GET', '/api/tts/status', async (_req, res) => ok(res, await ttsApi.ttsStatus()));

route('POST', '/api/tts/start', async (req, res) => {
  const payload = await readJSON(req);
  return ok(res, await ttsApi.startTtsEngine({...payload, keepResident: payload.keep_resident === true}));
});

route('POST', '/api/tts/stop', async (_req, res) => ok(res, await ttsApi.stopTts()));

// 「释放内存」按钮：TTS 停实例，Comfy 卸载模型；有任务在跑时会拒绝并返回占用者。
route('POST', '/api/tts/release', async (_req, res) => ok(res, await ttsApi.releaseTtsResources()));

route('POST', '/api/comfy/release', async (_req, res) => ok(res, await comfyApi.releaseComfyMemory()));

route('GET', '/api/gpu/status', async (_req, res) => ok(res, gpuApi.describeGpuLock()));

route('GET', '/api/tts/log', async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  return ok(res, await ttsApi.ttsLog({
    engine: url.searchParams.get('engine') || '',
    lines: Number.parseInt(url.searchParams.get('lines') || '60', 10) || 60,
  }));
});

route('GET', '/api/tts/voices', async (_req, res) => ok(res, await ttsApi.ttsStatus().then((status) => ({
  voices: status.voices,
  gptsovits_voices: status.gptsovits_voices,
  enabled: status.enabled,
}))));

route('GET', '/api/tts/voices/{name}/audio', async (_req, res, params) => {
  const source = ttsApi.serveVoiceSource(params.name);
  if (!fs.existsSync(source.file)) return fail(res, 404, '音色来源音频不存在', 404);
  const buffer = fs.readFileSync(source.file);
  res.writeHead(200, {
    'Content-Type': source.mimeType,
    'Content-Length': buffer.length,
    'Content-Disposition': `inline; filename="${encodeURIComponent(source.name)}"`,
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
  });
  res.end(buffer);
});

route('GET', '/api/tts/tasks', async (_req, res) =>
  ok(res, {items: [...store.ttsTasks].map((task) => ttsApi.serializeTtsTask(task))}));

route('POST', '/api/tts/tasks', async (req, res) => {
  const payload = await readJSON(req);
  const task = ttsApi.createTtsTask(payload);
  return ok(res, ttsApi.serializeTtsTask(task));
});

route('GET', '/api/tts/tasks/{taskNo}', async (_req, res, params) => {
  const task = ttsApi.findTtsTask(params.taskNo);
  if (!task) return fail(res, 404, '任务不存在', 404);
  return ok(res, ttsApi.serializeTtsTask(task));
});

route('DELETE', '/api/tts/tasks/{taskNo}', async (_req, res, params) => {
  if (!ttsApi.deleteTtsTask(params.taskNo)) return fail(res, 404, '任务不存在', 404);
  return ok(res, {deleted: true});
});

route('POST', '/api/tts/tasks/{taskNo}/cancel', async (_req, res, params) => {
  const task = ttsApi.cancelTtsTask(params.taskNo);
  if (!task) return fail(res, 404, '任务不存在', 404);
  return ok(res, ttsApi.serializeTtsTask(task));
});

route('GET', '/api/workspace/history', async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  const page = Math.max(1, Number.parseInt(url.searchParams.get('page') || '1', 10));
  const pageSize = Math.max(1, Number.parseInt(url.searchParams.get('page_size') || '12', 10));
  const keyword = (url.searchParams.get('q') || '').trim().toLowerCase();

  // 生图、小说与 TTS 任务共用一份历史列表，先合并排序再分页。
  const items = [
    ...store.tasks
      .filter((task) => (keyword ? task.prompt.toLowerCase().includes(keyword) : true))
      .map((task) => taskApi.serializeTask(task)),
    ...store.novelTasks
      .filter((task) => (keyword ? task.book_filename.toLowerCase().includes(keyword) : true))
      .map((task) => novelApi.historyItem(task)),
    ...store.ttsTasks
      .filter((task) => (keyword ? task.text.toLowerCase().includes(keyword) : true))
      .map((task) => ttsApi.ttsHistoryItem(task)),
  ].sort((left, right) => String(right.submitted_at || '').localeCompare(String(left.submitted_at || '')));

  const total = items.length;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const currentPage = Math.min(page, totalPages);
  const start = (currentPage - 1) * pageSize;
  return ok(res, {
    items: items.slice(start, start + pageSize),
    pagination: {
      page: currentPage,
      page_size: pageSize,
      total,
      total_pages: totalPages,
      has_next: currentPage < totalPages,
      has_previous: currentPage > 1,
    },
  });
});

route('POST', '/api/images/{imageId}/download-url', async (_req, res, params) => {
  const imageId = Number.parseInt(params.imageId, 10);
  for (const task of store.tasks) {
    const image = (task.images || []).find((item) => item.id === imageId);
    if (image) {
      return ok(res, {url: downloadURL(image.token, {download: true}), preview_url: downloadURL(image.token, {preview: true})});
    }
  }
  for (const task of store.novelTasks) {
    const image = (task.images || []).find((item) => item.id === imageId);
    if (image?.token) {
      return ok(res, {url: downloadURL(image.token, {download: true}), preview_url: downloadURL(image.token, {preview: true})});
    }
  }
  return fail(res, 404, '图片不存在', 404);
});

// ── 媒体下载 ──

async function serveDownload(req, res, params) {
  const media = mediaByToken(params.token);
  if (!media || !fs.existsSync(media.file)) return fail(res, 404, '文件不存在或已过期', 404);
  const url = new URL(req.url, 'http://localhost');
  const inline = url.searchParams.get('preview') === '1' || url.searchParams.get('disposition') !== 'attachment';
  const buffer = fs.readFileSync(media.file);
  res.writeHead(200, {
    'Content-Type': media.mime_type,
    'Content-Length': buffer.length,
    'Content-Disposition': `${inline ? 'inline' : 'attachment'}; filename="${encodeURIComponent(media.name)}"`,
    'Cache-Control': 'no-store',
    'X-Content-Type-Options': 'nosniff',
  });
  res.end(buffer);
}

route('GET', '/downloads/{token}', serveDownload);

// ── 上传会话 ──

route('POST', '/api/uploads/sessions', async (req, res) => {
  const payload = await readJSON(req);
  return ok(res, uploadApi.createSession(payload));
});

route('GET', '/api/uploads/sessions/{uploadId}', async (_req, res, params) =>
  ok(res, uploadApi.sessionJSON(params.uploadId)),
);

route('DELETE', '/api/uploads/sessions/{uploadId}', async (_req, res, params) =>
  ok(res, uploadApi.cancelSession(params.uploadId)),
);

route('PUT', '/api/uploads/sessions/{uploadId}/chunks/{index}', async (req, res, params) => {
  const body = await readBody(req);
  const index = Number.parseInt(params.index, 10);
  return ok(res, uploadApi.saveChunk(params.uploadId, index, body));
});

route('POST', '/api/uploads/sessions/{uploadId}/complete', async (_req, res, params) =>
  ok(res, await uploadApi.completeSession(params.uploadId)),
);

// ── 小说可视化 ──

route('GET', '/api/novelviz/books', async (_req, res) => ok(res, novelApi.listBooks()));

route('POST', '/api/novelviz/books/upload', async (req, res) => {
  const contentType = req.headers['content-type'] || '';
  const boundaryMatch = /boundary=([^;]+)/i.exec(contentType);
  if (!boundaryMatch) return fail(res, 400, '请使用 multipart/form-data 上传 EPUB', 400);
  const body = await readBody(req);
  const part = readMultipartFile(body, boundaryMatch[1].replace(/^"|"$/g, ''), 'file');
  if (!part) return fail(res, 400, '没有收到上传文件', 400);
  if (!/\.epub$/i.test(part.filename) && part.contentType !== 'application/epub+zip') {
    return fail(res, 400, '只支持 EPUB 格式的小说', 400);
  }
  return ok(res, novelApi.registerUploadedBook({
    filename: part.filename,
    displayName: part.filename,
    buffer: part.content,
  }));
});

route('GET', '/api/novelviz/tasks', async (_req, res) => ok(res, novelApi.listTasks()));

route('POST', '/api/novelviz/tasks', async (req, res) => {
  const payload = await readJSON(req);
  return ok(res, novelApi.createTask(payload));
});

route('GET', '/api/novelviz/tasks/{taskId}', async (_req, res, params) =>
  ok(res, novelApi.taskJSON(Number.parseInt(params.taskId, 10))),
);

route('DELETE', '/api/novelviz/tasks/{taskId}', async (_req, res, params) => {
  if (!novelApi.deleteTask(Number.parseInt(params.taskId, 10))) return fail(res, 404, '任务不存在', 404);
  return ok(res, {deleted: true});
});

route('POST', '/api/novelviz/tasks/{taskId}/retry', async (_req, res, params) =>
  ok(res, novelApi.retryTask(Number.parseInt(params.taskId, 10))),
);

route('GET', '/api/novelviz/tasks/{taskId}/events', async (req, res, params) => {
  const taskId = Number.parseInt(params.taskId, 10);
  const task = novelApi.findTask(taskId);
  if (!task) return fail(res, 404, '任务不存在', 404);

  const url = new URL(req.url, 'http://localhost');
  const lastEventId = Number.parseInt(url.searchParams.get('last_event_id') || '0', 10) || 0;

  res.writeHead(200, {
    'Content-Type': 'text/event-stream; charset=utf-8',
    'Cache-Control': 'no-cache, no-store',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });

  const write = (event) => {
    res.write(`id: ${event.id}\nevent: ${event.type}\ndata: ${JSON.stringify(event.payload)}\n\n`);
  };
  const terminal = new Set(['task_done', 'task_failed']);

  for (const event of novelApi.eventsAfter(taskId, lastEventId)) {
    write(event);
    if (terminal.has(event.type)) {
      res.end();
      return;
    }
  }

  const unsubscribe = novelApi.subscribe(taskId, (event) => {
    write(event);
    if (terminal.has(event.type)) {
      unsubscribe();
      clearInterval(keepAlive);
      res.end();
    }
  });
  const keepAlive = setInterval(() => res.write(': keep-alive\n\n'), 15000);
  req.on('close', () => {
    unsubscribe();
    clearInterval(keepAlive);
  });
});

// ── 管理台 ──

route('GET', '/console/api/dashboard/', async (_req, res) => ok(res, consoleApi.dashboard()));

route('GET', '/console/api/users/', async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  return ok(res, consoleApi.listUsers({
    page: Number.parseInt(url.searchParams.get('page') || '1', 10),
    perPage: Number.parseInt(url.searchParams.get('per_page') || '12', 10),
    keyword: url.searchParams.get('keyword') || '',
    status: url.searchParams.get('status') || '',
  }));
});

route('GET', '/console/api/users/{userId}/', async (_req, res, params) =>
  ok(res, consoleApi.userDetail(Number.parseInt(params.userId, 10))),
);

route('POST', '/console/api/users/{userId}/quota/', async (req, res, params) => {
  const payload = await readJSON(req);
  return ok(res, consoleApi.updateUserQuota(Number.parseInt(params.userId, 10), payload));
});

route('GET', '/console/api/tasks/', async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  return ok(res, consoleApi.listTasks({
    page: Number.parseInt(url.searchParams.get('page') || '1', 10),
    perPage: Number.parseInt(url.searchParams.get('per_page') || '20', 10),
    keyword: url.searchParams.get('keyword') || '',
    status: url.searchParams.get('status') || '',
  }));
});

route('GET', '/console/api/config/', async (req, res) => {
  const url = new URL(req.url, 'http://localhost');
  return ok(res, consoleApi.configList(url.searchParams.get('category') || ''));
});

route('PUT', '/console/api/config/batch/', async (req, res) => {
  const payload = await readJSON(req);
  return ok(res, consoleApi.batchUpdateConfig(payload.items || []));
});

route('PUT', '/console/api/config/{key}/', async (req, res, params) => {
  const payload = await readJSON(req);
  return ok(res, consoleApi.updateConfig(params.key, payload));
});

route('GET', '/console/api/image-channels/', async (_req, res) => ok(res, consoleApi.listChannels()));

route('POST', '/console/api/image-channels/', async (req, res) => {
  const payload = await readJSON(req);
  return ok(res, consoleApi.createChannel(payload));
});

route('PUT', '/console/api/image-channels/{channelId}/', async (req, res, params) => {
  const payload = await readJSON(req);
  return ok(res, consoleApi.updateChannel(Number.parseInt(params.channelId, 10), payload));
});

route('DELETE', '/console/api/image-channels/{channelId}/', async (_req, res, params) =>
  ok(res, consoleApi.deleteChannel(Number.parseInt(params.channelId, 10))),
);

// ── 健康检查与本地信息页 ──

// 这些前缀由本地 API 认领；单端口模式下其余路径交给 Vite（工作台 / 管理台）。
const API_PREFIXES = ['/api/', '/console/api/', '/downloads/', '/healthz', '/__local'];

export function isApiPath(pathname) {
  return API_PREFIXES.some((prefix) => pathname === prefix || pathname.startsWith(prefix));
}

route('GET', '/healthz', async (_req, res) => ok(res, {status: 'ok'}));

function infoHTML({comfyLine, chatLine, ttsLine}) {
  return `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <title>绘镜 · 本地开发者模式</title>
  <style>
    body { font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; background: #f7f8fb; color: #0f172a; display: grid; place-items: center; min-height: 100vh; margin: 0; }
    main { background: #fff; padding: 32px 40px; border-radius: 20px; box-shadow: 0 24px 60px rgba(15, 23, 42, .08); max-width: 560px; }
    h1 { font-size: 20px; margin: 0 0 6px; }
    p { color: #475569; font-size: 14px; line-height: 1.7; margin: 6px 0; }
    ul { padding-left: 18px; }
    a { color: #2563eb; text-decoration: none; font-weight: 600; }
    code { background: #f1f5f9; padding: 2px 6px; border-radius: 6px; font-size: 13px; }
  </style>
</head>
<body>
  <main>
    <h1>绘镜 · 本地开发者模式</h1>
    <p>本地 API 已就绪（无数据库、无登录、无注册），与两个前端共用同一个端口。</p>
    <ul>
      <li><a href="/">工作台</a>：<code>/</code></li>
      <li><a href="/admin">管理台</a>：<code>/admin</code></li>
    </ul>
    <p>数据目录：<code>${DATA_DIR}</code>；图片：<code>${path.join(DATA_DIR, 'media')}</code>。</p>
    <p>生图渠道：${providerConfigured ? '<b>真实渠道</b>' : '本地占位图（未配置 PROVIDER_API_KEY）'}；
       对话渠道：${chatLine}。</p>
    <p>ComfyUI（工作台「Comfy 生图」板块）：${comfyLine}。</p>
    <p>TTS（工作台「TTS 语音合成」板块）：${ttsLine}。</p>
  </main>
</body>
</html>`;
}

route('GET', '/__local', async (_req, res) => {
  const comfy = await comfyApi.comfyStatus();
  const comfyLine = comfy.running
    ? `<b>运行中</b>（<code>${comfy.base_url}</code>${comfy.stats?.comfyui_version ? ` · ComfyUI ${comfy.stats.comfyui_version}` : ''}）`
    : `未运行（<code>${comfy.base_url}</code>）；可打开工作台的「Comfy 生图」板块点击启动`;
  const chat = chatApi.chatConfigView();
  const chatLine = chat.configured
    ? `<b>真实渠道</b>（<code>${chat.base_url}</code> · ${chat.model} · 思考等级 ${chat.reasoning_effort}）`
    : '本地模拟回复（在工作台右下角悬浮窗 →「设置」里配置中转站）';
  const tts = await ttsApi.ttsStatus();
  const ttsLine = tts.enabled
    ? tts.instance
      ? `<b>实例运行中</b>（${tts.instance.engine} · pid ${tts.instance.pid ?? '?'}），CLI <code>${tts.cli.script}</code>`
      : `CLI 就绪（<code>${tts.cli.script}</code>），暂无运行实例`
    : `未找到 TTS CLI（<code>${tts.cli.script}</code>）；本板块依赖 E:\\AI\\TTS 契约环境`;
  res.writeHead(200, {'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store'});
  res.end(infoHTML({comfyLine, chatLine, ttsLine}));
});

// ── 调度 ──

/**
 * 处理一个本地 API 请求。返回值表示是否认领：
 * true  = 已处理（响应已发出）；false = 不属于 API 前缀，调用方决定后续（例如交给 Vite）。
 */
export async function handleRequest(req, res) {
  const url = new URL(req.url, `http://${req.headers.host || 'localhost'}`);
  const pathname = url.pathname;
  if (!isApiPath(pathname)) return false;

  for (const entry of routes) {
    const params = match(entry, req.method, pathname);
    if (!params) continue;
    try {
      await entry.handler(req, res, params);
    } catch (error) {
      if (res.headersSent) {
        res.end();
        return true;
      }
      const known = error instanceof ValidationError || error instanceof uploadApi.UploadError || error instanceof novelApi.NovelError || error instanceof comfyApi.ComfyError || error instanceof ttsApi.TtsError;
      if (known) {
        fail(res, 400, error.message, 400);
      } else if (error instanceof chatApi.ChatError) {
        // 对话渠道的报错自带状态码：配置类 400，上游类 502。
        fail(res, error.status || 400, error.message, error.status || 400);
      } else {
        console.error(`[local-api] ${req.method} ${pathname} 处理失败：`, error);
        fail(res, 500, error.message || '本地服务内部错误', 500);
      }
    }
    return true;
  }

  fail(res, 404, `本地服务没有实现这个接口：${req.method} ${pathname}`, 404);
  return true;
}

// ── 单独运行（npm run local:api）：只提供接口，不含前端页面 ──

function reportListenError(error) {
  if (error.code === 'EADDRINUSE') {
    console.error(`\n本地 API 端口 ${PORT} 已被占用：`);
    console.error(`  - 可能已经有一个本地服务在跑，直接访问 http://${HOST}:${PORT}/ 即可；`);
    console.error('  - 也可能是 Go 服务或 Docker 容器占用了 8000，请先停掉它，或在 .env.local 里改 LOCAL_API_PORT。');
  } else {
    console.error(`\n本地 API 启动失败：${error.message}`);
  }
  process.exit(1);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const server = http.createServer((req, res) => {
    handleRequest(req, res).then((handled) => {
      if (handled || res.writableEnded) return;
      fail(res, 404, `这是只跑接口的模式（npm run local:api）；要打开页面请用 npm run local。请求：${req.method} ${req.url}`, 404);
    });
  });
  server.on('error', reportListenError);
  server.listen(PORT, HOST, () => {
    console.log('');
    console.log('  绘镜 · 本地 API（仅接口模式）');
    console.log('  ────────────────────────────────────────────────');
    console.log(`  本地 API      http://${HOST}:${PORT}`);
    console.log(`  数据目录      ${DATA_DIR}`);
    console.log(`  生图渠道      ${providerConfigured ? `真实渠道 ${PROVIDER.modelName}` : '本地占位图（零配置可用）'}`);
    const chat = chatApi.chatConfigView();
    console.log(`  对话渠道      ${chat.configured ? `真实渠道 ${chat.model}（思考等级 ${chat.reasoning_effort}）` : '本地模拟回复（工作台悬浮窗 → 设置里配置中转站）'}`);
    console.log('  ────────────────────────────────────────────────');
    console.log('  这个模式不含前端页面：页面请用 npm run local，');
    console.log(`  或让 npm run dev:workspace / dev:console 代理到 http://${HOST}:${PORT}。`);
    console.log(`  存储文件：${STORE_FILE}`);
    console.log('');
  });
}

export {reportListenError};
