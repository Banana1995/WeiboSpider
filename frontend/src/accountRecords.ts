import type { ImportedRow } from "./ledgerImport";
export type AccountRecordOrigin =
  | "import"
  | "manual"
  | "currentrefresh"
  | "weekly"
  | "weekly_carry"
  | "operation";
export const accountRecordOriginLabels: Record<AccountRecordOrigin, string> = {
  import: "Excel 导入",
  manual: "手工记录",
  currentrefresh: "自动估值",
  weekly: "周六估值",
  weekly_carry: "每周沿用",
  operation: "持仓操作",
};
export interface AccountEntry {
  kind: "asset" | "cash_flow" | "log";
  date: string;
  flow: string | null;
  total_assets: string | null;
  note: string;
}
export interface AccountRecordSource {
  id: string;
  account_id: string;
  sequence: string;
  version: string;
  date: string;
  origin: AccountRecordOrigin;
  total_assets: string;
  manual_assertion?: boolean;
}
export interface AccountRecord extends AccountEntry {
  id: string;
  sequence?: string;
  account_id: string;
  origin: AccountRecordOrigin;
  operation_id?: string;
  quote_audit_id?: string;
  manual_assertion?: boolean;
  carried_from?: AccountRecordSource;
  original: ImportedRow | null;
  version: string;
  voided: boolean;
  created_at: string;
  updated_at: string;
}
export interface AccountRecordRevision {
  record: AccountRecord;
  reason: string;
}
export interface EffectiveSummary {
  row_count: number;
  asset_count: number;
  flow_count: number;
  log_count: number;
  voided_count: number;
  from: string | null;
  to: string | null;
  total_in: string;
  total_out: string;
  latest_assets: string | null;
  latest_asset_date: string | null;
  latest_asset_count: number;
}
