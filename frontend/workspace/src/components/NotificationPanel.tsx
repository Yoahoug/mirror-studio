import { useMemo, useState } from "react";
import { motion, AnimatePresence } from "motion/react";
import { Bell, CheckCircle2, ChevronLeft, History, Sparkles } from "lucide-react";
import { type ReleaseInfo } from "./ReleaseNotesModal";
import { type AccountSummaryResponse } from "../services/apiService";

export const READ_NOTIFICATIONS_KEY = "mirror.notifications.read.v1";

interface NotificationItem {
  id: string;
  title: string;
  desc: string;
  time: string;
  icon: typeof Sparkles;
  color: string;
  content: string;
  actionLabel?: string;
  action?: () => void;
}

function formatTime(value?: string | null) {
  if (!value) return "刚刚";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "刚刚";
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function statusText(status: string) {
  switch (status) {
    case "queued":
      return "任务已进入排队队列";
    case "running":
      return "任务正在生成中";
    case "succeeded":
      return "任务已生成完成";
    case "failed":
      return "任务执行失败，请检查提示词或稍后重试";
    case "canceled":
      return "任务已取消";
    default:
      return `任务状态：${status}`;
  }
}

export function readStoredNotificationIds() {
  try {
    const raw = window.localStorage.getItem(READ_NOTIFICATIONS_KEY);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : [];
  } catch {
    return [];
  }
}

export default function NotificationPanel({
  onClose,
  accountSummary,
  releaseInfo,
  onOpenHistory,
  onOpenTask,
  readIds,
  onReadIdsChange,
}: {
  onClose: () => void;
  accountSummary?: AccountSummaryResponse | null;
  releaseInfo?: ReleaseInfo | null;
  onOpenHistory?: () => void;
  onOpenTask?: (taskNo: string) => void;
  readIds: string[];
  onReadIdsChange: (ids: string[]) => void;
}) {
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const persistReadIds = (ids: string[]) => {
    onReadIdsChange(ids);
    window.localStorage.setItem(READ_NOTIFICATIONS_KEY, JSON.stringify(ids));
  };

  const notifications = useMemo<NotificationItem[]>(() => {
    const items: NotificationItem[] = [];

    if (releaseInfo) {
      const firstLine = releaseInfo.notes.split(/\r?\n/).map((item) => item.trim()).find(Boolean) || releaseInfo.title;
      items.push({
        id: `release:${releaseInfo.version}`,
        title: `${releaseInfo.version} 已上线`,
        desc: firstLine,
        time: formatTime(releaseInfo.publishedAt),
        icon: Sparkles,
        color: "bg-amber-50 text-amber-500",
        content: releaseInfo.notes || `${releaseInfo.title} 已发布。`,
        actionLabel: "关闭面板",
        action: onClose,
      });
    }

    if (accountSummary?.quota) {
      items.push({
        id: "quota:summary",
        title: "配额状态",
        desc: `剩余 ${accountSummary.quota.available_quota}，冻结 ${accountSummary.quota.frozen_quota}，今日总额 ${accountSummary.quota.daily_quota}`,
        time: accountSummary.quota.last_reset_date ? `重置于 ${formatTime(accountSummary.quota.last_reset_date)}` : "实时数据",
        icon: CheckCircle2,
        color: "bg-emerald-50 text-emerald-500",
        content: `当前账户可用次数 ${accountSummary.quota.available_quota}，冻结 ${accountSummary.quota.frozen_quota}，累计成功生成 ${accountSummary.quota.total_generated_count} 张。`,
        actionLabel: "查看历史记录",
        action: onOpenHistory,
      });
    }

    for (const task of accountSummary?.recent_tasks || []) {
      items.push({
        id: `task:${task.task_no}`,
        title: `最近任务 ${task.task_no.slice(-8)}`,
        desc: statusText(task.status),
        time: formatTime(task.created_at),
        icon: History,
        color: "bg-blue-50 text-blue-500",
        content: `任务号 ${task.task_no}\n${statusText(task.status)}`,
        actionLabel: "进入会话",
        action: () => onOpenTask?.(task.task_no),
      });
    }

    return items;
  }, [accountSummary, onClose, onOpenHistory, onOpenTask, releaseInfo]);

  const selectedNotification = notifications.find((item) => item.id === selectedId) || null;

  const markAsRead = (id: string) => {
    if (readIds.includes(id)) return;
    persistReadIds([...readIds, id]);
  };

  const handleSelect = (id: string) => {
    markAsRead(id);
    setSelectedId(id);
  };

  const markAllAsRead = () => {
    persistReadIds(notifications.map((item) => item.id));
  };

  return (
    <motion.div
      initial={{ opacity: 0, y: 10, scale: 0.95 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: 10, scale: 0.95 }}
      className="fixed inset-x-3 top-[5.25rem] z-50 flex max-h-[calc(100vh-6.5rem)] flex-col overflow-hidden rounded-[2rem] border border-gray-100 bg-white shadow-2xl neumorph-subtle sm:inset-x-auto sm:right-6 sm:top-[4.5rem] sm:max-h-[30rem] sm:w-96"
      onClick={(event) => event.stopPropagation()}
    >
      <AnimatePresence mode="wait">
        {!selectedId ? (
          <motion.div
            key="list"
            initial={{ x: -20, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            exit={{ x: -20, opacity: 0 }}
            className="flex min-h-0 flex-1 flex-col"
          >
            <div className="flex items-center justify-between border-b border-gray-50 bg-gray-50/30 p-5 sm:p-6">
              <div>
                <h3 className="font-bold leading-none text-gray-900">通知中心</h3>
                <p className="mt-1 text-[10px] font-bold uppercase tracking-widest italic text-gray-400">Notifications</p>
              </div>
              <button
                type="button"
                onClick={markAllAsRead}
                className="text-[11px] font-bold uppercase tracking-tighter text-gray-300 transition-colors hover:text-black sm:text-xs"
              >
                全部标记已读
              </button>
            </div>

            <div className="flex-1 overflow-y-auto overscroll-contain">
              {notifications.length ? notifications.map((item) => {
                const unread = !readIds.includes(item.id);
                return (
                  <button
                    key={item.id}
                    type="button"
                    onClick={() => handleSelect(item.id)}
                    className="group flex w-full cursor-pointer border-b border-gray-50 p-4 text-left transition-colors hover:bg-gray-50 sm:p-5"
                  >
                    <div className="flex w-full gap-3 sm:gap-4">
                      <div className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-2xl ${item.color}`}>
                        <item.icon className="h-5 w-5 stroke-[1.8]" />
                      </div>
                      <div className="flex-1 space-y-1">
                        <div className="flex items-center justify-between gap-3">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-bold text-gray-900">{item.title}</span>
                            {unread && <span className="h-2 w-2 rounded-full bg-rose-500" />}
                          </div>
                          <span className="text-[10px] font-bold italic text-gray-300">{item.time}</span>
                        </div>
                        <p className="line-clamp-2 text-xs leading-relaxed text-gray-500">{item.desc}</p>
                      </div>
                    </div>
                  </button>
                );
              }) : (
                <div className="grid min-h-52 place-items-center p-6 text-center text-sm text-gray-400">
                  当前没有新的系统通知。
                </div>
              )}
            </div>

            <div className="bg-gray-50/30 p-4 text-center">
              <button
                type="button"
                onClick={onOpenHistory}
                className="group mx-auto flex items-center justify-center gap-1 text-xs font-bold text-gray-400 transition-all hover:text-black"
              >
                <span>查看历史记录</span>
                <div className="h-1.5 w-1.5 rounded-full bg-gray-200 transition-colors group-hover:bg-black" />
              </button>
            </div>
          </motion.div>
        ) : (
          <motion.div
            key="preview"
            initial={{ x: 20, opacity: 0 }}
            animate={{ x: 0, opacity: 1 }}
            exit={{ x: 20, opacity: 0 }}
            className="flex min-h-0 flex-1 flex-col space-y-6 overflow-y-auto p-5 overscroll-contain sm:space-y-8 sm:p-8"
          >
            <button
              type="button"
              onClick={() => setSelectedId(null)}
              className="group flex items-center gap-2 text-xs font-bold text-gray-400 transition-colors hover:text-black"
            >
              <ChevronLeft className="h-4 w-4 transition-transform group-hover:-translate-x-1" />
              返回列表
            </button>

            <div className="space-y-6">
              <div className="flex items-center gap-4">
                <div className={`flex h-14 w-14 items-center justify-center rounded-[1.5rem] ${selectedNotification?.color}`}>
                  {selectedNotification && <selectedNotification.icon className="h-7 w-7" />}
                </div>
                <div className="space-y-1">
                  <h4 className="text-xl font-bold text-gray-900">{selectedNotification?.title}</h4>
                  <p className="text-xs font-bold text-gray-400">{selectedNotification?.time} · 实时状态</p>
                </div>
              </div>

              <div className="rounded-[2rem] border border-gray-100 bg-gray-50 p-5 sm:p-6">
                <p className="whitespace-pre-wrap text-sm leading-relaxed text-gray-600">{selectedNotification?.content}</p>
              </div>

              {selectedNotification?.action && (
                <button
                  type="button"
                  onClick={selectedNotification.action}
                  className="flex w-full items-center justify-center gap-2 rounded-2xl bg-black py-4 text-xs font-bold text-white shadow-xl shadow-black/10 transition-all hover:scale-[1.02] active:scale-[0.98]"
                >
                  {selectedNotification.actionLabel || "立即处理"}
                  <Bell className="h-4 w-4" />
                </button>
              )}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  );
}
