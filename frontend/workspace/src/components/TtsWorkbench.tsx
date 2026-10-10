import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  AudioLines,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  CircleStop,
  Download,
  Eraser,
  Library,
  Loader2,
  Mic,
  RefreshCcw,
  Settings2,
  Sparkles,
  TriangleAlert,
  Upload,
  UserRound,
  Wand2,
  X,
} from "lucide-react";
import {
  AuthRequiredError,
  cancelTtsTask,
  cancelUploadSession,
  createTtsTask,
  fetchTtsStatus,
  fetchTtsTaskDetail,
  releaseTtsMemory,
  uploadFileResumable,
  type ChatModel,
  type TtsOutput,
  type TtsStatus,
  type TtsTask,
  type TtsVoice,
  type UploadProgress,
} from "../services/apiService";
import TtsSettingsModal from "./TtsSettingsModal";

// 「TTS 语音合成」板块：仅本地开发者模式（npm run local）出现，任务交给 E:\AI\TTS 的 CLI 契约执行。
// 四种模式（音色设计 / 音色复用 / 声音克隆 / 预设音色）+ 音色库 + 引擎实例管理；
// 页面骨架沿用 ComfyWorkbench（左栏表单 + 右侧结果区 + 轮询/恢复）。

interface TtsWorkbenchProps {
  isAuthenticated: boolean;
  selectedModel: string;
  chatModels: ChatModel[];
  onOpenAuth: (mode: "login" | "register") => void;
  addToast: (message: string, type?: "success" | "error" | "info") => void;
  selectedTaskNo?: string;
  resetSignal?: number;
  onGenerationSubmitted?: () => void;
}

type TtsMode = "design" | "say" | "clone" | "preset";

const ACTIVE_TASK_KEY = "mirror.activeTtsTaskNo";
const runningStatuses = ["queued", "running"];
const terminalStatuses = ["succeeded", "failed", "canceled"];
const ACTIVE_TASK_RETENTION_MS = 30 * 60 * 1000;
const POST_SUCCESS_GRACE_MS = 15 * 1000;
// 与 GenerationWorkbench / shared/hooks/useTaskPolling 保持同一护栏语义（台账 #3）。
const MAX_POLL_ATTEMPTS = 900;
const MAX_POLL_ERROR_COUNT = 3;
const MAX_POLL_LIFETIME_MS = 35 * 60 * 1000;
const MAX_REF_AUDIO_BYTES = 30 * 1024 * 1024;
const MAX_TEXT_LENGTH = 5000;
const MAX_TAKES = 5;
const REF_AUDIO_EXTENSIONS = /\.(wav|mp3|flac|m4a|ogg|opus|aac)$/i;
// 连续合成次数达到该值时，弹窗询问是否让模型常驻内存（省掉每次 8~70 秒加载）。
const RESIDENT_PROMPT_AFTER = 3;
const RESIDENT_PROMPT_KEY = "mirror.tts.residentPromptDismissed";

const MODE_TABS: Array<{ value: TtsMode; label: string; note: string }> = [
  { value: "design", label: "音色设计", note: "文字描述造音色，可存档复用" },
  { value: "say", label: "音色复用", note: "用音色库里的音色朗读" },
  { value: "clone", label: "声音克隆", note: "参考音频复刻，四引擎可选" },
  { value: "preset", label: "预设音色", note: "官方 / 社区现成音色" },
];

const LANGUAGE_OPTIONS = [
  { value: "auto", label: "自动判断" },
  { value: "zh", label: "中文" },
  { value: "en", label: "英语" },
  { value: "ja", label: "日语" },
  { value: "ko", label: "韩语" },
];

const QWEN_PRESET_SPEAKERS = [
  { value: "serena", label: "Serena · 女声" },
  { value: "vivian", label: "Vivian · 女声" },
  { value: "uncle_fu", label: "Uncle Fu · 男声" },
  { value: "ryan", label: "Ryan · 男声" },
  { value: "aiden", label: "Aiden · 男声" },
  { value: "ono_anna", label: "Ono Anna · 女声" },
  { value: "sohee", label: "Sohee · 女声" },
  { value: "eric", label: "Eric · 四川话" },
  { value: "dylan", label: "Dylan · 北京话" },
];

const CLONE_ENGINE_NOTES: Record<string, string> = {
  "qwen-base": "3 秒克隆；提供参考文本时走 ICL 模式（推荐），质量更好",
  gptsovits: "音色生态最丰富；参考音频硬性 3~10 秒，参考文本必填",
  indextts: "情感控制最强；参考音频自动截到 15 秒",
  megatts3: "火山同源技术；参考音频 ≤28 秒，不支持指定语种",
};

