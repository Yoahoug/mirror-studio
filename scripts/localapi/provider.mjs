// 渠道调用层：配了真实渠道就调真实接口，没配就用本地确定性结果。
// 两条路的返回值形状一致，上层（任务流水线）不需要分支。
import {LLM, PROVIDER, providerConfigured, llmConfigured} from './config.mjs';
import {hashSeed, placeholderPNG} from './png.mjs';

function joinURL(baseURL, apiPath) {
  return `${String(baseURL || '').replace(/\/+$/, '')}/${String(apiPath || '').replace(/^\/+/, '')}`;
}

async function withTimeout(url, options, timeoutSeconds) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), Math.max(5, timeoutSeconds) * 1000);
  try {
    return await fetch(url, {...options, signal: controller.signal});
  } finally {
    clearTimeout(timer);
  }
}

// ── 生图 ──

/**
 * @returns {Promise<{buffer: Buffer, mimeType: string, source: string, requestId: string}>}
 */
export async function generateImage({
  prompt,
  sizeCode,
  qualityCode = 'auto',
  negativePrompt = '',
  reference = null,
}) {
  if (!providerConfigured) {
    return {
      buffer: placeholderPNG(prompt, sizeCode),
      mimeType: 'image/png',
      source: 'local-placeholder',
      requestId: `local-${hashSeed(prompt).toString(16)}`,
    };
  }

  const payload = {
    model: PROVIDER.modelName,
    prompt: negativePrompt ? `${prompt}\n\nNegative prompt: ${negativePrompt}` : prompt,
    size: sizeCode || '1024x1024',
    quality: qualityCode || 'auto',
    response_format: 'b64_json',
    n: 1,
  };
  if (reference?.dataURL) {
    payload.image = reference.dataURL;
  }

  const response = await withTimeout(
    joinURL(PROVIDER.baseURL, PROVIDER.apiPath),
    {
      method: 'POST',
      headers: {'Content-Type': 'application/json', Authorization: `Bearer ${PROVIDER.apiKey}`},
      body: JSON.stringify(payload),
    },
    PROVIDER.timeoutSeconds,
  );

  const raw = await response.text();
  if (!response.ok) {
    throw new Error(`图片渠道返回 HTTP ${response.status}：${raw.slice(0, 300)}`);
  }
  let decoded;
  try {
    decoded = JSON.parse(raw);
  } catch {
    throw new Error(`图片渠道返回的不是 JSON：${raw.slice(0, 200)}`);
  }
  const first = decoded?.data?.[0];
  if (!first) throw new Error('图片渠道没有返回图片数据');

  if (first.b64_json) {
    return {
      buffer: Buffer.from(first.b64_json, 'base64'),
      mimeType: 'image/png',
      source: 'provider',
      requestId: String(decoded.id || first.revised_prompt || 'provider'),
    };
  }
  if (first.url) {
    const imageResponse = await withTimeout(first.url, {method: 'GET'}, PROVIDER.timeoutSeconds);
    if (!imageResponse.ok) {
      throw new Error(`下载渠道返回的图片失败：HTTP ${imageResponse.status}`);
    }
    return {
      buffer: Buffer.from(await imageResponse.arrayBuffer()),
      mimeType: imageResponse.headers.get('content-type') || 'image/png',
      source: 'provider',
      requestId: String(decoded.id || 'provider'),
    };
  }
  throw new Error('图片渠道返回结构里既没有 b64_json 也没有 url');
}

// ── 对话 ──

/**
 * 没有配置 LLM 渠道时返回一段可辨识的本地模拟回复，
 * 让工作台对话面板在零配置下也能走通（回复内容明确标注为本地模拟）。
 */
export async function chatReply({prompt, history = []}) {
  if (!llmConfigured) {
    return localChatReply(prompt);
  }
  const messages = [
    {role: 'system', content: '你是绘镜工作台的创作助手，请用简体中文回答，语气简洁、可执行。'},
    ...history
      .filter((item) => item?.parts?.[0]?.text)
      .map((item) => ({role: item.role === 'model' ? 'assistant' : 'user', content: item.parts[0].text})),
    {role: 'user', content: prompt},
  ];
  const response = await withTimeout(
    joinURL(LLM.baseURL, LLM.apiPath),
    {
      method: 'POST',
      headers: {'Content-Type': 'application/json', Authorization: `Bearer ${LLM.apiKey}`},
      body: JSON.stringify({model: LLM.modelName, messages, temperature: 0.7}),
    },
    LLM.timeoutSeconds,
  );
  const raw = await response.text();
  if (!response.ok) {
    throw new Error(`对话渠道返回 HTTP ${response.status}：${raw.slice(0, 300)}`);
  }
  let decoded;
  try {
    decoded = JSON.parse(raw);
  } catch {
    throw new Error('对话渠道返回的不是 JSON');
  }
  const content = decoded?.choices?.[0]?.message?.content;
  if (typeof content !== 'string' || !content.trim()) {
    throw new Error('对话渠道没有返回内容');
  }
  return content.trim();
}

