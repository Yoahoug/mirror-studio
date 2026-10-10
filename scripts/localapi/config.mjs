// 本地模式的配置解析：进程环境 + 仓库根目录的 .env.local（可选）。
// 零配置即可启动：不读数据库、不连邮件、不需要任何密钥；只有想接真实
// 图片/对话渠道时才需要在 .env.local 里填 PROVIDER_*/LLM_*。
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

export const ROOT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

// .env.local 只做最简单的 KEY=VALUE 解析（支持 # 注释、单双引号），
// 已存在的进程环境变量优先，键名与 Go 服务的环境变量保持一致。
function loadDotEnvLocal() {
  const file = path.join(ROOT_DIR, '.env.local');
  const values = {};
  if (!fs.existsSync(file)) return values;
  for (const rawLine of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) continue;
    const separator = line.indexOf('=');
    if (separator <= 0) continue;
    const key = line.slice(0, separator).trim();
    let value = line.slice(separator + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    if (key) values[key] = value;
  }
  return values;
}

const fileEnv = loadDotEnvLocal();

function env(name, fallback = '') {
  const fromProcess = process.env[name];
  if (fromProcess !== undefined && fromProcess !== '') return fromProcess;
  const fromFile = fileEnv[name];
  if (fromFile !== undefined && fromFile !== '') return fromFile;
  return fallback;
}

function envInt(name, fallback) {
  const raw = env(name, '');
  if (!raw) return fallback;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

export const DATA_DIR = path.resolve(ROOT_DIR, env('LOCAL_DATA_DIR', 'local-data'));
export const MEDIA_DIR = path.join(DATA_DIR, 'media');
export const NOVEL_DIR = path.join(DATA_DIR, 'novel');
export const UPLOAD_DIR = path.join(DATA_DIR, 'uploads');
export const STORE_FILE = path.join(DATA_DIR, 'store.json');

export const HOST = env('LOCAL_API_HOST', '127.0.0.1');
export const PORT = envInt('LOCAL_API_PORT', 8000);

// 模拟生图耗时，让工作台的排队/生成中状态可见；0 表示立即完成。
export const IMAGE_DELAY_MS = envInt('LOCAL_IMAGE_DELAY_MS', 900);
export const IMAGE_SIZE = env('LOCAL_IMAGE_SIZE', '1024x1024');
export const NOVELVIZ_MAX_SCENES = envInt('NOVELVIZ_MAX_SCENES', 6);
export const NOVELVIZ_IMAGE_CONCURRENCY = Math.max(1, Math.min(8, envInt('NOVELVIZ_IMAGE_CONCURRENCY', 3)));

export const PROVIDER = {
  baseURL: env('PROVIDER_BASE_URL', '').replace(/\/+$/, ''),
  apiPath: env('PROVIDER_API_PATH', '/v1/images/generations'),
  modelName: env('PROVIDER_MODEL_NAME', ''),
  apiKey: env('PROVIDER_API_KEY', ''),
  timeoutSeconds: envInt('PROVIDER_TIMEOUT_SECONDS', 300),
};

export const LLM = {
  baseURL: env('LLM_BASE_URL', '').replace(/\/+$/, ''),
  apiPath: env('LLM_API_PATH', '/v1/chat/completions'),
  modelName: env('LLM_MODEL_NAME', ''),
  apiKey: env('LLM_API_KEY', ''),
  timeoutSeconds: envInt('LLM_TIMEOUT_SECONDS', 300),
};

export const providerConfigured = Boolean(PROVIDER.baseURL && PROVIDER.modelName && PROVIDER.apiKey);
export const llmConfigured = Boolean(LLM.baseURL && LLM.modelName && LLM.apiKey);

export const CHUNK_SIZE = 4 * 1024 * 1024;
