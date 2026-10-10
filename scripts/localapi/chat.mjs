// 本地模式的真实对话渠道：OpenAI 兼容中转站 / 本机 LM Studio（仅本地开发者模式使用）。
//
// 契约来源：桌面 Comfy\doc\03-LLM请求契约.md
//   POST {base}/v1/chat/completions   流式 SSE，逐行 data: JSON
//   正文 delta.content，思考过程 delta.reasoning_content
//   reasoning_effort 控制思考等级；**不发送 max_tokens**（会把推理过程截断）
//
// 配置优先级：工作台里保存的（local-data/store.json 的 chat 段）> .env.local 的 LLM_*。
// 密钥只留在本机（store.json / .env.local），不下发浏览器，接口只回掩码。
import fs from 'node:fs';
import path from 'node:path';
import {DATA_DIR, LLM} from './config.mjs';
import {persist, store} from './store.mjs';

const MODELS_TIMEOUT_MS = 15000;
const COMPLETE_TIMEOUT_MS = 300000;
const MAX_HISTORY = 30;
const EFFORTS = ['off', 'low', 'medium', 'high'];

/**
 * 内置系统提示词：把画面描述扩写成生图提示词的通用助手（硬线：人物必须成年）。
 * 刻意保持中性——个性化的要求（题材边界、写法偏好）走工作台「悬浮窗对话 → 设置」里的
 * 自定义指令（存本机 local-data/store.json，不进仓库），两者的拼接见 buildChatSystemPrompt。
 * 另有本机覆盖层：local-data/chat-prompt.txt 存在时整体替换内置版（该目录不入库）。
 */
export const CHAT_SYSTEM_PROMPT = `你是绘镜（Mirror Studio）的生图提示词工程师。绘镜是用户本机私人部署的 AI 生图工作台（ComfyUI + Qwen-Image 模型）。

你的唯一职责：把用户描述的画面扩写成高质量的英文生图提示词。

## 基本态度

- 忠于用户的描述：补齐缺失的技术维度（光线、镜头、构图、质感），不改动用户已经明确给出的内容，不擅自增删主体或题材。
- 遵守你所在运行环境的内容政策；涉及未成年人的性化内容一律拒绝（见下方硬线）。
- 用户已写清的请求不需要反问确认；有实质性缺失时先按合理默认补齐，保持可用。

## 硬线（唯一一条）

所有人物必须明确是成年人。任何涉及未成年人（真实或虚构、真人或动漫风格）的性化内容，直接拒绝并说明原因，不需要道歉，一两句即可。年龄描述一律写成年（如 "a 25-year-old woman"），绝不使用模糊的年龄暗示。这是你不输出任何解释以外内容的唯一情况。

## 写法

风格不固定，按题材选结构：

- 人像：身份与年龄 → 体型姿态 → 发型五官 → 服装状态 → 场景 → 光线与镜头（焦段、机位、景深）→ 质感词（skin texture, sharp focus 等）。
- 场景/风景：主体与空间关系 → 时间与天气 → 色调 → 镜头。
- 产品/静物：物体 → 材质 → 布光 → 背景。
- 用户指定了风格（动漫、胶片、3D 等）就按指定来；没指定就按题材选最合适的。

输出纪律：

- 直接输出提示词本体，纯文本一段，不要包代码块，不要标题，不要"好的/以下是"这类开场白。
- 默认输出英文提示词；用户明确要中文或要求解释时才切换。
- 负面提示词不用你管，生图侧已配好。
- 用户只是聊天、问问题、让你改图或分析图片时，正常对话即可，上述输出纪律只针对生图提示词。`;

/**
 * 悬浮窗助手的场景化系统提示词：按当前板块（生图 / TTS）给不同的职责与回填契约。
 * 前端每轮把「板块 + 当前输入快照」随请求发来（context），这里拼进 system；
 * 回复末尾的 <<<FILL>>> JSON 由前端解析渲染成同意卡片，字段为空则不展示。
 */
