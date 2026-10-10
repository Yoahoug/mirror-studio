import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Card, Button, Input, Textarea, Skeleton, ErrorState, useToast } from '@shared/ui';
import { useLanguage } from '@shared/i18n';
import {
  fetchConsoleConfig,
  updateConsoleSetting,
  batchUpdateConsoleConfig,
  type SiteConfigItem,
} from '@shared/services/console';

/** 基础设置展示的固定键顺序（发布说明相关键单独分区展示） */
const RELEASE_KEY_PREFIX = 'site.workspace_release_';

const DEFAULT_PRESETS = [
  { key: 'site.default_daily_free_quota', value: '3' },
  { key: 'site.register_enabled', value: '1' },
  { key: 'email.verification_enabled', value: '1' },
];

export function SettingsView() {
  const { t } = useLanguage();
  const { addToast } = useToast();
  const [items, setItems] = useState<SiteConfigItem[]>([]);
  const [allItems, setAllItems] = useState<SiteConfigItem[]>([]);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [savingKey, setSavingKey] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadConfig = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetchConsoleConfig();
      const list = res.items || [];
      setAllItems(list);
      // 基础设置聚焦核心键 + 发布说明键；没有注册进 siteconfig 表的键不展示
      setItems(list.filter((item) => item.key.startsWith(RELEASE_KEY_PREFIX) || item.category === 'site'));
      const next: Record<string, string> = {};
      list.forEach((item) => {
        next[item.key] = item.value;
      });
      setDrafts(next);
    } catch (err: any) {
      setError(err?.message || t.console.settings.loadFailed);
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  const releaseItems = useMemo(
    () => items.filter((item) => item.key.startsWith(RELEASE_KEY_PREFIX)),
    [items]
  );
  const basicItems = useMemo(
    () => items.filter((item) => !item.key.startsWith(RELEASE_KEY_PREFIX)),
    [items]
  );

  const handleSave = async (item: SiteConfigItem) => {
    setSavingKey(item.key);
    try {
      await updateConsoleSetting(item.key, {
        value: drafts[item.key] ?? '',
        is_secret: item.is_secret,
        description: item.description,
        category: item.category,
      });
      addToast(t.console.settings.saved, 'success');
      await loadConfig();
    } catch (err: any) {
      addToast(err?.message || t.console.settings.saveFailed, 'error');
    } finally {
      setSavingKey(null);
    }
  };

  const handleRestoreDefaults = async () => {
    try {
      await batchUpdateConsoleConfig(DEFAULT_PRESETS);
      addToast(t.console.settings.restored, 'success');
      await loadConfig();
    } catch (err: any) {
      addToast(err?.message || t.console.settings.restoreFailed, 'error');
    }
  };

  if (loading) {
    return (
      <div className="space-y-6">
        <Card className="space-y-5 p-6">
          <Skeleton className="h-6 w-48" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-10 w-2/3" />
        </Card>
      </div>
    );
  }

  if (error) {
    return <ErrorState message={error} onRetry={loadConfig} />;
  }

  const renderEditor = (item: SiteConfigItem) => {
    const isLongText = item.key === 'site.workspace_release_notes';
    return (
      <div
        key={item.key}
        className="rounded-2xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-4"
      >
        <div className="text-sm font-bold text-[var(--color-ink)]">
          {item.description || item.key}
        </div>
        <div className="mt-1 font-mono text-xs text-[var(--color-submuted)]">{item.key}</div>
        {isLongText ? (
          <Textarea
            rows={8}
            className="mt-4"
            placeholder={t.console.settings.notesPlaceholder}
            value={drafts[item.key] ?? ''}
            onChange={(e) => setDrafts((prev) => ({ ...prev, [item.key]: e.target.value }))}
          />
        ) : (
          <Input
            className="mt-4"
            value={drafts[item.key] ?? ''}
            onChange={(e) => setDrafts((prev) => ({ ...prev, [item.key]: e.target.value }))}
          />
        )}
        <div className="mt-3">
          <Button
            variant="primary"
            size="sm"
            loading={savingKey === item.key}
            onClick={() => handleSave(item)}
          >
            {t.console.settings.save}
          </Button>
        </div>
      </div>
    );
  };

  return (
    <div className="space-y-6">
      {/* 基础设置 */}
      <Card className="p-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-bold text-[var(--color-ink)]">{t.console.settings.basicSection}</h3>
            <p className="mt-1 text-xs text-[var(--color-muted)]">{t.console.settings.basicSectionDesc}</p>
          </div>
          <Button variant="secondary" size="sm" onClick={handleRestoreDefaults}>
            {t.console.settings.restoreDefaults}
          </Button>
        </div>
        {basicItems.length === 0 ? (
          <p className="mt-5 rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] p-4 text-xs text-[var(--color-muted)]">
            {t.common.emptyDesc}
          </p>
        ) : (
          <div className="mt-5 grid gap-4 md:grid-cols-2">{basicItems.map(renderEditor)}</div>
        )}
      </Card>

      {/* 版本与更新日志 */}
      <Card className="p-5">
        <h3 className="text-sm font-bold text-[var(--color-ink)]">{t.console.settings.releaseSection}</h3>
        <p className="mt-1 text-xs text-[var(--color-muted)]">{t.console.settings.releaseSectionDesc}</p>
        <div className="mt-5 grid gap-4">{releaseItems.map(renderEditor)}</div>
      </Card>

      {/* 完整配置预览 */}
      <Card className="overflow-hidden p-0">
        <div className="p-5 pb-4">
          <h3 className="text-sm font-bold text-[var(--color-ink)]">{t.console.settings.previewSection}</h3>
          <p className="mt-1 text-xs text-[var(--color-muted)]">{t.console.settings.previewSectionDesc}</p>
        </div>
        <div className="w-full overflow-x-auto border-t border-[var(--color-border-subtle)]">
          <table className="w-full min-w-[560px] divide-y divide-[var(--color-border-subtle)] text-left text-xs">
            <thead className="bg-[var(--color-surface-sunken)] font-bold uppercase tracking-wider text-[var(--color-muted)]">
              <tr>
                <th className="px-5 py-3">{t.console.settings.key}</th>
                <th className="px-4 py-3">{t.console.settings.value}</th>
                <th className="px-4 py-3">{t.console.settings.category}</th>
                <th className="px-5 py-3">{t.console.settings.updatedAt}</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[var(--color-border-subtle)] text-[var(--color-ink)]">
              {allItems.map((item) => (
                <tr key={item.key} className="transition-colors hover:bg-[var(--color-surface-hover)]">
                  <td className="px-5 py-3 font-mono font-bold text-[var(--color-ink-secondary)]">{item.key}</td>
                  <td className="max-w-[280px] truncate px-4 py-3 text-[var(--color-muted)]">
                    {item.is_secret ? '••••••' : item.value || '--'}
                  </td>
                  <td className="px-4 py-3 text-[var(--color-muted)]">{item.category}</td>
                  <td className="px-5 py-3 text-[var(--color-submuted)] tabular-nums">
                    {item.updated_at ? new Date(item.updated_at).toLocaleString() : '--'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </div>
  );
}
