import type { AccountRecord } from "./accountRecords";
import type { LedgerRecord, ValuationSummary } from "./ledger";

export interface BasisPoint {
  date: string;
  sequence: string;
  record_id: string;
  version: string;
  assets: string | null;
  flow: string | null;
  selected: boolean;
  status: string;
  source_id: string;
  source_version: string;
  source_date: string;
  record?: AccountRecord;
  operation?: LedgerRecord;
  valuation?: ValuationSummary;
}
export const pointLabels: Record<string, string> = {
  reported: "明确记录",
  carried: "沿用此前原值，未增加资金流",
  unavailable: "缺少资产依据，不补零",
  observed: "已追踪估值，未发现后续相关修改",
  stale: "旧估值受修改影响，未重算",
  untracked: "旧估值未追踪",
  flow: "外部资金事件",
  operation: "持仓操作（非外部资金流）",
  log: "投资日志",
};
export const pointNote = (p: BasisPoint) =>
  p.record?.note ?? p.operation?.note ?? "";
export function flowLabel(flow: string | null) {
  if (flow === null) return "无资金事件";
  if (/^-?0\.00$/.test(flow)) return "零额记录";
  return flow.startsWith("-") ? "转出" : "转入";
}
export interface AssetDay {
  date: string;
  points: BasisPoint[];
  chosen?: BasisPoint;
}
export function assetDays(points: BasisPoint[]): AssetDay[] {
  if (points.length > 10000)
    throw new Error("最多展示 10,000 条记录，请缩小范围");
  const days = new Map<string, AssetDay>();
  for (const p of points) {
    let day = days.get(p.date);
    if (!day) days.set(p.date, (day = { date: p.date, points: [] }));
    day.points.push(p);
    if (p.selected) day.chosen = p;
  }
  return [...days.values()].sort((a, b) => a.date.localeCompare(b.date));
}

// Only chart coordinates cross the floating-point boundary. Labels never do.
export function assetLine(days: AssetDay[], status: string) {
  const data: {
    value: [number, number | null];
    date: string;
    exact: string | null;
    status: string;
  }[] = [];
  let previous: number | undefined;
  for (const day of days) {
    const time = Date.parse(`${day.date}T00:00:00Z`);
    // One null sentinel per gap, not millions of invented calendar samples.
    if (previous !== undefined && time - previous > 86400000)
      data.push({
        value: [previous + 86400000, null],
        date: "",
        exact: null,
        status,
      });
    const p = day.chosen;
    const exact = p?.status === status ? p.assets : null;
    data.push({
      value: [time, exact === null ? null : Number(exact)],
      date: day.date,
      exact,
      status,
    });
    previous = time;
  }
  return data;
}
