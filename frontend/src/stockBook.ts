import {
  decimal,
  LedgerError,
  type Account,
  type Currency,
  type FXQuote,
  type Instrument,
  type StockQuote,
} from "./ledger";
import { opaqueID, validDay, versionString } from "./ledgerView";

export interface DividendEvent {
  id: string;
  market: string;
  code: string;
  currency: Currency;
  per_share: string;
  record_date: string;
  ex_date: string;
  pay_date: string;
  source: string;
  content: string;
}
export interface StockEntry {
  id: string;
  version: string;
  sequence: string;
  instrument_id: string;
  kind: "buy" | "sell" | "dividend";
  date: string;
  quantity: string;
  price: string;
  fee: string;
  amount: string;
  fx: string;
  note: string;
  voided: boolean;
  override?: boolean;
  opening?: boolean;
  event?: DividendEvent;
  cycle?: string;
  settlement_fx?: FXQuote;
  created_at: string;
  updated_at: string;
}
export interface StockMetrics {
  cost: string | null;
  diluted_cost: string | null;
  profit: string | null;
  profit_rate: string | null;
  total_profit: string | null;
  total_rate: string | null;
  dividends: string;
  pending_dividend: string;
}
export interface StockItem {
  instrument: Instrument;
  quantity: string;
  price: string | null;
  market_value: string | null;
  account_market_value: string | null;
  weight: string | null;
  quote_status: "current" | "prior_date" | "unavailable" | "closed";
  quote: StockQuote | null;
  fx: FXQuote | null;
  opening: boolean;
  metrics: StockMetrics;
}
export interface StockBook {
  account_id: string;
  currency: Currency;
  version: string;
  cash: string;
  cash_date: string;
  as_of: string;
  positions_value: string | null;
  total_assets: string | null;
  complete: boolean;
  items: StockItem[];
  entries: StockEntry[];
  sync_checked_at: string;
  sync_message: string;
}
export interface StockWriteResult {
  account_id: string;
  version: string;
  id: string;
  action: string;
}
export const stockLabels = { buy: "买入", sell: "卖出", dividend: "分红" };
export const positiveDecimal = (v: string, scale: number) =>
  decimal(v, scale) && !v.startsWith("-") && /[1-9]/.test(v);
const number = (v: unknown, scale: number, signed = false): v is string =>
  typeof v === "string" && decimal(v, scale) && (signed || !v.startsWith("-"));
const nullable = (v: unknown, scale: number, signed = false) =>
  v === null || number(v, scale, signed);
const ratio = (v: unknown) =>
  v === null || (typeof v === "string" && /^-?\d{1,40}\.\d{2}$/.test(v));
export function validateStockBook(
  b: StockBook,
  account: Pick<Account, "id" | "currency">,
): StockBook {
  if (
    !b ||
    b.account_id !== account.id ||
    b.currency !== account.currency ||
    !(b.version === "0" || versionString(b.version)) ||
    !number(b.cash, 2) ||
    !validDay(b.as_of) ||
    !(b.cash_date === "" || validDay(b.cash_date)) ||
    !nullable(b.positions_value, 2) ||
    !nullable(b.total_assets, 2) ||
    typeof b.complete !== "boolean" ||
    b.complete !== (b.total_assets !== null) ||
    !Array.isArray(b.items) ||
    b.items.length > 200 ||
    !Array.isArray(b.entries) ||
    b.entries.length > 10000 ||
    typeof b.sync_checked_at !== "string" ||
    typeof b.sync_message !== "string"
  )
    throw new LedgerError("invalid_response");
  const ids = new Set<string>();
  const identities = new Set<string>();
  for (const row of b.items) {
    const i = row?.instrument,
      m = row?.metrics;
    if (
      !i ||
      !opaqueID(i.id) ||
      ids.has(i.id) ||
      typeof i.name !== "string" ||
      typeof i.market !== "string" ||
      typeof i.code !== "string" ||
      identities.has(`${i.market}/${i.code}`) ||
      !["CNY", "HKD", "USD"].includes(i.currency) ||
      !number(row.quantity, 6) ||
      !nullable(row.price, 6) ||
      !nullable(row.market_value, 2) ||
      !nullable(row.account_market_value, 2) ||
      !nullable(row.weight, 2) ||
      !["current", "prior_date", "unavailable", "closed"].includes(
        row.quote_status,
      ) ||
      typeof row.opening !== "boolean" ||
      !m ||
      !nullable(m.cost, 6) ||
      !nullable(m.diluted_cost, 6, true) ||
      !nullable(m.profit, 2, true) ||
      !nullable(m.total_profit, 2, true) ||
      !ratio(m.profit_rate) ||
      !ratio(m.total_rate) ||
      !number(m.dividends, 2) ||
      !number(m.pending_dividend, 2)
    )
      throw new LedgerError("invalid_response");
    if (
      row.quote !== null &&
      (!row.quote ||
        row.quote.price !== row.price ||
        row.quote.currency !== i.currency ||
        !validDay(row.quote.date))
    )
      throw new LedgerError("invalid_response");
    ids.add(i.id);
    identities.add(`${i.market}/${i.code}`);
  }
  const entryIDs = new Set<string>();
  for (const e of b.entries) {
    if (
      !e ||
      !opaqueID(e.id) ||
      entryIDs.has(e.id) ||
      !ids.has(e.instrument_id) ||
      !versionString(e.version) ||
      !versionString(e.sequence) ||
      !["buy", "sell", "dividend"].includes(e.kind) ||
      !validDay(e.date) ||
      !number(e.quantity, 6) ||
      !number(e.price, 6) ||
      !number(e.fee, 2) ||
      !number(e.amount, 2) ||
      !number(e.fx, 8) ||
      typeof e.voided !== "boolean" ||
      typeof e.note !== "string" ||
      (e.event &&
        (!opaqueID(e.event.id) ||
          !validDay(e.event.ex_date) ||
          !validDay(e.event.pay_date) ||
          !number(e.event.per_share, 6)))
    )
      throw new LedgerError("invalid_response");
    entryIDs.add(e.id);
  }
  return b;
}
export const stockTone = (value: string | null) =>
  value == null || !/[1-9]/.test(value)
    ? ""
    : value.startsWith("-")
      ? "stock-loss"
      : "stock-gain";
const units = (s: string, scale: number) => {
  const [w, f = ""] = s.split(".");
  return BigInt(w! + f.padEnd(scale, "0"));
};
export function tradePreview(
  quantity: string,
  price: string,
  fee: string,
  kind: string,
): string | null {
  if (
    !positiveDecimal(quantity, 6) ||
    !positiveDecimal(price, 6) ||
    !number(fee, 2)
  )
    return null;
  const gross =
    (units(quantity, 6) * units(price, 6) + 5000000000n) / 10000000000n;
  const net = gross + (kind === "sell" ? -units(fee, 2) : units(fee, 2));
  if (net < 0n || net > 9223372036854775807n) return null;
  return `${net / 100n}.${(net % 100n).toString().padStart(2, "0")}`;
}