function parseTimestamp(value?: string | null): number {
  if (!value) return 0;
  const timestamp = new Date(value).getTime();
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

function isTaskStillActive(detail: TtsTask): boolean {
  if (runningStatuses.includes(detail.status)) {
    const reference = parseTimestamp(detail.started_at) || parseTimestamp(detail.submitted_at) || Date.now();
    return Date.now() - reference <= ACTIVE_TASK_RETENTION_MS;
  }
  if (detail.status === "succeeded" && !detail.outputs?.length) {
    const finishedAt = parseTimestamp(detail.finished_at);
    return Date.now() - (finishedAt || parseTimestamp(detail.submitted_at) || Date.now()) <= POST_SUCCESS_GRACE_MS;
  }
  return false;
}

function clampProgress(value: number): number {
  return Math.max(0, Math.min(Math.round(value || 0), 100));
}

function formatElapsed(seconds: number): string {
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${String(seconds % 60).padStart(2, "0")} 秒`;
}

function formatSeconds(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "--";
  return `${Number(value).toFixed(value < 10 ? 2 : 1)}s`;
}

/** 目标模式需要哪个引擎实例；用于提交前的槽位切换确认。 */
function engineForMode(mode: TtsMode, cloneEngine: string, presetEngine: string): string {
  if (mode === "design") return "qwen-design";
  if (mode === "say") return "qwen-base";
  if (mode === "clone") return cloneEngine;
  return presetEngine;
}

/** 提交时该模式支持哪些高级参数（引擎白名单的常用子集，其余参数 CLI 侧按契约校验）。 */
function advancedDefaults(mode: TtsMode): Record<string, string | number | boolean> {
  if (mode === "design") return { trim_lead: false };
  if (mode === "say") return { trim_lead: false };
  return {};
}

export default function TtsWorkbench({
  isAuthenticated,
  onOpenAuth,
  addToast,
  selectedTaskNo = "",
  resetSignal = 0,
  onGenerationSubmitted,
}: TtsWorkbenchProps) {
  const referenceInputRef = useRef<HTMLInputElement | null>(null);
  const [mode, setMode] = useState<TtsMode>("design");

  // 表单状态
  const [text, setText] = useState("");
  const [instruct, setInstruct] = useState("");
  const [language, setLanguage] = useState("auto");
  const [takes, setTakes] = useState(1);
  const [saveAs, setSaveAs] = useState("");
  const [voice, setVoice] = useState("");
  const [cloneEngine, setCloneEngine] = useState("indextts");
  const [presetEngine, setPresetEngine] = useState("qwen-preset");
  const [speaker, setSpeaker] = useState("vivian");
  const [refText, setRefText] = useState("");
  const [showAdvanced, setShowAdvanced] = useState(false);

  // 高级参数（按引擎白名单展示常用键）
  const [advTemperature, setAdvTemperature] = useState("");
  const [advTrimLead, setAdvTrimLead] = useState(false);
  const [advSpeed, setAdvSpeed] = useState("");
  const [advSeed, setAdvSeed] = useState("");
  const [advEmoText, setAdvEmoText] = useState("");
  const [advEmoVector, setAdvEmoVector] = useState<number[]>([]);
  const [advPw, setAdvPw] = useState("");
  const [advTw, setAdvTw] = useState("");

  // 参考音频上传
  const [refAudioName, setRefAudioName] = useState("");
  const [refUploadToken, setRefUploadToken] = useState("");
  const [refUploadFile, setRefUploadFile] = useState<File | null>(null);
  const [refUploadProgress, setRefUploadProgress] = useState<UploadProgress | null>(null);
  const [isUploadingRef, setIsUploadingRef] = useState(false);

  // 状态与任务
  const [ttsStatus, setTtsStatus] = useState<TtsStatus | null>(null);
  const [isCheckingStatus, setIsCheckingStatus] = useState(false);
  const [showSettings, setShowSettings] = useState(false);
  const [showVoiceLibrary, setShowVoiceLibrary] = useState(false);
  const [confirmSwitch, setConfirmSwitch] = useState<{ targetEngine: string; currentEngine: string; payload: Record<string, any> } | null>(null);

  // 当前任务
  const [taskNo, setTaskNo] = useState("");
  const [taskStatus, setTaskStatus] = useState("");
  const [taskEngine, setTaskEngine] = useState("");
  const [progress, setProgress] = useState(0);
  const [outputs, setOutputs] = useState<TtsOutput[]>([]);
  const [taskWarnings, setTaskWarnings] = useState<string[]>([]);
  const [taskError, setTaskError] = useState("");
  const [taskTiming, setTaskTiming] = useState<{ handle_s?: number } | null>(null);
  const [taskLoadS, setTaskLoadS] = useState<number | null>(null);
  const [isGenerating, setIsGenerating] = useState(false);
  const [isRestoringTask, setIsRestoringTask] = useState(false);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [isReleasing, setIsReleasing] = useState(false);
  const [showResidentPrompt, setShowResidentPrompt] = useState(false);
  const completedTaskCountRef = useRef(0);

  const didMountRef = useRef(false);
  const pollingTimerRef = useRef<number | null>(null);
  const pollingAttemptRef = useRef(0);
  const pollingErrorCountRef = useRef(0);
  const pollingStartedAtRef = useRef(0);
  const completionToastTaskRef = useRef("");
  const taskSessionRef = useRef(0);
  const refUploadVersionRef = useRef(0);
  const refUploadAbortRef = useRef<AbortController | null>(null);
  const activeRefUploadIdRef = useRef("");
  const refUploadPromiseRef = useRef<Promise<string | null> | null>(null);
  const addToastRef = useRef(addToast);
  const onGenerationSubmittedRef = useRef(onGenerationSubmitted);

  useEffect(() => {
    addToastRef.current = addToast;
  }, [addToast]);

  useEffect(() => {
    onGenerationSubmittedRef.current = onGenerationSubmitted;
  }, [onGenerationSubmitted]);

  const beginTaskSession = () => {
    taskSessionRef.current += 1;
    return taskSessionRef.current;
  };

  const isCurrentTaskSession = (sessionId: number) => taskSessionRef.current === sessionId;

  const stopPolling = () => {
    if (pollingTimerRef.current) {
      window.clearTimeout(pollingTimerRef.current);
      pollingTimerRef.current = null;
    }
  };

  const refreshTtsStatus = async (): Promise<TtsStatus | null> => {
    setIsCheckingStatus(true);
    try {
      const status = await fetchTtsStatus();
      setTtsStatus(status);
      return status;
    } catch {
      setTtsStatus(null);
      return null;
    } finally {
      setIsCheckingStatus(false);
    }
  };

  useEffect(() => {
    void refreshTtsStatus();
  }, []);

  // 合成中显示已用时长（固定开销 15~40 秒，长文本更久）。
  useEffect(() => {
    if (!isGenerating || outputs.length) {
      setElapsedSeconds(0);
      return;
    }
    const startedAt = Date.now();
    const timer = window.setInterval(() => {
      setElapsedSeconds(Math.max(0, Math.round((Date.now() - startedAt) / 1000)));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [isGenerating, outputs.length, taskNo]);

  const invalidateRefUpload = () => {
    refUploadVersionRef.current += 1;
    refUploadAbortRef.current?.abort();
    refUploadAbortRef.current = null;
    if (activeRefUploadIdRef.current) {
      void cancelUploadSession(activeRefUploadIdRef.current).catch(() => {});
      activeRefUploadIdRef.current = "";
    }
    refUploadPromiseRef.current = null;
    setIsUploadingRef(false);
    setRefUploadProgress(null);
  };

  const startRefUpload = async (file: File): Promise<string | null> => {
    const uploadVersion = refUploadVersionRef.current + 1;
    refUploadVersionRef.current = uploadVersion;
    refUploadAbortRef.current?.abort();
    const abortController = new AbortController();
    refUploadAbortRef.current = abortController;
    setIsUploadingRef(true);

    const uploadPromise = uploadFileResumable("tts_ref_audio", file, {
      signal: abortController.signal,
      onSession: (session) => {
        if (refUploadVersionRef.current === uploadVersion) {
          activeRefUploadIdRef.current = session.upload_id;
        }
      },
      onProgress: (nextProgress) => {
        if (refUploadVersionRef.current === uploadVersion) {
          setRefUploadProgress(nextProgress);
        }
      },
    })
      .then(({ result }) => {
        if (refUploadVersionRef.current === uploadVersion) {
          setRefUploadToken(result.reference_upload_token);
          setRefAudioName(result.reference_audio_name || file.name);
        }
        return result.reference_upload_token;
      })
      .finally(() => {
        if (refUploadVersionRef.current === uploadVersion) {
          setIsUploadingRef(false);
          refUploadPromiseRef.current = null;
          refUploadAbortRef.current = null;
          activeRefUploadIdRef.current = "";
        }
      });

    refUploadPromiseRef.current = uploadPromise;
    return uploadPromise;
  };

  const clearRefAudio = () => {
    invalidateRefUpload();
    setRefAudioName("");
    setRefUploadToken("");
    setRefUploadFile(null);
    if (referenceInputRef.current) referenceInputRef.current.value = "";
  };

  const loadRefAudioFile = async (file: File) => {
    if (!REF_AUDIO_EXTENSIONS.test(file.name)) {
      addToast("参考音频只支持 wav / mp3 / flac / m4a / ogg 等格式", "info");
      return;
    }
    if (file.size > MAX_REF_AUDIO_BYTES) {
      addToast("参考音频不能超过 30MB", "error");
      return;
    }
    invalidateRefUpload();
    setRefAudioName(file.name);
    setRefUploadToken("");
    setRefUploadFile(file);
    if (!isAuthenticated) {
      addToast("参考音频已就绪，开始合成时会自动上传");
      return;
    }
    try {
      await startRefUpload(file);
      addToast("参考音频已就绪");
    } catch (error: any) {
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
        return;
      }
      addToast(error.message || "参考音频已就绪，开始合成时会继续处理", "info");
    }
  };

  const handleRefAudioChange = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    void loadRefAudioFile(file);
  };

  const resetWorkbench = () => {
    beginTaskSession();
    stopPolling();
    invalidateRefUpload();
    pollingAttemptRef.current = 0;
    pollingErrorCountRef.current = 0;
    pollingStartedAtRef.current = 0;
    completionToastTaskRef.current = "";
    setText("");
    setInstruct("");
    setTakes(1);
    setSaveAs("");
    setRefText("");
    setAdvEmoVector([]);
    setAdvEmoText("");
    setAdvTemperature("");
    setAdvSpeed("");
    setAdvSeed("");
    setAdvPw("");
    setAdvTw("");
    setAdvTrimLead(false);
    setTaskNo("");
    setTaskStatus("");
    setProgress(0);
    setOutputs([]);
    setTaskWarnings([]);
    setTaskError("");
    setTaskTiming(null);
    setTaskLoadS(null);
    setIsGenerating(false);
    setIsRestoringTask(false);
    window.localStorage.removeItem(ACTIVE_TASK_KEY);
  };

  const applyTaskDetail = async (detail: TtsTask, sessionId?: number) => {
    if (sessionId && !isCurrentTaskSession(sessionId)) return false;
    setTaskNo(detail.task_no);
    setTaskStatus(detail.status);
    setTaskEngine(detail.engine || "");
    setProgress(clampProgress(detail.progress_percent));
    setTaskWarnings(detail.warnings || []);
    setTaskError(detail.error_message || "");
    setTaskTiming(detail.timing && Object.keys(detail.timing).length ? detail.timing : null);
    setTaskLoadS(detail.load_s ?? null);

    if (runningStatuses.includes(detail.status) || (detail.status === "succeeded" && !detail.outputs?.length)) {
      window.localStorage.setItem(ACTIVE_TASK_KEY, detail.task_no);
    } else {
      window.localStorage.removeItem(ACTIVE_TASK_KEY);
    }

    if (detail.outputs?.length) {
      setIsGenerating(false);
      setOutputs(detail.outputs);
      setProgress(100);
      if (completionToastTaskRef.current !== detail.task_no) {
        completionToastTaskRef.current = detail.task_no;
        addToast("音频已生成");
        onGenerationSubmitted?.();
      }
      window.localStorage.removeItem(ACTIVE_TASK_KEY);
      // 连续合成多次后，若实例仍是「空闲自动退出」模式且用户没关闭过该提示，询问是否常驻。
      completedTaskCountRef.current += 1;
      const dismissed = window.localStorage.getItem(RESIDENT_PROMPT_KEY) === "1";
      const instanceIdleTimeout = ttsStatus?.instance?.idle_timeout_s ?? null;
      if (
        completedTaskCountRef.current >= RESIDENT_PROMPT_AFTER &&
        !dismissed &&
        instanceIdleTimeout !== 0 &&
        ttsStatus?.instance
      ) {
        setShowResidentPrompt(true);
      }
    } else if (detail.status === "failed" || detail.status === "canceled") {
      setIsGenerating(false);
      setOutputs([]);
      window.localStorage.removeItem(ACTIVE_TASK_KEY);
    } else {
      setOutputs([]);
    }
    return detail.outputs?.length > 0;
  };

  // 挂载后恢复未完成任务 / 打开历史任务。
  useEffect(() => {
    if (!isAuthenticated) return;
    if (selectedTaskNo) return;
    const savedTaskNo = window.localStorage.getItem(ACTIVE_TASK_KEY);
    if (!savedTaskNo) return;

    let ignore = false;
    const sessionId = beginTaskSession();
    setIsRestoringTask(true);
    fetchTtsTaskDetail(savedTaskNo)
      .then(async (detail) => {
        if (ignore || !isCurrentTaskSession(sessionId)) return;
        if (!isTaskStillActive(detail)) {
          window.localStorage.removeItem(ACTIVE_TASK_KEY);
          return;
        }
        setText(detail.text || "");
        setMode(detail.mode);
        await applyTaskDetail(detail, sessionId);
      })
      .catch(() => {
        if (!ignore && isCurrentTaskSession(sessionId)) {
          window.localStorage.removeItem(ACTIVE_TASK_KEY);
        }
      })
      .finally(() => {
        if (!ignore && isCurrentTaskSession(sessionId)) setIsRestoringTask(false);
      });
    return () => {
      ignore = true;
    };
  }, [isAuthenticated, selectedTaskNo]);

  useEffect(() => {
    if (!didMountRef.current) {
      didMountRef.current = true;
      return;
    }
    resetWorkbench();
  }, [resetSignal]);

  useEffect(() => {
    if (!isAuthenticated || !selectedTaskNo) return;
    // App 的 openHistoryTask 已把 "tts:" 前缀切掉；这里兼容带/不带前缀两种形态。
    const realTaskNo = selectedTaskNo.startsWith("tts:") ? selectedTaskNo.slice(4) : selectedTaskNo;
    let ignore = false;
    const sessionId = beginTaskSession();
    setIsRestoringTask(true);
    fetchTtsTaskDetail(realTaskNo)
      .then(async (detail) => {
        if (ignore || !isCurrentTaskSession(sessionId)) return;
        setMode(detail.mode);
        setText(detail.text || "");
        await applyTaskDetail(detail, sessionId);
      })
      .catch((error: any) => {
        if (!ignore && isCurrentTaskSession(sessionId)) {
          addToast(error.message || "历史任务打开失败", "error");
        }
      })
      .finally(() => {
        if (!ignore && isCurrentTaskSession(sessionId)) setIsRestoringTask(false);
      });
    return () => {
      ignore = true;
    };
  }, [selectedTaskNo, isAuthenticated]);

  useEffect(() => {
    return () => {
      stopPolling();
      refUploadAbortRef.current?.abort();
    };
  }, []);

  // 轮询任务状态（合成固定开销 15~40 秒，长文本更久；寿命放宽到 35 分钟）。
  useEffect(() => {
    stopPolling();
    if (!isAuthenticated || !taskNo || outputs.length) return;
    if (!runningStatuses.includes(taskStatus) && taskStatus !== "succeeded") return;

    let cancelled = false;
    pollingStartedAtRef.current = Date.now();
    pollingErrorCountRef.current = 0;

    const stopWithNotice = (message?: string) => {
      stopPolling();
      window.localStorage.removeItem(ACTIVE_TASK_KEY);
      setIsGenerating(false);
      if (message) addToastRef.current(message, "info");
    };

    const poll = async () => {
      if (cancelled) return;
      pollingAttemptRef.current += 1;
      if (
        pollingAttemptRef.current > MAX_POLL_ATTEMPTS ||
        Date.now() - pollingStartedAtRef.current > MAX_POLL_LIFETIME_MS
      ) {
        stopWithNotice("任务状态同步已停止，请到历史记录查看结果");
        return;
      }
      try {
        const detail = await fetchTtsTaskDetail(taskNo);
        if (cancelled) return;
        pollingErrorCountRef.current = 0;
        const hasOutput = await applyTaskDetail(detail, taskSessionRef.current);
        const keepPolling = !hasOutput && isTaskStillActive(detail);
        if (keepPolling) {
          const delay = pollingAttemptRef.current < 4 ? 1500 : 2500;
          pollingTimerRef.current = window.setTimeout(poll, delay);
        }
      } catch {
        if (cancelled) return;
        pollingErrorCountRef.current += 1;
        if (pollingErrorCountRef.current >= MAX_POLL_ERROR_COUNT) {
          stopWithNotice("任务状态同步暂时不可用，请稍后到历史记录查看");
          return;
        }
        pollingTimerRef.current = window.setTimeout(poll, 2000 + pollingErrorCountRef.current * 1500);
      }
    };

    pollingAttemptRef.current = 0;
    pollingTimerRef.current = window.setTimeout(poll, 1200);

    return () => {
      cancelled = true;
      stopPolling();
    };
  }, [isAuthenticated, taskNo, taskStatus, outputs.length]);

  const handleCancelTask = async () => {
    if (!taskNo) return;
    try {
      await cancelTtsTask(taskNo);
      addToast("已请求取消任务", "info");
    } catch (error: any) {
      addToast(error.message || "取消失败", "error");
    }
  };

  /** 顶部「释放内存」：停掉 TTS 常驻实例（有任务在跑时后端会拒绝）。 */
  const handleReleaseMemory = async () => {
    if (isWorking) {
      addToast("有任务正在使用显卡，完成后再释放", "info");
      return;
    }
    setIsReleasing(true);
    try {
      const result = await releaseTtsMemory();
      addToast(result.reason, result.released ? "success" : "info");
      await refreshTtsStatus();
    } catch (error: any) {
      addToast(error.message || "释放失败", "error");
    } finally {
      setIsReleasing(false);
    }
  };

  /** 常驻弹窗的选择：立即重启当前引擎为常驻模式（idle_timeout=0），或保持现状。 */
  const handleKeepResident = async () => {
    setShowResidentPrompt(false);
    const engine = ttsStatus?.instance?.engine;
    if (!engine) return;
    try {
      await (await import("../services/apiService")).startTtsEngine({ engine, force: true, keep_resident: true });
      addToast(`已切换为常驻模式：${engine} 不会自动退出；用完记得「释放内存」`, "success");
      await refreshTtsStatus();
    } catch (error: any) {
      addToast(error.message || "切换常驻模式失败", "error");
    }
  };

  const handleResidentPromptDismiss = (remember = false) => {
    setShowResidentPrompt(false);
    if (remember) {
      window.localStorage.setItem(RESIDENT_PROMPT_KEY, "1");
    }
  };

  const buildParams = (): Record<string, string | number | boolean | number[]> => {
    const params: Record<string, string | number | boolean | number[]> = {
      ...advancedDefaults(mode),
    };
    const targetEngine = engineForMode(mode, cloneEngine, presetEngine);
    if (advTrimLead) params.trim_lead = true;
    if (targetEngine.startsWith("qwen") || mode === "design" || mode === "say" || (mode === "preset" && presetEngine === "qwen-preset")) {
      if (advTemperature) params.temperature = Number(advTemperature);
    }
    if (mode === "clone") {
      if (cloneEngine === "gptsovits") {
        if (advSpeed) params.speed_factor = Number(advSpeed);
        if (advSeed) params.seed = Number.parseInt(advSeed, 10) || -1;
      }
      if (cloneEngine === "indextts") {
        if (advEmoText) {
          params.use_emo_text = true;
          params.emo_text = advEmoText;
        }
        if (advEmoVector.length === 8) params.emo_vector = advEmoVector;
      }
      if (cloneEngine === "megatts3") {
        if (advPw) params.p_w = Number(advPw);
        if (advTw) params.t_w = Number(advTw);
      }
    }
    return params;
  };

  const validateForm = (): string => {
    if (!text.trim()) return "请输入要合成的文本";
    if (text.length > MAX_TEXT_LENGTH) return "合成文本过长（上限 5000 字）";
    if (mode === "design") {
      if (!instruct.trim()) return "音色设计需要填写音色描述";
      return "";
    }
    if (mode === "say") {
      if (!voice) return "请先在音色库里选择一个音色";
      return "";
    }
    if (mode === "preset") {
      if (!speaker) return "请选择一个预设音色";
      return "";
    }
    // clone：gptsovits 社区音色（--set voice=）可以免参考音频，但本表单走上传路径，统一要求上传。
    if (!refUploadToken && !refUploadFile && !refAudioName) {
      return "声音克隆需要先上传参考音频";
    }
    if (cloneEngine === "gptsovits" && !refText.trim()) {
      return "GPT-SoVITS 需要填写参考音频的原话（参考文本）";
    }
    return "";
  };

  const submitTask = async (payload: Record<string, any>, sessionId: number) => {
    let task: TtsTask;
    try {
      task = await createTtsTask(payload as any);
    } catch (error) {
      if (error instanceof AuthRequiredError) throw error;
      const message = error instanceof Error ? error.message : String(error);
      if (/槽位|E_SLOT_BUSY|另一个引擎/.test(message)) {
        setConfirmSwitch({ targetEngine: payload.engine || engineForMode(mode, cloneEngine, presetEngine), currentEngine: ttsStatus?.instance?.engine || "", payload: { ...payload, switch: true } });
        return null;
      }
      throw error;
    }
    if (!isCurrentTaskSession(sessionId)) return null;
    return task;
  };

  const handleGenerate = async (overrideSwitch = false) => {
    if (!isAuthenticated) {
      onOpenAuth("login");
      return;
    }
    const validationError = validateForm();
    if (validationError) {
      addToast(validationError, "info");
      return;
    }

    // 参考音频上传等待
    let resolvedRefToken = refUploadToken;
    if (mode === "clone" && refUploadFile && !resolvedRefToken) {
      try {
        const token = await (refUploadPromiseRef.current || startRefUpload(refUploadFile));
        if (!token) throw new Error("参考音频上传失败，请重试");
        resolvedRefToken = token;
      } catch (error: any) {
        addToast(error.message || "参考音频上传失败", "error");
        return;
      }
    }

    // 提交前置检查：目标引擎与当前实例不同时，先弹确认（切换 = 先关闭当前引擎）。
    let status = ttsStatus;
    if (!status) {
      status = await refreshTtsStatus();
    }
    if (!status?.enabled) {
      addToast("TTS CLI 不可用，请先检查 E:\\AI\\TTS 环境", "error");
      setShowSettings(true);
      return;
    }
    const targetEngine = engineForMode(mode, cloneEngine, presetEngine);
    const currentEngine = status.instance?.engine || "";
    const needSwitch = Boolean(currentEngine) && currentEngine !== targetEngine;

    const payload: Record<string, any> = {
      mode,
      text: text.trim(),
      engine: mode === "clone" ? cloneEngine : mode === "preset" ? presetEngine : undefined,
      speaker_engine: mode === "preset" ? presetEngine : undefined,
      instruct: mode === "design" || mode === "preset" ? instruct.trim() : undefined,
      voice: mode === "say" ? voice : undefined,
      speaker: mode === "preset" ? speaker : undefined,
      language,
      takes,
      save_as: mode === "design" && saveAs.trim() ? saveAs.trim() : undefined,
      ref_token: mode === "clone" ? resolvedRefToken : undefined,
      ref_text: mode === "clone" ? refText.trim() : undefined,
      ref_lang: mode === "clone" ? language : undefined,
      params: buildParams(),
      switch: overrideSwitch || (needSwitch && confirmSwitch?.targetEngine === targetEngine),
    };

    if (needSwitch && !payload.switch) {
      setConfirmSwitch({ targetEngine, currentEngine, payload });
      return;
    }

    const sessionId = beginTaskSession();
    stopPolling();
    pollingAttemptRef.current = 0;
    pollingErrorCountRef.current = 0;
    pollingStartedAtRef.current = 0;
    completionToastTaskRef.current = "";
    setConfirmSwitch(null);
    window.localStorage.removeItem(ACTIVE_TASK_KEY);
    setIsGenerating(true);
    setIsRestoringTask(false);
    setTaskNo("");
    setOutputs([]);
    setTaskWarnings([]);
    setTaskError("");
    setTaskTiming(null);
    setTaskLoadS(null);
    setTaskStatus("queued");
    setProgress(8);
    setElapsedSeconds(0);

    try {
      const task = await submitTask(payload, sessionId);
      if (!task) return;
      await applyTaskDetail(task, sessionId);
      if (isCurrentTaskSession(sessionId) && !task.outputs?.length) {
        addToast("TTS 任务已提交，正在排队执行");
        onGenerationSubmittedRef.current?.();
      }
    } catch (error: any) {
      if (!isCurrentTaskSession(sessionId)) return;
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
      } else {
        addToast(error.message || "TTS 任务提交失败", "error");
      }
      setIsGenerating(false);
      setTaskNo("");
      setTaskStatus("");
      setProgress(0);
      window.localStorage.removeItem(ACTIVE_TASK_KEY);
    }
  };

  const isWorking = isGenerating || runningStatuses.includes(taskStatus);
  const instanceEngine = ttsStatus?.instance?.engine || "";
  const cloneEngineSpec = ttsStatus?.engines.find((engine) => engine.id === cloneEngine) || null;
  const cloneRunningEngineMatches = instanceEngine === cloneEngine;
  const activeVoice = (ttsStatus?.voices || []).find((item) => item.name === voice) || null;
  const refAudioReady = Boolean(refUploadToken || refAudioName);

  return (
    <div className="bg-[#f7f7f4] lg:h-[calc(100vh-4rem)] lg:overflow-hidden">
      <div className="grid grid-cols-1 lg:h-full lg:grid-cols-[360px_minmax(0,1fr)]">
        <aside className="order-2 flex flex-col border-t border-black/5 bg-white/85 p-4 lg:order-1 lg:h-full lg:min-h-0 lg:border-r lg:border-t-0">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <div className="text-[11px] font-black uppercase tracking-[0.3em] text-gray-400">TTS Studio</div>
              <h1 className="mt-1.5 text-[1.55rem] font-black tracking-tight text-gray-950 sm:text-[1.9rem]">
                {MODE_TABS.find((tab) => tab.value === mode)?.label || "TTS 语音合成"}
              </h1>
            </div>
            <button
              type="button"
              onClick={() => setShowSettings(true)}
              className="inline-flex items-center gap-1.5 rounded-2xl border border-gray-100 bg-white px-3 py-2 text-[11px] font-bold text-gray-600 shadow-sm transition hover:border-gray-300 hover:text-black"
            >
              <Settings2 className="h-3.5 w-3.5" />
              TTS 设置
            </button>
          </div>

          {!ttsStatus?.enabled && (
            <div className="mb-3 flex items-start gap-2.5 rounded-[1.4rem] border border-amber-100 bg-amber-50 px-3.5 py-3">
              <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-500" />
              <div className="min-w-0 flex-1">
                <div className="text-[12px] font-black text-amber-800">TTS CLI 不可用</div>
                <p className="mt-0.5 text-[11px] leading-5 text-amber-700">
                  {ttsStatus?.error || "未找到 E:\\AI\\TTS 契约环境。"}
                </p>
              </div>
              <button
                type="button"
                onClick={() => void refreshTtsStatus()}
                className="inline-flex h-8 shrink-0 items-center gap-1 rounded-xl bg-white px-2.5 text-[11px] font-bold text-amber-800 shadow-sm transition hover:text-black"
                title="重新探测"
              >
                {isCheckingStatus ? <Loader2 className="h-3 w-3 animate-spin" /> : <RefreshCcw className="h-3 w-3" />}
              </button>
            </div>
          )}

          <div className="space-y-3 pb-28 pr-0 md:pb-24 lg:flex-1 lg:min-h-0 lg:overflow-y-auto lg:pb-4 lg:pr-1">
            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-3.5 shadow-sm">
              <div className="mb-2 text-sm font-black text-gray-950">合成模式</div>
              <div className="grid grid-cols-2 gap-2">
                {MODE_TABS.map((tab) => (
                  <button
                    key={tab.value}
                    type="button"
                    onClick={() => setMode(tab.value)}
                    className={`rounded-2xl border px-3 py-3 text-left transition ${
                      mode === tab.value
                        ? "border-black bg-black text-white shadow-lg shadow-black/10"
                        : "border-gray-100 bg-gray-50 text-gray-600 hover:border-gray-200 hover:bg-white"
                    }`}
                  >
                    <span className="block text-[13px] font-black">{tab.label}</span>
                    <span className={`mt-1 block text-[11px] ${mode === tab.value ? "text-white/60" : "text-gray-400"}`}>{tab.note}</span>
                  </button>
                ))}
              </div>
            </section>

            {mode === "design" && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
                <div className="mb-3 text-sm font-black text-gray-950">音色描述</div>
                <textarea
                  value={instruct}
                  onChange={(event) => setInstruct(event.target.value)}
                  className="min-h-20 w-full resize-none rounded-2xl border border-gray-100 bg-gray-50/80 p-3.5 text-sm leading-6 text-gray-800 outline-none transition focus:border-black/20 focus:bg-white focus:ring-4 focus:ring-black/5"
                  placeholder="例：慵懒的年轻女性，嗓音略微沙哑，语速偏慢，带一点磁性"
                />
                <div className="mt-3">
                  <div className="mb-2 text-xs font-black text-gray-400">存为可复用音色（可选）</div>
                  <input
                    type="text"
                    value={saveAs}
                    onChange={(event) => setSaveAs(event.target.value.replace(/[\\/:*?"<>|]/g, "").slice(0, 64))}
                    placeholder="留空只试听；填名字则存入音色库（如 lazy-girl）"
                    className="h-10 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3.5 text-sm outline-none focus:border-black/20 focus:bg-white"
                  />
                  {saveAs && (
                    <p className="mt-2 text-[11px] leading-5 text-gray-400">
                      会经历两次模型加载（qwen-design → qwen-base，约 40~60 秒），完成后音色出现在音色库。
                    </p>
                  )}
                </div>
              </section>
            )}

            {mode === "say" && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
                <div className="mb-3 flex items-center justify-between">
                  <div className="text-sm font-black text-gray-950">选择音色</div>
                  <button
                    type="button"
                    onClick={() => setShowVoiceLibrary(true)}
                    className="inline-flex h-8 items-center gap-1.5 rounded-xl border border-gray-100 px-3 text-[11px] font-bold text-gray-600 transition hover:border-gray-300 hover:text-black"
                  >
                    <Library className="h-3.5 w-3.5" />
                    音色库（{ttsStatus?.voices.length ?? 0}）
                  </button>
                </div>
                {(ttsStatus?.voices || []).length ? (
                  <div className="max-h-52 space-y-2 overflow-y-auto pr-1">
                    {ttsStatus!.voices.map((item) => (
                      <button
                        key={item.name}
                        type="button"
                        onClick={() => setVoice(item.name)}
                        className={`w-full rounded-2xl border px-3 py-2.5 text-left transition ${
                          voice === item.name
                            ? "border-black bg-black text-white shadow-lg shadow-black/10"
                            : "border-gray-100 bg-gray-50 text-gray-600 hover:border-gray-200 hover:bg-white"
                        }`}
                      >
                        <span className="block text-[13px] font-black">{item.name}</span>
                        <span className={`mt-0.5 block truncate text-[11px] ${voice === item.name ? "text-white/60" : "text-gray-400"}`}>
                          {item.instruct || item.ref_text || item.source}
                        </span>
                      </button>
                    ))}
                  </div>
                ) : (
                  <div className="rounded-2xl border border-dashed border-gray-200 bg-gray-50/70 px-3 py-4 text-center text-[11px] leading-5 text-gray-400">
                    音色库还是空的。先用「音色设计」造一个，或用 CLI 的 <code className="rounded bg-white px-1">voices import</code> 导入。
                  </div>
                )}
                {activeVoice?.audio_url && (
                  <audio key={activeVoice.audio_url} controls preload="none" src={activeVoice.audio_url} className="mt-3 h-9 w-full" />
                )}
              </section>
            )}

            {mode === "clone" && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
                <div className="mb-3 text-sm font-black text-gray-950">克隆引擎</div>
                <div className="space-y-2">
                  {["qwen-base", "gptsovits", "indextts", "megatts3"].map((engineId) => {
                    const spec = ttsStatus?.engines.find((engine) => engine.id === engineId);
                    return (
                      <button
                        key={engineId}
                        type="button"
                        onClick={() => setCloneEngine(engineId)}
                        className={`w-full rounded-2xl border px-3 py-2.5 text-left transition ${
                          cloneEngine === engineId
                            ? "border-black bg-black text-white shadow-lg shadow-black/10"
                            : "border-gray-100 bg-gray-50 text-gray-600 hover:border-gray-200 hover:bg-white"
                        }`}
                      >
                        <span className="flex items-center justify-between text-[13px] font-black">
                          {spec?.label || engineId}
                          {instanceEngine === engineId && (
                            <span className={`rounded-full px-2 py-0.5 text-[10px] font-black ${cloneEngine === engineId ? "bg-white/15 text-white/80" : "bg-emerald-50 text-emerald-600"}`}>
                              已驻留
                            </span>
                          )}
                        </span>
                        <span className={`mt-1 block text-[11px] leading-4 ${cloneEngine === engineId ? "text-white/60" : "text-gray-400"}`}>
                          {CLONE_ENGINE_NOTES[engineId]}
                        </span>
                      </button>
                    );
                  })}
                </div>
              </section>
            )}

            {mode === "clone" && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
                <div className="mb-3 flex items-center justify-between">
                  <div>
                    <div className="text-sm font-black text-gray-950">参考音频</div>
                    <div className="mt-1 text-xs text-gray-400">
                      {cloneEngineSpec?.ref_audio?.max_s
                        ? `不超过 ${cloneEngineSpec.ref_audio.max_s} 秒${cloneEngineSpec.ref_audio.hard ? "（硬性）" : ""}`
                        : cloneEngineSpec?.ref_audio?.recommended || "干净的单声道人声效果最好"}
                    </div>
                  </div>
                  {refAudioReady && (
                    <button
                      type="button"
                      onClick={clearRefAudio}
                      className="inline-flex h-8 items-center gap-1 rounded-xl border border-gray-100 px-3 text-[11px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black"
                    >
                      <X className="h-3.5 w-3.5" />
                      清除
                    </button>
                  )}
                </div>
                <input
                  ref={referenceInputRef}
                  type="file"
                  accept=".wav,.mp3,.flac,.m4a,.ogg,.opus,.aac,audio/*"
                  onChange={handleRefAudioChange}
                  className="hidden"
                />
                {refAudioReady ? (
                  <div className="rounded-[1.4rem] border border-gray-100 bg-gray-50/80 p-3">
                    <div className="flex items-center gap-3">
                      <div className="grid h-12 w-12 shrink-0 place-items-center rounded-2xl bg-white shadow-sm">
                        <AudioLines className="h-5 w-5 text-gray-400" />
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-bold text-gray-900">{refAudioName || "参考音频"}</div>
                        <div className="mt-0.5 text-xs leading-5 text-gray-400">
                          {isUploadingRef
                            ? `参考音频上传中${refUploadProgress?.percent ? ` ${refUploadProgress.percent}%` : ""}…`
                            : refUploadToken
                              ? "已完整上传，提交时直接复用。"
                              : "已就绪，提交时自动上传。"}
                        </div>
                      </div>
                    </div>
                    {isUploadingRef && (
                      <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-white">
                        <div className="h-full rounded-full bg-black transition-all" style={{ width: `${Math.max(refUploadProgress?.percent || 0, 6)}%` }} />
                      </div>
                    )}
                  </div>
                ) : (
                  <button
                    type="button"
                    onClick={() => referenceInputRef.current?.click()}
                    className="flex w-full items-center gap-3 rounded-[1.4rem] border border-dashed border-gray-200 bg-gray-50/70 px-4 py-5 text-left transition hover:border-gray-300 hover:bg-white"
                  >
                    <div className="grid h-12 w-12 shrink-0 place-items-center rounded-2xl bg-white shadow-sm">
                      <Upload className="h-5 w-5 text-gray-400" />
                    </div>
                    <div className="min-w-0">
                      <div className="text-sm font-black text-gray-900">上传参考音频</div>
                      <div className="mt-1 text-xs leading-5 text-gray-400">3~10 秒干净、单声道、与参考文本一致的人声效果最好。</div>
                    </div>
                  </button>
                )}
                {cloneEngine === "gptsovits" && (ttsStatus?.gptsovits_voices.length || 0) > 0 && (
                  <div className="mt-3">
                    <div className="mb-2 text-xs font-black text-gray-400">或直接用社区音色（免上传参考音频）</div>
                    <div className="flex flex-wrap gap-2">
                      {ttsStatus!.gptsovits_voices.map((name) => (
                        <button
                          key={name}
                          type="button"
                          onClick={() => {
                            setPresetEngine("gptsovits");
                            setMode("preset");
                            setSpeaker(name);
                          }}
                          className="rounded-xl border border-gray-100 bg-gray-50 px-3 py-1.5 text-[11px] font-bold text-gray-600 transition hover:border-gray-300 hover:text-black"
                        >
                          {name}
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </section>
            )}

            {mode === "clone" && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
                <div className="mb-2 text-sm font-black text-gray-950">
                  参考文本{cloneEngine === "gptsovits" ? "（必填）" : cloneEngine === "qwen-base" ? "（推荐填写）" : "（可忽略）"}
                </div>
                <textarea
                  value={refText}
                  onChange={(event) => setRefText(event.target.value)}
                  disabled={cloneEngine === "indextts" || cloneEngine === "megatts3"}
                  className="min-h-16 w-full resize-none rounded-2xl border border-gray-100 bg-gray-50/80 p-3 text-sm leading-6 text-gray-800 outline-none transition focus:border-black/20 focus:bg-white focus:ring-4 focus:ring-black/5 disabled:opacity-50"
                  placeholder="参考音频里说的原话，必须逐字一致"
                />
                {cloneEngine === "qwen-base" && !refText.trim() && (
                  <p className="mt-2 text-[11px] leading-5 text-gray-400">不给参考文本会走 x-vector 模式，音色质量略差。</p>
                )}
              </section>
            )}

            {mode === "preset" && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
                <div className="mb-3 text-sm font-black text-gray-950">音色来源</div>
                <div className="grid grid-cols-2 gap-2">
                  {[
                    { value: "qwen-preset", label: "Qwen 官方 9 音色" },
                    { value: "gptsovits", label: "GPT-SoVITS 社区音色" },
                  ].map((item) => (
                    <button
                      key={item.value}
                      type="button"
                      onClick={() => setPresetEngine(item.value)}
                      className={`rounded-2xl border px-3 py-2.5 text-left text-[12px] font-black transition ${
                        presetEngine === item.value
                          ? "border-black bg-black text-white shadow-lg shadow-black/10"
                          : "border-gray-100 bg-gray-50 text-gray-600 hover:border-gray-200 hover:bg-white"
                      }`}
                    >
                      {item.label}
                    </button>
                  ))}
                </div>
                <div className="mt-3 max-h-52 space-y-2 overflow-y-auto pr-1">
                  {(presetEngine === "qwen-preset" ? QWEN_PRESET_SPEAKERS : (ttsStatus?.gptsovits_voices || []).map((name) => ({ value: name, label: name }))).map((item) => (
                    <button
                      key={item.value}
                      type="button"
                      onClick={() => setSpeaker(item.value)}
                      className={`w-full rounded-2xl border px-3 py-2 text-left text-[13px] font-black transition ${
                        speaker === item.value
                          ? "border-black bg-black text-white shadow-lg shadow-black/10"
                          : "border-gray-100 bg-gray-50 text-gray-600 hover:border-gray-200 hover:bg-white"
                      }`}
                    >
                      {item.label}
                    </button>
                  ))}
                </div>
                {presetEngine === "qwen-preset" && (
                  <div className="mt-3">
                    <div className="mb-2 text-xs font-black text-gray-400">风格指令（可选）</div>
                    <input
                      type="text"
                      value={instruct}
                      onChange={(event) => setInstruct(event.target.value)}
                      placeholder="例：用特别愤怒的语气说"
                      className="h-10 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3.5 text-sm outline-none focus:border-black/20 focus:bg-white"
                    />
                  </div>
                )}
              </section>
            )}

            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
              <div className="mb-3 text-sm font-black text-gray-950">要合成的文本</div>
              <textarea
                value={text}
                onChange={(event) => setText(event.target.value)}
                className="min-h-28 w-full resize-none rounded-2xl border border-gray-100 bg-gray-50/80 p-4 text-sm leading-6 text-gray-800 outline-none transition focus:border-black/20 focus:bg-white focus:ring-4 focus:ring-black/5"
                placeholder="输入要让音色朗读的内容…"
              />
              <div className="mt-2 flex items-center justify-between text-[11px] text-gray-400">
                <span>{text.length} / {MAX_TEXT_LENGTH} 字</span>
                {mode === "design" && !text && (
                  <button type="button" onClick={() => setText("你好呀，我是刚刚被设计出来的声音。")} className="font-bold text-gray-500 hover:text-black">
                    填入示例
                  </button>
                )}
              </div>
            </section>

            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-3.5 shadow-sm">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <div className="mb-2 text-xs font-black text-gray-400">语言</div>
                  <select
                    value={language}
                    onChange={(event) => setLanguage(event.target.value)}
                    disabled={mode === "clone" && cloneEngine === "megatts3"}
                    className="h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-2 text-xs font-bold text-gray-800 outline-none disabled:opacity-50"
                  >
                    {LANGUAGE_OPTIONS.map((item) => (
                      <option key={item.value} value={item.value}>
                        {item.label}
                      </option>
                    ))}
                  </select>
                  {mode === "clone" && cloneEngine === "megatts3" && (
                    <p className="mt-1.5 text-[10px] leading-4 text-gray-400">MegaTTS3 中英自动判定，不支持指定语种。</p>
                  )}
                </div>
                <div>
                  <div className="mb-2 text-xs font-black text-gray-400">生成条数</div>
                  <div className="grid grid-cols-5 gap-1">
                    {Array.from({ length: MAX_TAKES }, (_, index) => index + 1).map((value) => (
                      <button
                        key={value}
                        type="button"
                        onClick={() => setTakes(value)}
                        className={`h-9 rounded-xl text-xs font-black transition ${
                          takes === value ? "bg-black text-white shadow-lg shadow-black/10" : "bg-gray-50 text-gray-500 hover:bg-gray-100 hover:text-black"
                        }`}
                      >
                        {value}
                      </button>
                    ))}
                  </div>
                  {takes > 1 && <p className="mt-1.5 text-[10px] leading-4 text-gray-400">多条并列生成，挑一条最喜欢的。</p>}
                </div>
              </div>
            </section>

            {mode === "clone" && cloneEngineSpec && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-3.5 shadow-sm">
                <button
                  type="button"
                  onClick={() => setShowAdvanced((value) => !value)}
                  className="flex w-full cursor-pointer items-center justify-between text-left text-sm font-bold text-gray-800"
                >
                  <span className="inline-flex items-center gap-2">
                    <Wand2 className="h-4 w-4 text-gray-400" />
                    高级参数（{cloneEngineSpec.label}）
                  </span>
                  {showAdvanced ? <ChevronUp className="h-4 w-4 text-gray-300" /> : <ChevronDown className="h-4 w-4 text-gray-300" />}
                </button>
                {showAdvanced && (
                  <div className="mt-3 space-y-3">
                    {(cloneEngine.startsWith("qwen") || cloneEngine === "gptsovits" || cloneEngine === "indextts") && (
                      <div>
                        <div className="mb-1.5 text-xs font-black text-gray-400">采样温度（默认走引擎值）</div>
                        <input
                          type="text"
                          inputMode="decimal"
                          value={advTemperature}
                          onChange={(event) => setAdvTemperature(event.target.value.replace(/[^\d.]/g, "").slice(0, 5))}
                          placeholder={cloneEngine === "indextts" ? "0.8" : cloneEngine === "gptsovits" ? "1.0" : "0.9"}
                          className="h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20 focus:bg-white"
                        />
                      </div>
                    )}
                    {cloneEngine === "gptsovits" && (
                      <div className="grid grid-cols-2 gap-2">
                        <div>
                          <div className="mb-1.5 text-xs font-black text-gray-400">语速（0.5~2，默认 1）</div>
                          <input
                            type="text"
                            inputMode="decimal"
                            value={advSpeed}
                            onChange={(event) => setAdvSpeed(event.target.value.replace(/[^\d.]/g, "").slice(0, 4))}
                            placeholder="1.0"
                            className="h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20 focus:bg-white"
                          />
                        </div>
                        <div>
                          <div className="mb-1.5 text-xs font-black text-gray-400">种子（-1 随机）</div>
                          <input
                            type="text"
                            inputMode="numeric"
                            value={advSeed}
                            onChange={(event) => setAdvSeed(event.target.value.replace(/^-?\d/g, "").replace(/[^\d]/g, "").slice(0, 10))}
                            placeholder="-1"
                            className="h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20 focus:bg-white"
                          />
                        </div>
                      </div>
                    )}
                    {cloneEngine === "indextts" && (
                      <div>
                        <div className="mb-1.5 text-xs font-black text-gray-400">情感向量（8 维，总和建议 ≤0.8）</div>
                        <div className="space-y-1.5">
                          {["高兴", "愤怒", "悲伤", "恐惧", "反感", "低落", "惊讶", "平静"].map((label, index) => (
                            <div key={label} className="flex items-center gap-2">
                              <span className="w-8 shrink-0 text-[10px] font-black text-gray-400">{label}</span>
                              <input
                                type="range"
                                min={0}
                                max={1}
                                step={0.05}
                                value={advEmoVector[index] ?? 0}
                                onChange={(event) => {
                                  const next = [...(advEmoVector.length === 8 ? advEmoVector : Array(8).fill(0))];
                                  next[index] = Number(event.target.value);
                                  setAdvEmoVector(next);
                                }}
                                className="h-1.5 flex-1 accent-black"
                              />
                              <span className="w-8 shrink-0 text-right text-[10px] font-bold text-gray-500">{(advEmoVector[index] ?? 0).toFixed(2)}</span>
                            </div>
                          ))}
                        </div>
                        <div className="mt-2 text-[10px] font-bold text-gray-500">
                          向量总和 {advEmoVector.length === 8 ? advEmoVector.reduce((sum, value) => sum + value, 0).toFixed(2) : "0.00"}（超过 0.8 会掉音色保真）
                        </div>
                        <input
                          type="text"
                          value={advEmoText}
                          onChange={(event) => setAdvEmoText(event.target.value.slice(0, 200))}
                          placeholder="或用文字描述情感（需实例开启 use_qwen_emo，见 TTS 设置）"
                          className="mt-2 h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3 text-xs outline-none focus:border-black/20 focus:bg-white"
                        />
                      </div>
                    )}
                    {cloneEngine === "megatts3" && (
                      <div className="grid grid-cols-2 gap-2">
                        <div>
                          <div className="mb-1.5 text-xs font-black text-gray-400">清晰度 p_w（默认 1.6）</div>
                          <input
                            type="text"
                            inputMode="decimal"
                            value={advPw}
                            onChange={(event) => setAdvPw(event.target.value.replace(/[^\d.]/g, "").slice(0, 4))}
                            placeholder="1.6"
                            className="h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20 focus:bg-white"
                          />
                        </div>
                        <div>
                          <div className="mb-1.5 text-xs font-black text-gray-400">相似度 t_w（建议 2~5）</div>
                          <input
                            type="text"
                            inputMode="decimal"
                            value={advTw}
                            onChange={(event) => setAdvTw(event.target.value.replace(/[^\d.]/g, "").slice(0, 4))}
                            placeholder="2.5"
                            className="h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20 focus:bg-white"
                          />
                        </div>
                      </div>
                    )}
                    <label className="flex cursor-pointer items-center gap-2 text-xs font-bold text-gray-600">
                      <input type="checkbox" checked={advTrimLead} onChange={(event) => setAdvTrimLead(event.target.checked)} className="h-3.5 w-3.5 accent-black" />
                      裁掉开头静音（trim_lead，对 Qwen 系设计音色常有效）
                    </label>
                  </div>
                )}
              </section>
            )}

            {taskWarnings.length > 0 && taskStatus === "succeeded" && (
              <section className="rounded-[1.6rem] border border-amber-100 bg-amber-50/70 p-3.5">
                <div className="mb-1.5 text-[12px] font-black text-amber-700">产物体检告警</div>
                <ul className="space-y-1">
                  {taskWarnings.map((warning, index) => (
                    <li key={index} className="text-[11px] leading-5 text-amber-800">⚠ {warning}</li>
                  ))}
                </ul>
                <p className="mt-1.5 text-[10px] leading-4 text-amber-600">告警不拦截产物；可多生成几条（调高「生成条数」）挑一条正常的。</p>
              </section>
            )}

            {taskError && taskStatus === "failed" && (
              <section className="rounded-[1.6rem] border border-red-100 bg-red-50/60 p-3.5">
                <div className="text-[12px] font-black text-red-700">任务失败</div>
                <p className="mt-1.5 whitespace-pre-line break-words text-[11px] leading-5 text-red-800">{taskError}</p>
              </section>
            )}
          </div>

          <div className="sticky bottom-[calc(env(safe-area-inset-bottom)+5.25rem)] z-20 mt-4 rounded-[1.6rem] border border-gray-100 bg-white/95 p-3 shadow-[0_18px_40px_rgba(15,23,42,0.08)] backdrop-blur md:bottom-4 lg:static lg:mt-4 lg:shrink-0">
            {(isWorking || progress > 0) && (
              <div className="mb-3 rounded-2xl border border-gray-100 bg-gray-50 p-3">
                <div className="mb-2 flex items-center justify-between text-[11px] font-bold text-gray-500">
                  <span>{outputs.length ? "生成完成" : isWorking ? "正在合成" : "任务进度"}</span>
                  <div className="flex items-center gap-2">
                    {isWorking && taskNo && (
                      <button type="button" onClick={() => void handleCancelTask()} className="inline-flex items-center gap-1 font-bold text-gray-400 hover:text-red-500">
                        <CircleStop className="h-3 w-3" />
                        取消
                      </button>
                    )}
                    <span>{Math.round(progress)}%</span>
                  </div>
                </div>
                <div className="h-2 overflow-hidden rounded-full bg-white">
                  <motion.div
                    className="h-full rounded-full bg-black"
                    initial={false}
                    animate={{ width: `${Math.max(0, Math.min(progress, 100))}%` }}
                    transition={{ duration: 0.35, ease: "easeOut" }}
                  />
                </div>
              </div>
            )}
            <button
              type="button"
              onClick={() => void handleGenerate()}
              disabled={isWorking || isUploadingRef || !ttsStatus?.enabled}
              className="flex h-12 w-full cursor-pointer items-center justify-center gap-3 rounded-2xl bg-[#6d5dfc] text-sm font-black text-white shadow-xl shadow-[#6d5dfc]/20 transition hover:-translate-y-0.5 hover:bg-[#5b4cf2] disabled:cursor-not-allowed disabled:opacity-70"
            >
              {isWorking ? <Loader2 className="h-5 w-5 animate-spin" /> : mode === "design" ? <Sparkles className="h-5 w-5" /> : <AudioLines className="h-5 w-5" />}
              {isWorking ? "合成中" : isUploadingRef ? "参考音频上传中" : mode === "design" ? "生成并保存音色" : "开始合成"}
              {takes > 1 && <span className="rounded-full bg-white/15 px-2 py-0.5 text-xs">{takes} 条</span>}
            </button>
          </div>
        </aside>

        <main className="order-1 p-4 pb-2 lg:order-2 lg:h-full lg:min-h-0 lg:p-4">
          <div className="relative mx-auto flex min-h-[340px] max-w-5xl flex-col lg:h-full lg:min-h-0">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-sm text-gray-500">
              <div className="text-[11px] font-bold uppercase tracking-[0.2em] text-gray-400">
                {outputs.length ? "结果已同步" : isWorking ? "TTS 合成中" : "实时预览"}
              </div>
              <div className="flex items-center gap-2">
                <span className="inline-flex items-center gap-1.5 rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-500">
                  <span className={`h-2 w-2 rounded-full ${instanceEngine ? "bg-emerald-400" : "bg-gray-300"}`} />
                  {instanceEngine ? `实例：${instanceEngine}${ttsStatus?.instance?.idle_timeout_s === 0 ? " · 常驻" : ""}` : "无运行实例（提交时自动拉起）"}
                </span>
                {ttsStatus?.gpu && (
                  <span className="inline-flex items-center gap-1.5 rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-500">
                    显存 {ttsStatus.gpu.vram_used_mb}/{ttsStatus.gpu.vram_total_mb} MB
                  </span>
                )}
                <button
                  type="button"
                  onClick={() => void handleReleaseMemory()}
                  disabled={isReleasing || isWorking || !instanceEngine}
                  className="inline-flex items-center gap-1.5 rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-500 transition hover:border-rose-200 hover:text-rose-600 disabled:cursor-not-allowed disabled:opacity-50"
                  title="停掉 TTS 常驻实例，释放显存"
                >
                  {isReleasing ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Eraser className="h-3.5 w-3.5" />}
                  释放内存
                </button>
                <button
                  type="button"
                  onClick={() => setShowVoiceLibrary(true)}
                  className="inline-flex items-center gap-2 rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-500 hover:text-black"
                >
                  <Library className="h-3.5 w-3.5" />
                  音色库
                </button>
              </div>
            </div>

            <div className="grid min-h-[300px] flex-1 place-items-center rounded-[2rem] border border-black/5 bg-white/70 p-4 shadow-sm backdrop-blur sm:min-h-[380px] lg:min-h-0 lg:overflow-y-auto">
              <div className="w-full max-w-2xl">
                {outputs.length ? (
                  <div className="space-y-3">
                    {outputs.map((output, index) => (
                      <motion.div
                        key={output.id}
                        initial={{ opacity: 0, y: 10 }}
                        animate={{ opacity: 1, y: 0 }}
                        transition={{ delay: index * 0.05 }}
                        className="rounded-[1.5rem] border border-gray-100 bg-white p-4 shadow-sm"
                      >
                        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                          <div className="flex items-center gap-2 text-sm font-black text-gray-900">
                            <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                            {outputs.length > 1 ? `第 ${index + 1} 条` : "合成结果"}
                          </div>
                          <div className="flex items-center gap-2 text-[11px] text-gray-400">
                            {output.sr ? <span className="rounded-full bg-gray-50 px-2 py-0.5 font-bold">{output.sr} Hz</span> : null}
                            {output.duration_s ? <span className="rounded-full bg-gray-50 px-2 py-0.5 font-bold">{formatSeconds(output.duration_s)}</span> : null}
                            <a href={output.download_url} className="inline-flex items-center gap-1 rounded-xl border border-gray-100 bg-white px-3 py-1.5 font-bold text-gray-600 transition hover:border-gray-200 hover:text-black">
                              <Download className="h-3 w-3" />
                              下载
                            </a>
                          </div>
                        </div>
                        <audio controls preload="metadata" src={output.preview_url} className="w-full" />
                      </motion.div>
                    ))}
                    <div className="flex flex-wrap items-center justify-between gap-2 rounded-[1.5rem] border border-gray-100 bg-white/70 px-4 py-3 text-[11px] text-gray-400">
                      <span>
                        任务 {taskNo} · 引擎 {ttsStatus?.engines.find((engine) => engine.id === taskEngine)?.label || taskEngine}
                      </span>
                      <span>
                        合成 {formatSeconds(taskTiming?.handle_s)}{taskLoadS ? ` · 本次加载 ${formatSeconds(taskLoadS)}` : ""}
                      </span>
                    </div>
                  </div>
                ) : (
                  <div className="flex min-h-[300px] flex-col items-center justify-center rounded-[1.5rem] border border-gray-100 bg-[radial-gradient(circle_at_30%_20%,#ffffff,transparent_32%),linear-gradient(135deg,#f8fafc,#eceff3)] text-center sm:min-h-[380px]">
                    {isRestoringTask ? (
                      <>
                        <Loader2 className="h-10 w-10 animate-spin text-gray-400" />
                        <p className="mt-4 text-sm font-bold text-gray-700">正在打开历史任务</p>
                      </>
                    ) : isWorking ? (
                      <>
                        <Loader2 className="h-10 w-10 animate-spin text-gray-400" />
                        <p className="mt-4 text-sm font-bold text-gray-700">TTS 引擎正在合成</p>
                        <p className="mt-1 px-4 text-xs text-gray-400">
                          任务 {taskNo || "提交中"} · {taskStatus || "queued"}
                          {elapsedSeconds > 0 ? ` · 已用 ${formatElapsed(elapsedSeconds)}` : ""}
                        </p>
                        <p className="mt-1 px-4 text-[11px] text-gray-400">首次使用某引擎需要先加载模型（8~70 秒），合成固定开销约 15~40 秒。</p>
                        <div className="mt-5 w-64 max-w-[70%]">
                          <div className="mb-2 flex items-center justify-between text-[11px] font-bold text-gray-400">
                            <span>任务进度</span>
                            <span>{Math.round(progress)}%</span>
                          </div>
                          <div className="h-2 overflow-hidden rounded-full bg-white shadow-inner">
                            <motion.div
                              className="h-full rounded-full bg-black"
                              initial={false}
                              animate={{ width: `${Math.max(8, Math.min(progress, 100))}%` }}
                              transition={{ duration: 0.35, ease: "easeOut" }}
                            />
                          </div>
                        </div>
                      </>
                    ) : (
                      <>
                        <div className="grid h-20 w-20 place-items-center rounded-[1.5rem] bg-white shadow-sm">
                          <Mic className="h-8 w-8 text-gray-300" />
                        </div>
                        <p className="mt-4 text-sm font-bold text-gray-700">结果会显示在这里</p>
                        <p className="mt-1 max-w-md px-6 text-xs leading-6 text-gray-400">
                          左侧选择模式并填写内容后开始合成；产物是 16-bit PCM WAV，可在线试听与下载。
                        </p>
                      </>
                    )}
                  </div>
                )}
              </div>
            </div>
          </div>
        </main>
      </div>

      {/* 音色库抽屉 */}
      <AnimatePresence>
        {showVoiceLibrary && (
          <div className="fixed inset-0 z-[105] flex items-center justify-center bg-black/40 p-0 backdrop-blur-sm sm:p-4" onClick={() => setShowVoiceLibrary(false)}>
            <motion.div
              initial={{ scale: 0.96, opacity: 0, y: 20 }}
              animate={{ scale: 1, opacity: 1, y: 0 }}
              exit={{ scale: 0.97, opacity: 0, y: 20 }}
              transition={{ type: "spring", damping: 26, stiffness: 280 }}
              className="flex h-[100dvh] w-full max-w-lg flex-col overflow-hidden bg-white shadow-2xl sm:h-auto sm:max-h-[80vh] sm:rounded-[2rem]"
              onClick={(event) => event.stopPropagation()}
            >
              <header className="flex items-center justify-between border-b border-gray-100 px-5 py-4">
                <div>
                  <h2 className="text-lg font-black tracking-tight text-gray-950">音色库</h2>
                  <p className="mt-0.5 text-[10px] font-black uppercase tracking-[0.3em] text-gray-300">voices · E:\AI\TTS</p>
                </div>
                <button type="button" onClick={() => setShowVoiceLibrary(false)} className="rounded-xl p-2 text-gray-400 transition hover:bg-gray-50 hover:text-black" aria-label="关闭">
                  <X className="h-5 w-5" />
                </button>
              </header>
              <div className="flex-1 space-y-3 overflow-y-auto px-5 py-4">
                {(ttsStatus?.voices || []).length === 0 && (
                  <div className="rounded-2xl border border-dashed border-gray-200 bg-gray-50/70 px-4 py-8 text-center text-[12px] leading-6 text-gray-400">
                    音色库为空。用「音色设计」+「存为可复用音色」造一个；
                    <br />
                    也可以用 CLI 导入：<code className="rounded bg-white px-1">tts voices import --name 名字 --ref 外部.pt</code>
                  </div>
                )}
                {(ttsStatus?.voices || []).map((item: TtsVoice) => (
                  <div key={item.name} className="rounded-[1.4rem] border border-gray-100 bg-white p-3.5 shadow-sm">
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex min-w-0 items-center gap-2">
                        <UserRound className="h-4 w-4 shrink-0 text-gray-400" />
                        <span className="truncate text-sm font-black text-gray-900">{item.name}</span>
                        <span className="shrink-0 rounded-full bg-gray-50 px-2 py-0.5 text-[10px] font-bold text-gray-400">
                          {item.source === "design" ? "设计" : item.source === "clone" ? "克隆" : item.source === "import" ? "导入" : item.source}
                        </span>
                      </div>
                      <div className="flex shrink-0 items-center gap-1.5">
                        {item.audio_url && <audio controls preload="none" src={item.audio_url} className="h-8 w-40" />}
                        <button
                          type="button"
                          onClick={() => {
                            setVoice(item.name);
                            setMode("say");
                            setShowVoiceLibrary(false);
                            addToast(`已选择音色 ${item.name}，切到「音色复用」`);
                          }}
                          className="inline-flex h-8 shrink-0 items-center gap-1 rounded-xl bg-black px-2.5 text-[11px] font-bold text-white shadow-sm transition hover:opacity-90"
                        >
                          使用
                        </button>
                      </div>
                    </div>
                    {(item.instruct || item.ref_text) && (
                      <p className="mt-1.5 line-clamp-2 text-[11px] leading-5 text-gray-400">{item.instruct || `参考文本：${item.ref_text}`}</p>
                    )}
                    <p className="mt-1 text-[10px] text-gray-300">
                      创建于 {item.created_at.replace("T", " ").slice(0, 16)}{item.has_voice_pt ? " · voice.pt 就绪" : " · 缺 voice.pt"}
                    </p>
                  </div>
                ))}
              </div>
            </motion.div>
          </div>
        )}
      </AnimatePresence>

      {/* 槽位切换确认 */}
      <AnimatePresence>
        {confirmSwitch && (
          <div className="fixed inset-0 z-[120] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm">
            <motion.div
              initial={{ scale: 0.95, opacity: 0 }}
              animate={{ scale: 1, opacity: 1 }}
              exit={{ scale: 0.96, opacity: 0 }}
              transition={{ type: "spring", damping: 26, stiffness: 300 }}
              className="w-full max-w-md rounded-[2rem] bg-white p-6 shadow-2xl"
            >
              <div className="flex items-start gap-3">
                <div className="grid h-10 w-10 shrink-0 place-items-center rounded-2xl bg-amber-50">
                  <TriangleAlert className="h-5 w-5 text-amber-500" />
                </div>
                <div>
                  <h3 className="text-base font-black text-gray-950">切换 TTS 引擎</h3>
                  <p className="mt-1.5 text-[12px] leading-5 text-gray-500">
                    当前实例是 <b className="text-gray-800">{confirmSwitch.currentEngine}</b>，而这次合成需要{" "}
                    <b className="text-gray-800">{confirmSwitch.targetEngine}</b>。
                    8G 显存只能驻留一个引擎，切换会<b>先关闭当前引擎再加载新引擎</b>（预计 8~70 秒）。
                  </p>
                </div>
              </div>
              <div className="mt-5 flex justify-end gap-2">
                <button
                  type="button"
                  onClick={() => setConfirmSwitch(null)}
                  className="h-10 rounded-2xl border border-gray-100 px-4 text-[12px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black"
                >
                  取消
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setConfirmSwitch(null);
                    void handleGenerate(true);
                  }}
                  className="h-10 rounded-2xl bg-black px-4 text-[12px] font-black text-white shadow-lg shadow-black/10 transition hover:opacity-90"
                >
                  关闭当前引擎并继续
                </button>
              </div>
            </motion.div>
          </div>
        )}
      </AnimatePresence>

      {/* 常驻内存询问：连续合成多次后提示，可一键重启为常驻模式 */}
      <AnimatePresence>
        {showResidentPrompt && (
          <div className="fixed inset-0 z-[120] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm">
            <motion.div
              initial={{ scale: 0.95, opacity: 0 }}
              animate={{ scale: 1, opacity: 1 }}
              exit={{ scale: 0.96, opacity: 0 }}
              transition={{ type: "spring", damping: 26, stiffness: 300 }}
              className="w-full max-w-md rounded-[2rem] bg-white p-6 shadow-2xl"
            >
              <div className="flex items-start gap-3">
                <div className="grid h-10 w-10 shrink-0 place-items-center rounded-2xl bg-indigo-50">
                  <AudioLines className="h-5 w-5 text-indigo-500" />
                </div>
                <div>
                  <h3 className="text-base font-black text-gray-950">让模型常驻内存？</h3>
                  <p className="mt-1.5 text-[12px] leading-5 text-gray-500">
                    你已经连续合成了 {completedTaskCountRef.current} 次。当前实例空闲
                    {" "}
                    {ttsStatus?.instance?.idle_timeout_s ?? 600} 秒后会自动退出，下次合成要重新等 8~70 秒加载。
                    切到常驻模式可以立即续用（不重新加载），但显存会一直被占用——用完记得点「释放内存」。
                  </p>
                </div>
              </div>
              <div className="mt-5 flex flex-wrap justify-end gap-2">
                <button
                  type="button"
                  onClick={() => handleResidentPromptDismiss(true)}
                  className="h-10 rounded-2xl border border-gray-100 px-4 text-[12px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black"
                >
                  不再提示
                </button>
                <button
                  type="button"
                  onClick={() => handleResidentPromptDismiss(false)}
                  className="h-10 rounded-2xl border border-gray-100 px-4 text-[12px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black"
                >
                  保持现状
                </button>
                <button
                  type="button"
                  onClick={() => void handleKeepResident()}
                  className="h-10 rounded-2xl bg-black px-4 text-[12px] font-black text-white shadow-lg shadow-black/10 transition hover:opacity-90"
                >
                  切换为常驻
                </button>
              </div>
            </motion.div>
          </div>
        )}
      </AnimatePresence>

      <AnimatePresence>
        {showSettings && <TtsSettingsModal onClose={() => setShowSettings(false)} addToast={addToast} onStatusChange={setTtsStatus} />}
      </AnimatePresence>
    </div>
  );
}
