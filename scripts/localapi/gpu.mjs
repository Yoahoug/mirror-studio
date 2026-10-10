// 本机 GPU 资源互斥：ComfyUI 生图与 TTS 合成共用一块 8G 显存，同一时刻只允许一类任务在跑。
// 锁是进程内的（本地模式只有一个 Node 进程），带持有者标签便于排障；
// acquire 失败时返回当前持有者信息，前端据此提示「等一等或先释放」。
const waiters = [];

let lock = null; // { owner: 'comfy' | 'tts', label: string, acquiredAt: number }

export function gpuLockHolder() {
  if (!lock) return null;
  return {
    owner: lock.owner,
    label: lock.label,
    held_for_ms: Date.now() - lock.acquiredAt,
    waiting: waiters.length,
  };
}

/**
 * 请求 GPU 使用权。拿到后返回 release() 函数；拿不到返回 null（不排队，任务直接失败并给出提示）。
 * 选择「不排队」是因为两类任务都是分钟级长任务，静默排队会让前端进度条停在 15% 很久；
 * 失败信息里带上当前占用者，用户可以点「释放内存」或等它跑完再提交。
 */
export function acquireGpu(owner, label = '') {
  if (lock) return null;
  lock = { owner, label, acquiredAt: Date.now() };
  return releaseGpu;
}

function releaseGpu() {
  lock = null;
  // 当前不排队；waiters 保留给未来需要公平排队时使用。
  while (waiters.length) {
    const waiter = waiters.shift();
    try {
      waiter.reject(new Error('GPU 等待队列已清理，请重新提交任务'));
    } catch {
      // 已 resolve 的忽略
    }
  }
}

/** 供「释放内存」按钮：返回释放前的持有者快照。 */
export function describeGpuLock() {
  return gpuLockHolder();
}
