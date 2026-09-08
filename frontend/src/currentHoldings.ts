import { decimal, LedgerError, type Decimal } from "./ledger";

export interface CurrentPosition {
  instrument_id: string;
  quantity: Decimal;
}
export interface CurrentHoldings {
  account_id: string;
  audit_id: string;
  snapshot: {
    version: string;
    saved_at: string;
    cash: Decimal;
    positions: CurrentPosition[];
  } | null;
}
export interface CurrentHoldingsInput {
  expected_version: string;
  cash: Decimal;
  positions: CurrentPosition[];
}
export function validateCurrentHoldings(
  value: CurrentHoldings,
  accountId: string,
): CurrentHoldings {
  const s = value?.snapshot;
  if (
    !value ||
    value.account_id !== accountId ||
    (s === null
      ? value.audit_id !== ""
      : !s ||
        !/^[1-9]\d*$/.test(value.audit_id) ||
        !/^[1-9]\d*$/.test(s.version) ||
        !Number.isFinite(Date.parse(s.saved_at)) ||
        typeof s.cash !== "string" ||
        !decimal(s.cash, 2) ||
        !Array.isArray(s.positions) ||
        s.positions.length > 200 ||
        s.positions.some(
          (p, n) =>
            !/^[A-Za-z0-9_-]{1,128}$/.test(p.instrument_id) ||
            (n > 0 && p.instrument_id <= s.positions[n - 1]!.instrument_id) ||
            typeof p.quantity !== "string" ||
            !decimal(p.quantity, 6) ||
            !/[1-9]/.test(p.quantity),
        ))
  )
    throw new LedgerError("invalid_response");
  return value;
}
