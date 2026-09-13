// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { mount } from "@vue/test-utils";
import LedgerReturnChart from "./LedgerReturnChart.vue";
import type { Benchmark } from "./ledgerBenchmark";

const chart = vi.hoisted(() => ({
  setOption: vi.fn(),
  on: vi.fn(),
  resize: vi.fn(),
  dispose: vi.fn(),
}));
const init = vi.hoisted(() => vi.fn((...args: unknown[]) => chart));
vi.mock("echarts/core", () => ({ init, use: vi.fn() }));
const disconnect = vi.fn();
let resize: () => void;

const samples = [
  { date: "2026-01-01", value: 0, text: "0.00%" },
  { date: "2026-01-02", value: 0.05, text: "5.00%" },
  { date: "2026-01-05", value: 0.1, text: "10.00%" },
];
function makeBenchmark(
  code: string,
  name: string,
  currency: string,
  source: string,
  items: Benchmark["items"],
): Benchmark {
  return {
    code,
    name,
    currency,
    source,
    from: items[0]!.date,
    to: items[items.length - 1]!.date,
    items,
  };
}
const hs300 = makeBenchmark("H00300", "沪深300全收益", "CNY", "中证指数", [
  { date: "2026-01-02", close: "100", return: "0.00000000" },
  { date: "2026-01-05", close: "120", return: "0.20000000" },
]);
const dividend = makeBenchmark("H00922", "中证红利全收益", "CNY", "中证指数", [
  { date: "2026-01-02", close: "200", return: "0.00000000" },
  { date: "2026-01-05", close: "210", return: "0.05000000" },
]);
const sp500 = makeBenchmark("usINX", "标普500", "USD", "腾讯", [
  { date: "2026-01-02", close: "7000", return: "0.00000000" },
  { date: "2026-01-05", close: "7140", return: "0.02000000" },
]);

function setup(props: Record<string, unknown> = {}) {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        resize = callback;
      }
      observe() {}
      disconnect = disconnect;
    },
  );
  return mount(LedgerReturnChart, {
    props: {
      mode: "rate",
      samples,
      flows: [],
      benchmarks: [],
      ...props,
    },
  });
}
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

type Point = { value: [number, number]; text?: string };

it("renders a crisp thin solid account line at devicePixelRatio and cleans up", () => {
  const w = setup();
  expect(init).toHaveBeenCalledOnce();
  expect(init.mock.calls[0]![2]).toEqual({
    devicePixelRatio: window.devicePixelRatio || 1,
  });
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.useUTC).toBe(true);
  expect(option.animation).toBe(false);
  expect(option.series[0]).toMatchObject({
    name: "账户",
    type: "line",
    showSymbol: false,
    symbol: "none",
    smooth: false,
    connectNulls: false,
    sampling: "lttb",
    animation: false,
  });
  expect(option.series[0].lineStyle).toEqual({
    width: 1.8,
    color: "#1f6f5c",
    type: "solid",
  });
  expect((option.series[0].data as Point[]).map((p) => p.value[1])).toEqual([
    0, 0.05, 0.1,
  ]);
  expect(option.yAxis.axisLabel.formatter(0.26)).toBe("26.00%");
  resize();
  expect(chart.resize).toHaveBeenCalledOnce();
  w.unmount();
  expect(chart.dispose).toHaveBeenCalledOnce();
  expect(disconnect).toHaveBeenCalledOnce();
});

