// ComfyUI 本地生图执行器（仅本地开发者模式使用，线上 Go 服务没有这套接口）。
//
// 契约来源：桌面 Comfy\doc（ComfyUI 原生接口 + 三个 API 格式工作流模板）。
//   POST /prompt            提交工作流 → {prompt_id}
//   GET  /history/{id}      轮询状态与产物
//   GET  /view              取回 PNG 字节流
//   POST /upload/image      参考图落到 ComfyUI 的 input/
//   GET  /system_stats      存活探测（含内存/显存读数）
//   POST /interrupt         打断当前执行
//   POST /free              卸载模型释放内存
//
// 三个模型共用 Qwen-Image-2.1 图框架，模板里只差主模型与采样方式；
// 参考图不走单独的图生图模型，而是在同一张图上就地改造（LoadImage 901+ →
// 编码器 images.image_N → 采样 latent 换成编码器第 3 个输出）。
import fs from 'node:fs';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';

import {COMFY, DATA_DIR} from './config.mjs';
import {nowIso} from './store.mjs';

const WORKFLOW_DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), 'comfy', 'workflows');
const POLL_INTERVAL_MS = 1500;
const PROBE_TIMEOUT_MS = 2500;
const MAX_REFERENCE_IMAGES = 16;

export class ComfyError extends Error {}

// ── 模型定义（id 是前后端与工作流模板之间的稳定契约）──

export const COMFY_MODELS = [
  {
    id: 'sixstep',
    label: '6 步蒸馏',
    note: '最快（约 15 秒/张），固定 6 步调度，不支持改步数',
    unet: 'Qwen-Image-2.1-viggle-turbo-v0.3-6step-int8_convrot.safetensors',
    steps_mode: 'fixed',
    default_steps: null,
    max_steps: null,
  },
  {
    id: 'official',
    label: '官方 Qwen-Image 2.1',
    note: '官方模型，CFG 1，25 / 35 步可选',
    unet: 'qwen_image_2.1_int8_convrot.safetensors',
    steps_mode: 'selectable',
    default_steps: 25,
    max_steps: 35,
  },
  {
    id: 'turbo',
    label: '官方 Turbo 8 步',
    note: '官方 Turbo，固定 8 步日程，CFG 关闭（约 20 秒/张）',
    unet: 'qwen-image-2.1-turbo-int8-convrot.safetensors',
    steps_mode: 'fixed',
    default_steps: null,
    max_steps: null,
  },
  {
    id: 'uncensored',
    label: '无审查 NoctQ V4',
    note: '无审查模型，CFG 3，25 / 35 步可选',
    unet: 'NoctQ_V4_int8_convrot.safetensors',
    steps_mode: 'selectable',
    default_steps: 25,
    max_steps: 35,
  },
];

const MODEL_SPECS = {
  sixstep: {
    template: 'sixstep-t2i.json',
    encoder: '17',
    // 6 步这条链上 latent 有两个消费者：sigma 调度和采样器本体，都要换。
    latent_consumers: [['21', 'latent'], ['22', 'latent_image']],
    sampler: null,
  },
  turbo: {
    template: 'turbo-t2i.json',
    encoder: '17',
    // 与 6 步同族的自定义采样链，但 sigma 是纯日程（ManualSigmas）没有 latent 输入，
    // 参考图只需要换采样器本体这一个消费者。
    latent_consumers: [['22', 'latent_image']],
    sampler: null,
  },
  official: {
    template: 'official-t2i.json',
    encoder: '4',
    latent_consumers: [['6', 'latent_image']],
    sampler: '6',
  },
  uncensored: {
    template: 'uncensored-t2i.json',
    encoder: '4',
    latent_consumers: [['6', 'latent_image']],
    sampler: '6',
  },
};

export function findComfyModel(modelId) {
  return COMFY_MODELS.find((model) => model.id === modelId) || null;
}

export function comfyModelList() {
  return COMFY_MODELS.map((model) => ({...model}));
}

