// 小说可视化：EPUB 解包取正文 → 场景分析（真实 LLM 或本地抽取）
// → 场景图生成 → SSE 事件流。EPUB 解析只用 node:zlib，不依赖第三方库。
import fs from 'node:fs';
import path from 'node:path';
import zlib from 'node:zlib';

import {NOVEL_DIR, NOVELVIZ_IMAGE_CONCURRENCY, NOVELVIZ_MAX_SCENES} from './config.mjs';
import {acquireGpu, gpuLockHolder} from './gpu.mjs';
import {comfyStatus, generateWithComfy} from './comfy.mjs';
import {generateImage, planScenes} from './provider.mjs';
import {downloadURL, nextId, nowIso, persist, saveMedia, store} from './store.mjs';

export class NovelError extends Error {}

// ── ZIP / EPUB ──

function readZipEntries(buffer) {
  const EOCD_SIGNATURE = 0x06054b50;
  let eocd = -1;
  for (let index = buffer.length - 22; index >= 0 && index > buffer.length - 66000; index -= 1) {
    if (buffer.readUInt32LE(index) === EOCD_SIGNATURE) {
      eocd = index;
      break;
    }
  }
  if (eocd < 0) throw new NovelError('不是有效的 EPUB（未找到 ZIP 目录）');

  const entryCount = buffer.readUInt16LE(eocd + 10);
  let offset = buffer.readUInt32LE(eocd + 16);
  const entries = [];
  for (let index = 0; index < entryCount; index += 1) {
    if (buffer.readUInt32LE(offset) !== 0x02014b50) break;
    const method = buffer.readUInt16LE(offset + 10);
    const compressedSize = buffer.readUInt32LE(offset + 20);
    const nameLength = buffer.readUInt16LE(offset + 28);
    const extraLength = buffer.readUInt16LE(offset + 30);
    const commentLength = buffer.readUInt16LE(offset + 32);
    const localOffset = buffer.readUInt32LE(offset + 42);
    const name = buffer.toString('utf8', offset + 46, offset + 46 + nameLength);
    entries.push({name, method, compressedSize, localOffset});
    offset += 46 + nameLength + extraLength + commentLength;
  }
  return entries;
}

function extractEntry(buffer, entry) {
  if (buffer.readUInt32LE(entry.localOffset) !== 0x04034b50) return null;
  const nameLength = buffer.readUInt16LE(entry.localOffset + 26);
  const extraLength = buffer.readUInt16LE(entry.localOffset + 28);
  const start = entry.localOffset + 30 + nameLength + extraLength;
  const data = buffer.subarray(start, start + entry.compressedSize);
  if (entry.method === 0) return Buffer.from(data);
  if (entry.method === 8) {
    try {
      return zlib.inflateRawSync(data);
    } catch {
      return null;
    }
  }
  return null;
}

