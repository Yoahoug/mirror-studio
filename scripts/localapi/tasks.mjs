// 生图任务流水线：内存里排队，后台串行执行，结果落到 local-data/media。
// 状态、进度和字段命名与线上契约一致（queued → running → succeeded/failed/canceled）。
// engine="comfy" 的任务走本机 ComfyUI（仅本地模式），其余走占位图/真实渠道。
import fs from 'node:fs';

import {cancelComfyPrompt, findComfyModel, generateWithComfy} from './comfy.mjs';
import {IMAGE_DELAY_MS, IMAGE_SIZE} from './config.mjs';
import {generateImage} from './provider.mjs';
import {consumeQuota, downloadURL, localUser, mediaByToken, newTaskNo, nextId, nowIso, persist, saveMedia, store} from './store.mjs';

const SIZE_PATTERN = /^(\d{2,4})x(\d{2,4})$/;
const ALLOWED_QUALITIES = new Set(['auto', 'low', 'medium', 'high']);
const MAX_EDGE = 2048;

export class ValidationError extends Error {}

export function normalizeSizeCode(sizeCode) {
  const raw = sizeCode === undefined || sizeCode === null || sizeCode === '' ? IMAGE_SIZE : String(sizeCode);
  const normalized = raw.trim().toLowerCase();
  const match = SIZE_PATTERN.exec(normalized);
  if (!match) throw new ValidationError('不支持的尺寸');
  const width = Number.parseInt(match[1], 10);
  const height = Number.parseInt(match[2], 10);
  if (width % 16 !== 0 || height % 16 !== 0) throw new ValidationError('尺寸宽高必须是 16px 的倍数');
  if (width > MAX_EDGE || height > MAX_EDGE) throw new ValidationError('尺寸最大边不能超过 2048px');
  const shortEdge = Math.min(width, height);
  const longEdge = Math.max(width, height);
  if (shortEdge <= 0 || longEdge / shortEdge > 3) throw new ValidationError('尺寸长短边比例不能超过 3:1');
  return `${width}x${height}`;
}

function normalizeQualityCode(qualityCode) {
  const normalized = qualityCode ? String(qualityCode).trim().toLowerCase() : 'auto';
  if (!ALLOWED_QUALITIES.has(normalized)) throw new ValidationError('不支持的质量');
  return normalized;
}

/** 把上传会话/内联 data URL 得到的引用图统一登记成一个 media token。 */
export function registerReference({token = '', dataURL = '', filename = ''}) {
  if (token) {
    const upload = store.referenceUploads[token];
    if (!upload) return null;
    return {token: upload.mediaToken, name: upload.filename};
  }
  if (dataURL) {
    const match = /^data:([^;,]+)?(;base64)?,(.*)$/s.exec(String(dataURL));
    if (!match) throw new ValidationError('参考图格式不支持');
    const mimeType = match[1] || 'image/png';
    const buffer = Buffer.from(match[3] || '', match[2] ? 'base64' : 'utf8');
    if (!buffer.length) throw new ValidationError('参考图为空');
    const mediaToken = saveMedia(buffer, {mimeType, name: filename || 'reference-image.png'});
    persist();
    return {token: mediaToken, name: filename || 'reference-image.png'};
  }
  return null;
}

