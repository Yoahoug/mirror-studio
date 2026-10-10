import { useCallback, useEffect, useRef, useState } from 'react';
import type { TaskStatus, UnifiedTask } from '../types/common';

/**
 * 统一轮询上限为 900 次：
 * 旧版 GenerationWorkbench 为 50 次（~125 秒即超时放弃），而 ComfyUI 与 TTS 为 900 次。
 * 50 次对慢速生成渠道或队列等待极易导致前端提前判定失败；因此全站统一为 900 次（约 30~45 分钟），
 * 并辅以自适应重试与卸载自动 Abort 清理。
 */
export const MAX_POLL_ATTEMPTS = 900;
export const BASE_POLL_INTERVAL_MS = 2000;

export interface TaskPollingOptions<T extends UnifiedTask = UnifiedTask> {
  taskNo: string | null;
  fetcher: (taskNo: string, signal?: AbortSignal) => Promise<T>;
  onSuccess?: (task: T) => void;
  onError?: (error: Error) => void;
  maxAttempts?: number;
}

export function useTaskPolling<T extends UnifiedTask = UnifiedTask>({
  taskNo,
  fetcher,
  onSuccess,
  onError,
  maxAttempts = MAX_POLL_ATTEMPTS,
}: TaskPollingOptions<T>) {
  const [task, setTask] = useState<T | null>(null);
  const [isPolling, setIsPolling] = useState<boolean>(false);
  const [error, setError] = useState<Error | null>(null);

  const attemptRef = useRef<number>(0);
  const timerRef = useRef<NodeJS.Timeout | null>(null);
  const abortControllerRef = useRef<AbortController | null>(null);
  const isMountedRef = useRef<boolean>(true);

  const cleanup = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
  }, []);

  const poll = useCallback(async () => {
    if (!taskNo || !isMountedRef.current) return;

    cleanup();
    attemptRef.current += 1;

    if (attemptRef.current > maxAttempts) {
      const timeoutError = new Error(`任务等待超时（已轮询 ${maxAttempts} 次），请稍后在历史记录中查看状态。`);
      setError(timeoutError);
      setIsPolling(false);
      onError?.(timeoutError);
      return;
    }

    const controller = new AbortController();
    abortControllerRef.current = controller;

    try {
      const data = await fetcher(taskNo, controller.signal);
      if (!isMountedRef.current) return;

      setTask(data);
      setError(null);

      const status: TaskStatus = data.status;
      if (status === 'succeeded') {
        setIsPolling(false);
        onSuccess?.(data);
      } else if (status === 'failed' || status === 'canceled') {
        setIsPolling(false);
        const taskErr = new Error(data.error_message || (status === 'canceled' ? '任务已取消' : '任务执行失败'));
        setError(taskErr);
        onError?.(taskErr);
      } else {
        // queued 或 running，继续下一次轮询（平滑退避）
        setIsPolling(true);
        const delay = attemptRef.current > 30 ? 3000 : BASE_POLL_INTERVAL_MS;
        timerRef.current = setTimeout(poll, delay);
      }
    } catch (err: any) {
      if (!isMountedRef.current || controller.signal.aborted) return;
      // 网络瞬断时允许 5 次容错重试，不立即打断
      if (attemptRef.current < maxAttempts) {
        timerRef.current = setTimeout(poll, 3500);
      } else {
        setIsPolling(false);
        const finalErr = err instanceof Error ? err : new Error(String(err));
        setError(finalErr);
        onError?.(finalErr);
      }
    }
  }, [taskNo, fetcher, onSuccess, onError, maxAttempts, cleanup]);

  const startPolling = useCallback(() => {
    attemptRef.current = 0;
    setError(null);
    setIsPolling(true);
    poll();
  }, [poll]);

  useEffect(() => {
    isMountedRef.current = true;
    if (taskNo) {
      startPolling();
    } else {
      cleanup();
      setIsPolling(false);
      setTask(null);
    }

    return () => {
      isMountedRef.current = false;
      cleanup();
    };
  }, [taskNo, startPolling, cleanup]);

  return {
    task,
    isPolling,
    error,
    retry: startPolling,
    stop: cleanup,
  };
}
