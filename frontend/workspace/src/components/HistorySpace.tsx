import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";
import { motion, AnimatePresence } from "motion/react";
import {
  AlertTriangle,
  AudioLines,
  Clock,
  Download,
  Eye,
  Filter,
  Images,
  Image as ImageIcon,
  Loader2,
  RefreshCcw,
  Search,
  Trash2,
  X,
} from "lucide-react";
import {
  AuthRequiredError,
  cancelTask,
  deleteNovelTask,
  deleteGenerationTask,
  deleteTtsTask,
  cancelTtsTask,
  fetchImageDownloadUrl,
  fetchTasks,
  retryNovelTask,
  type GenerationTask,
  type PaginationMeta,
} from "../services/apiService";
import { triggerBrowserDownload } from "@shared/services/download";
import { StackGallery } from "@shared/ui";

const statusText: Record<string, string> = {
  queued: "排队中",
  running: "生成中",
  succeeded: "已完成",
  failed: "失败",
  canceled: "已取消",
};

const statusOptions = [
  { value: "all", label: "全部状态" },
  { value: "queued", label: "排队中" },
  { value: "running", label: "生成中" },
  { value: "succeeded", label: "已完成" },
  { value: "failed", label: "失败" },
  { value: "canceled", label: "已取消" },
] as const;

function formatTime(value?: string) {
  if (!value) return "未知时间";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "未知时间";
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function firstImage(task: GenerationTask) {
  return task.images?.find((image) => image.status === "available") || task.images?.[0];
}

function isNovelTask(task: GenerationTask) {
  return task.task_kind === "novelviz" || task.generation_mode === "novel_visualization" || task.task_no.startsWith("novelviz:");
}

function isTtsTask(task: GenerationTask) {
  return task.task_kind === "tts" || task.task_no.startsWith("tts:");
}

// 本地零配置渠道出的是渐变占位图（不是真实生图结果），本地 API 会在任务里带 placeholder 标记。
function isPlaceholderTask(task: GenerationTask) {
  return Boolean(task.placeholder) || task.channel_name === "本地占位生图";
}

// Comfy / TTS 任务（仅本地模式）点开后要回到对应板块，用前缀和小说任务走同一套约定。
function taskOpenKey(task: GenerationTask) {
  if (task.task_kind === "tts" || task.task_no.startsWith("tts:")) return task.task_no;
  return task.engine === "comfy" ? `comfy:${task.task_no}` : task.task_no;
}

function progressClass(status: string) {
  if (status === "failed") return "bg-red-400";
  if (status === "succeeded") return "bg-emerald-500";
  return "bg-black";
}

function isActiveStatus(status: string) {
  return ["queued", "running"].includes(status);
}

function taskSignature(task: GenerationTask): string {
  // Compact invariant used to detect real changes during silent polling. Stable
  // preview URLs mean unchanged tasks produce identical signatures, so the grid
  // keeps its rendered identity (and browser-cached thumbnails) between polls.
  const imgs = (task.images || []).map((i) => `${i.id}:${i.status}:${i.preview_url || i.image_url || ""}`).join("|");
  return `${task.task_no}:${task.status}:${task.progress_percent}:${imgs}`;
}

function sameTaskList(a: GenerationTask[], b: GenerationTask[]): boolean {
  if (a === b) return true;
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i += 1) {
    if (taskSignature(a[i]) !== taskSignature(b[i])) return false;
  }
  return true;
}

function isRetryableNovelTask(task: GenerationTask) {
  return isNovelTask(task) && ["failed", "canceled"].includes(task.status) && Boolean(task.novel_task_id);
}

function visibleProgress(task: GenerationTask) {
  const raw = Math.max(0, Math.min(Math.round(task.progress_percent || 0), 100));
  if (task.images?.length && ["succeeded", "failed", "canceled"].includes(task.status)) return 100;
  if (task.status === "queued") return Math.min(Math.max(raw, 8), 22);
  if (task.status === "running") return Math.min(Math.max(raw, 20), 72);
  if (task.status === "succeeded") return Math.min(Math.max(raw, 76), 88);
  if (task.status === "failed" || task.status === "canceled") return Math.min(raw, 82);
  return raw;
}

function appendImageQuery(url = "", query: string) {
  if (!url) return "";
  return `${url}${url.includes("?") ? "&" : "?"}${query}`;
}

function galleryImagePreviewUrl(image: GenerationTask["images"][number]) {
  return image.preview_url || appendImageQuery(image.image_url, "preview=1") || image.image_url || image.download_url || "";
}

function galleryImageFullUrl(image: GenerationTask["images"][number]) {
  return image.image_url || image.download_url || image.preview_url || "";
}

function galleryImageDownloadUrl(image: GenerationTask["images"][number]) {
  return image.download_url || appendImageQuery(image.image_url, "download=1") || image.image_url || "";
}

