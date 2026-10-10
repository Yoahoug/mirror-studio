import React, { Component, type ErrorInfo, type ReactNode } from 'react';
import { AlertTriangle, RefreshCcw } from 'lucide-react';
import { Button } from './Button';
import { useLanguage } from '../i18n';

interface Props {
  children: ReactNode;
  fallbackTitle?: string;
  onReset?: () => void;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

/** 类组件拿不到 hooks，fallback 文案交给内部函数组件渲染。 */
function ErrorFallback({
  title,
  message,
  onReset,
}: {
  title?: string;
  message: string;
  onReset: () => void;
}) {
  const { t } = useLanguage();
  return (
    <div className="flex min-h-[280px] w-full flex-col items-center justify-center rounded-3xl border border-[var(--color-danger)]/20 bg-[var(--color-surface)] p-8 text-center shadow-sm">
      <div className="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-[var(--color-danger-light)] text-[var(--color-danger)] shadow-sm">
        <AlertTriangle className="h-6 w-6" />
      </div>
      <h2 className="text-base font-bold text-[var(--color-ink)]">{title || t.common.errorTitle}</h2>
      <p className="mt-1.5 max-w-md font-mono text-xs text-[var(--color-danger-text)]/80 break-words leading-relaxed">
        {message}
      </p>
      <div className="mt-5 flex gap-2">
        <Button
          variant="secondary"
          size="sm"
          onClick={onReset}
          icon={<RefreshCcw className="h-3.5 w-3.5" />}
        >
          {t.common.errorRetry}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          onClick={() => window.location.reload()}
        >
          {t.common.refresh}
        </Button>
      </div>
    </div>
  );
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
        <ErrorFallback
          title={this.props.fallbackTitle}
          message={this.state.error?.message || ''}
          onReset={this.handleReset}
        />
      );
    }

    return this.props.children;
  }
}
