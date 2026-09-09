// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { mount } from "@vue/test-utils";
import LedgerAssetChart from "./LedgerAssetChart.vue";
import type { BasisPoint } from "./ledgerChart";

const chart = vi.hoisted(() => ({
  setOption: vi.fn(),
  on: vi.fn(),
  resize: vi.fn(),
  dispose: vi.fn(),
}));
vi.mock("echarts/core", () => ({ init: () => chart, use: vi.fn() }));
const disconnect = vi.fn();
let resize: () => void;
function setup(points: BasisPoint[]) {
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
  return mount(LedgerAssetChart, {
    props: {
      points,
      currency: "CNY",
      from: "2020-01-01",
      to: "2020-01-31",
      revision: "synthetic-snapshot",
    },
  });
}
const point = (overrides: Partial<BasisPoint> = {}): BasisPoint => ({
  date: "2020-01-01",
  record_id: "manual-a",
  sequence: "1",
  version: "1",
  assets: "100.00",
  flow: null,
  selected: true,
  status: "reported",
  source_date: "2020-01-01",
  source_id: "manual-a",
  source_version: "1",
  ...overrides,
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

it("clicks any event/point to select ALL same-day details; labels escape and coordinates never supply exact display", async () => {
  const malicious = '<img src=x onerror="alert(1)">{a|unsafe}';
  const p = point({
    assets: "92233720368547758.07",
    record: {
      id: "manual-a",
      account_id: "a",
      date: "2020-01-01",
      kind: "asset",
      flow: null,
      total_assets: "92233720368547758.07",
      note: malicious,
      origin: "manual",
      original: null,
      version: "1",
      voided: false,
      created_at: "",
      updated_at: "",
    },
  });
  const w = setup([
    p,
    point({
      sequence: "2",
      record_id: "manual-flow",
      selected: false,
      status: "carried",
      flow: "50.00",
      assets: p.assets,
    }),
    point({ date: "2020-01-03" }),
  ]);
  const callback = chart.on.mock.calls[0]![1];
  callback({ data: { date: "2020-01-01" } });
  await w.vm.$nextTick();
  const selectedDay = w.get('[aria-label="同日全部记录"]');
  expect(selectedDay.get("h4").text()).toContain("全部 2 条记录");
  const sameDay = w.get('[data-test="same-day-records"]');
  expect((sameDay.element as HTMLDetailsElement).open).toBe(false);
  expect(sameDay.get("summary").text()).toBe("查看数据来源与计算依据");
  expect(
    (w.get('[data-test="asset-date-data"]').element as HTMLDetailsElement).open,
  ).toBe(false);
  (sameDay.element as HTMLDetailsElement).open = true;
  await sameDay.trigger("toggle");
  expect(sameDay.text()).toContain(malicious);
  expect(w.find("img").exists()).toBe(false);
  expect(sameDay.text()).toContain(p.assets);
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.tooltip.renderMode).toBe("richText");
  const tooltip = option.tooltip.formatter({ data: option.series[0].data[0] });
  expect(tooltip).toContain(p.assets);
  expect(tooltip).not.toContain(malicious);
  expect(option.xAxis[0].min).toBe(option.xAxis[1].min);
  expect(option.series[0].connectNulls).toBe(false);
  resize();
  expect(chart.resize).toHaveBeenCalledOnce();
  w.unmount();
  expect(chart.dispose).toHaveBeenCalledOnce();
  expect(disconnect).toHaveBeenCalledOnce();
});
it("offers keyboard dates and paged HTML fallback including every same-day record", async () => {
  const w = setup(
    Array.from({ length: 35 }, (_, i) =>
      point({
        record_id: `manual-${i}`,
        sequence: String(i + 1),
        selected: i === 34,
      }),
    ),
  );
  await w.vm.$nextTick();
  const sameDay = w.get('[data-test="same-day-records"]');
  (sameDay.element as HTMLDetailsElement).open = true;
  await sameDay.trigger("toggle");
  expect(w.findAll("article")).toHaveLength(30);
  const pagination = w.get('[data-test="same-day-pagination"]');
  expect(pagination.text()).toContain("第 1 / 2 页");
  await pagination
    .findAll("button")
    .find((b) => b.text() === "下一页")!
    .trigger("click");
  expect(w.findAll("article")).toHaveLength(5);
  expect(w.text()).toContain("manual-34");
  await w.setProps({
    points: [
      point({
        date: "2020-01-02",
        assets: null,
        status: "unavailable",
        flow: "10.00",
      }),
      point({ date: "2020-01-03", assets: "0.00" }),
    ],
  });
  await w.get('input[name="chart_day"]').setValue("2020-01-02");
  expect(w.get('[aria-label="同日全部记录"]').text()).toContain(
    "资产依据 未记录",
  );
  const option = chart.setOption.mock.calls.at(-1)![0];
  expect(option.series[0].data[0].value[1]).toBeNull();
  expect(option.series[6].data[0].date).toBe("2020-01-02");
  await w.get('input[name="chart_day"]').setValue("2020-01-01");
  expect(w.text()).toContain("2020-01-01 当日无记录");
  expect(w.find("article").exists()).toBe(false);
  await w.setProps({ points: [] });
  expect(w.text()).toContain("所选区间无记录");
  expect(w.find("article").exists()).toBe(false);
  w.unmount();
});
it("keeps the exact date table collapsed and uses plain pagination only when needed", async () => {
  const w = setup(
    Array.from({ length: 35 }, (_, i) =>
      point({
        date: new Date(Date.UTC(2020, 0, 1 + i)).toISOString().slice(0, 10),
        record_id: `manual-${i}`,
        sequence: String(i + 1),
      }),
    ),
  );
  await w.vm.$nextTick();
  const data = w.get('[data-test="asset-date-data"]');
  expect(data.get("summary").text()).toBe("查看资产日期数据");
  expect((data.element as HTMLDetailsElement).open).toBe(false);
  (data.element as HTMLDetailsElement).open = true;
  await data.trigger("toggle");
  expect(data.findAll("tbody tr")).toHaveLength(30);
  const pagination = data.get('[data-test="asset-date-pagination"]');
  expect(pagination.text()).toContain("第 1 / 2 页");
  expect(pagination.text()).not.toContain("上一页日期");
  await pagination
    .findAll("button")
    .find((button) => button.text() === "下一页")!
    .trigger("click");
  expect(data.findAll("tbody tr")).toHaveLength(5);
  expect(pagination.text()).toContain("第 2 / 2 页");
  w.unmount();
});
it("retains accessible detail if ECharts rendering fails", async () => {
  chart.setOption.mockImplementationOnce(() => {
    throw new Error("synthetic renderer failure");
  });
  const w = setup([point()]);
  await w.vm.$nextTick();
  expect(w.text()).toContain("图形暂不可用");
  const data = w.get('[data-test="asset-date-data"]');
  expect((data.element as HTMLDetailsElement).open).toBe(false);
  (data.element as HTMLDetailsElement).open = true;
  await data.trigger("toggle");
  expect(data.text()).toContain("100.00");
  w.unmount();
});
