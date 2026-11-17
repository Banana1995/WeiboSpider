export type OptionSide = "PUT" | "CALL";
export const sideLabel = (side?: string) => (side === "CALL" ? "Call" : "Put");
export const candidateSide = (value: Candidate): OptionSide =>
  value.quote.contract.type === "CALL" ? "CALL" : "PUT";
export const capitalLabel = (side?: string) =>
  side === "CALL" ? "备兑股票参考市值" : "全额履约金额";
export const costLabel = (side?: string) =>
  side === "CALL" ? "收权利金后持股参考成本／股" : "扣权利金后接货成本／股";

export interface Instrument {
  code: string;
  name: string;
  english_name: string;
  aliases: string;
  kind: string;
  currency: string;
}
export interface Filter {
  min_days: string;
  max_days: string;
  max_strike: string;
  min_strike: string;
  min_sale_price: string;
  max_cost: string;
  min_apr: string;
  min_oi: string;
  min_volume: string;
  max_spread: string;
  max_delta: string;
  budget: string;
  moneyness: string;
}
export const emptyFilter = (): Filter => ({
  min_days: "",
  max_days: "",
  max_strike: "",
  min_strike: "",
  min_sale_price: "",
  max_cost: "",
  min_apr: "",
  min_oi: "",
  min_volume: "",
  max_spread: "",
  max_delta: "",
  budget: "",
  moneyness: "",
});
export interface Watch extends Instrument {
  enabled: boolean;
  version: number;
  filter: Filter;
  call_filter: Filter;
}
export interface FeeRule {
  per_contract: string;
  minimum: string;
  fixed: string;
  regulatory_per_contract: string;
}
export const emptyFee = (): FeeRule => ({
  per_contract: "",
  minimum: "",
  fixed: "",
  regulatory_per_contract: "",
});
export interface FeeProfile {
  name: string;
  source: string;
  open: FeeRule;
  close: FeeRule | null;
  assignment: FeeRule | null;
}
export interface Settings {
  version: number;
  mode: "full" | "net";
  fees: FeeProfile | null;
  filter: Filter;
  call_filter: Filter;
  horizon_days: number;
}
export interface Contract {
  code: string;
  symbol: string;
  name: string;
  expiry: string;
  strike: string;
  multiplier: number;
  type: string;
  standard: string;
  settlement: string;
  currency: string;
}
export interface Quote {
  contract: Contract;
  bid: string | null;
  ask: string | null;
  bid_size: string | null;
  ask_size: string | null;
  volume: string | null;
  oi: string | null;
  iv: string | null;
  delta: string | null;
  last_price: string | null;
  last_trade_at: string | null;
  quote_at: string | null;
  status: string;
}
export interface Snapshot {
  id: string;
  option_type?: OptionSide;
  symbol: string;
  name: string;
  source: string;
  started_at: string;
  captured_at: string;
  valuation_date_et: string;
  market_state: string;
  underlying: string | null;
  from: string;
  to: string;
  complete: boolean;
  total_contracts: number;
  warning: string;
}
export interface CalculationInput {
  quantity: number;
  basis: "bid" | "mid" | "custom";
  price: string | null;
  mode: "full" | "net";
  fee_version: number;
  fee_override: string | null;
  interest: { principal: string; annual_rate: string; days: number } | null;
  scenario: {
    kind: "expiry" | "close";
    price: string;
    fee: string | null;
    interest: string | null;
    stock_cost?: string | null;
  } | null;
}
export interface Calculation {
  version: string;
  status: string;
  dte: number;
  price: string | null;
  gross: string | null;
  intrinsic: string | null;
  time_value: string | null;
  fee: string | null;
  net: string | null;
  income: string | null;
  capital: string | null;
  base: string | null;
  rate: string | null;
  apr: string | null;
  cost: string | null;
  interest: string | null;
  combined_apr: string | null;
  scenario_pnl: string | null;
  scenario_fees_known: boolean;
  scenario_interest_known: boolean;
  capital_basis?: "stock_value" | "strike_value";
  covered_shares?: string | null;
  strike_proceeds?: string | null;
  net_sale_price?: string | null;
  covered_pnl?: string | null;
}
export interface Candidate {
  snapshot: Snapshot;
  quote: Quote;
  input: CalculationInput;
  result: Calculation;
  favorite_id?: string;
  budget_quantity: string | null;
  stale: boolean;
}
export interface CandidatePage {
  items: Candidate[];
  total: number;
  available: number;
  excluded: Record<string, number>;
  snapshots: Snapshot[];
  next_cursor?: string;
  view: string;
}
export interface Favorite {
  id: string;
  created_at: string;
  format_version: number;
  candidate: Candidate;
  fee_profile: FeeProfile | null;
}
export interface FavoritePage {
  items: Favorite[];
  total: number;
  next_cursor?: string;
}
export interface Run {
  id: string;
  option_type?: OptionSide;
  state: string;
  created_at: string;
  finished_at?: string;
  targets: {
    symbol: string;
    state: string;
    progress: string;
    error?: string;
    snapshot_id?: string;
  }[];
}
export interface Status {
  history_limited?: boolean;
  provider: {
    connected: boolean;
    version: string;
    market_state: string;
    us_options: string;
    checked_at: string;
    error?: string;
  };
  run: Run | null;
  favorite_count: number;
  manual_only: boolean;
}

