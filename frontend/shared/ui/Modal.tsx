import React, { useEffect, useRef } from 'react';
import { AnimatePresence, motion } from 'motion/react';
import { X } from 'lucide-react';
import { IconButton } from './IconButton';
import { useLanguage } from '../i18n';

export interface ModalProps {
  open: boolean;
  onClose: () => void;
  title?: React.ReactNode;
  description?: React.ReactNode;
  children: React.ReactNode;
  maxWidth?: 'sm' | 'md' | 'lg' | 'xl' | '2xl';
  showCloseButton?: boolean;
}

export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  maxWidth = 'md',
  showCloseButton = true,
}: ModalProps) {
  const modalRef = useRef<HTMLDivElement>(null);
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);
  const { t } = useLanguage();
  // Esc 用「最新」回调，但不进依赖数组：否则调用方的内联 onClose 会让 effect 在每次
  // 父组件重渲染时重跑，把焦点从用户正在输入的控件抢回弹窗容器。
  const onCloseRef = useRef(onClose);

  useEffect(() => {
    onCloseRef.current = onClose;
  });

  const maxWidthClass = {
    sm: 'max-w-sm',
    md: 'max-w-md',
    lg: 'max-w-lg',
    xl: 'max-w-xl',
    '2xl': 'max-w-2xl',
  }[maxWidth];

  useEffect(() => {
    if (!open) return;

    // 记录打开前的焦点元素并在关闭后归还
    previouslyFocusedRef.current = document.activeElement as HTMLElement | null;

    // 锁定 body 滚动
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    // Esc 关闭与焦点陷阱
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onCloseRef.current();
        return;
      }

      if (event.key === 'Tab' && modalRef.current) {
        const focusableElements = modalRef.current.querySelectorAll<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
        );
        if (focusableElements.length === 0) return;

        const firstElement = focusableElements[0];
        const lastElement = focusableElements[focusableElements.length - 1];

        if (event.shiftKey && document.activeElement === firstElement) {
          event.preventDefault();
          lastElement.focus();
        } else if (!event.shiftKey && document.activeElement === lastElement) {
          event.preventDefault();
          firstElement.focus();
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);

    // 延迟圈定初始焦点
    const timer = setTimeout(() => {
      modalRef.current?.focus();
    }, 50);

    return () => {
      document.body.style.overflow = originalOverflow;
      window.removeEventListener('keydown', handleKeyDown);
      clearTimeout(timer);
      previouslyFocusedRef.current?.focus();
    };
  }, [open]);

  return (
    <AnimatePresence>
      {open && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 sm:p-6 overflow-y-auto">
          {/* 背景遮罩 */}
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.18 }}
            onClick={onClose}
            className="fixed inset-0 bg-black/40 backdrop-blur-sm"
            aria-hidden="true"
          />

          {/* 模态弹窗主体 */}
          <motion.div
            ref={modalRef}
            tabIndex={-1}
            role="dialog"
            aria-modal="true"
            aria-labelledby={title ? 'modal-title' : undefined}
            aria-describedby={description ? 'modal-desc' : undefined}
            initial={{ opacity: 0, scale: 0.96, y: 8 }}
            animate={{ opacity: 1, scale: 1, y: 0 }}
            exit={{ opacity: 0, scale: 0.96, y: 6 }}
            transition={{ type: 'spring', stiffness: 350, damping: 30 }}
            className={`relative w-full ${maxWidthClass} rounded-3xl border border-[var(--color-border)] bg-[var(--color-surface)] p-6 shadow-[var(--shadow-overlay)] focus:outline-none z-10`}
          >
            {(title || showCloseButton) && (
              <div className="mb-5 flex items-start justify-between gap-4">
                <div className="min-w-0 flex-1">
                  {title && (
                    <h2 id="modal-title" className="text-lg font-bold tracking-tight text-[var(--color-ink)]">
                      {title}
                    </h2>
                  )}
                  {description && (
                    <p id="modal-desc" className="mt-1 text-xs text-[var(--color-muted)] leading-relaxed">
                      {description}
                    </p>
                  )}
                </div>
                {showCloseButton && (
                  <IconButton
                    aria-label={t.common.close}
                    onClick={onClose}
                    size="sm"
                    className="text-[var(--color-muted)] hover:text-[var(--color-ink)]"
                  >
                    <X className="h-4 w-4" />
                  </IconButton>
                )}
              </div>
            )}
            <div>{children}</div>
          </motion.div>
        </div>
      )}
    </AnimatePresence>
  );
}
