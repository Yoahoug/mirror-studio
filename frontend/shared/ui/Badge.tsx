import React from 'react';

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  variant?: 'neutral' | 'accent' | 'success' | 'warning' | 'danger' | 'info';
  size?: 'sm' | 'md';
  dot?: boolean;
}

export function Badge({
  variant = 'neutral',
  size = 'md',
  dot = false,
  className = '',
  children,
  ...props
}: BadgeProps) {
  const sizeClasses = {
    sm: 'text-[10px] px-1.5 py-0.5 rounded-full',
    md: 'text-xs px-2.5 py-0.5 rounded-full',
  }[size];

  const variantClasses = {
    neutral: 'bg-[var(--color-surface-hover)] text-[var(--color-ink-secondary)] border border-[var(--color-border)]',
    accent: 'bg-[var(--color-accent-light)] text-[var(--color-accent)] border border-[var(--color-accent)]/20',
    success: 'bg-[var(--color-success-light)] text-[var(--color-success-text)] border border-[var(--color-success)]/20',
    warning: 'bg-[var(--color-warning-light)] text-[var(--color-warning-text)] border border-[var(--color-warning)]/20',
    danger: 'bg-[var(--color-danger-light)] text-[var(--color-danger-text)] border border-[var(--color-danger)]/20',
    info: 'bg-[var(--color-info-light)] text-[var(--color-info-text)] border border-[var(--color-info)]/20',
  }[variant];

  const dotClasses = {
    neutral: 'bg-[var(--color-muted)]',
    accent: 'bg-[var(--color-accent)]',
    success: 'bg-[var(--color-success)]',
    warning: 'bg-[var(--color-warning)]',
    danger: 'bg-[var(--color-danger)]',
    info: 'bg-[var(--color-info)]',
  }[variant];

  return (
    <span
      className={`inline-flex items-center gap-1.5 font-bold uppercase tracking-wider tabular-nums ${sizeClasses} ${variantClasses} ${className}`}
      {...props}
    >
      {dot && <span className={`h-1.5 w-1.5 rounded-full ${dotClasses}`} />}
      {children}
    </span>
  );
}
