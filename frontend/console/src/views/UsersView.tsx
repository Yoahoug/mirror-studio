import React, { useCallback, useEffect, useState } from 'react';
import { Search, Edit3 } from 'lucide-react';
import {
  Card,
  Button,
  Input,
  Modal,
  Badge,
  Skeleton,
  ErrorState,
  EmptyState,
  useToast,
} from '@shared/ui';
import { useLanguage } from '@shared/i18n';
import {
  fetchConsoleUsers,
  fetchConsoleUserDetail,
  updateUserQuota,
  type AdminUser,
  type AdminUserDetail,
  type Pagination,
} from '@shared/services/console';

export function UsersView() {
  const { t } = useLanguage();
  const { addToast } = useToast();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [pagination, setPagination] = useState<Pagination | null>(null);
  const [page, setPage] = useState(1);
  const [keyword, setKeyword] = useState('');
  // 输入框草稿与真正参与查询的关键词分开：前者逐字更新，后者只在点「搜索」时生效，
  // 否则每敲一个字都会发一次请求，提交时还会用旧关键词再补一次。
  const [appliedKeyword, setAppliedKeyword] = useState('');
  const [status, setStatus] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // 配额修改弹窗状态
  const [selectedUser, setSelectedUser] = useState<AdminUserDetail | null>(null);
  const [dailyQuotaInput, setDailyQuotaInput] = useState('');
  const [availableQuotaInput, setAvailableQuotaInput] = useState('');
  const [remarkInput, setRemarkInput] = useState('');
  const [updating, setUpdating] = useState(false);

  const loadUsers = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetchConsoleUsers({ page, per_page: 15, keyword: appliedKeyword, status });
      setUsers(res.items || []);
      setPagination(res.pagination || null);
    } catch (err: any) {
      setError(err?.message || t.common.errorDesc);
    } finally {
      setLoading(false);
    }
  }, [page, appliedKeyword, status, t]);

  useEffect(() => {
    loadUsers();
  }, [loadUsers]);

  const openQuotaModal = async (user: AdminUser) => {
    try {
      setLoading(true);
      const detail = await fetchConsoleUserDetail(user.id);
      setSelectedUser(detail);
      setDailyQuotaInput(String(detail.quota?.daily_quota ?? 0));
      setAvailableQuotaInput(String(detail.quota?.available_quota ?? 0));
      setRemarkInput('');
    } catch (err: any) {
      addToast(err?.message || t.console.users.detailLoadFailed, 'error');
    } finally {
      setLoading(false);
    }
  };

  const handleSaveQuota = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedUser) return;
    setUpdating(true);
    try {
      await updateUserQuota(selectedUser.id, {
        daily_quota: Number(dailyQuotaInput),
        available_quota: Number(availableQuotaInput),
        remark: remarkInput,
      });
      addToast(t.console.users.quotaUpdated, 'success');
      const detail = await fetchConsoleUserDetail(selectedUser.id);
      setSelectedUser(detail);
      loadUsers();
    } catch (err: any) {
      addToast(err?.message || t.console.users.quotaUpdateFailed, 'error');
    } finally {
      setUpdating(false);
    }
  };

  const statusOptions = [
    { value: '', label: t.console.users.allStatuses },
    { value: 'active', label: t.console.users.statusActive },
    { value: 'disabled', label: t.console.users.statusDisabled },
    { value: 'locked', label: t.console.users.statusLocked },
  ];

  return (
    <div className="space-y-4">
      {/* 搜索与过滤工具栏 */}
      <Card className="p-4">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            // 只改状态：页码与关键词一起提交，effect 里发一次请求就够。
            setPage(1);
            setAppliedKeyword(keyword.trim());
          }}
          className="flex flex-wrap items-center gap-3"
        >
          <div className="min-w-[220px] flex-1">
            <Input
              placeholder={t.console.users.searchPlaceholder}
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
            aria-label={t.console.users.status}
            className="h-10 rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] px-3 text-xs font-semibold text-[var(--color-ink)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]"
          >
            {statusOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
          <Button type="submit" variant="secondary" size="md">
            {t.console.users.search}
          </Button>
        </form>
      </Card>

      {/* 用户数据表 (带横向滚动与移动端自适应) */}
      <Card className="overflow-hidden p-0">
        {loading && users.length === 0 ? (
          <div className="space-y-4 p-6">
            {[1, 2, 3, 4, 5].map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : error ? (
          <div className="p-6">
            <ErrorState message={error} onRetry={loadUsers} />
          </div>
        ) : users.length === 0 ? (
          <EmptyState
            title={t.console.users.emptyTitle}
            description={t.console.users.emptyDesc}
          />
        ) : (
          <div className="w-full overflow-x-auto">
            <table className="w-full min-w-[680px] divide-y divide-[var(--color-border-subtle)] text-left text-xs">
              <thead className="bg-[var(--color-surface-sunken)] font-bold uppercase tracking-wider text-[var(--color-muted)]">
                <tr>
                  <th className="px-5 py-3.5">{t.console.users.username}</th>
                  <th className="px-4 py-3.5">{t.console.users.role}</th>
                  <th className="px-4 py-3.5">{t.console.users.status}</th>
                  <th className="px-4 py-3.5">{t.console.users.dailyQuota}</th>
                  <th className="px-4 py-3.5">{t.console.users.availableQuota}</th>
                  <th className="px-4 py-3.5">{t.console.users.joinedAt}</th>
                  <th className="px-5 py-3.5 text-right">{t.console.users.actions}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--color-border-subtle)] text-[var(--color-ink)]">
                {users.map((user) => (
                  <tr key={user.id} className="transition-colors hover:bg-[var(--color-surface-hover)]">
                    <td className="px-5 py-3.5">
                      <div className="font-bold text-[var(--color-ink)]">{user.username}</div>
                      <div className="text-[11px] text-[var(--color-muted)]">
                        {user.email || t.console.overview.noEmail}
                      </div>
                    </td>
                    <td className="px-4 py-3.5">
                      {user.is_staff ? (
                        <Badge size="sm" variant="accent">
                          {t.console.users.admin}
                        </Badge>
                      ) : (
                        <span className="text-[var(--color-muted)]">{t.console.users.regularUser}</span>
                      )}
                    </td>
                    <td className="px-4 py-3.5">
                      <Badge size="sm" variant={user.status === 'active' ? 'success' : 'danger'}>
                        {user.status === 'active'
                          ? t.console.users.statusActive
                          : t.console.users.statusDisabled}
                      </Badge>
                    </td>
                    <td className="px-4 py-3.5 font-semibold tabular-nums">
                      {user.quota?.daily_quota ?? '--'}
                    </td>
                    <td className="px-4 py-3.5 font-bold tabular-nums text-[var(--color-accent)]">
                      {user.quota?.available_quota ?? '--'}
                    </td>
                    <td className="px-4 py-3.5 text-[var(--color-submuted)] tabular-nums">
                      {user.date_joined ? new Date(user.date_joined).toLocaleDateString("zh-CN") : '--'}
                    </td>
                    <td className="px-5 py-3.5 text-right">
                      <Button
                        variant="secondary"
                        size="sm"
                        icon={<Edit3 className="h-3 w-3" />}
                        onClick={() => openQuotaModal(user)}
                      >
                        {t.console.users.adjustQuota}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* 分页控制条 */}
        {pagination && pagination.total_pages > 1 && (
          <div className="flex items-center justify-between border-t border-[var(--color-border-subtle)] px-5 py-3 text-xs text-[var(--color-muted)]">
            <span>
              {t.console.users.paginationTotal.replace('{total}', String(pagination.total))}
              {' · '}
              {t.console.users.paginationPage
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

      {/* 配额调整模态弹窗 */}
      <Modal
        open={Boolean(selectedUser)}
        onClose={() => setSelectedUser(null)}
        title={`${t.console.users.quotaModalTitle}: ${selectedUser?.username ?? ''}`}
        description={t.console.users.quotaModalDesc}
      >
        {selectedUser && (
          <form onSubmit={handleSaveQuota} className="space-y-4">
            <Input
              label={t.console.users.dailyQuotaField}
              type="number"
              min={0}
              value={dailyQuotaInput}
              onChange={(e) => setDailyQuotaInput(e.target.value)}
              required
            />
            <Input
              label={t.console.users.availableQuotaField}
              type="number"
              min={0}
              value={availableQuotaInput}
              onChange={(e) => setAvailableQuotaInput(e.target.value)}
              required
            />
            <Input
              label={t.console.users.remarkField}
              placeholder={t.console.users.remarkPlaceholder}
              value={remarkInput}
              onChange={(e) => setRemarkInput(e.target.value)}
            />
            <div className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-4 text-xs leading-6 text-[var(--color-muted)]">
              <div>
                {t.console.users.frozenQuota}：{selectedUser.quota?.frozen_quota ?? 0}
              </div>
              <div>
                {t.console.users.totalGenerated}：{selectedUser.quota?.total_generated_count ?? 0}
              </div>
              <div>
                {t.console.users.lastResetDate}：{selectedUser.quota?.last_reset_date || '--'}
              </div>
            </div>
            <div className="mt-5 flex justify-end gap-2.5">
              <Button type="button" variant="ghost" onClick={() => setSelectedUser(null)}>
                {t.common.cancel}
              </Button>
              <Button type="submit" variant="primary" loading={updating}>
                {t.console.users.confirmSave}
              </Button>
            </div>
          </form>
        )}
      </Modal>
    </div>
  );
}
