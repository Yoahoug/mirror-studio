import React from 'react';

export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: 'surface' | 'sunken' | 'elevated' | 'glass';
  hoverEffect?: boolean;
}

export function Card({
  variant = 'surface',
  hoverEffect = false,
  className = '',
  children,
  ...props
}: CardProps) {
  const variantClasses = {
    surface: 'bg-[var(--color-surface)] border border-[var(--color-border)] shadow-[var(--shadow-raised)]',
    sunken: 'bg-[var(--color-surface-sunken)] border border-[var(--color-border-subtle)]',
    elevated: 'bg-[var(--color-surface)] border border-[var(--color-border)] shadow-[var(--shadow-floating)]',
    glass: 'glass-chrome border border-[var(--color-border)]',
  }[variant];

  const hoverClass = hoverEffect
    ? 'transition-all duration-200 hover:-translate-y-0.5 hover:shadow-[var(--shadow-floating)] hover:border-[var(--color-border-hover)]'
    : '';

  return (
    <div
      className={`rounded-2xl p-5 ${variantClasses} ${hoverClass} ${className}`}
      {...props}
    >
      {children}
    </div>
  );
}
