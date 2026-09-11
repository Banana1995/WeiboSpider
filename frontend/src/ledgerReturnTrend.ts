import type { ReturnPoint } from "./ledgerReturns";

export type TrendMetric = "profit" | "modified_dietz" | "twr";

// Join numeric samples irrespective of provenance. Skipped dates receive no
// synthetic value; conversion to Number is only for plotting coordinates.
export function trendLines(points: ReturnPoint[], key: TrendMetric) {
  return points
    .filter(p => p[key].value !== null)
    .map(p => [Date.parse(`${p.date}T00:00:00Z`), Number(p[key].value)]);
}
