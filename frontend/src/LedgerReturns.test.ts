// @vitest-environment jsdom
import { expect, it, vi } from "vitest";
import { mount } from "@vue/test-utils";
import LedgerReturns from "./LedgerReturns.vue";
import {
  returnPercent,
  validReturns,
  type LedgerReturns as Returns,
} from "./ledgerReturns";

vi.mock("./LedgerReturnTrend.vue", () => ({
  __esModule: true,
  default: { template: "<div />" },
}));

function fixture(): Returns {
  const metric = {
    value: "0.100000000000",
    percentage: "10.00",
    status: "available" as const,
    reason: "",
  };
  const opening = {
    date: "2021-01-01",
    record_id: "synthetic-opening",
    sequence: "1",
    version: "1",
    selected: true,
    status: "reported",
    assets: "100.00",
    flow: null,
    source_id: "synthetic-opening",
    source_version: "1",
    source_date: "2021-01-01",
  };
  const closing = {
    ...opening,
    date: "2022-01-01",
    record_id: "synthetic-closing",
    sequence: "2",
    assets: "110.00",
    source_id: "synthetic-closing",
    source_date: "2022-01-01",
  };
  return {
    revision: "synthetic",
    requested_from: "",
    requested_to: "",
    start_mode: "baseline",
    effective_from: "2021-01-01",
    effective_to: "2022-01-01",
    days: 365,
    period_days: 366,
    opening,
    closing,
    net_flow: "0.00",
    denominator: "100",
    profit: { ...metric, value: "10.00", percentage: null },
    modified_dietz: { ...metric },
    xirr: { ...metric },
    twr: { ...metric },
    twr_annualized: { ...metric },
    curve: [opening, closing].map((p, i) => ({
      date: p.date,
      record_id: p.record_id,
      baseline: i === 0,
      profit: {
        ...metric,
        value: i === 0 ? "0.00" : "10.00",
        percentage: null,
      },
      modified_dietz:
        i === 0
          ? { ...metric, value: "0.000000000000", percentage: "0.00" }
          : { ...metric },
      twr:
        i === 0
          ? { ...metric, value: "0.000000000000", percentage: "0.00" }
          : { ...metric },
    })),
    warnings: [],
    flows: [],
    investor_flows: [
      { date: "2021-01-01", amount: "-100.00" },
      { date: "2022-01-01", amount: "110.00" },
    ],
  };
}

it.each([
  [null, "不可用"],
  ["0.000000000000", "0.00%"],
  ["-0.000000000001", "0.00%"],
  ["0.1", "10.00%"],
  ["-0.1", "-10.00%"],
  ["0.00005", "0.01%"],
  ["-0.00005", "-0.01%"],
  ["90071992547409.010000000000", "9007199254740901.00%"],
])("formats exact percentages without Number: %s", (raw, expected) => {
  expect(returnPercent(raw)).toBe(expected);
});

it("uses the exact-calculation percentage instead of double rounding the rate", () => {
  expect(returnPercent("0.000050000000", "0.00")).toBe("0.00%");
});

it("accepts and explains uncertified XIRR precision without displaying zero", () => {
  const result = fixture();
  result.xirr = {
    value: null,
    percentage: null,
    status: "unavailable",
    reason: "precision_unresolved",
  };
  expect(validReturns(result, "synthetic", "", "")).toBe(true);
  const w = mount(LedgerReturns, {
    props: { result, currency: "CNY", points: [] },
  });
  const card = w.findAll(".returns-cards > div")[2]!;
  expect(card.text()).toContain("数值不确定性跨越舍入边界");
  expect(card.text()).toContain("不可用");
  expect(card.text()).not.toContain("0.00%");
  w.unmount();
});

it("shows all three metrics, investor signs, endpoint conventions and no extra request", async () => {
  const result = fixture();
  const w = mount(LedgerReturns, {
    props: { result, currency: "CNY", points: [] },
  });
  expect(w.get(".returns-cards").text()).toContain("10.00 CNY");
  expect(
    w
      .get(".returns-cards")
      .text()
      .match(/10.00%/g),
  ).toHaveLength(2);
  const summary = w.get('[data-test="return-summary"]');
  expect(summary.text()).toContain("365 自然日");
  expect(summary.text()).toContain("以首个明确日终资产为基准");
  expect(summary.text()).not.toContain("synthetic");
  const calculation = w.get('[data-test="return-calculation"]');
  expect((calculation.element as HTMLDetailsElement).open).toBe(false);
  (calculation.element as HTMLDetailsElement).open = true;
  await calculation.trigger("toggle");
  expect(calculation.text()).toContain("期末日权重为零");
  expect(calculation.text()).toContain("不是 Dietz 复利年化");
  expect(calculation.text()).toContain("-100.00 CNY");
  expect(calculation.text()).toContain("快照版本 synthetic");
  w.unmount();
});

