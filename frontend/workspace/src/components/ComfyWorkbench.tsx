import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { AnimatePresence, motion } from "motion/react";
import {
  ArrowRight,
  CheckCircle2,
  Copy,
  Image as ImageIcon,
  ImageUp,
  Layers,
  Loader2,
  MemoryStick,
  Play,
  RefreshCcw,
  Settings2,
  Sparkles,
  Wand2,
  X,
} from "lucide-react";
import {
  AuthRequiredError,
  cancelUploadSession,
  chatWithAI,
  createGenerationTask,
  fetchComfyLog,
  fetchComfyStatus,
  fetchImageDownloadUrl,
  fetchTaskDetail,
  freeComfyMemory,
  uploadFileResumable,
  type ChatModel,
  type ComfyLogReport,
  type ComfyStatus,
  type GenerationTask,
  type ReferenceUploadResult,
  type UploadProgress,
} from "../services/apiService";
import ComfySettingsModal from "./ComfySettingsModal";

// 「Comfy 生图」板块：仅本地开发者模式（npm run local）出现，任务交给本机 ComfyUI 执行。
// 页面结构与交互完全沿用 GenerationWorkbench（同款左栏 + 结果画布 + 轮询/恢复逻辑），
// 差别只在：模型是 ComfyUI 的三个本地工作流、多一组步数/种子，以及一个 Comfy 设置入口。

interface ComfyWorkbenchProps {
  isAuthenticated: boolean;
  selectedModel: string;
  chatModels: ChatModel[];
  onOpenAuth: (mode: "login" | "register") => void;
  addToast: (message: string, type?: "success" | "error" | "info") => void;
  selectedTaskNo?: string;
  resetSignal?: number;
  /** 悬浮窗对话「填回描述」的投递（App 只在当前板块是生图板块时下发）。 */
  fillRequest?: { id: number; text: string } | null;
  onFillRequestConsumed?: () => void;
  onGenerationSubmitted?: () => void;
}

type GenerationMode = "text_to_image" | "image_to_image";
type ResolutionCode = "1k" | "2k";
type SeedMode = "random" | "fixed";

interface RatioValue {
  width: number;
  height: number;
}

const ratioPresets = [
  { label: "1:1", width: 1, height: 1 },
  { label: "4:3", width: 4, height: 3 },
  { label: "3:2", width: 3, height: 2 },
  { label: "16:9", width: 16, height: 9 },
  { label: "9:16", width: 9, height: 16 },
  { label: "2:3", width: 2, height: 3 },
  { label: "3:4", width: 3, height: 4 },
];

const resolutionOptions = [
  { label: "1K", value: "1k" },
  { label: "2K", value: "2k" },
];

const ACTIVE_TASK_KEY = "mirror.activeComfyTaskNo";
// 「模型保持常驻」是本次会话的选择（关掉页面就忘掉），用一个 sessionStorage 记住。
const KEEP_RESIDENT_KEY = "mirror.comfyKeepResident";
const runningStatuses = ["queued", "running"];
const terminalStatuses = ["succeeded", "failed", "canceled"];
const EXAMPLE_PROMPT = "一只布偶猫在茶杯旁睡觉，温暖的晨光穿过纱帘，木地板反光，写实风格，浅景深";
const MAX_REFERENCE_SOURCE_IMAGE_BYTES = 24 * 1024 * 1024;
const MAX_REFERENCE_IMAGE_BYTES = 10 * 1024 * 1024;
const TARGET_REFERENCE_IMAGE_BYTES = 1.5 * 1024 * 1024;
const MAX_REFERENCE_IMAGE_DIMENSION = 1600;
const REFERENCE_IMAGE_JPEG_QUALITIES = [0.86, 0.8, 0.74, 0.68];
const ACTIVE_TASK_RETENTION_MS = 30 * 60 * 1000;
const POST_SUCCESS_GRACE_MS = 15 * 1000;
// 与 GenerationWorkbench / shared/hooks/useTaskPolling 保持同一护栏语义（台账 #3）。
const MAX_POLL_ATTEMPTS = 900;
const MAX_POLL_ERROR_COUNT = 3;
const MAX_POLL_LIFETIME_MS = 20 * 60 * 1000;

// ── 与 GenerationWorkbench 相同的纯函数工具（比例/尺寸/参考图压缩）──

function parseTimestamp(value?: string | null): number {
  if (!value) return 0;
  const timestamp = new Date(value).getTime();
  return Number.isNaN(timestamp) ? 0 : timestamp;
}

function resolveTaskTimestamp(detail: GenerationTask): number {
  return parseTimestamp(detail.started_at) || parseTimestamp(detail.submitted_at) || Date.now();
}

function clampProgress(value: number): number {
  return Math.max(0, Math.min(Math.round(value || 0), 100));
}

function getVisibleProgress(detail: GenerationTask): number {
  const rawProgress = clampProgress(detail.progress_percent ?? 0);
  if (detail.images?.length) return 100;
  if (detail.status === "queued") return Math.min(Math.max(rawProgress, 8), 22);
  if (detail.status === "running") return Math.min(Math.max(rawProgress, 20), 92);
  if (detail.status === "succeeded") return Math.min(Math.max(rawProgress, 76), 92);
  if (detail.status === "failed" || detail.status === "canceled") return Math.min(rawProgress, 82);
  return rawProgress;
}

function getDataUrlByteLength(dataUrl: string): number {
  const encoded = dataUrl.split(",", 2)[1] || "";
  const padding = encoded.endsWith("==") ? 2 : encoded.endsWith("=") ? 1 : 0;
  return Math.max(0, Math.floor((encoded.length * 3) / 4) - padding);
}

function readBlobAsDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onloadend = () => {
      if (typeof reader.result === "string") {
        resolve(reader.result);
        return;
      }
      reject(new Error("参考图读取失败"));
    };
    reader.onerror = () => reject(new Error("参考图读取失败"));
    reader.readAsDataURL(blob);
  });
}

function loadImageElement(src: string): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error("参考图解析失败"));
    image.src = src;
  });
}

function dataUrlToFile(dataUrl: string, fileName: string, mimeType: string): File {
  const encoded = dataUrl.split(",", 2)[1] || "";
  const binary = window.atob(encoded);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return new File([bytes], fileName, { type: mimeType });
}

function resizeImageDimensions(width: number, height: number, maxDimension: number) {
  const longestEdge = Math.max(width, height);
  if (!longestEdge || longestEdge <= maxDimension) {
    return { width, height, scaled: false };
  }
  const scale = maxDimension / longestEdge;
  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
    scaled: true,
  };
}

function hasTransparentPixels(canvas: HTMLCanvasElement, width: number, height: number): boolean {
  const context = canvas.getContext("2d", { willReadFrequently: true });
  if (!context) return false;
  const { data } = context.getImageData(0, 0, width, height);
  for (let index = 3; index < data.length; index += 4) {
    if (data[index] !== 255) return true;
  }
  return false;
}

function renameReferenceFile(name: string, mimeType: string): string {
  const trimmed = (name || "reference-image").trim() || "reference-image";
  const dotIndex = trimmed.lastIndexOf(".");
  const baseName = dotIndex > 0 ? trimmed.slice(0, dotIndex) : trimmed;
  const extension = mimeType === "image/png" ? ".png" : ".jpg";
  return `${baseName}${extension}`;
}

function greatestCommonDivisor(a: number, b: number): number {
  let x = Math.abs(Math.round(a));
  let y = Math.abs(Math.round(b));
  while (y) {
    const next = x % y;
    x = y;
    y = next;
  }
  return x || 1;
}

function normalizeRatio(width: number, height: number): RatioValue {
  const safeWidth = Math.max(1, Math.min(Math.round(width || 1), 99));
  const safeHeight = Math.max(1, Math.min(Math.round(height || 1), 99));
  const divisor = greatestCommonDivisor(safeWidth, safeHeight);
  return {
    width: Math.max(1, Math.round(safeWidth / divisor)),
    height: Math.max(1, Math.round(safeHeight / divisor)),
  };
}

function roundToMultiple(value: number, multiple: number): number {
  return Math.max(multiple, Math.round(value / multiple) * multiple);
}

function sizeCodeFromRatio(ratio: RatioValue, resolution: ResolutionCode): string {
  const normalized = normalizeRatio(ratio.width, ratio.height);
  if (resolution === "1k" && normalized.width === normalized.height) {
    return "1024x1024";
  }
  const longEdge = resolution === "2k" ? 2048 : 1536;
  const isLandscape = normalized.width >= normalized.height;
  const shortEdge = roundToMultiple(
    longEdge * (isLandscape ? normalized.height / normalized.width : normalized.width / normalized.height),
    16,
  );
  const width = isLandscape ? longEdge : shortEdge;
  const height = isLandscape ? shortEdge : longEdge;
  return `${width}x${height}`;
}

function parseSizeCode(sizeCode: string): { width: number; height: number } | null {
  const [width, height] = (sizeCode || "").split("x").map((item) => Number(item));
  if (!width || !height) return null;
  return { width, height };
}

