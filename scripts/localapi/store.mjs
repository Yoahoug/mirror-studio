// 本地模式的持久化层：一个 JSON 文件 + 一个媒体目录，刻意不引入任何数据库。
// 所有写入都是「整文件原子替换」，本地单人开发的数据量下足够可靠；
// 删除 local-data/ 就等于把环境重置回初始状态。
import fs from 'node:fs';
import path from 'node:path';

import {DATA_DIR, MEDIA_DIR, NOVEL_DIR, STORE_FILE, UPLOAD_DIR} from './config.mjs';

const STORE_VERSION = 1;

function ensureDirs() {
  for (const dir of [DATA_DIR, MEDIA_DIR, NOVEL_DIR, UPLOAD_DIR]) {
    fs.mkdirSync(dir, {recursive: true});
  }
}

export function nowIso() {
  return new Date().toISOString();
}

function defaultStore() {
  return {
    version: STORE_VERSION,
    counters: {media: 0, task: 0, image: 0, event: 0, novelTask: 0, book: 0, upload: 0, channel: 0, user: 0, ttsTask: 0},
    users: [],
    quota: {},
    tasks: [],
    media: {},
    referenceUploads: {},
    uploadSessions: {},
    books: [],
    novelTasks: [],
    ttsTasks: [],
    channels: [],
    config: {},
    events: {},
    // 工作台里配置的对话中转站（悬浮窗对话 → 设置）。
    // 密钥只留在本机这个文件里；/api/chat/config 只回掩码。
    chat: {
      base_url: '',
      api_key: '',
      model: '',
      reasoning_effort: 'high',
      temperature: 0.9,
      custom_instruction: '',
    },
  };
}

const LOCAL_USER_TEMPLATE = {
  username: 'local',
  role: 'admin',
  status: 'active',
  is_staff: true,
  email: 'local@example.com',
  mobile: '',
  last_login_ip: '127.0.0.1',
  profile: {
    nickname: '本地开发者',
    avatar: '',
    register_ip: '127.0.0.1',
    register_source: 'local-mode',
    remark: '本地模式的固定账号（无登录、无注册）',
  },
};

const DEFAULT_CONFIG = [
  ['site.name', '绘镜 Studio', '站点名称', 'site'],
  ['site.brand_name', '绘镜', '品牌名', 'site'],
  ['site.brand_english_name', 'MIRROR STUDIO', '品牌英文名', 'site'],
  ['site.brand_tagline', '把灵感写成图像，把作品留在自己的空间里', '品牌标语', 'site'],
  ['site.register_enabled', '0', '是否开放注册（本地模式固定关闭）', 'site'],
  ['site.login_captcha_enabled', '0', '登录验证码开关（本地模式固定关闭）', 'site'],
  ['site.image_retention_days', '3650', '图片保留天数', 'site'],
  ['site.daily_user_task_limit', '1000', '单用户每日任务上限', 'site'],
  ['site.max_pending_tasks', '10', '同时排队任务上限', 'site'],
  ['site.default_channel_code', 'local-default', '默认生图渠道编码', 'site'],
  ['site.default_daily_free_quota', '10000', '默认每日免费次数', 'site'],
  ['site.workspace_release_version', 'local', '工作台更新说明版本', 'site'],
  ['site.workspace_release_title', '本地模式', '工作台更新说明标题', 'site'],
  ['site.workspace_release_notes', '本地开发者模式：无数据库、无登录、图片落盘 local-data/media。', '工作台更新说明正文', 'site'],
  ['site.workspace_release_published_at', '', '工作台更新说明发布时间', 'site'],
  ['email.verification_enabled', '0', '注册邮件验证开关', 'email'],
  ['email.verification_ttl_seconds', '600', '验证码有效期（秒）', 'email'],
  ['email.verification_resend_seconds', '60', '验证码重发间隔（秒）', 'email'],
  ['risk.login_fail_limit', '5', '登录失败锁定阈值', 'risk'],
  ['risk.login_lock_minutes', '15', '登录锁定时长（分钟）', 'risk'],
  ['risk.task_window_seconds', '60', '任务风控窗口（秒）', 'risk'],
  ['risk.task_limit_per_window', '1000', '窗口内任务上限', 'risk'],
];

const DEFAULT_CHANNELS = [
  {
    name: '本地占位生图',
    channel_code: 'local-default',
    channel_type: 'image',
    provider_name: 'LocalPlaceholder',
    base_url: '',
    api_path: '/v1/images/generations',
    edit_api_path: '',
    model_name: 'local-placeholder',
    timeout_seconds: 60,
    max_retries: 0,
    is_active: true,
    priority: 1,
  },
  {
    name: '本地模拟对话',
    channel_code: 'local-llm',
    channel_type: 'llm',
    provider_name: 'LocalMock',
    base_url: '',
    api_path: '/v1/chat/completions',
    edit_api_path: '',
    model_name: 'local-mock',
    timeout_seconds: 60,
    max_retries: 0,
    is_active: true,
    priority: 1,
  },
];

