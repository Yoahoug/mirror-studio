import React, { useCallback, useEffect, useState } from 'react';
import { motion } from 'motion/react';
import { Activity, Layers, Users, Zap } from 'lucide-react';
import { Card, Skeleton, Badge, ErrorState, EmptyState } from '@shared/ui';
import { useLanguage } from '@shared/i18n';
import {
  fetchConsoleDashboard,
  type DashboardData,
} from '@shared/services/console';

type ConsoleTab = 'overview' | 'users' | 'tasks' | 'channels' | 'settings';

export function OverviewView({ onNavigate }: { onNavigate: (tab: ConsoleTab) => void }) {
  const { t } = useLanguage();
  const [data, setData] = useState<DashboardData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetchConsoleDashboard();
      setData(res);
    } catch (err: any) {
      setError(err?.message || t.common.errorDesc);
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  if (loading) {
    return (
      <div className="space-y-6">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <Skeleton key={i} className="h-28 w-full" />
          ))}
        </div>
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <Skeleton className="h-72 w-full" />
          <Skeleton className="h-72 w-full" />
        </div>
      </div>
    );
  }

  if (error || !data) {
    return <ErrorState message={error} onRetry={loadData} />;
  }

  const successRate = data.today?.tasks
    ? Math.round((data.today.succeeded_tasks / data.today.tasks) * 100)
    : 0;

  const cards = [
    {
      title: t.console.overview.todayTasks,
      value: data.today?.tasks ?? 0,
      sub: `${t.console.overview.successCount} ${data.today?.succeeded_tasks ?? 0} · ${t.console.overview.failedCount} ${data.today?.failed_tasks ?? 0}`,
      icon: <Layers className="h-5 w-5 text-[var(--color-accent)]" />,
      onClick: () => onNavigate('tasks'),
    },
    {
      title: t.console.overview.totalTasks,
      value: data.totals?.tasks ?? 0,
      sub: `${t.console.overview.totalImages} ${data.totals?.images ?? 0}`,
      icon: <Activity className="h-5 w-5 text-[var(--color-success)]" />,
      onClick: () => onNavigate('tasks'),
    },
    {
      title: t.console.overview.totalUsers,
      value: data.totals?.users ?? 0,
      sub: `${t.console.overview.todayNewUsers} ${data.today?.new_users ?? 0}`,
      icon: <Users className="h-5 w-5 text-[var(--color-info)]" />,
      onClick: () => onNavigate('users'),
    },
    {
      title: t.console.overview.activeChannels,
      value: data.totals?.active_channels ?? 0,
      sub: `${t.console.overview.channelsOnline} · ${successRate}% ${t.console.overview.successCount}`,
      icon: <Zap className="h-5 w-5 text-[var(--color-warning)]" />,
      onClick: () => onNavigate('channels'),
    },
  ];

  return (
    <div className="space-y-6">
      {/* 核心指标卡片 */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {cards.map((card, idx) => (
          <motion.div
            key={card.title}
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: idx * 0.05, duration: 0.2 }}
          >
            <Card
              hoverEffect
              onClick={card.onClick}
              className="cursor-pointer p-4 sm:p-5"
            >
              <div className="flex items-center justify-between">
                <span className="text-xs font-bold text-[var(--color-muted)]">{card.title}</span>
                <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-[var(--color-surface-sunken)]">
                  {card.icon}
                </div>
              </div>
              <div className="mt-3 text-2xl font-black tracking-tight text-[var(--color-ink)] tabular-nums sm:text-3xl">
                {card.value.toLocaleString()}
              </div>
              <p className="mt-1 text-xs text-[var(--color-submuted)]">{card.sub}</p>
            </Card>
          </motion.div>
        ))}
      </div>

      {/* 实时列表分区 */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        {/* 最新任务 */}
        <Card className="p-5">
          <div className="mb-4 flex items-center justify-between">
            <h3 className="text-sm font-bold text-[var(--color-ink)]">{t.console.overview.latestTasks}</h3>
            <button
              type="button"
              onClick={() => onNavigate('tasks')}
              className="text-xs font-semibold text-[var(--color-accent)] hover:underline"
            >
              {t.console.overview.viewAll}
            </button>
          </div>
          {(!data.latest_tasks || data.latest_tasks.length === 0) ? (
            <EmptyState
              icon={<Layers className="h-8 w-8 text-[var(--color-submuted)]" />}
              title={t.console.overview.noTasks}
              className="py-6"
            />
          ) : (
            <div className="space-y-2.5">
              {(data.latest_tasks || []).slice(0, 5).map((task) => (
                <div
                  key={task.id}
                  className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-3"
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-xs font-bold text-[var(--color-ink)]">
                          {task.task_no}
                        </span>
                        <Badge
                          size="sm"
                          variant={
                            task.status === 'succeeded'
                              ? 'success'
                              : task.status === 'failed'
                                ? 'danger'
                                : task.status === 'running'
                                  ? 'accent'
                                  : 'neutral'
                          }
                        >
                          {task.status}
                        </Badge>
                      </div>
                      <p className="mt-1 truncate text-xs text-[var(--color-muted)]">
                        {task.prompt || t.console.overview.noPrompt}
                      </p>
                    </div>
                    <span className="flex-shrink-0 text-[11px] text-[var(--color-submuted)] tabular-nums">
                      {task.created_at ? new Date(task.created_at).toLocaleTimeString() : ''}
                    </span>
                  </div>
                  {task.error_message && (
                    <p className="mt-2 rounded-lg bg-[var(--color-danger-light)] px-2 py-1 text-[11px] text-[var(--color-danger-text)]">
                      {task.error_message}
                    </p>
                  )}
                </div>
              ))}
            </div>
          )}
        </Card>

        <div className="space-y-6">
          {/* 最新失败日志 */}
          <Card className="p-5">
            <div className="mb-4 flex items-center justify-between">
              <div>
                <h3 className="text-sm font-bold text-[var(--color-ink)]">{t.console.overview.recentFailures}</h3>
                <p className="mt-0.5 text-[11px] text-[var(--color-muted)]">{t.console.overview.failureHint}</p>
              </div>
              <button
                type="button"
                onClick={() => onNavigate('tasks')}
                className="text-xs font-semibold text-[var(--color-accent)] hover:underline"
              >
                {t.console.overview.viewTasks}
              </button>
            </div>
            {(!data.recent_failures || data.recent_failures.length === 0) ? (
              <EmptyState
                icon={<Activity className="h-8 w-8 text-[var(--color-submuted)]" />}
                title={t.console.overview.noFailures}
                className="py-6"
              />
            ) : (
              <div className="space-y-2.5">
                {data.recent_failures.slice(0, 3).map((log) => (
                  <div
                    key={log.id}
                    className="rounded-xl border border-[var(--color-danger)]/15 bg-[var(--color-danger-light)]/50 p-3"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-mono text-xs font-bold text-[var(--color-danger-text)]">
                        {log.task_no || t.console.overview.unboundTaskNo}
                      </span>
                      <span className="rounded-full bg-[var(--color-surface)] px-2 py-0.5 text-[10px] font-bold text-[var(--color-danger-text)]">
                        {log.error_code || log.event_type}
                      </span>
                    </div>
                    <p className="mt-1.5 line-clamp-2 text-[11px] leading-relaxed text-[var(--color-danger-text)]/85">
                      {log.error_message || log.remark || t.console.overview.noErrorDetail}
                    </p>
                    <p className="mt-1 text-[10px] text-[var(--color-muted)] tabular-nums">
                      {log.created_at ? new Date(log.created_at).toLocaleString() : ''} · {log.source}
                    </p>
                  </div>
                ))}
              </div>
            )}
          </Card>

          {/* 最新注册用户 */}
          <Card className="p-5">
            <div className="mb-4 flex items-center justify-between">
              <h3 className="text-sm font-bold text-[var(--color-ink)]">{t.console.overview.latestUsers}</h3>
              <button
                type="button"
                onClick={() => onNavigate('users')}
                className="text-xs font-semibold text-[var(--color-accent)] hover:underline"
              >
                {t.console.overview.manageUsers}
              </button>
            </div>
            {(!data.latest_users || data.latest_users.length === 0) ? (
              <EmptyState
                icon={<Users className="h-8 w-8 text-[var(--color-submuted)]" />}
                title={t.console.overview.noUsers}
                className="py-6"
              />
            ) : (
              <div className="space-y-2.5">
                {(data.latest_users || []).slice(0, 5).map((user) => (
                  <div
                    key={user.id}
                    className="flex items-center justify-between gap-3 rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-3"
                  >
                    <div>
                      <div className="text-xs font-bold text-[var(--color-ink)]">{user.username}</div>
                      <div className="mt-0.5 text-[11px] text-[var(--color-muted)]">
                        {user.email || t.console.overview.noEmail}
                      </div>
                    </div>
                    <div className="text-right">
                      <span className="text-xs font-bold text-[var(--color-ink)] tabular-nums">
                        {user.quota?.available_quota ?? 0}
                      </span>
                      <span className="block text-[10px] text-[var(--color-submuted)]">
                        {t.console.overview.remainingQuota}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>
      </div>
    </div>
  );
}