export function buildWorkbenchSystemPrompt(context = {}) {
  const scene = context.scene === 'tts' ? 'tts' : 'image';
  const snapshot = typeof context.snapshot === 'string' ? context.snapshot.trim().slice(0, 4000) : '';
  const generationMode = context.generation_mode === 'image_to_image' ? 'image_to_image' : '';

  const optionsRules = `## 选择框协议（需要用户做选择时必须遵守）

回复正文之后，如果希望用户在几个选项里挑一个，在正文最后另起一行输出一个 JSON 块：

<<<OPTIONS>>>
[{"label": "选项一的短标题", "prompt": "选中后直接给用户的完整内容或一句话说明"}, {"label": "选项二", "prompt": "..."}]
<<<END>>>

- 用在：推荐玩法/风格/构图方向、给几版文案、问音色方向……凡是"你来定"的场景都先出选项框。
- 3~6 个选项，label 一句话内说清玩法亮点；prompt 写"用户选中后你会如何输出"的预告或直接给成品。
- 用户会直接点选，选中的 prompt 会作为他的下一条消息发给你，你收到后直接给最终成果。
- 纯聊天、用户已明确要求的内容，不要画蛇添足输出这个块。`;

  const fillRules = `## 参数计划与回填契约（最重要）

你的核心产出是一份「创作参数计划」：直接给出可执行的完整参数，用户看一眼点同意就自动入框。回复正文之后，把计划放进一个 JSON 块：

<<<FILL>>>
{"prompt": "...", "negative_prompt": "...", "ratio": "16:9", "resolution": "1k", "batch_count": 2, "style_code": "cinematic", "note": "一句话给用户看的说明"}
<<<END>>>

生图场景字段约定（都可选，按需给）：

- prompt：完整的正向提示词（中文，规范扩写后的成品，不是片段）。
- negative_prompt：负面提示词；只在该题材真正受益时给（如人物手部、文字水印），普通题材省略。
- ratio：常用值 1:1 / 4:3 / 3:2 / 16:9 / 9:16，按题材构图需要选，不写就保持用户当前选择。
- resolution：1k 或 2k（特殊题材对比细节时用 2k）。
- batch_count：建议生成张数，1~4。**默认一律 1 张**——本地串行出图慢，单张够判断就不浪费等待时间；只有题材本身需要对比才能多张（如"油画 vs 高清"这类显式对比、同一构图挑光影变体、角色设定三视图），并在正文里用一句话说明为什么需要 N 张。用户没要求对比时绝不给 2 张以上。
- style_code：general（通用）/ cinematic（电影感）/ anime（动漫）/ photography（摄影），与题材匹配时才给。
- note：一句话说明这份计划的意图（展示在同意按钮旁边）。

交互纪律：

- 一次只给一份计划；用户挑完选项、或对计划提修改意见后，更新计划再次输出。
- 用户回复"可以/同意/就这样/开始"等认可语义时，输出的 FILL 就是最终版（和上一份一致或按用户修改微调）。
- 图生图（用户上传了参考图）：prompt 要写清楚"保留参考图的什么、改变什么"，主体一致性描述放在最前面；用户提到多张参考图时，提示他把所有参考图都上传到参考图区，每张的角色在 prompt 里说明（如"图1是人物，图2是场景"）。
- 没有可回填的内容（纯聊天、提问、列选项）就不输出 FILL 块。
- JSON 之外不要解释这些字段；前端会把 JSON 渲染成一张「同意入框」卡片，用户点一下所有参数自动填好，生成按钮永远由用户自己按。`;

  const ttsFillRules = `## 回填契约（最重要）

本轮有可以直接填回工作台的成果时，在正文最后另起一行输出一个 JSON 块：

<<<FILL>>>
{"instruct": "...", "text": "..."}
<<<END>>>

- 字段按当前模式给（见上），用户没要的一律省略；text 是要合成的话术正文。
- 用户回复"可以/同意/就这样"等认可语义时，输出的 FILL 就是最终版。
- 没有可回填的内容（纯聊天、提问、列选项）就不输出这个块；前端会渲染成「同意入框」卡片。`;

  if (scene === 'tts') {
    const mode = typeof context.mode === 'string' ? context.mode : '';
    const modeGuide = {
      design: `当前模式是「音色设计」：帮用户把一个人物/角色的声音形象写成音色描述（instruct）。用中文写，一段话，覆盖性别年龄、音色质感（清亮/低沉/沙哑…）、语气语速、情感与场景感。先和用户确认人物形象（用户一句话带过时按常识给一版），用户问什么答什么；确认好后给完整 instruct。可回填字段：instruct、text（用户要合成的话术时才给）。`,
      say: `当前模式是「音色复用」：音色已在音色库里选定，重点是陪用户打磨要朗读的文本（text）。可回填字段：text。`,
      clone: `当前模式是「声音克隆」：音色来自参考音频，不需要设计音色描述。帮用户准备参考音频对应的文本（ref_text，与音频内容逐字一致）和要合成的文本（text）。可回填字段：ref_text、text。`,
      preset: `当前模式是「预设音色」：音色是现成官方音色，不需要设计音色描述；帮用户写风格指令（instruct，一句话，如"用特别慵懒的语气说"）和要合成的文本（text）。可回填字段：instruct、text。`,
    }[mode] || `当前模式未知：用户问什么答什么，需要时给出建议。`;

    return `你是绘镜（Mirror Studio）TTS 语音合成工作台里的 AI 助手。绘镜是用户本机私人部署的 AI 创作工作台。

你的职责：陪用户完成一次语音合成——按当前模式准备好「要合成什么内容、用什么声音」。

${modeGuide}

## 硬线

所有人物必须明确是成年人；涉及未成年人的性化内容直接拒绝并说明原因，不需要道歉。要合成的文本只能是用户有权使用的内容：不帮用户大段复述受版权保护的作品正文。

## 交互纪律

- 用户问音色特征就答音色特征，问合成文本就给合成文本，不要一次把所有字段都塞给用户。
- 需要用户做选择时用选择框协议；确认好后给最终内容。
- 直接说结论，不要"好的/以下是"这类开场白。

${ttsFillRules}

${optionsRules}${snapshot ? `\n\n## 工作台当前状态（用户正在看的界面，供你参考）\n\n${snapshot}` : ''}`;
  }

  return `${CHAT_SYSTEM_PROMPT}

## 多轮交互：从模糊想法到参数计划

- 用户说"随便推荐点好玩的玩法 / 给点灵感"这类开放请求时：用选择框协议给 3~6 个带编号的玩法选项（label 一句话说清玩法与效果），用户点选后你再给完整参数计划。
- 一次只给一份参数计划；用户选中后，正文简单说明这个玩法的看点（一两句），然后输出 FILL。
- 图生图参考图支持多张上传${generationMode === 'image_to_image' ? '（用户已上传参考图，当前是图生图）' : ''}；用户想固定主体/多图合成时，引导他把图都传到参考图区。
- 用户回复"可以 / 同意 / 开始"等认可语义时，直接输出最终 FILL（不要再反问）。

${fillRules}

${optionsRules}${snapshot ? `\n\n## 工作台当前状态（用户正在看的界面，供你参考）\n\n${snapshot}` : ''}`;
}

