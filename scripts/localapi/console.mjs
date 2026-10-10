// 管理台 API：/console/api/*。本地模式只有一个固定账号、一份配置、两个内置渠道，
// 但字段形状与线上一致，管理台页面可以完整渲染。
import {localUser, nextId, nowIso, persist, quotaView, store} from './store.mjs';

const CATEGORY_LABELS = {
  site: '站点',
  email: '邮件',
  risk: '风控',
  quota: '额度',
  generation: '生图',
};

function jsonValue(value) {
  if (value === undefined || value === null) return {};
  return value;
}

function userView(user) {
  return {
    id: user.id,
    username: user.username,
    email: user.email,
    mobile: user.mobile || '',
    role: user.role,
    status: user.status,
    is_staff: Boolean(user.is_staff),
    date_joined: user.date_joined,
    last_login: user.last_login,
    last_login_ip: user.last_login_ip || null,
    quota: quotaView(),
  };
}

function userDetailView(user) {
  return {
    ...userView(user),
    profile: {
      nickname: user.profile?.nickname || '',
      avatar: user.profile?.avatar || '',
      register_ip: user.profile?.register_ip || null,
      register_source: user.profile?.register_source || 'local-mode',
      remark: user.profile?.remark || '',
    },
  };
}

function taskView(task) {
  return {
    id: task.id,
    task_no: task.task_no,
    user_id: task.user_id,
    username: localUser().username,
    status: task.status,
    prompt: task.prompt,
    style_code: task.style_code || '',
    size_code: task.size_code || '',
    quality_code: task.quality_code || '',
    channel_name: task.channel_name || '',
    quota_cost: task.quota_cost ?? 0,
    error_code: task.error_code || '',
    error_message: task.error_message || '',
    provider_request_id: task.provider_request_id || '',
    provider_response_json: jsonValue(task.provider_response_json),
    created_at: task.submitted_at,
    finished_at: task.finished_at,
  };
}

function paginate(items, {page = 1, perPage = 20} = {}) {
  const total = items.length;
  const totalPages = Math.max(1, Math.ceil(total / perPage));
  const currentPage = Math.min(Math.max(1, page), totalPages);
  const start = (currentPage - 1) * perPage;
  return {
    items: items.slice(start, start + perPage),
    pagination: {
      page: currentPage,
      per_page: perPage,
      total,
      total_pages: totalPages,
      has_next: currentPage < totalPages,
      has_previous: currentPage > 1,
    },
  };
}

export function dashboard() {
  const today = new Date().toISOString().slice(0, 10);
  const users = store.users;
  const tasks = store.tasks;
  const todaysTasks = tasks.filter((task) => (task.submitted_at || '').slice(0, 10) === today);
  const failed = tasks.filter((task) => task.status === 'failed');

  return {
    today: {
      new_users: users.filter((user) => (user.date_joined || '').slice(0, 10) === today).length,
      tasks: todaysTasks.length,
      failed_tasks: todaysTasks.filter((task) => task.status === 'failed').length,
      succeeded_tasks: todaysTasks.filter((task) => task.status === 'succeeded').length,
    },
    totals: {
      users: users.length,
      images: tasks.reduce((count, task) => count + (task.images?.length || 0), 0),
      tasks: tasks.length,
      active_channels: store.channels.filter((channel) => channel.is_active).length,
    },
    latest_tasks: tasks.slice(0, 8).map(taskView),
    latest_users: users.slice(0, 8).map(userView),
    recent_failures: failed.slice(0, 8).map((task) => ({
      id: task.id,
      event_type: 'generation_failed',
      source: task.channel_name || 'local',
      success: false,
      remark: '',
      task_no: task.task_no,
      error_code: task.error_code || '',
      error_message: task.error_message || '',
      provider_request_id: task.provider_request_id || '',
      payload_json: {},
      created_at: task.finished_at || task.submitted_at,
    })),
  };
}

export function listUsers({page = 1, perPage = 12, keyword = '', status = ''} = {}) {
  const needle = String(keyword || '').trim().toLowerCase();
  const filtered = store.users
    .filter((user) => (status ? user.status === status : true))
    .filter((user) =>
      needle
        ? user.username.toLowerCase().includes(needle) || (user.email || '').toLowerCase().includes(needle)
        : true,
    )
    .map(userView);
  return paginate(filtered, {page, perPage});
}

export function userDetail(id) {
  const user = store.users.find((item) => item.id === id);
  if (!user) throw new Error('用户不存在');
  return userDetailView(user);
}

