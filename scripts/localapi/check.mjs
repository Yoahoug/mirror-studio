// 本地模式自检：参考图分片上传、图生图、EPUB 上传、小说可视化 SSE、Comfy、TTS。
// 只依赖 node 内置模块，直接 node scripts/localapi/check.mjs 运行。
//
// 默认自己起一个隔离实例：临时数据目录 + 空闲端口，自检产生的任务/图片/上传
// 全部落在系统临时目录，跑完即删，不会污染 local-data 的工作台历史。
// 想自检一个已经在跑的实例（会写它的数据），用：
//   LOCAL_API_BASE=http://127.0.0.1:8000 npm run local:check
import {spawn} from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import zlib from 'node:zlib';

import {placeholderPNG} from './png.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));

let BASE = process.env.LOCAL_API_BASE || '';
let isolatedChild = null;
let isolatedDir = '';

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let index = 0; index < 256; index += 1) {
    let value = index;
    for (let bit = 0; bit < 8; bit += 1) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
    table[index] = value >>> 0;
  }
  return table;
})();

function crc32(buffer) {
  let crc = 0xffffffff;
  for (const byte of buffer) crc = CRC_TABLE[(crc ^ byte) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

function zipBuild(entries) {
  const chunks = [];
  const central = [];
  let offset = 0;
  for (const entry of entries) {
    const name = Buffer.from(entry.name, 'utf8');
    const raw = Buffer.from(entry.content, 'utf8');
    const compressed = entry.store ? raw : zlib.deflateRawSync(raw);
    const crc = crc32(raw);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0, 6);
    local.writeUInt16LE(entry.store ? 0 : 8, 8);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(compressed.length, 18);
    local.writeUInt32LE(raw.length, 22);
    local.writeUInt16LE(name.length, 26);
    chunks.push(local, name, compressed);

    const header = Buffer.alloc(46);
    header.writeUInt32LE(0x02014b50, 0);
    header.writeUInt16LE(20, 4);
    header.writeUInt16LE(20, 6);
    header.writeUInt16LE(entry.store ? 0 : 8, 10);
    header.writeUInt32LE(crc, 16);
    header.writeUInt32LE(compressed.length, 20);
    header.writeUInt32LE(raw.length, 24);
    header.writeUInt16LE(name.length, 28);
    header.writeUInt32LE(offset, 42);
    central.push(header, name);
    offset += local.length + name.length + compressed.length;
  }
  const centralBuffer = Buffer.concat(central);
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0);
  eocd.writeUInt16LE(entries.length, 8);
  eocd.writeUInt16LE(entries.length, 10);
  eocd.writeUInt32LE(centralBuffer.length, 12);
  eocd.writeUInt32LE(offset, 16);
  return Buffer.concat([...chunks, centralBuffer, eocd]);
}

function makeEpub() {
  const chapter = (title, body) => `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>${title}</title></head>
<body><h1>${title}</h1><p>${body}</p></body></html>`;
  const text1 =
    '夜里下起了大雪，城门外的火把一盏盏熄灭。年轻的守城人把最后一封信塞进怀里，转身走向风雪深处的长街。'.repeat(6);
  const text2 =
    '清晨的钟声撞开云层，山谷里的雾气像潮水一样退去。她站在悬崖边，看见远处那座沉默的城在日光里一寸寸显形。'.repeat(6);
  return zipBuild([
    {name: 'mimetype', content: 'application/epub+zip', store: true},
    {
      name: 'META-INF/container.xml',
      content:
        '<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>',
    },
    {name: 'OEBPS/chapter1.xhtml', content: chapter('第一章 雪夜', text1)},
    {name: 'OEBPS/chapter2.xhtml', content: chapter('第二章 日出', text2)},
  ]);
}

async function api(path, options = {}) {
  const response = await fetch(`${BASE}${path}`, options);
  const text = await response.text();
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    body = {raw: text};
  }
  return {status: response.status, body};
}

function assert(condition, message) {
  if (!condition) throw new Error(`断言失败：${message}`);
  console.log(`  ✓ ${message}`);
}

