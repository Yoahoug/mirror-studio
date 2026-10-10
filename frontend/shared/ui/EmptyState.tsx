import React from 'react';
import { AlertCircle, FolderOpen, RefreshCcw } from 'lucide-react';
import { Button } from './Button';
import { useLanguage } from '../i18n';

export interface EmptyStateProps {
  icon?: React.ReactNode;
  title: string;
  description?: string;
  action?: {
    label: string;
    onClick: () => void;
  };
  className?: string;
}

export function EmptyState({
  icon = <FolderOpen className="h-8 w-8 text-[var(--color-submuted)]" />,
  title,
  description,
  action,
  className = '',
}: EmptyStateProps) {
  return (
    <div className={`flex flex-col items-center justify-center p-8 text-center ${className}`}>
      <div className="mb-3 flex h-14 w-14 items-center justify-center rounded-2xl border border-[var(--color-border)] bg-[var(--color-surface-sunken)] shadow-sm">
        {icon}
      </div>
      <h3 className="text-sm font-bold text-[var(--color-ink)]">{title}</h3>
      {description && (
        <p className="mt-1 max-w-sm text-xs text-[var(--color-muted)] leading-relaxed">
          {description}
        </p>
      )}
      {action && (
        <div className="mt-4">
          <Button variant="secondary" size="sm" onClick={action.onClick}>
            {action.label}
          </Button>
        </div>
      )}
    </div>
  );
}

export interface ErrorStateProps {
  title?: string;
  message?: string;
  retryLabel?: string;
  backLabel?: string;
  onRetry?: () => void;
  onBack?: () => void;
  className?: string;
}

export function ErrorState({
  title,
  message,
  retryLabel,
  backLabel,
  onRetry,
  onBack,
  className = '',
}: ErrorStateProps) {
  const { t } = useLanguage();
  const resolvedTitle = title ?? t.common.errorTitle;
  const resolvedMessage = message ?? t.common.errorDesc;
  const resolvedRetry = retryLabel ?? t.common.errorRetry;
  const resolvedBack = backLabel ?? t.common.errorBack;

  return (
    <div className={`flex flex-col items-center justify-center p-8 text-center rounded-2xl border border-[var(--color-danger)]/20 bg-[var(--color-danger-light)]/40 ${className}`}>
      <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--color-danger-light)] text-[var(--color-danger)] shadow-sm">
        <AlertCircle className="h-6 w-6" />
      </div>
      <h3 className="text-sm font-bold text-[var(--color-danger-text)]">{resolvedTitle}</h3>
      <p className="mt-1 max-w-md text-xs text-[var(--color-danger-text)]/80 leading-relaxed font-mono">
        {resolvedMessage}
      </p>
      <div className="mt-4 flex items-center gap-2">
        {onRetry && (
          <Button
            variant="danger"
            size="sm"
            onClick={onRetry}
            icon={<RefreshCcw className="h-3.5 w-3.5" />}
          >
            {resolvedRetry}
          </Button>
        )}
        {onBack && (
          <Button variant="ghost" size="sm" onClick={onBack}>
            {resolvedBack}
          </Button>
        )}
      </div>
    </div>
  );
}
