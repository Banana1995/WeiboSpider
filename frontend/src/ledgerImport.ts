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
  duplicate: boolean;
}

// File bytes and all semantic fields remain fixed even if a receipt is lost.
export class PendingImport {
  readonly key = newID();
  uncertain = false;
  private running = false;
  constructor(
    readonly file: File,
    readonly digest: string,
    readonly accountID: string,
    readonly create: boolean,
  ) {}
  async run() {
    if (this.running) throw new Error("正在确认导入");
    this.running = true;
    const body = new FormData();
    body.append("file", this.file);
    body.append("preview_digest", this.digest);
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
        result.imported_count > 10000 ||
        typeof result.duplicate !== "boolean"
      )
        throw new LedgerError("invalid_response");
      return result;
    } catch (e) {
      if (!(e instanceof LedgerError) || e.uncertain) this.uncertain = true;
      throw e;
    } finally {
      this.running = false;
    }
  }
}
