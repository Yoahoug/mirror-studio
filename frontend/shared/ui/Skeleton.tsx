import React from 'react';

export interface SkeletonProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: 'rectangular' | 'circular' | 'rounded';
  width?: string | number;
  height?: string | number;
}

export function Skeleton({
  variant = 'rounded',
  width,
  height,
  className = '',
  style,
  ...props
}: SkeletonProps) {
  const variantClasses = {
    rectangular: 'rounded-none',
    circular: 'rounded-full',
    rounded: 'rounded-xl',
  }[variant];

  return (
    <div
      className={`animate-pulse bg-[var(--color-surface-hover)] ${variantClasses} ${className}`}
      style={{
        width,
        height,
        ...style,
      }}
      {...props}
    />
  );
}