function HistoryModals({
  preview,
  gallery,
  onClosePreview,
  onCloseGallery,
  onOpenTask,
  onRetryNovelTask,
  retryingTaskNo,
}: {
  preview: { task: GenerationTask; url: string } | null;
  gallery: GenerationTask | null;
  onClosePreview: () => void;
  onCloseGallery: () => void;
  onOpenTask?: (taskNo: string) => void;
  onRetryNovelTask?: (event: React.MouseEvent, task: GenerationTask) => void;
  retryingTaskNo?: string;
}) {
  const [galleryPreview, setGalleryPreview] = useState<GenerationTask["images"][number] | null>(null);

  useEffect(() => {
    setGalleryPreview(null);
  }, [gallery?.task_no]);

  return createPortal(
    <AnimatePresence>
      {preview && (
        <motion.div
          className="fixed inset-0 z-[200] grid place-items-center bg-black/45 p-5 backdrop-blur-sm"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          onClick={onClosePreview}
        >
          <motion.div
            initial={{ opacity: 0, y: 16, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 16, scale: 0.98 }}
            onClick={(event) => event.stopPropagation()}
            className="w-full max-w-5xl overflow-hidden rounded-[2rem] bg-white shadow-2xl"
          >
            <div className="flex items-center justify-between border-b border-gray-100 px-5 py-4">
              <div className="min-w-0">
                <div className="text-xs font-black uppercase tracking-[0.18em] text-gray-400">图片预览</div>
                <p className="mt-1 line-clamp-1 text-sm font-bold text-gray-900">{preview.task.prompt}</p>
              </div>
              <button
                type="button"
                onClick={onClosePreview}
                className="grid h-10 w-10 place-items-center rounded-full bg-gray-50 text-gray-500 transition hover:text-black"
                aria-label="关闭预览"
              >
                <X className="h-5 w-5" />
              </button>
            </div>
            <div className="max-h-[72vh] overflow-auto bg-gray-50 p-4">
              <img src={preview.url} alt={preview.task.prompt} className="mx-auto max-h-[68vh] rounded-2xl object-contain" />
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4 text-xs text-gray-500">
              <span>点击卡片可回到对应创作会话</span>
              <button
                type="button"
                onClick={() => onOpenTask?.(taskOpenKey(preview.task))}
                className="rounded-2xl bg-black px-4 py-2 font-bold text-white transition hover:bg-gray-800"
              >
                进入会话
              </button>
            </div>
          </motion.div>
        </motion.div>
      )}

      {gallery && (
        <motion.div
          className="fixed inset-0 z-[200] grid place-items-center bg-black/45 p-5 backdrop-blur-sm"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          onClick={onCloseGallery}
        >
          <motion.div
            initial={{ opacity: 0, y: 16, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 16, scale: 0.98 }}
            onClick={(event) => event.stopPropagation()}
            className="relative flex h-[86vh] w-full max-w-[78rem] flex-col overflow-hidden rounded-[2rem] bg-white shadow-2xl"
          >
            <div className="flex shrink-0 items-center justify-between border-b border-gray-100 px-5 py-4">
              <div className="min-w-0">
                <div className="text-xs font-black uppercase tracking-[0.18em] text-gray-400">
                  {gallery.scene_mode === "targeted" ? "小说指定剧情单图" : "小说可视化图集"}
                </div>
                <p className="mt-1 line-clamp-1 text-sm font-bold text-gray-900">{gallery.prompt}</p>
                {gallery.scene_mode === "targeted" && gallery.target_plot && (
                  <p className="mt-1 line-clamp-1 text-xs font-medium text-gray-400">指定剧情：{gallery.target_plot}</p>
                )}
              </div>
              <div className="flex shrink-0 items-center gap-2">
                {isRetryableNovelTask(gallery) && (
                  <button
                    type="button"
                    onClick={(event) => onRetryNovelTask?.(event, gallery)}
                    disabled={retryingTaskNo === gallery.task_no}
                    className="inline-flex h-10 items-center gap-2 rounded-full bg-rose-50 px-4 text-xs font-black text-rose-700 transition hover:bg-rose-100 disabled:cursor-not-allowed disabled:opacity-70"
                  >
                    {retryingTaskNo === gallery.task_no ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCcw className="h-4 w-4" />}
                    重试
                  </button>
                )}
                <button
                  type="button"
                  onClick={onCloseGallery}
                  className="grid h-10 w-10 place-items-center rounded-full bg-gray-50 text-gray-400 transition hover:text-black"
                  aria-label="关闭图集"
                >
                  <X className="h-5 w-5" />
                </button>
              </div>
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto bg-gray-50/60 p-5">
              {gallery.images?.some((image) => image.status === "available") ? (
                <div className="grid gap-5 md:grid-cols-2 2xl:grid-cols-3">
                  {gallery.images
                    .filter((image) => image.status === "available")
                    .map((image) => (
                      <article key={image.id} className="overflow-hidden rounded-[1.35rem] border border-gray-100 bg-white shadow-sm">
                        <div className="relative aspect-[4/3] bg-gray-50">
                          <button
                            type="button"
                            onClick={() => setGalleryPreview(image)}
                            className="block h-full w-full cursor-zoom-in overflow-hidden text-left"
                            aria-label={`放大查看 ${image.title || "小说场景"}`}
                          >
                            <img
                              src={galleryImagePreviewUrl(image)}
                              alt={image.title || gallery.prompt}
                              loading="lazy"
                              decoding="async"
                              fetchPriority="low"
                              className="h-full w-full object-cover transition duration-300 hover:scale-[1.03]"
                            />
                          </button>
                          <div className="absolute left-3 top-3 rounded-full bg-white/90 px-3 py-1 text-xs font-black text-gray-700 shadow-sm backdrop-blur">
                            #{image.scene_number || image.id}
                          </div>
                          <a
                            href={galleryImageDownloadUrl(image)}
                            download
                            onClick={(event) => event.stopPropagation()}
                            className="absolute right-3 top-3 grid h-9 w-9 place-items-center rounded-full bg-white/90 text-gray-700 shadow-sm backdrop-blur transition hover:bg-white hover:text-black"
                            aria-label={`下载 ${image.title || "小说场景"}`}
                          >
                            <Download className="h-4 w-4" />
                          </a>
                        </div>
                        <div className="space-y-2.5 p-4">
                          <h3 className="line-clamp-2 text-base font-black leading-snug text-gray-950">{image.title || "小说场景"}</h3>
                          <p className="line-clamp-1 text-xs font-bold text-gray-400">{image.chapter || "章节未标注"}</p>
                          {image.story_significance && (
                            <p className="line-clamp-2 text-xs leading-5 text-gray-500">{image.story_significance}</p>
                          )}
                          {image.excerpt && (
                            <details className="rounded-2xl border border-dashed border-gray-200 bg-gray-50/70 px-3 py-2 text-xs leading-5 text-gray-400">
                              <summary className="cursor-pointer select-none font-bold text-gray-500 marker:text-gray-300">原文摘录</summary>
                              <blockquote className="mt-2 max-h-40 overflow-y-auto pr-1">{image.excerpt}</blockquote>
                            </details>
                          )}
                        </div>
                      </article>
                    ))}
                </div>
              ) : (
                <div className="grid min-h-[320px] place-items-center rounded-[1.5rem] border border-dashed border-gray-200 bg-gray-50/70 text-center">
                  <div>
                    <ImageIcon className="mx-auto h-10 w-10 text-gray-300" />
                    <h3 className="mt-4 text-sm font-black text-gray-900">这个小说任务还没有可展示图片</h3>
                    <p className="mt-2 text-xs leading-5 text-gray-400">{gallery.error_message || "等待任务完成后会用第一张图作为封面。"}</p>
                    {isRetryableNovelTask(gallery) && (
                      <button
                        type="button"
                        onClick={(event) => onRetryNovelTask?.(event, gallery)}
                        disabled={retryingTaskNo === gallery.task_no}
                        className="mt-4 inline-flex h-10 items-center gap-2 rounded-2xl bg-black px-4 text-xs font-black text-white transition hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-70"
                      >
                        {retryingTaskNo === gallery.task_no ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCcw className="h-4 w-4" />}
                        重试任务
                      </button>
                    )}
                  </div>
                </div>
              )}
            </div>
            {galleryPreview && (
              <div
                className="absolute inset-0 z-[220] grid place-items-center bg-black/55 p-4 backdrop-blur-sm"
                onClick={() => setGalleryPreview(null)}
              >
                <div
                  className="flex max-h-full w-full max-w-5xl flex-col overflow-hidden rounded-[2rem] bg-white shadow-2xl"
                  onClick={(event) => event.stopPropagation()}
                >
                  <div className="flex shrink-0 items-center justify-between gap-3 border-b border-gray-100 px-5 py-4">
                    <div className="min-w-0">
                      <div className="text-xs font-black uppercase tracking-[0.18em] text-gray-400">场景大图</div>
                      <h3 className="mt-1 line-clamp-1 text-sm font-black text-gray-950">
                        #{galleryPreview.scene_number || galleryPreview.id} {galleryPreview.title || "小说场景"}
                      </h3>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      <a
                        href={galleryImageDownloadUrl(galleryPreview)}
                        download
                        className="inline-flex h-10 items-center gap-2 rounded-full bg-black px-4 text-xs font-black text-white transition hover:bg-gray-800"
                      >
                        <Download className="h-4 w-4" />
                        下载
                      </a>
                      <button
                        type="button"
                        onClick={() => setGalleryPreview(null)}
                        className="grid h-10 w-10 place-items-center rounded-full bg-gray-50 text-gray-500 transition hover:text-black"
                        aria-label="关闭大图"
                      >
                        <X className="h-5 w-5" />
                      </button>
                    </div>
                  </div>
                  <div className="min-h-0 flex-1 overflow-auto bg-gray-50 p-4">
                    <img
                      src={galleryImageFullUrl(galleryPreview)}
                      alt={galleryPreview.title || gallery.prompt}
                      className="mx-auto max-h-[70vh] rounded-2xl object-contain shadow-sm"
                    />
                  </div>
                </div>
              </div>
            )}
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>,
    document.body,
  );
}

export default function HistorySpace({
  addToast,
  t,
  onOpenTask,
}: {
  addToast?: (msg: string, type?: "success" | "error" | "info") => void;
  t: any;
  onOpenTask?: (taskNo: string) => void;
}) {
  const [tasks, setTasks] = useState<GenerationTask[]>([]);
  const [query, setQuery] = useState("");
  const [debouncedQuery, setDebouncedQuery] = useState("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [pagination, setPagination] = useState<PaginationMeta | null>(null);
  const [preview, setPreview] = useState<{ task: GenerationTask; url: string } | null>(null);
  const [gallery, setGallery] = useState<GenerationTask | null>(null);
  const [retryingTaskNo, setRetryingTaskNo] = useState("");
  const [selectedStatus, setSelectedStatus] = useState<(typeof statusOptions)[number]["value"]>("all");
  const [showFilters, setShowFilters] = useState(false);

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), 350);
    return () => window.clearTimeout(timer);
  }, [query]);

  const loadTasks = async (page = 1, options: { silent?: boolean } = {}) => {
    const silent = Boolean(options.silent);
    if (page === 1 && !silent) setLoading(true);
    else if (page > 1) setLoadingMore(true);
    try {
      const result = await fetchTasks({ page, pageSize: 18, query: debouncedQuery });
      setTasks((current) => {
        if (page === 1) {
          // 非静默（首次加载 / 换搜索词）直接用第一页替换；
          // 静默刷新（8 秒轮询、任务完成事件）只更新第一页并保留已加载的后续页，
          // 否则用户点出来的「加载更多」会在下一个轮询周期被冲掉。
          if (!silent) return result.items;
          const byTaskNo = new Map(result.items.map((task) => [task.task_no, task]));
          const merged = current.map((task) => byTaskNo.get(task.task_no) ?? task);
          const known = new Set(current.map((task) => task.task_no));
          const fresh = result.items.filter((task) => !known.has(task.task_no));
          const next = fresh.length ? [...fresh, ...merged] : merged;
          // Preserve array identity when nothing meaningful changed, so the grid
          // doesn't re-render / re-run entrance animations on every silent poll.
          return sameTaskList(current, next) ? current : next;
        }
        return [...current, ...result.items];
      });
      setPagination(result.pagination);
    } catch (error: any) {
      if (error instanceof AuthRequiredError) return;
      addToast?.(error.message || "历史记录加载失败", "error");
    } finally {
      if (page === 1 && !silent) setLoading(false);
      if (page > 1) setLoadingMore(false);
    }
  };

  useEffect(() => {
    loadTasks(1);
  }, [debouncedQuery]);

  useEffect(() => {
    const handleTaskUpdated = (event: Event) => {
      const detail = (event as CustomEvent<{
        taskNo?: string;
        status?: string;
        hasImage?: boolean;
        progressPercent?: number;
      }>).detail;
      if (!detail?.taskNo) return;

      let matchedTask = false;
      setTasks((current) => current.map((task) => {
        if (task.task_no !== detail.taskNo) return task;
        matchedTask = true;
        return {
          ...task,
          status: detail.status || task.status,
          progress_percent:
            typeof detail.progressPercent === "number"
              ? detail.progressPercent
              : task.progress_percent,
        };
      }));

      if (!matchedTask || detail.hasImage || ["failed", "canceled", "succeeded"].includes(detail.status || "")) {
        loadTasks(1, { silent: tasks.length > 0 });
      }
    };

    window.addEventListener("mirror:task-updated", handleTaskUpdated);
    return () => window.removeEventListener("mirror:task-updated", handleTaskUpdated);
  }, [debouncedQuery, tasks.length]);

  useEffect(() => {
    if (!tasks.some((task) => isActiveStatus(task.status))) return;

    // Poll less aggressively than before: tasks are updated via the
    // `mirror:task-updated` event on completion, and preview URLs are now stable
    // cacheable keys, so a slower poll is enough to refresh progress.
    const timer = window.setInterval(() => {
      loadTasks(1, { silent: true });
    }, 8000);

    return () => window.clearInterval(timer);
  }, [debouncedQuery, tasks.some((task) => isActiveStatus(task.status))]);

  const filteredTasks = useMemo(() => {
    if (selectedStatus === "all") return tasks;
    return tasks.filter((task) => task.status === selectedStatus);
  }, [selectedStatus, tasks]);

  const totalText = useMemo(() => {
    if (!pagination) return "";
    const activeFilter = statusOptions.find((item) => item.value === selectedStatus)?.label || "全部状态";
    if (selectedStatus === "all") {
      return `已加载 ${tasks.length} 条，总记录 ${pagination.total}`;
    }
    return `当前筛选：${activeFilter} · 已加载 ${tasks.length} 条，当前已加载结果命中 ${filteredTasks.length} 条，总记录 ${pagination.total}`;
  }, [filteredTasks.length, pagination, selectedStatus, tasks.length]);

  const openPreview = async (event: React.MouseEvent, task: GenerationTask) => {
    event.stopPropagation();
    if (isTtsTask(task)) {
      onOpenTask?.(taskOpenKey(task));
      return;
    }
    if (isNovelTask(task)) {
      setGallery(task);
      return;
    }
    const image = firstImage(task);
    if (!image?.id) {
      addToast?.("该任务暂无可预览图片", "info");
      return;
    }
    try {
      const url = image.preview_url || (await fetchImageDownloadUrl(image.id)).preview_url || image.download_url || "";
      if (!url) throw new Error("图片预览地址生成失败");
      setPreview({ task, url });
    } catch (error: any) {
      addToast?.(error.message || "图片预览失败", "error");
    }
  };

  const downloadImage = async (event: React.MouseEvent, task: GenerationTask) => {
    event.stopPropagation();
    if (isTtsTask(task)) {
      const audio = (task.outputs || []).find((output: any) => output.download_url) as any;
      if (audio?.download_url) {
        triggerBrowserDownload(audio.download_url, audio.name);
      } else {
        addToast?.("该 TTS 任务暂无可下载音频", "info");
      }
      return;
    }
    if (isNovelTask(task)) {
      const image = firstImage(task);
      const url = image?.download_url || (image?.image_url ? appendImageQuery(image.image_url, "download=1") : "");
      if (url) {
        triggerBrowserDownload(url);
      } else {
        addToast?.("该小说任务暂无可下载图片", "info");
      }
      return;
    }
    const image = firstImage(task);
    if (!image?.id) {
      addToast?.("该任务暂无可下载图片", "info");
      return;
    }
    try {
      const url = image.download_url || (await fetchImageDownloadUrl(image.id)).url;
      triggerBrowserDownload(url);
    } catch (error: any) {
      addToast?.(error.message || "原图下载失败", "error");
    }
  };

  const retryTask = async (event: React.MouseEvent, task: GenerationTask) => {
    event.stopPropagation();
    if (!isRetryableNovelTask(task) || !task.novel_task_id) {
      addToast?.("当前小说任务还不能重试", "info");
      return;
    }
    setRetryingTaskNo(task.task_no);
    try {
      const retried = await retryNovelTask(task.novel_task_id);
      const nextStatus = retried.status || "queued";
      const nextProgress = retried.progress_percent || 1;
      setTasks((current) => current.map((item) => (
        item.task_no === task.task_no
          ? {
              ...item,
              status: nextStatus,
              progress_percent: nextProgress,
              error_message: retried.error_message,
              images: [],
            }
          : item
      )));
      setGallery((current) => current?.task_no === task.task_no ? {
        ...current,
        status: nextStatus,
        progress_percent: nextProgress,
        error_message: retried.error_message,
        images: [],
      } : current);
      window.dispatchEvent(new CustomEvent("mirror:task-updated", {
        detail: {
          taskNo: task.task_no,
          status: nextStatus,
          hasImage: false,
          progressPercent: nextProgress,
        },
      }));
      addToast?.("小说可视化任务已重新提交");
      window.setTimeout(() => {
        void loadTasks(1);
      }, 1000);
    } catch (error: any) {
      addToast?.(error.message || "重试失败", "error");
    } finally {
      setRetryingTaskNo("");
    }
  };

  const deleteTask = async (event: React.MouseEvent, task: GenerationTask) => {
    event.stopPropagation();
    if (isTtsTask(task)) {
      const realTaskNo = task.task_no.replace(/^tts:/, "");
      const isActiveTask = ["queued", "running"].includes(task.status);
      const confirmed = window.confirm(
        isActiveTask ? "该 TTS 任务仍在执行，删除前会先终止。确认继续吗？" : "确认删除这条 TTS 历史记录？",
      );
      if (!confirmed) return;
      try {
        if (isActiveTask) {
          await cancelTtsTask(realTaskNo);
        }
        await deleteTtsTask(realTaskNo);
        setTasks((current) => current.filter((item) => item.task_no !== task.task_no));
        setPagination((current) => current ? { ...current, total: Math.max(current.total - 1, 0) } : current);
        addToast?.("TTS 历史记录已删除");
      } catch (error: any) {
        addToast?.(error.message || "删除失败", "error");
      }
      return;
    }
    if (isNovelTask(task)) {
      const confirmed = window.confirm("确认删除这条小说可视化历史？删除后图集也会从历史中移除。");
      if (!confirmed || !task.novel_task_id) return;
      try {
        await deleteNovelTask(task.novel_task_id);
        setTasks((current) => current.filter((item) => item.task_no !== task.task_no));
        setPagination((current) => current ? { ...current, total: Math.max(current.total - 1, 0) } : current);
        addToast?.("小说可视化历史已删除");
      } catch (error: any) {
        addToast?.(error.message || "删除失败", "error");
      }
      return;
    }
    const isActiveTask = ["queued", "running"].includes(task.status);
    const confirmed = window.confirm(
      isActiveTask
        ? "该任务仍在执行，删除前会先终止任务。确认继续吗？"
        : "确认删除这条历史记录？删除后将不再显示，也不能继续预览或下载。",
    );
    if (!confirmed) return;
    try {
      if (isActiveTask) {
        await cancelTask(task.task_no);
      }
      await deleteGenerationTask(task.task_no);
      setTasks((current) => current.filter((item) => item.task_no !== task.task_no));
      setPagination((current) => current ? { ...current, total: Math.max(current.total - 1, 0) } : current);
      addToast?.(isActiveTask ? "任务已终止并从历史记录中移除" : "历史记录已删除");
    } catch (error: any) {
      addToast?.(error.message || "删除失败", "error");
    }
  };

  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      className="flex-1 overflow-y-auto"
    >
      <div className="mx-auto w-full max-w-6xl space-y-8 p-4 md:p-8">
        <div className="flex flex-col items-start justify-between gap-4 md:flex-row md:items-center">
          <div>
            <h1 className="text-3xl font-bold tracking-tight text-gray-900">{t.title}</h1>
            <p className="mt-1 text-sm font-medium text-gray-500">{t.subtitle}</p>
            {totalText && <p className="mt-2 text-xs font-bold text-gray-400">{totalText}</p>}
          </div>
          <div className="flex w-full items-center gap-3 md:w-auto">
            <div className="relative flex-1 md:w-72">
              <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
              <input
                type="text"
                placeholder={t.search}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                className="h-11 w-full rounded-2xl border border-gray-100 bg-white py-2 pl-10 pr-4 text-sm shadow-sm transition focus:border-gray-300 focus:outline-none focus:ring-4 focus:ring-black/5"
              />
            </div>
            <div className="relative">
              <button
                type="button"
                onClick={() => setShowFilters((value) => !value)}
                className={`grid h-11 w-11 place-items-center rounded-2xl border shadow-sm transition ${showFilters || selectedStatus !== "all" ? "border-black bg-black text-white" : "border-gray-100 bg-white text-gray-500 hover:border-gray-200 hover:text-black"}`}
                aria-label="筛选"
              >
                <Filter className="h-5 w-5" />
              </button>
              <AnimatePresence>
                {showFilters && (
                  <>
                    <div className="fixed inset-0 z-10" onClick={() => setShowFilters(false)} />
                    <motion.div
                      initial={{ opacity: 0, y: 10, scale: 0.96 }}
                      animate={{ opacity: 1, y: 0, scale: 1 }}
                      exit={{ opacity: 0, y: 10, scale: 0.96 }}
                      className="absolute right-0 top-14 z-20 w-44 rounded-2xl border border-gray-100 bg-white p-2 shadow-xl"
                    >
                      {statusOptions.map((option) => (
                        <button
                          key={option.value}
                          type="button"
                          onClick={() => {
                            setSelectedStatus(option.value);
                            setShowFilters(false);
                          }}
                          className={`flex w-full items-center justify-between rounded-xl px-3 py-2 text-sm transition ${selectedStatus === option.value ? "bg-black text-white" : "text-gray-600 hover:bg-gray-50 hover:text-black"}`}
                        >
                          <span>{option.label}</span>
                          {selectedStatus === option.value && <span className="text-[10px] font-black uppercase">ON</span>}
                        </button>
                      ))}
                    </motion.div>
                  </>
                )}
              </AnimatePresence>
            </div>
          </div>
        </div>

        {loading ? (
          <div className="grid min-h-[360px] place-items-center rounded-[2rem] border border-gray-100 bg-white/70">
            <div className="flex items-center gap-3 text-sm font-bold text-gray-500">
              <Loader2 className="h-5 w-5 animate-spin" />
              正在分批读取历史记录
            </div>
          </div>
        ) : !tasks.length ? (
          <div className="grid min-h-[420px] place-items-center rounded-[2rem] border border-dashed border-gray-200 bg-white/60 text-center">
            <div>
              <div className="mx-auto grid h-16 w-16 place-items-center rounded-3xl bg-gray-50 text-gray-300">
                <ImageIcon className="h-7 w-7" />
              </div>
              <h3 className="mt-5 text-lg font-black text-gray-900">暂无历史记录</h3>
              <p className="mt-2 text-sm text-gray-400">完成一次生图后，真实任务会自动出现在这里。</p>
            </div>
          </div>
        ) : (
          <>
            {!filteredTasks.length ? (
              <div className="grid min-h-[320px] place-items-center rounded-[2rem] border border-dashed border-gray-200 bg-white/60 text-center">
                <div>
                  <div className="mx-auto grid h-16 w-16 place-items-center rounded-3xl bg-gray-50 text-gray-300">
                    <Filter className="h-7 w-7" />
                  </div>
                  <h3 className="mt-5 text-lg font-black text-gray-900">当前已加载结果中没有匹配记录</h3>
                  <p className="mt-2 text-sm text-gray-400">请切换状态筛选，或继续加载更多历史任务。</p>
                </div>
              </div>
            ) : (
              <div className="grid grid-cols-1 gap-5 sm:grid-cols-2 xl:grid-cols-3">
                {filteredTasks.map((task, index) => {
                  const image = firstImage(task);
                  const previewUrl = image?.preview_url || "";
                  // 堆叠重放：同一任务有多张可用成图（小说图集 / 多格串行）时呈扇形堆叠，点击翻页。
                  const stackImages = (task.images || [])
                    .filter((item) => item.status === "available" || item.preview_url)
                    .map((item) => ({ key: item.id, src: item.preview_url || item.image_url || item.download_url || "" }))
                    .filter((item) => item.src);
                  return (
                    <motion.article
                      key={task.task_no}
                      initial={{ opacity: 0, y: 16 }}
                      animate={{ opacity: 1, y: 0 }}
                      transition={{ delay: Math.min(index, 12) * 0.025 }}
                      onClick={(event) => {
                        // 多图卡片：点击在图片上 = 翻下一张（堆叠重放）；点到操作按钮区 = 回跳编辑器。
                        const target = event.target as HTMLElement;
                        if (stackImages.length > 1 && target.closest("button") === null && target.tagName === "IMG") return;
                        onOpenTask?.(taskOpenKey(task));
                      }}
                      className="group cursor-pointer overflow-hidden rounded-[1.75rem] border border-gray-100 bg-white shadow-sm transition hover:-translate-y-1 hover:border-gray-200 hover:shadow-xl hover:shadow-black/5"
                    >
                      <div className="relative aspect-[4/3] bg-gray-50">
                        {isTtsTask(task) ? (
                          <div className="flex h-full flex-col items-center justify-center gap-3 bg-[radial-gradient(circle_at_30%_20%,#ffffff,transparent_32%),linear-gradient(135deg,#f8fafc,#eceff3)] px-4 text-center">
                            <div className="grid h-14 w-14 place-items-center rounded-3xl bg-white shadow-sm">
                              <AudioLines className="h-6 w-6 text-gray-300" />
                            </div>
                            {(() => {
                              const audio = ((task as any).outputs || []).find((output: any) => output.preview_url) as any;
                              return audio?.preview_url ? (
                                <audio controls preload="none" src={audio.preview_url} className="w-full max-w-[240px]" onClick={(event) => event.stopPropagation()} />
                              ) : (
                                <span className="text-[11px] font-bold text-gray-400">{statusText[task.status] || task.status}</span>
                              );
                            })()}
                          </div>
                        ) : stackImages.length > 1 ? (
                          <StackGallery
                            images={stackImages}
                            className="h-full w-full"
                            spread={10}
                            showArrows={false}
                          />
                        ) : previewUrl ? (
                          <img
                            src={previewUrl}
                            alt={task.prompt || task.task_no}
                            loading="lazy"
                            decoding="async"
                            fetchPriority="low"
                            className="h-full w-full object-cover"
                          />
                        ) : (
                          <div className="grid h-full place-items-center text-gray-300">
                            <ImageIcon className="h-10 w-10" />
                          </div>
                        )}
                        {isPlaceholderTask(task) && !isTtsTask(task) && (
                          <div className="pointer-events-none absolute bottom-3 left-3 inline-flex items-center gap-1 rounded-full bg-amber-500/95 px-2.5 py-1 text-[10px] font-black text-white shadow-sm">
                            <AlertTriangle className="h-3 w-3" />
                            占位图
                          </div>
                        )}
                          <div className="absolute inset-x-0 top-0 flex items-start justify-between p-3 opacity-0 transition group-hover:opacity-100">
                          <div className="rounded-full bg-white/90 px-3 py-1 text-[11px] font-black text-gray-700 shadow-sm backdrop-blur">
                            {isTtsTask(task)
                              ? `TTS · ${String((task as any).engine || "").replace("qwen-", "Qwen ").replace("megatts3", "MegaTTS3") || "语音"}`
                              : isNovelTask(task)
                                ? (task.scene_mode === "targeted" ? "指定剧情" : "小说图集")
                                : statusText[task.status] || task.status}
                          </div>
                          <div className="flex gap-2">
                            {isRetryableNovelTask(task) && (
                              <button
                                type="button"
                                onClick={(event) => retryTask(event, task)}
                                disabled={retryingTaskNo === task.task_no}
                                className="grid h-9 w-9 place-items-center rounded-full bg-white/90 text-rose-600 shadow-sm backdrop-blur transition hover:bg-rose-50 hover:text-rose-700 disabled:cursor-not-allowed disabled:opacity-70"
                                aria-label="重试小说可视化任务"
                              >
                                {retryingTaskNo === task.task_no ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCcw className="h-4 w-4" />}
                              </button>
                            )}
                            <button
                              type="button"
                              onClick={(event) => openPreview(event, task)}
                              className="grid h-9 w-9 place-items-center rounded-full bg-white/90 text-gray-600 shadow-sm backdrop-blur transition hover:text-black"
                              aria-label={isNovelTask(task) ? "打开小说图集" : "预览图片"}
                            >
                              {isNovelTask(task) ? <Images className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                            </button>
                            <button
                              type="button"
                              onClick={(event) => downloadImage(event, task)}
                              className="grid h-9 w-9 place-items-center rounded-full bg-white/90 text-gray-600 shadow-sm backdrop-blur transition hover:text-black"
                              aria-label="下载原图"
                            >
                              <Download className="h-4 w-4" />
                            </button>
                            <button
                              type="button"
                              onClick={(event) => deleteTask(event, task)}
                              className="grid h-9 w-9 place-items-center rounded-full bg-white/90 text-gray-500 shadow-sm backdrop-blur transition hover:bg-red-50 hover:text-red-500"
                              aria-label="删除历史记录"
                            >
                              <Trash2 className="h-4 w-4" />
                            </button>
                          </div>
                        </div>
                      </div>

                      <div className="p-5">
                        <div className="rounded-2xl bg-gray-50/80 p-3">
                          <div className="max-h-24 overflow-y-auto whitespace-pre-wrap break-words pr-1 text-[15px] font-black leading-7 text-gray-950">
                            {task.prompt || task.task_no}
                          </div>
                          {isTtsTask(task) && (
                            <div className="mt-2 inline-flex rounded-full bg-emerald-50 px-2.5 py-1 text-[11px] font-black text-emerald-600">
                              TTS 语音合成 · {String((task as any).mode) === "design" ? "音色设计" : String((task as any).mode) === "say" ? "音色复用" : String((task as any).mode) === "clone" ? "声音克隆" : "预设音色"}
                            </div>
                          )}
                          {isNovelTask(task) && (
                            <div className="mt-2 inline-flex rounded-full bg-[#6d5dfc]/10 px-2.5 py-1 text-[11px] font-black text-[#6d5dfc]">
                              小说可视化 · 工作台
                            </div>
                          )}
                        </div>

                        <div className="mt-4 rounded-2xl bg-gray-50 p-3">
                          <div className="mb-2 flex items-center justify-between text-[11px] font-bold text-gray-500">
                            <span>{statusText[task.status] || task.status}</span>
                            <span>{visibleProgress(task)}%</span>
                          </div>
                          <div className="h-2 overflow-hidden rounded-full bg-white">
                            <div
                              className={`h-full rounded-full ${progressClass(task.status)}`}
                              style={{ width: `${visibleProgress(task)}%` }}
                            />
                          </div>
                        </div>

                        {isPlaceholderTask(task) && !isTtsTask(task) && (
                          <div className="mt-3 rounded-2xl border border-amber-100 bg-amber-50 px-3 py-2 text-[11px] font-bold leading-5 text-amber-700">
                            {isNovelTask(task)
                              ? "占位图：未配置生图渠道，场景图是本地合成的渐变示意图，不是真实生图结果。"
                              : "占位图：未配置生图渠道，图片是本地合成的渐变示意图（不含画面内容），不是真实生图结果。"}
                          </div>
                        )}
                        {task.error_message && (
                          <div className="mt-4 rounded-2xl border border-rose-100 bg-rose-50 px-3 py-3 text-xs leading-6 text-rose-700">
                            {task.error_message}
                          </div>
                        )}

                        <div className="mt-4 flex items-center justify-between border-t border-gray-50 pt-4 text-[10px] font-bold uppercase tracking-tight text-gray-400">
                          <div className="flex items-center gap-1">
                            <Clock className="h-3 w-3" />
                            <span>{formatTime(task.submitted_at)}</span>
                          </div>
                          <span>
                            {isTtsTask(task)
                              ? `${((task as any).outputs || []).length || 0} 条音频`
                              : `${task.images?.filter((image) => image.status === "available").length || task.images?.length || 0} 张图片`}
                          </span>
                        </div>
                      </div>
                    </motion.article>
                  );
                })}
              </div>
            )}

            {pagination?.has_next && (
              <div className="flex justify-center pt-2">
                <button
                  type="button"
                  onClick={() => loadTasks((pagination.page || 1) + 1)}
                  disabled={loadingMore}
                  className="inline-flex h-11 cursor-pointer items-center gap-2 rounded-2xl border border-gray-100 bg-white px-5 text-sm font-bold text-gray-600 shadow-sm transition hover:border-gray-200 hover:text-black disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {loadingMore && <Loader2 className="h-4 w-4 animate-spin" />}
                  加载更多
                </button>
              </div>
            )}
          </>
        )}
      </div>

      <HistoryModals
        preview={preview}
        gallery={gallery}
        onClosePreview={() => setPreview(null)}
        onCloseGallery={() => setGallery(null)}
        onOpenTask={onOpenTask}
        onRetryNovelTask={retryTask}
        retryingTaskNo={retryingTaskNo}
      />
    </motion.div>
  );
}