// ── 1. 参考图分片上传 → 图生图任务 ──
async function checkReferenceUpload() {
  console.log('\n[1] 参考图分片上传 → 图生图');
  const referencePNG = placeholderPNG('self-check-reference-image', '512x512');
  assert(referencePNG.length > 0, `本地合成一张参考图（${referencePNG.length} 字节）`);

  const session = await api('/api/uploads/sessions', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      kind: 'generation_reference_image',
      filename: 'reference.png',
      content_type: 'image/png',
      total_size: referencePNG.length,
      client_upload_id: `check-reference-${Date.now()}`,
    }),
  });
  assert(session.body.code === 0, `创建上传会话（chunk_size=${session.body.data?.chunk_size}）`);
  const uploadId = session.body.data.upload_id;
  const chunkSize = session.body.data.chunk_size;
  const buffer = referencePNG;
  const totalChunks = Math.ceil(buffer.length / chunkSize);
  for (let index = 0; index < totalChunks; index += 1) {
    const slice = buffer.subarray(index * chunkSize, Math.min((index + 1) * chunkSize, buffer.length));
    await api(`/api/uploads/sessions/${uploadId}/chunks/${index}`, {
      method: 'PUT',
      headers: {'Content-Type': 'application/octet-stream'},
      body: slice,
    });
  }
  const completed = await api(`/api/uploads/sessions/${uploadId}/complete`, {method: 'POST'});
  assert(completed.body.code === 0, '完成上传并拿到 reference_upload_token');
  const token = completed.body.data.result.reference_upload_token;

  const task = await api('/api/generate/tasks', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      prompt: '把参考图改成黄昏色调，电影感',
      generation_mode: 'image_to_image',
      size_code: '1024x1024',
      reference_upload_token: token,
      reference_image_name: 'reference.png',
    }),
  });
  assert(task.body.code === 0, '提交图生图任务');
  const taskNo = task.body.data.task_no;
  await new Promise((resolve) => setTimeout(resolve, 2500));
  const detail = await api(`/api/generate/tasks/${taskNo}`);
  assert(detail.body.data.status === 'succeeded', `任务 ${taskNo} 生成成功`);
  const preview = detail.body.data.images[0].preview_url;
  const image = await fetch(`${BASE}${preview}`);
  assert(image.headers.get('content-type') === 'image/png', `预览图可访问：${preview}`);
}

// ── 2. 对话（中转站配置 + 真实/模拟两条路）──
async function checkChat() {
  console.log('\n[2] 对话接口');
  const config = await api('/api/chat/config');
  assert(config.body.code === 0, '读取对话渠道配置成功');
  const chat = config.body.data;
  assert(typeof chat.configured === 'boolean', `可判断是否已配置中转站（configured=${chat.configured}）`);
  assert(
    typeof chat.reasoning_effort === 'string' && Array.isArray(chat.efforts) && chat.efforts.includes(chat.reasoning_effort),
    `思考等级可设置（当前 ${chat.reasoning_effort}，可选 ${(chat.efforts || []).join('/')}）`,
  );
  assert(typeof chat.has_key === 'boolean' && typeof chat.key_hint === 'string', '密钥只回掩码，不回原文');

  const reply = await api('/api/chat', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({prompt: '帮我写一条生图提示词', history: []}),
  });
  assert(reply.body.code === 0 && reply.body.data.reply.length > 0, '返回对话内容');
  if (chat.configured) {
    assert(!reply.body.data.reply.includes('本地模拟回复'), `回复来自真实中转站（${chat.model}）`);
  } else {
    assert(reply.body.data.reply.includes('本地模拟回复'), '未配置渠道时明确标注本地模拟');
  }
}

