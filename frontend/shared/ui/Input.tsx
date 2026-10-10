import React, { forwardRef } from 'react';

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  error?: string;
  helperText?: string;
  leftIcon?: React.ReactNode;
  rightIcon?: React.ReactNode;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ label, error, helperText, leftIcon, rightIcon, className = '', id, ...props }, ref) => {
    const inputId = id || (label ? `input-${label.toLowerCase().replace(/\s+/g, '-')}` : undefined);

    return (
      <div className="w-full">
        {label && (
          <label htmlFor={inputId} className="mb-1.5 block text-xs font-bold text-[var(--color-ink)]">
            {label}
          </label>
        )}
        <div className="relative flex items-center">
          {leftIcon && (
            <span className="pointer-events-none absolute left-3 flex items-center text-[var(--color-muted)]">
              {leftIcon}
            </span>
          )}
          <input
            ref={ref}
            id={inputId}
            className={`w-full rounded-xl border bg-[var(--color-surface)] px-3.5 py-2 text-sm text-[var(--color-ink)] placeholder-[var(--color-submuted)] transition-colors duration-150 focus:outline-none focus:ring-2 focus:ring-[#6d5dfc] focus:border-transparent disabled:cursor-not-allowed disabled:opacity-50 ${
              leftIcon ? 'pl-9' : ''
            } ${rightIcon ? 'pr-9' : ''} ${
              error
                ? 'border-[var(--color-danger)] focus:ring-[var(--color-danger)]'
                : 'border-[var(--color-border)] hover:border-[var(--color-border-hover)]'
            } ${className}`}
            {...props}
          />
          {rightIcon && (
            <span className="pointer-events-none absolute right-3 flex items-center text-[var(--color-muted)]">
              {rightIcon}
            </span>
          )}
        </div>
        {error ? (
          <p className="mt-1 text-xs text-[var(--color-danger)]">{error}</p>
        ) : helperText ? (
          <p className="mt-1 text-xs text-[var(--color-muted)]">{helperText}</p>
        ) : null}
      </div>
    );
  }
);

Input.displayName = 'Input';

export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> {
  label?: string;
  error?: string;
  helperText?: string;
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(
  ({ label, error, helperText, className = '', id, ...props }, ref) => {
    const textareaId = id || (label ? `textarea-${label.toLowerCase().replace(/\s+/g, '-')}` : undefined);

    return (
      <div className="w-full">
        {label && (
          <label htmlFor={textareaId} className="mb-1.5 block text-xs font-bold text-[var(--color-ink)]">
            {label}
          </label>
        )}
        <textarea
          ref={ref}
          id={textareaId}
          className={`w-full rounded-xl border bg-[var(--color-surface)] px-3.5 py-2.5 text-sm text-[var(--color-ink)] placeholder-[var(--color-submuted)] transition-colors duration-150 focus:outline-none focus:ring-2 focus:ring-[#6d5dfc] focus:border-transparent disabled:cursor-not-allowed disabled:opacity-50 ${
            error
              ? 'border-[var(--color-danger)] focus:ring-[var(--color-danger)]'
              : 'border-[var(--color-border)] hover:border-[var(--color-border-hover)]'
          } ${className}`}
          {...props}
        />
        {error ? (
          <p className="mt-1 text-xs text-[var(--color-danger)]">{error}</p>
        ) : helperText ? (
          <p className="mt-1 text-xs text-[var(--color-muted)]">{helperText}</p>
        ) : null}
      </div>
    );
  }
);

Textarea.displayName = 'Textarea';