it("distinguishes unavailable from zero, loss, carry, stale and possible multiple roots", async () => {
  const result = fixture();
  result.profit = {
    value: "-20.00",
    percentage: null,
    status: "reference",
    reason: "",
  };
  result.modified_dietz = {
    value: "0.000000000000",
    percentage: "0.00",
    status: "reference",
    reason: "",
  };
  result.xirr = {
    value: null,
    percentage: null,
    status: "unavailable",
    reason: "possible_multiple_roots",
  };
  result.warnings = [
    "carried_assets_unchanged",
    "short_period_extrapolation",
    "sampled_valuation_not_daily_close",
  ];
  const w = mount(LedgerReturns, {
    props: { result, currency: "HKD", points: [] },
  });
  expect(w.text()).toContain("-20.00 HKD");
  expect(w.text()).toContain("0.00%");
  expect(w.text()).toContain("不选择任意根");
  expect(w.text()).toContain("最近明确总资产加后续净转入推算");
  expect(w.text()).toContain("短区间年化外推风险");
  expect(w.text()).toContain("不保证当天收盘价");
  await w.setProps({
    result: {
      ...result,
      xirr: {
        value: null,
        percentage: null,
        status: "unavailable",
        reason: "stale_endpoint",
      },
    },
  });
  expect(w.text()).toContain("需要核实或修正");
  w.unmount();
});

it("paginates cashflow details and resets pages with the snapshot", async () => {
  const result = fixture();
  result.flows = Array.from({ length: 61 }, (_, i) => ({
    date: "2021-02-01",
    record_id: `manual-${i}`,
    version: "1",
    flow: "92233720368547758.07",
    weight_days: 334,
    period_days: 366,
  }));
  const w = mount(LedgerReturns, {
    props: { result, currency: "USD", points: [] },
  });
  const calculation = w.get('[data-test="return-calculation"]');
  expect((calculation.element as HTMLDetailsElement).open).toBe(false);
  (calculation.element as HTMLDetailsElement).open = true;
  await calculation.trigger("toggle");
  expect(calculation.text()).toContain("manual-29");
  expect(calculation.text()).not.toContain("manual-30");
  const next = w.findAll("button").find((b) => b.text() === "下一页资金流")!;
  await next.trigger("click");
  expect(calculation.text()).toContain("manual-30");
  expect(calculation.text()).toContain("92233720368547758.07 USD");
  await w.setProps({ result: { ...result, revision: "updated" } });
  expect(calculation.text()).toContain("manual-29");
  expect(calculation.text()).not.toContain("manual-30");
  w.unmount();
});

it("switches perspectives locally without requests or persistence", async () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const w = mount(LedgerReturns, {
    props: { result: fixture(), currency: "CNY", points: [] },
  });
  await w.get('select[name="return_view"]').setValue(true);
  expect(w.find(".returns-cards").text()).toContain("TWR 复合年化");
  expect(w.find(".returns-cards").text()).not.toContain("XIRR");
  expect(fetch).not.toHaveBeenCalled();
  w.unmount();
  const fresh = mount(LedgerReturns, {
    props: { result: fixture(), currency: "CNY", points: [] },
  });
  expect(fresh.find(".returns-cards").text()).toContain("XIRR");
  fresh.unmount();
  vi.unstubAllGlobals();
});

it("rejects malformed metrics, identity, input range and unbounded detail lists", () => {
  expect(validReturns(fixture(), "synthetic", "", "")).toBe(true);
  expect(validReturns(fixture(), "other", "", "")).toBe(false);
  expect(validReturns(fixture(), "synthetic", "2021-01-01", "")).toBe(false);
  for (const override of [
    { xirr: { value: "NaN", status: "available", reason: "" } },
    { xirr: { value: "0", status: "unavailable", reason: "no_solution" } },
    { xirr: { value: null, status: "available", reason: "" } },
    { flows: Array(10001).fill(null) },
    { warnings: ["invented"] },
    { twr: undefined },
    { period_days: undefined },
    { period_days: 365 },
    { period_days: 0 },
    { period_days: -1 },
    { period_days: 366.5 },
    { curve: Array(10002).fill(null) },
  ])
    expect(
      validReturns(
        { ...fixture(), ...override } as Returns,
        "synthetic",
        "",
        "",
      ),
    ).toBe(false);
});

