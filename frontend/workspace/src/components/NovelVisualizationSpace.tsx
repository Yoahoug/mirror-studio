import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { motion } from "motion/react";
import {
  AlertTriangle,
  BookOpen,
  CheckCircle2,
  Clock,
  Download,
  Film,
  Image as ImageIcon,
  Loader2,
  Play,
  RefreshCcw,
  Sparkles,
  UploadCloud,
  X,
  XCircle,
} from "lucide-react";
import {
  AuthRequiredError,
  createNovelTask,
  fetchNovelBooks,
  fetchNovelTaskDetail,
  fetchNovelTasks,
  retryNovelTask,
  cancelUploadSession,
  uploadFileResumable,
  type ChatModel,
  type NovelBook,
  type NovelSceneImage,
  type NovelVisualizationTask,
  type UploadProgress,
} from "../services/apiService";

interface NovelVisualizationSpaceProps {
  isAuthenticated: boolean;
  selectedModel?: string;
  chatModels?: ChatModel[];
  resetSignal?: number;
  onOpenAuth: (mode: "login" | "register") => void;
  addToast: (message: string, type?: "success" | "error" | "info") => void;
}

type NovelLog = {
  id: number;
  type: string;
  message: string;
  createdAt: string;
};

const terminalStatuses = ["succeeded", "failed", "canceled"];
const retryableStatuses = ["failed", "canceled"];
const MAX_EPUB_UPLOAD_BYTES = 80 * 1024 * 1024;

const statusLabel: Record<string, string> = {
  queued: "排队中",
  running: "生成中",
  succeeded: "已完成",
  failed: "失败",
  canceled: "已取消",
  pending: "待生成",
};

