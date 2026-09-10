import { decimal, LedgerError, type Account, type Page } from "./ledger";
import { accountRecordOriginLabels, type AccountEntry, type AccountRecord, type EffectiveSummary } from "./accountRecords";
import { pointLabels, type BasisPoint } from "./ledgerChart";
import { validReturns, type LedgerReturns } from "./ledgerReturns";

export const todayShanghai = () => new Intl.DateTimeFormat("sv-SE", {
  timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit",
}).format(new Date());
export function validDay(value: unknown): value is string {
  return typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value) &&
    !value.startsWith("0000-") && Number.isFinite(Date.parse(`${value}T00:00:00Z`)) &&
    new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;
}
export const opaqueID = (v: unknown): v is string => typeof v === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(v);
export const versionString = (v: unknown): v is string => typeof v === "string" && /^[1-9]\d{0,18}$/.test(v) && BigInt(v) <= 9223372036854775807n;
const moneyValue = (v: unknown): v is string => typeof v === "string" && decimal(v, 2);
const nullableMoney = (v: unknown) => v === null || moneyValue(v);
// Display never converts financial values to floating point, including large totals.
export function money(value: string | null | undefined) {
  if (value == null || value === "") return "—";
  const [whole, fraction] = value.split(".");
  return whole!.replace(/\B(?=(\d{3})+(?!\d))/g, ",") + (fraction === undefined ? "" : `.${fraction}`);
}
export function recordKind(r: Pick<AccountEntry, "kind" | "flow">) {
  return r.kind === "asset" ? "总资产" : r.kind === "log" ? "备注" : r.flow?.startsWith("-") ? "转出" : "转入";
}
export function validRecord(r: AccountRecord, accountID: string) {
  return !!(r && r.account_id === accountID && opaqueID(r.id) && versionString(r.version) &&
    (r.sequence === undefined || versionString(r.sequence)) && validDay(r.date) &&
    Object.hasOwn(accountRecordOriginLabels, r.origin) && typeof r.voided === "boolean" &&
    typeof r.note === "string" && nullableMoney(r.flow) && nullableMoney(r.total_assets) &&
    (r.total_assets === null || !r.total_assets.startsWith("-")) &&
    (r.kind === "asset" ? r.total_assets !== null && r.flow === null :
      r.kind === "cash_flow" ? r.flow !== null : r.kind === "log" && r.flow === null && r.total_assets === null) &&
    (!r.operation_id || opaqueID(r.operation_id)) &&
    (!r.quote_audit_id || versionString(r.quote_audit_id)));
}
export function recordPage(value: Page<AccountRecord>, id: string, from: string, to: string, status: string, cursor = "") {
  if (!value || !Array.isArray(value.items) || value.items.length > 100 ||
    (value.next_cursor !== undefined && typeof value.next_cursor !== "string")) throw new LedgerError("invalid_response");
  let previous = cursor ? cursor.split(":") : undefined;
  const seen = new Set<string>();
  for (const r of value.items) {
    if (!validRecord(r, id) || !versionString(r.sequence) || seen.has(r.id) ||
      (from && r.date < from) || (to && r.date > to) ||
      (status === "active" && r.voided) || (status === "voided" && !r.voided) ||
      (previous && (r.date > previous[0]! || (r.date === previous[0] && BigInt(r.sequence) >= BigInt(previous[1]!)))))
      throw new LedgerError("invalid_response");
    previous = [r.date, r.sequence];
    seen.add(r.id);
  }
  const last = value.items.at(-1);
  if (value.next_cursor && (!last || value.next_cursor !== `${last.date}:${last.sequence}`)) throw new LedgerError("invalid_response");
  return value;
}
export function validSummary(s: EffectiveSummary) {
  return !!(s && [s.row_count, s.asset_count, s.flow_count, s.log_count, s.voided_count, s.latest_asset_count]
    .every(v => Number.isSafeInteger(v) && v >= 0) &&
    [s.from, s.to, s.latest_asset_date].every(v => v === null || validDay(v)) &&
    typeof s.total_in === "string" && /^\d+\.\d{2}$/.test(s.total_in) &&
    typeof s.total_out === "string" && /^\d+\.\d{2}$/.test(s.total_out) && nullableMoney(s.latest_assets) &&
    (s.latest_assets === null) === (s.latest_asset_date === null));
}
export interface AnalysisBasis {
  account_id: string;
  currency: string;
  timezone: string;
  from: string;
  to: string;
  revision: string;
  change_revision: string;
  status: string;
  points: BasisPoint[];
  opening: BasisPoint | null;
  closing: BasisPoint | null;
  net_flow: string;
  previous_basis_affected: boolean;
  changes: { revision: string; source_id: string; source_version: string; from: string; reason: string }[];
  returns: LedgerReturns;
}
export function validateBasis(b: AnalysisBasis, account: Account, from: string, to: string): AnalysisBasis {
  const point = (p: BasisPoint) => !!(p && validDay(p.date) && opaqueID(p.record_id) &&
    versionString(p.sequence) && versionString(p.version) && Object.hasOwn(pointLabels, p.status) &&
    typeof p.selected === "boolean" && nullableMoney(p.assets) && nullableMoney(p.flow) &&
    typeof p.source_id === "string" && typeof p.source_version === "string" && typeof p.source_date === "string" &&
    (p.source_id === "" ? p.source_version === "" && p.source_date === "" : opaqueID(p.source_id) && versionString(p.source_version) && validDay(p.source_date) && p.source_date <= p.date) &&
    (!p.record || (validRecord(p.record, account.id) && p.record.id === p.record_id && p.record.date === p.date && p.record.version === p.version && p.record.sequence === p.sequence && p.record.flow === p.flow &&
      (p.record.total_assets === null || p.assets === p.record.total_assets) && !p.record.voided)) &&
    (!p.valuation || (p.valuation.account_id === account.id && p.valuation.id === p.record_id && p.valuation.as_of === p.date && p.valuation.currency === account.currency)) &&
    (!p.operation || ((p.operation.operation.account_id === account.id || p.operation.operation.to_account_id === account.id) &&
      p.operation.operation.id === p.record?.operation_id && p.operation.operation.date === p.date)));
  if (!b || b.account_id !== account.id || b.currency !== account.currency || b.timezone !== "Asia/Shanghai" ||
    b.from !== (from || "0001-01-01") || b.to !== to || !validDay(b.to) ||
    typeof b.revision !== "string" || !/^[a-f0-9]{64}$/.test(b.revision) ||
    typeof b.change_revision !== "string" || !/^\d+$/.test(b.change_revision) ||
    !["current", "unavailable", "pending_recalculation", "untracked_history"].includes(b.status) ||
    typeof b.previous_basis_affected !== "boolean" || typeof b.net_flow !== "string" || !/^-?\d+\.\d{2}$/.test(b.net_flow) ||
    !Array.isArray(b.points) || b.points.length > 10000 ||
    b.points.some((p, i) => !point(p) || p.date < b.from || p.date > b.to || (i > 0 &&
      (p.date < b.points[i - 1]!.date || (p.date === b.points[i - 1]!.date && BigInt(p.sequence) <= BigInt(b.points[i - 1]!.sequence))))) ||
    ![b.opening, b.closing].every(p => p === null || (point(p) && p.date <= b.to)) ||
    !Array.isArray(b.changes) || b.changes.length > 10000 ||
    b.changes.some(c => !c || !versionString(c.revision) || !opaqueID(c.source_id) || !versionString(c.source_version) || !validDay(c.from) || typeof c.reason !== "string") ||
    !validReturns(b.returns, b.revision, from, to)) throw new LedgerError("invalid_response");
  const selected = new Map<string, string>();
  const ids = new Set<string>();
  for (const p of b.points) {
    if (ids.has(p.record_id) || (p.selected && selected.has(p.date))) throw new LedgerError("invalid_response");
    ids.add(p.record_id);
    if (p.selected) selected.set(p.date, p.record_id);
  }
  if (b.returns.curve.some(p => p.baseline ? p.record_id !== b.returns.opening?.record_id : selected.get(p.date) !== p.record_id) ||
    [b.returns.opening, b.returns.closing].some(p => p !== null && (!point(p) || p.date > b.to)))
    throw new LedgerError("invalid_response");
  const byID = new Map(b.points.map(p => [p.record_id, p]));
  if (b.returns.flows.some(f => {
    const p = byID.get(f.record_id);
    return !p || p.date !== f.date || p.version !== f.version || p.flow !== f.flow;
  })) throw new LedgerError("invalid_response");
  return b;
}
