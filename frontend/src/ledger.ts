export type Decimal = string;
export type Currency = "CNY" | "HKD" | "USD";
export interface FX {
  rate: Decimal;
  date: string;
  source: string;
  fetched_at: string;
}
export interface FXQuote extends FX {
  base: Currency;
  quote: Currency;
  mode: "latest" | "historical";
  requested_date: string;
  quoted_at?: string;
}
export interface Instrument {
  id: string;
  market: string;
  code: string;
  name: string;
  currency: Currency;
}
export interface StockQuote {
  symbol: string;
  price: Decimal;
  currency: Currency;
  source: string;
  date: string;
  quoted_at: string;
  fetched_at: string;
}
export interface ValuationItem {
  instrument_id: string;
  quantity: Decimal;
  quote: StockQuote | null;
  fx: FXQuote | null;
  market_value: Decimal | null;
  status: "current" | "prior_date" | "unavailable" | "closed";
  error_code?: string;
}
export interface Valuation {
  source: "manual_snapshot";
  current_holdings?: import("./currentHoldings").CurrentHoldings;
  ledger_revision: string;
  history_id?: string;
  account_id: string;
  currency: Currency;
  as_of: string;
  ledger_at: string;
  calculated_at: string;
  cash: Decimal;
  known_positions_value: Decimal;
  positions_value: Decimal | null;
  total_assets: Decimal | null;
  complete: boolean;
  items: ValuationItem[];
}
export interface ValuationSummary {
  id: string;
  account_id: string;
  currency: Currency;
  as_of: string;
  ledger_at: string;
  calculated_at: string;
  saved_at: string;
  ledger_revision: string;
  cash: Decimal;
  positions_value: Decimal;
  total_assets: Decimal;
}
export interface ValuationHistory {
  id: string;
  saved_at: string;
  schema_version: 1;
  account_name: string;
  valuation: Omit<Valuation, "history_id">;
  instruments: Instrument[];
}
export interface AccountInput {
  id: string;
  name: string;
  currency: Currency;
  opening_date: string;
}
export interface Account extends AccountInput {
  current_holdings_input: "manual_snapshot";
  opening_cash: Decimal | null;
  version: string;
}
export interface AccountDetail extends Account {
  cash: Decimal | null;
}
export interface Page<T> {
  items: T[];
  next_cursor?: string;
}
const errors: Record<string, string> = {
  network_error: "网络连接中断，请重试",
  invalid_import: "导入文件格式或内容不合法，请检查指定行列",
  preview_mismatch: "文件校验不一致，请重新选择文件后导入",
  currency_mismatch: "文件币种与目标账户不一致，不能导入",
  import_already_exists: "账户已有不同的导入批次，不支持增量合并",
  initialization_requires_empty_account:
    "导入仅用于初始化，账户已有业务记录，不能追加导入或清空重置",
  upload_too_large: "文件超过 8 MiB 限制",
  fx_unavailable: "腾讯汇率暂不可用，请稍后重试",
  fx_timeout: "腾讯汇率查询超时，请稍后重试",
  unsupported_currency: "汇率仅支持 CNY、HKD、USD",
  invalid_body: "请求字段格式不正确",
  invalid_query: "查询条件不合法",
  invalid_idempotency_key: "请求凭据不合法",
  invalid_operation: "账本字段或日期不合法",
  invalid_precision: "数值精度或范围不合法",
  unauthorized: "服务未授权，请检查受控代理配置",
  invalid_host: "Host 访问检查未通过",
  cross_origin: "跨站写入被拒绝",
  local_only: "账本仅允许受信任的本机访问",
  local_proxy_disabled:
    "记账开发代理未开启，请以 LEDGER_DEV_PROXY=true 启动前端",
  not_found: "资源不存在或账本尚未启用",
  method_not_allowed: "接口不支持此方法",
  request_canceled: "请求已取消，需使用原请求确认结果",
  idempotency_conflict: "本次请求与已保存的内容冲突，请保留此页核查",
  version_conflict: "记录已被修改，请重新读取详情后更正",
  basis_changed: "报价期间当前持仓已变化，本次未保存",
  operation_voided: "记录已作废，不能更正",
  conflict: "账户信息或证券身份重复",
  body_too_large: "请求内容过大",
  content_type: "请求内容类型不正确",
  unsupported_operation: "尚无当前估值来源，或不支持此操作",
  data_integrity: "存储一致性检查失败",
  incomplete_valuation: "估值不完整，未保存总资产，请核对持仓和报价来源",
  internal_error: "服务内部错误",
  storage_busy: "存储繁忙，请按原请求重试",
  instrument_search_unavailable: "证券查询服务暂不可用",
  instrument_search_timeout: "证券查询超时",
  request_timeout: "请求超时，需按原请求确认结果",
};
export class LedgerError extends Error {
  constructor(
    public code: string,
    public status = 0,
    public detail_code?: string,
    public row?: number,
    public column?: string,
  ) {
    super(
      `${errors[code] ?? "无法确认服务响应"} [${code}]${row ? `；第 ${row} 行` : ""}${column ? `；列 ${column}` : ""}`,
    );
  }
  get uncertain() {
    return this.status === 0 || this.status === 408 || this.status >= 500;
  }
}
export const failure = (e: unknown) =>
  e instanceof Error ? e.message : "请求失败";
