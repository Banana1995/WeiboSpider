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
  missing: { record: TaxRecord; quantity: string; amount: string }[];
  method: string;
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
export function taxCSV(rows: (string | number)[][]): string {
  return (
    "\uFEFF" +
    rows
      .map((row) =>
        row
          .map((cell) => {
            let text = String(cell);
            // Spreadsheet formula escaping only applies to text, not parsed decimals.
            if (!/^-?\d+(\.\d+)?$/.test(text) && /^[\s]*[=+@-]/.test(text))
              text = "'" + text;
            return '"' + text.replace(/"/g, '""') + '"';
          })
          .join(","),
      )
      .join("\r\n") +
    "\r\n"
  );
}
export function downloadTax(name: string, rows: (string | number)[][]) {
  const url = URL.createObjectURL(
    new Blob([taxCSV(rows)], { type: "text/csv;charset=utf-8" }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export const recordColumns: [keyof TaxRecord, string][] = [
  ["date", "日期"],
  ["account", "账户名称"],
  ["account_id", "账户号码"],
  ["kind", "品类"],
  ["code", "证券代码"],
  ["market", "交易所"],
  ["direction", "方向"],
  ["currency", "币种"],
  ["quantity", "数量"],
  ["price", "价格"],
  ["amount", "成交金额"],
  ["fee", "总费用"],
  ["change", "变动金额"],
  ["note", "备注"],
  ["reason", "变动原因"],
];
export const summaryColumns: [keyof TaxSummary, string][] = [
  ["sell_close", "卖出平仓"],
  ["buy_open", "对应买入开仓"],
  ["buy_close", "买入平仓"],
  ["sell_open", "对应卖出开仓"],
  ["redemption", "赎回金额"],
  ["subscription", "对应申购"],
  ["trade_fees", "交易费用"],
  ["other_fees", "其他费用"],
  ["fees", "总费用"],
];
