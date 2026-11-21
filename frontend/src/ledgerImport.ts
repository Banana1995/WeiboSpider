import { LedgerError, newID, request, type Currency } from "./ledger";

export interface ImportMetadata {
  name: string;
  goal: string;
  expected_return: string;
  expected_return_raw?: string;
  investment_horizon: string;
  currency: Currency;
  money_bucket: string;
}
export interface ImportedRow {
  source_row: number;
  kind: "asset" | "cash_flow";
  date: string;
  flow: string | null;
  total_assets: string | null;
  note: string;
  source_created_at: string;
  detail: string;
  date_raw: string;
  created_raw: string;
}
export interface ImportSummary {
  row_count: number;
  asset_count: number;
  flow_count: number;
  from: string;
  to: string;
  total_in: string;
  total_out: string;
  latest_assets: string | null;
  latest_asset_date: string | null;
}
export interface ImportPreview {
  digest: string;
  metadata: ImportMetadata;
  rows: ImportedRow[];
  summary: ImportSummary;
  warnings: string[];
  channels?: {
    names: string[];
    snapshot_count: number;
    unresolved_rows: number[];
  };
}
export interface ImportedSummary {
  batch_id: string;
  metadata: ImportMetadata;
  summary: ImportSummary;
  imported_at: string;
}
export interface ImportResult {
  account_id: string;
  batch_id: string;
  imported_count: number;
  skipped_count?: number;
  duplicate: boolean;
  added_source_rows?: number[];
}

type DiffRows = { added: ImportedRow[]; skipped: ImportedRow[] };
export interface ImportReport {
  accountID: string;
  accountName: string;
  currency: Currency;
  added: number;
  skipped: number;
  rows: DiffRows | null;
}

function importCounts(result: ImportResult) {
  // Older, frozen duplicate receipts reported the original batch size.
  if (result.duplicate && result.skipped_count === undefined)
    return { added: 0, skipped: result.imported_count };
  return { added: result.imported_count, skipped: result.skipped_count ?? 0 };
}

export function importResultText({ added, skipped }: ImportReport) {
  return `本次新增 ${added} 笔，跳过重复 ${skipped} 笔。`;
}

// The two files with the same business row can have different row numbers.
// Coordinates here refer only to this request's digest-checked preview. If an
// older server only supplies mixed counts, retain success without guessing rows.
function diffRows(
  rows: ImportedRow[],
  added: number,
  coordinates?: number[],
): DiffRows | null {
  if (coordinates === undefined) {
    if (added === 0) return { added: [], skipped: rows };
    if (added === rows.length) return { added: rows, skipped: [] };
    return null;
  }
  if (!Array.isArray(coordinates) || coordinates.length !== added) return null;
  const ids = new Set(coordinates);
  const groups: DiffRows = { added: [], skipped: [] };
  for (const row of rows)
    (ids.has(row.source_row) ? groups.added : groups.skipped).push(row);
  return groups.added.length === added ? groups : null;
}

// File bytes and all semantic fields remain fixed even if a receipt is lost.
export class PendingImport {
  readonly key = newID();
  uncertain = false;
  private running = false;
  constructor(
    readonly file: File,
    readonly preview: ImportPreview,
    readonly accountID: string,
    readonly create: boolean,
    readonly accountName: string,
  ) {}
  async run(): Promise<ImportReport> {
    if (this.running) throw new Error("正在确认导入");
    this.running = true;
    const body = new FormData();
    body.append("file", this.file);
    body.append("preview_digest", this.preview.digest);
    body.append("create_account", String(this.create));
    try {
      const result = await request<ImportResult>(
        `/accounts/${this.accountID}/imports/youzhiyouxing`,
        { method: "POST", headers: { "Idempotency-Key": this.key }, body },
      );
      if (
        !result ||
        result.account_id !== this.accountID ||
        typeof result.batch_id !== "string" ||
        !/^[A-Za-z0-9_-]{1,128}$/.test(result.batch_id) ||
        !Number.isInteger(result.imported_count) ||
        result.imported_count < 0 ||
        (result.skipped_count !== undefined &&
          (!Number.isInteger(result.skipped_count) ||
            result.skipped_count < 0)) ||
        typeof result.duplicate !== "boolean"
      )
        throw new LedgerError("invalid_response");
      const { added, skipped } = importCounts(result);
      if (
        added + skipped !== this.preview.rows.length ||
        result.duplicate !== (added === 0)
      )
        throw new LedgerError("invalid_response");
      return {
        accountID: this.accountID,
        accountName: this.accountName,
        currency: this.preview.metadata.currency,
        added,
        skipped,
        rows: diffRows(this.preview.rows, added, result.added_source_rows),
      };
    } catch (e) {
      if (!(e instanceof LedgerError) || e.uncertain) this.uncertain = true;
      throw e;
    } finally {
      this.running = false;
    }
  }
}