export function updateUserQuota(id, payload) {
  const user = store.users.find((item) => item.id === id);
  if (!user) throw new Error('用户不存在');
  if (payload.daily_quota !== undefined && payload.daily_quota !== '') {
    const daily = Number.parseInt(payload.daily_quota, 10);
    if (Number.isFinite(daily) && daily >= 0) store.quota.daily_quota = daily;
  }
  if (payload.available_quota !== undefined && payload.available_quota !== '') {
    const available = Number.parseInt(payload.available_quota, 10);
    if (Number.isFinite(available) && available >= 0) store.quota.available_quota = available;
  }
  if (payload.remark !== undefined && user.profile) {
    user.profile.remark = String(payload.remark || '');
  }
  persist();
  return quotaView();
}

export function listTasks({page = 1, perPage = 20, keyword = '', status = ''} = {}) {
  const needle = String(keyword || '').trim().toLowerCase();
  const filtered = store.tasks
    .filter((task) => (status ? task.status === status : true))
    .filter((task) => (needle ? task.prompt.toLowerCase().includes(needle) || task.task_no.includes(needle) : true))
    .map(taskView);
  return paginate(filtered, {page, perPage});
}

export function configList(category = '') {
  const items = Object.entries(store.config)
    .filter(([, item]) => (category ? item.category === category : true))
    .map(([key, item], index) => ({
      id: index + 1,
      key,
      value: item.value,
      is_secret: Boolean(item.is_secret),
      description: item.description || '',
      category: item.category || 'site',
      updated_at: item.updated_at || null,
    }));
  const categories = [...new Set(Object.values(store.config).map((item) => item.category))].map((code) => ({
    code,
    label: CATEGORY_LABELS[code] || code,
  }));
  return {items, categories};
}

export function updateConfig(key, payload) {
  const existing = store.config[key];
  store.config[key] = {
    value: payload.value === undefined || payload.value === null ? '' : String(payload.value),
    is_secret: payload.is_secret ?? existing?.is_secret ?? false,
    description: payload.description ?? existing?.description ?? '',
    category: payload.category ?? existing?.category ?? 'site',
    updated_at: nowIso(),
  };
  persist();
  return {key, updated_at: store.config[key].updated_at};
}

export function batchUpdateConfig(items) {
  let updated = 0;
  for (const item of items || []) {
    if (!item?.key) continue;
    updateConfig(item.key, item);
    updated += 1;
  }
  return {updated};
}

function channelView(channel) {
  return {
    id: channel.id,
    name: channel.name,
    channel_code: channel.channel_code,
    channel_type: channel.channel_type,
    provider_name: channel.provider_name,
    base_url: channel.base_url,
    api_path: channel.api_path,
    edit_api_path: channel.edit_api_path,
    model_name: channel.model_name,
    timeout_seconds: channel.timeout_seconds,
    max_retries: channel.max_retries,
    is_active: Boolean(channel.is_active),
    priority: channel.priority,
    created_at: channel.created_at,
    updated_at: channel.updated_at,
  };
}

export function listChannels() {
  return store.channels.map(channelView);
}

function normalizeChannel(payload, channelCode) {
  const channelType = payload.channel_type === 'llm' ? 'llm' : 'image';
  return {
    name: String(payload.name || '').trim() || (channelType === 'llm' ? '未命名对话渠道' : '未命名生图渠道'),
    channel_code: String(payload.channel_code || channelCode || '').trim() || `local-${nextId('channel')}`,
    channel_type: channelType,
    provider_name: String(payload.provider_name || ''),
    base_url: String(payload.base_url || ''),
    api_path: String(payload.api_path || ''),
    edit_api_path: String(payload.edit_api_path || ''),
    model_name: String(payload.model_name || ''),
    timeout_seconds: Number.parseInt(payload.timeout_seconds, 10) || 60,
    max_retries: Number.parseInt(payload.max_retries, 10) || 0,
    is_active: payload.is_active === undefined ? true : Boolean(payload.is_active),
    priority: Number.parseInt(payload.priority, 10) || 1,
    api_key: payload.api_key ? String(payload.api_key) : '',
  };
}

export function createChannel(payload) {
  const normalized = normalizeChannel(payload, '');
  const channel = {
    id: nextId('channel'),
    ...normalized,
    created_at: nowIso(),
    updated_at: nowIso(),
  };
  store.channels.push(channel);
  persist();
  return channelView(channel);
}

export function updateChannel(id, payload) {
  const index = store.channels.findIndex((channel) => channel.id === id);
  if (index < 0) throw new Error('渠道不存在');
  const existing = store.channels[index];
  // 这里只做展示层维护：本地模式实际调用哪个渠道由 .env.local 决定。
  store.channels[index] = {
    ...existing,
    ...normalizeChannel(payload, existing.channel_code),
    id: existing.id,
    created_at: existing.created_at,
    updated_at: nowIso(),
  };
  persist();
  return channelView(store.channels[index]);
}

export function deleteChannel(id) {
  const index = store.channels.findIndex((channel) => channel.id === id);
  if (index < 0) throw new Error('渠道不存在');
  store.channels.splice(index, 1);
  persist();
  return {deleted: true};
}
