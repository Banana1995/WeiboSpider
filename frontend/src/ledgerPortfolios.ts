import { decimal, LedgerError, type Currency, type FXQuote } from "./ledger";
import { returnReasons, type ReturnMetric } from "./ledgerReturns";
import {
  opaqueID,
  validDay,
  validateBasis,
  versionString,
  type AnalysisBasis,
} from "./ledgerView";

export interface Portfolio {
  id: string;
  name: string;
  currency: Currency;
  account_ids: string[];
  version: string;
  created_at: string;
  updated_at: string;
}

export interface PortfolioEntry {
  id: string;
  account_id: string;
  record_id: string;
  date: string;
  kind: "cash_flow" | "opening";
  amount: string;
}

export interface PortfolioContribution {
  currency: Currency;
  account_id: string;
  name: string;
  state: "active" | "not_started" | "empty";
  first_date: string;
  initial_assets: string | null;
  source_date: string;
  from: string;
  to: string;
  assets: string;
  asset_share: ReturnMetric;
  profit: ReturnMetric;
  modified_dietz: ReturnMetric;
  xirr: ReturnMetric;
  twr: ReturnMetric;
  twr_annualized: ReturnMetric;
  carried: boolean;
}

export interface PortfolioBasis extends AnalysisBasis {
  fx: FXQuote[];
  portfolio: Portfolio;
  members: PortfolioContribution[];
  entries: PortfolioEntry[];
  carried: boolean;
}

export function validPortfolio(p: Portfolio): boolean {
  return !!(
    p &&
    opaqueID(p.id) &&
    typeof p.name === "string" &&
    p.name.trim() === p.name &&
    p.name.length > 0 &&
    ["CNY", "HKD", "USD"].includes(p.currency) &&
    versionString(p.version) &&
    [p.created_at, p.updated_at].every(
      (v) => typeof v === "string" && Number.isFinite(Date.parse(v)),
    ) &&
    Date.parse(p.updated_at) >= Date.parse(p.created_at) &&
    Array.isArray(p.account_ids) &&
    p.account_ids.length > 0 &&
    p.account_ids.length <= 50 &&
    p.account_ids.every(
      (id, i) => opaqueID(id) && (i === 0 || id > p.account_ids[i - 1]!),
    )
  );
}

const amount = (v: unknown): v is string =>
  typeof v === "string" && /^-?\d+\.\d{2}$/.test(v) && decimal(v, 2);
const metric = (m: ReturnMetric, rate = true) =>
  !!(
    m &&
    ["available", "reference", "unavailable"].includes(m.status) &&
    (m.status === "unavailable"
      ? m.value === null &&
        m.percentage === null &&
        Object.hasOwn(returnReasons, m.reason)
      : typeof m.value === "string" &&
        (rate ? /^-?\d{1,100}\.\d{12}$/ : /^-?\d{1,100}\.\d{2}$/).test(
          m.value,
        ) &&
        m.reason === "" &&
        (rate
          ? typeof m.percentage === "string" &&
            /^-?\d{1,100}\.\d{2}$/.test(m.percentage)
          : m.percentage === null))
  );
const cents = (value: string) => BigInt(value.replace(".", ""));

export function validatePortfolioBasis(
  b: PortfolioBasis,
  portfolio: Portfolio,
  from: string,
  to: string,
): PortfolioBasis {
  validateBasis(b, portfolio, from, to);
  if (
    !validPortfolio(b.portfolio) ||
    b.portfolio.id !== portfolio.id ||
    b.portfolio.version !== portfolio.version ||
    b.portfolio.currency !== portfolio.currency ||
    b.portfolio.account_ids.join(",") !== portfolio.account_ids.join(",") ||
    typeof b.carried !== "boolean" ||
    !Array.isArray(b.fx) ||
    b.fx.length > 2 ||
    new Set(b.fx.map((fx) => fx?.base)).size !== b.fx.length ||
    b.fx.some(
      (fx) =>
        !fx ||
        !["CNY", "HKD", "USD"].includes(fx.base) ||
        fx.base === portfolio.currency ||
        fx.quote !== portfolio.currency ||
        fx.mode !== "latest" ||
        typeof fx.rate !== "string" ||
        !decimal(fx.rate, 8) ||
        !/[1-9]/.test(fx.rate) ||
        fx.rate.startsWith("-") ||
        !validDay(fx.date) ||
        typeof fx.source !== "string" ||
        !fx.source ||
        !Number.isFinite(Date.parse(fx.fetched_at)) ||
        !Number.isFinite(Date.parse(fx.quoted_at ?? "")),
    ) ||
    !Array.isArray(b.members) ||
    b.members.length !== portfolio.account_ids.length ||
    b.members.some(
      (m, i) =>
        !m ||
        m.account_id !== portfolio.account_ids[i] ||
        typeof m.name !== "string" ||
        !["CNY", "HKD", "USD"].includes(m.currency) ||
        (m.currency !== portfolio.currency &&
          !b.fx.some((fx) => fx.base === m.currency)) ||
        !["active", "not_started", "empty"].includes(m.state) ||
        ![m.first_date, m.source_date, m.from, m.to].every(
          (d) => d === "" || (validDay(d) && d <= b.to),
        ) ||
        !amount(m.assets) ||
        m.assets.startsWith("-") ||
        (m.initial_assets !== null &&
          (!amount(m.initial_assets) || m.initial_assets.startsWith("-"))) ||
        typeof m.carried !== "boolean" ||
        !metric(m.profit, false) ||
        ![
          m.asset_share,
          m.modified_dietz,
          m.xirr,
          m.twr,
          m.twr_annualized,
        ].every((r) => metric(r)),
    ) ||
    !Array.isArray(b.entries) ||
    b.entries.length > 10050 ||
    b.entries.some(
      (e, i) =>
        !e ||
        !opaqueID(e.id) ||
        !portfolio.account_ids.includes(e.account_id) ||
        !validDay(e.date) ||
        e.date < b.from ||
        e.date > b.to ||
        !amount(e.amount) ||
        (e.kind === "opening"
          ? e.record_id !== "" || e.amount.startsWith("-")
          : e.kind !== "cash_flow" || !opaqueID(e.record_id)) ||
        (i > 0 && e.date < b.entries[i - 1]!.date),
    ) ||
    new Set(b.entries.map((e) => e.id)).size !== b.entries.length
  )
    throw new LedgerError("invalid_response");
  if (
    (b.closing?.assets != null &&
      b.members.reduce((total, m) => total + cents(m.assets), 0n) !==
        cents(b.closing.assets)) ||
    (b.returns.profit.value !== null &&
      (b.members.some((m) => m.profit.value === null) ||
        b.members.reduce((total, m) => total + cents(m.profit.value!), 0n) !==
          cents(b.returns.profit.value)))
  )
    throw new LedgerError("invalid_response");
  return b;
}
