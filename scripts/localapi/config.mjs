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
  // 思考等级（off / low / medium / high）与随机性只作为默认值：
  // 工作台「悬浮窗对话 → 设置」里保存的配置优先（见 chat.mjs）。
  reasoningEffort: env('LLM_REASONING_EFFORT', 'high'),
  temperature: Number.parseFloat(env('LLM_TEMPERATURE', '0.9')) || 0.9,
};

export const providerConfigured = Boolean(PROVIDER.baseURL && PROVIDER.modelName && PROVIDER.apiKey);
// 对话是否已配置是动态的（工作台里随时可改），统一用 chat.mjs 的 resolveChatConfig() 判断。

// ComfyUI 本地生图（工作台的「Comfy 生图」板块，仅本地模式存在）。
// 契约见桌面 Comfy\doc：原生接口在 8188，工作流模板随仓库放在 comfy/workflows/。
export const COMFY = {
  baseURL: env('COMFY_BASE_URL', 'http://127.0.0.1:8188').replace(/\/+$/, ''),
  // 板块里「启动 ComfyUI」按钮执行的命令：可以是脚本路径（含空格也能用）或一段 shell 命令；留空则按钮提示手动启动。
  startCmd: env('COMFY_START_CMD', ''),
  timeoutMs: Math.max(30000, envInt('COMFY_TIMEOUT_MS', 15 * 60 * 1000)),
  // 每张图前先 POST /free 卸载模型（显存紧张时打开；会带来每张重新加载权重的耗时）。
  freeMemory: envInt('COMFY_FREE_MEMORY', 0) === 1,
};

// 本机 TTS（工作台的「TTS 语音合成」板块，仅本地模式存在）。
// 契约见 E:\AI\TTS\TTS-调用契约.md：CLI 是唯一后端，--json 时 stdout 恰好一个 JSON 信封。
// 默认直接调「任意 Python 3.9+ × tts_cli.py」（契约等价入口），避开 Windows spawn .cmd 的引号问题。
export const TTS = {
  python: env('TTS_PYTHON', 'E:\\AI\\TTS\\Qwen3-TTS\\.venv\\Scripts\\python.exe'),
  cli: env('TTS_CLI', 'E:\\AI\\TTS\\tts_cli.py'),
  timeoutMs: Math.max(60000, envInt('TTS_TIMEOUT_MS', 30 * 60 * 1000)),
  // 引擎空闲自动退出（秒）：合成完挂着实例会一直占显存，默认 10 分钟无请求自动释放。
  idleTimeoutSeconds: envInt('TTS_IDLE_TIMEOUT_SECONDS', 600),
  // 参考音频上传上限（MB）。
  maxRefAudioMb: Math.max(1, envInt('TTS_MAX_REF_AUDIO_MB', 30)),
};

export const ttsConfigured = fs.existsSync(TTS.python) && fs.existsSync(TTS.cli);

export const CHUNK_SIZE = 4 * 1024 * 1024;