function stripMarkup(html) {
  return html
    .replace(/<script[\s\S]*?<\/script>/gi, ' ')
    .replace(/<style[\s\S]*?<\/style>/gi, ' ')
    .replace(/<(br|\/p|\/div|\/h[1-6]|\/li)[^>]*>/gi, '\n')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'")
    .replace(/&#(\d+);/g, (_, code) => String.fromCodePoint(Number.parseInt(code, 10)))
    .replace(/[ \t\u00a0]+/g, ' ')
    .replace(/\n{2,}/g, '\n')
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .join('\n');
}

/** 从 EPUB 里按文件名顺序拼出正文；不是有效 EPUB 时按纯文本兜底读取。 */
export function extractEpubText(filePath) {
  const buffer = fs.readFileSync(filePath);
  let entries;
  try {
    entries = readZipEntries(buffer);
  } catch (error) {
    const text = stripMarkup(buffer.toString('utf8'));
    if (text.trim().length >= 100) return text;
    throw error;
  }
  const documents = entries
    .filter((entry) => /\.(x?html?|xhtml|htm)$/i.test(entry.name))
    .sort((left, right) => left.name.localeCompare(right.name, 'en', {numeric: true}));
  const chunks = [];
  for (const entry of documents) {
    const content = extractEntry(buffer, entry);
    if (!content) continue;
    const text = stripMarkup(content.toString('utf8'));
    if (text.trim()) chunks.push(text);
  }
  const merged = chunks.join('\n').trim();
  if (merged.length < 100) {
    throw new NovelError('EPUB 正文过长或格式特殊，未能提取到有效文本');
  }
  return merged;
}

// ── 任务 ──

function taskView(task) {
  return {
    id: task.id,
    book_filename: task.book_filename,
    book_source: task.book_source,
    uploaded_book_id: task.uploaded_book_id ?? null,
    scene_mode: task.scene_mode,
    target_plot: task.target_plot || '',
    status: task.status,
    progress_percent: task.progress_percent,
    error_message: task.error_message || '',
    channel_name: task.channel_name || '',
    placeholder: task.channel_name === '本地占位生图',
    scenes: task.scenes || [],
    images: (task.images || []).map(sceneImageView),
    logs: (store.events[task.id] || []).map((event) => ({
      id: event.id,
      type: event.type,
      payload: event.payload,
      created_at: event.created_at,
    })),
    created_at: task.created_at,
    started_at: task.started_at,
    finished_at: task.finished_at,
  };
}

function sceneImageView(image) {
  const ready = image.status === 'succeeded' && image.token;
  return {
    id: image.id,
    scene_number: image.scene_number,
    title: image.title,
    chapter: image.chapter,
    story_significance: image.story_significance,
    excerpt: image.excerpt,
    visual_prompt: image.visual_prompt,
    status: image.status,
    error_message: image.error_message || '',
    image_url: ready ? downloadURL(image.token) : '',
    preview_url: ready ? downloadURL(image.token, {preview: true}) : '',
    download_url: ready ? downloadURL(image.token, {download: true}) : '',
    attempts: image.attempts || 0,
    created_at: image.created_at,
    finished_at: image.finished_at,
  };
}

export function listBooks() {
  return store.books.map((book) => ({
    id: book.id,
    source: book.source,
    filename: book.filename,
    display_name: book.display_name,
    size: book.size,
    uploaded_book_id: book.uploaded_book_id ?? null,
    created_at: book.created_at,
  }));
}

export function registerUploadedBook({filename, displayName, buffer}) {
  fs.mkdirSync(NOVEL_DIR, {recursive: true});
  const safeName = String(filename || 'novel.epub').replace(/[\\/:*?"<>|]+/g, '-');
  const stored = `${Date.now()}-${safeName}`;
  fs.writeFileSync(path.join(NOVEL_DIR, stored), buffer);
  const bookId = nextId('book');
  const book = {
    id: String(bookId),
    source: 'uploaded',
    filename: stored,
    display_name: String(displayName || safeName).replace(/\.[^.]+$/, ''),
    size: buffer.length,
    uploaded_book_id: bookId,
    created_at: nowIso(),
  };
  store.books.unshift(book);
  persist();
  return listBooks()[0];
}

export function listTasks() {
  return store.novelTasks.map(taskView);
}

/**
 * 历史记录里的小说条目形状：task_no 前缀 novelviz:、task_kind=novelviz，
 * 与生图任务共用同一个列表（HistorySpace 靠这三个标记区分）。
 */
export function historyItem(task) {
  const images = (task.images || []).map((image) => {
    const ready = image.status === 'succeeded' && image.token;
    return {
      id: image.id,
      status: ready ? 'available' : image.status,
      width: 1024,
      height: 1024,
      expires_at: null,
      preview_url: ready ? downloadURL(image.token, {preview: true}) : '',
      download_url: ready ? downloadURL(image.token, {download: true}) : '',
      image_url: ready ? downloadURL(image.token) : '',
      scene_number: image.scene_number,
      title: image.title,
      chapter: image.chapter,
      story_significance: image.story_significance,
      excerpt: image.excerpt,
    };
  });
  return {
    task_no: `novelviz:${task.id}`,
    task_kind: 'novelviz',
    novel_task_id: task.id,
    status: task.status,
    progress_percent: task.progress_percent,
    generation_mode: 'novel_visualization',
    prompt: task.scene_mode === 'targeted' ? `小说指定剧情：${task.book_filename}` : `小说可视化：${task.book_filename}`,
    negative_prompt: '',
    style_code: '',
    size_code: '1024x1024',
    quality_code: 'auto',
    reference_image_name: task.book_filename,
    reference_image_attached: false,
    reference_image_data_url: '',
    quota_cost: 0,
    error_code: '',
    error_message: task.error_message || '',
    channel_name: task.channel_name || '',
    placeholder: task.channel_name === '本地占位生图',
    topic_id: null,
    book_filename: task.book_filename,
    scene_mode: task.scene_mode,
    target_plot: task.target_plot || '',
    submitted_at: task.created_at,
    started_at: task.started_at,
    finished_at: task.finished_at,
    images,
  };
}

export function findTask(taskId) {
  return store.novelTasks.find((task) => task.id === taskId) || null;
}

export function taskJSON(taskId) {
  const task = findTask(taskId);
  if (!task) throw new NovelError('小说可视化任务不存在');
  return taskView(task);
}

/**
 * 任务只能引用 NOVEL_DIR 里的一个 EPUB 文件，规则与 Go 端 resolve_book_path 一致：
 * 取基名、必须 .epub、必须落在 NOVEL_DIR 内、必须存在。此前 book_filename 原样
 * 拼进 path.join，'../../.env.local' 这类取值可以读到数据目录以外的文件。
 */
function safeBookFilename(raw) {
  const text = String(raw || '').trim();
  if (!text) throw new NovelError('请先选择一本 EPUB 小说');
  const base = path.basename(text.replace(/[\\]/g, '/'));
  if (!base.toLowerCase().endsWith('.epub')) throw new NovelError('请选择 EPUB 小说文件');
  const root = path.resolve(NOVEL_DIR);
  const resolved = path.resolve(root, base);
  if (path.dirname(resolved) !== root) throw new NovelError('EPUB 文件路径不合法');
  if (!fs.existsSync(resolved)) throw new NovelError('EPUB 文件不存在');
  return base;
}

export function createTask(payload) {
  const filename = safeBookFilename(payload.book_filename);
  const sceneMode = payload.scene_mode === 'targeted' ? 'targeted' : 'auto';
  const targetPlot = String(payload.target_plot || '').trim();
  if (sceneMode === 'targeted' && targetPlot.length < 4) {
    throw new NovelError('请填写想呈现的剧情');
  }

  const now = nowIso();
  const id = nextId('novelTask');
  const task = {
    id,
    book_filename: filename,
    book_source: payload.book_source === 'uploaded' ? 'uploaded' : 'uploaded',
    uploaded_book_id: payload.uploaded_book_id ?? null,
    scene_mode: sceneMode,
    target_plot: targetPlot,
    llm_channel_code: String(payload.llm_channel_code || ''),
    status: 'queued',
    progress_percent: 0,
    error_message: '',
    scenes: [],
    images: [],
    created_at: now,
    started_at: null,
    finished_at: null,
  };
  store.novelTasks.unshift(task);
  store.events[id] = [];
  persist();
  schedule(id, 0);
  return taskView(task);
}

export function retryTask(taskId) {
  const task = findTask(taskId);
  if (!task) throw new NovelError('小说可视化任务不存在');
  if (task.status === 'running' || task.status === 'queued') {
    throw new NovelError('任务正在进行中，无需重试');
  }
  const hasPlan = (task.images || []).length > 0;
  task.status = 'queued';
  task.error_message = '';
  task.progress_percent = hasPlan ? 36 : 0;
  task.finished_at = null;
  if (!hasPlan) {
    task.scenes = [];
  }
  for (const image of task.images || []) {
    if (image.status !== 'succeeded') {
      image.status = 'pending';
      image.error_message = '';
    }
  }
  persist();
  schedule(taskId, 0, {imageOnly: hasPlan});
  return taskView(task);
}

export function deleteTask(taskId) {
  const index = store.novelTasks.findIndex((task) => task.id === taskId);
  if (index < 0) return false;
  store.novelTasks.splice(index, 1);
  delete store.events[taskId];
  persist();
  return true;
}

// ── 事件流 ──

const subscribers = new Map();

function appendEvent(taskId, type, payload) {
  const list = store.events[taskId] || (store.events[taskId] = []);
  const event = {
    id: nextId('event'),
    type,
    payload,
    created_at: nowIso(),
  };
  list.push(event);
  const listeners = subscribers.get(taskId);
  if (listeners) {
    for (const listener of [...listeners]) {
      listener(event);
    }
  }
  return event;
}

export function subscribe(taskId, listener) {
  const listeners = subscribers.get(taskId) || new Set();
  listeners.add(listener);
  subscribers.set(taskId, listeners);
  return () => {
    listeners.delete(listener);
    if (!listeners.size) subscribers.delete(taskId);
  };
}

export function eventsAfter(taskId, lastEventId) {
  const list = store.events[taskId] || [];
  return list.filter((event) => event.id > lastEventId);
}

function schedule(taskId, delayMs, options = {}) {
  const timer = setTimeout(() => {
    run(taskId, options).catch((error) => {
      console.error(`[local-api] 小说任务 ${taskId} 执行失败：${error.message}`);
      failTask(taskId, error.message);
    });
  }, Math.max(0, delayMs));
  if (typeof timer.unref === 'function') timer.unref();
}

function failTask(taskId, message) {
  const task = findTask(taskId);
  if (!task) return;
  task.status = 'failed';
  task.error_message = message;
  task.finished_at = nowIso();
  appendEvent(taskId, 'task_failed', {message: `任务失败：${message}`, progress_percent: task.progress_percent});
  persist();
}

function log(taskId, message, progress, extra = {}) {
  appendEvent(taskId, 'log', {message, progress_percent: progress, ...extra});
}

function sleep(ms) {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, Math.max(0, ms));
    if (typeof timer.unref === 'function') timer.unref();
  });
}

