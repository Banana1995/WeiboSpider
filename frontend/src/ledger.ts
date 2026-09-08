export type Decimal = string;
export type Currency = "CNY" | "HKD" | "USD";
export type Kind =
  | "deposit"
  | "withdrawal"
  | "buy"
  | "sell"
  | "deposit_buy"
  | "sell_withdraw"
  | "dividend"
  | "transfer";
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
export interface OpeningPosition {
  instrument_id: string;
  quantity: Decimal;
  cost: Decimal | null;
  diluted_basis: Decimal | null;
}
export interface AccountInput {
  id: string;
  name: string;
  currency: Currency;
  opening_date: string;
  opening_cash: Decimal;
  positions: OpeningPosition[];
}
export interface Account extends Omit<
  AccountInput,
  "positions" | "opening_cash"
> {
  accounting_mode: "holdings" | "reported";
  opening_cash: Decimal | null;
  version: string;
}
export interface AccountDetail extends Account {
  cash: Decimal | null;
}
export interface Position {
  instrument_id: string;
  cycle_id: string;
  quantity: Decimal;
  remaining_cost: Decimal | null;
  moving_average: Decimal | null;
  diluted_basis: Decimal | null;
  diluted_cost: Decimal | null;
  realized_profit: Decimal | null;
  dividends: Decimal;
}
export interface Operation {
  id: string;
  date: string;
  sequence: string;
  kind: Kind;
  account_id: string;
  to_account_id?: string;
  instrument_id?: string;
  amount?: Decimal;
  quantity?: Decimal;
  price?: Decimal;
  fee?: Decimal | null;
  fx?: FX | null;
  cycle_id?: string;
}
export interface LedgerRecord {
  operation: Operation & { voided: boolean };
  note: string;
  version: string;
  created_at: string;
  updated_at: string;
}
export interface Revision {
  record: LedgerRecord;
  reason: string;
}
export interface Page<T> {
  items: T[];
  next_cursor?: string;
}
export interface Mutation {
  operation: Operation;
  note: string;
  reason: string;
  expected_version?: string;
}
export const kinds: Record<Kind, string> = {
  deposit: "转入现金",
  withdrawal: "转出现金",
  buy: "买入",
  sell: "卖出",
  deposit_buy: "转入并买入",
  sell_withdraw: "卖出并转出",
  dividend: "分红",
  transfer: "账户间转账",
};
const errors: Record<string, string> = {
  invalid_import: "导入文件格式或内容不合法，请检查指定行列",
  preview_mismatch: "文件与预览不一致，请重新预览",
  currency_mismatch: "文件币种与目标账户不一致，不能导入",
  import_already_exists: "账户已有不同的导入批次，不支持增量合并",
  initialization_requires_empty_account:
    "导入仅用于初始化，账户已有业务记录，不能追加导入或清空重置",
  upload_too_large: "文件超过 8 MiB 限制",
  fx_unavailable: "腾讯汇率暂不可用，请重试或明确手工录入",
  fx_timeout: "腾讯汇率查询超时，请重试或明确手工录入",
  unsupported_currency: "汇率仅支持 CNY、HKD、USD",
  invalid_body: "请求字段格式不正确",
  invalid_query: "查询条件或资源 ID 不合法",
  invalid_idempotency_key: "幂等键不合法",
  invalid_version: "版本必须为正整数字符串",
  invalid_version_or_id: "版本或操作 ID 不匹配",
  invalid_operation: "业务字段、日期或全局日内序号不合法",
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
  idempotency_conflict: "幂等键与原始请求冲突，请保留原请求核查",
  version_conflict: "记录已被修改，请重新读取详情后更正",
  operation_voided: "记录已作废，不能更正",
  conflict: "ID 或日期序号冲突",
  body_too_large: "请求内容过大",
  content_type: "请求内容类型不正确",
  insufficient_cash: "历史重放后现金不足",
  insufficient_position: "历史重放后持仓不足",
  unsupported_operation: "不支持此操作（包括跨币种转账）",
  source_managed_record: "请修正或作废关联的持仓操作；转账两腿必须一起更新",
  data_integrity: "存储一致性检查失败",
  incomplete_valuation: "估值不完整，未保存总资产，请核对持仓和报价来源",
  internal_error: "服务内部错误",
  storage_busy: "存储繁忙，请按原请求重试",
  request_timeout: "请求超时，需按原请求确认结果",
};
export class LedgerError extends Error {
  constructor(
    public code: string,
    public status = 0,
    public operation_id?: string,
    public date?: string,
    public detail_code?: string,
    public row?: number,
    public column?: string,
  ) {
    super(
      `${errors[code] ?? "无法确认服务响应"} [${code}]${operation_id ? `；操作 ${operation_id}` : ""}${date ? `；日期 ${date}` : ""}${row ? `；第 ${row} 行` : ""}${column ? `；列 ${column}` : ""}`,
    );
  }
  get uncertain() {
    return this.status === 0 || this.status === 408 || this.status >= 500;
  }
}
export const failure = (e: unknown) =>
  e instanceof Error ? e.message : "请求失败";
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
        data.operation_id,
        data.date,
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
  let cursor = "";
  do {
    const page = await request<Page<T>>(
      path + query({ limit: "100", cursor }),
      { signal },
    );
    items.push(...page.items);
    cursor = page.next_cursor ?? "";
  } while (cursor && !signal.aborted);
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
    readonly account?: AccountInput,
  ) {
    this.body = JSON.stringify(payload);
    this.key = newID();
    if (account) this.account = JSON.parse(this.body) as AccountInput;
  }
  async run(): Promise<T> {
    if (this.running) throw new Error("请求正在确认中");
    this.running = true;
    try {
      if (this.account && this.uncertain) {
        try {
          const current = await request<AccountDetail>(
            `/accounts/${this.account.id}`,
          );
          const a = this.account;
          const normalize = (s: string) => {
            const [whole, fraction = ""] = s.split(".");
            return BigInt(whole! + fraction.padEnd(2, "0"));
          };
          if (
            current.id !== a.id ||
            current.name !== a.name ||
            current.currency !== a.currency ||
            current.opening_date !== a.opening_date ||
            current.opening_cash === null ||
            normalize(current.opening_cash) !== normalize(a.opening_cash)
          )
            throw new LedgerError("conflict", 409);
          return current as T;
        } catch (e) {
          if (!(
            e instanceof LedgerError &&
            e.code === "not_found" &&
            e.status === 404
          ))
            throw e;
        }
      }
      return await request<T>(this.path, {
        method: this.method,
        headers: {
          "Content-Type": "application/json",
          ...(!this.account && this.path !== "/instruments"
            ? { "Idempotency-Key": this.key }
            : {}),
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