/**
 * 让 ComfyUI 卸载模型并归还内存（POST /free）。
 * 出图后模型会一直驻留（编码器 8.9G + 主模型 6.9G + VAE 0.6G），
 * 工作台的「释放内存」按钮与出图后的常驻提示都走这里。
 */
export async function freeComfyMemory() {
  let response;
  try {
    response = await fetchWithTimeout(
      `${COMFY.baseURL}/free`,
      {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({unload_models: true, free_memory: true}),
      },
      30000,
    );
  } catch (error) {
    throw new ComfyError(`释放内存失败：${describeFetchError(error)}`);
  }
  if (!response.ok) {
    throw new ComfyError(`释放内存失败（HTTP ${response.status}）`);
  }
  // 释放后再读一次状态，前端可以直接用返回里的新读数刷新显示。
  const status = await comfyStatus();
  return {freed: true, status};
}

// ── 工作流模板（随仓库版本化，运行时深拷贝后改写）──

const templateCache = new Map();

function loadTemplate(fileName) {
  if (!templateCache.has(fileName)) {
    const absolute = path.join(WORKFLOW_DIR, fileName);
    templateCache.set(fileName, JSON.parse(fs.readFileSync(absolute, 'utf8')));
  }
  return templateCache.get(fileName);
}

/** 尺寸统一压到 32 的倍数：Qwen 工作流对宽高敏感。 */
function quantize(value, multiple = 32) {
  return Math.max(multiple, Math.round(Number(value) / multiple) * multiple);
}

function randomSeed() {
  return Math.floor(Math.random() * (2 ** 31 - 1)) + 1;
}

function parseSizeCode(sizeCode) {
  const match = /^(\d{2,4})x(\d{2,4})$/.exec(String(sizeCode || ''));
  if (!match) return {width: 1024, height: 1024};
  return {width: Number.parseInt(match[1], 10), height: Number.parseInt(match[2], 10)};
}

/**
 * 组装要提交给 ComfyUI 的工作流：
 *   文生图 = 模板改提示词/种子/步数 + 空 latent 写宽高；
 *   图生图 = 在文生图框架上加 LoadImage 节点，latent 换成参考图编码结果。
 * 出图比例跟第一张参考图走（resolution 是参考图的边长预算），与所选比例无关。
 */
export function buildComfyWorkflow({
  model,
  prompt,
  negativePrompt = '',
  width,
  height,
  seed,
  steps = null,
  referenceNames = [],
}) {
  const spec = MODEL_SPECS[model];
  if (!spec) throw new ComfyError(`未知的 Comfy 模型：${model}`);

  const workflow = JSON.parse(JSON.stringify(loadTemplate(spec.template)));
  const encoder = workflow[spec.encoder]?.inputs;
  if (!encoder) throw new ComfyError(`工作流模板缺少编码器节点 ${spec.encoder}`);

  encoder.prompt = prompt;
  encoder.negative_prompt = negativePrompt;

  const names = referenceNames
    .filter((name) => typeof name === 'string' && name.length > 0)
    .slice(0, MAX_REFERENCE_IMAGES);

  if (names.length > 0) {
    names.forEach((name, index) => {
      const nodeId = String(900 + index + 1);
      workflow[nodeId] = {class_type: 'LoadImage', inputs: {image: name, upload: 'image'}};
      encoder[`images.image_${index + 1}`] = [nodeId, 0];
    });
    for (const [nodeId, field] of spec.latent_consumers) {
      if (workflow[nodeId]?.inputs) workflow[nodeId].inputs[field] = [spec.encoder, 2];
    }
    if (!encoder.vae) {
      const vaeEntry = Object.entries(workflow).find(([, node]) => node?.class_type === 'VAELoader');
      if (vaeEntry) encoder.vae = [vaeEntry[0], 0];
    }
    encoder.resolution = quantize(Math.sqrt(width * height));
  } else {
    const latent = Object.values(workflow).find((node) => node?.class_type === 'EmptyLatentImage');
    if (latent?.inputs) {
      latent.inputs.width = quantize(width);
      latent.inputs.height = quantize(height);
      latent.inputs.batch_size = 1;
    }
  }

  // 种子写在所有声明了 seed / noise_seed 的节点上（6 步模型用的是 RandomNoise.noise_seed）。
  for (const node of Object.values(workflow)) {
    if (!node?.inputs) continue;
    if (node.inputs.seed !== undefined) node.inputs.seed = seed;
    if (node.inputs.noise_seed !== undefined) node.inputs.noise_seed = seed;
  }

  // 步数只对普通 KSampler 生效；6 步那条是固定调度，没有 KSampler，自动跳过。
  if (spec.sampler && steps) {
    const sampler = workflow[spec.sampler]?.inputs;
    if (sampler?.steps !== undefined) sampler.steps = steps;
  }

  return workflow;
}