async function run(taskId, {imageOnly = false} = {}) {
  const task = findTask(taskId);
  if (!task || task.status !== 'queued') return;
  task.status = 'running';
  task.started_at = nowIso();
  appendEvent(taskId, 'task_started', {message: '任务已开始', progress_percent: 2, task: taskView(task)});
  persist();

  if (!imageOnly) {
    await runAnalysis(taskId);
  }
  const current = findTask(taskId);
  if (!current || current.status !== 'running') return;
  await runImages(taskId);
}

async function runAnalysis(taskId) {
  const task = findTask(taskId);
  task.progress_percent = 5;
  log(taskId, `正在读取 EPUB：${task.book_filename}`, 5);
  persist();

  const bookPath = path.join(NOVEL_DIR, task.book_filename);
  if (!fs.existsSync(bookPath)) {
    throw new NovelError(`找不到小说文件：${task.book_filename}`);
  }
  const text = extractEpubText(bookPath);
  const textLength = [...text].length;

  const {scenes, source} = await planScenes({
    text,
    maxScenes: NOVELVIZ_MAX_SCENES,
    targetPlot: task.scene_mode === 'targeted' ? task.target_plot : '',
  });
  if (!scenes.length) throw new NovelError('没有解析出可用场景');

  const current = findTask(taskId);
  if (!current) return;
  log(taskId, `正文提取完成，共 ${textLength} 字，开始使用 ${source} 分析`, 18, {
    text_length: textLength,
    scene_mode: current.scene_mode,
  });

  current.scenes = scenes;
  current.images = scenes.map((scene) => ({
    id: nextId('image'),
    scene_number: scene.number,
    title: scene.title,
    chapter: scene.chapter,
    story_significance: scene.story_significance,
    excerpt: scene.excerpt,
    visual_prompt: scene.visual_prompt,
    status: 'pending',
    error_message: '',
    token: '',
    attempts: 0,
    created_at: nowIso(),
    finished_at: null,
  }));
  current.progress_percent = 36;
  appendEvent(taskId, 'scene_plan_ready', {
    message: `已挑选 ${scenes.length} 个震撼场景，开始并发生成图片`,
    progress_percent: 36,
    scenes,
    task: taskView(current),
  });
  persist();
}