function localChatReply(prompt) {
  const seed = hashSeed(prompt);
  const angles = [
    '先确定主体与视角，再补光线和材质，最后加情绪词。',
    '把画面拆成「主体 / 环境 / 光线 / 镜头」，每一部分都给一个具体名词。',
    '同一个构思先用两种不同风格各生成一张，再挑更接近的那张继续迭代。',
    '如果要图生图，参考图只保留构图，描述里写清想替换掉的元素。',
  ];
  return [
    '【本地模拟回复】当前没有配置 LLM 渠道，下面是本地生成的提示词建议：',
    '',
    `你刚才的问题是：${prompt}`,
    '',
    `建议：${angles[seed % angles.length]}`,
    `可以直接试这条提示词：${prompt}，电影级实拍摄影，自然光，浅景深，细节丰富，画面干净`,
    '',
    '想接入真实对话渠道，在仓库根目录 .env.local 里填 LLM_BASE_URL / LLM_MODEL_NAME / LLM_API_KEY 后重启本地服务即可。',
  ].join('\n');
}

// ── 小说场景分析 ──

const SCENE_SYSTEM_PROMPT = [
  '你是小说视觉化导演，需要把小说正文拆成适合生成电影级概念图的场景。',
  '只输出 JSON 数组，不要输出解释文字；每个对象包含字段：',
  'number（序号，从 1 开始）、title（中文短标题）、chapter（所在章节）、',
  'story_significance（故事意义）、excerpt（原文摘录）、',
  'visual_prompt（200-300 词的英文视觉描述，写实、电影级）。',
].join('');

/**
 * @returns {Promise<{scenes: Array, source: string}>}
 */
export async function planScenes({text, maxScenes, targetPlot = ''}) {
  if (llmConfigured) {
    try {
      const scenes = await remoteScenePlan({text, maxScenes, targetPlot});
      if (scenes.length) return {scenes, source: `${LLM.modelName}（真实渠道）`};
    } catch (error) {
      // 真实渠道失败时退回本地抽取，保证本地模式不会因为渠道抖动而卡死。
      console.warn(`[local-api] 场景分析调用真实渠道失败，改用本地抽取：${error.message}`);
    }
  }
  return {scenes: localScenePlan({text, maxScenes, targetPlot}), source: '本地抽取（未配置 LLM 渠道）'};
}

async function remoteScenePlan({text, maxScenes, targetPlot}) {
  const instruction = targetPlot
    ? `围绕以下指定剧情只挑 1 个最适合画成电影概念图的镜头：\n${targetPlot}\n`
    : `按情节顺序挑选不超过 ${maxScenes} 个最震撼、最适合电影级概念图的视觉场景。\n`;
  const response = await withTimeout(
    joinURL(LLM.baseURL, LLM.apiPath),
    {
      method: 'POST',
      headers: {'Content-Type': 'application/json', Authorization: `Bearer ${LLM.apiKey}`},
      body: JSON.stringify({
        model: LLM.modelName,
        temperature: targetPlot ? 0.65 : 0.7,
        messages: [
          {role: 'system', content: SCENE_SYSTEM_PROMPT},
          {role: 'user', content: `${instruction}\n小说全文：\n${text}`},
        ],
      }),
    },
    LLM.timeoutSeconds,
  );
  const raw = await response.text();
  if (!response.ok) {
    throw new Error(`HTTP ${response.status}：${raw.slice(0, 200)}`);
  }
  const decoded = JSON.parse(raw);
  const content = decoded?.choices?.[0]?.message?.content || '';
  return parseScenePlan(content, targetPlot ? 1 : maxScenes);
}

export function parseScenePlan(content, limit) {
  let text = String(content || '').trim();
  const fence = /```(?:json)?\s*([\s\S]*?)```/.exec(text);
  if (fence) text = fence[1].trim();
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    const start = text.indexOf('[');
    const end = text.lastIndexOf(']');
    if (start < 0 || end <= start) return [];
    try {
      parsed = JSON.parse(text.slice(start, end + 1));
    } catch {
      return [];
    }
  }
  if (parsed && !Array.isArray(parsed)) {
    parsed = parsed.scenes || parsed['场景'] || parsed.items || [];
  }
  if (!Array.isArray(parsed)) return [];
  return parsed
    .slice(0, Math.max(1, limit))
    .map((item, index) => normalizeScene(item, index + 1))
    .filter((scene) => scene.title && scene.visual_prompt);
}

function pick(object, keys, fallback) {
  for (const key of keys) {
    const value = object?.[key];
    if (value !== undefined && value !== null && String(value).trim() !== '') {
      return String(value).trim();
    }
  }
  return fallback;
}

