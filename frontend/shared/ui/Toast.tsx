import React, { createContext, useCallback, useContext, useState } from 'react';
import { AnimatePresence, motion } from 'motion/react';
import { AlertCircle, CheckCircle2, Info, X } from 'lucide-react';
import { useLanguage } from '../i18n';

export type ToastType = 'success' | 'error' | 'info';

export interface ToastItem {
  id: string;
  message: string;
  type: ToastType;
}

interface ToastContextType {
  addToast: (message: string, type?: ToastType) => void;
  removeToast: (id: string) => void;
}

const ToastContext = createContext<ToastContextType | null>(null);

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const { t } = useLanguage();

  const removeToast = useCallback((id: string) => {
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const addToast = useCallback((message: string, type: ToastType = 'info') => {
    const id = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    setToasts((prev) => [...prev.slice(-4), { id, message, type }]); // 最大保留 5 条

    setTimeout(() => {
      removeToast(id);
    }, 3800);
  }, [removeToast]);

  return (
    <ToastContext.Provider value={{ addToast, removeToast }}>
      {children}
      <div
        className="fixed bottom-5 right-5 z-[200] flex flex-col gap-2 pointer-events-none max-w-sm w-full px-4 sm:px-0"
        aria-live="polite"
      >
        <AnimatePresence>
          {toasts.map((toast) => {
            const icons = {
              success: <CheckCircle2 className="h-4 w-4 text-[var(--color-success)] flex-shrink-0" />,
              error: <AlertCircle className="h-4 w-4 text-[var(--color-danger)] flex-shrink-0" />,
              info: <Info className="h-4 w-4 text-[var(--color-info)] flex-shrink-0" />,
            };

            const borderClasses = {
              success: 'border-[var(--color-success)]/25 bg-[var(--color-surface)]',
              error: 'border-[var(--color-danger)]/25 bg-[var(--color-surface)]',
              info: 'border-[var(--color-border)] bg-[var(--color-surface)]',
            };

            return (
              <motion.div
                key={toast.id}
                layout
                initial={{ opacity: 0, y: 16, scale: 0.94 }}
                animate={{ opacity: 1, y: 0, scale: 1 }}
                exit={{ opacity: 0, scale: 0.9, y: 10 }}
                transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                className={`pointer-events-auto flex items-center justify-between gap-3 rounded-2xl border p-3.5 shadow-[var(--shadow-floating)] backdrop-blur-md ${borderClasses[toast.type]}`}
              >
                <div className="flex items-center gap-2.5 min-w-0">
                  {icons[toast.type]}
                  <p className="text-xs font-semibold text-[var(--color-ink)] leading-snug break-words">
                    {toast.message}
                  </p>
                </div>
                <button
                  type="button"
                  aria-label={t.common.close}
                  onClick={() => removeToast(toast.id)}
                  className="rounded-lg p-1 text-[var(--color-muted)] hover:bg-[var(--color-surface-hover)] hover:text-[var(--color-ink)] transition-colors flex-shrink-0"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              </motion.div>
            );
          })}
        </AnimatePresence>
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  const context = useContext(ToastContext);
  if (!context) {
    throw new Error('useToast must be used within a ToastProvider');
  }
  return context;
}
