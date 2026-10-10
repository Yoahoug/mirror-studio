import { useCallback, useEffect, useRef, useState } from "react";
import { motion } from "motion/react";
import {
  Cpu,
  FolderCog,
  HardDrive,
  Layers,
  Loader2,
  MemoryStick,
  Play,
  RefreshCcw,
  ScrollText,
  Server,
  Timer,
  TriangleAlert,
  X,
} from "lucide-react";
import {
  fetchComfyLog,
  fetchComfyStatus,
  freeComfyMemory,
  startComfy,
  type ComfyLogReport,
  type ComfyStatus,
} from "../services/apiService";

interface ComfySettingsModalProps {
  onClose: () => void;
  addToast: (message: string, type?: "success" | "error" | "info") => void;
  onStatusChange?: (status: ComfyStatus) => void;
}

function formatBytes(value: number): string {
  if (!value) return "--";
  const gb = value / 1024 ** 3;
  return `${gb.toFixed(1)} GB`;
}

function formatMinutes(ms: number): string {
  return `${Math.max(1, Math.round(ms / 60000))} 分钟`;
}

export default function ComfySettingsModal({ onClose, addToast, onStatusChange }: ComfySettingsModalProps) {
  const [status, setStatus] = useState<ComfyStatus | null>(null);
  const [logReport, setLogReport] = useState<ComfyLogReport | null>(null);
  const [isLoadingStatus, setIsLoadingStatus] = useState(true);
  const [isLoadingLog, setIsLoadingLog] = useState(true);
  const [isStarting, setIsStarting] = useState(false);
  const [isReleasing, setIsReleasing] = useState(false);
  const [notice, setNotice] = useState("");
  const wasRunningRef = useRef<boolean | null>(null);
  const onStatusChangeRef = useRef(onStatusChange);
  const addToastRef = useRef(addToast);

  useEffect(() => {
    onStatusChangeRef.current = onStatusChange;
  }, [onStatusChange]);

  useEffect(() => {
    addToastRef.current = addToast;
  }, [addToast]);

  const applyStatus = useCallback((next: ComfyStatus) => {
    setStatus(next);
    onStatusChangeRef.current?.(next);
    if (wasRunningRef.current === false && next.running) {
      addToastRef.current("ComfyUI 已就绪，可以开始生成", "success");
      setNotice("");
    }
    wasRunningRef.current = next.running;
  }, []);

  const refreshStatus = useCallback(async () => {
    try {
      applyStatus(await fetchComfyStatus());
    } catch {
      // 状态查询失败时保留上一次结果
    } finally {
      setIsLoadingStatus(false);
    }
  }, [applyStatus]);

  const refreshLog = useCallback(async () => {
    setIsLoadingLog(true);
    try {
      setLogReport(await fetchComfyLog(8));
    } catch {
      setLogReport(null);
    } finally {
      setIsLoadingLog(false);
    }
  }, []);

  const refreshAll = useCallback(async () => {
    await Promise.all([refreshStatus(), refreshLog()]);
  }, [refreshStatus, refreshLog]);

  useEffect(() => {
    void refreshAll();
    const timer = window.setInterval(() => {
      void refreshStatus();
    }, 3000);
    return () => window.clearInterval(timer);
  }, [refreshAll, refreshStatus]);

  const handleStart = async () => {
    setIsStarting(true);
    setNotice("正在执行启动命令…");
    try {
      const result = await startComfy();
      applyStatus(result.status);
      if (result.status.running) {
        setNotice("ComfyUI 已就绪。");
        addToast("ComfyUI 已启动", "success");
      } else {
        setNotice("启动命令已执行，正在等待 ComfyUI 就绪；首次加载模型通常需要 1-2 分钟，请保持本窗口打开。");
      }
      if (result.comfy_issues) {
        setNotice((current) => `${current}\n启动过程中 ComfyUI 日志里出现异常：${result.comfy_issues}`);
      }
      void refreshLog();
    } catch (error: any) {
      setNotice("");
      addToast(error?.message || "启动 ComfyUI 失败", "error");
    } finally {
      setIsStarting(false);
    }
  };

  const handleFreeMemory = async () => {
    setIsReleasing(true);
    try {
      const result = await freeComfyMemory();
      applyStatus(result.status);
      addToast("已释放 ComfyUI 内存（模型卸载，下次出图会重新加载）", "success");
    } catch (error: any) {
      addToast(error?.message || "释放内存失败", "error");
    } finally {
      setIsReleasing(false);
    }
  };

  const running = Boolean(status?.running);
  const canStart = Boolean(status?.start_command_configured);

  return (
    <div className="fixed inset-0 z-[110] flex items-center justify-center bg-black/40 p-0 backdrop-blur-sm sm:p-4">
      <motion.div
        initial={{ scale: 0.96, opacity: 0, y: 20 }}
        animate={{ scale: 1, opacity: 1, y: 0 }}
        exit={{ scale: 0.97, opacity: 0, y: 20 }}
        transition={{ type: "spring", damping: 26, stiffness: 280 }}
        className="flex h-[100dvh] w-full max-w-2xl flex-col overflow-hidden bg-white shadow-2xl sm:h-auto sm:max-h-[88vh] sm:rounded-[2rem]"
      >
        <header className="flex items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 sm:px-7 sm:py-5">
          <div>
            <h2 className="text-xl font-black tracking-tight text-gray-950 sm:text-2xl">Comfy 设置</h2>
            <p className="mt-1 text-[10px] font-black uppercase tracking-[0.3em] text-gray-300">ComfyUI · 本地引擎</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-xl p-2 text-gray-400 transition hover:bg-gray-50 hover:text-black"
            aria-label="关闭"
          >
            <X className="h-5 w-5" />
          </button>
        </header>

        <div className="flex-1 space-y-4 overflow-y-auto px-5 py-5 sm:px-7">
          {/* 运行状态 + 启动按钮 */}
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-3">
                <span className={`h-2.5 w-2.5 rounded-full ${running ? "bg-emerald-400 animate-pulse" : "bg-gray-300"}`} />
                <div>
                  <div className="text-sm font-black text-gray-950">{running ? "ComfyUI 运行中" : "ComfyUI 未运行"}</div>
                  <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-gray-400">
                    <Server className="h-3 w-3" />
                    {status?.base_url || "http://127.0.0.1:8188"}
                    {status?.stats?.comfyui_version ? ` · ComfyUI ${status.stats.comfyui_version}` : ""}
                  </div>
                </div>
              </div>
              <button
                type="button"
                onClick={() => void refreshAll()}
                className="inline-flex h-9 items-center gap-1.5 rounded-xl border border-gray-100 px-3 text-[11px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black"
              >
                <RefreshCcw className="h-3.5 w-3.5" />
                刷新状态
              </button>
            </div>

            {!running && status?.error ? (
              <div className="mt-3 rounded-2xl border border-gray-100 bg-gray-50 px-3.5 py-2.5 text-[11px] leading-5 text-gray-500">
                探测结果：{status.error}
              </div>
            ) : null}

            {running && status?.stats ? (
              <div className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
                <div className="rounded-2xl border border-gray-100 bg-gray-50/80 px-3.5 py-2.5">
                  <div className="flex items-center gap-1.5 text-[10px] font-black uppercase tracking-widest text-gray-400">
                    <HardDrive className="h-3 w-3" /> 内存
                  </div>
                  <div className="mt-1 text-[12px] font-bold text-gray-700">
                    空闲 {formatBytes(status.stats.ram_free)} / 共 {formatBytes(status.stats.ram_total)}
                  </div>
                </div>
                {(status.stats.devices || []).slice(0, 2).map((device) => (
                  <div key={device.name} className="rounded-2xl border border-gray-100 bg-gray-50/80 px-3.5 py-2.5">
                    <div className="flex items-center gap-1.5 text-[10px] font-black uppercase tracking-widest text-gray-400">
                      <Cpu className="h-3 w-3" /> 显存
                    </div>
                    <div className="mt-1 truncate text-[12px] font-bold text-gray-700" title={device.name}>
                      {device.name.split(":")[0]} · 空闲 {formatBytes(device.vram_free)} / 共 {formatBytes(device.vram_total)}
                    </div>
                  </div>
                ))}
              </div>
            ) : null}

            <button
              type="button"
              onClick={() => void handleStart()}
              disabled={running || isStarting || isLoadingStatus}
              className="mt-4 flex h-11 w-full cursor-pointer items-center justify-center gap-2.5 rounded-2xl bg-[#6d5dfc] text-sm font-black text-white shadow-xl shadow-[#6d5dfc]/20 transition hover:-translate-y-0.5 hover:bg-[#5b4cf2] disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isStarting || isLoadingStatus ? <Loader2 className="h-4.5 w-4.5 animate-spin" /> : <Play className="h-4.5 w-4.5" />}
              {running ? "ComfyUI 已运行" : isStarting ? "正在启动 ComfyUI" : "启动 ComfyUI"}
            </button>

            {running ? (
              <button
                type="button"
                onClick={() => void handleFreeMemory()}
                disabled={isReleasing}
                className="mt-2 flex h-10 w-full cursor-pointer items-center justify-center gap-2 rounded-2xl border border-gray-100 bg-white text-xs font-black text-gray-500 transition hover:border-gray-200 hover:text-black disabled:cursor-not-allowed disabled:opacity-60"
                title="让 ComfyUI 卸载模型、归还内存（约 16G；下次出图会重新加载）"
              >
                {isReleasing ? <Loader2 className="h-4 w-4 animate-spin" /> : <MemoryStick className="h-4 w-4" />}
                {isReleasing ? "释放中" : "释放内存（卸载模型）"}
              </button>
            ) : null}

            {notice ? (
              <div className="mt-3 whitespace-pre-line rounded-2xl border border-indigo-100 bg-indigo-50 px-3.5 py-2.5 text-[11px] leading-5 text-indigo-700">
                {notice}
              </div>
            ) : null}

            {!canStart && !running && !isLoadingStatus ? (
              <div className="mt-3 flex items-start gap-2 rounded-2xl border border-amber-100 bg-amber-50 px-3.5 py-2.5 text-[11px] leading-5 text-amber-700">
                <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span>
                  还没有配置启动命令：在仓库根目录的 <code className="rounded bg-white/70 px-1">.env.local</code> 里设置
                  <code className="mx-1 rounded bg-white/70 px-1">COMFY_START_CMD</code>
                  （可以直接填 <code className="rounded bg-white/70 px-1">start-comfyui.bat</code> 的完整路径），保存后重启
                  <code className="mx-1 rounded bg-white/70 px-1">npm run local</code>；也可以手动启动 ComfyUI 后点「刷新状态」。
                </span>
              </div>
            ) : null}
          </section>

          {/* ComfyUI 服务日志异常：自定义节点 / Manager 联网 / Traceback 等只写在日志里，任务状态看不到 */}
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="mb-3 flex items-center justify-between gap-3">
              <div className="flex items-center gap-2 text-sm font-black text-gray-950">
                <ScrollText className="h-4 w-4 text-gray-400" />
                ComfyUI 日志异常
              </div>
              <button
                type="button"
                onClick={() => void refreshLog()}
                disabled={isLoadingLog}
                className="inline-flex h-8 items-center gap-1.5 rounded-xl border border-gray-100 px-2.5 text-[11px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black disabled:opacity-50"
              >
                {isLoadingLog ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCcw className="h-3.5 w-3.5" />}
                重新读取
              </button>
            </div>

            {isLoadingLog && !logReport ? (
              <div className="text-[11px] text-gray-400">正在读取 ComfyUI 日志…</div>
            ) : !logReport?.available ? (
              <div className="rounded-2xl border border-gray-100 bg-gray-50 px-3.5 py-2.5 text-[11px] leading-5 text-gray-500">
                {logReport?.error
                  ? `读取失败：${logReport.error}`
                  : "ComfyUI 未运行或版本不支持日志接口；启动后点「重新读取」即可看到服务端报错。"}
              </div>
            ) : (
              <>
                <div className="flex flex-wrap items-center gap-2 text-[11px]">
                  <span className={`rounded-full px-2 py-0.5 font-black ${logReport.errors ? "bg-red-50 text-red-600" : "bg-gray-100 text-gray-400"}`}>
                    错误 {logReport.errors}
                  </span>
                  <span className={`rounded-full px-2 py-0.5 font-black ${logReport.warnings ? "bg-amber-50 text-amber-700" : "bg-gray-100 text-gray-400"}`}>
                    警告 {logReport.warnings}
                  </span>
                  <span className="text-gray-400">本次读取 {logReport.total_lines} 行日志</span>
                </div>
                {logReport.issues.length ? (
                  <ul className="mt-3 space-y-2">
                    {logReport.issues.map((issue, index) => (
                      <li
                        key={`${issue.at}-${index}`}
                        className={`rounded-2xl border px-3.5 py-2.5 ${
                          issue.level === "error" ? "border-red-100 bg-red-50/60" : "border-amber-100 bg-amber-50/60"
                        }`}
                      >
                        <div className="flex items-center gap-2">
                          <span className={`text-[10px] font-black uppercase tracking-widest ${issue.level === "error" ? "text-red-500" : "text-amber-600"}`}>
                            {issue.level === "error" ? "错误" : "警告"}
                          </span>
                          {issue.at ? <span className="text-[10px] text-gray-400">{issue.at.replace("T", " ").slice(0, 19)}</span> : null}
                        </div>
                        <p className="mt-1 break-words text-[11px] leading-5 text-gray-700" title={issue.message}>
                          {issue.message}
                        </p>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <div className="mt-3 rounded-2xl border border-emerald-100 bg-emerald-50 px-3.5 py-2.5 text-[11px] text-emerald-700">
                    未发现错误或警告。
                  </div>
                )}
                <p className="mt-3 text-[10px] leading-5 text-gray-400">
                  这里统计的是 ComfyUI 服务日志里的 ERROR / WARNING（自定义节点、Manager 联网、模型加载等），
                  它们不会进入任务状态；生图任务本身的报错会直接显示在任务与历史记录里。
                </p>
              </>
            )}
          </section>

          {/* 模型与工作流 */}
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="mb-3 flex items-center gap-2 text-sm font-black text-gray-950">
              <Layers className="h-4 w-4 text-gray-400" />
              模型与工作流
            </div>
            <div className="space-y-2">
              {(status?.models || []).map((model) => (
                <div key={model.id} className="rounded-2xl border border-gray-100 bg-gray-50/70 px-3.5 py-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="text-[13px] font-black text-gray-900">{model.label}</span>
                    <span className="rounded-full bg-white px-2 py-0.5 text-[10px] font-black uppercase tracking-wider text-gray-400">
                      {model.steps_mode === "fixed" ? "固定 6 步" : `${model.default_steps} / ${model.max_steps} 步`}
                    </span>
                  </div>
                  <p className="mt-1 text-[11px] leading-5 text-gray-500">{model.note}</p>
                  <p className="mt-1 truncate text-[10px] text-gray-400" title={model.unet}>
                    {model.unet}
                  </p>
                </div>
              ))}
            </div>
          </section>

          {/* 说明 */}
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="mb-3 flex items-center gap-2 text-sm font-black text-gray-950">
              <FolderCog className="h-4 w-4 text-gray-400" />
              说明
            </div>
            <ul className="space-y-2 text-[11px] leading-5 text-gray-500">
              <li className="flex items-start gap-2">
                <Timer className="mt-0.5 h-3.5 w-3.5 shrink-0 text-gray-300" />
                <span>
                  单张超时 {status ? formatMinutes(status.timeout_ms) : "15 分钟"}
                  {status?.free_memory ? "；已开启每张前卸载模型（COMFY_FREE_MEMORY=1），出图会更慢" : ""}。
                </span>
              </li>
              <li className="flex items-start gap-2">
                <Layers className="mt-0.5 h-3.5 w-3.5 shrink-0 text-gray-300" />
                <span>
                  图生图沿用文生图工作流就地改造：参考图接到编码器，出图比例跟随参考图（与所选比例无关），
                  当前板块支持 1 张参考图（ComfyUI 契约上限 16 张）。
                </span>
              </li>
              <li className="flex items-start gap-2">
                <Server className="mt-0.5 h-3.5 w-3.5 shrink-0 text-gray-300" />
                <span>
                  「Comfy 生图」板块只在本地开发者模式（<code className="rounded bg-gray-50 px-1">npm run local</code>）出现，
                  线上部署不会显示，也不会启动本机进程。
                </span>
              </li>
            </ul>
          </section>
        </div>
      </motion.div>
    </div>
  );
}