/** 组装最终 system 消息：本机覆盖层或内置提示词 + 用户自定义附加偏好。 */
export function buildChatSystemPrompt(customInstruction = '') {
  const base = loadLocalPromptOverride() || CHAT_SYSTEM_PROMPT;
  const extra = String(customInstruction || '').trim();
  if (!extra) return base;
  return `${base}\n\n## 用户的个人偏好（用户自己配置的，遵守它）\n\n${extra}`;
}

/**
 * 悬浮窗流式对话的 system 组装：工作台上下文走场景化提示词（buildWorkbenchSystemPrompt），
 * 本机覆盖层（chat-prompt.txt）仍然整体替换生图场景的基础版；自定义偏好照常附加。
 */
export function buildWorkbenchMessages(history = [], {context = {}, customInstruction = ''} = {}) {
  const override = loadLocalPromptOverride();
  const scene = context?.scene === 'tts' ? 'tts' : 'image';
  let system;
  if (scene === 'image' && override) {
    // 覆盖层只针对生图场景；TTS 场景的职责与回填契约不走覆盖层。
    system = buildWorkbenchSystemPrompt(context).replace(CHAT_SYSTEM_PROMPT, override);
  } else {
    system = buildWorkbenchSystemPrompt(context);
  }
  const extra = String(customInstruction || '').trim();
  if (extra) system = `${system}\n\n## 用户的个人偏好（用户自己配置的，遵守它）\n\n${extra}`;

  const cleaned = (Array.isArray(history) ? history : [])
    .filter((item) => item && (item.role === 'user' || item.role === 'assistant') && messageContentText(item.content))
    .slice(-MAX_HISTORY)
    .map((item) => ({role: item.role, content: item.content}));
  return [{role: 'system', content: system}, ...cleaned];
}