// Keep typed/debug codes in state for conflict handling, not in business copy.
export const errorText = (message: string) =>
  message.replace(/\s\[[a-z_]+\]/g, "");
export const newID = () => {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
};
export function query(values: Record<string, string>) {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(values))
    if (value) params.set(key, value);
  return params.size ? `?${params}` : "";
}
export function decimal(value: string, digits: number): boolean {
  if (
    value.length > 32 ||
    !new RegExp(`^-?\\d+(?:\\.\\d{1,${digits}})?$`).test(value)
  )
    return false;
  const [whole, fraction = ""] = value.replace("-", "").split(".");
  const scaled = BigInt(whole! + fraction.padEnd(digits, "0"));
  return (
    scaled <=
    (value.startsWith("-") ? 9223372036854775808n : 9223372036854775807n)
  );
}
export async function request<T>(
  path: string,
  options: RequestInit = {},
  timeout = 15000,
): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  options.signal?.addEventListener("abort", abort, { once: true });
  if (options.signal?.aborted) abort();
  const timer = setTimeout(abort, timeout);
  try {
    const response = await fetch(`/api/platform/ledger${path}`, {
      ...options,
      cache: "no-store",
      signal: controller.signal,
    });
    const data = await response.json();
    if (!response.ok)
      throw new LedgerError(
        data.code ?? "invalid_response",
        response.status,
        data.detail_code,
        data.row,
        data.column,
      );
    if (controller.signal.aborted) throw new LedgerError("request_timeout");
    return data as T;
  } catch (e) {
    if (e instanceof LedgerError) throw e;
    throw new LedgerError(
      controller.signal.aborted ? "request_timeout" : "network_error",
    );
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener("abort", abort);
  }
}
export async function all<T>(path: string, signal: AbortSignal): Promise<T[]> {
  const items: T[] = [];
  const cursors = new Set<string>();
  let cursor = "";
  do {
    if (signal.aborted) throw new LedgerError("request_canceled");
    if (cursors.has(cursor) || cursors.size >= 100)
      throw new Error(
        "记录较多或分页发生变化，无法完整读取。请重新加载，不展示部分合计。",
      );
    cursors.add(cursor);
    const page = await request<Page<T>>(
      path + query({ limit: "100", cursor }),
      { signal },
    );
    if (
      !page ||
      !Array.isArray(page.items) ||
      page.items.length > 100 ||
      (page.next_cursor !== undefined && typeof page.next_cursor !== "string")
    )
      throw new LedgerError("invalid_response");
    items.push(...page.items);
    cursor = page.next_cursor ?? "";
  } while (cursor);
  if (signal.aborted) throw new LedgerError("request_canceled");
  return items;
}
// A write owns immutable bytes and identity. A network error never creates a new key.
export class PendingWrite<T> {
  readonly body: string;
  readonly key: string;
  uncertain = false;
  private running = false;
  constructor(
    readonly path: string,
    readonly method: "POST" | "PUT" | "DELETE",
    payload: unknown,
  ) {
    this.body = JSON.stringify(payload);
    this.key = newID();
  }
  async run(): Promise<T> {
    if (this.running) throw new Error("请求正在确认中");
    this.running = true;
    try {
      return await request<T>(this.path, {
        method: this.method,
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": this.key,
        },
        body: this.body,
      });
    } catch (e) {
      if (!(e instanceof LedgerError) || e.uncertain) this.uncertain = true;
      throw e;
    } finally {
      this.running = false;
    }
  }
}
