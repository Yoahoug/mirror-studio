// 悬浮窗助手与工作台之间的结构化契约：
// - AssistantWorkbenchState：助手发给服务端的「当前界面状态快照」（随每轮对话透传）；
// - AssistantFillPayload：模型回复末尾 <<<FILL>>> JSON 解析出的「参数计划」，
//   用户点「同意入框」后由工作台按 scene 白名单择字段写入（空字段前端已隐藏，这里再做一次防御）；
// - AssistantOptionsPayload：模型回复末尾 <<<OPTIONS>>> JSON 解析出的选择框，
//   用户点选后把该项 prompt 作为下一条消息发出。

export type AssistantScene = "image" | "tts";

export interface AssistantWorkbenchState {
  /** 当前板块：生图（create/comfy）= image，TTS = tts。 */
  scene: AssistantScene;
  /** TTS 的合成模式（design/say/clone/preset）；生图场景可空。 */
  mode?: string;
  /** 生图的创作模式（text_to_image/image_to_image）；TTS 场景可空。 */
  generation_mode?: string;
  /** 人类可读的当前输入快照，拼进 system prompt 供模型参考。 */
  snapshot: string;
}

/** 生图场景可回填：完整参数计划（提示词 + 画面参数 + 建议张数）。 */
export interface ImageFillPayload {
  prompt?: string;
  negative_prompt?: string;
  /** 常用值 1:1 / 4:3 / 3:2 / 16:9 / 9:16，空则保持用户当前选择。 */
  ratio?: string;
  /** 1k / 2k，空则保持。 */
  resolution?: string;
  /** 建议生成张数 1~4（本地串行，逐张提交）。 */
  batch_count?: number;
  /** general / cinematic / anime / photography。 */
  style_code?: string;
  /** 一句话给用户看的计划说明。 */
  note?: string;
}

/** TTS 场景可回填：音色描述 / 风格指令 / 要合成的文本 / 参考音频对应文本。 */
export interface TtsFillPayload {
  instruct?: string;
  style?: string;
  text?: string;
  ref_text?: string;
}

export type AssistantFillPayload = ImageFillPayload & TtsFillPayload;

/**
 * 工作台句柄（React 19 ref-as-prop，悬浮窗助手持有）：
 * - getAssistantState：读当前输入快照（随对话发给服务端）；
 * - applyAssistantFill：把参数计划写入表单；
 * - matchesAssistantFill：当前表单是否已与计划一致（动态「已入框」判定，
 *   用户入框后又手改了表单 → 自动复归为可点状态）。
 */
export interface AssistantWorkbenchHandle {
  getAssistantState: () => AssistantWorkbenchState;
  applyAssistantFill: (fill: AssistantFillPayload) => void;
  matchesAssistantFill: (fill: AssistantFillPayload) => boolean;
}

/** 选择框：模型给出 3~6 个候选项，用户点选后 prompt 作为下一条消息发出。 */
export interface AssistantOption {
  label: string;
  prompt: string;
}

export type AssistantOptionsPayload = AssistantOption[];

/** 把 fill JSON 映射成界面文案：键顺序即卡片展示顺序。 */
export const ASSISTANT_FILL_LABELS: Record<string, string> = {
  prompt: "正向提示词",
  negative_prompt: "负面提示词",
  ratio: "画面比例",
  resolution: "分辨率",
  batch_count: "建议张数",
  style_code: "风格",
  note: "计划说明",
  instruct: "音色描述",
  style: "风格指令",
  text: "合成文本",
  ref_text: "参考音频文本",
};

/** 用户的认可语义（中英常见说法）：命中即代表「同意当前参数计划」。 */
export const APPROVAL_PATTERN =
  /^(可以|好的|好|行|嗯|嗯嗯|ok|okay|OK|OKay|Ok|yes|Yes|YES|同意|没问题|就这样|就这样吧|开始|生成|走起|干|冲|对了可以|可以了|行了|成|中|要|要了|确认|确认无误|同意了|批准|通过|go|Go|GO|fine|Fine|sure|Sure|yep|Yep|yeah|Yeah)\s*[。.!！~～]*$/;