it("aligns an optional benchmark with a flat baseline before its first point", () => {
  const w = setup({ benchmarks: [hs300] });
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.series).toHaveLength(2);
  expect(option.series[1].lineStyle).toEqual({
    width: 1.5,
    color: "#2f6fb0",
    type: [6, 3],
  });
  const points = option.series[1].data as Point[];
  expect(points.map((p) => p.value[1])).toEqual([0, 0, 0.2]);
  expect(points[2]!.text).toBe("20.00%");
  const tooltip = option.tooltip.formatter([
    {
      value: option.series[0].data[1].value,
      seriesType: "line",
      seriesName: "账户",
      data: option.series[0].data[1],
    },
    {
      value: option.series[1].data[1].value,
      seriesType: "line",
      seriesName: "沪深300全收益",
      data: option.series[1].data[1],
    },
  ]);
  expect(tooltip).toContain("2026-01-02");
  expect(tooltip).toContain("账户：5.00%");
  expect(tooltip).toContain("沪深300全收益：0.00%");
  // Each series/event sits on its own row instead of one horizontal line.
  expect(tooltip).toBe("2026-01-02<br/>账户：5.00%<br/>沪深300全收益：0.00%");
  w.unmount();
});

it("distinguishes account and every benchmark by dash pattern, not color alone", () => {
  const w = setup({ benchmarks: [hs300, dividend, sp500] });
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.series).toHaveLength(4);
  expect(option.series[0].lineStyle).toEqual({
    width: 1.8,
    color: "#1f6f5c",
    type: "solid",
  });
  expect(option.series[1].lineStyle).toEqual({
    width: 1.5,
    color: "#2f6fb0",
    type: [6, 3],
  });
  expect(option.series[2].lineStyle).toEqual({
    width: 1.5,
    color: "#a8721f",
    type: [1.5, 3],
  });
  expect(option.series[3].lineStyle).toEqual({
    width: 1.5,
    color: "#6f5b8f",
    type: [8, 3, 2, 3],
  });
  const patterns = option.series
    .slice(1)
    .map((series: { lineStyle: { type: number[] } }) =>
      series.lineStyle.type.join(","),
    );
  expect(new Set(patterns).size).toBe(3);

  // Hovering a line focuses just that series and dims the rest.
  for (const series of option.series) {
    expect(series.triggerLineEvent).toBe(true);
    expect(series.emphasis.focus).toBe("series");
    expect(series.blur.lineStyle.opacity).toBe(0.15);
  }

  // The legend/control lives in the parent so it is not duplicated above the plot.
  expect(w.find(".lp-chart-legend").exists()).toBe(false);
  expect(w.get(".lp-return-canvas").attributes("aria-label")).toContain(
    "不同虚线样式",
  );
  w.unmount();
});

it("draws red transfer-in and green transfer-out dots and emits locate", () => {
  const flows = [
    { id: "f-in", date: "2026-01-05", amount: "1,000.00", direction: "in" },
    { id: "f-out", date: "2026-01-05", amount: "500.00", direction: "out" },
  ];
  const w = setup({ mode: "profit", flows });
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.yAxis.axisLabel.formatter(90071992547409.03)).toBe(
    "90,071,992,547,409",
  );
  expect(option.series).toHaveLength(3);
  expect(option.series[1].name).toBe("转入");
  expect(option.series[1].itemStyle).toEqual({ color: "#bc514c" });
  expect(option.series[2].name).toBe("转出");
  expect(option.series[2].itemStyle).toEqual({ color: "#2f7d5b" });
  expect(option.series[1].data[0].value[1]).toBe(0.1);
  const eventTooltip = option.tooltip.formatter([
    {
      value: option.series[1].data[0].value,
      seriesType: "scatter",
      seriesName: "转入",
      data: option.series[1].data[0],
    },
  ]);
  expect(eventTooltip).toBe("2026-01-05<br/>转入 1,000.00");
  const onClick = chart.on.mock.calls[0]![1];
  onClick({ data: option.series[1].data[0] });
  expect(w.emitted("locate")).toEqual([[{ id: "f-in", date: "2026-01-05" }]]);
  w.unmount();
});

it("skips null samples without synthesizing values", () => {
  const w = setup({
    samples: [
      { date: "2026-01-01", value: null, text: "—" },
      { date: "2026-01-02", value: 0.1, text: "10.00%" },
    ],
  });
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.series[0].data).toHaveLength(1);
  expect(option.series[0].data[0].value[1]).toBe(0.1);
  w.unmount();
});
