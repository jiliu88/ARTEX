export type RequestOptions = RequestInit & { timeoutMs?: number };

export class HttpError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

// 定时器覆盖响应体读取；外部取消与超时共用一个可清理的控制器。
export async function requestJSON<T>(url: string, options: RequestOptions = {}): Promise<T> {
  const { timeoutMs = /^(GET|HEAD)$/i.test(options.method ?? "GET") ? 15000 : 60000,
    signal, ...init } = options;
  const controller = new AbortController();
  const cancel = () => controller.abort(signal?.reason);
  if (signal?.aborted) cancel();
  else signal?.addEventListener("abort", cancel, { once: true });
  const timer = setTimeout(() => controller.abort(new DOMException("请求超时，请重试", "TimeoutError")), timeoutMs);
  try {
    const response = await fetch(url, { ...init, signal: controller.signal });
    if (!response.ok) {
      let message = `请求失败：${response.status}`;
      try {
        const body: { error?: unknown } = await response.json();
        if (typeof body.error === "string" && body.error.trim()) message = body.error;
      } catch (error) {
        if (controller.signal.aborted) throw error;
      }
      throw new HttpError(message, response.status);
    }
    return response.status === 204 ? undefined as T : await response.json() as T;
  } catch (error) {
    if (controller.signal.aborted) throw controller.signal.reason;
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", cancel);
  }
}