/** 取消息 content 的纯文本（多模态数组时取 text 段，用于判断空消息与快照截断）。 */
function messageContentText(content) {
  if (typeof content === 'string') return content.trim();
  if (Array.isArray(content)) {
    return content
      .map((part) => (typeof part?.text === 'string' ? part.text : ''))
      .join('')
      .trim();
  }
  return '';
}

/**
 * 清理历史里的多模态消息：图片 data URL 只保留最近 2 条 user 消息里的（控制请求体积），
 * 更早的降级成纯文本。
 */
function trimHistoryImages(history) {
  let imageBudget = 2;
  const cleaned = [];
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const item = history[index];
    if (!item || (item.role !== 'user' && item.role !== 'assistant')) continue;
    if (Array.isArray(item.content)) {
      const hasImage = item.content.some((part) => part?.type === 'image_url');
      if (hasImage && imageBudget > 0) {
        imageBudget -= 1;
        cleaned.unshift(item);
        continue;
      }
      cleaned.unshift({
        role: item.role,
        content: messageContentText(item.content) || '（此前的图片消息）',
      });
      continue;
    }
    cleaned.unshift(item);
  }
  return cleaned;
}

/** 本机覆盖层：local-data/chat-prompt.txt 整体替换内置提示词（纯文本正文，不含 JS 语法）。 */
function loadLocalPromptOverride() {
  try {
    const text = fs.readFileSync(path.join(DATA_DIR, 'chat-prompt.txt'), 'utf8').trim();
    return text || null;
  } catch {
    return null; // 文件不存在 = 用内置中性版
  }
}

/** 对话渠道的报错：配置类用 400，上游类用 502（server.mjs 按 status 回给前端）。 */
export class ChatError extends Error {
  constructor(message, status = 400) {
    super(message);
    this.status = status;
  }
}

// ── 配置（工作台可改，落在 local-data/store.json 的 chat 段）──

function defaults() {
  return {
    base_url: '',
    api_key: '',
    model: '',
    reasoning_effort: 'high',
    temperature: 0.9,
    custom_instruction: '',
  };
}

function saved() {
  if (!store.chat || typeof store.chat !== 'object') store.chat = defaults();
  return store.chat;
}

function normalizeBaseURL(value) {
  return String(value || '').trim().replace(/\/+$/, '');
}

function normalizeEffort(value, fallback = 'high') {
  const effort = String(value || '').trim();
  return EFFORTS.includes(effort) ? effort : fallback;
}