// ── 3. EPUB 上传 + 小说可视化 ──
async function checkNovel() {
  console.log('\n[3] EPUB 上传 → 小说可视化任务');
  const epub = makeEpub();
  const form = new FormData();
  form.append('file', new Blob([epub], {type: 'application/epub+zip'}), 'check-novel.epub');
  const uploaded = await fetch(`${BASE}/api/novelviz/books/upload`, {method: 'POST', body: form}).then((res) => res.json());
  assert(uploaded.code === 0, `上传 EPUB（${uploaded.data?.display_name}）`);

  const task = await api('/api/novelviz/tasks', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({
      book_source: uploaded.data.source,
      book_filename: uploaded.data.filename,
      uploaded_book_id: uploaded.data.uploaded_book_id,
      scene_mode: 'auto',
      target_plot: '',
    }),
  });
  assert(task.body.code === 0, '创建小说可视化任务');
  const taskId = task.body.data.id;

  const controller = new AbortController();
  const response = await fetch(`${BASE}/api/novelviz/tasks/${taskId}/events?last_event_id=0`, {
    signal: controller.signal,
    headers: {Accept: 'text/event-stream'},
  });
  assert(response.status === 200, 'SSE 事件流已连接');

  const seen = [];
  let buffer = '';
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    const {value, done} = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, {stream: true});
    const frames = buffer.split('\n\n');
    buffer = frames.pop() || '';
    for (const frame of frames) {
      const type = /^event: (.+)$/m.exec(frame)?.[1];
      if (type) seen.push(type);
      if (type === 'task_done' || type === 'task_failed') {
        controller.abort();
        break;
      }
    }
    if (seen.includes('task_done') || seen.includes('task_failed')) break;
  }
  assert(seen.includes('scene_plan_ready'), `收到 scene_plan_ready（事件：${seen.join(', ')}）`);
  assert(seen.includes('task_done') || seen.includes('task_failed'), '收到终态事件');

  const detail = await api(`/api/novelviz/tasks/${taskId}`);
  const scenes = detail.body.data.images;
  assert(scenes.length > 0, `场景图记录 ${scenes.length} 条`);
  const firstReady = scenes.find((scene) => scene.status === 'succeeded');
  if (firstReady) {
    const image = await fetch(`${BASE}${firstReady.preview_url}`);
    assert(image.headers.get('content-type') === 'image/png', '场景图可访问');
  }
  const history = await api('/api/workspace/history?page=1&page_size=20');
  assert(
    history.body.data.items.some((item) => item.task_kind === 'novelviz'),
    '历史记录里能看到小说任务',
  );
  return taskId;
}

// ── 4. 管理台 ──
async function checkConsole() {
  console.log('\n[4] 管理台接口');
  const dashboard = await api('/console/api/dashboard/');
  assert(dashboard.body.data.totals.tasks > 0, '概览统计到任务数');
  const users = await api('/console/api/users/?page=1&per_page=12');
  assert(users.body.data.items.length === 1, '用户列表返回本地账号');
  const tasks = await api('/console/api/tasks/?page=1&per_page=20');
  const row = tasks.body.data.items.find((item) => item.task_no.startsWith('L'));
  assert(Boolean(row?.prompt), '任务列表带出 prompt 字段');
  const config = await api('/console/api/config/');
  assert(config.body.data.items.length > 0 && config.body.data.categories.length > 0, '配置项与分类可读');
  const channels = await api('/console/api/image-channels/');
  assert(channels.body.data.length >= 2, '渠道列表包含生图与对话渠道');
}

// ── 5. Comfy 本地生图板块（本地专属接口；ComfyUI 未运行时也要能返回状态）──
async function checkComfy() {
  console.log('\n[5] Comfy 生图板块接口');
  const status = await api('/api/comfy/status');
  assert(status.body.code === 0, '查询 ComfyUI 状态成功');
  const data = status.body.data;
  assert(typeof data.running === 'boolean', `ComfyUI 运行状态可读（running=${data.running}）`);
  assert(typeof data.base_url === 'string' && data.base_url.length > 0, `ComfyUI 地址：${data.base_url}`);
  assert(
    Array.isArray(data.models) && data.models.length >= 3 && data.models.every((model) => model.id && model.label),
    `模型清单包含 ${data.models?.length} 个模型（${data.models?.map((model) => model.id).join(', ')}）`,
  );
  assert(typeof data.start_command_configured === 'boolean', '启动命令配置状态可读');

  const log = await api('/api/comfy/log?limit=5');
  assert(log.body.code === 0, '读取 ComfyUI 日志接口成功');
  assert(typeof log.body.data.available === 'boolean', `日志可读性可判断（available=${log.body.data.available}）`);
  if (log.body.data.available) {
    assert(
      Array.isArray(log.body.data.issues) && log.body.data.issues.length <= 5,
      `日志异常抽取正常（错误 ${log.body.data.errors} / 警告 ${log.body.data.warnings}）`,
    );
  }
}

