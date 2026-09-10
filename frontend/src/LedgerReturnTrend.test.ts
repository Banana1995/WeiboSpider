// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { mount } from "@vue/test-utils";
import LedgerReturnTrend from "./LedgerReturnTrend.vue";
import { trendLines } from "./ledgerReturnTrend";
import {
  validReturns,
  type LedgerReturns,
  type ReturnMetric,
  type ReturnPoint,
} from "./ledgerReturns";
import type { BasisPoint } from "./ledgerChart";

const chart = vi.hoisted(() => ({
  setOption: vi.fn(),
  resize: vi.fn(),
  dispose: vi.fn(),
}));
vi.mock("echarts/core", () => ({ init: () => chart, use: vi.fn() }));
const disconnect = vi.fn();
let resize: () => void;
const metric = (
  status: ReturnMetric["status"] = "available",
): ReturnMetric => ({
  value: status === "unavailable" ? null : "0.170000000000",
  percentage: status === "unavailable" ? null : "17.00",
  status,
  reason: status === "unavailable" ? "missing_flow_boundary" : "",
});
function point(
  i: number,
  status: ReturnMetric["status"] = "available",
): ReturnPoint {
  return {
    date: new Date(Date.UTC(2021, 0, 1 + i * 7)).toISOString().slice(0, 10),
    record_id: `synthetic-${i}`,
    baseline: i === 0,
    profit: {
      ...metric(status),
      value: status === "unavailable" ? null : "90071992547409.03",
      percentage: null,
    },
    modified_dietz: metric(status),
    twr: metric(status),
  };
}
function result(curve: ReturnPoint[]): LedgerReturns {
  const last = curve.at(-1)!;
  const endpoint = (p: ReturnPoint): BasisPoint => ({
    date: p.date,
    record_id: p.record_id,
    sequence: "1",
    version: "1",
    assets: "100.00",
    flow: null,
    selected: true,
    status: "reported",
    source_id: p.record_id,
    source_version: "1",
    source_date: p.date,
  });
  return {
    revision: "synthetic",
    requested_from: "",
    requested_to: "",
    start_mode: "baseline",
    effective_from: curve[0]!.date,
    effective_to: last.date,
    days: (curve.length - 1) * 7,
    period_days: (curve.length - 1) * 7 + 1,
    opening: endpoint(curve[0]!),
    closing: endpoint(last),
    net_flow: "0.00",
    denominator: null,
    profit: last.profit,
    modified_dietz: last.modified_dietz,
    twr: last.twr,
    twr_annualized: metric(),
    xirr: metric(),
    curve,
    flows: [],
    investor_flows: [],
    warnings: [],
  };
}
function setup(curve: ReturnPoint[], points: BasisPoint[] = []) {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(cb: () => void) {
        resize = cb;
      }
      observe() {}
      disconnect = disconnect;
    },
  );
  return mount(LedgerReturnTrend, {
    props: { result: result(curve), points, currency: "CNY", manager: false },
  });
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

it("connects only known adjacent samples, with a real unavailable break and reference edges", () => {
  const points = [
    point(0),
    point(1),
    point(2, "reference"),
    point(3, "unavailable"),
    point(4),
    point(5),
  ];
  const lines = trendLines(points, "twr");
  expect(lines.available).toHaveLength(6);
  expect(lines.reference).toHaveLength(3);
  expect(lines.available[2]![1]).toBeNull();
  expect(lines.reference[2]![1]).toBeNull();
  expect(points).toHaveLength(6);
});

it("uses safe Canvas exact text, same-snapshot red/green events, local selectors and cleanup", async () => {
  const malicious = "<img src=x onerror=alert(1)>{x|unsafe}";
  const p: BasisPoint = {
    date: "2021-01-01",
    record_id: malicious,
    version: "1",
    sequence: "1",
    flow: "123.45",
    assets: null,
    selected: false,
    status: "carried",
    source_id: "",
    source_date: "",
    source_version: "",
  };
  const points = [point(0), point(1, "reference"), point(2, "unavailable")];
  points[0]!.record_id = malicious;
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const w = setup(points, [p, { ...p, flow: "-67.89" }]);
  const data = w.get('[data-test="return-trend-data"]');
  expect(data.get("summary").text()).toBe("查看收益趋势数据");
  expect((data.element as HTMLDetailsElement).open).toBe(false);
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.tooltip.renderMode).toBe("richText");
  expect(option.series[0].lineStyle.type).toBe("solid");
  expect(option.series[2].lineStyle.type).toBe("dashed");
  expect(option.series[3].symbol).toBe("emptyCircle");
  expect(option.series[4].itemStyle.color).toBe("#bc514c");
  expect(option.series[5].itemStyle.color).toBe("#24745b");
  expect(
    option.tooltip.formatter({ data: option.series[4].data[0] }),
  ).toContain("123.45");
  expect(
    option.tooltip.formatter({ data: option.series[1].data[0] }),
  ).not.toContain(malicious);
  (data.element as HTMLDetailsElement).open = true;
  await data.trigger("toggle");
  expect(data.text()).toContain(malicious);
  expect(w.find("img").exists()).toBe(false);
  expect(data.text()).toContain("90071992547409.03");
  expect(data.text()).toContain("外部资金流边界缺少总资产");
  await w.get('select[name="return_trend_metric"]').setValue("profit");
  expect(chart.setOption.mock.calls.at(-1)![0].yAxis[0].name).toContain(
    "收益坐标",
  );
  expect(fetch).not.toHaveBeenCalled();
  resize();
  expect(chart.resize).toHaveBeenCalledOnce();
  w.unmount();
  expect(disconnect).toHaveBeenCalledOnce();
  expect(chart.dispose).toHaveBeenCalledOnce();
});

it("paginates 30 exact rows, resets on new snapshot and retains a fallback on chart error", async () => {
  const w = setup(Array.from({ length: 61 }, (_, i) => point(i)));
  const data = w.get('[data-test="return-trend-data"]');
  (data.element as HTMLDetailsElement).open = true;
  await data.trigger("toggle");
  expect(w.findAll("tbody tr")).toHaveLength(30);
  const pagination = w.get('[data-test="return-trend-pagination"]');
  expect(pagination.text()).toContain("第 1 / 3 页");
  await pagination
    .findAll("button")
    .find((b) => b.text() === "下一页")!
    .trigger("click");
  expect(w.text()).toContain("synthetic-30");
  expect(w.text()).not.toContain("synthetic-29");
  chart.setOption.mockImplementationOnce(() => {
    throw new Error("synthetic");
  });
  await w.setProps({ result: result([point(0)]) });
  expect(w.findAll("tbody tr")).toHaveLength(1);
  expect(w.find('[data-test="return-trend-pagination"]').exists()).toBe(false);
  expect(w.text()).toContain("图形暂不可用");
  w.unmount();
});

it("strictly rejects invalid curves, dates, metrics and mismatched endpoints", () => {
  const r = result([point(0), point(1)]);
  expect(validReturns(r, "synthetic", "", "")).toBe(true);
  for (const curve of [
    [point(0), point(0)],
    [point(0), { ...point(1), date: "2021-02-30" }],
    [point(0), { ...point(1), twr: metric("unavailable") }],
    [point(0), { ...point(1), baseline: true }],
  ]) {
    expect(validReturns({ ...r, curve }, "synthetic", "", "")).toBe(false);
  }
});
