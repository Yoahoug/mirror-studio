import React from 'react';
import { Loader2 } from 'lucide-react';
import { motion } from 'motion/react';

export interface SpinnerProps {
  size?: 'sm' | 'md' | 'lg';
  className?: string;
}

export function Spinner({ size = 'md', className = '' }: SpinnerProps) {
  const sizeClasses = {
    sm: 'h-3.5 w-3.5',
    md: 'h-5 w-5',
    lg: 'h-8 w-8',
  }[size];

  return <Loader2 className={`animate-spin text-current ${sizeClasses} ${className}`} />;
}

export interface ProgressBarProps {
  value: number; // 0 to 100
  className?: string;
  showLabel?: boolean;
}

export function ProgressBar({ value, className = '', showLabel = false }: ProgressBarProps) {
  const clamped = Math.max(0, Math.min(100, Math.round(value)));

  return (
    <div className={`w-full ${className}`}>
      <div className="h-1.5 w-full overflow-hidden rounded-full bg-[var(--color-surface-hover)] border border-[var(--color-border-subtle)]">
        <motion.div
          className="h-full rounded-full bg-[var(--color-accent)]"
          initial={{ width: 0 }}
          animate={{ width: `${clamped}%` }}
          transition={{ type: 'spring', stiffness: 200, damping: 25 }}
        />
      </div>
      {showLabel && (
        <div className="mt-1 flex justify-between text-[11px] font-bold text-[var(--color-muted)] tabular-nums">
          <span>处理进度</span>
          <span>{clamped}%</span>
        </div>
      )}
    </div>
  );
}
