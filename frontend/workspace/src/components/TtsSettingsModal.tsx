import { useCallback, useEffect, useRef, useState } from "react";
import { motion } from "motion/react";
import {
  AudioLines,
  CircleStop,
  Cpu,
  FolderCog,
  HardDrive,
  Loader2,
  Play,
  RefreshCcw,
  ScrollText,
  Timer,
  TriangleAlert,
  X,
} from "lucide-react";
import {
  fetchTtsLog,
  fetchTtsStatus,
  releaseTtsMemory,
  startTtsEngine,
  stopTts,
  type TtsEngineSpec,
  type TtsStatus,
} from "../services/apiService";

interface TtsSettingsModalProps {
  onClose: () => void;
  addToast: (message: string, type?: "success" | "error" | "info") => void;
  onStatusChange?: (status: TtsStatus) => void;
}

const CAPABILITY_LABELS: Record<string, string> = {
  design: "音色设计",
  clone: "声音克隆",
  say: "音色复用",
  preset: "预设音色",
  voice_save: "音色保存",
  set_voice: "权重热切换",
};

function describeRefAudio(spec: TtsEngineSpec): string {
  if (!spec.ref_audio) return "无需参考音频";
  const { min_s: min, max_s: max, hard } = spec.ref_audio;
  const range = min && max ? `${min}~${max} 秒` : max ? `不超过 ${max} 秒` : min ? `至少 ${min} 秒` : "任意时长";
  return `参考音频：${range}${hard ? "（硬性限制）" : ""}`;
}

function describeSamplingRate(spec: TtsEngineSpec): string {
  if (!spec.output_sr) return "";
  if (typeof spec.output_sr === "number") return `${(spec.output_sr / 1000).toFixed(spec.output_sr % 1000 ? 2 : 0)}kHz 输出`;
  const entries = Object.entries(spec.output_sr);
  if (entries.length) return `输出采样率随版本：${entries.map(([version, rate]) => `${version}=${rate / 1000}k`).join(" / ")}`;
  return "";
}

