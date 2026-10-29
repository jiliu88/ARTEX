const attempts = new WeakMap<AbortSignal, { failed: boolean }>();

export function notePollFailure(signal?: AbortSignal | null) {
  const attempt = signal && attempts.get(signal);
  if (attempt) attempt.failed = true;
}

// 每轮完成后调度下一轮，取消离开页面的请求，隐藏页面不产生新请求。
export function startPolling(
  load: (signal: AbortSignal) => Promise<unknown>,
  intervalMs: number,
  initialDelay = 0,
): () => void {
  let stopped = false;
  let running = false;
  let failures = 0;
  let controller: AbortController | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = (delay: number) => {
    clearTimeout(timer);
    if (!stopped && !document.hidden) timer = setTimeout(run, delay);
  };
  const run = async () => {
    if (stopped || running || document.hidden) return;
    running = true;
    controller = new AbortController();
    const attempt = { failed: false };
    attempts.set(controller.signal, attempt);
    try { await load(controller.signal); } catch { attempt.failed = true; }
    finally {
      attempts.delete(controller.signal);
      running = false;
      failures = attempt.failed ? failures + 1 : 0;
      schedule(failures ? [5000, 10000, 20000, 30000][Math.min(failures - 1, 3)] : intervalMs);
    }
  };
  const visibility = () => {
    clearTimeout(timer);
    if (document.hidden) controller?.abort();
    else if (!running) schedule(0);
  };
  document.addEventListener("visibilitychange", visibility);
  schedule(initialDelay);
  return () => {
    stopped = true;
    clearTimeout(timer);
    controller?.abort();
    document.removeEventListener("visibilitychange", visibility);
  };
}
