import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  Brain,
  Check,
  ChevronDown,
  Copy,
  Loader2,
  MessageSquare,
  Plus,
  RotateCcw,
  Send,
  Settings,
  Sparkles,
  Trash2,
  Wand2,
  X,
} from "lucide-react";
import {
  fetchChatConfig,
  fetchChatRelayModels,
  saveChatConfig,
  streamChat,
  type ChatProviderConfig,
  type ChatReasoningEffort,
} from "../services/apiService";

// 悬浮窗对话（仅本地开发者模式）：延续参考项目（Dreamframe 的 DreamPilot）的形态——
// 会话列表 + 流式思考过程 + 「填回描述」，配置项改成工作台里直接可改的线上中转站。
// system prompt 在服务端（scripts/localapi/chat.mjs），这里只发 user/assistant 历史。

const SESSIONS_KEY = "mirror.chat.sessions";
const ACTIVE_SESSION_KEY = "mirror.chat.activeSession";
const STORAGE_INTERVAL_MS = 1200;
const MAX_SESSIONS = 20;

interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  reasoning?: string;
  error?: boolean;
}

interface ChatSession {
  id: string;
  title: string;
  updatedAt: number;
  messages: ChatMessage[];
}

const EFFORT_OPTIONS: Array<{ value: ChatReasoningEffort; label: string; note: string }> = [
  { value: "off", label: "关闭", note: "不发送思考等级" },
  { value: "low", label: "低", note: "快，思考短" },
  { value: "medium", label: "中", note: "均衡" },
  { value: "high", label: "高", note: "默认，写词更细" },
];

const ENDPOINT_PRESETS = [
  { label: "线上中转站", baseURL: "http://172.0.0.1:3000" },
  { label: "本机 LM Studio", baseURL: "http://127.0.0.1:1234" },
];

const DEMO_PROMPTS = ["黄昏天台上的少女，电影感", "赛博朋克夜市街景", "一只穿宇航服的柴犬"];

function newId(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

function createSession(): ChatSession {
  return { id: newId(), title: "新对话", updatedAt: Date.now(), messages: [] };
}

function loadSessions(): ChatSession[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(SESSIONS_KEY) || "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item) => item?.id && Array.isArray(item.messages)).slice(0, MAX_SESSIONS);
  } catch {
    return [];
  }
}

function sessionTitle(messages: ChatMessage[]): string {
  const first = messages.find((message) => message.role === "user");
  return first?.content.trim().slice(0, 24) || "新对话";
}

/** 从最后一条助手消息里提取可回填的提示词（去掉代码块围栏）。 */
export function extractPromptForFill(messages: ChatMessage[]): string | null {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (message.role !== "assistant" || message.error) continue;
    const text = message.content.trim();
    if (!text) continue;
    const unwrapped = text.replace(/^```[a-zA-Z]*\n([\s\S]*?)\n```$/m, "$1").trim();
    return unwrapped.slice(0, 4000);
  }
  return null;
}

interface ChatWidgetProps {
  addToast: (message: string, type?: "success" | "error" | "info") => void;
  /** 把提示词填进当前生图板块的「创作描述」；不在板块里时返回 false（调用方改成复制）。 */
  onFillPrompt: (prompt: string) => boolean;
}