function ratioFromSizeCode(sizeCode: string): RatioValue {
  const parsed = parseSizeCode(sizeCode);
  if (!parsed) return { width: 16, height: 9 };
  return normalizeRatio(parsed.width, parsed.height);
}

function resolutionFromSizeCode(sizeCode: string): ResolutionCode {
  const parsed = parseSizeCode(sizeCode);
  if (!parsed) return "1k";
  return Math.max(parsed.width, parsed.height) > 1536 ? "2k" : "1k";
}

async function optimizeReferenceImageFile(file: File): Promise<{
  dataUrl: string;
  fileName: string;
  bytes: number;
  compressed: boolean;
  file: File;
}> {
  const objectUrl = URL.createObjectURL(file);
  try {
    const image = await loadImageElement(objectUrl);
    const { width, height, scaled } = resizeImageDimensions(
      image.naturalWidth || image.width,
      image.naturalHeight || image.height,
      MAX_REFERENCE_IMAGE_DIMENSION,
    );
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("浏览器暂不支持参考图压缩");
    context.drawImage(image, 0, 0, width, height);

    if (hasTransparentPixels(canvas, width, height)) {
      const dataUrl = canvas.toDataURL("image/png");
      const fileName = renameReferenceFile(file.name, "image/png");
      return { dataUrl, fileName, bytes: getDataUrlByteLength(dataUrl), compressed: scaled, file: dataUrlToFile(dataUrl, fileName, "image/png") };
    }

    let bestDataUrl = canvas.toDataURL("image/jpeg", REFERENCE_IMAGE_JPEG_QUALITIES[0]);
    for (const quality of REFERENCE_IMAGE_JPEG_QUALITIES) {
      const candidate = canvas.toDataURL("image/jpeg", quality);
      bestDataUrl = candidate;
      if (getDataUrlByteLength(candidate) <= TARGET_REFERENCE_IMAGE_BYTES) break;
    }
    const fileName = renameReferenceFile(file.name, "image/jpeg");
    return {
      dataUrl: bestDataUrl,
      fileName,
      bytes: getDataUrlByteLength(bestDataUrl),
      compressed: scaled || bestDataUrl !== (await readBlobAsDataUrl(file)),
      file: dataUrlToFile(bestDataUrl, fileName, "image/jpeg"),
    };
  } finally {
    URL.revokeObjectURL(objectUrl);
  }
}

function getAspectRatioFromSizeCode(sizeCode: string): number {
  const [width, height] = sizeCode.split("x").map((item) => Number(item));
  if (!width || !height) return 1;
  return width / height;
}

function isTaskStillActive(detail: GenerationTask): boolean {
  if (runningStatuses.includes(detail.status)) {
    return Date.now() - resolveTaskTimestamp(detail) <= ACTIVE_TASK_RETENTION_MS;
  }
  if (detail.status === "succeeded" && !detail.images?.length) {
    const finishedAt = parseTimestamp(detail.finished_at);
    const reference = finishedAt || resolveTaskTimestamp(detail);
    return Date.now() - reference <= POST_SUCCESS_GRACE_MS;
  }
  return false;
}