// ── HTTP 工具 ──

function fetchWithTimeout(url, options = {}, timeoutMs = PROBE_TIMEOUT_MS) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  if (typeof timer.unref === 'function') timer.unref();
  return fetch(url, {...options, signal: controller.signal}).finally(() => clearTimeout(timer));
}

function describeFetchError(error) {
  if (error?.name === 'AbortError') return '请求超时';
  const code = error?.cause?.code || error?.code || '';
  if (code === 'ECONNREFUSED' || /ECONNREFUSED/.test(String(error?.message || ''))) return '连接被拒绝（ComfyUI 未启动）';
  if (code === 'ENOTFOUND' || code === 'EAI_AGAIN') return '地址无法解析';
  return error?.message || '无法连接';
}

function sleep(ms) {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, Math.max(0, ms));
    if (typeof timer.unref === 'function') timer.unref();
  });
}

function summarizeStats(stats) {
  const devices = Array.isArray(stats?.devices) ? stats.devices : [];
  return {
    comfyui_version: stats?.system?.comfyui_version || '',
    ram_total: Number(stats?.system?.ram_total || 0),
    ram_free: Number(stats?.system?.ram_free || 0),
    devices: devices.map((device) => ({
      name: device?.name || '设备',
      vram_total: Number(device?.vram_total || 0),
      vram_free: Number(device?.vram_free || 0),
    })),
  };
}

/** 探测 ComfyUI 是否可用；不可用时也返回完整结构，供前端展示与引导。 */
export async function comfyStatus() {
  const baseInfo = {
    base_url: COMFY.baseURL,
    start_command_configured: Boolean(COMFY.startCmd),
    timeout_ms: COMFY.timeoutMs,
    free_memory: COMFY.freeMemory,
    models: comfyModelList(),
  };
  try {
    const response = await fetchWithTimeout(`${COMFY.baseURL}/system_stats`, {}, PROBE_TIMEOUT_MS);
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const stats = await response.json();
    return {...baseInfo, running: true, error: '', stats: summarizeStats(stats)};
  } catch (error) {
    return {...baseInfo, running: false, error: describeFetchError(error), stats: null};
  }
}

// ── ComfyUI 日志与异常检测 ──
//
// ComfyUI 自己的报错（自定义节点、Manager 联网失败、Traceback……）不会进入任务的
// status，只写进服务端日志。这里通过 /internal/logs/raw 读回日志并抽出 ERROR/WARNING，
// 让任务失败时和设置界面都能看到「ComfyUI 侧到底报了什么」，而不是只显示「任务失败」。

