import React from 'react';
import { motion } from 'motion/react';

export interface SegmentedOption<T extends string = string> {
  value: T;
  label: React.ReactNode;
  icon?: React.ReactNode;
}

export interface SegmentedControlProps<T extends string = string> {
  options: SegmentedOption<T>[];
  value: T;
  onChange: (value: T) => void;
  className?: string;
  size?: 'sm' | 'md';
}

export function SegmentedControl<T extends string = string>({
  options,
  value,
  onChange,
  className = '',
  size = 'md',
}: SegmentedControlProps<T>) {
  const sizeClasses = {
    sm: 'p-0.5 text-xs h-8',
    md: 'p-1 text-xs sm:text-sm h-10',
  }[size];

  const itemHeightClass = {
    sm: 'h-7 px-2.5',
    md: 'h-8 px-3.5',
  }[size];

  return (
    <div
      role="radiogroup"
      className={`inline-flex items-center rounded-2xl border border-[var(--color-border-subtle)] bg-[var(--color-surface-sunken)] select-none ${sizeClasses} ${className}`}
    >
      {options.map((option) => {
        const isSelected = option.value === value;
        return (
          <button
            key={option.value}
            type="button"
            role="radio"
            aria-checked={isSelected}
            onClick={() => onChange(option.value)}
            className={`relative flex items-center justify-center gap-1.5 rounded-xl font-semibold transition-colors duration-150 z-10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#6d5dfc] ${itemHeightClass} ${
              isSelected
                ? 'text-[var(--color-ink)]'
                : 'text-[var(--color-muted)] hover:text-[var(--color-ink)]'
            }`}
          >
            {isSelected && (
              <motion.div
                layoutId={`segment-pill-${options.map((o) => o.value).join('-')}`}
                transition={{ type: 'spring', stiffness: 350, damping: 32 }}
                className="absolute inset-0 rounded-xl bg-[var(--color-surface)] shadow-sm -z-10"
              />
            )}
            {option.icon && <span className="h-3.5 w-3.5 flex-shrink-0">{option.icon}</span>}
            <span>{option.label}</span>
          </button>
        );
      })}
    </div>
  );
}