export function createTask(payload) {
  const prompt = String(payload.prompt ?? '').trim();
  if (!prompt) throw new ValidationError('请输入生图描述');
  if (prompt.length > 4000) throw new ValidationError('生图描述过长');

  const generationMode = payload.generation_mode === 'image_to_image' ? 'image_to_image' : 'text_to_image';
  const sizeCode = normalizeSizeCode(payload.size_code);
  const qualityCode = normalizeQualityCode(payload.quality_code);

  // Comfy 专属参数：engine="comfy" 时任务改走本机 ComfyUI。
  const engine = payload.engine === 'comfy' ? 'comfy' : '';
  let comfyModel = '';
  let comfyModelLabel = '';
  let steps = null;
  let seed = null;
  if (engine === 'comfy') {
    comfyModel = String(payload.comfy_model || '').trim() || 'sixstep';
    const spec = findComfyModel(comfyModel);
    if (!spec) throw new ValidationError('不支持的 Comfy 模型');
    comfyModelLabel = spec.label;
    if (spec.steps_mode === 'selectable') {
      if (payload.steps === undefined || payload.steps === null || payload.steps === '') {
        steps = spec.default_steps;
      } else {
        const parsedSteps = Number.parseInt(payload.steps, 10);
        if (!Number.isFinite(parsedSteps) || parsedSteps < 1 || parsedSteps > spec.max_steps) {
          throw new ValidationError(`步数需要是 1-${spec.max_steps} 之间的整数`);
        }
        steps = parsedSteps;
      }
    }
    if (payload.seed !== undefined && payload.seed !== null && payload.seed !== '') {
      const parsedSeed = Number.parseInt(payload.seed, 10);
      if (!Number.isFinite(parsedSeed) || parsedSeed <= 0) throw new ValidationError('种子需要是正整数');
      seed = parsedSeed;
    }
  }

  let reference = null;
  if (payload.reference_upload_token || payload.reference_image_data_url) {
    reference = registerReference({
      token: String(payload.reference_upload_token || '').trim(),
      dataURL: String(payload.reference_image_data_url || ''),
      filename: String(payload.reference_image_name || ''),
    });
    if (!reference && generationMode === 'image_to_image') {
      throw new ValidationError('参考图上传结果不存在或已过期，请重新选择图片');
    }
  }
  if (generationMode === 'image_to_image' && !reference) {
    throw new ValidationError('图生图需要先上传一张参考图');
  }

  const now = nowIso();
  const task = {
    id: nextId('image'),
    task_no: newTaskNo(),
    user_id: localUser().id,
    status: 'queued',
    progress_percent: 0,
    generation_mode: generationMode,
    prompt,
    negative_prompt: String(payload.negative_prompt ?? '').trim(),
    style_code: String(payload.style_code ?? ''),
    size_code: sizeCode,
    quality_code: qualityCode,
    channel_name: engine === 'comfy' ? `ComfyUI · ${comfyModelLabel}` : '本地占位生图',
    engine,
    comfy_model: comfyModel,
    comfy_prompt_id: '',
    steps,
    seed,
    reference_image_name: reference?.name || '',
    reference_token: reference?.token || '',
    error_code: '',
    error_message: '',
    provider_request_id: '',
    quota_cost: 1,
    submitted_at: now,
    started_at: null,
    finished_at: null,
    images: [],
  };
  store.tasks.unshift(task);
  persist();
  schedule(task, 0);
  return task;
}

function schedule(task, delayMs) {
  const timer = setTimeout(() => {
    run(task.task_no).catch((error) => {
      console.error(`[local-api] 生图任务 ${task.task_no} 执行失败：${error.message}`);
      failTask(task.task_no, error.message);
    });
  }, Math.max(0, delayMs));
  if (typeof timer.unref === 'function') timer.unref();
}