/** 解析出真正要用的配置：工作台保存的优先，缺项回落到 .env.local 的 LLM_*。 */
export function resolveChatConfig() {
  const state = saved();
  const baseURL = normalizeBaseURL(state.base_url) || normalizeBaseURL(LLM.baseURL);
  const model = String(state.model || LLM.modelName || '').trim();
  const apiKey = String(state.api_key || LLM.apiKey || '').trim();
  const fromWorkbench = Boolean(normalizeBaseURL(state.base_url) || String(state.model || '').trim() || String(state.api_key || '').trim());
  return {
    base_url: baseURL,
    api_path: LLM.apiPath,
    api_key: apiKey,
    model,
    reasoning_effort: normalizeEffort(state.reasoning_effort, normalizeEffort(LLM.reasoningEffort)),
    temperature: Number.isFinite(Number(state.temperature)) ? Number(state.temperature) : LLM.temperature,
    custom_instruction: String(state.custom_instruction || ''),
    // 本机 LM Studio 常常不要密钥，所以只要求地址 + 模型。
    configured: Boolean(baseURL && model),
    source: fromWorkbench ? 'workbench' : baseURL || model ? 'env' : 'none',
  };
}

/** 给前端的视图：密钥只回掩码，不回原文。 */
export function chatConfigView() {
  const config = resolveChatConfig();
  return {
    base_url: config.base_url,
    api_path: config.api_path,
    model: config.model,
    reasoning_effort: config.reasoning_effort,
    temperature: config.temperature,
    custom_instruction: config.custom_instruction,
    configured: config.configured,
    source: config.source,
    has_key: Boolean(config.api_key),
    key_hint: config.api_key ? `${config.api_key.slice(0, 3)}…${config.api_key.slice(-4)}` : '',
    efforts: [...EFFORTS],
  };
}

/** 保存工作台里的配置。api_key 留空表示沿用旧值；clear_api_key=true 才清空。 */
export function saveChatConfig(patch = {}) {
  const state = saved();
  if (patch.base_url !== undefined) state.base_url = normalizeBaseURL(patch.base_url);
  if (patch.model !== undefined) state.model = String(patch.model || '').trim().slice(0, 200);
  if (patch.reasoning_effort !== undefined) state.reasoning_effort = normalizeEffort(patch.reasoning_effort, 'high');
  if (patch.temperature !== undefined) {
    const temperature = Number(patch.temperature);
    if (!Number.isFinite(temperature) || temperature < 0 || temperature > 2) {
      throw new ChatError('随机性需要在 0 ~ 2 之间');
    }
    state.temperature = temperature;
  }
  if (patch.custom_instruction !== undefined) state.custom_instruction = String(patch.custom_instruction || '').slice(0, 4000);
  if (patch.clear_api_key === true) state.api_key = '';
  else if (patch.api_key !== undefined && String(patch.api_key).trim()) state.api_key = String(patch.api_key).trim();
  if (state.base_url && !/^https?:\/\//i.test(state.base_url)) {
    throw new ChatError('接口地址需要以 http:// 或 https:// 开头');
  }
  persist();
  return chatConfigView();
}

// ── 上游调用 ──

function headersFor(config, json = false) {
  const headers = {};
  if (json) headers['Content-Type'] = 'application/json';
  if (config.api_key) headers.Authorization = `Bearer ${config.api_key}`;
  return headers;
}

function describeFetchError(error) {
  if (error?.name === 'AbortError') return '请求超时';
  const code = error?.cause?.code || error?.code || '';
  if (code === 'ECONNREFUSED' || /ECONNREFUSED/.test(String(error?.message || ''))) return '连接被拒绝（地址或端口不对 / 服务未启动）';
  if (code === 'ENOTFOUND' || code === 'EAI_AGAIN') return '地址无法解析';
  if (/certificate|SSL/i.test(String(error?.message || ''))) return 'HTTPS 证书校验失败';
  return error?.message || '无法连接';
}

function requireConfig() {
  const config = resolveChatConfig();
  if (!config.configured) {
    throw new ChatError('尚未配置对话中转站：打开工作台右下角的悬浮窗 →「设置」填写接口地址与模型（或在 .env.local 里填 LLM_*）');
  }
  return config;
}

