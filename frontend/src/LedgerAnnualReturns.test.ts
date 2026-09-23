// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import LedgerAnnualReturns from "./LedgerAnnualReturns.vue";
import {
  createLedgerWorkspace,
  ledgerWorkspaceKey,
} from "./useLedgerWorkspace";
import type { Account } from "./ledger";
import type { AnnualReturnRow, AnnualReturns } from "./ledgerAnnualReturns";
import { validateAnnualReturns, yearDescription } from "./ledgerAnnualReturns";
import type { ReturnMetric } from "./ledgerReturns";

const account: Account = {
  id: "sample",
  name: "合成账户",
  currency: "CNY",
  opening_date: "2024-08-01",
  opening_cash: null,
  current_holdings_input: "manual_snapshot",
  version: "1",
};
const rate = (
  value: string | null,
  status: ReturnMetric["status"] = "available",
  reason = "",
): ReturnMetric => ({
  value,
  percentage: value === null ? null : (Number(value) * 100).toFixed(2),
  status,
  reason,
});
const row = (year: number, from: string, to: string): AnnualReturnRow => ({
  year,
  from,
  to,
  money_weighted: rate("0.130000000000", "reference"),
  time_weighted: rate("0.090000000000"),
  benchmark: rate("0.080000000000"),
  benchmark_from: from,
  benchmark_to: to,
});
function annual(code: "H00300" | "H00922" = "H00300"): AnnualReturns {
  const lifetime = row(0, "2024-08-01", "2026-09-01");
  return {
    account_id: "sample",
    currency: "CNY",
    as_of: "2026-09-06",
    revision: "a".repeat(64),
    benchmark_code: code,
    benchmark_name: code === "H00300" ? "沪深300全收益" : "中证红利全收益",
    benchmark_currency: "CNY",
    benchmark_source: "中证指数",
    benchmark_error: "",
    annualized: {
      ...lifetime,
      money_weighted: rate("0.040000000000"),
      time_weighted: rate("0.030000000000"),
      benchmark: rate("0.020000000000"),
    },
    since: lifetime,
    years: [
      row(2024, "2024-08-01", "2024-12-31"),
      row(2025, "2024-12-31", "2025-12-31"),
      row(2026, "2025-12-31", "2026-09-01"),
    ],
  };
}

let wrapper: VueWrapper;
let calls: string[];
beforeEach(() => {
  calls = [];
  HTMLDialogElement.prototype.showModal = vi.fn();
  HTMLDialogElement.prototype.close = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      calls.push(path);
      const code = new URL(path, "http://localhost").searchParams.get(
        "benchmark",
      ) as "H00300" | "H00922";
      return new Response(JSON.stringify(annual(code)), {
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});
afterEach(() => {
  wrapper?.unmount();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function setup() {
  wrapper = mount(LedgerAnnualReturns, {
    props: { account, refreshKey: 0, view: "personal" },
    global: {
      provide: {
        [ledgerWorkspaceKey as symbol]: createLedgerWorkspace(() => {}),
      },
    },
  });
  await flushPromises();
}

it("shows recent years and lifetime, including carried returns without a reference badge", async () => {
  await setup();
  expect(calls).toEqual([
    "/api/platform/ledger/accounts/sample/annual-returns?benchmark=H00300",
  ]);
  const rows = wrapper.findAll(".lp-annual > .lp-annual-scroll tbody tr");
  expect(rows).toHaveLength(4);
  expect(rows.map((item) => item.find("th").text())).toEqual([
    "年化收益率",
    "2025年 全年",
    "2026年 年初 至 09-01",
    "记账以来",
  ]);
  expect(rows[1]!.text()).toContain("+13.00%");
  expect(wrapper.text()).not.toContain("仅供参考");
  await wrapper.get("button.lp-text-button").trigger("click");
  expect(wrapper.find("dialog").exists()).toBe(true);
  expect(wrapper.findAll(".lp-annual-full tbody tr")).toHaveLength(5);
  expect(wrapper.find(".lp-annual-full").text()).toContain(
    "2024年08-01 至 年末",
  );
  await wrapper.findAll("dialog select")[1]!.setValue("H00922");
  await flushPromises();
  expect(wrapper.find("dialog").exists()).toBe(true);
  expect(wrapper.find(".lp-annual-full").text()).toContain("中证红利全收益");
  await wrapper.get(".lp-close").trigger("click");
  expect(wrapper.find("dialog").exists()).toBe(false);
});

it("changes the benchmark and syncs the annual perspective with the overview", async () => {
  await setup();
  await wrapper.findAll("select")[1]!.setValue("H00922");
  await flushPromises();
  expect(calls.at(-1)).toBe(
    "/api/platform/ledger/accounts/sample/annual-returns?benchmark=H00922",
  );
  expect(wrapper.get(".lp-annual-table thead").text()).toContain(
    "中证红利全收益",
  );
  await wrapper.findAll("select")[0]!.setValue("manager");
  expect(wrapper.emitted("view")).toEqual([["manager"]]);
  await wrapper.setProps({ view: "manager", refreshKey: 1 });
  await flushPromises();
  expect(wrapper.get(".lp-annual-table thead").text()).toContain(
    "时间加权收益率",
  );
  expect(
    wrapper.findAll(".lp-annual > .lp-annual-scroll tbody tr")[1]!.text(),
  ).toContain("+9.00%");
  expect(calls).toHaveLength(3);
});

it("keeps account values when a benchmark is missing, and rejects mismatched responses", async () => {
  const data = annual();
  data.benchmark_error = "benchmark_unavailable";
  for (const row of [data.annualized, data.since, ...data.years]) {
    row.benchmark = rate(null, "unavailable", "benchmark_unavailable");
    row.benchmark_from = "";
    row.benchmark_to = "";
  }
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(data))),
  );
  await setup();
  expect(wrapper.get(".lp-annual-note[role='status']").text()).toContain(
    "账户收益仍可查看",
  );
  const cells = wrapper
    .findAll(".lp-annual > .lp-annual-scroll tbody tr")[1]!
    .findAll("td");
  expect(cells[0]!.text()).toBe("+13.00%");
  expect(cells[1]!.text()).toContain("—");
  expect(cells[1]!.text()).toContain("指数暂时不可用");
  expect(validateAnnualReturns(data, account, "H00300")).toBe(data);
  expect(() =>
    validateAnnualReturns(data, { ...account, id: "other" }, "H00300"),
  ).toThrow();
  expect(yearDescription({ ...data.years[0]!, from: "", to: "" })).toBe(
    "暂无有效区间",
  );
  expect(
    yearDescription({
      ...data.years[0]!,
      from: "2024-08-01",
      to: "2024-08-01",
    }),
  ).toBe("暂无有效区间");
});
