import React, { Component, type ErrorInfo, type ReactNode } from 'react';
import { AlertTriangle, RefreshCcw } from 'lucide-react';
import { Button } from './Button';

interface Props {
  children: ReactNode;
  fallbackTitle?: string;
  onReset?: () => void;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  public state: State = {
    hasError: false,
    error: null,
  };

  public static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('ErrorBoundary caught an unhandled error:', error, errorInfo);
  }

  private handleReset = () => {
    this.setState({ hasError: false, error: null });
    if (this.props.onReset) {
      this.props.onReset();
    }
  };

  public render() {
    if (this.state.hasError) {
      return (
        <div className="flex min-h-[280px] w-full flex-col items-center justify-center rounded-3xl border border-[var(--color-danger)]/20 bg-[var(--color-surface)] p-8 text-center shadow-sm">
          <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--color-danger-light)] text-[var(--color-danger)] shadow-sm">
            <AlertTriangle className="h-6 w-6" />
          </div>
          <h2 className="text-base font-bold text-[var(--color-ink)]">
            {this.props.fallbackTitle || '该区域加载时遇到意外错误'}
          </h2>
          <p className="mt-1.5 max-w-md font-mono text-xs text-[var(--color-danger-text)]/80 break-words leading-relaxed">
            {this.state.error?.message || '未知运行时错误'}
          </p>
          <div className="mt-5 flex gap-2">
            <Button
              variant="secondary"
              size="sm"
              onClick={this.handleReset}
              icon={<RefreshCcw className="h-3.5 w-3.5" />}
            >
              重置并重试
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => window.location.reload()}
            >
              刷新页面
            </Button>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}