export default function ChatWidget({ addToast, onFillPrompt }: ChatWidgetProps) {
  const [open, setOpen] = useState(false);
  const [showSessions, setShowSessions] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [sessions, setSessions] = useState<ChatSession[]>(() => {
    const loaded = loadSessions();
    return loaded.length ? loaded : [createSession()];
  });
  const [activeId, setActiveId] = useState<string>(() => {
    const saved = window.localStorage.getItem(ACTIVE_SESSION_KEY) || "";
    return saved;
  });
  const [input, setInput] = useState("");
  const [isStreaming, setIsStreaming] = useState(false);
  const [streamError, setStreamError] = useState("");
  const [copiedId, setCopiedId] = useState("");
  const [config, setConfig] = useState<ChatProviderConfig | null>(null);

  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const dirtyRef = useRef(false);

  const activeSession = useMemo(
    () => sessions.find((session) => session.id === activeId) || sessions[0] || null,
    [sessions, activeId]
  );

  // 首次打开时校准 activeId（不落地一个不存在的会话）。
  useEffect(() => {
    if (!sessions.some((session) => session.id === activeId)) {
      setActiveId(sessions[0]?.id || "");
    }
  }, [sessions, activeId]);

  useEffect(() => {
    if (activeId) window.localStorage.setItem(ACTIVE_SESSION_KEY, activeId);
  }, [activeId]);

  // 对话配置从服务端读（密钥只回掩码），打开悬浮窗时刷新一次。
  const refreshConfig = useCallback(async () => {
    try {
      setConfig(await fetchChatConfig());
    } catch {
      // 本地服务不可用时保持旧值，发送时会给出明确报错。
    }
  }, []);

  useEffect(() => {
    if (open) void refreshConfig();
  }, [open, refreshConfig]);

  // 会话清单防抖持久化（流式过程中会频繁更新）。
  useEffect(() => {
    if (!dirtyRef.current) return;
    const timer = window.setTimeout(() => {
      window.localStorage.setItem(SESSIONS_KEY, JSON.stringify(sessions.slice(0, MAX_SESSIONS)));
      dirtyRef.current = false;
    }, STORAGE_INTERVAL_MS);
    return () => window.clearTimeout(timer);
  }, [sessions]);

  useEffect(() => {
    if (!open) return;
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [activeSession?.messages.length, isStreaming, open]);

  const updateSession = useCallback((id: string, updater: (session: ChatSession) => ChatSession) => {
    dirtyRef.current = true;
    setSessions((prev) => prev.map((session) => (session.id === id ? { ...updater(session), updatedAt: Date.now() } : session)));
  }, []);

  const handleNewSession = () => {
    const fresh = createSession();
    dirtyRef.current = true;
    setSessions((prev) => [fresh, ...prev]);
    setActiveId(fresh.id);
    setShowSessions(false);
    setInput("");
  };

  const handleDeleteSession = (id: string) => {
    setSessions((prev) => {
      const next = prev.filter((session) => session.id !== id);
      const ensured = next.length ? next : [createSession()];
      dirtyRef.current = true;
      window.localStorage.setItem(SESSIONS_KEY, JSON.stringify(ensured));
      if (id === activeId) setActiveId(ensured[0].id);
      return ensured;
    });
  };

  const sendMessage = async (text: string) => {
    const content = text.trim();
    if (!content || isStreaming || !activeSession) return;
    if (!config?.configured) {
      addToast("还没有配置对话中转站：在设置里填接口地址与模型", "info");
      setShowSettings(true);
      return;
    }
    setInput("");
    setStreamError("");
    setIsStreaming(true);

    const userMessage: ChatMessage = { id: newId(), role: "user", content };
    const assistantId = newId();
    const history = [...activeSession.messages, userMessage]
      .filter((message) => !message.error && message.content.trim())
      .map((message) => ({ role: message.role, content: message.content }));

    updateSession(activeSession.id, (session) => ({
      ...session,
      title: session.messages.length === 0 ? sessionTitle([userMessage]) : session.title,
      messages: [...session.messages, userMessage, { id: assistantId, role: "assistant", content: "" }],
    }));

    const patchAssistant = (patch: Partial<ChatMessage>) => {
      updateSession(activeSession.id, (session) => ({
        ...session,
        messages: session.messages.map((message) => (message.id === assistantId ? { ...message, ...patch } : message)),
      }));
    };

    const controller = new AbortController();
    abortRef.current = controller;
    let answer = "";
    let reasoning = "";

    try {
      await streamChat(
        history,
        {
          onDelta: (delta) => {
            if (delta.reasoning) reasoning += delta.reasoning;
            if (delta.content) answer += delta.content;
            patchAssistant({ content: answer, reasoning: reasoning || undefined });
          },
        },
        controller.signal
      );
      if (!answer && !reasoning) {
        patchAssistant({ content: "（上游没有返回内容）", error: true });
      } else if (!answer && reasoning) {
        patchAssistant({ content: "（模型只输出了思考过程，没有正文；可以把思考等级调低再试）", error: true });
      }
    } catch (error: any) {
      const aborted = error instanceof DOMException && error.name === "AbortError";
      if (!aborted) {
        const message = error?.message || String(error);
        setStreamError(message);
        patchAssistant({ content: `请求失败：${message}`, error: true });
      }
    } finally {
      setIsStreaming(false);
      abortRef.current = null;
    }
  };

  const handleStop = () => {
    abortRef.current?.abort();
  };

  const handleFill = () => {
    const prompt = extractPromptForFill(activeSession?.messages || []);
    if (!prompt) return;
    const filled = onFillPrompt(prompt);
    navigator.clipboard?.writeText(prompt).catch(() => {});
    addToast(filled ? "已填入创作描述" : "提示词已复制；切到生图板块粘贴即可", filled ? "success" : "info");
  };

  const canFill = extractPromptForFill(activeSession?.messages || []) !== null;
  const configured = Boolean(config?.configured);

  return (
    <>
      {/* 悬浮球 */}
      {!open && (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="fixed bottom-24 right-4 z-[60] flex cursor-pointer items-center justify-center rounded-full bg-gradient-to-br from-[#6d5dfc] to-indigo-500 text-white shadow-[0_14px_36px_-10px_rgba(109,93,252,0.7)] transition hover:scale-105 lg:bottom-6 lg:right-6"
          style={{ height: "3.25rem", width: "3.25rem" }}
          title="绘镜助手 · 生图提示词对话"
        >
          <Sparkles className="h-6 w-6" />
          {!configured ? (
            <span className="absolute -right-0.5 -top-0.5 h-3 w-3 rounded-full border-2 border-white bg-amber-400" />
          ) : null}
        </button>
      )}

      {/* 悬浮窗 */}
      <AnimatePresence>
        {open && (
          <motion.div
            initial={{ opacity: 0, y: 18, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 18, scale: 0.97 }}
            transition={{ type: "spring", stiffness: 320, damping: 30 }}
            className="fixed bottom-24 right-3 z-[60] flex h-[min(640px,calc(100vh-8rem))] w-[min(420px,calc(100vw-1.5rem))] flex-col overflow-hidden rounded-[1.6rem] border border-gray-100 bg-white/95 shadow-[0_30px_80px_-20px_rgba(15,23,42,0.45)] backdrop-blur-2xl lg:bottom-6 lg:right-6"
          >
            {/* 头部 */}
            <div className="flex items-center gap-2 border-b border-gray-100 bg-gradient-to-r from-indigo-50/80 to-violet-50/60 px-4 py-3">
              <div className="grid h-8 w-8 place-items-center rounded-xl bg-gradient-to-br from-[#6d5dfc] to-indigo-500 text-white">
                <Sparkles className="h-4 w-4" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="text-sm font-black text-gray-900">绘镜助手</div>
                <div className="truncate text-[11px] font-bold text-gray-400">
                  {configured ? `${config?.model || "未选模型"} · ${config?.base_url || ""}` : "未配置中转站 · 点右侧齿轮设置"}
                </div>
              </div>
              <button
                type="button"
                onClick={() => {
                  setShowSessions((value) => !value);
                  setShowSettings(false);
                }}
                className={`rounded-lg p-2 text-gray-500 transition hover:bg-white/80 hover:text-gray-700 ${showSessions ? "bg-white/90 text-gray-700" : ""}`}
                title="会话列表"
              >
                <MessageSquare className="h-4 w-4" />
              </button>
              <button
                type="button"
                onClick={() => {
                  setShowSettings((value) => !value);
                  setShowSessions(false);
                }}
                className={`rounded-lg p-2 text-gray-500 transition hover:bg-white/80 hover:text-gray-700 ${showSettings ? "bg-white/90 text-gray-700" : ""}`}
                title="设置中转站"
              >
                <Settings className="h-4 w-4" />
              </button>
              <button
                type="button"
                onClick={() => setOpen(false)}
                className="rounded-lg p-2 text-gray-500 transition hover:bg-white/80 hover:text-gray-700"
                title="收起"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {/* 会话列表 */}
            {showSessions && (
              <div className="border-b border-gray-100 bg-white/70 px-3 py-2">
                <button
                  type="button"
                  onClick={handleNewSession}
                  className="mb-2 flex w-full cursor-pointer items-center gap-2 rounded-xl border border-indigo-100 bg-indigo-50/70 px-3 py-2 text-sm font-bold text-indigo-700 transition hover:bg-indigo-100/70"
                >
                  <Plus className="h-4 w-4" /> 新对话
                </button>
                <div className="max-h-44 space-y-1 overflow-y-auto">
                  {sessions.map((session) => (
                    <div
                      key={session.id}
                      className={`group flex items-center gap-2 rounded-xl px-3 py-2 text-sm transition ${
                        session.id === activeId ? "bg-indigo-100/80 text-indigo-900" : "text-gray-600 hover:bg-gray-100/70"
                      }`}
                    >
                      <button
                        type="button"
                        className="min-w-0 flex-1 cursor-pointer truncate text-left"
                        onClick={() => {
                          setActiveId(session.id);
                          setShowSessions(false);
                        }}
                      >
                        {session.title || "新对话"}
                        <span className="ml-2 text-[10px] text-gray-400">{session.messages.length} 条</span>
                      </button>
                      <button
                        type="button"
                        onClick={() => handleDeleteSession(session.id)}
                        className="hidden cursor-pointer rounded p-1 text-gray-400 hover:text-red-500 group-hover:block"
                        title="删除会话"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* 设置面板 */}
            {showSettings && (
              <ChatSettingsPanel
                config={config}
                onSaved={(next) => {
                  setConfig(next);
                  addToast("对话配置已保存", "success");
                }}
                onToast={addToast}
              />
            )}

            {/* 消息区 */}
            <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto px-4 py-3">
              {!configured ? (
                <div className="rounded-2xl border border-amber-100 bg-amber-50/80 px-3.5 py-2.5 text-[11px] leading-5 text-amber-700">
                  还没有配置对话中转站。点右上角齿轮，填「接口地址 + 模型 + 密钥」（线上中转站或本机 LM Studio），
                  就能用真实模型写提示词；配好前发送会被拦下。
                </div>
              ) : null}

              {activeSession && activeSession.messages.length === 0 && (
                <div className="mt-10 text-center">
                  <div className="mx-auto mb-3 grid h-12 w-12 place-items-center rounded-2xl bg-gradient-to-br from-indigo-100 to-violet-100 text-indigo-500">
                    <Wand2 className="h-6 w-6" />
                  </div>
                  <div className="text-sm font-black text-gray-700">描述你想要的画面</div>
                  <div className="mt-1 px-6 text-xs leading-relaxed text-gray-400">
                    中文描述、随手一句话都行，我按生图提示词的写法扩写成英文提示词，点「填回描述」直接进左侧创作描述。
                  </div>
                  <div className="mt-4 flex flex-wrap justify-center gap-2 px-4">
                    {DEMO_PROMPTS.map((demo) => (
                      <button
                        key={demo}
                        type="button"
                        onClick={() => setInput(demo)}
                        className="cursor-pointer rounded-full border border-indigo-100 bg-white px-3 py-1.5 text-xs text-gray-600 transition hover:border-indigo-300 hover:text-indigo-700"
                      >
                        {demo}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              {(activeSession?.messages || []).map((message) => (
                <ChatMessageBubble
                  key={message.id}
                  message={message}
                  copied={copiedId === message.id}
                  onCopy={() => {
                    navigator.clipboard?.writeText(message.content).catch(() => {});
                    setCopiedId(message.id);
                    window.setTimeout(() => setCopiedId(""), 1500);
                  }}
                />
              ))}

              {isStreaming && (
                <div className="flex items-center gap-2 text-xs text-gray-400">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {config?.reasoning_effort === "off" ? "生成中…" : `思考中…（思考等级 ${config?.reasoning_effort || "high"}，不限长度，可能要等一会）`}
                </div>
              )}
            </div>

            {streamError && (
              <div className="mx-4 mb-2 flex items-center gap-2 rounded-xl border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-600">
                <span className="flex-1 break-all">{streamError}</span>
                <button type="button" onClick={() => setStreamError("")} className="cursor-pointer">
                  <X className="h-3.5 w-3.5" />
                </button>
              </div>
            )}

            {/* 底部：填回描述 + 输入框 */}
            <div className="border-t border-gray-100 bg-white/80 px-4 pb-3 pt-2">
              <div className="mb-2 flex items-center justify-between">
                <button
                  type="button"
                  onClick={handleFill}
                  disabled={!canFill || isStreaming}
                  className={`flex items-center gap-1.5 rounded-full px-3 py-1.5 text-xs font-black transition ${
                    canFill && !isStreaming
                      ? "cursor-pointer bg-gradient-to-r from-[#6d5dfc] to-indigo-500 text-white shadow-sm hover:shadow-md"
                      : "cursor-not-allowed bg-gray-100 text-gray-400"
                  }`}
                  title={canFill ? "把最新一条提示词填进当前板块的创作描述" : "还没有可用的提示词"}
                >
                  <Wand2 className="h-3.5 w-3.5" /> 填回描述
                </button>
                <span className="text-[11px] font-bold text-gray-400">
                  {config?.reasoning_effort === "off" ? "不思考" : `思考等级 ${config?.reasoning_effort || "high"}`} · 不限 tokens
                </span>
              </div>
              <div className="flex items-end gap-2">
                <textarea
                  value={input}
                  onChange={(event) => setInput(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                      event.preventDefault();
                      void sendMessage(input);
                    }
                  }}
                  placeholder="描述画面，Enter 发送，Shift+Enter 换行"
                  rows={2}
                  className="max-h-32 flex-1 resize-none rounded-2xl border border-gray-200 bg-white px-3.5 py-2.5 text-sm text-gray-800 outline-none placeholder:text-gray-400 focus:border-[#6d5dfc]/50 focus:ring-2 focus:ring-[#6d5dfc]/15"
                />
                {isStreaming ? (
                  <button
                    type="button"
                    onClick={handleStop}
                    className="grid h-10 w-10 shrink-0 cursor-pointer place-items-center rounded-full bg-gray-200 text-gray-600 transition hover:bg-gray-300"
                    title="停止"
                  >
                    <RotateCcw className="h-4 w-4" />
                  </button>
                ) : (
                  <button
                    type="button"
                    onClick={() => void sendMessage(input)}
                    disabled={!input.trim()}
                    className="grid h-10 w-10 shrink-0 cursor-pointer place-items-center rounded-full bg-gradient-to-br from-[#6d5dfc] to-indigo-500 text-white transition hover:shadow-md disabled:cursor-not-allowed disabled:opacity-40"
                    title="发送"
                  >
                    <Send className="h-4 w-4" />
                  </button>
                )}
              </div>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </>
  );
}

// ── 消息气泡 ──

function ChatMessageBubble({ message, copied, onCopy }: { message: ChatMessage; copied: boolean; onCopy: () => void }) {
  const [showReasoning, setShowReasoning] = useState(false);

  if (message.role === "user") {
    return (
      <div className="flex justify-end">
        <div className="max-w-[85%] rounded-2xl rounded-br-md bg-gradient-to-br from-[#6d5dfc] to-indigo-500 px-3.5 py-2.5 text-sm leading-relaxed text-white shadow-sm">
          {message.content}
        </div>
      </div>
    );
  }

  return (
    <div className="group flex justify-start">
      <div
        className={`max-w-[92%] rounded-2xl rounded-bl-md px-3.5 py-2.5 text-sm leading-relaxed shadow-sm ${
          message.error ? "border border-red-200 bg-red-50 text-red-600" : "border border-gray-100 bg-white text-gray-800"
        }`}
      >
        {message.reasoning ? (
          <div className="mb-2">
            <button
              type="button"
              onClick={() => setShowReasoning((value) => !value)}
              className="flex cursor-pointer items-center gap-1 text-[11px] font-bold text-gray-400 transition hover:text-gray-600"
            >
              <Brain className="h-3 w-3" />
              {showReasoning ? "收起思考" : "思考过程"}
              <ChevronDown className={`h-3 w-3 transition-transform ${showReasoning ? "rotate-180" : ""}`} />
            </button>
            {showReasoning && (
              <div className="mt-1.5 max-h-40 overflow-y-auto whitespace-pre-wrap rounded-xl bg-gray-50 px-2.5 py-2 text-[11px] leading-relaxed text-gray-500">
                {message.reasoning}
              </div>
            )}
          </div>
        ) : null}
        <div className="whitespace-pre-wrap break-words">{message.content || "…"}</div>
        {message.content && !message.error ? (
          <div className="mt-1.5 flex justify-end opacity-0 transition group-hover:opacity-100">
            <button
              type="button"
              onClick={onCopy}
              className="flex cursor-pointer items-center gap-1 rounded-md px-1.5 py-0.5 text-[10px] text-gray-400 hover:bg-gray-100 hover:text-gray-600"
            >
              {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
              {copied ? "已复制" : "复制"}
            </button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

// ── 设置面板（工作台里配置线上中转站）──

function ChatSettingsPanel({
  config,
  onSaved,
  onToast,
}: {
  config: ChatProviderConfig | null;
  onSaved: (config: ChatProviderConfig) => void;
  onToast: (message: string, type?: "success" | "error" | "info") => void;
}) {
  const [baseUrl, setBaseUrl] = useState(config?.base_url || "");
  const [apiKey, setApiKey] = useState("");
  const [model, setModel] = useState(config?.model || "");
  const [effort, setEffort] = useState<ChatReasoningEffort>(config?.reasoning_effort || "high");
  const [temperature, setTemperature] = useState(config?.temperature ?? 0.9);
  const [customInstruction, setCustomInstruction] = useState(config?.custom_instruction || "");
  const [models, setModels] = useState<string[]>([]);
  const [isTesting, setIsTesting] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; message: string } | null>(null);
  const hydratedRef = useRef(false);

  // 配置从服务端回来（首次打开面板时）后同步进表单；之后不再覆盖用户的编辑。
  useEffect(() => {
    if (hydratedRef.current || !config) return;
    hydratedRef.current = true;
    setBaseUrl(config.base_url || "");
    setModel(config.model || "");
    setEffort(config.reasoning_effort || "high");
    setTemperature(config.temperature ?? 0.9);
    setCustomInstruction(config.custom_instruction || "");
  }, [config]);

  const handleTest = async () => {
    setIsTesting(true);
    setTestResult(null);
    try {
      const list = await fetchChatRelayModels({ base_url: baseUrl, api_key: apiKey });
      setModels(list);
      setTestResult({ ok: true, message: list.length ? `连接成功，${list.length} 个模型` : "连接成功，但没有返回模型" });
      if (!model && list.length) setModel(list[0]);
    } catch (error: any) {
      setTestResult({ ok: false, message: error?.message || "连接失败" });
    } finally {
      setIsTesting(false);
    }
  };

  const handleSave = async () => {
    setIsSaving(true);
    try {
      const next = await saveChatConfig({
        base_url: baseUrl,
        model,
        reasoning_effort: effort,
        temperature,
        custom_instruction: customInstruction,
        ...(apiKey.trim() ? { api_key: apiKey.trim() } : {}),
      });
      setApiKey("");
      onSaved(next);
    } catch (error: any) {
      onToast(error?.message || "保存失败", "error");
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <div className="max-h-[70%] space-y-3 overflow-y-auto border-b border-gray-100 bg-white/70 px-4 py-3">
      <div>
        <div className="mb-1.5 flex items-center justify-between text-[11px] font-black text-gray-400">
          <span>预设端点</span>
          <span className="font-bold normal-case text-gray-300">{config?.source === "env" ? "当前来自 .env.local" : ""}</span>
        </div>
        <div className="grid grid-cols-2 gap-2">
          {ENDPOINT_PRESETS.map((preset) => (
            <button
              key={preset.label}
              type="button"
              onClick={() => setBaseUrl(preset.baseURL)}
              className={`cursor-pointer rounded-xl border px-3 py-2 text-left text-xs font-bold transition ${
                baseUrl === preset.baseURL
                  ? "border-[#6d5dfc]/50 bg-indigo-50/80 text-indigo-800"
                  : "border-gray-200 bg-white text-gray-600 hover:border-gray-300"
              }`}
            >
              {preset.label}
              <span className="mt-0.5 block truncate text-[10px] font-bold text-gray-400">{preset.baseURL}</span>
            </button>
          ))}
        </div>
      </div>

      <div className="space-y-2 rounded-2xl border border-gray-100 bg-white p-3">
        <div>
          <label className="mb-1 block text-[11px] font-black text-gray-400">接口地址</label>
          <input
            value={baseUrl}
            onChange={(event) => setBaseUrl(event.target.value)}
            placeholder="http://172.0.0.1:3000"
            className="w-full rounded-xl border border-gray-200 px-2.5 py-1.5 text-xs text-gray-700 outline-none focus:border-[#6d5dfc]/50"
          />
        </div>
        <div>
          <label className="mb-1 block text-[11px] font-black text-gray-400">密钥（只存本机 local-data，不显示原文）</label>
          <input
            type="password"
            value={apiKey}
            onChange={(event) => setApiKey(event.target.value)}
            placeholder={config?.has_key ? `已保存 ${config.key_hint}；留空表示不改动` : "sk-…（本机 LM Studio 可以留空）"}
            className="w-full rounded-xl border border-gray-200 px-2.5 py-1.5 text-xs text-gray-700 outline-none focus:border-[#6d5dfc]/50"
          />
        </div>
        <div>
          <div className="mb-1 flex items-center justify-between">
            <label className="text-[11px] font-black text-gray-400">模型</label>
            <button
              type="button"
              onClick={() => void handleTest()}
              disabled={isTesting || !baseUrl.trim()}
              className="flex cursor-pointer items-center gap-1 text-[11px] font-black text-[#6d5dfc] hover:text-indigo-700 disabled:opacity-50"
            >
              {isTesting ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
              获取模型列表 / 测试连通
            </button>
          </div>
          {models.length ? (
            <select
              value={model}
              onChange={(event) => setModel(event.target.value)}
              className="w-full cursor-pointer rounded-xl border border-gray-200 bg-white px-2.5 py-1.5 text-xs text-gray-700 outline-none focus:border-[#6d5dfc]/50"
            >
              {!models.includes(model) && model ? <option value={model}>{model}</option> : null}
              {models.map((item) => (
                <option key={item} value={item}>
                  {item}
                </option>
              ))}
            </select>
          ) : (
            <input
              value={model}
              onChange={(event) => setModel(event.target.value)}
              placeholder="如 deepseek-v4.1-flash"
              className="w-full rounded-xl border border-gray-200 px-2.5 py-1.5 text-xs text-gray-700 outline-none focus:border-[#6d5dfc]/50"
            />
          )}
          {testResult ? (
            <div className={`mt-1 text-[10px] font-bold ${testResult.ok ? "text-emerald-600" : "text-red-500"}`}>
              {testResult.message}
            </div>
          ) : null}
        </div>
      </div>

      <div>
        <div className="mb-1 flex items-center justify-between text-[11px] font-black text-gray-400">
          <span>思考等级（reasoning_effort）</span>
          <span className="font-bold text-gray-300">不发送 max_tokens，思考不限长</span>
        </div>
        <div className="grid grid-cols-4 gap-2">
          {EFFORT_OPTIONS.map((option) => (
            <button
              key={option.value}
              type="button"
              onClick={() => setEffort(option.value)}
              title={option.note}
              className={`h-9 cursor-pointer rounded-xl text-xs font-black transition ${
                effort === option.value ? "bg-black text-white shadow-lg shadow-black/10" : "bg-gray-50 text-gray-500 hover:bg-gray-100 hover:text-black"
              }`}
            >
              {option.label}
            </button>
          ))}
        </div>
      </div>

      <div>
        <div className="mb-1 flex items-center justify-between text-[11px] font-black text-gray-400">
          <span>随机性（temperature）</span>
          <span className="font-mono font-bold text-gray-400">{temperature.toFixed(1)}</span>
        </div>
        <input
          type="range"
          min={0}
          max={2}
          step={0.1}
          value={temperature}
          onChange={(event) => setTemperature(Number(event.target.value))}
          className="w-full cursor-pointer accent-[#6d5dfc]"
        />
      </div>

      <div>
        <label className="mb-1 block text-[11px] font-black text-gray-400">个人偏好（附加到系统提示词，可选）</label>
        <textarea
          value={customInstruction}
          onChange={(event) => setCustomInstruction(event.target.value)}
          placeholder="例：默认带电影感构图；常拍 9:16 竖版；提示词末尾固定加 film grain"
          rows={2}
          className="w-full resize-none rounded-xl border border-gray-200 px-2.5 py-1.5 text-xs text-gray-700 outline-none focus:border-[#6d5dfc]/50"
        />
      </div>

      <button
        type="button"
        onClick={() => void handleSave()}
        disabled={isSaving || !baseUrl.trim() || !model.trim()}
        className="flex h-10 w-full cursor-pointer items-center justify-center gap-2 rounded-2xl bg-[#6d5dfc] text-xs font-black text-white shadow-lg shadow-[#6d5dfc]/20 transition hover:-translate-y-0.5 hover:bg-[#5b4cf2] disabled:cursor-not-allowed disabled:opacity-60"
      >
        {isSaving ? <Loader2 className="h-4 w-4 animate-spin" /> : null}
        保存配置
      </button>

      <div className="rounded-xl bg-gray-50 px-2.5 py-2 text-[10px] leading-relaxed text-gray-500">
        密钥与配置保存在本机 <code className="rounded bg-white px-1">local-data/store.json</code>（接口只回掩码，不进浏览器存储）。
        系统提示词按生图提示词工程师写：要什么写什么、直接出英文提示词，硬线是人物必须成年。
        云端中转站出词更稳的建议：露骨题材走本机 LM Studio 端点。
      </div>
    </div>
  );
}