const ANSI_RE = /\u001b\[[0-9;]*m/g;
const MAX_LOG_ENTRIES = 4000;

function stripAnsi(text) {
  return String(text || '').replace(ANSI_RE, '');
}

async function readComfyLogEntries() {
  let response = await fetchWithTimeout(`${COMFY.baseURL}/internal/logs/raw`, {}, 6000);
  if (!response.ok) {
    // 旧版 ComfyUI 只有 /internal/logs，返回的是纯文本
    response = await fetchWithTimeout(`${COMFY.baseURL}/internal/logs`, {}, 6000);
    if (!response.ok) throw new ComfyError(`读取 ComfyUI 日志失败（HTTP ${response.status}）`);
    const text = await response.text();
    return text
      .split(/\r?\n/)
      .map((line) => ({t: '', m: line}))
      .slice(-MAX_LOG_ENTRIES);
  }
  const payload = await response.json();
  const entries = Array.isArray(payload?.entries) ? payload.entries : [];
  return entries.slice(-MAX_LOG_ENTRIES).map((entry) => ({t: entry?.t || '', m: entry?.m || ''}));
}

/** 从 ComfyUI 日志里抽出最近的 ERROR / WARNING（含 Traceback 的最后一行异常）。 */
export function extractComfyIssues(entries, {limit = 8} = {}) {
  const issues = [];
  const levelOf = (line) => {
    const match = /\[(ERROR|WARNING)\]\s*(.*)$/.exec(line);
    if (!match) return null;
    const message = match[2].trim();
    if (!message) return null;
    return {level: match[1] === 'ERROR' ? 'error' : 'warning', message};
  };

  for (let index = 0; index < entries.length; index += 1) {
    const line = stripAnsi(entries[index]?.m).trimEnd();
    const parsed = levelOf(line);
    if (parsed) {
      issues.push({...parsed, at: entries[index]?.t || ''});
      continue;
    }
    if (!/^Traceback \(most recent call last\):/.test(line.trim()) && !/^Exception in thread /.test(line.trim())) {
      continue;
    }
    // Traceback 的最后一非空行才是真正的异常。日志条目里帧与帧之间夹着空行，
    // 所以跳过空行继续找，遇到下一条日志级条目（[INFO]/[WARNING]/[ERROR]）才停。
    let detail = line.trim();
    for (let cursor = index + 1; cursor < entries.length && cursor <= index + 60; cursor += 1) {
      const next = stripAnsi(entries[cursor]?.m).trim();
      if (!next) continue;
      if (/^\[(INFO|WARNING|ERROR)\]/.test(next)) break;
      if (/^Traceback \(most recent call last\):/.test(next)) break;
      detail = next;
    }
    issues.push({level: 'error', message: `Traceback: ${detail}`, at: entries[index]?.t || ''});
  }

  const deduped = [];
  const seen = new Set();
  for (let index = issues.length - 1; index >= 0; index -= 1) {
    const issue = issues[index];
    const key = `${issue.level}:${issue.message}`;
    if (seen.has(key)) continue;
    seen.add(key);
    deduped.unshift(issue);
  }
  return deduped.slice(-limit);
}

/**
 * 读取 ComfyUI 日志并汇总异常；ComfyUI 未运行或版本不支持日志接口时也返回结构。
 * @returns {{available: boolean, comfyui_version: string, total_lines: number, errors: number, warnings: number, issues: Array}}
 */
export async function readComfyLog({limit = 8} = {}) {
  try {
    const entries = await readComfyLogEntries();
    const issues = extractComfyIssues(entries, {limit});
    return {
      available: true,
      total_lines: entries.length,
      errors: issues.filter((issue) => issue.level === 'error').length,
      warnings: issues.filter((issue) => issue.level === 'warning').length,
      issues,
    };
  } catch (error) {
    return {
      available: false,
      total_lines: 0,
      errors: 0,
      warnings: 0,
      issues: [],
      error: error instanceof ComfyError ? error.message : describeFetchError(error),
    };
  }
}

/** 把最近的 ComfyUI 日志异常拼进错误信息，让任务失败时能直接看到服务端报了什么。 */
async function describeRecentComfyIssues(maxItems = 2) {
  const report = await readComfyLog({limit: maxItems});
  if (!report.issues.length) return '';
  const text = report.issues.map((issue) => `[${issue.level}] ${issue.message}`).join('；');
  return `\nComfyUI 最近日志：${text}`;
}

/** 启动 ComfyUI：执行 COMFY_START_CMD，日志写 local-data/comfy-start.log，随后由前端轮询状态。 */
export async function startComfy() {
  const status = await comfyStatus();
  if (status.running) {
    return {started: false, already_running: true, status};
  }
  if (!COMFY.startCmd) {
    throw new ComfyError(
      '未配置 ComfyUI 启动命令：请在仓库根目录的 .env.local 里设置 COMFY_START_CMD（可指向 start-comfyui.bat），或手动启动 ComfyUI 后刷新状态。',
    );
  }

  fs.mkdirSync(DATA_DIR, {recursive: true});
  const logFile = path.join(DATA_DIR, 'comfy-start.log');
  const logFd = fs.openSync(logFile, 'a');
  fs.writeSync(logFd, `\n[${nowIso()}] 启动 ComfyUI：${COMFY.startCmd}\n`);

  let child;
  try {
    child = spawnComfyCommand(COMFY.startCmd, logFd);
  } finally {
    fs.closeSync(logFd);
  }
  child.unref();

  // 命令本身有问题（脚本不存在、闪退）时 5 秒内就会退出，把日志尾部一起报出去。
  const exitCode = await new Promise((resolve) => {
    const timer = setTimeout(() => resolve(null), 5000);
    child.once('exit', (code) => {
      clearTimeout(timer);
      resolve(code === null ? -1 : code);
    });
    child.once('error', () => {
      clearTimeout(timer);
      resolve(-1);
    });
  });

  if (exitCode !== null) {
    const tail = readLogTail(logFile);
    throw new ComfyError(
      `启动命令提前退出（exit ${exitCode}），请检查 COMFY_START_CMD。日志：${logFile}${tail ? `\n${tail}` : ''}`,
    );
  }

  // 命令跑起来了但 ComfyUI 可能仍在报错（端口占用、依赖缺失）：启动后带一次日志异常提示。
  const issues = await describeRecentComfyIssues(2);
  return {started: true, already_running: false, status: await comfyStatus(), comfy_issues: issues.trim()};
}

function spawnComfyCommand(command, logFd) {
  const trimmed = command.trim();
  const stdio = ['ignore', logFd, logFd];
  // 值是真实存在的文件（脚本/可执行文件）时用参数数组启动，路径里的空格不会被 shell 拆开。
  if (fs.existsSync(trimmed)) {
    if (process.platform === 'win32') {
      return spawn('cmd.exe', ['/d', '/c', trimmed], {detached: true, windowsHide: true, stdio});
    }
    return spawn(trimmed, [], {detached: true, stdio});
  }
  return spawn(trimmed, {shell: true, detached: true, windowsHide: true, stdio});
}

function readLogTail(logFile, maxLines = 12) {
  try {
    const lines = fs.readFileSync(logFile, 'utf8').trimEnd().split(/\r?\n/);
    return lines.slice(-maxLines).join('\n');
  } catch {
    return '';
  }
}

/** 打断当前执行（ComfyUI 的 /interrupt）。 */
export async function interruptComfy() {
  try {
    const response = await fetchWithTimeout(`${COMFY.baseURL}/interrupt`, {method: 'POST'}, 4000);
    return {interrupted: response.ok};
  } catch {
    return {interrupted: false};
  }
}

/**
 * 释放内存按钮（Comfy 侧）：POST /free 卸载模型并清空显存缓存。
 * ComfyUI 未运行时视为已释放；GPU 锁被占用时不执行（任务还在跑）。
 */
export async function releaseComfyMemory() {
  const {gpuLockHolder} = await import('./gpu.mjs');
  const holder = gpuLockHolder();
  if (holder) {
    return {released: false, running: true, reason: `有任务正在使用显卡（${holder.label || holder.owner}），完成后再释放。`, holder};
  }
  const status = await comfyStatus();
  if (!status.running) {
    return {released: true, running: false, reason: 'ComfyUI 未运行，显存已空闲。'};
  }
  try {
    const response = await fetchWithTimeout(`${COMFY.baseURL}/free`, {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({unload_models: true, free_memory: true}),
    }, 15000);
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    return {released: true, running: true, reason: '已请求 ComfyUI 卸载模型并释放显存。'};
  } catch (error) {
    return {released: false, running: true, reason: `释放失败：${error.message}（ComfyUI 可能正忙，稍后再试。）`};
  }
}

/** 取消一个已提交的 ComfyUI 任务：先从队列删除（排队中），再打断（执行中）。 */
export async function cancelComfyPrompt(promptId) {
  if (!promptId) return;
  try {
    await fetchWithTimeout(
      `${COMFY.baseURL}/queue`,
      {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({delete: [promptId]}),
      },
      4000,
    );
  } catch {
    // 队列删除失败不影响下面的打断
  }
  await interruptComfy();
}

// ── 生图 ──

async function uploadReferenceImage({file, mimeType, name}) {
  const bytes = fs.readFileSync(file);
  const extension = path.extname(name || '') || '.png';
  const safeStem = String(name || 'reference').replace(/\.[^.]+$/, '').replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]+/g, '-').slice(0, 40) || 'reference';
  const uploadName = `mirror-${Date.now().toString(36)}-${safeStem}${extension}`;
  const form = new FormData();
  form.append('image', new Blob([new Uint8Array(bytes)], {type: mimeType || 'image/png'}), uploadName);
  form.append('overwrite', 'true');
  form.append('type', 'input');
  const response = await fetchWithTimeout(`${COMFY.baseURL}/upload/image`, {method: 'POST', body: form}, 60000);
  const text = await response.text();
  if (!response.ok) {
    throw new ComfyError(`上传参考图到 ComfyUI 失败（HTTP ${response.status}）：${text.slice(0, 300)}`);
  }
  const uploaded = JSON.parse(text);
  return uploaded.subfolder ? `${uploaded.subfolder}/${uploaded.name}` : uploaded.name;
}

