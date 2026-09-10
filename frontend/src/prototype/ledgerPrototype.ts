export const DEMO_TODAY = "2026-09-10";
export type RecordKind = "in" | "out" | "asset" | "note";
export const kindLabels: Record<RecordKind, string> = {
  in: "转入",
  out: "转出",
  asset: "总资产",
  note: "备注",
};
export interface DemoRecord {
  id: number;
  date: string;
  kind: RecordKind;
  amount: number | null;
  assets: number | null;
  note: string;
  voided: boolean;
  history: string[];
}
export interface Security {
  id: number;
  name: string;
  market: string;
  code: string;
  currency: string;
  price: number;
}
export interface Holding {
  security: number;
  quantity: number;
}
export interface DemoAccount {
  id: number;
  name: string;
  currency: string;
  records: DemoRecord[];
  cash: number;
  holdings: Holding[];
}
export const demoSecurities = (): Security[] => [
  {
    id: 1,
    name: "宽基组合（示例）",
    market: "沪深",
    code: "DEMO01",
    currency: "CNY",
    price: 1250,
  },
  {
    id: 2,
    name: "债券组合（示例）",
    market: "沪深",
    code: "DEMO02",
    currency: "CNY",
    price: 10200,
  },
];
export function createRecord(
  id: number,
  date: string,
  kind: RecordKind,
  amount: number | null,
  assets: number | null,
  note: string,
): DemoRecord {
  return {
    id,
    date,
    kind,
    amount,
    assets,
    note,
    voided: false,
    history: ["创建了示例记录"],
  };
}
export function createAccounts(): DemoAccount[] {
  const rows: [string, RecordKind, number | null, number | null, string][] = [
    ["2025-09-10", "in", 10000000, 10000000, "给长期计划一个起点"],
    ["2025-10-10", "asset", null, 10180000, ""],
    ["2025-11-10", "in", 2000000, 12360000, "增加长期投入"],
    ["2025-12-31", "asset", null, 12680000, "年末记录"],
    ["2026-01-01", "asset", null, 12680000, "新年起点"],
    ["2026-02-10", "asset", null, 12440000, "市场回落，保持计划"],
    ["2026-03-10", "in", 1500000, 14320000, "春季追加"],
    ["2026-04-10", "asset", null, 14760000, ""],
    ["2026-05-10", "out", 800000, 14190000, "留一笔旅行预算"],
    ["2026-06-10", "asset", null, 14820000, "半年回顾"],
    ["2026-07-10", "in", 1000000, 16150000, "继续定投"],
    ["2026-07-24", "note", null, null, "减少查看频率，每月记录一次"],
    ["2026-08-10", "asset", null, 16520000, ""],
    ["2026-08-25", "asset", null, 16390000, ""],
    ["2026-09-10", "asset", null, 16984000, "九月例行更新"],
  ];
  return [
    {
      id: 1,
      name: "长期投资（示例）",
      currency: "CNY",
      records: rows.map((r, i) => createRecord(i + 1, ...r)),
      cash: 6984000,
      holdings: [{ security: 1, quantity: 8000 }],
    },
    {
      id: 2,
      name: "稳健储备（示例）",
      currency: "CNY",
      cash: 500000,
      holdings: [],
      records: [
        createRecord(30, "2026-01-01", "in", 5000000, 5000000, "年度储备"),
        createRecord(31, "2026-04-01", "asset", null, 4900000, "阶段回落"),
        createRecord(32, "2026-07-01", "out", 500000, 4350000, "家庭备用"),
        createRecord(33, DEMO_TODAY, "asset", null, 4420000, "仍在恢复中"),
      ],
    },
    {
      id: 3,
      name: "待补全资产（示例）",
      currency: "CNY",
      cash: 0,
      holdings: [],
      records: [
        createRecord(40, "2026-01-01", "asset", null, 3000000, "期初资产"),
        createRecord(
          41,
          "2026-03-01",
          "in",
          1000000,
          null,
          "转入后尚未记录总资产",
        ),
        createRecord(42, DEMO_TODAY, "asset", null, 4250000, "最新资产"),
      ],
    },
    {
      id: 4,
      name: "新计划（空账户）",
      currency: "CNY",
      records: [],
      cash: 0,
      holdings: [],
    },
  ];
}
export function importExample(): DemoRecord[] {
  return [
    createRecord(1, "2026-01-01", "in", 2000000, 2000000, "示例期初投入"),
    createRecord(2, "2026-06-01", "in", 500000, 2560000, "示例追加投入"),
    createRecord(3, DEMO_TODAY, "asset", null, 2680000, "示例资产更新"),
  ];
}
export const ascending = (records: DemoRecord[]) =>
  [...records].sort((a, b) => a.date.localeCompare(b.date) || a.id - b.id);
