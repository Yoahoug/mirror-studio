import React, { useCallback, useEffect, useState } from 'react';
import { Zap, CheckCircle2, AlertCircle, Plus, Pencil, Trash2 } from 'lucide-react';
import { Card, Button, Badge, Skeleton, ErrorState, EmptyState, Modal, Input, useToast } from '@shared/ui';
import { useLanguage } from '@shared/i18n';
import {
  fetchConsoleChannels,
  createConsoleChannel,
  updateConsoleChannel,
  deleteConsoleChannel,
  type ImageChannel,
  type ImageChannelPayload,
} from '@shared/services/console';

const CHANNEL_TEMPLATE: ImageChannelPayload = {
  name: '',
  channel_code: '',
  channel_type: 'image',
  provider_name: '',
  base_url: '',
  api_path: '/v1/images/generations',
  edit_api_path: '',
  model_name: '',
  timeout_seconds: 60,
  max_retries: 3,
  is_active: true,
  priority: 1,
  api_key: '',
};

function toForm(item: ImageChannel | null): ImageChannelPayload {
  if (!item) return { ...CHANNEL_TEMPLATE };
  return {
    name: item.name,
    channel_code: item.channel_code,
    channel_type: item.channel_type,
    provider_name: item.provider_name,
    base_url: item.base_url,
    api_path: item.api_path,
    edit_api_path: item.edit_api_path,
    model_name: item.model_name,
    timeout_seconds: item.timeout_seconds,
    max_retries: item.max_retries,
    is_active: item.is_active,
    priority: item.priority,
    api_key: '',
  };
}