function describeHistoryFailure(entry) {
  if (entry?.status?.status_str !== 'error') return null;
  const messages = Array.isArray(entry.status.messages) ? entry.status.messages : [];
  const detail = messages
    .filter(([event]) => event === 'execution_error')
    .map(([, data]) => data?.exception_message || data?.exception_type || JSON.stringify(data))
    .join('; ');
  return detail || 'ComfyUI 执行报错（详情见 ComfyUI 控制台）';
}

function describeSubmitFailure(text) {
  try {
    const parsed = JSON.parse(text);
    if (parsed?.node_errors && Object.keys(parsed.node_errors).length) {
      return Object.entries(parsed.node_errors)
        .map(([nodeId, info]) => {
          const errors = Array.isArray(info?.errors) ? info.errors : [];
          const message = errors.map((item) => item?.message || item?.details || '').filter(Boolean).join('; ');
          return `节点 ${nodeId}（${info?.class_type || '未知类型'}）：${message || '校验失败'}`;
        })
        .join('；');
    }
    if (parsed?.error) return JSON.stringify(parsed.error).slice(0, 800);
  } catch {
    // 原样返回文本
  }
  return text.slice(0, 800);
}

/** 从 PNG 字节流读宽高（ComfyUI 产物固定是 PNG）。 */
function pngDimensions(buffer) {
  if (buffer.length < 24) return null;
  const signature = buffer.subarray(0, 8).toString('hex');
  if (signature !== '89504e470d0a1a0a') return null;
  if (buffer.subarray(12, 16).toString('ascii') !== 'IHDR') return null;
  return {width: buffer.readUInt32BE(16), height: buffer.readUInt32BE(20)};
}

