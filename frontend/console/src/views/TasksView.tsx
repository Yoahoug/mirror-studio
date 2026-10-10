import React, { useCallback, useEffect, useState } from 'react';
import { Search, Info, AlertTriangle } from 'lucide-react';
import { Card, Button, Input, Modal, Badge, Skeleton, ErrorState, EmptyState } from '@shared/ui';
import { useLanguage } from '@shared/i18n';
import { fetchConsoleTasks, type AdminTask, type Pagination } from '@shared/services/console';

export function TasksView() {
  const { t } = useLanguage();
  const [tasks, setTasks] = useState<AdminTask[]>([]);
  const [pagination, setPagination] = useState<Pagination | null>(null);
  const [page, setPage] = useState(1);
  const [keyword, setKeyword] = useState('');
  const [status, setStatus] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // 错误详情弹窗
  const [inspectTask, setInspectTask] = useState<AdminTask | null>(null);

  const loadTasks = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetchConsoleTasks({ page, per_page: 15, keyword, status });
      setTasks(res.items || []);
      setPagination(res.pagination || null);
    } catch (err: any) {
      setError(err?.message || t.common.errorDesc);
    } finally {
      setLoading(false);
    }
  }, [page, keyword, status, t]);

  useEffect(() => {
    loadTasks();
  }, [loadTasks]);

  const statusVariant = (value: string) =>
    value === 'succeeded'
      ? 'success'
      : value === 'failed'
        ? 'danger'
        : value === 'running'
          ? 'accent'
          : value === 'canceled'
            ? 'warning'
            : 'neutral';

  const statusLabel = (value: string) => {
    const map: Record<string, string> = {
      queued: t.common.statusQueued,
      running: t.common.statusRunning,
      succeeded: t.common.statusSucceeded,
      failed: t.common.statusFailed,
      canceled: t.common.statusCanceled,
    };
    return map[value] || value;
  };

  const statusOptions = [
    { value: '', label: t.console.tasks.allStatuses },
    { value: 'succeeded', label: t.common.statusSucceeded },
    { value: 'failed', label: t.common.statusFailed },
    { value: 'running', label: t.common.statusRunning },
    { value: 'queued', label: t.common.statusQueued },
    { value: 'canceled', label: t.common.statusCanceled },
  ];

  return (
    <div className="space-y-4">
      {/* 筛选条 */}
      <Card className="p-4">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setPage(1);
            loadTasks();
          }}
          className="flex flex-wrap items-center gap-3"
        >
          <div className="min-w-[220px] flex-1">
            <Input
              placeholder={t.console.tasks.searchPlaceholder}
              leftIcon={<Search className="h-4 w-4" />}
              value={keyword}
              onChange={(e) => setKeyword(e.target.value)}
            />
          </div>
          <select
            value={status}
            onChange={(e) => {
              setStatus(e.target.value);
              setPage(1);
            }}
            aria-label={t.console.tasks.status}
            className="h-10 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] px-3 text-xs font-semibold text-[var(--color-ink)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]"
          >
            {statusOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
          <Button type="submit" variant="secondary" size="md">
            {t.console.tasks.filter}
          </Button>
        </form>
      </Card>

      {/* 数据表 */}
      <Card className="overflow-hidden p-0">
        {loading && tasks.length === 0 ? (
          <div className="space-y-4 p-6">
            {[1, 2, 3, 4, 5].map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : error ? (
          <div className="p-6">
            <ErrorState message={error} onRetry={loadTasks} />
          </div>
        ) : tasks.length === 0 ? (
          <EmptyState
            title={t.console.tasks.emptyTitle}
            description={t.console.tasks.emptyDesc}
          />
        ) : (
          <div className="w-full overflow-x-auto">
            <table className="w-full min-w-[760px] divide-y divide-[var(--color-border-subtle)] text-left text-xs">
              <thead className="bg-[var(--color-surface-sunken)] font-bold uppercase tracking-wider text-[var(--color-muted)]">
                <tr>
                  <th className="px-5 py-3.5">{t.console.tasks.taskNo}</th>
                  <th className="px-4 py-3.5">{t.console.tasks.user}</th>
                  <th className="px-4 py-3.5">{t.console.tasks.status}</th>
                  <th className="px-4 py-3.5">{t.console.tasks.promptSummary}</th>
                  <th className="px-4 py-3.5">{t.console.tasks.submittedAt}</th>
                  <th className="px-4 py-3.5">{t.console.tasks.cost}</th>
                  <th className="px-5 py-3.5 text-right">{t.console.tasks.details}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--color-border-subtle)] text-[var(--color-ink)]">
                {tasks.map((task) => (
                  <tr key={task.id} className="transition-colors hover:bg-[var(--color-surface-hover)]">
                    <td className="px-5 py-3.5 font-mono text-xs font-bold">{task.task_no}</td>
                    <td className="px-4 py-3.5 font-semibold">{task.username || `ID:${task.user_id}`}</td>
                    <td className="px-4 py-3.5">
                      <Badge size="sm" variant={statusVariant(task.status)}>
                        {statusLabel(task.status)}
                      </Badge>
                    </td>
                    <td className="max-w-[260px] px-4 py-3.5">
                      <p className="truncate text-xs text-[var(--color-ink)]">
                        {task.prompt || t.console.overview.noPrompt}
                      </p>
                      <div className="mt-0.5 flex gap-1.5 text-[10px] text-[var(--color-muted)]">
                        {task.style_code && <span>{t.console.tasks.style}: {task.style_code}</span>}
                        {task.size_code && <span>{t.console.tasks.size}: {task.size_code}</span>}
                        {task.channel_name && <span>{t.console.tasks.channel}: {task.channel_name}</span>}
                      </div>
                    </td>
                    <td className="px-4 py-3.5 text-[var(--color-submuted)] tabular-nums">
                      {task.created_at ? new Date(task.created_at).toLocaleString("zh-CN") : '--'}
                    </td>
                    <td className="px-4 py-3.5 tabular-nums">
                      {t.console.tasks.costPoints.replace(
                        '{count}',
                        String(task.quota_cost ?? 0)
                      )}
                    </td>
                    <td className="px-5 py-3.5 text-right">
                      {task.status === 'failed' ? (
                        <Button
                          variant="danger"
                          size="sm"
                          icon={<AlertTriangle className="h-3 w-3" />}
                          onClick={() => setInspectTask(task)}
                        >
                          {t.console.tasks.inspectError}
                        </Button>
                      ) : (
                        <Button
                          variant="ghost"
                          size="sm"
                          icon={<Info className="h-3 w-3" />}
                          onClick={() => setInspectTask(task)}
                        >
                          {t.console.tasks.view}
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* 分页 */}
        {pagination && pagination.total_pages > 1 && (
          <div className="flex items-center justify-between border-t border-[var(--color-border-subtle)] px-5 py-3 text-xs text-[var(--color-muted)]">
            <span>
              {t.console.tasks.paginationTotal.replace('{total}', String(pagination.total))}
              {' · '}
              {t.console.tasks.paginationPage
                .replace('{page}', String(pagination.page))
                .replace('{totalPages}', String(pagination.total_pages))}
            </span>
            <div className="flex gap-2">
              <Button
                variant="secondary"
                size="sm"
                disabled={!pagination.has_previous}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                {t.common.pagePrev}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={!pagination.has_next}
                onClick={() => setPage((p) => p + 1)}
              >
                {t.common.pageNext}
              </Button>
            </div>
          </div>
        )}
      </Card>

      {/* 任务详情/错误诊断弹窗 */}
      <Modal
        open={Boolean(inspectTask)}
        onClose={() => setInspectTask(null)}
        title={`${t.console.tasks.detailTitle}: ${inspectTask?.task_no ?? ''}`}
        description={t.console.tasks.detailDesc}
        maxWidth="lg"
      >
        {inspectTask && (
          <div className="space-y-4 text-xs">
            <div className="grid grid-cols-2 gap-3 rounded-2xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-3.5">
              <div>
                <span className="text-[var(--color-muted)]">{t.console.tasks.currentStatus}：</span>
                <span className="ml-1 font-bold">{statusLabel(inspectTask.status)}</span>
              </div>
              <div>
                <span className="text-[var(--color-muted)]">{t.console.tasks.ownerAccount}：</span>
                <span className="ml-1 font-bold">{inspectTask.username}</span>
              </div>
              <div>
                <span className="text-[var(--color-muted)]">{t.console.tasks.submittedAt}：</span>
                <span className="ml-1">{inspectTask.created_at || '--'}</span>
              </div>
              <div>
                <span className="text-[var(--color-muted)]">{t.console.tasks.finishedAt}：</span>
                <span className="ml-1">{inspectTask.finished_at || '--'}</span>
              </div>
            </div>

            <div>
              <label className="mb-1 block font-bold text-[var(--color-ink)]">{t.console.tasks.fullPrompt}</label>
              <div className="max-h-36 overflow-y-auto break-words rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] p-3 font-mono text-[11px] leading-relaxed">
                {inspectTask.prompt || t.console.tasks.emptyPrompt}
              </div>
            </div>

            {inspectTask.error_message && (
              <div>
                <label className="mb-1 block font-bold text-[var(--color-danger)]">{t.console.tasks.failureReason}</label>
                <div className="break-words rounded-xl border border-[var(--color-danger)]/30 bg-[var(--color-danger-light)] p-3 font-mono text-[11px] leading-relaxed text-[var(--color-danger-text)]">
                  {inspectTask.error_code && (
                    <span className="mb-1 block font-bold">[{inspectTask.error_code}]</span>
                  )}
                  {inspectTask.error_message}
                </div>
              </div>
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}