/** 组装请求体：思考等级来自配置，**永远不带 max_tokens**（思考过程不限长）。 */
function chatBody(config, messages, {stream, temperature, effort}) {
  const body = {model: config.model, messages, stream};
  const temp = Number.isFinite(Number(temperature)) ? Number(temperature) : config.temperature;
  if (Number.isFinite(temp)) body.temperature = temp;
  const level = effort === undefined ? config.reasoning_effort : effort;
  if (level && level !== 'off') body.reasoning_effort = level;
  return body;
}

async function fetchWithTimeout(url, options, timeoutMs) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), Math.max(1000, timeoutMs));
  try {
    return await fetch(url, {...options, signal: controller.signal});
  } finally {
    clearTimeout(timer);
  }
}

async function readErrorBody(response) {
  const text = await response.text().catch(() => '');
  return String(text || '').slice(0, 300);
}

/** 请求上游模型列表（设置面板的「获取模型 / 测试连通」）。 */
export async function listRelayModels({baseUrl = '', apiKey = ''} = {}) {
  const config = resolveChatConfig();
  const base = normalizeBaseURL(baseUrl) || config.base_url;
  const key = String(apiKey || '').trim() || config.api_key;
  if (!base) throw new ChatError('请先填写接口地址');
  let response;
  try {
    response = await fetchWithTimeout(`${base}/v1/models`, {headers: key ? {Authorization: `Bearer ${key}`} : {}}, MODELS_TIMEOUT_MS);
  } catch (error) {
    throw new ChatError(`连接上游失败：${describeFetchError(error)}`, 502);
  }
  const raw = await response.text();
  if (!response.ok) throw new ChatError(`上游返回 HTTP ${response.status}：${raw.slice(0, 200)}`, 502);
  let decoded;
  try {
    decoded = JSON.parse(raw);
  } catch {
    throw new ChatError('上游返回的不是 JSON（确认地址指向 OpenAI 兼容中转站）', 502);
  }
  const models = (Array.isArray(decoded?.data) ? decoded.data : [])
    .map((item) => String(item?.id || '').trim())
    .filter(Boolean);
  return models;
}

/** 非流式补全：AI 生成描述、场景分析等一次拿完整正文的调用走这里。 */
export async function chatComplete({messages, temperature = null, effort, timeoutMs = COMPLETE_TIMEOUT_MS} = {}) {
  const config = requireConfig();
  let response;
  try {
    response = await fetchWithTimeout(
      `${config.base_url}${config.api_path}`,
      {
        method: 'POST',
        headers: headersFor(config, true),
        body: JSON.stringify(chatBody(config, messages, {stream: false, temperature, effort})),
      },
      timeoutMs,
    );
  } catch (error) {
    throw new ChatError(`对话渠道调用失败：${describeFetchError(error)}`, 502);
  }
  const raw = await response.text();
  if (!response.ok) throw new ChatError(`对话渠道返回 HTTP ${response.status}：${raw.slice(0, 300)}`, 502);
  let decoded;
  try {
    decoded = JSON.parse(raw);
  } catch {
    throw new ChatError('对话渠道返回的不是 JSON', 502);
  }
  const content = decoded?.choices?.[0]?.message?.content;
  if (typeof content !== 'string' || !content.trim()) {
    throw new ChatError('对话渠道没有返回正文内容（模型可能只输出了思考过程）', 502);
  }
  return content.trim();
}

/** 流式补全：把上游的 SSE 原样交给调用方（悬浮窗对话用）。 */
export async function openChatStream({messages, signal} = {}) {
  const config = requireConfig();
  let response;
  try {
    response = await fetch(`${config.base_url}${config.api_path}`, {
      method: 'POST',
      headers: headersFor(config, true),
      body: JSON.stringify(chatBody(config, messages, {stream: true})),
      signal,
    });
  } catch (error) {
    if (signal?.aborted) throw new ChatError('已取消');
    throw new ChatError(`对话渠道调用失败：${describeFetchError(error)}`, 502);
  }
  if (!response.ok) {
    throw new ChatError(`对话渠道返回 HTTP ${response.status}：${await readErrorBody(response)}`, 502);
  }
  return response;
}