async function fetchOutputImages(entry) {
  const images = [];
  for (const output of Object.values(entry.outputs || {})) {
    for (const image of output.images || []) {
      const params = new URLSearchParams({
        filename: image.filename,
        subfolder: image.subfolder || '',
        type: image.type || 'output',
      });
      const response = await fetchWithTimeout(`${COMFY.baseURL}/view?${params.toString()}`, {}, 60000);
      if (!response.ok) {
        throw new ComfyError(`取回产物失败（HTTP ${response.status}）：${image.filename}`);
      }
      images.push(Buffer.from(await response.arrayBuffer()));
    }
  }
  return images;
}

/**
 * 执行一次 ComfyUI 生图。
 * @returns {{buffer: Buffer, mimeType: string, source: string, requestId: string, width: number, height: number}}
 */
export async function generateWithComfy({
  prompt,
  negativePrompt = '',
  sizeCode,
  model,
  steps = null,
  seed = null,
  reference = null,
  onProgress = null,
  onPromptId = null,
}) {
  const status = await comfyStatus();
  if (!status.running) {
    throw new ComfyError(
      `ComfyUI 未运行（${COMFY.baseURL}）：请在工作台的「Comfy 生图 → 设置」里点击「启动 ComfyUI」，或手动运行启动脚本。`,
    );
  }

  const {width, height} = parseSizeCode(sizeCode);
  const chosenSeed = Number.isFinite(seed) && seed > 0 ? Math.floor(seed) : randomSeed();

  const referenceNames = [];
  if (reference?.file) {
    onProgress?.(20);
    referenceNames.push(await uploadReferenceImage(reference));
  }

  const workflow = buildComfyWorkflow({
    model,
    prompt,
    negativePrompt,
    width,
    height,
    seed: chosenSeed,
    steps,
    referenceNames,
  });

  if (COMFY.freeMemory) {
    try {
      await fetchWithTimeout(`${COMFY.baseURL}/free`, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({unload_models: true, free_memory: true}),
      }, 10000);
    } catch {
      // 释放失败不影响本次生图
    }
  }

  onProgress?.(30);
  const clientId = `mirror-studio-${Date.now().toString(36)}`;
  const submit = await fetchWithTimeout(
    `${COMFY.baseURL}/prompt`,
    {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({prompt: workflow, client_id: clientId}),
    },
    20000,
  );
  const submitText = await submit.text();
  if (!submit.ok) {
    throw new ComfyError(`ComfyUI 拒绝了工作流（HTTP ${submit.status}）：${describeSubmitFailure(submitText)}`);
  }
  const {prompt_id: promptId} = JSON.parse(submitText);
  if (!promptId) throw new ComfyError('ComfyUI 没有返回 prompt_id，提交失败');
  onPromptId?.(promptId);

  const startedAt = Date.now();
  const deadline = startedAt + COMFY.timeoutMs;
  let entry = null;
  while (Date.now() < deadline) {
    await sleep(POLL_INTERVAL_MS);
    try {
      const response = await fetchWithTimeout(`${COMFY.baseURL}/history/${promptId}`, {}, 8000);
      if (response.ok) {
        const history = await response.json();
        entry = history?.[promptId] || null;
        if (entry) {
          const failure = describeHistoryFailure(entry);
          if (failure) throw new ComfyError(`ComfyUI 执行失败：${failure}${await describeRecentComfyIssues()}`);
          if (entry.status?.completed || entry.outputs) break;
        }
      }
    } catch (error) {
      if (error instanceof ComfyError) throw error;
      // 单次轮询失败继续等待
    }
    // 进度条按「3 分钟内从 45% 爬到 88%」推进：真实耗时大多在十几秒到几分钟，动起来才有反馈感。
    const pacing = Math.min(1, (Date.now() - startedAt) / Math.min(COMFY.timeoutMs, 3 * 60 * 1000));
    onProgress?.(Math.min(88, 45 + Math.round(pacing * 43)));
  }

  if (!entry?.outputs) {
    throw new ComfyError(
      `ComfyUI 任务超时（${Math.round(COMFY.timeoutMs / 1000)} 秒）仍未产出图片${await describeRecentComfyIssues()}`,
    );
  }

  const images = await fetchOutputImages(entry);
  if (!images.length) throw new ComfyError('ComfyUI 任务完成但没有产物图片');
  onProgress?.(95);

  const dimensions = pngDimensions(images[0]) || {width: quantize(width), height: quantize(height)};
  return {
    buffer: images[0],
    mimeType: 'image/png',
    source: 'comfy',
    requestId: promptId,
    width: dimensions.width,
    height: dimensions.height,
  };
}
