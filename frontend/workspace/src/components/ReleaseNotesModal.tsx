import { useEffect, useState } from "react";
import { BellRing, Check } from "lucide-react";
import { Modal, IconButton, Button } from "@shared/ui";
import { useLanguage } from "@shared/i18n";

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
  const { t } = useLanguage();
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
    <Modal
      open={open}
      onClose={() => onClose(dismissForCurrentVersion)}
      maxWidth="lg"
      showCloseButton={false}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="inline-flex items-center gap-2 rounded-full bg-[var(--color-success-light)] px-3 py-1 text-[11px] font-black uppercase tracking-widest text-[var(--color-success-text)]">
            <BellRing className="h-3.5 w-3.5" />
            {t.workspace.nav.releaseNotes}
          </div>
          <h3 className="mt-3 text-xl font-black tracking-tight text-[var(--color-ink)]">
            {release.title || t.workspace.nav.releaseNotes}
          </h3>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-[var(--color-submuted)]">
            <span className="rounded-full bg-[var(--color-surface-hover)] px-2.5 py-1 font-bold text-[var(--color-ink-secondary)] tabular-nums">
              {release.version}
            </span>
            {release.publishedAt && <span className="tabular-nums">{release.publishedAt}</span>}
          </div>
        </div>
        <IconButton
          aria-label={t.common.close}
          size="sm"
          onClick={() => onClose(dismissForCurrentVersion)}
        >
          ✕
        </IconButton>
      </div>

      <div className="mt-5 space-y-2 rounded-2xl bg-[var(--color-surface-sunken)] p-4">
        {noteLines.length ? (
          noteLines.map((item) => (
            <div key={item} className="flex items-start gap-3 text-sm text-[var(--color-ink-secondary)]">
              <span className="mt-0.5 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-[var(--color-ink)] text-[var(--color-surface)]">
                <Check className="h-3 w-3" />
              </span>
              <span className="leading-6">{item}</span>
            </div>
          ))
        ) : (
          <p className="text-sm leading-6 text-[var(--color-muted)]">
            {t.common.appTagline}
          </p>
        )}
      </div>

      <label className="mt-5 flex cursor-pointer items-center gap-3 rounded-2xl border border-[var(--color-border)] px-4 py-3 text-sm text-[var(--color-ink-secondary)]">
        <input
          type="checkbox"
          checked={dismissForCurrentVersion}
          onChange={(event) => setDismissForCurrentVersion(event.target.checked)}
          className="h-4 w-4 rounded border-[var(--color-border)] accent-[var(--color-accent)]"
        />
        {t.workspace.nav.releaseNotes}
      </label>

      <div className="mt-5 flex justify-end">
        <Button variant="primary" size="md" onClick={() => onClose(dismissForCurrentVersion)}>
          {t.common.confirm}
        </Button>
      </div>
    </Modal>
  );
}
