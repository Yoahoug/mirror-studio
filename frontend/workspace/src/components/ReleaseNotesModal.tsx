import { useEffect, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { BellRing, Check, X } from "lucide-react";

export interface ReleaseInfo {
  version: string;
  title: string;
  notes: string;
  publishedAt: string;
}

interface ReleaseNotesModalProps {
  release: ReleaseInfo;
  open: boolean;
  onClose: (dismissForCurrentVersion: boolean) => void;
}

export default function ReleaseNotesModal({ release, open, onClose }: ReleaseNotesModalProps) {
  const [dismissForCurrentVersion, setDismissForCurrentVersion] = useState(false);
  const noteLines = release.notes
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean);

  useEffect(() => {
    if (open) {
      setDismissForCurrentVersion(false);
    }
  }, [open, release.version]);

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          className="fixed inset-0 z-[120] flex items-center justify-center bg-black/30 px-4 py-6 backdrop-blur-sm"
        >
          <motion.div
            initial={{ opacity: 0, y: 18, scale: 0.97 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 12, scale: 0.98 }}
            transition={{ duration: 0.22, ease: "easeOut" }}
            className="w-full max-w-lg rounded-[2rem] border border-white/60 bg-white p-5 shadow-[0_30px_90px_rgba(15,23,42,0.18)] sm:p-6"
          >
            <div className="flex items-start justify-between gap-4">
              <div className="min-w-0">
                <div className="inline-flex items-center gap-2 rounded-full bg-emerald-50 px-3 py-1 text-[11px] font-black uppercase tracking-[0.18em] text-emerald-700">
                  <BellRing className="h-3.5 w-3.5" />
                  更新日志
                </div>
                <h3 className="mt-3 text-xl font-black tracking-tight text-gray-950">{release.title || "版本更新"}</h3>
                <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-gray-400">
                  <span className="rounded-full bg-gray-100 px-2.5 py-1 font-bold text-gray-600">{release.version}</span>
                  {release.publishedAt && <span>{release.publishedAt}</span>}
                </div>
              </div>
              <button
                type="button"
                onClick={() => onClose(dismissForCurrentVersion)}
                className="rounded-xl p-2 text-gray-400 transition hover:bg-gray-50 hover:text-black"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className="mt-5 space-y-2 rounded-[1.5rem] bg-[#f7f7f4] p-4">
              {noteLines.length ? (
                noteLines.map((item) => (
                  <div key={item} className="flex items-start gap-3 text-sm text-gray-700">
                    <span className="mt-0.5 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-black text-white">
                      <Check className="h-3 w-3" />
                    </span>
                    <span className="leading-6">{item}</span>
                  </div>
                ))
              ) : (
                <p className="text-sm leading-6 text-gray-600">本次版本已发布，欢迎开始体验。</p>
              )}
            </div>

            <label className="mt-5 flex cursor-pointer items-center gap-3 rounded-2xl border border-gray-100 px-4 py-3 text-sm text-gray-600">
              <input
                type="checkbox"
                checked={dismissForCurrentVersion}
                onChange={(event) => setDismissForCurrentVersion(event.target.checked)}
                className="h-4 w-4 rounded border-gray-300 text-black focus:ring-black/20"
              />
              本次更新日志永久关闭
            </label>

            <div className="mt-5 flex justify-end">
              <button
                type="button"
                onClick={() => onClose(dismissForCurrentVersion)}
                className="inline-flex h-11 items-center justify-center rounded-2xl bg-black px-5 text-sm font-black text-white transition hover:-translate-y-0.5"
              >
                我知道了
              </button>
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