function normalizeScene(raw, fallbackNumber) {
  if (typeof raw !== 'object' || raw === null) {
    return {
      number: fallbackNumber,
      title: `场景 ${fallbackNumber}`,
      chapter: '',
      story_significance: '',
      excerpt: '',
      visual_prompt: String(raw || '').trim(),
    };
  }
  const number = Number.parseInt(pick(raw, ['number', '编号', 'index'], String(fallbackNumber)), 10);
  return {
    number: Number.isFinite(number) && number > 0 ? number : fallbackNumber,
    title: pick(raw, ['title', '中文标题', '场景标题'], `场景 ${fallbackNumber}`).slice(0, 160),
    chapter: pick(raw, ['chapter', '所在章节', '章节'], '').slice(0, 160),
    story_significance: pick(raw, ['story_significance', '故事意义', '意义'], ''),
    excerpt: pick(raw, ['excerpt', '原文摘录', '摘录'], ''),
    visual_prompt: pick(raw, ['visual_prompt', '详细英文视觉描述', '画面描述', 'prompt'], ''),
  };
}

/**
 * 本地场景抽取：不调用任何模型，按正文顺序均匀取若干段落，
 * 用首句当标题、段落当摘录，并拼一段英文视觉描述。
 * 结果明确标注来源，避免和真实 AI 分析混淆。
 */
export function localScenePlan({text, maxScenes, targetPlot = ''}) {
  const paragraphs = String(text || '')
    .split(/\n+/)
    .map((line) => line.trim())
    .filter((line) => line.replace(/\s/g, '').length >= 30);
  if (!paragraphs.length) {
    return [
      {
        number: 1,
        title: '场景 1',
        chapter: '',
        story_significance: '正文段落过短，本地模式无法按段落抽取场景。',
        excerpt: String(text || '').slice(0, 120),
        visual_prompt: buildVisualPrompt('a quiet interior scene with soft window light'),
      },
    ];
  }

  const wanted = targetPlot ? 1 : Math.max(1, Math.min(maxScenes, Math.ceil(paragraphs.length / 3)));

  if (targetPlot) {
    const keyword = targetPlot.replace(/\s/g, '').slice(0, 8);
    const index = paragraphs.findIndex((paragraph) => keyword && paragraph.includes(keyword));
    const paragraph = paragraphs[index >= 0 ? index : Math.floor(paragraphs.length / 2)];
    return [sceneFromParagraph(paragraph, 1, `指定剧情：${targetPlot}`, index)];
  }

  const scenes = [];
  for (let index = 0; index < wanted; index += 1) {
    const position = Math.min(
      paragraphs.length - 1,
      Math.round(((index + 0.5) / wanted) * (paragraphs.length - 1)),
    );
    const paragraph = paragraphs[position];
    const chapter = detectChapter(text, position, paragraphs.length);
    scenes.push(sceneFromParagraph(paragraph, index + 1, '', position, chapter));
  }
  return dedupeScenes(scenes);
}

function detectChapter(text, position, total) {
  const slice = String(text || '').slice(0, Math.max(1, Math.round((position / Math.max(1, total)) * String(text || '').length)));
  const matches = slice.match(/第[0-9零一二三四五六七八九十百千]+章[^\n]{0,24}/g);
  if (matches?.length) return matches[matches.length - 1].trim().slice(0, 40);
  return `全文约 ${Math.round((position / Math.max(1, total)) * 100)}% 处`;
}

function sceneFromParagraph(paragraph, number, significance, position, chapter = '') {
  const sentence = paragraph.split(/(?<=[。！？.!?])/)[0] || paragraph;
  const title = sentence.replace(/[。！？.!?"'“”]/g, '').slice(0, 18) || `场景 ${number}`;
  const excerpt = paragraph.length > 140 ? `${paragraph.slice(0, 140)}…` : paragraph;
  return {
    number,
    title: `场景 ${number}·${title}`,
    chapter: chapter || `全文约 ${Math.round((position / Math.max(1, position + 1)) * 100)}% 处`,
    story_significance:
      significance ||
      `本地抽取：正文第 ${position + 1} 段附近的情节画面，不包含模型分析（配置 LLM 渠道后会自动改为 AI 场景分析）。`,
    excerpt,
    visual_prompt: buildVisualPrompt(excerpt),
  };
}

function buildVisualPrompt(basis) {
  const compact = String(basis).replace(/\s+/g, ' ').slice(0, 220);
  return [
    'Cinematic photorealistic concept image for a novel scene, live-action film still,',
    'dramatic natural lighting, tactile materials, atmospheric depth, physically plausible lens,',
    'high-end production design, emotionally intense, vivid and dynamic.',
    'Avoid cartoon, anime, manga, illustration and painterly styles.',
    `Scene content: ${compact}`,
  ].join(' ');
}

function dedupeScenes(scenes) {
  const seen = new Set();
  const unique = [];
  for (const scene of scenes) {
    if (seen.has(scene.excerpt)) continue;
    seen.add(scene.excerpt);
    unique.push({...scene, number: unique.length + 1});
  }
  return unique.length ? unique : scenes;
}
