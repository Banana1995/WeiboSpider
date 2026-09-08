import { expect, it } from "vitest";
import {
  assetDays,
  assetLine,
  flowLabel,
  type BasisPoint,
} from "./ledgerChart";

export function point(overrides: Partial<BasisPoint> = {}): BasisPoint {
  return {
    date: "2020-01-01",
    record_id: "manual-a",
    sequence: "1",
    version: "1",
    assets: "100.00",
    flow: null,
    status: "reported",
    selected: true,
    source_date: "2020-01-01",
    source_id: "manual-a",
    source_version: "1",
    ...overrides,
  };
}
it("preserves null and zero without converting flow into an asset or using a future value", () => {
  const days = assetDays([
    point({ assets: null, status: "unavailable", flow: "50.00" }),
    point({ date: "2020-01-02", assets: "0.00" }),
    point({
      date: "2020-01-03",
      assets: "0.00",
      status: "carried",
      flow: "500.00",
    }),
  ]);
  expect(assetLine(days, "reported").map((d) => d.value[1])).toEqual([
    null,
    0,
    null,
  ]);
  expect(assetLine(days, "carried").map((d) => d.value[1])).toEqual([
    null,
    null,
    0,
  ]);
  expect(days[2]!.chosen!.flow).toBe("500.00");
});
it("uses server-selected stable daily sequence while retaining every same-day record", () => {
  const points = [
    point({ selected: false, assets: "999.00", version: "7" }),
    point({ sequence: "2", status: "carried" }),
    point({ sequence: "3", status: "log", assets: null, selected: false }),
  ];
  const days = assetDays(points);
  expect(days[0]!.chosen).toBe(points[1]);
  expect(days[0]!.points).toEqual(points);
  expect(assetLine(days, "reported")[0]!.value[1]).toBeNull();
});
it("inserts only one gap sentinel even over two millennia and preserves exact huge decimals", () => {
  const days = assetDays([
    point({ date: "0001-01-01" }),
    point({ date: "2026-09-01", assets: "92233720368547758.07" }),
  ]);
  const series = assetLine(days, "reported");
  expect(series).toHaveLength(3);
  expect(series[1]!.value[1]).toBeNull();
  expect(series[2]!.exact).toBe("92233720368547758.07");
  expect(series[2]!.value[1]).toBeTypeOf("number");
});
it("keeps stale, untracked and carried tracks distinct and caps instead of truncating", () => {
  const days = assetDays([
    point({ status: "stale" }),
    point({ date: "2020-01-02", status: "untracked" }),
  ]);
  expect(assetLine(days, "observed").every((p) => p.exact === null)).toBe(true);
  expect(assetLine(days, "stale")[0]!.exact).toBe("100.00");
  expect(assetDays([])).toEqual([]);
  expect(() => assetDays(Array(10001).fill(point()))).toThrow("10,000");
  expect([null, "0.00", "-1.00", "2.00"].map(flowLabel)).toEqual([
    "无资金事件",
    "零额记录",
    "转出",
    "转入",
  ]);
});