export default function TtsSettingsModal({ onClose, addToast, onStatusChange }: TtsSettingsModalProps) {
  const [status, setStatus] = useState<TtsStatus | null>(null);
  const [logEngine, setLogEngine] = useState("qwen-base");
  const [logText, setLogText] = useState("");
  const [logError, setLogError] = useState("");
  const [isLoadingLog, setIsLoadingLog] = useState(false);
  const [isStarting, setIsStarting] = useState(false);
  const [isStopping, setIsStopping] = useState(false);
  const [isReleasing, setIsReleasing] = useState(false);
  const [startEngine, setStartEngine] = useState("qwen-base");
  const [keepResident, setKeepResident] = useState(false);
  const [notice, setNotice] = useState("");
  const wasRunningRef = useRef<string>("");
  const onStatusChangeRef = useRef(onStatusChange);
  const addToastRef = useRef(addToast);

  useEffect(() => {
    onStatusChangeRef.current = onStatusChange;
  }, [onStatusChange]);

  useEffect(() => {
    addToastRef.current = addToast;
  }, [addToast]);

  const applyStatus = useCallback((next: TtsStatus) => {
    setStatus(next);
    onStatusChangeRef.current?.(next);
    const current = next.instance?.engine || "";
    if (wasRunningRef.current && !current) {
      addToastRef.current("TTS 实例已停止，显存已释放", "info");
    } else if (!wasRunningRef.current && current) {
      addToastRef.current(`引擎 ${current} 已就绪`, "success");
    }
    wasRunningRef.current = current;
    if (!next.enabled) return;
  }, []);

  const refreshStatus = useCallback(async () => {
    try {
      applyStatus(await fetchTtsStatus());
    } catch {
      // 状态查询失败时保留上一次结果
    } finally {
      // nothing
    }
  }, [applyStatus]);

  const refreshLog = useCallback(async (engine: string) => {
    setIsLoadingLog(true);
    setLogError("");
    try {
      const report = await fetchTtsLog(engine, 60);
      setLogText(report.text || "（日志为空）");
    } catch (error: any) {
      setLogText("");
      setLogError(error?.message || "读取日志失败");
    } finally {
      setIsLoadingLog(false);
    }
  }, []);

  useEffect(() => {
    void refreshStatus();
    const timer = window.setInterval(() => {
      void refreshStatus();
    }, 4000);
    return () => window.clearInterval(timer);
  }, [refreshStatus]);

  useEffect(() => {
    void refreshLog(logEngine);
  }, [logEngine, refreshLog]);

  const handleStart = async () => {
    setIsStarting(true);
    setNotice(`正在启动 ${startEngine}（首次加载约 8~70 秒）…`);
    try {
      await startTtsEngine({ engine: startEngine, keep_resident: keepResident });
      setNotice(
        keepResident
          ? `启动命令已执行；${startEngine} 将常驻显存（不会自动退出），用完后记得点「释放内存」。`
          : `启动命令已执行；${startEngine} 加载完成后即可合成，空闲 ${status?.config.idle_timeout_seconds ?? 600} 秒自动释放。`,
      );
      await refreshStatus();
    } catch (error: any) {
      setNotice("");
      addToast(error?.message || "启动失败", "error");
    } finally {
      setIsStarting(false);
    }
  };

  const handleStop = async () => {
    setIsStopping(true);
    try {
      await stopTts();
      addToast("已停止实例", "success");
      await refreshStatus();
    } catch (error: any) {
      addToast(error?.message || "停止失败", "error");
    } finally {
      setIsStopping(false);
    }
  };

  const handleRelease = async () => {
    setIsReleasing(true);
    try {
      const result = await releaseTtsMemory();
      addToast(result.reason, result.released ? "success" : "info");
      await refreshStatus();
    } catch (error: any) {
      addToast(error?.message || "释放失败", "error");
    } finally {
      setIsReleasing(false);
    }
  };

  const running = Boolean(status?.instance);
  const instanceEngine = status?.instance?.engine || "";
  const gpu = status?.gpu;
  const vramFreeMb = gpu ? Math.max(0, gpu.vram_total_mb - gpu.vram_used_mb) : 0;

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
            <h2 className="text-xl font-black tracking-tight text-gray-950 sm:text-2xl">TTS 设置</h2>
            <p className="mt-1 text-[10px] font-black uppercase tracking-[0.3em] text-gray-300">本地引擎 · E:\AI\TTS</p>
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
          {!status?.enabled && (
            <section className="flex items-start gap-2 rounded-[1.6rem] border border-amber-100 bg-amber-50 p-4">
              <TriangleAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-600" />
              <div className="text-[11px] leading-5 text-amber-700">
                <div className="font-black text-amber-800">未找到 TTS CLI</div>
                <p className="mt-1">
                  {status?.error ||
                    "本板块依赖 E:\\AI\\TTS 契约环境。可在仓库根目录 .env.local 里用 TTS_PYTHON / TTS_CLI 指向其他位置，保存后重启 npm run local。"}
                </p>
              </div>
            </section>
          )}

          {/* 实例状态 */}
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex items-center gap-3">
                <span className={`h-2.5 w-2.5 rounded-full ${running ? "bg-emerald-400 animate-pulse" : "bg-gray-300"}`} />
                <div>
                  <div className="text-sm font-black text-gray-950">
                    {running ? `实例运行中 · ${instanceEngine}` : "暂无运行实例"}
                  </div>
                  <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-gray-400">
                    <AudioLines className="h-3 w-3" />
                    {status?.instance
                      ? `pid ${status.instance.pid ?? "?"} · 加载耗时 ${status.instance.load_s ?? "?"}s · 空闲 ${status.instance.idle_timeout_s ?? 0}s 自动退出`
                      : `CLI ${status?.cli.script || "未配置"}`}
                  </div>
                </div>
              </div>
              <button
                type="button"
                onClick={() => void refreshStatus()}
                className="inline-flex h-9 items-center gap-1.5 rounded-xl border border-gray-100 px-3 text-[11px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black"
              >
                <RefreshCcw className="h-3.5 w-3.5" />
                刷新状态
              </button>
            </div>

            {gpu && (
              <div className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
                <div className="rounded-2xl border border-gray-100 bg-gray-50/80 px-3.5 py-2.5">
                  <div className="flex items-center gap-1.5 text-[10px] font-black uppercase tracking-widest text-gray-400">
                    <Cpu className="h-3 w-3" /> 显卡
                  </div>
                  <div className="mt-1 truncate text-[12px] font-bold text-gray-700" title={gpu.name}>
                    {gpu.name}
                  </div>
                </div>
                <div className="rounded-2xl border border-gray-100 bg-gray-50/80 px-3.5 py-2.5">
                  <div className="flex items-center gap-1.5 text-[10px] font-black uppercase tracking-widest text-gray-400">
                    <HardDrive className="h-3 w-3" /> 显存
                  </div>
                  <div className="mt-1 text-[12px] font-bold text-gray-700">
                    已用 {gpu.vram_used_mb} / {gpu.vram_total_mb} MB · 空闲 {vramFreeMb} MB
                    {vramFreeMb < 5000 && !running ? " · 启动引擎前建议释放显存" : ""}
                  </div>
                </div>
              </div>
            )}

            <div className="mt-4 grid grid-cols-1 gap-2 sm:grid-cols-[1fr_auto_auto]">
              {!running && (
                <select
                  value={startEngine}
                  onChange={(event) => setStartEngine(event.target.value)}
                  className="h-11 rounded-2xl border border-gray-100 bg-gray-50 px-3 text-sm font-bold text-gray-800 outline-none focus:border-black/20"
                >
                  {(status?.engines || []).map((engine) => (
                    <option key={engine.id} value={engine.id}>
                      {engine.label}（{engine.capabilities.map((cap) => CAPABILITY_LABELS[cap] || cap).join(" / ")}）
                    </option>
                  ))}
                </select>
              )}
              {!running ? (
                <button
                  type="button"
                  onClick={() => void handleStart()}
                  disabled={!status?.enabled || isStarting}
                  className="flex h-11 items-center justify-center gap-2.5 rounded-2xl bg-[#6d5dfc] px-6 text-sm font-black text-white shadow-xl shadow-[#6d5dfc]/20 transition hover:-translate-y-0.5 hover:bg-[#5b4cf2] disabled:cursor-not-allowed disabled:opacity-60"
                >
                  {isStarting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
                  启动引擎
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => void handleStop()}
                  disabled={isStopping}
                  className="flex h-11 items-center justify-center gap-2.5 rounded-2xl bg-black px-6 text-sm font-black text-white shadow-xl shadow-black/10 transition hover:-translate-y-0.5 disabled:cursor-not-allowed disabled:opacity-60 sm:col-span-2 sm:justify-self-end"
                >
                  {isStopping ? <Loader2 className="h-4 w-4 animate-spin" /> : <CircleStop className="h-4 w-4" />}
                  停止实例（释放显存）
                </button>
              )}
            </div>

            {/* 常驻内存开关：启动时生效；运行中的实例要改行为请先停止再启动。 */}
            {!running && (
              <label className="mt-3 flex cursor-pointer items-start gap-2.5 rounded-2xl border border-gray-100 bg-gray-50/70 px-3.5 py-3">
                <input
                  type="checkbox"
                  checked={keepResident}
                  onChange={(event) => setKeepResident(event.target.checked)}
                  className="mt-0.5 h-3.5 w-3.5 accent-black"
                />
                <span className="text-[11px] leading-5 text-gray-600">
                  <b className="text-gray-800">让模型常驻内存</b>
                  （不设空闲超时）：连续合成时省掉每次 8~70 秒的加载，但显存会被一直占用；
                  用完记得点下方「释放内存」或「停止实例」。
                </span>
              </label>
            )}
            {running && status?.instance && !status.instance.idle_timeout_s && (
              <div className="mt-3 flex items-start gap-2 rounded-2xl border border-indigo-100 bg-indigo-50 px-3.5 py-2.5 text-[11px] leading-5 text-indigo-700">
                <Timer className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span>当前实例是常驻模式（不会自动退出）；连续任务打完后记得「停止实例」或「释放内存」。</span>
              </div>
            )}

            {/* 释放内存：TTS 侧 = 停掉常驻实例；有任务在跑时后端会拒绝。 */}
            <button
              type="button"
              onClick={() => void handleRelease()}
              disabled={isReleasing || !status?.enabled}
              className="mt-3 flex h-10 w-full cursor-pointer items-center justify-center gap-2 rounded-2xl border border-gray-200 bg-white text-[12px] font-black text-gray-700 transition hover:border-gray-300 hover:text-black disabled:cursor-not-allowed disabled:opacity-60"
            >
              {isReleasing ? <Loader2 className="h-4 w-4 animate-spin" /> : <HardDrive className="h-4 w-4" />}
              释放内存（停止 TTS 实例）
            </button>

            {notice ? (
              <div className="mt-3 whitespace-pre-line rounded-2xl border border-indigo-100 bg-indigo-50 px-3.5 py-2.5 text-[11px] leading-5 text-indigo-700">
                {notice}
              </div>
            ) : null}

            <p className="mt-3 text-[10px] leading-5 text-gray-400">
              8G 显存同一时刻只能驻留一个引擎；切换引擎时工作台会先弹确认（等价 --switch：先停当前引擎再加载目标引擎）。
              实例空闲 {status?.config.idle_timeout_seconds ?? 600} 秒后自动退出释放显存（TTS_IDLE_TIMEOUT_SECONDS 可调）。
            </p>
          </section>

          {/* 引擎清单 */}
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="mb-3 flex items-center gap-2 text-sm font-black text-gray-950">
              <AudioLines className="h-4 w-4 text-gray-400" />
              引擎清单
            </div>
            <div className="space-y-2">
              {(status?.engines || []).map((engine) => (
                <div key={engine.id} className="rounded-2xl border border-gray-100 bg-gray-50/70 px-3.5 py-3">
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <span className="text-[13px] font-black text-gray-900">{engine.label}</span>
                    <span className="flex items-center gap-1.5">
                      {engine.running && <span className="rounded-full bg-emerald-50 px-2 py-0.5 text-[10px] font-black text-emerald-600">运行中</span>}
                      {engine.python_ok === false && <span className="rounded-full bg-red-50 px-2 py-0.5 text-[10px] font-black text-red-500">缺环境</span>}
                      <span className="rounded-full bg-white px-2 py-0.5 text-[10px] font-black uppercase tracking-wider text-gray-400">
                        {engine.capabilities.map((cap) => CAPABILITY_LABELS[cap] || cap).join(" / ")}
                      </span>
                    </span>
                  </div>
                  <p className="mt-1 text-[11px] leading-5 text-gray-500">
                    {describeRefAudio(engine)}
                    {describeSamplingRate(engine) ? ` · ${describeSamplingRate(engine)}` : ""}
                  </p>
                  {engine.notes.length > 0 && (
                    <p className="mt-1 text-[10px] leading-4 text-gray-400">{engine.notes[0]}</p>
                  )}
                </div>
              ))}
            </div>
          </section>

          {/* 引擎日志 */}
          <section className="rounded-[1.6rem] border border-gray-100 bg-white p-4 shadow-sm">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2 text-sm font-black text-gray-950">
                <ScrollText className="h-4 w-4 text-gray-400" />
                引擎日志
              </div>
              <div className="flex items-center gap-2">
                <select
                  value={logEngine}
                  onChange={(event) => setLogEngine(event.target.value)}
                  className="h-8 rounded-xl border border-gray-100 bg-gray-50 px-2 text-[11px] font-bold text-gray-700 outline-none"
                >
                  {(status?.engines || []).map((engine) => (
                    <option key={engine.id} value={engine.id}>
                      {engine.id}
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  onClick={() => void refreshLog(logEngine)}
                  disabled={isLoadingLog}
                  className="inline-flex h-8 items-center gap-1.5 rounded-xl border border-gray-100 px-2.5 text-[11px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black disabled:opacity-50"
                >
                  {isLoadingLog ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCcw className="h-3.5 w-3.5" />}
                  重新读取
                </button>
              </div>
            </div>
            {logError ? (
              <div className="rounded-2xl border border-gray-100 bg-gray-50 px-3.5 py-2.5 text-[11px] leading-5 text-gray-500">
                {logError}（该引擎还没跑过时没有日志文件，属正常现象。）
              </div>
            ) : (
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded-2xl border border-gray-100 bg-gray-900 px-3.5 py-3 text-[10px] leading-5 text-gray-200">
                {logText || "…"}
              </pre>
            )}
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
                  单任务超时 {status ? `${Math.round(status.config.timeout_ms / 60000)} 分钟` : "30 分钟"}；首次加载引擎 8~70 秒（冷启动更久），常驻实例二次合成更快。
                </span>
              </li>
              <li className="flex items-start gap-2">
                <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-gray-300" />
                <span>
                  「Comfy 生图」与本板块共用同一块显卡：一边跑任务时另一边可能因显存不足失败，请避免同时生成。
                </span>
              </li>
              <li className="flex items-start gap-2">
                <AudioLines className="mt-0.5 h-3.5 w-3.5 shrink-0 text-gray-300" />
                <span>
                  「TTS 语音合成」板块只在本地开发者模式（<code className="rounded bg-gray-50 px-1">npm run local</code>）出现，线上部署不会显示。
                </span>
              </li>
            </ul>
          </section>
        </div>
      </motion.div>
    </div>
  );
}
