import {
  decimal,
  LedgerError,
  type Account,
  type Currency,
  type FXQuote,
  type Instrument,
  type StockQuote,
} from "./ledger";
import { money, opaqueID, validDay, versionString } from "./ledgerView";

export interface HoldingItem {
  instrument: Instrument;
  quantity: string;
  market_value: string | null;
  account_market_value: string | null;
  price: string | null;
  weight: string | null;
  quote_status: "current" | "prior_date" | "unavailable";
  quote: StockQuote | null;
  fx: FXQuote | null;
}
export interface HoldingsView {
  account_id: string;
  currency: Currency;
  source: "manual_snapshot";
  as_of: string;
  ledger_at: string;
  revision: string;
  manual_version: string;
  configured: boolean;
  cash: string | null;
  complete: boolean;
  total_assets: string | null;
  items: HoldingItem[];
}

// Drop insignificant zeros, never financial precision.
export function holdingNumber(
  value: string | null | undefined,
  fractionDigits = 0,
) {
  if (value == null) return money(value);
  const [whole, fraction = ""] = value.split(".");
  const digits = fraction.replace(/0+$/, "").padEnd(fractionDigits, "0");
  return money(whole + (digits ? `.${digits}` : ""));
}
const number = (v: unknown, scale: number): v is string =>
  typeof v === "string" && decimal(v, scale) && !v.startsWith("-");
const nullable = (v: unknown, scale: number) => v === null || number(v, scale);
const positive = (v: unknown, scale: number) =>
  number(v, scale) && /[1-9]/.test(v);
export function validateHoldings(
  v: HoldingsView,
  account: Account,
): HoldingsView {
  if (
    !v ||
    v.account_id !== account.id ||
    v.currency !== account.currency ||
    v.source !== "manual_snapshot" ||
    !validDay(v.as_of) ||
    typeof v.ledger_at !== "string" ||
    !Number.isFinite(Date.parse(v.ledger_at)) ||
    !/^[a-f0-9]{64}$/.test(v.revision) ||
    typeof v.configured !== "boolean" ||
    typeof v.complete !== "boolean" ||
    !nullable(v.cash, 2) ||
    !nullable(v.total_assets, 2) ||
    (v.configured
      ? v.cash === null || !versionString(v.manual_version)
      : v.cash !== null ||
        v.complete ||
        v.total_assets !== null ||
        v.manual_version !== "0") ||
    v.complete !== (v.total_assets !== null) ||
    !Array.isArray(v.items) ||
    v.items.length > 200 ||
    (!v.configured && v.items.length)
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
      !i.market ||
      typeof i.code !== "string" ||
      !i.code ||
      !["CNY", "HKD", "USD"].includes(i.currency) ||
      seen.has(`${i.market}/${i.code}`) ||
      !positive(item.quantity, 6) ||
      !["current", "prior_date", "unavailable"].includes(item.quote_status) ||
      !nullable(item.market_value, 2) ||
      !nullable(item.account_market_value, 2) ||
      !nullable(item.price, 6) ||
      !nullable(item.weight, 2) ||
      (!v.complete && item.weight !== null) ||
      (item.weight !== null &&
        (!positive(v.total_assets, 2) || item.account_market_value === null)) ||
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
    seen.add(`${i.market}/${i.code}`);
  }
  return v;
}
