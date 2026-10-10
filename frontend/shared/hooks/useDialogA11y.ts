import { useEffect, useRef } from 'react';

/**
 * 弹窗无障碍外壳（台账 #12）：Esc 关闭、Tab 焦点圈定、打开时锁定滚动、
 * 关闭后归还焦点。供尚未迁移到共享 <Modal> 的旧弹窗使用：
 *
 *   const { dialogRef } = useDialogA11y({ open, onClose });
 *   <div ref={dialogRef} role="dialog" aria-modal="true" aria-label="设置">…</div>
 */
export function useDialogA11y({ open, onClose }: { open: boolean; onClose: () => void }) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const previouslyFocusedRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;

    previouslyFocusedRef.current = document.activeElement as HTMLElement | null;
    const originalOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key === 'Tab' && dialogRef.current) {
        const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
        );
        if (focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    const timer = setTimeout(() => dialogRef.current?.focus(), 50);

    return () => {
      document.body.style.overflow = originalOverflow;
      window.removeEventListener('keydown', handleKeyDown);
      clearTimeout(timer);
      previouslyFocusedRef.current?.focus();
    };
  }, [open, onClose]);

  return { dialogRef };
}
