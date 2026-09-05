export interface Quote {
  price_date: string;
  price_cents: number;
  change_cents: number;
  fetched_at: string;
}
export interface Product {
  id: number;
  name: string;
  specifications: string;
  unit: string;
  sort: number;
}
export interface Latest {
  source: string;
  price_basis: string;
  price_date: string;
  items: (Product & Quote)[];
}
export interface History {
  source: string;
  price_basis: string;
  product: Product;
  items: Quote[];
}
export interface SyncStatus {
  state: string;
  last_success_at: string;
  error_code: string;
}

export const money = (cents: number) =>
  (cents / 100).toLocaleString("zh-CN", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
export const change = (cents: number) =>
  `${cents > 0 ? "+" : cents < 0 ? "-" : ""}${money(Math.abs(cents))}`;
export const tone = (cents: number) =>
  cents > 0 ? "up" : cents < 0 ? "down" : "flat";
export function shiftDate(date: string, days: number) {
  const value = new Date(`${date}T00:00:00Z`);
  value.setUTCDate(value.getUTCDate() + days);
  return value.toISOString().slice(0, 10);
}
export function calendarSeries(
  items: Quote[],
  from: string,
  to: string,
): [string, number | null][] {
  const values = new Map(
    items.map((item) => [item.price_date, item.price_cents / 100]),
  );
  const result: [string, number | null][] = [];
  for (let date = from; date <= to; date = shiftDate(date, 1))
    result.push([date, values.get(date) ?? null]);
  return result;
}
export async function request<T>(
  path: string,
  signal?: AbortSignal,
): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.addEventListener("abort", abort, { once: true });
  if (signal?.aborted) abort();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    abort();
  }, 15000);
  try {
    const response = await fetch(`/api/platform/liquor/${path}`, {
      signal: controller.signal,
    });
    if (!response.ok) {
      if (response.status === 401 || response.status === 403)
        throw new Error("访问被拒绝，请检查代理访问权限。");
      throw new Error(
        `行情服务暂时不可用（HTTP ${response.status}），请稍后重试。`,
      );
    }
    if (!response.headers.get("content-type")?.includes("application/json"))
      throw new Error("服务未返回 JSON，请检查 API 代理配置。");
    return (await response.json()) as T;
  } catch (error) {
    if (timedOut) throw new Error("行情请求超时，请稍后重试。");
    throw error;
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", abort);
  }
}