function seed(store) {
  const now = nowIso();
  store.counters.user += 1;
  store.users = [
    {
      id: store.counters.user,
      ...LOCAL_USER_TEMPLATE,
      date_joined: now,
      last_login: now,
    },
  ];
  store.quota = {
    daily_quota: 10000,
    available_quota: 10000,
    frozen_quota: 0,
    total_generated_count: 0,
    last_reset_date: now.slice(0, 10),
  };
  for (const [key, value, description, category] of DEFAULT_CONFIG) {
    store.config[key] = {value, is_secret: false, description, category, updated_at: now};
  }
  for (const channel of DEFAULT_CHANNELS) {
    store.counters.channel += 1;
    store.channels.push({id: store.counters.channel, ...channel, created_at: now, updated_at: now});
  }
}

function load() {
  ensureDirs();
  if (!fs.existsSync(STORE_FILE)) {
    const fresh = defaultStore();
    seed(fresh);
    save(fresh);
    return fresh;
  }
  try {
    const parsed = JSON.parse(fs.readFileSync(STORE_FILE, 'utf8'));
    const base = defaultStore();
    const merged = {...base, ...parsed};
    merged.counters = {...base.counters, ...(parsed.counters || {})};
    if (!merged.users?.length) seed(merged);
    return merged;
  } catch (error) {
    throw new Error(`本地数据文件损坏：${STORE_FILE}（删除该文件或整个 local-data/ 即可重置）\n${error.message}`);
  }
}

function save(target = store) {
  const temporary = `${STORE_FILE}.tmp`;
  fs.writeFileSync(temporary, JSON.stringify(target, null, 2), 'utf8');
  fs.renameSync(temporary, STORE_FILE);
}

export const store = load();

/** 每次写操作后落盘；本地模式下数据量很小，直接同步写最简单可靠。 */
export function persist() {
  save(store);
}

export function nextId(kind) {
  store.counters[kind] = (store.counters[kind] || 0) + 1;
  return store.counters[kind];
}

export function newTaskNo() {
  const stamp = new Date().toISOString().slice(0, 10).replace(/-/g, '');
  const sequence = String(nextId('task')).padStart(4, '0');
  return `L${stamp}-${sequence}`;
}

export function localUser() {
  return store.users[0];
}

export function quotaView() {
  const quota = store.quota;
  return {
    daily_quota: quota.daily_quota,
    available_quota: Math.max(0, quota.available_quota),
    frozen_quota: 0,
    total_generated_count: quota.total_generated_count || 0,
    last_reset_date: quota.last_reset_date || null,
  };
}

/** 生成成功后扣一次额度，并在跨天时按每日上限重置。 */
export function consumeQuota() {
  const today = nowIso().slice(0, 10);
  if (store.quota.last_reset_date !== today) {
    store.quota.last_reset_date = today;
    store.quota.available_quota = store.quota.daily_quota;
  }
  store.quota.available_quota = Math.max(0, store.quota.available_quota - 1);
  store.quota.total_generated_count = (store.quota.total_generated_count || 0) + 1;
}

// ── 媒体文件 ──

const MIME_EXTENSIONS = {
  'image/png': '.png',
  'image/jpeg': '.jpg',
  'image/jpg': '.jpg',
  'image/webp': '.webp',
  'image/gif': '.gif',
  'application/epub+zip': '.epub',
  'application/octet-stream': '.bin',
};

export function extensionFor(mimeType, filename = '') {
  const fromMime = MIME_EXTENSIONS[String(mimeType || '').toLowerCase()];
  if (fromMime) return fromMime;
  const fromName = path.extname(filename || '');
  return fromName || '.bin';
}

/**
 * 把一个二进制对象写进 local-data/media 并登记成 media token。
 * 图片 URL 一律通过 /downloads/{token}/ 暴露，和线上契约保持同一形状。
 */
export function saveMedia(buffer, {mimeType = 'application/octet-stream', name = 'file', directory = MEDIA_DIR} = {}) {
  fs.mkdirSync(directory, {recursive: true});
  const id = nextId('media');
  const extension = extensionFor(mimeType, name);
  const safeStem = String(name || 'file')
    .replace(/\.[^.]+$/, '')
    .replace(/[^a-zA-Z0-9\u4e00-\u9fa5_-]+/g, '-')
    .slice(0, 40) || 'file';
  const filename = `${String(id).padStart(5, '0')}-${safeStem}${extension}`;
  const absolute = path.join(directory, filename);
  fs.writeFileSync(absolute, buffer);
  const token = `m${id}`;
  store.media[token] = {
    token,
    file: absolute,
    mime_type: mimeType,
    name: `${safeStem}${extension}`,
    size: buffer.length,
    created_at: nowIso(),
  };
  return token;
}

export function mediaByToken(token) {
  return store.media[token] || null;
}

export function downloadURL(token, {preview = false, download = false} = {}) {
  if (!token) return '';
  const query = preview ? '?preview=1' : download ? '?download=1' : '';
  return `/downloads/${token}/${query}`;
}
