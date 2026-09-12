import {
  decimal,
  LedgerError,
  type Account,
  type Currency,
  type FXQuote,
  type Instrument,
  type Page,
  type StockQuote,
} from "./ledger";
import { money, opaqueID, validDay, versionString } from "./ledgerView";

export interface HoldingItem {
  instrument: Instrument;
  quantity: string;
  market_value: string | null;
  account_market_value: string | null;
  price: string | null;
  holding_cost: string | null;
  diluted_cost: string | null;
  weight: string | null;
  cost_status: "known" | "unknown" | "closed";
  cycle_id: string;
  quote_status: "current" | "prior_date" | "unavailable" | "closed";
  quote: StockQuote | null;
  fx: FXQuote | null;
}
export interface HoldingsView {
  account_id: string;
  currency: Currency;
  source: Account["current_holdings_input"];
  as_of: string;
  ledger_at: string;
  revision: string;
  manual_version: string | null;
  trade_date_floor: string | null;
  configured: boolean;
  cash: string | null;
  complete: boolean;
  total_assets: string | null;
  items: HoldingItem[];
}
export interface HoldingTransaction {
  id: string;
  kind: "buy" | "sell" | "dividend";
  date: string;
  quantity: string | null;
  price: string | null;
  amount: string;
  fee: string | null;
  note: string;
  cycle_id: string;
  source: "manual" | "operation";
  operation_id?: string;
}
export const tradeLabels = { buy: "买入", sell: "卖出", dividend: "分红" };
// Drop only insignificant trailing zeros; never round away financial precision.
export function holdingNumber(
  value: string | null | undefined,
  fractionDigits = 0,
) {
  if (value == null) return money(value);
  const [whole, fraction = ""] = value.split(".");
  const digits = fraction.replace(/0+$/, "").padEnd(fractionDigits, "0");
  return money(whole + (digits ? `.${digits}` : ""));
}
const number = (
  value: unknown,
  scale: number,
  negative = false,
): value is string =>
  typeof value === "string" &&
  decimal(value, scale) &&
  (negative || !value.startsWith("-"));
const nullable = (value: unknown, scale: number, negative = false) =>
  value === null || number(value, scale, negative);
const positive = (value: unknown, scale: number) =>
  number(value, scale) && /[1-9]/.test(value);
const cycleID = (value: unknown) =>
  typeof value === "string" && value.length > 0 && value.length <= 512;
export function validateHoldings(
  v: HoldingsView,
  account: Account,
): HoldingsView {
  if (
    !v ||
    v.account_id !== account.id ||
    v.currency !== account.currency ||
    v.source !== account.current_holdings_input ||
    !validDay(v.as_of) ||
    typeof v.ledger_at !== "string" ||
    !Number.isFinite(Date.parse(v.ledger_at)) ||
    !/^[a-f0-9]{64}$/.test(v.revision) ||
    typeof v.configured !== "boolean" ||
    typeof v.complete !== "boolean" ||
    !nullable(v.cash, 2) ||
    !nullable(v.total_assets, 2) ||
    (v.configured
      ? v.cash === null
      : v.cash !== null || v.complete || v.total_assets !== null) ||
    v.complete !== (v.total_assets !== null) ||
    (v.source === "manual_snapshot"
      ? v.configured
        ? !versionString(v.manual_version) ||
          !validDay(v.trade_date_floor) ||
          v.trade_date_floor > v.as_of
        : v.manual_version !== "0" || v.trade_date_floor !== null
      : v.manual_version !== null || v.trade_date_floor !== null) ||
    !Array.isArray(v.items) ||
    (!v.configured && v.items.length > 0)
  )
    throw new LedgerError("invalid_response");
  const seen = new Set<string>();
  for (const item of v.items) {
    const i = item?.instrument;
    if (
      !i ||
      !opaqueID(i.id) ||
      typeof i.name !== "string" ||
      !i.name.trim() ||
      typeof i.market !== "string" ||
      typeof i.code !== "string" ||
      !["CNY", "HKD", "USD"].includes(i.currency) ||
      seen.has(i.id) ||
      !number(item.quantity, 6) ||
      !cycleID(item.cycle_id) ||
      !["known", "unknown", "closed"].includes(item.cost_status) ||
      !["current", "prior_date", "unavailable", "closed"].includes(
        item.quote_status,
      ) ||
      !nullable(item.market_value, 2) ||
      !nullable(item.account_market_value, 2) ||
      !nullable(item.price, 6) ||
      !nullable(item.holding_cost, 6) ||
      !nullable(item.diluted_cost, 6, true) ||
      !nullable(item.weight, 2) ||
      (!v.complete && item.weight !== null) ||
      (item.weight !== null &&
        (!positive(v.total_assets, 2) || item.account_market_value === null)) ||
      (item.cost_status === "known"
        ? item.holding_cost === null ||
          item.diluted_cost === null ||
          !positive(item.quantity, 6)
        : item.holding_cost !== null || item.diluted_cost !== null) ||
      (item.cost_status === "closed") !== !positive(item.quantity, 6) ||
      (item.quote !== null &&
        (!item.quote ||
          item.quote.currency !== i.currency ||
          item.quote.price !== item.price ||
          !positive(item.price, 6) ||
          !validDay(item.quote.date) ||
          typeof item.quote.source !== "string" ||
          !Number.isFinite(Date.parse(item.quote.quoted_at)) ||
          !Number.isFinite(Date.parse(item.quote.fetched_at)))) ||
      (item.quote === null && item.price !== null) ||
      (item.fx !== null &&
        (!item.fx ||
          item.fx.base !== i.currency ||
          item.fx.quote !== account.currency ||
          !positive(item.fx.rate, 8) ||
          !validDay(item.fx.date) ||
          typeof item.fx.source !== "string"))
    )
      throw new LedgerError("invalid_response");
    seen.add(i.id);
  }
  return v;
}
export function validateHoldingTransactions(
  page: Page<HoldingTransaction>,
): Page<HoldingTransaction> {
  if (
    !page ||
    !Array.isArray(page.items) ||
    page.items.length > 100 ||
    (page.next_cursor !== undefined &&
      (typeof page.next_cursor !== "string" || page.next_cursor.length > 1024))
  )
    throw new LedgerError("invalid_response");
  const seen = new Set<string>();
  for (const [index, row] of page.items.entries()) {
    if (
      !row ||
      !opaqueID(row.id) ||
      seen.has(row.id) ||
      !Object.hasOwn(tradeLabels, row.kind) ||
      !validDay(row.date) ||
      (index > 0 && row.date > page.items[index - 1]!.date) ||
      !number(row.amount, 2) ||
      !nullable(row.fee, 2) ||
      typeof row.note !== "string" ||
      !cycleID(row.cycle_id) ||
      !["manual", "operation"].includes(row.source) ||
      (row.source === "operation" && !opaqueID(row.operation_id)) ||
      (row.kind === "dividend"
        ? row.quantity !== null ||
          row.price !== null ||
          row.fee !== null ||
          !positive(row.amount, 2)
        : !positive(row.quantity, 6) || !positive(row.price, 6))
    )
      throw new LedgerError("invalid_response");
    seen.add(row.id);
  }
  return page;
}
