import type { ReturnPoint } from "./ledgerReturns";

export type TrendMetric = "profit" | "modified_dietz" | "twr";

// Connect known samples only. Two bounded series, not one series per segment.
// Reference at either endpoint makes that connection reference; null metrics
// remove both adjacent edges. Conversion to Number is only for coordinates.
export function trendLines(points: ReturnPoint[], key: TrendMetric) {
  const lines: Record<"available" | "reference", (number | null)[][]> = {
    available: [],
    reference: [],
  };
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1]!,
      b = points[i]!;
    if (a[key].value === null || b[key].value === null) continue;
    const status =
      a[key].status === "reference" || b[key].status === "reference"
        ? "reference"
        : "available";
    lines[status].push(
      [Date.parse(`${a.date}T00:00:00Z`), Number(a[key].value)],
      [Date.parse(`${b.date}T00:00:00Z`), Number(b[key].value)],
      [Date.parse(`${b.date}T00:00:00Z`), null],
    );
  }
  return lines;
}
