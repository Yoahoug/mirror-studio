import React, { forwardRef } from 'react';
import { Loader2 } from 'lucide-react';

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: 'primary' | 'secondary' | 'accent' | 'ghost' | 'danger';
  size?: 'sm' | 'md' | 'lg';
  loading?: boolean;
  icon?: React.ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      variant = 'secondary',
      size = 'md',
      loading = false,
      icon,
      children,
      className = '',
      disabled,
      ...props
    },
    ref
  ) => {
    const sizeClasses = {
      sm: 'h-8 px-3 text-xs gap-1.5 rounded-lg',
      md: 'h-10 px-4 text-sm gap-2 rounded-xl',
      lg: 'h-12 px-6 text-base gap-2.5 rounded-2xl',
    }[size];

    const variantClasses = {
      primary:
        'bg-[#1a1a1a] text-white hover:bg-black active:scale-[0.98] shadow-sm shadow-black/10 dark:bg-white dark:text-[#1a1a1a] dark:hover:bg-neutral-100',
      secondary:
        'border border-[var(--color-border)] bg-[var(--color-surface)] text-[var(--color-ink)] hover:border-[var(--color-border-hover)] hover:bg-[var(--color-surface-hover)] active:scale-[0.98]',
      accent:
        'bg-[#6d5dfc] text-white hover:bg-[#5b4ce0] active:scale-[0.98] shadow-sm shadow-[#6d5dfc]/20',
      ghost:
        'text-[var(--color-ink-secondary)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)] active:scale-[0.98]',
      danger:
        'bg-[var(--color-danger-light)] text-[var(--color-danger-text)] border border-[var(--color-danger)]/30 hover:bg-[var(--color-danger)] hover:text-white active:scale-[0.98]',
    }[variant];

    return (
      <button
        ref={ref}
        disabled={disabled || loading}
        className={`inline-flex items-center justify-center font-semibold transition-all duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#6d5dfc] focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 select-none ${sizeClasses} ${variantClasses} ${className}`}
        {...props}
      >
        {loading ? (
          <Loader2 className="h-4 w-4 animate-spin text-current" />
        ) : (
          icon && <span className="flex-shrink-0 text-current">{icon}</span>
        )}
        {children && <span>{children}</span>}
      </button>
    );
  }
);

Button.displayName = 'Button';
