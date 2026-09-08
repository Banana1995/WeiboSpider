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
  return {
    revision: "synthetic",
    requested_from: "",
    requested_to: "",
    start_mode: "baseline",
    effective_from: "2021-01-01",
    effective_to: "2022-01-01",
    days: 365,
    opening: null,
    closing: null,
    net_flow: "0.00",
    denominator: "100",
    profit: { ...metric, value: "10.00", percentage: null },
    modified_dietz: { ...metric },
    xirr: { ...metric },
    twr: { ...metric },
    twr_annualized: { ...metric },
    curve: [],
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

it("shows all three metrics, investor signs, endpoint conventions and no extra request", () => {
  const result = fixture();
  const w = mount(LedgerReturns, {
    props: { result, currency: "CNY", points: [] },
  });
  expect(w.text()).toContain("10.00 CNY");
  expect(w.text().match(/10.00%/g)).toHaveLength(2);
  expect(w.text()).toContain("365 自然日");
  expect(w.text()).toContain("期末日权重为零");
  expect(w.text()).toContain("不是 Dietz 复利年化");
  expect(w.text()).toContain("-100.00 CNY");
  expect(w.text()).toContain("快照版本 synthetic");
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
  expect(w.text()).toContain("入金但未更新总资产时可能显示亏损");
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
    period_days: 365,
  }));
  const w = mount(LedgerReturns, {
    props: { result, currency: "USD", points: [] },
  });
  expect(w.text()).toContain("manual-29");
  expect(w.text()).not.toContain("manual-30");
  const next = w.findAll("button").find((b) => b.text() === "下一页资金流")!;
  await next.trigger("click");
  expect(w.text()).toContain("manual-30");
  expect(w.text()).toContain("92233720368547758.07 USD");
  await w.setProps({ result: { ...result, revision: "updated" } });
  expect(w.text()).toContain("manual-29");
  expect(w.text()).not.toContain("manual-30");
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