export function ChannelsView() {
  const { t } = useLanguage();
  const { addToast } = useToast();
  const [channels, setChannels] = useState<ImageChannel[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // 编辑/创建弹窗：editingItem 为 null 且 open 时表示新建
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingItem, setEditingItem] = useState<ImageChannel | null>(null);
  const [form, setForm] = useState<ImageChannelPayload>(CHANNEL_TEMPLATE);
  const [saving, setSaving] = useState(false);

  // 删除确认弹窗
  const [deletingItem, setDeletingItem] = useState<ImageChannel | null>(null);
  const [deleting, setDeleting] = useState(false);

  const loadChannels = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await fetchConsoleChannels();
      setChannels(res);
    } catch (err: any) {
      setError(err?.message || t.common.errorDesc);
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    loadChannels();
  }, [loadChannels]);

  const openCreate = () => {
    setEditingItem(null);
    setForm({ ...CHANNEL_TEMPLATE });
    setEditorOpen(true);
  };

  const openEdit = (item: ImageChannel) => {
    setEditingItem(item);
    setForm(toForm(item));
    setEditorOpen(true);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      if (editingItem) {
        await updateConsoleChannel(editingItem.id, form);
        addToast(t.console.channels.updated, 'success');
      } else {
        await createConsoleChannel(form);
        addToast(t.console.channels.created, 'success');
      }
      setEditorOpen(false);
      loadChannels();
    } catch (err: any) {
      addToast(err?.message || t.console.channels.saveFailed, 'error');
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async () => {
    if (!deletingItem) return;
    setDeleting(true);
    try {
      await deleteConsoleChannel(deletingItem.id);
      addToast(t.console.channels.deleted, 'success');
      setDeletingItem(null);
      loadChannels();
    } catch (err: any) {
      addToast(err?.message || t.console.channels.deleteFailed, 'error');
    } finally {
      setDeleting(false);
    }
  };

  const numberField = (
    label: string,
    key: 'timeout_seconds' | 'max_retries' | 'priority',
    min = 0
  ) => (
    <Input
      label={label}
      type="number"
      min={min}
      value={String(form[key])}
      onChange={(e) => setForm({ ...form, [key]: Number(e.target.value) })}
      required
    />
  );

  return (
    <div className="space-y-4">
      <Card className="p-5">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-bold text-[var(--color-ink)]">{t.console.channels.title}</h3>
            <p className="mt-1 text-xs text-[var(--color-muted)]">{t.console.channels.desc}</p>
          </div>
          <div className="flex gap-2">
            <Button variant="secondary" size="sm" onClick={loadChannels}>
              {t.console.channels.refresh}
            </Button>
            <Button variant="primary" size="sm" icon={<Plus className="h-3.5 w-3.5" />} onClick={openCreate}>
              {t.console.channels.create}
            </Button>
          </div>
        </div>
      </Card>

      <Card className="overflow-hidden p-0">
        {loading && channels.length === 0 ? (
          <div className="space-y-4 p-6">
            {[1, 2, 3].map((i) => (
              <Skeleton key={i} className="h-16 w-full" />
            ))}
          </div>
        ) : error ? (
          <div className="p-6">
            <ErrorState message={error} onRetry={loadChannels} />
          </div>
        ) : channels.length === 0 ? (
          <EmptyState
            icon={<Zap className="h-8 w-8 text-[var(--color-submuted)]" />}
            title={t.console.channels.emptyTitle}
            description={t.console.channels.emptyDesc}
            action={{ label: t.console.channels.create, onClick: openCreate }}
          />
        ) : (
          <div className="w-full overflow-x-auto">
            <table className="w-full min-w-[720px] divide-y divide-[var(--color-border-subtle)] text-left text-xs">
              <thead className="bg-[var(--color-surface-sunken)] font-bold uppercase tracking-wider text-[var(--color-muted)]">
                <tr>
                  <th className="px-5 py-3.5">{t.console.channels.name}</th>
                  <th className="px-4 py-3.5">{t.console.channels.type}</th>
                  <th className="px-4 py-3.5">{t.console.channels.model}</th>
                  <th className="px-4 py-3.5">{t.console.channels.status}</th>
                  <th className="px-4 py-3.5">{t.console.channels.priority}</th>
                  <th className="px-4 py-3.5">{t.console.channels.health}</th>
                  <th className="px-5 py-3.5 text-right">{t.console.users.actions}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--color-border-subtle)] text-[var(--color-ink)]">
                {channels.map((channel) => (
                  <tr key={channel.id} className="transition-colors hover:bg-[var(--color-surface-hover)]">
                    <td className="px-5 py-3.5">
                      <div className="font-bold">{channel.name}</div>
                      <div className="font-mono text-[10px] text-[var(--color-muted)]">
                        {channel.channel_code} · {channel.provider_name || '--'}
                      </div>
                    </td>
                    <td className="px-4 py-3.5 text-[var(--color-muted)]">{channel.channel_type}</td>
                    <td className="px-4 py-3.5 font-mono text-[11px]">
                      {channel.model_name || t.console.channels.defaultModel}
                    </td>
                    <td className="px-4 py-3.5">
                      <Badge size="sm" variant={channel.is_active ? 'success' : 'neutral'}>
                        {channel.is_active ? t.console.channels.enabled : t.console.channels.disabled}
                      </Badge>
                    </td>
                    <td className="px-4 py-3.5 font-semibold tabular-nums">{channel.priority ?? 100}</td>
                    <td className="px-4 py-3.5">
                      {channel.is_active ? (
                        <span className="inline-flex items-center gap-1 text-[11px] font-bold text-[var(--color-success)]">
                          <CheckCircle2 className="h-3.5 w-3.5" /> {t.console.channels.healthy}
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-[11px] font-bold text-[var(--color-warning)]">
                          <AlertCircle className="h-3.5 w-3.5" /> {t.console.channels.offline}
                        </span>
                      )}
                    </td>
                    <td className="px-5 py-3.5 text-right">
                      <div className="flex justify-end gap-1.5">
                        <Button
                          variant="secondary"
                          size="sm"
                          icon={<Pencil className="h-3 w-3" />}
                          onClick={() => openEdit(channel)}
                        >
                          {t.console.channels.edit}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          icon={<Trash2 className="h-3 w-3" />}
                          className="text-[var(--color-danger)] hover:bg-[var(--color-danger-light)]"
                          onClick={() => setDeletingItem(channel)}
                        >
                          {t.console.channels.delete}
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>

      {/* 创建/编辑渠道弹窗 */}
      <Modal
        open={editorOpen}
        onClose={() => setEditorOpen(false)}
        title={editingItem ? `${t.console.channels.editTitle} · ${editingItem.name}` : t.console.channels.createTitle}
        maxWidth="lg"
      >
        <form onSubmit={handleSave} className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Input
            label={t.console.channels.name}
            value={form.name}
            onChange={(e) => setForm({ ...form, name: e.target.value })}
            required
          />
          <Input
            label={t.console.channels.code}
            value={form.channel_code || ''}
            onChange={(e) => setForm({ ...form, channel_code: e.target.value })}
            disabled={Boolean(editingItem)}
            required={!editingItem}
          />
          <div>
            <label className="mb-1.5 block text-xs font-bold text-[var(--color-ink)]">
              {t.console.channels.type}
            </label>
            <select
              value={form.channel_type}
              onChange={(e) => setForm({ ...form, channel_type: e.target.value as 'image' | 'llm' })}
              className="h-10 w-full rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] px-3 text-sm font-semibold text-[var(--color-ink)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]"
            >
              <option value="image">image</option>
              <option value="llm">llm</option>
            </select>
          </div>
          <Input
            label={t.console.channels.provider}
            value={form.provider_name}
            onChange={(e) => setForm({ ...form, provider_name: e.target.value })}
          />
          <Input
            label={t.console.channels.baseUrl}
            value={form.base_url}
            onChange={(e) => setForm({ ...form, base_url: e.target.value })}
          />
          <Input
            label={t.console.channels.apiPath}
            value={form.api_path}
            onChange={(e) => setForm({ ...form, api_path: e.target.value })}
            required
          />
          <Input
            label={t.console.channels.editApiPath}
            placeholder={t.console.channels.editApiPathPlaceholder}
            value={form.edit_api_path}
            onChange={(e) => setForm({ ...form, edit_api_path: e.target.value })}
          />
          <Input
            label={t.console.channels.model}
            value={form.model_name}
            onChange={(e) => setForm({ ...form, model_name: e.target.value })}
          />
          <Input
            label={t.console.channels.apiKey}
            type="password"
            autoComplete="new-password"
            placeholder={editingItem ? t.console.channels.apiKeyKeepHint : ''}
            value={form.api_key || ''}
            onChange={(e) => setForm({ ...form, api_key: e.target.value })}
          />
          {numberField(t.console.channels.timeout, 'timeout_seconds')}
          {numberField(t.console.channels.maxRetries, 'max_retries')}
          {numberField(t.console.channels.priority, 'priority')}
          <div>
            <label className="mb-1.5 block text-xs font-bold text-[var(--color-ink)]">
              {t.console.channels.active}
            </label>
            <select
              value={form.is_active ? '1' : '0'}
              onChange={(e) => setForm({ ...form, is_active: e.target.value === '1' })}
              className="h-10 w-full rounded-xl border border-[var(--color-border)] bg-[var(--color-surface)] px-3 text-sm font-semibold text-[var(--color-ink)] focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)]"
            >
              <option value="1">{t.console.channels.enabled}</option>
              <option value="0">{t.console.channels.disabled}</option>
            </select>
          </div>
          <div className="col-span-full mt-2 flex justify-end gap-2.5">
            <Button type="button" variant="ghost" onClick={() => setEditorOpen(false)}>
              {t.common.cancel}
            </Button>
            <Button type="submit" variant="primary" loading={saving}>
              {t.common.save}
            </Button>
          </div>
        </form>
      </Modal>

      {/* 删除确认弹窗 */}
      <Modal
        open={Boolean(deletingItem)}
        onClose={() => setDeletingItem(null)}
        title={t.console.channels.deleteConfirmTitle}
        description={t.console.channels.deleteConfirmDesc.replace(
          '{name}',
          deletingItem?.name ?? ''
        )}
        maxWidth="sm"
      >
        <div className="flex justify-end gap-2.5">
          <Button variant="ghost" onClick={() => setDeletingItem(null)}>
            {t.common.cancel}
          </Button>
          <Button variant="danger" loading={deleting} onClick={handleDelete}>
            {t.common.delete}
          </Button>
        </div>
      </Modal>
    </div>
  );
}
