import { motion } from "motion/react";
import { Loader2, TriangleAlert } from "lucide-react";

import type { GpuLockHolder } from "../services/apiService";

// 显卡锁冲突弹窗：本机 8G 显存同一时刻只允许一类任务（ComfyUI 生图 / TTS 合成）在跑。
// 提交时撞上占用者就弹这里，让用户自己决定「等它完成」还是「终止对方」（后端 /api/gpu/terminate）。

interface GpuBusyDialogProps {
  holder: GpuLockHolder;
  /** 本次要提交的任务名（文案用，例如「TTS 合成」「ComfyUI 生图」）。 */
  taskLabel: string;
  terminating: boolean;
  onCancel: () => void;
  onTerminate: () => void;
}

function formatHeld(heldMs: number): string {
  const seconds = Math.max(0, Math.round((heldMs || 0) / 1000));
  if (seconds < 60) return `${seconds} 秒`;
  return `${Math.floor(seconds / 60)} 分 ${String(seconds % 60).padStart(2, "0")} 秒`;
}

export default function GpuBusyDialog({ holder, taskLabel, terminating, onCancel, onTerminate }: GpuBusyDialogProps) {
  const ownerLabel = holder.owner === "tts" ? "TTS 语音合成" : "ComfyUI 生图";
  const consequence = holder.owner === "tts"
    ? "将取消这条 TTS 任务并停止 TTS 实例（显存一并释放），正在进行的那次合成会作废。"
    : "将取消这条生图任务：从 ComfyUI 队列里删除并打断执行，对方任务会标记为已取消。";
  return (
    <div className="fixed inset-0 z-[130] flex items-center justify-center bg-black/40 p-4 backdrop-blur-sm">
      <motion.div
        initial={{ scale: 0.95, opacity: 0 }}
        animate={{ scale: 1, opacity: 1 }}
        exit={{ scale: 0.96, opacity: 0 }}
        transition={{ type: "spring", damping: 26, stiffness: 300 }}
        role="dialog"
        aria-modal="true"
        aria-label="显卡被占用"
        className="w-full max-w-md rounded-[2rem] bg-white p-6 shadow-2xl"
      >
        <div className="flex items-start gap-3">
          <div className="grid h-10 w-10 shrink-0 place-items-center rounded-2xl bg-amber-50">
            <TriangleAlert className="h-5 w-5 text-amber-500" />
          </div>
          <div className="min-w-0">
            <h3 className="text-base font-black text-gray-950">显卡正被占用</h3>
            <p className="mt-1.5 text-[12px] leading-5 text-gray-500">
              <b className="text-gray-800">{ownerLabel}</b>
              {holder.ref ? `（${holder.ref}）` : ""}
              正在使用显卡，已占用 <b className="text-gray-800">{formatHeld(holder.held_for_ms)}</b>。
              8G 显存同一时刻只能跑一类任务：本次{taskLabel}要么等它完成，要么终止它再开始。
            </p>
            <p className="mt-2 rounded-2xl bg-gray-50 px-3 py-2 text-[11px] leading-5 text-gray-500">
              选「终止对方并继续」：{consequence}
            </p>
          </div>
        </div>
        <div className="mt-5 flex flex-wrap justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            disabled={terminating}
            className="h-10 rounded-2xl border border-gray-100 px-4 text-[12px] font-bold text-gray-500 transition hover:border-gray-300 hover:text-black disabled:cursor-not-allowed disabled:opacity-50"
          >
            取消（等它完成）
          </button>
          <button
            type="button"
            onClick={onTerminate}
            disabled={terminating}
            className="inline-flex h-10 items-center gap-2 rounded-2xl bg-rose-600 px-4 text-[12px] font-black text-white shadow-lg shadow-rose-600/20 transition hover:bg-rose-500 disabled:cursor-not-allowed disabled:opacity-70"
          >
            {terminating ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
            终止对方并继续
          </button>
        </div>
      </motion.div>
    </div>
  );
}
