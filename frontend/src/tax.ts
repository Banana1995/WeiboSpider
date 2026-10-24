export interface TaxRecord {
  date: string;
  account: string;
  account_id: string;
  kind: string;
  code: string;
  market: string;
  direction: string;
  currency: string;
  quantity: string;
  price: string;
  amount: string;
  fee: string;
  change: string;
  note: string;
  reason: string;
  source: string;
  line: number;
}
export interface TaxRate {
  year: number;
  currency: string;
  value: string;
  date: string;
  source: string;
}
export interface TaxBatch {
  id: number;
  name: string;
  digest: string;
  count: number;
  created: string;
}
export interface TaxState {
  imports: TaxBatch[];
  records: TaxRecord[];
  rates: TaxRate[];
}
export interface TaxSummary {
  year: number;
  currency: string;
  sell_close: string;
  buy_open: string;
  buy_close: string;
  sell_open: string;
  redemption: string;
  subscription: string;
  trade_fees: string;
  incurred_trade_fees: string;
  realized_open_fees: string;
  realized_close_fees: string;
  deferred_open_fees: string;
  unmatched_close_fees: string;
  other_fees: string;
  fees: string;
  net: string;
  dividends: string;
  profit_tax: string | null;
  dividend_tax: string | null;
  tax: string | null;
  rate: string | null;
  complete: boolean;
  missing_rate: boolean;
}
export interface TaxMatch {
  open: TaxRecord;
  close: TaxRecord;
  quantity: string;
  open_amount: string;
  close_amount: string;
  profit: string;
}
export interface TaxReport {
  rows: TaxSummary[];
  matches: TaxMatch[];
  entries: TaxDetail[];
  missing: {
    record: TaxRecord;
    quantity: string;
    amount: string;
    fee: string;
  }[];
  method: string;
}
export interface TaxOpening {
  record: TaxRecord;
  quantity: string;
  amount: string;
  fee: string;
}
export interface TaxDetail {
  record: TaxRecord;
  openings: TaxOpening[];
  has_close: boolean;
  fees: string;
  capitalized_fee: string;
  profit: string;
  dividends: string;
  complete: boolean;
}

export function currencyName(currency: string): string {
  return (
    ({ HKD: "港币", USD: "美元", CNY: "人民币" } as Record<string, string>)[
      currency
    ] ?? currency
  );
}
export function taxMoney(
  value: string | null | undefined,
  currency: string,
): string {
  if (value == null || value === "") return "—";
  if (!/^-?\d+(\.\d+)?$/.test(value)) return "—";
  const negative = value.startsWith("-");
  const [whole, fraction = ""] = (negative ? value.slice(1) : value).split(".");
  let cents = BigInt(whole!) * 100n + BigInt((fraction + "00").slice(0, 2));
  if ((fraction[2] ?? "0") >= "5") cents++;
  const text = `${negative && cents !== 0n ? "-" : ""}${cents / 100n}.${(cents % 100n).toString().padStart(2, "0")}`;
  return `${taxNumber(text)} ${currencyName(currency)}`;
}
export function taxPrice(value: string, currency: string): string {
  return value
    ? `${taxNumber(taxExact(value))} ${currencyName(currency)}`
    : "—";
}
export function securityIdentity(r: TaxRecord): {
  key: string;
  code: string;
  market: string;
} {
  let market = r.market,
    code = r.code.toUpperCase();
  if (r.kind === "资金进出") {
    const hk = r.note.match(/<SEHK\s+([0-9]{1,5})\s/);
    const us = r.note.match(/^([A-Z][A-Z0-9.-]{0,15})\s+[0-9.]+\s+SHARES\s/);
    if (hk) {
      market = "SEHK";
      code = hk[1]!;
    } else if (us) {
      market = "US";
      code = us[1]!;
    }
  } else if (r.kind !== "证券") {
    return { key: "", code: r.code, market: r.market };
  }
  if ((market === "SEHK" || market === "HK") && /^\d{1,5}$/.test(code)) {
    code = code.padStart(5, "0");
    return { key: `hk${code}`, code, market: "港股" };
  }
  if (market === "US" && /^[A-Z][A-Z0-9.-]{0,15}$/.test(code))
    return { key: `us${code}`, code, market: "美股" };
  return { key: "", code: r.code, market: r.market };
}
export function pivotRates(rates: TaxRate[]) {
  const years = new Map<
    number,
    { year: number; HKD: string | null; USD: string | null }
  >();
  for (const rate of rates) {
    const row = years.get(rate.year) ?? {
      year: rate.year,
      HKD: null,
      USD: null,
    };
    if (rate.currency === "HKD" || rate.currency === "USD")
      row[rate.currency] = rate.value;
    years.set(rate.year, row);
  }
  return [...years.values()].sort((a, b) => b.year - a.year);
}
export interface TaxPreview {
  digest: string;
  count: number;
  added: number;
  rows: TaxRecord[];
}

export function taxKey(): string {
  const name = "tax-workspace-v1";
  let key = localStorage.getItem(name);
  if (!key || !/^[a-f0-9]{64}$/.test(key)) {
    key = Array.from(crypto.getRandomValues(new Uint8Array(32)), (n) =>
      n.toString(16).padStart(2, "0"),
    ).join("");
    localStorage.setItem(name, key);
  }
  return key;
}
export async function taxRequest<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Tax-Key", taxKey());
  const response = await fetch(`/api/platform/tax/${path}`, {
    ...init,
    headers,
    cache: "no-store",
  });
  const body = await response.json().catch(() => null);
  if (!response.ok)
    throw new Error(body?.message || `税务服务请求失败（${response.status}）`);
  if (!body) throw new Error("税务服务返回了无效数据");
  return body as T;
}
export function taxDay(date: string) {
  return date.length === 8
    ? `${date.slice(0, 4)}-${date.slice(4, 6)}-${date.slice(6, 8)}`
    : date.slice(0, 10);
}
export function taxNumber(value: string | null | undefined): string {
  if (value == null || value === "") return "—";
  const [whole, decimal] = value.split(".");
  return (
    whole!.replace(/\B(?=(\d{3})+(?!\d))/g, ",") +
    (decimal === undefined ? "" : `.${decimal}`)
  );
}
export function taxExact(value: string): string {
  return value.includes(".")
    ? value.replace(/0+$/, "").replace(/\.$/, "")
    : value;
}
export const summaryColumns: [keyof TaxSummary, string][] = [
  ["sell_close", "卖出平仓"],
  ["buy_open", "对应买入开仓"],
  ["buy_close", "买入平仓"],
  ["sell_open", "对应卖出开仓"],
  ["redemption", "赎回金额"],
  ["subscription", "对应申购"],
  ["trade_fees", "计入损益交易费"],
  ["other_fees", "其他费用"],
  ["fees", "计入损益总费用"],
  ["deferred_open_fees", "期末待结转开仓费"],
];