// ── 工作台接口用的两条路（形状一致，上层不需要分支）──

/**
 * 整理悬浮窗里的对话历史：客户端只发 user/assistant，system 提示词在这里统一注入。
 * @returns {Array<{role: string, content: string}>}
 */
export function buildChatMessages(history = [], customInstruction = '') {
  const messages = [{role: 'system', content: buildChatSystemPrompt(customInstruction)}];
  const cleaned = (Array.isArray(history) ? history : [])
    .filter((item) => item && (item.role === 'user' || item.role === 'assistant') && String(item.content || '').trim())
    .slice(-MAX_HISTORY)
    .map((item) => ({role: item.role, content: String(item.content).slice(0, 20000)}));
  return [...messages, ...cleaned];
}

/**
 * 悬浮窗流式对话入口（POST /api/chat/stream）：按工作台上下文组装 system，
 * 历史（含多模态图片消息）做预算裁剪后透传。
 * @returns {Array<{role: string, content: string|Array}>}
 */
export function buildStreamMessages(messages = [], {context = {}, customInstruction = ''} = {}) {
  const system = buildWorkbenchMessages([], {context, customInstruction})[0];
  const cleaned = trimHistoryImages(
    (Array.isArray(messages) ? messages : [])
      .filter((item) => item && (item.role === 'user' || item.role === 'assistant'))
  );
  return [system, ...cleaned];
}

/**
 * 非流式对话（POST /api/chat）：配了中转站就调真实接口，没配就用本地模拟回复。
 * @returns {Promise<string>}
 */
export async function chatReply({prompt, history = []}) {
  const config = resolveChatConfig();
  if (!config.configured) return localMockReply(prompt, config);
  const messages = buildChatMessages(
    [
      ...history
        .filter((item) => item?.parts?.[0]?.text)
        .map((item) => ({role: item.role === 'model' ? 'assistant' : 'user', content: item.parts[0].text})),
      {role: 'user', content: String(prompt || '')},
    ],
    config.custom_instruction,
  );
  return chatComplete({messages});
}

/** 未配置时的本地模拟回复：明确标注来源与配置入口，不冒充真实模型。 */
export function localMockReply(prompt, config = resolveChatConfig()) {
  const angles = [
    '先确定主体与视角，再补光线和材质，最后加情绪词。',
    '把画面拆成「主体 / 环境 / 光线 / 镜头」，每一部分都给一个具体名词。',
    '同一个构思先用两种不同风格各生成一张，再挑更接近的那张继续迭代。',
    '如果要图生图，参考图只保留构图，描述里写清想替换掉的元素。',
  ];
  let seed = 0;
  for (const char of String(prompt || '')) seed = (seed * 31 + char.codePointAt(0)) % 1000003;
  return [
    '【本地模拟回复】还没有配置对话中转站，下面是本地生成的提示词建议：',
    '',
    `你刚才的问题是：${prompt}`,
    '',
    `建议：${angles[seed % angles.length]}`,
    `可以直接试这条提示词：${prompt}，电影级实拍摄影，自然光，浅景深，细节丰富，画面干净`,
    '',
    '接入真实模型：工作台右下角悬浮窗 →「设置」里填写接口地址（线上中转站或本机 LM Studio）、模型和密钥；',
    '也可以在 .env.local 里填 LLM_BASE_URL / LLM_MODEL_NAME / LLM_API_KEY 后重启本地服务。',
    config.base_url || config.model ? `（当前解析到的地址：${config.base_url || '未填'}；模型：${config.model || '未填'}）` : '',
  ]
    .filter((line) => line !== '')
    .join('\n');
}