async function runImages(taskId) {
  // ComfyUI 出图要独占 GPU，而锁按设计不排队：多个 worker 并发时，除了拿到锁的
  // 那个，其余场景会立刻以「显卡正被…占用」失败（3 并发实测只有 1 张能出图，
  // 重试同样复现）。走真实渠道/占位图时不占 GPU，可以保持并发。
  const comfy = await comfyStatus().catch(() => null);
  const concurrency = comfy?.running ? 1 : Math.max(1, Math.min(8, NOVELVIZ_IMAGE_CONCURRENCY));
  let cursor = 0;

  const worker = async () => {
    for (;;) {
      const task = findTask(taskId);
      if (!task || task.status !== 'running') return;
      const index = cursor;
      cursor += 1;
      const image = task.images[index];
      if (!image) return;
      if (image.status === 'succeeded') continue;
      await generateSceneImage(taskId, image);
    }
  };

  await Promise.all(Array.from({length: Math.min(concurrency, findTask(taskId)?.images.length || 1)}, worker));

  const task = findTask(taskId);
  if (!task || task.status !== 'running') return;
  const failed = task.images.filter((image) => image.status !== 'succeeded').length;
  task.progress_percent = 100;
  task.finished_at = nowIso();
  if (failed === task.images.length && task.images.length > 0) {
    task.status = 'failed';
    task.error_message = '所有场景图片都生成失败';
    appendEvent(taskId, 'task_failed', {
      message: '所有场景图片都生成失败，可点击重试',
      progress_percent: 100,
      task: taskView(task),
    });
  } else {
    task.status = 'succeeded';
    appendEvent(taskId, 'task_done', {
      message: `场景图片生成完成：成功 ${task.images.length - failed} 张${failed ? `，失败 ${failed} 张` : ''}`,
      progress_percent: 100,
      task: taskView(task),
    });
  }
  persist();
}