function formatElapsed(seconds: number): string {
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${String(seconds % 60).padStart(2, "0")} 秒`;
}

export default function ComfyWorkbench({
  isAuthenticated,
  selectedModel,
  chatModels,
  onOpenAuth,
  addToast,
  selectedTaskNo = "",
  resetSignal = 0,
  fillRequest = null,
  onFillRequestConsumed,
  onGenerationSubmitted,
}: ComfyWorkbenchProps) {
  const referenceInputRef = useRef<HTMLInputElement | null>(null);
  const [prompt, setPrompt] = useState("");
  const [negativePrompt, setNegativePrompt] = useState("");
  const [ratio, setRatio] = useState<RatioValue>({ width: 1, height: 1 });
  const [customRatioWidth, setCustomRatioWidth] = useState("1");
  const [customRatioHeight, setCustomRatioHeight] = useState("1");
  const [resolutionCode, setResolutionCode] = useState<ResolutionCode>("1k");
  const [generationMode, setGenerationMode] = useState<GenerationMode>("text_to_image");
  const [comfyModel, setComfyModel] = useState("sixstep");
  const [steps, setSteps] = useState(25);
  const [seedMode, setSeedMode] = useState<SeedMode>("random");
  const [seedValue, setSeedValue] = useState("");
  const [lastSeed, setLastSeed] = useState<number | null>(null);
  const [referenceImageDataUrl, setReferenceImageDataUrl] = useState("");
  const [referenceImageName, setReferenceImageName] = useState("");
  const [referenceUploadToken, setReferenceUploadToken] = useState("");
  const [referenceUploadFile, setReferenceUploadFile] = useState<File | null>(null);
  const [referenceUploadProgress, setReferenceUploadProgress] = useState<UploadProgress | null>(null);
  const [isUploadingReference, setIsUploadingReference] = useState(false);
  const [helperDraft, setHelperDraft] = useState("");
  const [isHelping, setIsHelping] = useState(false);
  const [taskNo, setTaskNo] = useState("");
  const [taskStatus, setTaskStatus] = useState("");
  const [progress, setProgress] = useState(0);
  const [previewUrl, setPreviewUrl] = useState("");
  const [downloadUrl, setDownloadUrl] = useState("");
  const [previewSize, setPreviewSize] = useState<{ width: number; height: number } | null>(null);
  const [isGenerating, setIsGenerating] = useState(false);
  const [isRestoringTask, setIsRestoringTask] = useState(false);
  const [elapsedSeconds, setElapsedSeconds] = useState(0);
  const [comfyStatus, setComfyStatus] = useState<ComfyStatus | null>(null);
  const [isCheckingComfy, setIsCheckingComfy] = useState(false);
  const [issueReport, setIssueReport] = useState<ComfyLogReport | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  // 出图后模型会常驻内存（编码器 + 主模型 + VAE 约 16G），成功后弹一次
  // 「保持常驻 / 释放内存」；选择保持则本次会话不再询问（sessionStorage）。
  const [showMemoryPrompt, setShowMemoryPrompt] = useState(false);
  const [isReleasingMemory, setIsReleasingMemory] = useState(false);
  const keepResidentRef = useRef(false);
  const memoryPromptedTaskRef = useRef("");
  const issueCheckedTaskRef = useRef("");
  const didMountRef = useRef(false);
  const pollingTimerRef = useRef<number | null>(null);
  const pollingAttemptRef = useRef(0);
  const pollingErrorCountRef = useRef(0);
  const pollingStartedAtRef = useRef(0);
  const completionToastTaskRef = useRef("");
  const taskSessionRef = useRef(0);
  const referenceUploadVersionRef = useRef(0);
  const referenceUploadPromiseRef = useRef<Promise<ReferenceUploadResult | null> | null>(null);
  const referenceUploadAbortRef = useRef<AbortController | null>(null);
  const activeReferenceUploadIdRef = useRef("");
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

  const refreshComfyStatus = async (): Promise<ComfyStatus | null> => {
    setIsCheckingComfy(true);
    try {
      const status = await fetchComfyStatus();
      setComfyStatus(status);
      if (status.models?.length && !status.models.some((model) => model.id === comfyModel)) {
        setComfyModel(status.models[0].id);
      }
      return status;
    } catch {
      setComfyStatus(null);
      return null;
    } finally {
      setIsCheckingComfy(false);
    }
  };

  useEffect(() => {
    void refreshComfyStatus();
  }, []);

  // 生成中显示已用时长（ComfyUI 单张十几秒到几分钟，给用户一个参照）。
  useEffect(() => {
    if (!isGenerating || previewUrl) {
      setElapsedSeconds(0);
      return;
    }
    const startedAt = Date.now();
    const timer = window.setInterval(() => {
      setElapsedSeconds(Math.max(0, Math.round((Date.now() - startedAt) / 1000)));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [isGenerating, previewUrl, taskNo]);

  const invalidateReferenceUpload = () => {
    referenceUploadVersionRef.current += 1;
    referenceUploadAbortRef.current?.abort();
    referenceUploadAbortRef.current = null;
    if (activeReferenceUploadIdRef.current) {
      void cancelUploadSession(activeReferenceUploadIdRef.current).catch(() => {});
      activeReferenceUploadIdRef.current = "";
    }
    referenceUploadPromiseRef.current = null;
    setIsUploadingReference(false);
    setReferenceUploadProgress(null);
  };

  const isReferenceUploadTokenError = (error: unknown) => {
    const message = error instanceof Error ? error.message : String(error || "");
    return (
      message.includes("参考图上传凭证") ||
      message.includes("reference_upload_token") ||
      (message.includes("参考图") && (message.includes("失效") || message.includes("无效") || message.includes("重新上传")))
    );
  };

  const startReferenceUpload = async (file: File, fileName: string): Promise<ReferenceUploadResult | null> => {
    if (!file) return null;

    const uploadVersion = referenceUploadVersionRef.current + 1;
    referenceUploadVersionRef.current = uploadVersion;
    referenceUploadAbortRef.current?.abort();
    const abortController = new AbortController();
    referenceUploadAbortRef.current = abortController;
    setIsUploadingReference(true);

    const uploadFile = file.name === fileName ? file : new File([file], fileName, { type: file.type || "image/jpeg" });
    const uploadPromise = uploadFileResumable("generation_reference_image", uploadFile, {
      signal: abortController.signal,
      onSession: (session) => {
        if (referenceUploadVersionRef.current === uploadVersion) {
          activeReferenceUploadIdRef.current = session.upload_id;
        }
      },
      onProgress: (nextProgress) => {
        if (referenceUploadVersionRef.current === uploadVersion) {
          setReferenceUploadProgress(nextProgress);
        }
      },
    })
      .then(({ result: uploadResult }) => {
        if (referenceUploadVersionRef.current === uploadVersion) {
          setReferenceUploadToken(uploadResult.reference_upload_token);
          setReferenceImageName(uploadResult.reference_image_name || fileName);
        }
        return uploadResult;
      })
      .finally(() => {
        if (referenceUploadVersionRef.current === uploadVersion) {
          setIsUploadingReference(false);
          referenceUploadPromiseRef.current = null;
          referenceUploadAbortRef.current = null;
          activeReferenceUploadIdRef.current = "";
        }
      });

    referenceUploadPromiseRef.current = uploadPromise;
    return uploadPromise;
  };

  const ensureReferenceUpload = async (
    file: File | null,
    fileName: string,
    options: { force?: boolean } = {},
  ): Promise<ReferenceUploadResult | null> => {
    const { force = false } = options;

    if (!file) {
      return referenceUploadToken
        ? {
            reference_upload_token: referenceUploadToken,
            reference_image_name: referenceImageName || fileName,
            reference_image_attached: true,
          }
        : null;
    }
    if (!force && referenceUploadToken) {
      return {
        reference_upload_token: referenceUploadToken,
        reference_image_name: referenceImageName || fileName,
        reference_image_attached: true,
      };
    }
    if (!force && referenceUploadPromiseRef.current) {
      return referenceUploadPromiseRef.current;
    }
    return startReferenceUpload(file, fileName);
  };

  const broadcastTaskUpdate = (detail: GenerationTask, hasImage: boolean) => {
    window.dispatchEvent(
      new CustomEvent("mirror:task-updated", {
        detail: {
          taskNo: detail.task_no,
          status: detail.status,
          hasImage,
          progressPercent: getVisibleProgress(detail),
        },
      }),
    );
  };

  const applyImageSettingsFromTask = (detail: GenerationTask) => {
    const restoredRatio = ratioFromSizeCode(detail.size_code || "1024x1024");
    setRatio(restoredRatio);
    setCustomRatioWidth(String(restoredRatio.width));
    setCustomRatioHeight(String(restoredRatio.height));
    setResolutionCode(resolutionFromSizeCode(detail.size_code || "1024x1024"));
    if (detail.comfy_model) setComfyModel(detail.comfy_model);
    if (typeof detail.steps === "number" && detail.steps > 0) setSteps(detail.steps);
    if (typeof detail.seed === "number" && detail.seed > 0) {
      setSeedMode("fixed");
      setSeedValue(String(detail.seed));
      setLastSeed(detail.seed);
    }
  };

  const resetWorkbench = () => {
    beginTaskSession();
    stopPolling();
    invalidateReferenceUpload();
    pollingAttemptRef.current = 0;
    pollingErrorCountRef.current = 0;
    pollingStartedAtRef.current = 0;
    completionToastTaskRef.current = "";
    setPrompt("");
    setNegativePrompt("");
    setRatio({ width: 1, height: 1 });
    setCustomRatioWidth("1");
    setCustomRatioHeight("1");
    setResolutionCode("1k");
    setGenerationMode("text_to_image");
    setSeedMode("random");
    setSeedValue("");
    setLastSeed(null);
    setReferenceImageDataUrl("");
    setReferenceImageName("");
    setReferenceUploadToken("");
    setReferenceUploadFile(null);
    setReferenceUploadProgress(null);
    setHelperDraft("");
    setTaskNo("");
    setTaskStatus("");
    setProgress(0);
    setPreviewUrl("");
    setDownloadUrl("");
    setPreviewSize(null);
    setIssueReport(null);
    issueCheckedTaskRef.current = "";
    setIsGenerating(false);
    setIsRestoringTask(false);
    window.localStorage.removeItem(ACTIVE_TASK_KEY);
  };

  const requireLogin = (mode: "login" | "register" = "login") => {
    if (!isAuthenticated) {
      onOpenAuth(mode);
      return false;
    }
    return true;
  };

  const applyTaskDetail = async (detail: GenerationTask, sessionId?: number) => {
    if (sessionId && !isCurrentTaskSession(sessionId)) {
      return { hasImage: false, hasRenderablePreview: false };
    }
    setTaskNo(detail.task_no);
    setTaskStatus(detail.status);
    setProgress(getVisibleProgress(detail));
    setGenerationMode((detail.generation_mode || "text_to_image") as GenerationMode);
    setReferenceImageName(detail.reference_image_name || "");
    if (runningStatuses.includes(detail.status) || (detail.status === "succeeded" && !detail.images?.length)) {
      window.localStorage.setItem(ACTIVE_TASK_KEY, detail.task_no);
    } else {
      window.localStorage.removeItem(ACTIVE_TASK_KEY);
    }

    const firstImage = detail.images?.[0];
    let hasImage = false;
    let hasRenderablePreview = false;
    if (firstImage?.id) {
      hasImage = true;
      setIsGenerating(false);
      setPreviewSize(
        firstImage.width && firstImage.height ? { width: firstImage.width, height: firstImage.height } : null,
      );
      let resolvedPreview = firstImage.preview_url || firstImage.download_url || "";
      let resolvedDownload = firstImage.download_url || "";
      if (!resolvedPreview) {
        try {
          const download = await fetchImageDownloadUrl(firstImage.id);
          if (sessionId && !isCurrentTaskSession(sessionId)) {
            return { hasImage: true, hasRenderablePreview: false };
          }
          resolvedPreview = download.preview_url || download.url || firstImage.download_url || "";
          resolvedDownload = download.url || firstImage.download_url || "";
        } catch {
          resolvedPreview = firstImage.download_url || "";
          resolvedDownload = firstImage.download_url || "";
        }
      }
      if (sessionId && !isCurrentTaskSession(sessionId)) {
        return { hasImage: true, hasRenderablePreview: Boolean(resolvedPreview) };
      }
      setPreviewUrl(resolvedPreview);
      setDownloadUrl(resolvedDownload || resolvedPreview);
      hasRenderablePreview = Boolean(resolvedPreview);
      setProgress(100);
      if (hasRenderablePreview && completionToastTaskRef.current !== detail.task_no) {
        completionToastTaskRef.current = detail.task_no;
        addToast("图片已生成");
        onGenerationSubmitted?.();
      }
    } else {
      setPreviewUrl("");
      setDownloadUrl("");
      setPreviewSize(null);
      if (detail.status === "failed" || detail.status === "canceled") {
        setIsGenerating(false);
        window.localStorage.removeItem(ACTIVE_TASK_KEY);
        // 任务失败时顺带把 ComfyUI 服务日志里的 ERROR/WARNING 抓回来展示，避免“有问题但检测不到”。
        if (detail.status === "failed" && issueCheckedTaskRef.current !== detail.task_no) {
          issueCheckedTaskRef.current = detail.task_no;
          void fetchComfyLog(4)
            .then((report) => {
              if (isCurrentTaskSession(sessionId ?? taskSessionRef.current)) setIssueReport(report);
            })
            .catch(() => {});
        }
      }
    }
    broadcastTaskUpdate(detail, hasImage);
    return { hasImage, hasRenderablePreview };
  };

  const clearReferenceImage = () => {
    invalidateReferenceUpload();
    setReferenceImageDataUrl("");
    setReferenceImageName("");
    setReferenceUploadToken("");
    setReferenceUploadFile(null);
    setReferenceUploadProgress(null);
    if (referenceInputRef.current) {
      referenceInputRef.current.value = "";
    }
  };

  const loadReferenceImageFile = async (file: File) => {
    if (!file.type.startsWith("image/")) {
      addToast("请上传图片文件", "info");
      return;
    }
    if (file.size > MAX_REFERENCE_SOURCE_IMAGE_BYTES) {
      addToast("参考图源文件不能超过 24MB", "error");
      return;
    }
    try {
      const optimized = await optimizeReferenceImageFile(file);
      if (optimized.bytes > MAX_REFERENCE_IMAGE_BYTES) {
        addToast("参考图压缩后仍超过 10MB，请裁剪后再试", "error");
        return;
      }
      const nextReferenceImageName = optimized.fileName || file.name || "粘贴图片.jpg";
      invalidateReferenceUpload();
      setReferenceImageDataUrl(optimized.dataUrl);
      setReferenceImageName(nextReferenceImageName);
      setReferenceUploadToken("");
      setReferenceUploadFile(optimized.file);

      if (!isAuthenticated) {
        addToast("参考图已就绪，登录后开始生成时会自动上传");
        return;
      }
      try {
        await startReferenceUpload(optimized.file, nextReferenceImageName);
        addToast(optimized.compressed ? "参考图已压缩并就绪" : "参考图已就绪");
      } catch (error: any) {
        if (error instanceof AuthRequiredError) {
          onOpenAuth("login");
          return;
        }
        addToast(error.message || "参考图已就绪，开始生成时会继续处理", "info");
      }
    } catch (error: any) {
      addToast(error.message || "参考图处理失败", "error");
    }
  };

  // 挂载后恢复上次未完成的任务 / 从历史记录打开任务。
  useEffect(() => {
    if (!isAuthenticated) return;
    if (selectedTaskNo) return;
    const savedTaskNo = window.localStorage.getItem(ACTIVE_TASK_KEY);
    if (!savedTaskNo) return;

    let ignore = false;
    const sessionId = beginTaskSession();
    setIsRestoringTask(true);
    fetchTaskDetail(savedTaskNo)
      .then(async (detail) => {
        if (ignore || !isCurrentTaskSession(sessionId)) return;
        if (!isTaskStillActive(detail)) {
          window.localStorage.removeItem(ACTIVE_TASK_KEY);
          return;
        }
        setPrompt(detail.prompt || "");
        setNegativePrompt(detail.negative_prompt || "");
        applyImageSettingsFromTask(detail);
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
    let ignore = false;
    const sessionId = beginTaskSession();
    invalidateReferenceUpload();
    setPreviewUrl("");
    setDownloadUrl("");
    setPreviewSize(null);
    setIsRestoringTask(true);
    fetchTaskDetail(selectedTaskNo)
      .then(async (detail) => {
        if (ignore || !isCurrentTaskSession(sessionId)) return;
        setPrompt(detail.prompt || "");
        setNegativePrompt(detail.negative_prompt || "");
        applyImageSettingsFromTask(detail);
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
      referenceUploadAbortRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (generationMode !== "image_to_image") return;
    const handlePaste = (event: ClipboardEvent) => {
      const items = Array.from(event.clipboardData?.items || []);
      const imageItem = items.find((item) => item.type.startsWith("image/"));
      if (!imageItem) return;
      const file = imageItem.getAsFile();
      if (!file) return;
      event.preventDefault();
      void loadReferenceImageFile(file);
    };
    window.addEventListener("paste", handlePaste);
    return () => window.removeEventListener("paste", handlePaste);
  }, [generationMode]);

  // 轮询任务状态（ComfyUI 单张耗时会长一些，轮询寿命放宽到 20 分钟）。
  useEffect(() => {
    stopPolling();
    if (!isAuthenticated || !taskNo || previewUrl) return;
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
        stopWithNotice("任务状态同步已停止，请到历史记录手动刷新查看结果");
        return;
      }
      try {
        const detail = await fetchTaskDetail(taskNo);
        if (cancelled) return;
        pollingErrorCountRef.current = 0;
        const { hasImage, hasRenderablePreview } = await applyTaskDetail(detail, taskSessionRef.current);

        const shouldKeepPolling =
          (!hasImage && isTaskStillActive(detail)) ||
          (hasImage && !hasRenderablePreview && detail.status === "succeeded");

        if (shouldKeepPolling) {
          const delay = detail.status === "succeeded" ? 1200 : pollingAttemptRef.current < 4 ? 1200 : 2500;
          pollingTimerRef.current = window.setTimeout(poll, delay);
        }
      } catch {
        if (cancelled) return;
        pollingErrorCountRef.current += 1;
        if (pollingErrorCountRef.current >= MAX_POLL_ERROR_COUNT) {
          stopWithNotice("任务状态同步暂时不可用，请稍后到历史记录手动刷新");
          return;
        }
        pollingTimerRef.current = window.setTimeout(poll, 2000 + pollingErrorCountRef.current * 1500);
      }
    };

    pollingAttemptRef.current = 0;
    pollingTimerRef.current = window.setTimeout(poll, taskStatus === "succeeded" ? 600 : 1200);

    return () => {
      cancelled = true;
      stopPolling();
    };
  }, [isAuthenticated, previewUrl, taskNo, taskStatus]);

  // 悬浮窗对话「填回描述」：写进创作描述。
  useEffect(() => {
    if (!fillRequest) return;
    setPrompt(fillRequest.text);
    addToastRef.current("已把对话里的提示词填进创作描述", "success");
    onFillRequestConsumed?.();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fillRequest]);

  // ── 内存常驻 / 释放 ──
  // 出图完成后模型会一直驻留（编码器 8.9G + 主模型 6.9G + VAE 0.6G）：
  // 继续出图时保持常驻最省时间，不再出图就释放。选择「保持常驻」后本次会话不再弹窗。
  useEffect(() => {
    keepResidentRef.current = window.sessionStorage.getItem(KEEP_RESIDENT_KEY) === "1";
  }, []);

  useEffect(() => {
    if (taskStatus !== "succeeded" || !previewUrl) return;
    if (keepResidentRef.current) return;
    if (memoryPromptedTaskRef.current === taskNo) return;
    memoryPromptedTaskRef.current = taskNo || "latest";
    setShowMemoryPrompt(true);
  }, [taskStatus, previewUrl, taskNo]);

  const handleFreeMemory = async () => {
    setShowMemoryPrompt(false);
    setIsReleasingMemory(true);
    try {
      const result = await freeComfyMemory();
      setComfyStatus(result.status);
      addToastRef.current("已释放 ComfyUI 内存（模型卸载，下次出图会重新加载）", "success");
    } catch (error: any) {
      addToastRef.current(error?.message || "释放内存失败", "error");
    } finally {
      setIsReleasingMemory(false);
    }
  };

  const handleKeepResident = () => {
    keepResidentRef.current = true;
    window.sessionStorage.setItem(KEEP_RESIDENT_KEY, "1");
    setShowMemoryPrompt(false);
    addToastRef.current("模型保持常驻，本次会话不再提醒", "info");
  };

  const handleReferenceImageChange = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    void loadReferenceImageFile(file);
  };

  const handlePresetRatioSelect = (nextRatio: RatioValue) => {
    const normalized = normalizeRatio(nextRatio.width, nextRatio.height);
    setRatio(normalized);
    setCustomRatioWidth(String(normalized.width));
    setCustomRatioHeight(String(normalized.height));
  };

  const handleCustomRatioChange = (field: "width" | "height", value: string) => {
    const cleaned = value.replace(/\D/g, "").slice(0, 2);
    const nextWidth = field === "width" ? cleaned : customRatioWidth;
    const nextHeight = field === "height" ? cleaned : customRatioHeight;
    setCustomRatioWidth(nextWidth);
    setCustomRatioHeight(nextHeight);
    const numericWidth = Number(nextWidth);
    const numericHeight = Number(nextHeight);
    if (numericWidth > 0 && numericHeight > 0) {
      setRatio(normalizeRatio(numericWidth, numericHeight));
    }
  };

  const handleSeedValueChange = (value: string) => {
    setSeedValue(value.replace(/\D/g, "").slice(0, 12));
  };

  const handleGenerate = async () => {
    if (!requireLogin("register")) return;
    if (!prompt.trim()) {
      addToast("请先填写创作描述", "info");
      return;
    }
    if (Math.max(ratio.width, ratio.height) / Math.min(ratio.width, ratio.height) > 3) {
      addToast("自定义比例不能超过 3:1", "error");
      return;
    }
    if (generationMode === "image_to_image" && !referenceImageDataUrl && !referenceUploadToken) {
      addToast("请先上传参考图", "info");
      return;
    }
    const fixedSeed = seedMode === "fixed" ? Number.parseInt(seedValue, 10) : 0;
    if (seedMode === "fixed" && (!Number.isFinite(fixedSeed) || fixedSeed <= 0)) {
      addToast("固定种子需要是正整数", "error");
      return;
    }

    // 先确认 ComfyUI 在运行；没启动就引导到设置里去启动，而不是提交一个必然失败的任务。
    let status = comfyStatus;
    if (!status) {
      status = await refreshComfyStatus();
    }
    if (!status?.running) {
      addToast("ComfyUI 未运行，请先在 Comfy 设置里启动", "info");
      setShowSettings(true);
      return;
    }

    const resolvedSizeCode = sizeCodeFromRatio(ratio, resolutionCode);
    const sessionId = beginTaskSession();
    stopPolling();
    pollingAttemptRef.current = 0;
    pollingErrorCountRef.current = 0;
    pollingStartedAtRef.current = 0;
    completionToastTaskRef.current = "";
    setIssueReport(null);
    issueCheckedTaskRef.current = "";
    window.localStorage.removeItem(ACTIVE_TASK_KEY);
    setIsGenerating(true);
    setIsRestoringTask(false);
    setTaskNo("");
    setPreviewUrl("");
    setDownloadUrl("");
    setPreviewSize(null);
    setTaskStatus("queued");
    setProgress(8);
    setElapsedSeconds(0);

    const currentReferenceUploadFile = referenceUploadFile;
    const currentReferenceImageName = referenceImageName;
    let resolvedReferenceUploadToken = referenceUploadToken;
    let resolvedReferenceImageName = referenceImageName;
    const submissionPayload = {
      prompt,
      negative_prompt: negativePrompt,
      size_code: resolvedSizeCode,
      quality_code: "auto" as const,
      generation_mode: generationMode,
      reference_image_name: referenceImageName,
      reference_image_data_url: "",
      reference_upload_token: referenceUploadToken,
      client_request_id: `${Date.now()}-${Math.random().toString(16).slice(2)}`,
      wait_seconds: 0,
      engine: "comfy" as const,
      comfy_model: comfyModel,
      seed: seedMode === "fixed" ? fixedSeed : undefined,
      steps,
    };
    let submissionAccepted = false;
    try {
      let task: GenerationTask | null = null;
      const syncReferenceUploadForSubmission = async (force = false) => {
        const uploadResult = await ensureReferenceUpload(currentReferenceUploadFile, currentReferenceImageName, { force });
        if (!isCurrentTaskSession(sessionId)) return;
        if (!uploadResult?.reference_upload_token) {
          throw new Error("参考图上传失败，请重试");
        }
        resolvedReferenceUploadToken = uploadResult.reference_upload_token;
        resolvedReferenceImageName = uploadResult.reference_image_name || currentReferenceImageName;
        setReferenceUploadToken(resolvedReferenceUploadToken);
        setReferenceImageName(resolvedReferenceImageName);
        submissionPayload.reference_upload_token = resolvedReferenceUploadToken;
        submissionPayload.reference_image_name = resolvedReferenceImageName;
      };
      if (generationMode === "image_to_image" && currentReferenceUploadFile) {
        await syncReferenceUploadForSubmission();
      }
      try {
        task = await createGenerationTask(submissionPayload);
      } catch (error) {
        if (error instanceof AuthRequiredError) throw error;
        if (generationMode === "image_to_image" && currentReferenceUploadFile && isReferenceUploadTokenError(error)) {
          await syncReferenceUploadForSubmission(true);
          task = await createGenerationTask(submissionPayload);
        } else {
          throw error;
        }
      }
      if (!task) throw new Error("生图任务提交失败");
      submissionAccepted = true;
      if (!isCurrentTaskSession(sessionId)) return;
      if (typeof task.seed === "number" && task.seed > 0) setLastSeed(task.seed);
      let detail = task;
      if (!task.images?.length && !terminalStatuses.includes(task.status)) {
        try {
          detail = await fetchTaskDetail(task.task_no);
        } catch {
          detail = task;
        }
      }
      await applyTaskDetail(detail, sessionId);
      if (isCurrentTaskSession(sessionId) && !detail.images?.length) {
        addToast("Comfy 生图任务正在执行");
        onGenerationSubmittedRef.current?.();
      }
    } catch (error: any) {
      if (!isCurrentTaskSession(sessionId)) return;
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
      } else {
        addToast(error.message || "生图任务提交失败", "error");
      }
    } finally {
      if (isCurrentTaskSession(sessionId) && !submissionAccepted) {
        setIsGenerating(false);
        setTaskNo("");
        setTaskStatus("");
        setProgress(0);
        setPreviewUrl("");
        setDownloadUrl("");
        setPreviewSize(null);
        window.localStorage.removeItem(ACTIVE_TASK_KEY);
      }
    }
  };

  const handleImprovePrompt = async () => {
    if (!requireLogin("login")) return;
    if (!prompt.trim()) {
      addToast("请先填写创作描述", "info");
      return;
    }
    setIsHelping(true);
    setHelperDraft("");
    try {
      const reply = await chatWithAI(
        `请把下面这段画面描述改写成更适合 Qwen-Image 2.1 出图的中文提示词，保留主体与风格，只输出改写后的提示词：\n\n${prompt}`,
        [],
        selectedModel,
      );
      setHelperDraft(reply.trim());
    } catch (error: any) {
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
      } else {
        addToast(error.message || "AI 生成描述失败", "error");
      }
    } finally {
      setIsHelping(false);
    }
  };

  const isWorking = isGenerating || runningStatuses.includes(taskStatus);
  const hasTerminalState = terminalStatuses.includes(taskStatus) && !previewUrl;
  const isPreparingReference = generationMode === "image_to_image" && isUploadingReference;
  const referenceUploadPercent = referenceUploadProgress?.percent || 0;
  const hasReferenceImage = Boolean(referenceImageDataUrl || referenceImageName || referenceUploadToken);
  const comfyRunning = Boolean(comfyStatus?.running);
  const activeModel = comfyStatus?.models?.find((model) => model.id === comfyModel) || null;
  const stepsSelectable = activeModel?.steps_mode === "selectable";
  // 内存弹窗里的用量读数：内存 + 第一块显卡的显存（ComfyUI 的 system_stats）。
  const memoryUsageText = (() => {
    const stats = comfyStatus?.stats;
    if (!stats) return "";
    const parts: string[] = [];
    const ramUsed = Math.max(0, (stats.ram_total || 0) - (stats.ram_free || 0));
    if (ramUsed > 0) parts.push(`内存 ${(ramUsed / 1024 ** 3).toFixed(1)}G`);
    const device = stats.devices?.[0];
    const vramUsed = Math.max(0, (device?.vram_total || 0) - (device?.vram_free || 0));
    if (vramUsed > 0) parts.push(`显存 ${(vramUsed / 1024 ** 3).toFixed(1)}G`);
    return parts.length ? `当前占用 ${parts.join(" · ")}` : "";
  })();
  const aspectRatio = getAspectRatioFromSizeCode(sizeCodeFromRatio(ratio, resolutionCode));
  const frameMaxWidthClass = aspectRatio >= 1.15 ? "max-w-4xl" : aspectRatio <= 0.88 ? "max-w-xl" : "max-w-2xl";
  const frameStyle = previewSize
    ? { aspectRatio: `${previewSize.width} / ${previewSize.height}`, maxHeight: "62vh" }
    : aspectRatio
      ? { aspectRatio: `${aspectRatio}`, maxHeight: "62vh" }
      : undefined;

  return (
    <div className="bg-[#f7f7f4] lg:h-[calc(100vh-4rem)] lg:overflow-hidden">
      <div className="grid grid-cols-1 lg:h-full lg:grid-cols-[360px_minmax(0,1fr)]">
        <aside className="order-2 flex flex-col border-t border-black/5 bg-white/85 p-4 lg:order-1 lg:h-full lg:min-h-0 lg:border-r lg:border-t-0">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <div className="text-[11px] font-black uppercase tracking-[0.3em] text-gray-400">COMFYUI STUDIO</div>
              <h1 className="mt-1.5 text-[1.55rem] font-black tracking-tight text-gray-950 sm:text-[1.9rem]">
                {generationMode === "image_to_image" ? "Comfy 图生图" : "Comfy 文生图"}
              </h1>
            </div>
            <button
              type="button"
              onClick={() => setShowSettings(true)}
              className="inline-flex items-center gap-1.5 rounded-2xl border border-gray-100 bg-white px-3 py-2 text-[11px] font-bold text-gray-600 shadow-sm transition hover:border-gray-300 hover:text-black"
            >
              <Settings2 className="h-3.5 w-3.5" />
              Comfy 设置
            </button>
          </div>

          {!comfyRunning && (
            <div className="mb-3 flex items-start gap-2.5 rounded-[1.4rem] border border-amber-100 bg-amber-50 px-3.5 py-3">
              <span className="mt-1 h-2 w-2 shrink-0 rounded-full bg-amber-400" />
              <div className="min-w-0 flex-1">
                <div className="text-[12px] font-black text-amber-800">ComfyUI 未运行</div>
                <p className="mt-0.5 text-[11px] leading-5 text-amber-700">
                  {isCheckingComfy ? "正在探测本机 ComfyUI…" : `在 ${comfyStatus?.base_url || "http://127.0.0.1:8188"} 上没有检测到服务。`}
                </p>
              </div>
              <button
                type="button"
                onClick={() => void refreshComfyStatus()}
                className="inline-flex h-8 shrink-0 items-center gap-1 rounded-xl bg-white px-2.5 text-[11px] font-bold text-amber-800 shadow-sm transition hover:text-black"
                title="重新探测 ComfyUI"
              >
                {isCheckingComfy ? <Loader2 className="h-3 w-3 animate-spin" /> : <RefreshCcw className="h-3 w-3" />}
              </button>
              <button
                type="button"
                onClick={() => setShowSettings(true)}
                className="inline-flex h-8 shrink-0 items-center gap-1 rounded-xl bg-white px-3 text-[11px] font-bold text-amber-800 shadow-sm transition hover:text-black"
              >
                <Play className="h-3 w-3" />
                去启动
              </button>
            </div>
          )}

          <div className="space-y-3 pb-28 pr-0 md:pb-24 lg:flex-1 lg:min-h-0 lg:overflow-y-auto lg:pb-4 lg:pr-1">
            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-3.5 shadow-sm">
              <div className="mb-2 text-sm font-black text-gray-950">创作模式</div>
              <div className="grid grid-cols-2 gap-2">
                {[
                  { value: "text_to_image", label: "文生图", note: "从描述直接生成" },
                  { value: "image_to_image", label: "图生图", note: "参考图接入编码器" },
                ].map((item) => (
                  <button
                    key={item.value}
                    type="button"
                    onClick={() => setGenerationMode(item.value as GenerationMode)}
                    className={`rounded-2xl border px-3 py-3 text-left transition ${
                      generationMode === item.value
                        ? "border-black bg-black text-white shadow-lg shadow-black/10"
                        : "border-gray-100 bg-gray-50 text-gray-600 hover:border-gray-200 hover:bg-white"
                    }`}
                  >
                    <span className="block text-[13px] font-black">{item.label}</span>
                    <span className={`mt-1 block text-[11px] ${generationMode === item.value ? "text-white/60" : "text-gray-400"}`}>
                      {item.note}
                    </span>
                  </button>
                ))}
              </div>
            </section>

            {generationMode === "image_to_image" && (
              <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
                <div className="mb-3 flex items-center justify-between">
                  <div>
                    <div className="text-sm font-black text-gray-950">参考图</div>
                    <div className="mt-1 text-xs text-gray-400">支持 PNG / JPG / WEBP；出图比例跟随参考图</div>
                  </div>
                  {hasReferenceImage && (
                    <button
                      type="button"
                      onClick={clearReferenceImage}
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
                  accept="image/png,image/jpeg,image/webp"
                  onChange={handleReferenceImageChange}
                  className="hidden"
                />
                {hasReferenceImage ? (
                  <div className="rounded-[1.4rem] border border-gray-100 bg-gray-50/80 p-3">
                    <div className="flex items-center gap-3">
                      {referenceImageDataUrl ? (
                        <img src={referenceImageDataUrl} alt="参考图预览" className="h-20 w-20 rounded-2xl object-cover shadow-sm" />
                      ) : (
                        <div className="grid h-20 w-20 place-items-center rounded-2xl bg-white shadow-sm">
                          <ImageIcon className="h-7 w-7 text-gray-400" />
                        </div>
                      )}
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-bold text-gray-900">{referenceImageName || "已上传参考图"}</div>
                        <div className="mt-1 text-xs leading-5 text-gray-400">
                          {isUploadingReference
                            ? `参考图正在准备${referenceUploadPercent ? ` ${referenceUploadPercent}%` : ""}，点击开始生成时会自动等待完成。`
                            : referenceUploadToken
                              ? "参考图已完整上传，开始生成时会直接复用上传结果。"
                              : referenceImageDataUrl
                                ? "参考图已就绪，开始生成时会继续完成提交。"
                                : "该任务的参考图已提交，当前页面仅保留文件名。"}
                        </div>
                        {isUploadingReference && (
                          <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-white">
                            <div className="h-full rounded-full bg-black transition-all" style={{ width: `${Math.max(referenceUploadPercent, 6)}%` }} />
                          </div>
                        )}
                        <div className="mt-1 text-xs leading-5 text-gray-400">
                          {referenceImageDataUrl ? "也可以直接 `Command + V` 粘贴图片替换它。" : "当前页面不会回填完整参考图内容。"}
                        </div>
                      </div>
                    </div>
                  </div>
                ) : (
                  <button
                    type="button"
                    onClick={() => referenceInputRef.current?.click()}
                    className="flex w-full items-center gap-3 rounded-[1.4rem] border border-dashed border-gray-200 bg-gray-50/70 px-4 py-5 text-left transition hover:border-gray-300 hover:bg-white"
                  >
                    <div className="grid h-12 w-12 shrink-0 place-items-center rounded-2xl bg-white shadow-sm">
                      <ImageUp className="h-5 w-5 text-gray-400" />
                    </div>
                    <div className="min-w-0">
                      <div className="text-sm font-black text-gray-900">上传一张参考图</div>
                      <div className="mt-1 text-xs leading-5 text-gray-400">把参考图接到 Qwen 编码器，做重绘与风格迁移。</div>
                      <div className="mt-1 text-xs leading-5 text-gray-400">支持直接粘贴截图或剪贴板里的图片。</div>
                    </div>
                  </button>
                )}
              </section>
            )}

            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
              <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                <label className="text-sm font-black text-gray-950">创作描述</label>
                <button
                  type="button"
                  onClick={handleImprovePrompt}
                  disabled={isHelping}
                  className="inline-flex h-8 cursor-pointer items-center gap-2 rounded-xl border border-gray-100 px-3 text-[11px] font-bold text-gray-600 transition hover:border-gray-300 hover:text-black disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {isHelping ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Wand2 className="h-3.5 w-3.5" />}
                  AI 生成描述
                </button>
              </div>
              <textarea
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                className="min-h-24 w-full resize-none rounded-2xl border border-gray-100 bg-gray-50/80 p-4 text-sm leading-6 text-gray-800 outline-none transition focus:border-black/20 focus:bg-white focus:ring-4 focus:ring-black/5"
                placeholder="描述主体、环境、光线、风格、镜头语言..."
              />
              {!prompt && (
                <div className="mt-3 rounded-2xl border border-dashed border-gray-200 bg-gray-50/70 p-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="mb-1 text-[10px] font-black uppercase tracking-[0.18em] text-gray-400">示例描述</div>
                      <p className="line-clamp-2 text-[12px] leading-5 text-gray-600">{EXAMPLE_PROMPT}</p>
                    </div>
                    <button
                      type="button"
                      onClick={() => setPrompt(EXAMPLE_PROMPT)}
                      className="h-8 shrink-0 rounded-xl bg-white px-3 text-[11px] font-bold text-gray-700 shadow-sm transition hover:text-black"
                    >
                      使用
                    </button>
                  </div>
                </div>
              )}
            </section>

            {helperDraft && (
              <motion.section
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                className="rounded-[1.4rem] border border-amber-100 bg-amber-50 px-4 py-3"
              >
                <div className="mb-2 flex items-center justify-between">
                  <div className="inline-flex items-center gap-2 text-[11px] font-black text-amber-700">
                    <Sparkles className="h-3.5 w-3.5" />
                    LLM 辅助提示词
                  </div>
                  <button
                    type="button"
                    onClick={() => setPrompt(helperDraft)}
                    className="text-[11px] font-bold text-amber-800 underline-offset-4 hover:underline"
                  >
                    应用
                  </button>
                </div>
                <p className="line-clamp-3 text-[13px] leading-6 text-amber-900">{helperDraft}</p>
              </motion.section>
            )}

            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-3.5 shadow-sm">
              <button
                type="button"
                onClick={() => setNegativePrompt((value) => value || "畸形手指，低清晰度，水印，文字，过曝，模糊")}
                className="flex w-full cursor-pointer items-center justify-between text-left text-sm font-bold text-gray-800"
              >
                不希望呈现的内容（非必填）
                <ArrowRight className="h-4 w-4 text-gray-300" />
              </button>
              {negativePrompt && (
                <textarea
                  value={negativePrompt}
                  onChange={(event) => setNegativePrompt(event.target.value)}
                  className="mt-3 min-h-16 w-full resize-none rounded-2xl border border-gray-100 bg-gray-50 p-3 text-sm outline-none focus:border-black/20 focus:ring-4 focus:ring-black/5"
                />
              )}
            </section>

            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-3.5 shadow-sm">
              <div className="mb-3 flex items-center gap-2 text-sm font-black text-gray-950">
                <Layers className="h-4 w-4 text-gray-400" />
                Comfy 模型
              </div>
              <div className="space-y-2">
                {(comfyStatus?.models || []).map((model) => (
                  <button
                    key={model.id}
                    type="button"
                    onClick={() => {
                      setComfyModel(model.id);
                      if (model.steps_mode === "selectable" && model.default_steps) setSteps(model.default_steps);
                    }}
                    className={`w-full rounded-2xl border px-3 py-2.5 text-left transition ${
                      comfyModel === model.id
                        ? "border-black bg-black text-white shadow-lg shadow-black/10"
                        : "border-gray-100 bg-gray-50 text-gray-600 hover:border-gray-200 hover:bg-white"
                    }`}
                  >
                    <span className="block text-[13px] font-black">{model.label}</span>
                    <span className={`mt-1 block text-[11px] ${comfyModel === model.id ? "text-white/60" : "text-gray-400"}`}>
                      {model.note}
                    </span>
                  </button>
                ))}
                {!comfyStatus?.models?.length && (
                  <div className="rounded-2xl border border-dashed border-gray-200 bg-gray-50/70 px-3 py-4 text-center text-[11px] text-gray-400">
                    模型清单来自本地服务，正在加载…
                  </div>
                )}
              </div>

              {stepsSelectable && (
                <div className="mt-3">
                  <div className="mb-2 text-xs font-black text-gray-400">步数</div>
                  <div className="grid grid-cols-2 gap-2">
                    {[activeModel?.default_steps || 25, activeModel?.max_steps || 35].map((value) => (
                      <button
                        key={value}
                        type="button"
                        onClick={() => setSteps(value)}
                        className={`h-9 rounded-2xl text-xs font-black transition ${
                          steps === value ? "bg-black text-white shadow-lg shadow-black/10" : "bg-gray-50 text-gray-500 hover:bg-gray-100 hover:text-black"
                        }`}
                      >
                        {value} 步
                      </button>
                    ))}
                  </div>
                </div>
              )}

              <div className="mt-3">
                <div className="mb-2 text-xs font-black text-gray-400">种子</div>
                <div className="grid grid-cols-2 gap-2">
                  {[
                    { value: "random", label: "随机" },
                    { value: "fixed", label: "固定" },
                  ].map((item) => (
                    <button
                      key={item.value}
                      type="button"
                      onClick={() => setSeedMode(item.value as SeedMode)}
                      className={`h-9 rounded-2xl text-xs font-black transition ${
                        seedMode === item.value ? "bg-black text-white shadow-lg shadow-black/10" : "bg-gray-50 text-gray-500 hover:bg-gray-100 hover:text-black"
                      }`}
                    >
                      {item.label}
                    </button>
                  ))}
                </div>
                {seedMode === "fixed" ? (
                  <input
                    type="text"
                    inputMode="numeric"
                    value={seedValue}
                    onChange={(event) => handleSeedValueChange(event.target.value)}
                    placeholder="输入正整数种子"
                    className="mt-2 h-9 w-full rounded-2xl border border-gray-100 bg-gray-50 px-3 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20 focus:bg-white"
                  />
                ) : (
                  <p className="mt-2 text-[11px] leading-5 text-gray-400">
                    {lastSeed ? `上次使用种子 ${lastSeed}，切到「固定」填同一个种子可以复现。` : "每次随机；想复现同一张图时切到「固定」并填入上次的种子。"}
                  </p>
                )}
              </div>
            </section>

            <section className="rounded-[1.6rem] border border-gray-100 bg-white p-3.5 shadow-sm">
              <div className="space-y-3">
                <div>
                  <div className="mb-2 text-xs font-black text-gray-400">画面比例</div>
                  <div className="grid grid-cols-4 gap-2">
                    {ratioPresets.map((item) => {
                      const itemRatio = normalizeRatio(item.width, item.height);
                      const selected = itemRatio.width === ratio.width && itemRatio.height === ratio.height;
                      return (
                        <button
                          key={item.label}
                          type="button"
                          onClick={() => handlePresetRatioSelect(item)}
                          className={`h-9 rounded-2xl text-xs font-black transition ${
                            selected ? "bg-black text-white shadow-lg shadow-black/10" : "bg-gray-50 text-gray-500 hover:bg-gray-100 hover:text-black"
                          }`}
                        >
                          {item.label}
                        </button>
                      );
                    })}
                  </div>
                  <div className="mt-2 flex items-center gap-2 rounded-2xl bg-gray-50 p-2">
                    <span className="shrink-0 px-1 text-[11px] font-black text-gray-400">自定义</span>
                    <input
                      type="text"
                      inputMode="numeric"
                      value={customRatioWidth}
                      onChange={(event) => handleCustomRatioChange("width", event.target.value)}
                      className="h-8 min-w-0 flex-1 rounded-xl border border-gray-100 bg-white px-2 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20"
                    />
                    <span className="text-xs font-black text-gray-300">:</span>
                    <input
                      type="text"
                      inputMode="numeric"
                      value={customRatioHeight}
                      onChange={(event) => handleCustomRatioChange("height", event.target.value)}
                      className="h-8 min-w-0 flex-1 rounded-xl border border-gray-100 bg-white px-2 text-center text-xs font-black text-gray-800 outline-none focus:border-black/20"
                    />
                  </div>
                </div>

                <div>
                  <div className="mb-2 text-xs font-black text-gray-400">分辨率</div>
                  <div className="grid grid-cols-2 gap-2">
                    {resolutionOptions.map((item) => (
                      <button
                        key={item.value}
                        type="button"
                        onClick={() => setResolutionCode(item.value as ResolutionCode)}
                        className={`h-9 rounded-2xl text-xs font-black transition ${
                          item.value === resolutionCode ? "bg-black text-white shadow-lg shadow-black/10" : "bg-gray-50 text-gray-500 hover:bg-gray-100 hover:text-black"
                        }`}
                      >
                        {item.label}
                      </button>
                    ))}
                  </div>
                  <p className="mt-2 text-[11px] leading-5 text-gray-400">
                    {generationMode === "image_to_image"
                      ? "图生图出图比例跟随参考图，这里的分辨率只决定参考图的像素预算。"
                      : `实际出图尺寸 ${sizeCodeFromRatio(ratio, resolutionCode)}（按 32 对齐）。`}
                  </p>
                </div>
              </div>
            </section>

            {issueReport && (taskStatus === "failed" || taskStatus === "canceled") && (
              <section className="rounded-[1.6rem] border border-red-100 bg-red-50/60 p-3.5">
                <div className="flex items-center justify-between gap-2">
                  <div className="text-[12px] font-black text-red-700">任务失败 · ComfyUI 侧日志</div>
                  <button
                    type="button"
                    onClick={() => setShowSettings(true)}
                    className="inline-flex h-7 items-center gap-1 rounded-xl bg-white px-2.5 text-[11px] font-bold text-red-700 shadow-sm transition hover:text-black"
                  >
                    <Settings2 className="h-3 w-3" />
                    查看详情
                  </button>
                </div>
                {!issueReport.available ? (
                  <p className="mt-2 text-[11px] leading-5 text-red-700/80">
                    读取 ComfyUI 日志失败{issueReport.error ? `：${issueReport.error}` : "（服务可能已停止）"}。
                  </p>
                ) : issueReport.issues.length ? (
                  <ul className="mt-2 space-y-1.5">
                    {issueReport.issues.slice(-3).map((issue, index) => (
                      <li key={`${issue.at}-${index}`} className="text-[11px] leading-5 text-red-800">
                        <span className="mr-1 font-black uppercase">{issue.level === "error" ? "[错误]" : "[警告]"}</span>
                        <span className="break-words">{issue.message}</span>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="mt-2 text-[11px] leading-5 text-red-700/80">
                    ComfyUI 日志里没有 ERROR / WARNING；失败原因见任务错误信息（例如连不上 ComfyUI 或参数不被接受）。
                  </p>
                )}
              </section>
            )}
          </div>

          <div className="sticky bottom-[calc(env(safe-area-inset-bottom)+5.25rem)] z-20 mt-4 rounded-[1.6rem] border border-gray-100 bg-white/95 p-3 shadow-[0_18px_40px_rgba(15,23,42,0.08)] backdrop-blur md:bottom-4 lg:static lg:mt-4 lg:shrink-0">
            {(isWorking || progress > 0) && (
              <div className="mb-3 rounded-2xl border border-gray-100 bg-gray-50 p-3">
                <div className="mb-2 flex items-center justify-between text-[11px] font-bold text-gray-500">
                  <span>{previewUrl ? "生成完成" : isWorking ? "正在生成" : "任务进度"}</span>
                  <span>{Math.round(progress)}%</span>
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
              onClick={handleGenerate}
              disabled={isWorking}
              className="flex h-12 w-full cursor-pointer items-center justify-center gap-3 rounded-2xl bg-[#6d5dfc] text-sm font-black text-white shadow-xl shadow-[#6d5dfc]/20 transition hover:-translate-y-0.5 hover:bg-[#5b4cf2] disabled:cursor-not-allowed disabled:opacity-70"
            >
              {isWorking || isPreparingReference ? <Loader2 className="h-5 w-5 animate-spin" /> : <Sparkles className="h-5 w-5" />}
              {isWorking ? "生成中" : isPreparingReference ? "参考图上传中" : "立即生成"}
              <span className="rounded-full bg-white/15 px-2 py-0.5 text-xs">1 次</span>
            </button>
          </div>
        </aside>

        <main className="order-1 p-4 pb-2 lg:order-2 lg:h-full lg:min-h-0 lg:p-4">
          <div className="relative mx-auto flex min-h-[340px] max-w-5xl flex-col lg:h-full lg:min-h-0">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-sm text-gray-500">
              <div className="text-[11px] font-bold uppercase tracking-[0.2em] text-gray-400">
                {previewUrl ? "结果已同步" : isWorking ? "ComfyUI 生成中" : "实时预览"}
              </div>
              <div className="flex items-center gap-2">
                <span className="inline-flex items-center gap-1.5 rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-500">
                  <span className={`h-2 w-2 rounded-full ${comfyRunning ? "bg-emerald-400" : "bg-gray-300"}`} />
                  {comfyRunning ? "ComfyUI 已连接" : "ComfyUI 未连接"}
                </span>
                {comfyRunning && !isWorking && (
                  <button
                    type="button"
                    onClick={handleFreeMemory}
                    disabled={isReleasingMemory}
                    className="inline-flex items-center gap-2 rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-500 hover:text-black disabled:cursor-not-allowed disabled:opacity-60"
                    title="让 ComfyUI 卸载模型、归还内存（下次出图会重新加载）"
                  >
                    {isReleasingMemory ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <MemoryStick className="h-3.5 w-3.5" />}
                    {isReleasingMemory ? "释放中" : "释放内存"}
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => prompt && navigator.clipboard.writeText(prompt)}
                  className="inline-flex items-center gap-2 rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-500 hover:text-black"
                >
                  <Copy className="h-3.5 w-3.5" />
                  复制描述
                </button>
              </div>
            </div>

            <div className="grid min-h-[300px] flex-1 place-items-center rounded-[2rem] border border-black/5 bg-white/70 p-4 shadow-sm backdrop-blur sm:min-h-[380px] lg:min-h-0">
              <div className={`w-full ${frameMaxWidthClass}`}>
                <div
                  className="relative mx-auto flex max-h-[62vh] w-full items-center justify-center overflow-hidden rounded-[1.5rem] border border-gray-100 bg-gray-50 shadow-2xl"
                  style={frameStyle}
                >
                  {previewUrl ? (
                    <div className="flex h-full w-full items-center justify-center bg-[radial-gradient(circle_at_20%_20%,#ffffff,transparent_30%),linear-gradient(135deg,#f8fafc,#eceff3)] p-3 sm:p-4">
                      <img src={previewUrl} alt="生成结果" className="max-h-full max-w-full rounded-[1.15rem] object-contain shadow-[0_18px_50px_rgba(15,23,42,0.12)]" />
                    </div>
                  ) : (
                    <div className="flex h-full flex-col items-center justify-center bg-[radial-gradient(circle_at_30%_20%,#ffffff,transparent_32%),linear-gradient(135deg,#f8fafc,#eceff3)] text-center">
                      {isRestoringTask ? (
                        <>
                          <Loader2 className="h-10 w-10 animate-spin text-gray-400" />
                          <p className="mt-4 text-sm font-bold text-gray-700">正在打开历史任务</p>
                          <p className="mt-1 px-4 text-xs text-gray-400">请稍候</p>
                        </>
                      ) : isWorking ? (
                        <>
                          <Loader2 className="h-10 w-10 animate-spin text-gray-400" />
                          <p className="mt-4 text-sm font-bold text-gray-700">ComfyUI 正在生成画面</p>
                          <p className="mt-1 px-4 text-xs text-gray-400">
                            任务 {taskNo || "提交中"} · {taskStatus || "queued"}
                            {elapsedSeconds > 0 ? ` · 已用 ${formatElapsed(elapsedSeconds)}` : ""}
                          </p>
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
                      ) : !comfyRunning ? (
                        <>
                          <div className="grid h-20 w-20 place-items-center rounded-[1.5rem] bg-white shadow-sm">
                            <Play className="h-8 w-8 text-gray-300" />
                          </div>
                          <p className="mt-4 text-sm font-bold text-gray-700">先启动本机 ComfyUI</p>
                          <p className="mt-1 max-w-md px-6 text-xs leading-6 text-gray-400">
                            板块只负责调用，不会自动拉起进程；点击「Comfy 设置」里的启动按钮即可。
                          </p>
                          <button
                            type="button"
                            onClick={() => setShowSettings(true)}
                            className="mt-4 inline-flex h-9 items-center gap-1.5 rounded-xl bg-black px-4 text-[11px] font-bold text-white shadow-lg shadow-black/10 transition hover:opacity-90"
                          >
                            <Play className="h-3.5 w-3.5" />
                            打开 Comfy 设置
                          </button>
                        </>
                      ) : hasTerminalState ? (
                        <>
                          <div className="grid h-20 w-20 place-items-center rounded-[1.5rem] bg-white shadow-sm">
                            <RefreshCcw className="h-9 w-9 text-gray-300" />
                          </div>
                          <p className="mt-4 text-sm font-bold text-gray-700">
                            {taskStatus === "failed" ? "任务执行失败" : taskStatus === "canceled" ? "任务已取消" : "图片已生成，等待结果同步"}
                          </p>
                          <p className="mt-1 max-w-md px-6 text-xs leading-6 text-gray-400">
                            {taskStatus === "failed"
                              ? "这次提交没有拿到可展示图片，失败原因见左下方提示与历史记录。"
                              : taskStatus === "canceled"
                                ? "该任务已经被终止，不会继续向 ComfyUI 轮询。"
                                : "任务状态已完成，但图片文件仍在同步中，请稍候。"}
                          </p>
                        </>
                      ) : (
                        <>
                          <div className="grid h-20 w-20 place-items-center rounded-[1.5rem] bg-white shadow-sm">
                            {generationMode === "image_to_image" ? <ImageUp className="h-9 w-9 text-gray-300" /> : <ImageIcon className="h-9 w-9 text-gray-300" />}
                          </div>
                          <p className="mt-4 text-sm font-bold text-gray-700">结果会显示在这里</p>
                          <p className="mt-1 text-xs text-gray-400">
                            {generationMode === "image_to_image" ? "上传参考图并填写描述后开始生成" : "左侧输入描述后点击立即生成"}
                          </p>
                        </>
                      )}
                    </div>
                  )}
                </div>

                <div className="mt-5 flex flex-col gap-3 text-xs text-gray-400 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex items-center gap-2">
                    {previewUrl ? <CheckCircle2 className="h-4 w-4 text-emerald-500" /> : <RefreshCcw className="h-4 w-4" />}
                    <span>任务状态：{taskStatus || "待提交"} · {Math.round(progress)}%</span>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    {previewUrl && (
                      <a
                        href={downloadUrl || previewUrl}
                        className="rounded-xl border border-gray-100 bg-white px-3 py-2 text-xs font-bold text-gray-600 transition hover:border-gray-200 hover:text-black"
                      >
                        下载原图
                      </a>
                    )}
                    <span>{taskNo ? `任务号：${taskNo}` : "生成后自动记录到历史"}</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </main>
      </div>

      <AnimatePresence>
        {showSettings && (
          <ComfySettingsModal
            onClose={() => setShowSettings(false)}
            addToast={addToast}
            onStatusChange={setComfyStatus}
          />
        )}
        {showMemoryPrompt && (
          <motion.div
            key="comfy-memory-prompt"
            className="fixed inset-0 z-[70] flex items-center justify-center bg-black/25 p-4 backdrop-blur-sm"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            onClick={() => setShowMemoryPrompt(false)}
          >
            <motion.div
              initial={{ scale: 0.96, y: 12 }}
              animate={{ scale: 1, y: 0 }}
              exit={{ scale: 0.96, opacity: 0 }}
              transition={{ type: "spring", stiffness: 320, damping: 28 }}
              onClick={(event) => event.stopPropagation()}
              className="w-full max-w-md rounded-[1.6rem] border border-white/60 bg-white p-5 shadow-[0_30px_80px_rgba(15,23,42,0.28)]"
            >
              <div className="flex items-start gap-3">
                <div className="grid h-11 w-11 shrink-0 place-items-center rounded-2xl bg-amber-50 text-amber-500">
                  <MemoryStick className="h-5 w-5" />
                </div>
                <div className="min-w-0">
                  <p className="text-sm font-black text-gray-900">出图完成，模型还在内存里</p>
                  <p className="mt-1 text-[11px] font-bold text-gray-400">{memoryUsageText || "模型与文本编码器仍处于常驻状态"}</p>
                </div>
              </div>
              <p className="mt-3 text-xs leading-6 text-gray-500">
                主模型 + 文本编码器会一直驻留（约 16G）。继续生图时保持常驻最省时间；
                暂时不出图就释放，把内存和显存还回系统（下次出图会自动重新加载）。
              </p>
              <div className="mt-4 flex flex-col gap-2 sm:flex-row-reverse">
                <button
                  type="button"
                  onClick={handleFreeMemory}
                  disabled={isReleasingMemory}
                  className="flex h-10 flex-1 cursor-pointer items-center justify-center gap-2 rounded-2xl bg-[#6d5dfc] text-xs font-black text-white shadow-lg shadow-[#6d5dfc]/20 transition hover:-translate-y-0.5 hover:bg-[#5b4cf2] disabled:cursor-not-allowed disabled:opacity-70"
                >
                  {isReleasingMemory ? <Loader2 className="h-4 w-4 animate-spin" /> : <MemoryStick className="h-4 w-4" />}
                  {isReleasingMemory ? "释放中" : "释放内存"}
                </button>
                <button
                  type="button"
                  onClick={handleKeepResident}
                  className="flex h-10 flex-1 cursor-pointer items-center justify-center rounded-2xl border border-gray-100 bg-white text-xs font-black text-gray-600 transition hover:border-gray-200 hover:text-black"
                >
                  保持常驻（本次会话不再提醒）
                </button>
              </div>
            </motion.div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