export const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));
// HTTP origins are not secure contexts, so randomUUID can be missing there.
export const requestID = () => {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
};
export const money = (value: string | null | undefined, digits = 2) =>
  value == null
    ? "—"
    : Number(value).toLocaleString("en-US", {
        minimumFractionDigits: digits,
        maximumFractionDigits: digits,
      });
export const percent = (value: string | null | undefined) =>
  value == null ? "—" : `${money(value)}%`;
export const beijing = (value: string | null | undefined) =>
  value
    ? new Intl.DateTimeFormat("zh-CN", {
        timeZone: "Asia/Shanghai",
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      }).format(new Date(value))
    : "来源未提供";
export const symbolLabel = (code: string) => code.replace(/^US\./, "");
export const identity = (c: Candidate) =>
  `${c.snapshot.id}:${c.quote.contract.code}`;
export const statusText = (status: string) =>
  ({
    ok: "",
    quote_unavailable: "卖出价 Bid 不可用，可输入自定义价格测算",
    unsupported_contract: "非标准合约或交割属性未确认，暂不计算",
    expired: "估值日已到期，年化不计算",
    invalid_base: "资金基数非正，收入率不计算",
    underlying_unavailable: "缺少正股参考价，暂不能计算备兑股票市值与年化",
  })[status] ?? status;
export const running = (run: Run | null | undefined) =>
  run?.state === "running" || run?.state === "queued";
export async function optionsRequest<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const controller = new AbortController();
  const abort = () => controller.abort();
  init.signal?.addEventListener("abort", abort, { once: true });
  if (init.signal?.aborted) abort();
  const timeout = setTimeout(abort, 30000);
  try {
    const response = await fetch(`/api/platform/options${path}`, {
      ...init,
      cache: "no-store",
      signal: controller.signal,
    });
    const data = await response.json().catch(() => null);
    if (!response.ok)
      throw new Error(
        data?.message ??
          (response.status === 401
            ? "请登录个人期权页面"
            : `服务返回 HTTP ${response.status}`),
      );
    if (!data) throw new Error("期权服务返回了无效数据");
    return data as T;
  } finally {
    clearTimeout(timeout);
    init.signal?.removeEventListener("abort", abort);
  }
}
export const write = <T>(
  path: string,
  body: unknown,
  method = "POST",
  signal?: AbortSignal,
) =>
  optionsRequest<T>(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal,
  });