async function generateSceneImage(taskId, image) {
  const task = findTask(taskId);
  if (!task) return;
  image.status = 'running';
  image.attempts = (image.attempts || 0) + 1;
  const completed = task.images.filter((item) => item.status === 'succeeded').length;
  const total = task.images.length || 1;
  const progress = Math.min(96, 36 + Math.round((completed / total) * 58));
  appendEvent(taskId, 'image_started', {
    message: `正在生成场景 ${image.scene_number}：${image.title}`,
    progress_percent: progress,
    scene: sceneImageView(image),
  });
  persist();

  try {
    const prompt = [
      'Create a cinematic, photorealistic concept image for this novel scene.',
      'Style: live-action film still, realistic human scale, dramatic natural lighting,',
      'tactile materials, atmospheric depth, physically plausible camera lens.',
      `Scene title: ${image.title}`,
      `Chapter: ${image.chapter}`,
      `Story meaning: ${image.story_significance}`,
      `Visual description: ${image.visual_prompt}`,
    ].join('\n');

    // 生图通路：本机 ComfyUI 在跑就优先走 Comfy（真实出图），否则退回占位图/真实渠道。
    // 与生图/TTS 任务共用 GPU 互斥锁，避免同一块显存被两边同时抢。
    let result;
    let engineNote = '';
    const comfy = await comfyStatus().catch(() => null);
    if (comfy?.running) {
      const releaseGpu = acquireGpu('comfy', `小说绘卷场景 ${image.scene_number}`, `novelviz:${taskId}`);
      if (!releaseGpu) {
        throw new Error(`显卡正被${gpuLockHolder()?.owner === 'tts' ? ' TTS 语音合成' : '其他生图任务'}占用（${gpuLockHolder()?.label || ''}），本场景稍后可重试`);
      }
      try {
        const defaultModel = comfy.models?.[0]?.id || 'sixstep';
        const comfyResult = await generateWithComfy({
          prompt,
          sizeCode: '1024x1024',
          model: defaultModel,
          onProgress: null,
        });
        result = {buffer: comfyResult.buffer, mimeType: comfyResult.mimeType, source: 'comfy'};
        engineNote = `ComfyUI · ${comfy.models?.[0]?.label || defaultModel}`;
      } finally {
        releaseGpu();
      }
    } else {
      const fallback = await generateImage({prompt, sizeCode: '1024x1024', qualityCode: 'high'});
      result = fallback;
      engineNote = fallback.source === 'provider' ? '真实渠道' : '本地占位生图';
    }
    task.channel_name = engineNote;
    const token = saveMedia(result.buffer, {
      mimeType: result.mimeType,
      name: `novel-${taskId}-scene-${image.scene_number}`,
    });
    image.token = token;
    image.status = 'succeeded';
    image.finished_at = nowIso();
    const done = task.images.filter((item) => item.status === 'succeeded').length;
    appendEvent(taskId, 'image_done', {
      message: `场景 ${image.scene_number} 已完成`,
      progress_percent: Math.min(98, 36 + Math.round((done / total) * 60)),
      scene: sceneImageView(image),
    });
  } catch (error) {
    image.status = 'failed';
    image.error_message = error.message;
    image.finished_at = nowIso();
    appendEvent(taskId, 'image_failed', {
      message: `场景 ${image.scene_number} 生成失败：${error.message}`,
      progress_percent: Math.min(98, 36 + Math.round((completed / total) * 60)),
      scene: sceneImageView(image),
    });
  }
  persist();
  await sleep(200);
}