export const activeRecords = (records: DemoRecord[]) =>
  ascending(records.filter((r) => !r.voided));
export const flow = (r: DemoRecord) =>
  r.kind === "in" ? (r.amount ?? 0) : r.kind === "out" ? -(r.amount ?? 0) : 0;
export const day = (date: string) => Date.parse(`${date}T00:00:00Z`) / 86400000;
export function cents(input: string): number | null {
  if (!/^\d+(?:\.\d{1,2})?$/.test(input)) return null;
  const [whole, fraction = ""] = input.split(".");
  const value = Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
  return Number.isSafeInteger(value) && value <= 1e12 ? value : null;
}
export const money = (value: number | null) =>
  value === null
    ? "—"
    : (value / 100).toLocaleString("zh-CN", {
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
      });
export const percent = (value: number | null) =>
  value === null || !Number.isFinite(value)
    ? "—"
    : `${value > 0 ? "+" : ""}${(value * 100).toFixed(2)}%`;

// All assets are post-flow. The last asset entry on a day represents that day's close.
// Never manufacture observations on days where only a flow or note was recorded.
export function analyze(
  records: DemoRecord[],
  from: string,
  to: string,
  view: "personal" | "manager",
) {
  const active = activeRecords(records);
  const assetsByDate = new Map<string, DemoRecord>();
  active.forEach((r) => {
    if (r.assets !== null && r.date >= from && r.date <= to)
      assetsByDate.set(r.date, r);
  });
  const observations = [...assetsByDate.values()];
  const first = observations[0];
  const last = observations.at(-1);
  const missingBoundary = active.some(
    (r) =>
      flow(r) &&
      first &&
      last &&
      r.date > first.date &&
      r.date <= last.date &&
      !assetsByDate.has(r.date),
  );
  let product = 1;
  const points = observations.map((r, index) => {
    const days = day(r.date) - day(first!.date);
    const flows = active.filter(
      (f) => f.date > first!.date && f.date <= r.date && flow(f),
    );
    const profit =
      r.assets! - first!.assets! - flows.reduce((sum, f) => sum + flow(f), 0);
    const denominator =
      first!.assets! +
      flows.reduce(
        (sum, f) =>
          sum + flow(f) * (days ? (day(r.date) - day(f.date)) / days : 0),
        0,
      );
    let rate: number | null = denominator > 0 ? profit / denominator : null;
    if (view === "manager") {
      if (index > 0 && Number.isFinite(product)) {
        const prev = observations[index - 1]!;
        const segmentFlows = active.filter(
          (f) => f.date > prev.date && f.date <= r.date && flow(f),
        );
        const factor =
          (r.assets! - segmentFlows.reduce((sum, f) => sum + flow(f), 0)) /
          prev.assets!;
        if (
          prev.assets! <= 0 ||
          segmentFlows.some((f) => f.date !== r.date) ||
          factor < 0
        ) {
          product = NaN;
        } else {
          product *= factor;
        }
      }
      rate = Number.isFinite(product) ? product - 1 : null;
    }
    return { date: r.date, profit, rate };
  });
  const days = first && last ? day(last.date) - day(first.date) : 0;
  const end = points.at(-1);
  const enough = days > 0;
  const rate = enough ? (end?.rate ?? null) : null;
  const annual =
    rate !== null && rate > -1 ? Math.pow(1 + rate, 365 / days) - 1 : null;
  const trailingFlow = active.some(
    (r) => last && r.date > last.date && r.date <= to && flow(r),
  );
  return {
    points,
    first,
    last,
    days,
    missingBoundary,
    trailingFlow,
    profit: enough ? end!.profit : null,
    rate,
    annual: annual !== null && Number.isFinite(annual) ? annual : null,
    events: active.filter(
      (r) =>
        flow(r) && first && last && r.date >= first.date && r.date <= last.date,
    ),
  };
}
