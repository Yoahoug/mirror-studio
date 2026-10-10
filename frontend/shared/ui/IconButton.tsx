import React, { forwardRef } from 'react';
import { Loader2 } from 'lucide-react';

export interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  'aria-label': string; // 强制提供 aria-label 消除无障碍隐患
  variant?: 'primary' | 'secondary' | 'accent' | 'ghost' | 'danger';
  size?: 'sm' | 'md' | 'lg';
  loading?: boolean;
}

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(
  (
    {
      'aria-label': ariaLabel,
      variant = 'ghost',
      size = 'md',
      loading = false,
      children,
      className = '',
      disabled,
      ...props
    },
    ref
  ) => {
    const sizeClasses = {
      sm: 'h-8 w-8 rounded-lg text-xs',
      md: 'h-9 w-9 rounded-xl text-sm',
      lg: 'h-11 w-11 rounded-2xl text-base',
    }[size];

    const variantClasses = {
      primary:
        'bg-[var(--color-ink)] text-[var(--color-surface)] hover:opacity-90 active:scale-95 shadow-sm',
      secondary:
        'border border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-ink)] hover:border-[var(--color-border-hover)] hover:bg-[var(--color-surface-hover)] active:scale-95',
      accent:
        'bg-[var(--color-accent)] text-white hover:bg-[var(--color-accent-hover)] active:scale-95 shadow-sm shadow-[var(--color-accent)]/20',
      ghost:
        'text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)] active:scale-95',
      danger:
        'text-[var(--color-danger)] hover:bg-[var(--color-danger-light)] active:scale-95',
    }[variant];

    return (
      <button
        ref={ref}
        aria-label={ariaLabel}
        title={ariaLabel}
        disabled={disabled || loading}
        className={`inline-flex items-center justify-center transition-all duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-accent)] focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 select-none ${sizeClasses} ${variantClasses} ${className}`}
        {...props}
      >
        {loading ? <Loader2 className="h-4 w-4 animate-spin text-current" /> : children}
      </button>
    );
  }
);

IconButton.displayName = 'IconButton';