// ── 6. TTS 板块（本地专属接口；不做真实合成，只验证 CLI 探测与接口结构）──
async function checkTts() {
  console.log('\n[6] TTS 板块接口');
  const status = await api('/api/tts/status');
  assert(status.body.code === 0, '查询 TTS 状态成功');
  const data = status.body.data;
  assert(typeof data.enabled === 'boolean', `CLI 可用性可判断（enabled=${data.enabled}）`);
  assert(Array.isArray(data.engines) && data.engines.length >= 5, `引擎元数据 ${data.engines?.length} 条（含 contract.json 的高级参数表）`);
  assert(
    data.engines.every((engine) => engine.id && Array.isArray(engine.capabilities)),
    `引擎能力齐全（${data.engines?.map((engine) => engine.id).join(', ')}）`,
  );
  assert(Array.isArray(data.voices), `音色库可读（${data.voices?.length} 个音色）`);

  const voices = await api('/api/tts/voices');
  assert(voices.body.code === 0 && Array.isArray(voices.body.data.voices), '音色列表接口可用');
  assert(Array.isArray(voices.body.data.gptsovits_voices), 'GPT-SoVITS 社区音色列表可读');

  if (data.enabled) {
    const log = await api('/api/tts/log?engine=qwen-base&lines=5');
    // 没跑过该引擎时契约返回 E_NOT_FOUND，两种情况都算接口通了。
    assert(
      log.body.code === 0 || String(log.body.message).includes('日志'),
      `日志接口可达（${log.body.code === 0 ? '有日志' : '暂无日志'}）`,
    );
    const qwenAudio = data.voices.find((voice) => voice.audio_url);
    if (qwenAudio) {
      const audio = await fetch(`${BASE}${qwenAudio.audio_url}`);
      assert(audio.status === 200 || audio.status === 404, '音色试听路由可达');
    }
  }

  const task = await api('/api/tts/tasks', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({mode: 'clone', text: '自检', engine: 'unknown-engine'}),
  });
  assert(task.body.code === 400, '非法引擎参数被拒绝（参数校验生效）');

  const list = await api('/api/tts/tasks');
  assert(list.body.code === 0 && Array.isArray(list.body.data.items), 'TTS 任务列表接口可用');
}

async function main() {
  if (!BASE) {
    await startIsolatedServer();
  }
  console.log(`本地模式自检：${BASE}${isolatedChild ? '（隔离实例，临时数据目录，跑完即删）' : ''}`);
  try {
    await checkReferenceUpload();
    await checkChat();
    await checkNovel();
    await checkConsole();
    await checkComfy();
    await checkTts();
    console.log('\n全部自检通过 ✅');
  } finally {
    await stopIsolatedServer();
  }
}

// ── 隔离实例：临时数据目录 + 空闲端口，让自检不碰 local-data ──

function freePort() {
  return new Promise((resolve, reject) => {
    const probe = net.createServer();
    probe.on('error', reject);
    probe.listen(0, '127.0.0.1', () => {
      const address = probe.address();
      probe.close(() => resolve(address.port));
    });
  });
}

async function startIsolatedServer() {
  const port = await freePort();
  isolatedDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mirror-local-check-'));
  BASE = `http://127.0.0.1:${port}`;
  isolatedChild = spawn(process.execPath, [path.join(HERE, 'server.mjs')], {
    env: {
      ...process.env,
      LOCAL_API_HOST: '127.0.0.1',
      LOCAL_API_PORT: String(port),
      LOCAL_DATA_DIR: isolatedDir,
      // 自检用最小模拟耗时，跑得快一点；占位图/模拟对话由零配置默认值决定。
      LOCAL_IMAGE_DELAY_MS: '60',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stderr = '';
  isolatedChild.stderr.on('data', (chunk) => {
    stderr += chunk.toString();
  });
  isolatedChild.stdout.on('data', () => {}); // 排空子进程输出，避免管道写满阻塞
  isolatedChild.on('exit', (code) => {
    if (code && code !== 0) stderr += `\n[隔离实例退出码 ${code}]`;
  });

  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (isolatedChild.exitCode !== null) {
      throw new Error(`隔离实例启动失败：${stderr.trim() || `退出码 ${isolatedChild.exitCode}`}`);
    }
    try {
      const response = await fetch(`${BASE}/__local`);
      if (response.ok) return;
    } catch {
      // 还没起来，继续等。
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error(`隔离实例 15 秒内没有就绪：${stderr.trim()}`);
}

async function stopIsolatedServer() {
  if (isolatedChild && isolatedChild.exitCode === null) {
    const exited = new Promise((resolve) => isolatedChild.once('exit', resolve));
    isolatedChild.kill('SIGTERM');
    await Promise.race([exited, new Promise((resolve) => setTimeout(resolve, 3000))]);
    if (isolatedChild.exitCode === null) isolatedChild.kill('SIGKILL');
  }
  isolatedChild = null;
  if (isolatedDir) {
    fs.rmSync(isolatedDir, {recursive: true, force: true});
    isolatedDir = '';
  }
}

main().catch(async (error) => {
  console.error(`\n自检失败 ❌ ${error.message}`);
  await stopIsolatedServer();
  process.exitCode = 1;
});