async function run(taskNo) {
  const task = findTask(taskNo);
  if (!task || task.status !== 'queued') return;

  const isComfy = task.engine === 'comfy';
  task.status = 'running';
  task.progress_percent = 15;
  task.started_at = nowIso();
  persist();

  const updateProgress = (percent) => {
    const current = findTask(taskNo);
    if (!current || current.status !== 'running') return;
    current.progress_percent = Math.max(current.progress_percent || 0, Math.min(100, Math.round(percent)));
    persist();
  };

  if (!isComfy) {
    await sleep(IMAGE_DELAY_MS);
    const current = findTask(taskNo);
    if (!current || current.status === 'canceled') return;
    current.progress_percent = 55;
    persist();
  }

  const current = findTask(taskNo);
  if (!current || current.status === 'canceled') return;

  const reference = current.reference_token ? mediaByToken(current.reference_token) : null;
  const result = isComfy
    ? await generateWithComfy({
        prompt: current.prompt,
        negativePrompt: current.negative_prompt,
        sizeCode: current.size_code,
        model: current.comfy_model,
        steps: current.steps,
        seed: current.seed,
        reference: reference ? {file: reference.file, mimeType: reference.mime_type, name: reference.name} : null,
        onProgress: updateProgress,
        onPromptId: (promptId) => {
          const target = findTask(taskNo);
          if (!target) return;
          target.comfy_prompt_id = promptId;
          persist();
        },
      })
    : await generateImage({
        prompt: current.prompt,
        sizeCode: current.size_code,
        qualityCode: current.quality_code,
        negativePrompt: current.negative_prompt,
        reference: reference
          ? {dataURL: `data:${reference.mime_type};base64,${fs.readFileSync(reference.file).toString('base64')}`}
          : null,
      });

  const finalTask = findTask(taskNo);
  if (!finalTask || finalTask.status === 'canceled') return;

  const mediaToken = saveMedia(result.buffer, {
    mimeType: result.mimeType,
    name: `generated-${finalTask.task_no}`,
  });
  const fallbackDimensions = parseDimensions(finalTask.size_code);
  const [width, height] = Number.isFinite(result.width) && Number.isFinite(result.height)
    ? [result.width, result.height]
    : fallbackDimensions;
  const imageId = nextId('image');
  finalTask.images = [
    {
      id: imageId,
      token: mediaToken,
      status: 'available',
      width,
      height,
      expires_at: null,
    },
  ];
  finalTask.status = 'succeeded';
  finalTask.progress_percent = 100;
  finalTask.finished_at = nowIso();
  finalTask.provider_request_id = result.requestId;
  if (!finalTask.engine) {
    finalTask.channel_name = result.source === 'provider' ? '真实渠道' : '本地占位生图';
  }
  consumeQuota();
  persist();
}

function parseDimensions(sizeCode) {
  const match = SIZE_PATTERN.exec(String(sizeCode || ''));
  if (!match) return [1024, 1024];
  return [Number.parseInt(match[1], 10), Number.parseInt(match[2], 10)];
}

function sleep(ms) {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, Math.max(0, ms));
    if (typeof timer.unref === 'function') timer.unref();
  });
}

function failTask(taskNo, message) {
  const task = findTask(taskNo);
  if (!task || task.status === 'canceled') return;
  task.status = 'failed';
  task.error_message = message;
  task.error_code = 'local_failure';
  task.finished_at = nowIso();
  persist();
}

export function findTask(taskNo) {
  return store.tasks.find((task) => task.task_no === taskNo) || null;
}

export function cancelTask(taskNo) {
  const task = findTask(taskNo);
  if (!task) return null;
  if (task.status === 'queued' || task.status === 'running') {
    task.status = 'canceled';
    task.finished_at = nowIso();
    task.error_message = '任务已取消';
    persist();
    // Comfy 任务还要让 ComfyUI 停下：排队中的从队列删除，执行中的直接打断。
    if (task.engine === 'comfy') {
      void cancelComfyPrompt(task.comfy_prompt_id || task.provider_request_id).catch(() => {});
    }
  }
  return task;
}

export function deleteTask(taskNo) {
  const index = store.tasks.findIndex((task) => task.task_no === taskNo);
  if (index < 0) return false;
  store.tasks.splice(index, 1);
  persist();
  return true;
}

export function taskImages(task) {
  return (task.images || []).map((image) => ({
    id: image.id,
    status: image.status,
    width: image.width,
    height: image.height,
    expires_at: image.expires_at ?? null,
    preview_url: downloadURL(image.token, {preview: true}),
    download_url: downloadURL(image.token, {download: true}),
  }));
}

export function serializeTask(task) {
  return {
    task_no: task.task_no,
    task_kind: 'generation',
    status: task.status,
    progress_percent: task.progress_percent,
    generation_mode: task.generation_mode,
    prompt: task.prompt,
    negative_prompt: task.negative_prompt,
    style_code: task.style_code,
    size_code: task.size_code,
    quality_code: task.quality_code,
    engine: task.engine || '',
    comfy_model: task.comfy_model || '',
    steps: task.steps ?? null,
    seed: task.seed ?? null,
    reference_image_name: task.reference_image_name,
    reference_image_attached: Boolean(task.reference_token),
    reference_image_data_url: '',
    quota_cost: task.quota_cost,
    error_code: task.error_code || '',
    error_message: task.error_message || '',
    topic_id: null,
    submitted_at: task.submitted_at,
    started_at: task.started_at,
    finished_at: task.finished_at,
    images: taskImages(task),
  };
}