it("accepts optional exact TWR estimates beyond int64 and all four warnings without changing original values", () => {
  const result = fixture();
  const original = structuredClone(result);
  expect(validReturns(result, "synthetic", "", "")).toBe(true);
  const estimate = {
    assets: "9223372036854775808.09",
    source_record_id: "synthetic-opening",
    source_date: "2021-01-01",
    net_flow: "-9223372036854775808.09",
  };
  result.curve[1]!.twr_estimate = estimate;
  result.warnings = [
    "carried_assets_unchanged",
    "sampled_valuation_not_daily_close",
    "short_period_extrapolation",
    "twr_estimated_assets",
  ];
  expect(validReturns(result, "synthetic", "", "")).toBe(true);
  for (const assets of ["0.00", "-1.00", `${"9".repeat(100)}.99`]) {
    result.curve[1]!.twr_estimate = {
      ...estimate,
      assets,
      source_date: "2022-01-01",
    };
    expect(validReturns(result, "synthetic", "", "")).toBe(true);
  }
  expect(result.opening).toEqual(original.opening);
  expect(result.closing).toEqual(original.closing);
  for (const key of [
    "profit",
    "modified_dietz",
    "xirr",
    "net_flow",
    "denominator",
  ] as const)
    expect(result[key]).toEqual(original[key]);
  expect(
    validReturns(
      { ...result, warnings: [...result.warnings, "twr_estimated_assets"] },
      "synthetic",
      "",
      "",
    ),
  ).toBe(false);
});

it("rejects present but malformed TWR estimate objects, provenance and bounded money strings", () => {
  const estimate = {
    assets: "120.00",
    source_record_id: "synthetic-opening",
    source_date: "2021-01-01",
    net_flow: "20.00",
  };
  const malformed: unknown[] = [
    null,
    undefined,
    [],
    "estimate",
    {},
    ...[
      "",
      "2021-02-29",
      "2021-02-30",
      "0000-01-01",
      "2022-01-02",
      "2021-1-01",
      "2021-01-01T00:00:00Z",
      null,
      20210101,
    ].map((source_date) => ({ ...estimate, source_date })),
    ...["", "   ", null, 123].map((source_record_id) => ({
      ...estimate,
      source_record_id,
    })),
    ...[
      "",
      "1",
      "1.0",
      "1.001",
      "1e2",
      "+1.00",
      "NaN",
      " 1.00",
      `${"9".repeat(101)}.00`,
      120,
      null,
      undefined,
    ].flatMap((value) => [
      { ...estimate, assets: value },
      { ...estimate, net_flow: value },
    ]),
  ];
  for (const twr_estimate of malformed) {
    const result = fixture();
    result.curve[1] = {
      ...result.curve[1]!,
      twr_estimate,
    } as Returns["curve"][number];
    expect(
      validReturns(result, "synthetic", "", ""),
      JSON.stringify(twr_estimate),
    ).toBe(false);
  }
});

it("scopes the estimated TWR warning to manager view and labels original endpoint amounts", async () => {
  const result = fixture();
  result.warnings = ["carried_assets_unchanged", "twr_estimated_assets"];
  const w = mount(LedgerReturns, {
    props: { result, currency: "CNY", points: [] },
  });
  const warnings = () =>
    w
      .findAll('[role="status"]')
      .map((p) => p.text())
      .join(" ");
  expect(warnings()).not.toContain("TWR");
  expect(warnings()).toContain("最近明确总资产加后续净转入推算");
  await w.get('select[name="return_view"]').setValue(true);
  expect(warnings()).toContain("TWR 仅供参考");
  expect(warnings()).toContain("最后明确总资产加后续净流入");
  expect(warnings()).toContain("端点资产按最近明确总资产加后续净转入推算");
  const details = w.get('[data-test="return-calculation"]');
  expect(details.text()).toContain("期末资产（各收益指标统一口径）：110.00");
  expect(details.text()).not.toContain("沿用原额只能参考");
  w.unmount();
});
