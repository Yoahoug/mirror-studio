// 本机 TTS 执行器（仅本地开发者模式使用，线上 Go 服务没有这套接口）。
//
// 契约来源：E:\AI\TTS\TTS-调用契约.md（v1）+ contract.json（机器可读）。
// CLI 是唯一后端：spawn「python tts_cli.py <命令> --json」，stdout 恰好一个 JSON 信封，
// 退出码判成败。引擎元数据（参考音频要求、高级参数表）直接读 contract.json 供前端生成表单。
//
// 8G 显存同一时刻只允许一个引擎实例：切换必须带 --switch（先停后起）；
// 合成请求串行执行（内存 Promise 链），空闲实例靠 --init-set idle_timeout 自动退出释放显存。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';

import {TTS, ttsConfigured} from './config.mjs';
import {acquireGpu, gpuLockHolder} from './gpu.mjs';
import {downloadURL, localUser, newTaskNo, nextId, nowIso, persist, saveMedia, store} from './store.mjs';

const TTS_ROOT = path.dirname(path.resolve(TTS.cli));
// 音色库要和 CLI 读同一份（tts_cli.py：ASSETS = TTS_ROOT/../资产，VOICES_DIR = ASSETS/TTS/音色库），
// 否则 UI 里音色库恒为空、克隆类预热也拿不到参考音频。
const VOICES_DIR = path.join(path.dirname(TTS_ROOT), '资产', 'TTS', '音色库');
const CONTRACT_FILE = path.join(TTS_ROOT, 'contract.json');
const PROBE_TIMEOUT_MS = 60000;
const MAX_TAKES = 5;
const CANONICAL_LANGUAGES = new Set(['auto', 'zh', 'en', 'ja', 'ko', 'yue', 'ar', 'es']);
const CLONE_ENGINES = new Set(['qwen-base', 'gptsovits', 'indextts', 'megatts3']);
const PRESET_ENGINES = new Set(['qwen-preset', 'gptsovits']);
const VOICE_NAME_PATTERN = /^[^\\/:*?"<>|]{1,64}$/;

export class TtsError extends Error {
  constructor(message, {code = 'E_INTERNAL', hint = ''} = {}) {
    super(message);
    this.code = code;
    this.hint = hint;
  }
}

// ── CLI 调用 ──

/**
 * 跑一次 TTS CLI（--json 模式）。契约保证：成功时 stdout 恰好一个 JSON 信封；
 * 失败时若信封可解析就用信封里的错误码/提示，否则退回 stderr。
 */
export function runTtsCli(args, {timeoutMs = TTS.timeoutMs} = {}) {
  if (!ttsConfigured) {
    return Promise.reject(new TtsError(
      `TTS CLI 不可用：请确认 ${TTS.python} 与 ${TTS.cli} 存在，或在 .env.local 里配置 TTS_PYTHON / TTS_CLI。`,
      {code: 'E_ENV_MISSING'},
    ));
  }
  return new Promise((resolve, reject) => {
    const child = spawn(TTS.python, [TTS.cli, ...args, '--json'], {
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const stdout = [];
    const stderr = [];
    let settled = false;
    const timer = setTimeout(() => {
      settled = true;
      child.kill();
      reject(new TtsError(`TTS CLI 超时（${Math.round(timeoutMs / 1000)} 秒）被终止`, {code: 'E_TIMEOUT'}));
    }, timeoutMs);
    if (typeof timer.unref === 'function') timer.unref();

    child.stdout.on('data', (chunk) => stdout.push(chunk));
    child.stderr.on('data', (chunk) => stderr.push(chunk));
    child.on('error', (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(new TtsError(`无法启动 TTS CLI：${error.message}`, {code: 'E_INTERNAL'}));
    });
    child.on('close', (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      const text = Buffer.concat(stdout).toString('utf8').trim();
      let envelope = null;
      try {
        envelope = JSON.parse(text);
      } catch {
        envelope = null;
      }
      if (envelope && envelope.ok) {
        resolve(envelope);
        return;
      }
      const reported = envelope?.error || {};
      const stderrTail = Buffer.concat(stderr).toString('utf8').trim().split(/\r?\n/).slice(-6).join('\n');
      reject(new TtsError(reported.message || `TTS CLI 退出码 ${code}`, {
        code: reported.code || 'E_INTERNAL',
        hint: reported.hint || (stderrTail ? `CLI 输出：\n${stderrTail}` : ''),
      }));
    });
    return child;
  });
}

/** 把高级参数对象序列化成 --set k=v 数组（值按 JSON 解析，字符串原样）。 */
function setArgs(params) {
  const args = [];
  for (const [key, value] of Object.entries(params || {})) {
    if (!/^[a-zA-Z_][a-zA-Z0-9_]{0,40}$/.test(key)) {
      throw new TtsError(`高级参数键不合法：${key}`, {code: 'E_PARAM'});
    }
    const serialized = typeof value === 'string' ? value : JSON.stringify(value);
    if (serialized === undefined) continue;
    args.push('--set', `${key}=${serialized}`);
  }
  return args;
}

// ── 引擎元数据（contract.json）与状态聚合 ──

const FALLBACK_CONTRACT = {
  contract_version: 'v1',
  engines: [
    {
      id: 'qwen-design',
      label: 'Qwen-TTS (设计)',
      capabilities: ['design', 'voice_save'],
      model: 'Qwen3-TTS-Design',
      output_sr: 24000,
    },
    {
      id: 'qwen-base',
      label: 'Qwen-TTS (基础/克隆)',
      capabilities: ['clone', 'say', 'voice_save'],
      model: 'Qwen3-TTS-Base',
      output_sr: 24000,
      ref_audio: {min_s: 3, max_s: 30, recommended: '3~10 秒单声道人声'},
    },
    {
      id: 'qwen-preset',
      label: 'Qwen-TTS (官方预设)',
      capabilities: ['preset'],
      model: 'Qwen3-TTS-Preset',
      output_sr: 24000,
      speakers: ['zh-female-1', 'zh-male-1'],
    },
    {
      id: 'gptsovits',
      label: 'GPT-SoVITS',
      capabilities: ['clone', 'preset'],
      model: 'GPT-SoVITS-v2',
      output_sr: 32000,
      ref_audio: {min_s: 3, max_s: 10, hard: true},
    },
    {
      id: 'indextts',
      label: 'IndexTTS',
      capabilities: ['clone'],
      model: 'IndexTTS',
      output_sr: 24000,
      ref_audio: {min_s: 3, max_s: 30},
    },
    {
      id: 'megatts3',
      label: 'MegaTTS3',
      capabilities: ['clone'],
      model: 'MegaTTS3',
      output_sr: 24000,
      ref_audio: {min_s: 3, max_s: 30},
    },
  ],
};

// contract.json 的 mtime 缓存。缺了这个声明，loadContract() 会在第一次读缓存时抛
// ReferenceError 被 catch 吞掉，永远退回 FALLBACK_CONTRACT（音色表/参数表/加载估时全丢）。
let contractCache = {mtime: -1, data: null};

function loadContract() {
  try {
    const mtime = fs.statSync(CONTRACT_FILE).mtimeMs;
    if (contractCache.mtime !== mtime) {
      contractCache = {mtime, data: JSON.parse(fs.readFileSync(CONTRACT_FILE, 'utf8'))};
    }
    return contractCache.data;
  } catch {
    return FALLBACK_CONTRACT;
  }
}

function contractEngines() {
  const contract = loadContract();
  const engines = Array.isArray(contract?.engines) ? contract.engines : [];
  return engines.map((engine) => ({
    id: engine.id,
    label: engine.label || engine.id,
    capabilities: Array.isArray(engine.capabilities) ? engine.capabilities : [],
    model: engine.model || '',
    output_sr: engine.output_sr ?? null,
    ref_audio: engine.ref_audio ?? null,
    speakers: Array.isArray(engine.speakers) ? engine.speakers : null,
    community_voices: engine.community_voices?.current ?? null,
    load_seconds_estimate: Array.isArray(engine.load_seconds_estimate) ? engine.load_seconds_estimate : null,
    init_params: engine.init_params || {},
    request_params: engine.request_params || {},
    notes: Array.isArray(engine.notes) ? engine.notes : [],
  }));
}

/** 音色库直接读 voices/<名>/meta.json（与 CLI voices_list 同一数据源，不占 CLI 进程）。 */
function voicesFromDisk() {
  let entries = [];
  try {
    entries = fs.readdirSync(VOICES_DIR, {withFileTypes: true}).filter((entry) => entry.isDirectory());
  } catch {
    return [];
  }
  const voices = [];
  for (const entry of entries) {
    let meta = {};
    try {
      meta = JSON.parse(fs.readFileSync(path.join(VOICES_DIR, entry.name, 'meta.json'), 'utf8'));
    } catch {
      // meta 缺失/损坏也列出目录，has_voice_pt 等字段照常探测
    }
    const name = meta.name || entry.name;
    voices.push({
      name,
      source: meta.source || 'unknown',
      created_at: meta.created_at || '',
      instruct: meta.instruct || '',
      ref_text: meta.ref_text || '',
      language: meta.language || '',
      protocol: meta.protocol || '',
      has_voice_pt: fs.existsSync(path.join(VOICES_DIR, entry.name, 'voice.pt')),
      audio_url: fs.existsSync(path.join(VOICES_DIR, entry.name, 'source.wav'))
        ? `/api/tts/voices/${encodeURIComponent(name)}/audio`
        : '',
    });
  }
  return voices.sort((left, right) => String(right.created_at).localeCompare(String(left.created_at)));
}

/** 供 clone --ref 使用的音色来源音频路径（跨引擎复用走 source.wav）。 */
export function voiceSourceFile(name) {
  if (!VOICE_NAME_PATTERN.test(String(name || ''))) return null;
  const base = path.resolve(VOICES_DIR);
  const directory = path.resolve(base, String(name));
  if (!directory.startsWith(base + path.sep)) return null;
  for (const candidate of ['source.wav', 'source_audio.wav']) {
    const file = path.join(directory, candidate);
    if (fs.existsSync(file)) return file;
  }
  return null;
}

function serializeInstance(slot) {
  if (!slot) return null;
  return {
    engine: slot.engine || '',
    pid: slot.pid ?? null,
    started_at: slot.started_at || '',
    load_s: slot.load_s ?? null,
    idle_timeout_s: slot.idle_timeout_s ?? null,
  };
}

// ── 常驻保活 ──
// TTS 项目支持两种「模型留在显存里复用」的方式：
//   1) 契约 `start --idle-timeout 0`（默认 0 = 永不自动退出）；
//   2) 实例已经在跑时不重启，由本进程定期发一次 start（等价 ping，会刷新 worker 的
//      空闲计时）把空闲超时顶住——好处是不用重付一次 8~70 秒的模型加载，已经预热好的
//      热态也保留下来。
// 前端只关心「常驻中/非常驻」，两种方式归一成 keep_alive 状态。
const KEEP_ALIVE_MIN_INTERVAL_MS = 60 * 1000;
let keepAlive = null;
let keepAliveNote = '';

function keepAliveIntervalMs() {
  const idleSeconds = Number(TTS.idleTimeoutSeconds) || 0;
  if (idleSeconds <= 0) return KEEP_ALIVE_MIN_INTERVAL_MS;
  return Math.max(KEEP_ALIVE_MIN_INTERVAL_MS, Math.floor((idleSeconds * 1000) / 2));
}

function keepAliveView() {
  if (!keepAlive) {
    return {enabled: false, engine: '', interval_s: 0, started_at: '', last_tick_at: '', note: keepAliveNote};
  }
  return {
    enabled: true,
    engine: keepAlive.engine,
    interval_s: Math.round(keepAlive.intervalMs / 1000),
    started_at: keepAlive.startedAt,
    last_tick_at: keepAlive.lastTickAt,
    note: keepAliveNote,
  };
}

export function clearTtsKeepAlive() {
  if (!keepAlive) return;
  clearInterval(keepAlive.timer);
  keepAlive = null;
}

/**
 * 打开常驻保活：同引擎只刷新活跃时间，实例已消失则顺带拉起。
 * 槽位被别的引擎占用（E_SLOT_BUSY）时自动关闭并把原因留给前端展示。
 */
export function setTtsKeepAlive(engine) {
  const id = String(engine || '').trim();
  clearTtsKeepAlive();
  keepAliveNote = '';
  if (!id) return keepAliveView();
  const intervalMs = keepAliveIntervalMs();
  const timer = setInterval(() => {
    runTtsCli(['start', '--engine', id], {timeoutMs: PROBE_TIMEOUT_MS * 6})
      .then(() => {
        if (keepAlive && keepAlive.engine === id) keepAlive.lastTickAt = nowIso();
      })
      .catch((error) => {
        if (error instanceof TtsError && error.code === 'E_SLOT_BUSY') {
          keepAliveNote = `${id} 的常驻保活已停止：${error.message}`;
          clearTtsKeepAlive();
          return;
        }
        keepAliveNote = `${id} 常驻保活失败：${error.message}`;
      });
  }, intervalMs);
  if (typeof timer.unref === 'function') timer.unref();
  keepAlive = {engine: id, timer, intervalMs, startedAt: nowIso(), lastTickAt: ''};
  return keepAliveView();
}

/** 聚合状态：一次 list 探测（引擎/实例/GPU/社区音色）+ 本机音色库；CLI 不可用时也返回完整结构。 */
export async function ttsStatus() {
  const base = {
    enabled: ttsConfigured,
    cli: {python: TTS.python, script: TTS.cli},
    config: {
      timeout_ms: TTS.timeoutMs,
      idle_timeout_seconds: TTS.idleTimeoutSeconds,
    },
    contract_version: loadContract()?.contract_version || '',
    engines: contractEngines(),
    instance: null,
    keep_alive: keepAliveView(),
    gpu: null,
    voices: [],
    gptsovits_voices: [],
    paths: null,
    error: '',
  };
  if (!ttsConfigured) {
    base.error = `未找到 TTS CLI（${TTS.cli}）。本板块依赖 E:\AI\TTS 契约环境，可在 .env.local 里用 TTS_PYTHON / TTS_CLI 指向其他位置。`;
    return base;
  }
  try {
    // 一次 CLI 调用拿全：契约的 status 只多出 worker 内存信息（这里用不到），
    // 少起一个 Python 进程，状态探测与设置页轮询都快一倍。
    const listEnvelope = await runTtsCli(['list'], {timeoutMs: PROBE_TIMEOUT_MS});
    const listInfo = listEnvelope.info || {};
    const runtime = new Map((listInfo.engines || []).map((engine) => [engine.engine, engine]));
    base.engines = base.engines.map((engine) => {
      const live = runtime.get(engine.id) || {};
      return {...engine, python_ok: Boolean(live.python_ok), running: Boolean(live.running), note: live.note || engine.note};
    });
    base.instance = serializeInstance(listInfo.running || null);
    base.gpu = listInfo.gpu || null;
    base.gptsovits_voices = Array.isArray(listInfo.gptsovits_voices) ? listInfo.gptsovits_voices : [];
    base.paths = listInfo.paths || null;
  } catch (error) {
    base.error = error instanceof TtsError ? `${error.message}${error.hint ? `\n${error.hint}` : ''}` : error.message;
    base.engines = base.engines.map((engine) => ({...engine, python_ok: null, running: false}));
  }
  base.voices = voicesFromDisk();
  return base;
}

// ── 实例控制 ──

// ── 引擎预热 ──
// 实测（RTX 5060 / Qwen3-TTS-12Hz-1.7B-CustomVoice，磁盘缓存已热）：
//   加载模型 9s（冷启动 ~50s）→ 实例内首次合成 21s → 之后同样文本 5s。
// 多出来的 ~16s 是首次推理的一次性 kernel 初始化，预热就是启动后先跑一条极短请求，
// 把这段开销从「用户第一次提交」挪到「预加载」阶段，产物直接丢弃。
const WARMUP_TEXT = '预热测试。';
const WARMUP_TIMEOUT_MS = 5 * 60 * 1000;

// 已预热的实例：engine → pid。复用中的实例只要预热过（或刚跑完成功任务），
// 就不再空跑一条合成，点「预加载」/切常驻都是秒回。
const warmedInstancePid = new Map();

/** 社区音色注册表（契约 §5.4 / 文件一览）：只用来给 gptsovits 挑一个预热音色。 */
function gptsovitsRegistryVoices() {
  try {
    const registry = JSON.parse(fs.readFileSync(path.join(TTS_ROOT, 'tts_workers', 'gptsovits_voices.json'), 'utf8'));
    return Object.keys(registry).filter((name) => !name.startsWith('_'));
  } catch {
    return [];
  }
}

/** 按引擎能力拼一条最小预热请求；缺前置条件时给出 skip 原因（预热失败不影响引擎已就绪）。 */
function warmupPlan(engine) {
  const spec = contractEngines().find((item) => item.id === engine);
  const out = path.join(os.tmpdir(), `mirror-tts-warmup-${engine}-${process.pid}.wav`);
  const voices = voicesFromDisk();
  const sourced = voices.find((voice) => voice.audio_url);
  if (engine === 'qwen-design') {
    return {args: ['design', '--instruct', '标准普通话女声，语速平稳', '--text', WARMUP_TEXT, '--out', out], out};
  }
  if (engine === 'qwen-preset') {
    const speakers = spec?.speakers || [];
    const speaker = speakers.includes('vivian') ? 'vivian' : speakers[0];
    if (!speaker) return {skip: '契约没有给出可用的预设音色'};
    return {args: ['preset', '--speaker', speaker, '--text', WARMUP_TEXT, '--out', out], out};
  }
  if (engine === 'gptsovits') {
    const community = gptsovitsRegistryVoices()[0];
    if (community) {
      return {args: ['preset', '--engine', 'gptsovits', '--speaker', community, '--text', WARMUP_TEXT, '--out', out], out};
    }
    if (!sourced) return {skip: 'GPT-SoVITS 社区音色表为空且音色库没有来源音频'};
    return {args: ['clone', '--engine', 'gptsovits', '--ref', voiceSourceFile(sourced.name), '--text', WARMUP_TEXT, '--out', out], out};
  }
  if (engine === 'qwen-base') {
    const prompt = voices.find((voice) => voice.has_voice_pt);
    if (prompt) return {args: ['say', '--voice', prompt.name, '--text', WARMUP_TEXT, '--out', out], out};
    if (!sourced) return {skip: '音色库为空，没有可用于预热的音色'};
    return {args: ['clone', '--engine', 'qwen-base', '--ref', voiceSourceFile(sourced.name), '--set', 'x_vector_only_mode=true', '--text', WARMUP_TEXT, '--out', out], out};
  }
  // indextts / megatts3：只有克隆能力，用音色库来源音频（indextts 自动截到 15 秒）
  if (!sourced) return {skip: '音色库为空，没有可用于预热的参考音频'};
  return {args: ['clone', '--engine', engine, '--ref', voiceSourceFile(sourced.name), '--text', WARMUP_TEXT, '--out', out], out};
}

/**
 * 跑一条极短合成把模型带进热态。成功返回 {ok, seconds, handle_s}；
 * 缺条件/显卡忙/合成失败都只是「没预热成」，不改变引擎已就绪的事实。
 * gpuHeld=true 表示调用方（startTtsEngine）已经拿着 GPU 锁，这里不再重复申请。
 */
export async function warmupTtsEngine(engine, {keepResident = false, gpuHeld = false} = {}) {
  const plan = warmupPlan(engine);
  if (plan.skip) return {ok: false, skipped: true, reason: plan.skip};
  const releaseGpu = gpuHeld ? null : acquireGpu('tts', `TTS 预热（${engine}）`);
  if (!gpuHeld && !releaseGpu) {
    const holder = gpuLockHolder();
    return {
      ok: false,
      skipped: true,
      reason: `显卡正被${holder?.owner === 'comfy' ? ' ComfyUI 生图' : '其他任务'}占用（${holder?.label || ''}），已跳过预热。`,
    };
  }
  const started = Date.now();
  try {
    const envelope = await runTtsCli(
      [...plan.args, '--init-set', `idle_timeout=${keepResident ? 0 : TTS.idleTimeoutSeconds}`],
      {timeoutMs: WARMUP_TIMEOUT_MS},
    );
    const pid = Number(envelope.info?.instance?.pid) || null;
    if (pid) warmedInstancePid.set(engine, pid);
    return {
      ok: true,
      seconds: Number(((Date.now() - started) / 1000).toFixed(1)),
      handle_s: envelope.timing?.handle_s ?? null,
    };
  } catch (error) {
    return {ok: false, reason: error instanceof TtsError ? error.message : String(error?.message || error)};
  } finally {
    if (releaseGpu) releaseGpu();
    try {
      fs.rmSync(plan.out, {force: true});
    } catch {
      // 临时产物（系统临时目录）删不掉不影响结果
    }
  }
}

/**
 * 启动（或复用）引擎实例。
 * - 已在跑同一引擎且未要求 force：直接复用，不重启（重启要重付一次模型加载）；
 * - keepResident=true：新实例用 `--idle-timeout 0`（永不自动退出），复用中的实例改用
 *   本进程保活把空闲超时顶住，两条路都不丢已经预热好的热态；
 * - warmup 默认开：启动后立刻预热，让用户第一次提交就是热态；已预热过的实例跳过；
 * - 加载与预热都占显存，期间持 GPU 锁，避免与 ComfyUI 生图同时上显存。
 */
export async function startTtsEngine({engine, force = false, switch: doSwitch = false, keepResident = false, warmup = true} = {}) {
  const id = String(engine || '').trim();
  if (!id) throw new TtsError('请指定要启动的引擎', {code: 'E_PARAM'});
  const known = contractEngines().some((item) => item.id === id);
  if (!known) throw new TtsError(`不支持的 TTS 引擎：${id}`, {code: 'E_PARAM'});

  const before = await ttsStatus();
  const current = before.instance?.engine || '';
  let slot = before.instance;
  let reused = false;
  const willLoad = current !== id || force;
  const willWarm = warmup !== false;

  let releaseGpu = null;
  if (willLoad || willWarm) {
    releaseGpu = acquireGpu('tts', `TTS 预加载（${id}）`);
    if (!releaseGpu) {
      const holder = gpuLockHolder();
      throw new TtsError(
        `显卡正被${holder?.owner === 'comfy' ? ' ComfyUI 生图' : '其他任务'}占用（${holder?.label || ''}），等它完成或先「释放内存」再加载引擎。`,
        {code: 'E_GPU_BUSY'},
      );
    }
  }

  let result;
  try {
    if (willLoad) {
      // keepResident=true 时不带 idle_timeout（契约默认 0 = 永不自动退出），模型常驻显存直到手动停止。
      const args = ['start', '--engine', id];
      if (!keepResident) args.push('--idle-timeout', String(TTS.idleTimeoutSeconds));
      if (force) args.push('--force');
      if (doSwitch) args.push('--switch');
      const envelope = await runTtsCli(args, {timeoutMs: PROBE_TIMEOUT_MS * 6});
      slot = serializeInstance(envelope.info?.slot || null);
      warmedInstancePid.delete(id);   // 新实例 = 冷态，第一次推理要重新热身
    } else {
      reused = true;
    }

    const alreadyWarm = reused && slot?.pid != null && warmedInstancePid.get(id) === slot.pid;
    result = !willWarm
      ? {ok: false, skipped: true, reason: '本次未请求预热'}
      : alreadyWarm
        ? {ok: true, skipped: true, reason: '实例已是热态，无需重复预热'}
        : await warmupTtsEngine(id, {keepResident, gpuHeld: true});
  } finally {
    if (releaseGpu) releaseGpu();
  }

  // 常驻意图落地：不常驻时清掉上一轮的保活，别让它在后台继续占着显存。
  if (keepResident) setTtsKeepAlive(id);
  else clearTtsKeepAlive();

  return {
    engine: id,
    slot,
    reused,
    resident: Boolean(slot && (keepResident || slot.idle_timeout_s === 0)),
    warmup: result,
  };
}

export async function stopTts() {
  clearTtsKeepAlive();
  warmedInstancePid.clear();
  const envelope = await runTtsCli(['stop'], {timeoutMs: PROBE_TIMEOUT_MS});
  return {stopped: Boolean(envelope.info?.stopped)};
}

/**
 * 释放内存按钮（TTS 侧）：停掉常驻实例释放显存。GPU 锁被占用时不执行——
 * 说明有一类任务正在跑，先等它完成；此时返回 released:false + 占用者。
 */
export async function releaseTtsResources() {
  const holder = gpuLockHolder();
  if (holder) {
    // 有任务在跑：拒绝释放，也不动常驻保活（被拒绝的操作不该有副作用）。
    return {released: false, reason: `有任务正在使用显卡（${holder.label || holder.owner}），完成后再释放。`, holder};
  }
  clearTtsKeepAlive();
  warmedInstancePid.clear();
  const status = await ttsStatus();
  if (!status.instance) {
    return {released: true, stopped: false, reason: '当前没有运行中的 TTS 实例，显存已空闲。'};
  }
  const envelope = await runTtsCli(['stop'], {timeoutMs: PROBE_TIMEOUT_MS});
  return {released: true, stopped: Boolean(envelope.info?.stopped), reason: 'TTS 实例已停止，显存已释放。'};
}

export async function ttsLog({engine, lines = 60} = {}) {
  const id = String(engine || '').trim();
  if (!id) throw new TtsError('请指定要查看日志的引擎', {code: 'E_PARAM'});
  // log 命令成功时输出纯文本（不是信封），失败时走 stderr——直接读文本。
  return new Promise((resolve, reject) => {
    if (!ttsConfigured) {
      reject(new TtsError('TTS CLI 不可用', {code: 'E_ENV_MISSING'}));
      return;
    }
    const child = spawn(TTS.python, [TTS.cli, 'log', '--engine', id, '--lines', String(Math.max(1, Math.min(400, lines)))], {
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const stdout = [];
    const stderr = [];
    const timer = setTimeout(() => child.kill(), 30000);
    if (typeof timer.unref === 'function') timer.unref();
    child.stdout.on('data', (chunk) => stdout.push(chunk));
    child.stderr.on('data', (chunk) => stderr.push(chunk));
    child.on('error', (error) => {
      clearTimeout(timer);
      reject(new TtsError(`无法启动 TTS CLI：${error.message}`));
    });
    child.on('close', (code) => {
      clearTimeout(timer);
      if (code === 0) {
        resolve({engine: id, text: Buffer.concat(stdout).toString('utf8').trim()});
        return;
      }
      const message = Buffer.concat(stderr).toString('utf8').trim() || `读取日志失败（exit ${code}）`;
      reject(new TtsError(message, {code: code === 8 ? 'E_NOT_FOUND' : 'E_INTERNAL'}));
    });
  });
}

/** 音色来源音频试听（voices/<名>/source.wav，不经过媒体库）。 */
export function serveVoiceSource(name) {
  const file = voiceSourceFile(name);
  if (!file) throw new TtsError('音色不存在或没有来源音频', {code: 'E_NOT_FOUND'});
  return {file, mimeType: 'audio/wav', name: `${path.basename(path.dirname(file))}-source.wav`};
}

// ── 任务模型 ──

const MODE_ENGINES = {
  design: () => 'qwen-design',
  say: () => 'qwen-base',
};

/** 模式 → 目标引擎。clone/preset 由调用方选择，其余固定。 */
function engineForTask({mode, engine, speaker_engine}) {
  if (mode === 'design') return MODE_ENGINES.design();
  if (mode === 'say') return MODE_ENGINES.say();
  if (mode === 'clone') {
    if (!CLONE_ENGINES.has(engine)) {
      throw new TtsError('声音克隆需要选择引擎：qwen-base / gptsovits / indextts / megatts3', {code: 'E_PARAM'});
    }
    return engine;
  }
  if (mode === 'preset') {
    const chosen = speaker_engine || 'qwen-preset';
    if (!PRESET_ENGINES.has(chosen)) {
      throw new TtsError('预设音色只支持 qwen-preset / gptsovits', {code: 'E_PARAM'});
    }
    return chosen;
  }
  throw new TtsError('不支持的模式', {code: 'E_PARAM'});
}

function normalizeTakes(value) {
  if (value === undefined || value === null || value === '') return 1;
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || parsed < 1 || parsed > MAX_TAKES) {
    throw new TtsError(`生成条数需要是 1-${MAX_TAKES} 的整数`, {code: 'E_PARAM'});
  }
  return parsed;
}

function normalizeLanguage(value) {
  if (value === undefined || value === null || value === '') return 'auto';
  const normalized = String(value).trim();
  if (!/^[a-zA-Z_]{2,12}$/.test(normalized)) {
    throw new TtsError('语言码不合法', {code: 'E_PARAM'});
  }
  // 统一简写或引擎原生取值都原样交给 CLI 翻译（不支持时报 E_LANG_UNSUPPORTED + hint）。
  return normalized.toLowerCase() === 'auto' ? 'auto' : normalized;
}

function normalizeParams(value) {
  if (value === undefined || value === null || value === '') return {};
  if (typeof value !== 'object' || Array.isArray(value)) {
    throw new TtsError('高级参数需要是对象', {code: 'E_PARAM'});
  }
  const result = {};
  for (const [key, raw] of Object.entries(value)) {
    if (raw === undefined || raw === null || raw === '') continue;
    if (!['string', 'number', 'boolean'].includes(typeof raw) && !Array.isArray(raw)) {
      throw new TtsError(`高级参数 ${key} 的值类型不支持`, {code: 'E_PARAM'});
    }
    result[key] = raw;
  }
  return result;
}

function validateVoiceName(name, label = '音色名') {
  if (!VOICE_NAME_PATTERN.test(String(name || ''))) {
    throw new TtsError(`${label}不能包含 \\ / : * ? " < > | 且不超过 64 字`, {code: 'E_PARAM'});
  }
  return String(name);
}

/**
 * 创建 TTS 任务：校验参数 → 落盘任务 → 排队。合成由队列串行执行（契约并发=1）。
 */
export function createTtsTask(payload) {
  if (!ttsConfigured) {
    throw new TtsError(`TTS CLI 不可用（${TTS.cli}）`, {code: 'E_ENV_MISSING'});
  }
  const mode = String(payload.mode || '').trim();
  if (!['design', 'say', 'clone', 'preset'].includes(mode)) {
    throw new TtsError('不支持的模式', {code: 'E_PARAM'});
  }
  const text = String(payload.text ?? '').trim();
  if (!text) throw new TtsError('请输入要合成的文本', {code: 'E_PARAM'});
  if (text.length > 5000) throw new TtsError('合成文本过长（上限 5000 字）', {code: 'E_PARAM'});

  const engine = engineForTask({mode, engine: String(payload.engine || '').trim(), speaker_engine: String(payload.speaker_engine || '').trim()});
  const takes = normalizeTakes(payload.takes);
  const language = normalizeLanguage(payload.language);
  const params = normalizeParams(payload.params);
  const wantSwitch = payload.switch === true || payload.switch === 'true';

  const task = {
    id: nextId('ttsTask'),
    task_no: newTaskNo(),
    user_id: localUser().id,
    status: 'queued',
    progress_percent: 0,
    mode,
    engine,
    engine_label: '',
    text,
    instruct: String(payload.instruct ?? '').trim().slice(0, 1000),
    voice: mode === 'say' ? validateVoiceName(payload.voice, '音色名') : '',
    speaker: mode === 'preset' ? String(payload.speaker || '').trim() : '',
    language,
    takes,
    save_as: mode === 'design' && payload.save_as ? validateVoiceName(payload.save_as, '保存音色名') : '',
    ref_token: '',
    ref_file: '',
    ref_filename: '',
    ref_text: String(payload.ref_text ?? '').trim(),
    ref_lang: '',
    params,
    switch: wantSwitch,
    outputs: [],
    saved_voice: '',
    warnings: [],
    timing: {},
    load_s: null,
    error_code: '',
    error_message: '',
    submitted_at: nowIso(),
    started_at: null,
    finished_at: null,
  };

  if (mode === 'design' && !task.instruct) {
    throw new TtsError('音色设计需要填写音色描述（instruct）', {code: 'E_PARAM'});
  }
  if (mode === 'preset' && !task.speaker) {
    throw new TtsError('预设音色需要选择 speaker', {code: 'E_PARAM'});
  }
  if (mode === 'clone') {
    if (payload.ref_token) {
      const upload = store.referenceUploads[String(payload.ref_token)];
      if (!upload || upload.kind !== 'tts_ref_audio') {
        throw new TtsError('参考音频上传结果不存在或已过期，请重新上传', {code: 'E_PARAM'});
      }
      const media = store.media[upload.mediaToken];
      if (!media || !fs.existsSync(media.file)) {
        throw new TtsError('参考音频文件已丢失，请重新上传', {code: 'E_PARAM'});
      }
      task.ref_token = upload.mediaToken;
      task.ref_file = media.file;
      task.ref_filename = upload.filename || media.name;
    }
    // gptsovits 可以用 --set voice=<社区音色> 从注册表带出参考音频，此时 ref 可省略。
    if (!task.ref_file && !(engine === 'gptsovits' && params.voice)) {
      throw new TtsError('声音克隆需要先上传参考音频', {code: 'E_PARAM'});
    }
    if (engine === 'gptsovits' && !task.ref_text && !params.voice) {
      throw new TtsError('GPT-SoVITS 需要填写参考音频的原话（ref_text）', {code: 'E_PARAM'});
    }
    task.ref_lang = payload.ref_lang ? normalizeLanguage(payload.ref_lang) : '';
  }

  const spec = contractEngines().find((item) => item.id === engine);
  task.engine_label = spec?.label || engine;

  store.ttsTasks.unshift(task);
  persist();
  schedule(task, 0);
  return task;
}

function schedule(task, delayMs) {
  const timer = setTimeout(() => {
    runTtsTask(task.task_no).catch((error) => {
      console.error(`[local-api] TTS 任务 ${task.task_no} 执行失败：${error.message}`);
      failTtsTask(task.task_no, error.message, error.code || 'local_failure');
    });
  }, Math.max(0, delayMs));
  if (typeof timer.unref === 'function') timer.unref();
}

// 合成串行执行：契约不支持并发请求，同时只会跑一个 CLI 合成进程。
let synthChain = Promise.resolve();
const runningChildren = new Map();

function buildArgs(task) {
  const args = [];
  if (task.switch) args.push('--switch');
  args.push('--init-set', `idle_timeout=${TTS.idleTimeoutSeconds}`);
  args.push('--timeout', String(Math.floor(TTS.timeoutMs / 1000)));
  const advanced = setArgs(task.params);

  if (task.mode === 'design') {
    args.unshift('design', '--instruct', task.instruct, '--text', task.text);
    if (task.language !== 'auto') args.push('--language', task.language);
    if (task.save_as) args.push('--save-as', task.save_as);
    if (task.takes > 1) args.push('--takes', String(task.takes));
    args.push(...advanced);
    return args;
  }
  if (task.mode === 'say') {
    args.unshift('say', '--voice', task.voice, '--text', task.text);
    if (task.language !== 'auto') args.push('--language', task.language);
    if (task.takes > 1) args.push('--takes', String(task.takes));
    args.push(...advanced);
    return args;
  }
  if (task.mode === 'clone') {
    args.unshift('clone', '--engine', task.engine, '--text', task.text);
    if (task.ref_file) args.push('--ref', task.ref_file);
    if (task.ref_text) args.push('--ref-text', task.ref_text);
    if (task.ref_lang) args.push('--ref-lang', task.ref_lang);
    if (task.language !== 'auto') args.push('--language', task.language);
    args.push(...advanced);
    return args;
  }
  // preset
  args.unshift('preset', '--speaker', task.speaker, '--text', task.text);
  if (task.engine === 'gptsovits') args.unshift('--engine', 'gptsovits');
  if (task.instruct && task.engine !== 'gptsovits') args.push('--instruct', task.instruct);
  if (task.language !== 'auto') args.push('--language', task.language);
  if (task.takes > 1) args.push('--takes', String(task.takes));
  args.push(...advanced);
  return args;
}

function ttsAudioMime(fileName) {
  const extension = path.extname(fileName).toLowerCase();
  return extension === '.wav' ? 'audio/wav' : 'application/octet-stream';
}

async function collectOutputs(task, envelope) {
  const outputs = [];
  let savedVoice = '';
  for (const [index, output] of (envelope.outputs || []).entries()) {
    const file = String(output.path || '');
    if (!file || !fs.existsSync(file)) continue;
    if (output.kind === 'voice_pt' || file.toLowerCase().endsWith('.pt')) {
      savedVoice = path.basename(path.dirname(file));
      continue;
    }
    if (!/\.(wav|mp3|flac|m4a|ogg)$/i.test(file)) continue;
    const buffer = fs.readFileSync(file);
    const token = saveMedia(buffer, {
      mimeType: ttsAudioMime(file),
      name: `tts-${task.task_no}-${output.kind === 'voice_pt' ? 'voice' : `t${index + 1}`}`,
    });
    outputs.push({
      token,
      name: path.basename(file),
      kind: 'audio',
      sr: Number(output.sr) || null,
      duration_s: Number(output.duration_s) || null,
      size: buffer.length,
    });
  }
  return {outputs, savedVoice};
}

async function runTtsTask(taskNo) {
  const task = findTtsTask(taskNo);
  if (!task || task.status !== 'queued') return;
  const previous = synthChain;
  let release;
  synthChain = new Promise((resolve) => {
    release = resolve;
  });

  await previous;
  const current = findTtsTask(taskNo);
  if (!current || current.status !== 'queued') {
    release();
    return;
  }

  // GPU 互斥：与 ComfyUI 生图共用一块 8G 显存。抢不到锁时任务失败并提示占用者；
  // 锁里带上任务号，前端弹窗选择「终止对方」时能精确取消这一条任务。
  const releaseGpu = acquireGpu('tts', `TTS 任务 ${taskNo}`, taskNo);
  if (!releaseGpu) {
    release();
    const holder = gpuLockHolder();
    failTtsTask(
      taskNo,
      `显卡正被${holder?.owner === 'comfy' ? ' ComfyUI 生图' : '其他 TTS 任务'}占用（${holder?.label || ''}），请等它完成，或先在工作台里「释放内存」再提交。`,
      'E_GPU_BUSY',
    );
    return;
  }

  current.status = 'running';
  current.progress_percent = 8;
  current.started_at = nowIso();
  persist();

  const updateProgress = (percent) => {
    const target = findTtsTask(taskNo);
    if (!target || target.status !== 'running') return;
    target.progress_percent = Math.max(target.progress_percent || 0, Math.min(95, Math.round(percent)));
    persist();
  };

  // 进度按「3 分钟内 15% → 88%」的时间 pacing 推进：CLI --json 模式不回传进度。
  const pacing = setInterval(() => {
    const target = findTtsTask(taskNo);
    if (!target || target.status !== 'running') return;
    const elapsed = Date.now() - new Date(target.started_at).getTime();
    updateProgress(15 + Math.min(1, elapsed / (3 * 60 * 1000)) * 73);
  }, 2000);
  if (typeof pacing.unref === 'function') pacing.unref();

  try {
    const args = buildArgs(current);
    // 注意：必须带 --json，否则 CLI 按普通模式只往 stdout 打产物路径，无法拿到 sr/时长/告警。
    const child = spawn(TTS.python, [TTS.cli, ...args, '--json'], {
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    runningChildren.set(taskNo, child);
    const stdout = [];
    const stderr = [];
    child.stdout.on('data', (chunk) => stdout.push(chunk));
    child.stderr.on('data', (chunk) => stderr.push(chunk));
    const exitCode = await new Promise((resolve, reject) => {
      const killer = setTimeout(() => {
        child.kill();
        resolve(-1);
      }, TTS.timeoutMs + 60000);
      if (typeof killer.unref === 'function') killer.unref();
      child.on('error', (error) => {
        clearTimeout(killer);
        reject(error);
      });
      child.on('close', (code) => {
        clearTimeout(killer);
        resolve(code === null ? -1 : code);
      });
    });
    runningChildren.delete(taskNo);
    clearInterval(pacing);

    const finalTask = findTtsTask(taskNo);
    if (!finalTask) return;
    if (finalTask.status === 'canceled') return;

    const text = Buffer.concat(stdout).toString('utf8').trim();
    let envelope = null;
    try {
      envelope = JSON.parse(text);
    } catch {
      envelope = null;
    }
    if (exitCode !== 0 || !envelope?.ok) {
      const reported = envelope?.error || {};
      const stderrTail = Buffer.concat(stderr).toString('utf8').trim().split(/\r?\n/).slice(-6).join('\n');
      throw new TtsError(reported.message || `TTS CLI 退出码 ${exitCode}`, {
        code: reported.code || 'E_INTERNAL',
        hint: reported.hint || stderrTail,
      });
    }

    const {outputs, savedVoice} = await collectOutputs(finalTask, envelope);
    finalTask.outputs = outputs;
    finalTask.saved_voice = savedVoice;
    finalTask.warnings = Array.isArray(envelope.warnings) ? envelope.warnings : [];
    finalTask.timing = envelope.timing || {};
    finalTask.load_s = Number(envelope.info?.load_s) || null;
    finalTask.status = 'succeeded';
    finalTask.progress_percent = 100;
    finalTask.finished_at = nowIso();
    // 成功跑过一条合成就说明这个实例已经是热态：之后的复用/预加载不必再空跑预热。
    const instancePid = Number(envelope.info?.instance?.pid) || null;
    if (instancePid) warmedInstancePid.set(finalTask.engine || current.engine, instancePid);
    persist();
  } catch (error) {
    clearInterval(pacing);
    runningChildren.delete(taskNo);
    failTtsTask(taskNo, error.message, error instanceof TtsError ? error.code : 'local_failure', error.hint || '');
    throw error;
  } finally {
    releaseGpu();
    release();
  }
}

function failTtsTask(taskNo, message, code = 'local_failure', hint = '') {
  const task = findTtsTask(taskNo);
  if (!task || task.status === 'canceled' || task.status === 'succeeded') return;
  task.status = 'failed';
  task.error_code = code;
  task.error_message = hint ? `${message}\n${hint}` : message;
  task.finished_at = nowIso();
  persist();
}

export function findTtsTask(taskNo) {
  return store.ttsTasks.find((task) => task.task_no === taskNo) || null;
}

/** 取消：杀掉 CLI 进程并标记 canceled。契约没有中断接口，引擎可能在后台完成本次合成后随 idle_timeout 释放。 */
export function cancelTtsTask(taskNo) {
  const task = findTtsTask(taskNo);
  if (!task) return null;
  if (task.status === 'queued' || task.status === 'running') {
    const child = runningChildren.get(taskNo);
    if (child) {
      try {
        child.kill();
      } catch {
        // 进程可能刚好退出
      }
      runningChildren.delete(taskNo);
    }
    task.status = 'canceled';
    task.error_message = '任务已取消';
    task.finished_at = nowIso();
    persist();
  }
  return task;
}

export function deleteTtsTask(taskNo) {
  const index = store.ttsTasks.findIndex((task) => task.task_no === taskNo);
  if (index < 0) return false;
  store.ttsTasks.splice(index, 1);
  persist();
  return true;
}

// ── 序列化 ──

export function ttsOutputs(task) {
  return (task.outputs || []).map((output, index) => ({
    id: `${task.task_no}-${index}`,
    name: output.name,
    kind: output.kind,
    sr: output.sr ?? null,
    duration_s: output.duration_s ?? null,
    size: output.size ?? null,
    audio_url: downloadURL(output.token),
    preview_url: downloadURL(output.token, {preview: true}),
    download_url: downloadURL(output.token, {download: true}),
  }));
}

export function serializeTtsTask(task) {
  return {
    task_no: task.task_no,
    task_kind: 'tts',
    status: task.status,
    progress_percent: task.progress_percent,
    mode: task.mode,
    engine: task.engine,
    engine_label: task.engine_label,
    text: task.text,
    instruct: task.instruct || '',
    voice: task.voice || '',
    speaker: task.speaker || '',
    language: task.language || 'auto',
    takes: task.takes ?? 1,
    save_as: task.save_as || '',
    ref_filename: task.ref_filename || '',
    ref_text: task.ref_text || '',
    params: task.params || {},
    outputs: ttsOutputs(task),
    saved_voice: task.saved_voice || '',
    warnings: task.warnings || [],
    timing: task.timing || {},
    load_s: task.load_s ?? null,
    error_code: task.error_code || '',
    error_message: task.error_message || '',
    quota_cost: 0,
    submitted_at: task.submitted_at,
    started_at: task.started_at,
    finished_at: task.finished_at,
  };
}

/** 历史列表条目：与生图/小说条目共列，task_no 带 tts: 前缀供前端分发。 */
export function ttsHistoryItem(task) {
  const outputs = ttsOutputs(task);
  return {
    task_no: `tts:${task.task_no}`,
    task_kind: 'tts',
    tts_task_no: task.task_no,
    status: task.status,
    progress_percent: task.progress_percent,
    generation_mode: `tts_${task.mode}`,
    prompt: task.text,
    negative_prompt: '',
    style_code: '',
    size_code: '',
    quality_code: 'auto',
    reference_image_name: task.ref_filename || '',
    reference_image_attached: Boolean(task.ref_token),
    reference_image_data_url: '',
    quota_cost: 0,
    error_code: task.error_code || '',
    error_message: task.error_message || '',
    topic_id: null,
    mode: task.mode,
    engine: task.engine,
    engine_label: task.engine_label,
    outputs,
    warnings: task.warnings || [],
    submitted_at: task.submitted_at,
    started_at: task.started_at,
    finished_at: task.finished_at,
    images: [],
  };
}