function formatBytes(bytes: number) {
  if (!bytes) return "未知大小";
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function formatTime(value?: string | null) {
  if (!value) return "刚刚";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "未知时间";
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function normalizeTaskLogs(task: NovelVisualizationTask): NovelLog[] {
  return (task.logs || []).map((item) => ({
    id: item.id,
    type: item.type,
    message: String(item.payload?.message || item.type || "状态更新"),
    createdAt: item.created_at,
  }));
}

function mergeImages(current: NovelSceneImage[], incoming: NovelSceneImage[]) {
  const map = new Map<number, NovelSceneImage>();
  current.forEach((image) => map.set(image.scene_number, image));
  incoming.forEach((image) => map.set(image.scene_number, image));
  return Array.from(map.values()).sort((a, b) => a.scene_number - b.scene_number);
}

function progressColor(status: string) {
  if (status === "failed") return "bg-red-500";
  if (status === "succeeded") return "bg-emerald-400";
  return "bg-[#6d5dfc]";
}

function sceneStatusIcon(status: string) {
  if (status === "succeeded") return <CheckCircle2 className="h-4 w-4 text-emerald-500" />;
  if (status === "failed") return <XCircle className="h-4 w-4 text-red-500" />;
  if (status === "running") return <Loader2 className="h-4 w-4 animate-spin text-[#6d5dfc]" />;
  return <Clock className="h-4 w-4 text-gray-400" />;
}

function appendImageQuery(url = "", query: string) {
  if (!url) return "";
  return `${url}${url.includes("?") ? "&" : "?"}${query}`;
}

function scenePreviewUrl(scene: NovelSceneImage) {
  return scene.preview_url || appendImageQuery(scene.image_url, "preview=1") || scene.image_url || "";
}

function sceneFullUrl(scene: NovelSceneImage) {
  return scene.image_url || scene.download_url || scene.preview_url || "";
}

function sceneDownloadUrl(scene: NovelSceneImage) {
  return scene.download_url || appendImageQuery(scene.image_url, "download=1") || scene.image_url || "";
}

export default function NovelVisualizationSpace({
  isAuthenticated,
  selectedModel = "",
  chatModels = [],
  resetSignal = 0,
  onOpenAuth,
  addToast,
}: NovelVisualizationSpaceProps) {
  const [books, setBooks] = useState<NovelBook[]>([]);
  const [tasks, setTasks] = useState<NovelVisualizationTask[]>([]);
  const [selectedBookId, setSelectedBookId] = useState("");
  const [activeTask, setActiveTask] = useState<NovelVisualizationTask | null>(null);
  const [sceneImages, setSceneImages] = useState<NovelSceneImage[]>([]);
  const [logs, setLogs] = useState<NovelLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploadProgress, setUploadProgress] = useState<UploadProgress | null>(null);
  const [retrying, setRetrying] = useState(false);
  const [connected, setConnected] = useState(false);
  const [sceneMode, setSceneMode] = useState<"auto" | "targeted">("auto");
  const [targetPlot, setTargetPlot] = useState("");
  const [previewScene, setPreviewScene] = useState<NovelSceneImage | null>(null);
  const eventSourceRef = useRef<EventSource | null>(null);
  const lastEventIdRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const uploadAbortRef = useRef<AbortController | null>(null);
  const activeUploadIdRef = useRef("");

  const defaultBook = useMemo(() => {
    return books.find((book) => book.filename.includes("三体全集"))?.id || books[0]?.id || "";
  }, [books]);
  const selectedBook = useMemo(() => {
    return books.find((book) => book.id === selectedBookId) || null;
  }, [books, selectedBookId]);

  const completedCount = sceneImages.filter((image) => image.status === "succeeded").length;
  const failedCount = sceneImages.filter((image) => image.status === "failed").length;
  const running = activeTask ? !terminalStatuses.includes(activeTask.status) : false;
  const canRetryActiveTask = activeTask ? retryableStatuses.includes(activeTask.status) : false;
  const selectedChatModel = chatModels.find((model) => model.channel_code === selectedModel);
  const uploadPercent = uploadProgress?.percent ?? 0;

  const appendLog = (type: string, payload: Record<string, any>, id?: number) => {
    const nextId = id || Date.now();
    setLogs((current) => {
      if (current.some((item) => item.id === nextId)) return current;
      return [
        ...current,
        {
          id: nextId,
          type,
          message: String(payload.message || type),
          createdAt: new Date().toISOString(),
        },
      ].slice(-200);
    });
  };

  const applyTask = (task: NovelVisualizationTask) => {
    setActiveTask(task);
    setSceneImages(task.images || []);
    setLogs(normalizeTaskLogs(task));
    setPreviewScene(null);
    lastEventIdRef.current = Math.max(0, ...(task.logs || []).map((item) => item.id || 0));
  };

  const loadInitialData = async () => {
    if (!isAuthenticated) {
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const [bookItems, taskItems] = await Promise.all([fetchNovelBooks(), fetchNovelTasks()]);
      setBooks(bookItems);
      setTasks(taskItems);
      setSelectedBookId((current) => {
        if (current && bookItems.some((book) => book.id === current)) return current;
        return bookItems.find((book) => book.filename.includes("三体全集"))?.id || bookItems[0]?.id || "";
      });
      const latestRunning = taskItems.find((task) => !terminalStatuses.includes(task.status));
      if (latestRunning) {
        const detail = await fetchNovelTaskDetail(latestRunning.id);
        applyTask(detail);
        const lastEventId = Math.max(0, ...(detail.logs || []).map((item) => item.id || 0));
        connectEvents(detail.id, lastEventId);
      } else if (!activeTask && taskItems[0]) {
        applyTask(taskItems[0]);
      }
    } catch (error: any) {
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
      } else {
        addToast(error.message || "小说可视化数据加载失败", "error");
      }
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadInitialData();
    return () => {
      eventSourceRef.current?.close();
      uploadAbortRef.current?.abort();
    };
  }, [isAuthenticated]);

  useEffect(() => {
    if (!resetSignal) return;
    eventSourceRef.current?.close();
    setConnected(false);
    setActiveTask(null);
    setSceneImages([]);
    setLogs([]);
    setPreviewScene(null);
    setSceneMode("auto");
    setTargetPlot("");
    setUploadProgress(null);
    uploadAbortRef.current?.abort();
    uploadAbortRef.current = null;
    if (activeUploadIdRef.current) {
      void cancelUploadSession(activeUploadIdRef.current).catch(() => {});
      activeUploadIdRef.current = "";
    }
    lastEventIdRef.current = 0;
    void loadInitialData();
  }, [resetSignal]);

  useEffect(() => {
    if (!selectedBookId && defaultBook) {
      setSelectedBookId(defaultBook);
    }
  }, [defaultBook, selectedBookId]);

  const refreshHistory = async () => {
    try {
      setTasks(await fetchNovelTasks());
    } catch {
      // History refresh is best-effort after event updates.
    }
  };

  const connectEvents = (taskId: number, startAfterEventId = 0) => {
    eventSourceRef.current?.close();
    lastEventIdRef.current = startAfterEventId;
    const url = `/api/novelviz/tasks/${taskId}/events?last_event_id=${startAfterEventId}`;
    const source = new EventSource(url, { withCredentials: true });
    eventSourceRef.current = source;
    setConnected(true);

    const handleEvent = (event: MessageEvent, type: string) => {
      const eventId = Number(event.lastEventId || 0);
      if (eventId) lastEventIdRef.current = Math.max(lastEventIdRef.current, eventId);
      let payload: Record<string, any> = {};
      try {
        payload = JSON.parse(event.data || "{}");
      } catch {
        payload = { message: event.data || type };
      }
      appendLog(type, payload, eventId || undefined);
      if (payload.scene) {
        setSceneImages((current) => mergeImages(current, [payload.scene as NovelSceneImage]));
      }
      if (payload.task) {
        const task = payload.task as NovelVisualizationTask;
        setActiveTask(task);
        setSceneImages((current) => mergeImages(current, task.images || []));
      } else if (typeof payload.progress_percent === "number") {
        setActiveTask((current) => current ? { ...current, progress_percent: payload.progress_percent } : current);
      }
      if (payload.scenes && !payload.task) {
        setActiveTask((current) => current ? { ...current, scenes: payload.scenes } : current);
      }
      if (type === "task_done" || type === "task_failed") {
        if (!payload.task) {
          setActiveTask((current) => current ? {
            ...current,
            status: type === "task_done" ? "succeeded" : "failed",
            progress_percent: typeof payload.progress_percent === "number" ? payload.progress_percent : 100,
            error_message: type === "task_failed" ? String(payload.message || current.error_message || "") : current.error_message,
          } : current);
        }
        window.dispatchEvent(new CustomEvent("mirror:task-updated", {
          detail: {
            taskNo: activeTask?.id ? `novelviz:${activeTask.id}` : "",
            status: type === "task_done" ? "succeeded" : "failed",
            hasImage: Boolean(payload.task?.images?.length || payload.scene),
            progressPercent: typeof payload.progress_percent === "number" ? payload.progress_percent : 100,
          },
        }));
        source.close();
        setConnected(false);
        void refreshHistory();
      }
    };

    ["task_started", "log", "scene_plan_ready", "image_started", "image_done", "image_failed", "task_done", "task_failed"].forEach((type) => {
      source.addEventListener(type, (event) => handleEvent(event as MessageEvent, type));
    });
    source.onerror = () => {
      setConnected(false);
      if (!activeTask || terminalStatuses.includes(activeTask.status)) {
        source.close();
      }
    };
  };

  const submitTask = async () => {
    if (!isAuthenticated) {
      onOpenAuth("register");
      return;
    }
    if (!selectedBook) {
      addToast("请先选择一本 EPUB 小说", "info");
      return;
    }
    const cleanTargetPlot = targetPlot.trim();
    if (sceneMode === "targeted" && cleanTargetPlot.length < 4) {
      addToast("请填写想呈现的剧情", "info");
      return;
    }
    setSubmitting(true);
    setLogs([]);
    setSceneImages([]);
    try {
      const task = await createNovelTask(selectedBook, selectedModel, {
        sceneMode,
        targetPlot: sceneMode === "targeted" ? cleanTargetPlot : "",
      });
      applyTask(task);
      setTasks((current) => [task, ...current.filter((item) => item.id !== task.id)]);
      addToast(sceneMode === "targeted" ? "指定剧情单图任务已提交" : "小说可视化任务已提交");
      connectEvents(task.id, lastEventIdRef.current);
    } catch (error: any) {
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
      } else {
        addToast(error.message || "任务提交失败", "error");
      }
    } finally {
      setSubmitting(false);
    }
  };

  const retryActiveTask = async () => {
    if (!activeTask || !canRetryActiveTask) {
      addToast("当前任务还不能重试", "info");
      return;
    }
    setRetrying(true);
    eventSourceRef.current?.close();
    setConnected(false);
    try {
      const task = await retryNovelTask(activeTask.id);
      applyTask(task);
      setTasks((current) => [task, ...current.filter((item) => item.id !== task.id)]);
      window.dispatchEvent(new CustomEvent("mirror:task-updated", {
        detail: {
          taskNo: `novelviz:${task.id}`,
          status: task.status,
          hasImage: false,
          progressPercent: task.progress_percent,
        },
      }));
      addToast("小说可视化任务已重新提交");
      connectEvents(task.id, 0);
    } catch (error: any) {
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
      } else {
        addToast(error.message || "重试失败", "error");
      }
    } finally {
      setRetrying(false);
    }
  };

  const handleUploadFile = async (file?: File | null) => {
    if (!file) return;
    if (!isAuthenticated) {
      onOpenAuth("login");
      return;
    }
    if (!file.name.toLowerCase().endsWith(".epub")) {
      addToast("请上传 EPUB 格式的小说文件", "error");
      return;
    }
    if (file.size > MAX_EPUB_UPLOAD_BYTES) {
      addToast("EPUB 文件不能超过 80 MB", "error");
      return;
    }
    setUploading(true);
    setUploadProgress(null);
    uploadAbortRef.current?.abort();
    if (activeUploadIdRef.current) {
      void cancelUploadSession(activeUploadIdRef.current).catch(() => {});
      activeUploadIdRef.current = "";
    }
    const abortController = new AbortController();
    uploadAbortRef.current = abortController;
    try {
      const { result: uploaded } = await uploadFileResumable("novel_epub", file, {
        signal: abortController.signal,
        onSession: (session) => {
          activeUploadIdRef.current = session.upload_id;
        },
        onProgress: setUploadProgress,
      });
      setBooks((current) => [uploaded, ...current.filter((book) => book.id !== uploaded.id)]);
      setSelectedBookId(uploaded.id);
      addToast("EPUB 已上传，可以开始可视化");
    } catch (error: any) {
      if (error instanceof AuthRequiredError) {
        onOpenAuth("login");
      } else {
        addToast(error.name === "AbortError" ? "EPUB 上传已取消" : error.message || "EPUB 上传失败", "error");
      }
    } finally {
      setUploading(false);
      uploadAbortRef.current = null;
      activeUploadIdRef.current = "";
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    }
  };

  const openTask = async (taskId: number) => {
    try {
      const task = await fetchNovelTaskDetail(taskId);
      applyTask(task);
      if (!terminalStatuses.includes(task.status)) {
        connectEvents(task.id, lastEventIdRef.current);
      } else {
        eventSourceRef.current?.close();
        setConnected(false);
      }
    } catch (error: any) {
      addToast(error.message || "任务打开失败", "error");
    }
  };

  if (!isAuthenticated) {
    return (
      <div className="flex min-h-[calc(100vh-4rem)] items-center justify-center bg-[#f7f7f4] p-6 text-gray-950">
        <div className="max-w-md text-center">
          <div className="mx-auto mb-4 grid h-14 w-14 place-items-center rounded-[1.4rem] bg-white shadow-sm">
            <Film className="h-7 w-7 text-gray-900" />
          </div>
          <h1 className="text-2xl font-bold">小说场景可视化</h1>
          <p className="mt-3 text-sm leading-6 text-gray-500">登录后上传 EPUB，自动分析震撼场景并生成电影级概念图。</p>
          <button
            type="button"
            onClick={() => onOpenAuth("login")}
            className="mt-6 inline-flex items-center gap-2 rounded-2xl bg-black px-5 py-3 text-sm font-bold text-white shadow-lg shadow-black/10 transition hover:bg-gray-800"
          >
            <Sparkles className="h-4 w-4" />
            登录后开始
          </button>
        </div>
      </div>
    );
  }

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      className="bg-[#f7f7f4] text-gray-950 lg:min-h-[calc(100vh-4rem)]"
    >
      <div className="grid grid-cols-1 lg:min-h-[calc(100vh-4rem)] lg:grid-cols-[340px_minmax(0,1fr)]">
        <aside className="order-2 flex flex-col border-t border-black/5 bg-white/85 p-4 lg:order-1 lg:min-h-0 lg:border-r lg:border-t-0">
          <div className="mb-4 flex items-center justify-between">
            <div>
              <div className="text-[11px] font-black uppercase tracking-[0.3em] text-gray-400">Novel Cinema</div>
              <h1 className="mt-1.5 text-[1.55rem] font-black tracking-tight text-gray-950 sm:text-[1.9rem]">
                小说场景可视化
              </h1>
            </div>
            <div className="rounded-2xl bg-black px-3 py-1.5 text-[10px] font-bold text-white sm:text-[11px]">实验模块</div>
          </div>

          <div className="space-y-3 pb-28 pr-0 md:pb-24 lg:flex-1 lg:min-h-0 lg:overflow-y-auto lg:pb-4 lg:pr-1">
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="flex items-center gap-2 text-gray-900">
              <BookOpen className="h-5 w-5" />
              <h2 className="text-sm font-black">小说输入</h2>
            </div>
            <p className="mt-3 text-sm leading-6 text-gray-500">
              可自动挑选全书震撼场景，也可以指定一个剧情，让 AI 结合全文只生成一张关键画面。
            </p>
            <div className="mt-5 space-y-3">
              <label className="text-xs font-black uppercase tracking-[0.18em] text-gray-400">EPUB 小说</label>
              <input
                ref={fileInputRef}
                type="file"
                accept=".epub,application/epub+zip"
                className="hidden"
                onChange={(event) => void handleUploadFile(event.target.files?.[0])}
              />
              <button
                type="button"
                onClick={() => fileInputRef.current?.click()}
                disabled={uploading || running}
                className="flex w-full items-center gap-3 rounded-[1.4rem] border border-dashed border-gray-200 bg-gray-50/70 px-4 py-4 text-left transition hover:border-gray-300 hover:bg-white disabled:cursor-not-allowed disabled:opacity-70"
              >
                <div className="grid h-11 w-11 shrink-0 place-items-center rounded-2xl bg-white text-gray-400 shadow-sm">
                  {uploading ? <Loader2 className="h-5 w-5 animate-spin" /> : <UploadCloud className="h-5 w-5" />}
                </div>
                <div className="min-w-0 flex-1">
                  <div className="text-sm font-black text-gray-900">{uploading ? `正在上传 EPUB${uploadPercent ? ` ${uploadPercent}%` : ""}` : "上传客户 EPUB"}</div>
                  <div className="mt-1 text-xs leading-5 text-gray-400">支持 .epub，最大 80 MB。分片上传，弱网失败会自动重试。</div>
                  {uploading && (
                    <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-white">
                      <div className="h-full rounded-full bg-[#6d5dfc] transition-all" style={{ width: `${Math.max(uploadPercent, 6)}%` }} />
                    </div>
                  )}
                </div>
              </button>
              {uploading && (
                <button
                  type="button"
                  onClick={() => {
                    uploadAbortRef.current?.abort();
                    if (activeUploadIdRef.current) {
                      void cancelUploadSession(activeUploadIdRef.current).catch(() => {});
                      activeUploadIdRef.current = "";
                    }
                  }}
                  className="flex h-10 w-full items-center justify-center gap-2 rounded-2xl border border-gray-100 bg-white text-sm font-black text-gray-500 transition hover:border-gray-200 hover:text-gray-900"
                >
                  <X className="h-4 w-4" />
                  取消上传
                </button>
              )}
              <select
                value={selectedBookId}
                onChange={(event) => setSelectedBookId(event.target.value)}
                className="w-full rounded-2xl border border-gray-100 bg-gray-50/80 px-4 py-3 text-sm font-semibold text-gray-800 outline-none transition focus:border-black/20 focus:bg-white focus:ring-4 focus:ring-black/5"
              >
                {books.map((book) => (
                  <option key={book.id} value={book.id}>
                    {book.source === "uploaded" ? "已上传 · " : "内置 · "}
                    {book.display_name || book.filename}
                  </option>
                ))}
              </select>
              {books.length === 0 && (
                <div className="flex items-start gap-2 rounded-2xl border border-amber-100 bg-amber-50 p-3 text-xs leading-5 text-amber-800">
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                  还没有可用 EPUB，可以直接上传客户文件。
                </div>
              )}
              <div className="text-xs font-medium text-gray-400">
                {selectedBook
                  ? `${selectedBook.source === "uploaded" ? "客户上传" : "项目内置"} · 文件大小：${formatBytes(selectedBook.size || 0)}`
                  : "选择一本小说开始"}
              </div>
              <div className="rounded-2xl border border-gray-100 bg-gray-50/80 px-4 py-3 text-xs leading-5 text-gray-500">
                分析模型：{selectedChatModel ? `${selectedChatModel.name} · ${selectedChatModel.model_name}` : "使用后端默认 LLM 渠道"}
              </div>
              <div className="rounded-2xl border border-gray-100 bg-gray-50/80 p-1">
                <div className="grid grid-cols-2 gap-1">
                  <button
                    type="button"
                    onClick={() => setSceneMode("auto")}
                    disabled={running}
                    className={`h-10 rounded-xl text-xs font-black transition disabled:cursor-not-allowed disabled:opacity-70 ${
                      sceneMode === "auto" ? "bg-white text-gray-950 shadow-sm" : "text-gray-400 hover:text-gray-700"
                    }`}
                  >
                    自动挑选多图
                  </button>
                  <button
                    type="button"
                    onClick={() => setSceneMode("targeted")}
                    disabled={running}
                    className={`h-10 rounded-xl text-xs font-black transition disabled:cursor-not-allowed disabled:opacity-70 ${
                      sceneMode === "targeted" ? "bg-white text-gray-950 shadow-sm" : "text-gray-400 hover:text-gray-700"
                    }`}
                  >
                    指定剧情单图
                  </button>
                </div>
              </div>
              {sceneMode === "targeted" && (
                <div className="space-y-2">
                  <label className="text-xs font-black uppercase tracking-[0.18em] text-gray-400">想呈现的剧情</label>
                  <textarea
                    value={targetPlot}
                    onChange={(event) => setTargetPlot(event.target.value)}
                    disabled={running}
                    maxLength={2000}
                    rows={4}
                    placeholder="例如：红岸基地第一次向太阳发出信号、罗辑面对黑暗森林威慑的瞬间、章北海带领舰队逃离..."
                    className="w-full resize-none rounded-2xl border border-gray-100 bg-gray-50/80 px-4 py-3 text-sm leading-6 text-gray-800 outline-none transition placeholder:text-gray-300 focus:border-black/20 focus:bg-white focus:ring-4 focus:ring-black/5 disabled:cursor-not-allowed disabled:opacity-70"
                  />
                  <div className="flex items-center justify-between text-xs font-medium text-gray-400">
                    <span>AI 会读取全文定位剧情上下文，只生成 1 张图。</span>
                    <span>{targetPlot.length}/2000</span>
                  </div>
                </div>
              )}
              <button
                type="button"
                onClick={submitTask}
                disabled={submitting || uploading || retrying || running || !selectedBook}
                className="flex h-12 w-full cursor-pointer items-center justify-center gap-3 rounded-2xl bg-[#6d5dfc] text-sm font-black text-white shadow-xl shadow-[#6d5dfc]/20 transition hover:-translate-y-0.5 hover:bg-[#5b4cf2] disabled:cursor-not-allowed disabled:opacity-70"
              >
                {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
                {running ? "任务运行中" : sceneMode === "targeted" ? "生成指定剧情单图" : "提交可视化任务"}
              </button>
              {canRetryActiveTask && (
                <button
                  type="button"
                  onClick={retryActiveTask}
                  disabled={retrying}
                  className="flex h-11 w-full cursor-pointer items-center justify-center gap-2 rounded-2xl border border-rose-100 bg-rose-50 text-sm font-black text-rose-700 transition hover:border-rose-200 hover:bg-white disabled:cursor-not-allowed disabled:opacity-70"
                >
                  {retrying ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCcw className="h-4 w-4" />}
                  重试当前失败任务
                </button>
              )}
            </div>
          </section>

          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-sm font-black text-gray-950">独立历史</h2>
              <button
                type="button"
                onClick={() => void loadInitialData()}
                className="rounded-xl border border-gray-100 bg-white p-1.5 text-gray-400 shadow-sm transition hover:text-black"
                title="刷新"
              >
                <RefreshCcw className="h-4 w-4" />
              </button>
            </div>
            <div className="max-h-[420px] space-y-2 overflow-y-auto pr-1">
              {loading ? (
                <div className="flex items-center gap-2 py-8 text-sm font-bold text-gray-500">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  正在载入
                </div>
              ) : tasks.length ? (
                tasks.map((task) => (
                  <button
                    key={task.id}
                    type="button"
                    onClick={() => void openTask(task.id)}
                    className={`w-full rounded-lg border p-3 text-left transition ${
                      activeTask?.id === task.id
                        ? "border-[#6d5dfc]/20 bg-[#6d5dfc] text-white shadow-lg shadow-[#6d5dfc]/15"
                        : "border-gray-100 bg-gray-50 text-gray-700 hover:border-gray-200 hover:bg-white"
                    }`}
                  >
                    <div className="line-clamp-1 text-sm font-black">{task.book_filename}</div>
                    <div className={`mt-2 flex items-center justify-between text-xs ${activeTask?.id === task.id ? "text-white/60" : "text-gray-400"}`}>
                      <span>{formatTime(task.created_at)}</span>
                      <span>{statusLabel[task.status] || task.status}</span>
                    </div>
                    <div className={`mt-2 h-1.5 overflow-hidden rounded-full ${activeTask?.id === task.id ? "bg-white/15" : "bg-white"}`}>
                      <div className={`h-full ${activeTask?.id === task.id ? "bg-white" : progressColor(task.status)}`} style={{ width: `${task.progress_percent || 0}%` }} />
                    </div>
                  </button>
                ))
              ) : (
                <div className="rounded-2xl border border-dashed border-gray-200 bg-gray-50/60 p-4 text-sm text-gray-400">
                  暂无小说可视化任务。
                </div>
              )}
            </div>
          </section>
          </div>
        </aside>

        <main className="order-1 flex min-h-[calc(100vh-4rem)] flex-col gap-4 overflow-hidden p-4 pb-4 lg:order-2 lg:min-h-0 lg:p-5">
          <section className="rounded-[2rem] border border-black/5 bg-white/70 p-5 shadow-sm backdrop-blur">
            <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
              <div>
                <div className="flex items-center gap-2 text-[11px] font-black uppercase tracking-[0.18em] text-gray-400">
                  <Film className="h-4 w-4" />
                  Cinema Concept Board
                  {connected && <span className="rounded-full bg-emerald-50 px-2 py-0.5 text-[11px] text-emerald-600">SSE 已连接</span>}
                </div>
                <h2 className="mt-2 text-2xl font-black tracking-tight text-gray-950">
                  {activeTask?.book_filename || "等待提交小说任务"}
                </h2>
                {activeTask?.scene_mode === "targeted" && activeTask.target_plot && (
                  <p className="mt-2 line-clamp-2 max-w-3xl text-sm leading-6 text-gray-500">
                    指定剧情：{activeTask.target_plot}
                  </p>
                )}
              </div>
              <div className="grid grid-cols-3 gap-2 text-center">
                <div className="rounded-2xl border border-gray-100 bg-white px-4 py-3 shadow-sm">
                  <div className="text-xl font-black text-gray-950">{sceneImages.length}</div>
                  <div className="text-[11px] font-bold text-gray-400">场景</div>
                </div>
                <div className="rounded-2xl border border-gray-100 bg-white px-4 py-3 shadow-sm">
                  <div className="text-xl font-black text-emerald-500">{completedCount}</div>
                  <div className="text-[11px] font-bold text-gray-400">完成</div>
                </div>
                <div className="rounded-2xl border border-gray-100 bg-white px-4 py-3 shadow-sm">
                  <div className="text-xl font-black text-red-400">{failedCount}</div>
                  <div className="text-[11px] font-bold text-gray-400">失败</div>
                </div>
              </div>
            </div>
            <div className="mt-5 h-2 overflow-hidden rounded-full bg-gray-100">
              <div
                className={`h-full transition-all duration-500 ${progressColor(activeTask?.status || "running")}`}
                style={{ width: `${activeTask?.progress_percent || 0}%` }}
              />
            </div>
            {canRetryActiveTask && (
              <div className="mt-4 flex flex-col gap-3 rounded-2xl border border-rose-100 bg-rose-50/80 p-4 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <div className="text-sm font-black text-rose-900">任务失败后可以直接重试</div>
                  <p className="mt-1 line-clamp-2 text-xs leading-5 text-rose-700">
                    {activeTask?.error_message || "会清空旧的失败结果，并使用同一本 EPUB 重新分析和生成。"}
                  </p>
                </div>
                <button
                  type="button"
                  onClick={retryActiveTask}
                  disabled={retrying}
                  className="inline-flex h-10 shrink-0 cursor-pointer items-center justify-center gap-2 rounded-2xl bg-rose-600 px-4 text-sm font-black text-white shadow-lg shadow-rose-600/15 transition hover:bg-rose-700 disabled:cursor-not-allowed disabled:opacity-70"
                >
                  {retrying ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCcw className="h-4 w-4" />}
                  重试任务
                </button>
              </div>
            )}
          </section>

          <section className="grid min-h-0 flex-1 gap-4 2xl:grid-cols-[minmax(0,1fr)_320px]">
            <div className="flex min-h-[520px] flex-col overflow-hidden rounded-[1.75rem] border border-gray-100 bg-white/75 shadow-sm">
              <div className="flex shrink-0 flex-col gap-3 border-b border-gray-100 px-4 py-4 sm:flex-row sm:items-center sm:justify-between sm:px-5">
                <div>
                  <h2 className="text-sm font-black text-gray-950">场景图集</h2>
                  <p className="mt-1 text-xs font-medium text-gray-400">按情节顺序汇总已生成的电影概念画面。</p>
                </div>
                <div className="flex items-center gap-2 text-xs font-black text-gray-500">
                  <span className="rounded-full bg-gray-100 px-3 py-1">{completedCount}/{sceneImages.length || 0} 已完成</span>
                  {activeTask?.scene_mode === "targeted" && <span className="rounded-full bg-amber-50 px-3 py-1 text-amber-700">单图</span>}
                  {running && <span className="rounded-full bg-[#6d5dfc]/10 px-3 py-1 text-[#6d5dfc]">生成中</span>}
                </div>
              </div>

              <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4 sm:px-5 lg:max-h-[calc(100vh-300px)]">
                <div className="grid gap-4 sm:grid-cols-2 2xl:grid-cols-3">
                  {sceneImages.length ? (
                    sceneImages.map((scene) => (
                      <article key={scene.id || scene.scene_number} className="group overflow-hidden rounded-[1.35rem] border border-gray-100 bg-white shadow-sm transition hover:-translate-y-0.5 hover:border-gray-200 hover:shadow-lg hover:shadow-black/5">
                        <div className="relative aspect-[4/3] bg-gray-50">
                          {scene.image_url ? (
                            <>
                              <button
                                type="button"
                                onClick={() => setPreviewScene(scene)}
                                className="block h-full w-full cursor-zoom-in overflow-hidden text-left"
                                aria-label={`放大查看 ${scene.title}`}
                              >
                                <img src={scenePreviewUrl(scene)} alt={scene.title} loading="lazy" className="h-full w-full object-cover transition duration-300 group-hover:scale-[1.03]" />
                              </button>
                              <a
                                href={sceneDownloadUrl(scene)}
                                download
                                onClick={(event) => event.stopPropagation()}
                                className="absolute right-3 top-3 grid h-9 w-9 place-items-center rounded-full bg-white/90 text-gray-700 shadow-sm backdrop-blur transition hover:bg-white hover:text-black"
                                aria-label={`下载 ${scene.title}`}
                              >
                                <Download className="h-4 w-4" />
                              </a>
                            </>
                          ) : (
                            <div className="flex h-full flex-col items-center justify-center gap-3 text-gray-300">
                              {scene.status === "running" ? <Loader2 className="h-8 w-8 animate-spin text-gray-800" /> : <ImageIcon className="h-8 w-8" />}
                              <span className="text-xs font-bold text-gray-400">{statusLabel[scene.status] || scene.status}</span>
                            </div>
                          )}
                          <div className="absolute left-3 top-3 rounded-full bg-white/90 px-3 py-1 text-xs font-black text-gray-700 shadow-sm backdrop-blur">
                            #{scene.scene_number}
                          </div>
                        </div>
                        <div className="space-y-2.5 p-4">
                          <div className="flex items-start justify-between gap-3">
                            <div className="min-w-0">
                              <h3 className="line-clamp-2 text-base font-black leading-snug text-gray-950">{scene.title}</h3>
                              <p className="mt-1 line-clamp-1 text-xs font-bold text-gray-400">{scene.chapter || "章节未标注"}</p>
                            </div>
                            <div className="mt-0.5 shrink-0">{sceneStatusIcon(scene.status)}</div>
                          </div>
                          <p className="line-clamp-2 text-xs leading-5 text-gray-500">{scene.story_significance || "等待故事意义分析"}</p>
                          {scene.excerpt && (
                            <details className="group/detail rounded-2xl border border-dashed border-gray-200 bg-gray-50/70 px-3 py-2 text-xs leading-5 text-gray-400">
                              <summary className="cursor-pointer select-none font-bold text-gray-500 marker:text-gray-300">原文摘录</summary>
                              <blockquote className="mt-2 line-clamp-4">{scene.excerpt}</blockquote>
                            </details>
                          )}
                          {scene.error_message && <p className="rounded-2xl border border-rose-100 bg-rose-50 p-3 text-xs leading-5 text-rose-700">{scene.error_message}</p>}
                        </div>
                      </article>
                    ))
                  ) : (
                    <div className="col-span-full flex min-h-[360px] items-center justify-center rounded-[1.5rem] border border-dashed border-gray-200 bg-white/60 p-8 text-center">
                      <div>
                        <div className="mx-auto mb-4 grid h-16 w-16 place-items-center rounded-3xl bg-gray-50 text-gray-300">
                          <Sparkles className="h-7 w-7" />
                        </div>
                        <h3 className="text-lg font-black text-gray-900">还没有场景画面</h3>
                        <p className="mt-2 text-sm text-gray-400">提交 EPUB 后，AI 会先拆解场景，再逐张把结果推送到这里。</p>
                      </div>
                    </div>
                  )}
                </div>
              </div>
            </div>

            <aside className="flex min-h-[320px] flex-col overflow-hidden rounded-[1.75rem] border border-gray-100 bg-white shadow-sm 2xl:max-h-[calc(100vh-300px)]">
              <div className="shrink-0 border-b border-gray-100 px-4 py-4">
                <h2 className="text-sm font-black text-gray-950">实时进度日志</h2>
                <p className="mt-1 text-xs font-medium text-gray-400">分析、排队、重试与完成事件实时同步。</p>
              </div>
              <div className="min-h-0 flex-1 space-y-2 overflow-y-auto p-4">
                {logs.length ? (
                  logs.map((log) => (
                    <div key={log.id} className="rounded-2xl border border-gray-100 bg-gray-50/80 p-3">
                      <div className="mb-1 flex items-center justify-between gap-2 text-[11px] font-bold text-gray-400">
                        <span>{log.type}</span>
                        <span>{formatTime(log.createdAt)}</span>
                      </div>
                      <p className="text-sm leading-6 text-gray-700 2xl:text-xs 2xl:leading-5">{log.message}</p>
                    </div>
                  ))
                ) : (
                  <div className="rounded-2xl border border-dashed border-gray-200 bg-gray-50/60 p-4 text-sm leading-6 text-gray-400">
                    等待任务启动。日志会通过 SSE 实时推送，不需要刷新页面。
                  </div>
                )}
              </div>
            </aside>
          </section>
        </main>
      </div>
      {previewScene && createPortal(
        <div
          className="fixed inset-0 z-[210] grid place-items-center bg-black/55 p-4 backdrop-blur-sm"
          onClick={() => setPreviewScene(null)}
        >
          <div
            className="flex max-h-[90vh] w-full max-w-6xl flex-col overflow-hidden rounded-[2rem] bg-white shadow-2xl"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="flex shrink-0 items-center justify-between gap-3 border-b border-gray-100 px-5 py-4">
              <div className="min-w-0">
                <div className="text-xs font-black uppercase tracking-[0.18em] text-gray-400">场景大图</div>
                <h3 className="mt-1 line-clamp-1 text-sm font-black text-gray-950">
                  #{previewScene.scene_number} {previewScene.title}
                </h3>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <a
                  href={sceneDownloadUrl(previewScene)}
                  download
                  className="inline-flex h-10 items-center gap-2 rounded-full bg-black px-4 text-xs font-black text-white transition hover:bg-gray-800"
                >
                  <Download className="h-4 w-4" />
                  下载
                </a>
                <button
                  type="button"
                  onClick={() => setPreviewScene(null)}
                  className="grid h-10 w-10 place-items-center rounded-full bg-gray-50 text-gray-500 transition hover:text-black"
                  aria-label="关闭大图"
                >
                  <X className="h-5 w-5" />
                </button>
              </div>
            </div>
            <div className="min-h-0 flex-1 overflow-auto bg-gray-50 p-4">
              <img
                src={sceneFullUrl(previewScene)}
                alt={previewScene.title}
                className="mx-auto max-h-[72vh] rounded-2xl object-contain shadow-sm"
              />
            </div>
          </div>
        </div>,
        document.body,
      )}
    </motion.div>
  );
}
